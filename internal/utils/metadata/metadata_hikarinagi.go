package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"yukihub/internal/common/enums"
	"yukihub/internal/models"
	"yukihub/internal/version"
)

const (
	hikarinagiAPIBaseURL = "https://api.hikarinagi.org/v3"
	hikarinagiTokenURL   = "https://id.hikarinagi.org/oidc/token"

	// 元数据走**应用级凭据**（OAuth 2.0 Client Credentials，Basic 认证换令牌，1 小时有效，
	// 限速 60 次/分钟/应用）。它与「用户登录」用的是**两个不同的 OAuth 应用**：
	//
	//   登录   hkn_qtmXMJfBoxcNLA-a   scope: openid user:read（public client，无 secret）
	//   元数据 hkn_4poXX7v37j_iM2-o   scope: catalog:read（client_credentials，有 secret）
	//
	// **不能混用**：拿登录 client 去换应用令牌会因为没有 catalog:read 被拒；
	// 拿元数据 client 去做用户登录也没有 user:read。
	// 上游 LunaBox 把两者统一成一套注入凭据，所以它的 scope 写的是 catalog:full，
	// 且必须构建时注入——照抄到 YukiHub 上两边都跑不通。
	//
	// 下面的默认值与 Android 端 `metadata/HikarinagiClient.java` 同源（YukiHub 自己申请的应用）。
	// 需要更换时用 YUKIHUB_HIKARINAGI_METADATA_CLIENT_ID / _SECRET 注入覆盖。
	hikarinagiMetadataDefaultClientID     = "hkn_4poXX7v37j_iM2-o"
	hikarinagiMetadataDefaultClientSecret = "hks_Wv6tW5O6ev8Mbifvg1tPJ7UexehLATQcKpZJiFhV48Y"
	hikarinagiScope                       = "catalog:read"
)

// hikarinagiMetadataClientCredentials 返回元数据 API 的应用凭据。
// 构建注入优先，其次用内置默认值。
func hikarinagiMetadataClientCredentials() (string, string) {
	clientID := strings.TrimSpace(version.HikarinagiMetadataClientID)
	if clientID == "" {
		clientID = hikarinagiMetadataDefaultClientID
	}
	clientSecret := strings.TrimSpace(version.HikarinagiMetadataClientSecret)
	if clientSecret == "" {
		clientSecret = hikarinagiMetadataDefaultClientSecret
	}
	return clientID, clientSecret
}

// ErrHikarinagiUnauthorized 表示应用级令牌被拒（401/403），
// doAuthorizedGet 依赖它决定是否强制刷新令牌后重试一次。
var ErrHikarinagiUnauthorized = errors.New("hikarinagi unauthorized")

type HikarinagiInfoGetter struct {
	client   *http.Client
	tagLimit int
}

func NewHikarinagiInfoGetter(options ...GetterOption) *HikarinagiInfoGetter {
	config := newGetterConfig(options)
	return &HikarinagiInfoGetter{
		client:   config.client,
		tagLimit: config.tagLimit,
	}
}

var _ Getter = (*HikarinagiInfoGetter)(nil)

var hikarinagiTokenCache struct {
	clientID     string
	clientSecret string
	token        string
	expiresAt    time.Time
	mu           sync.Mutex
}

type hikarinagiTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

type hikarinagiEnvelope[T any] struct {
	Success   bool            `json:"success"`
	Data      T               `json:"data"`
	Message   string          `json:"message"`
	Error     json.RawMessage `json:"error"`
	RequestID string          `json:"request_id"`
}

type hikarinagiCover struct {
	URL      string `json:"url"`
	Width    *int   `json:"width"`
	Height   *int   `json:"height"`
	Sexual   int    `json:"sexual"`
	Violence int    `json:"violence"`
	Votes    int    `json:"votes"`
}

type hikarinagiTag struct {
	Name  string `json:"name"`
	Likes int    `json:"likes"`
}

type hikarinagiGame struct {
	ID          int64             `json:"id"`
	OriginTitle string            `json:"origin_title"`
	TransTitle  *string           `json:"trans_title"`
	Aliases     []string          `json:"aliases"`
	Covers      []hikarinagiCover `json:"covers"`
	// Images 是详情接口返回的游戏截图（对齐手机端 HikarinagiClient 读的 images[]）。
	Images      []hikarinagiCover `json:"images"`
	ReleaseDate *string           `json:"release_date"`
	OriginIntro *string           `json:"origin_intro"`
	TransIntro  *string           `json:"trans_intro"`
	NSFW        bool              `json:"nsfw"`
	Tags        []hikarinagiTag   `json:"tags"`
	Rating      hikarinagiRating  `json:"rating"`
	Developer   *string           `json:"developer"`
}

type hikarinagiRating struct {
	Score *float64 `json:"score"`
}

type hikarinagiSearchHit struct {
	Type      string           `json:"type"`
	ID        int64            `json:"id"`
	Title     string           `json:"title"`
	Subtitle  *string          `json:"subtitle"`
	Developer *string          `json:"developer"`
	Cover     *hikarinagiCover `json:"cover"`
}

type hikarinagiSearchData struct {
	Items []hikarinagiSearchHit `json:"items"`
}

func NormalizeHikarinagiID(id string) (string, bool) {
	normalized := strings.TrimSpace(id)
	parsed, err := strconv.ParseInt(normalized, 10, 64)
	if err != nil || parsed <= 0 {
		return "", false
	}
	return strconv.FormatInt(parsed, 10), true
}

func (h HikarinagiInfoGetter) FetchMetadata(id string, accessToken string) (MetadataResult, error) {
	normalizedID, ok := NormalizeHikarinagiID(id)
	if !ok {
		return MetadataResult{}, fmt.Errorf("invalid Hikarinagi ID format: %s", id)
	}

	bodyBytes, err := h.doAuthorizedGet(
		fmt.Sprintf("%s/galgames/%s", hikarinagiAPIBaseURL, url.PathEscape(normalizedID)),
		accessToken,
	)
	if err != nil {
		return MetadataResult{}, err
	}

	var envelope hikarinagiEnvelope[hikarinagiGame]
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return MetadataResult{}, fmt.Errorf("decode Hikarinagi detail response: %w", err)
	}
	if !envelope.Success {
		return MetadataResult{}, hikarinagiEnvelopeError("Hikarinagi detail API", envelope.Message, envelope.Error, envelope.RequestID)
	}
	if envelope.Data.ID <= 0 {
		return MetadataResult{}, errors.New("Hikarinagi API returned no game data")
	}

	return h.convertToMetadataResult(envelope.Data), nil
}

func (h HikarinagiInfoGetter) FetchMetadataByName(name string, accessToken string) (MetadataResult, error) {
	results, err := h.FetchMetadataCandidatesByName(name, accessToken)
	if err != nil {
		return MetadataResult{}, err
	}
	return results[0], nil
}

func (h HikarinagiInfoGetter) FetchMetadataCandidatesByName(name string, accessToken string) ([]MetadataResult, error) {
	keyword := strings.TrimSpace(name)
	if keyword == "" {
		return nil, errors.New("Hikarinagi search keyword is empty")
	}

	params := url.Values{}
	params.Set("q", keyword)
	params.Add("types", "galgame")
	params.Set("page", "1")
	params.Set("page_size", strconv.Itoa(metadataSearchCandidateLimit))
	bodyBytes, err := h.doAuthorizedGet(fmt.Sprintf("%s/search?%s", hikarinagiAPIBaseURL, params.Encode()), accessToken)
	if err != nil {
		return nil, err
	}

	var envelope hikarinagiEnvelope[hikarinagiSearchData]
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return nil, fmt.Errorf("decode Hikarinagi search response: %w", err)
	}
	if !envelope.Success {
		return nil, hikarinagiEnvelopeError("Hikarinagi search API", envelope.Message, envelope.Error, envelope.RequestID)
	}
	if len(envelope.Data.Items) == 0 {
		return nil, errors.New("no results found")
	}

	hits := make([]hikarinagiSearchHit, 0, metadataSearchCandidateLimit)
	candidateNames := make([][]string, 0, metadataSearchCandidateLimit)
	for _, hit := range envelope.Data.Items {
		if len(hits) >= metadataSearchCandidateLimit {
			break
		}
		if hit.Type != "galgame" || hit.ID <= 0 {
			continue
		}
		names := []string{hit.Title}
		if hit.Subtitle != nil {
			names = append(names, *hit.Subtitle)
		}
		hits = append(hits, hit)
		candidateNames = append(candidateNames, names)
	}
	if len(hits) == 0 {
		return nil, errors.New("no results found")
	}
	indexes := exactMetadataCandidateIndexes(keyword, candidateNames)
	if len(indexes) == 0 {
		indexes = []int{0}
	}

	results := make([]MetadataResult, 0, len(indexes))
	seenIDs := make(map[int64]struct{}, len(indexes))
	var lastErr error
	for _, index := range indexes {
		hit := hits[index]
		if _, exists := seenIDs[hit.ID]; exists {
			continue
		}
		result, fetchErr := h.FetchMetadata(strconv.FormatInt(hit.ID, 10), accessToken)
		if fetchErr != nil {
			lastErr = fetchErr
			continue
		}
		if result.Game.Company == "" && hit.Developer != nil {
			result.Game.Company = strings.TrimSpace(*hit.Developer)
		}
		if result.Game.CoverURL == "" && hit.Cover != nil {
			result.Game.CoverURL = strings.TrimSpace(hit.Cover.URL)
		}
		seenIDs[hit.ID] = struct{}{}
		results = append(results, result)
	}
	if len(results) > 0 {
		return results, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("no results found")
}

func (h HikarinagiInfoGetter) getAccessToken() (string, error) {
	clientID, clientSecret := hikarinagiMetadataClientCredentials()
	if clientID == "" || clientSecret == "" {
		return "", errors.New("Hikarinagi 元数据 API 缺少应用凭据")
	}

	hikarinagiTokenCache.mu.Lock()
	defer hikarinagiTokenCache.mu.Unlock()

	now := time.Now().UTC()
	if hikarinagiTokenCache.clientID == clientID &&
		hikarinagiTokenCache.clientSecret == clientSecret &&
		hikarinagiTokenCache.token != "" &&
		now.Before(hikarinagiTokenCache.expiresAt) {
		return hikarinagiTokenCache.token, nil
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("scope", hikarinagiScope)
	req, err := http.NewRequest(http.MethodPost, hikarinagiTokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create Hikarinagi token request: %w", err)
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request Hikarinagi access token: %w", err)
	}
	defer closeResponseBody(resp.Body)
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read Hikarinagi token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Hikarinagi token API returned status: %d, body: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var tokenResponse hikarinagiTokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResponse); err != nil {
		return "", fmt.Errorf("decode Hikarinagi token response: %w", err)
	}
	token := strings.TrimSpace(tokenResponse.AccessToken)
	if token == "" {
		return "", errors.New("Hikarinagi token API returned an empty access token")
	}
	expiresIn := time.Duration(tokenResponse.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}
	refreshBefore := time.Minute
	if expiresIn <= refreshBefore {
		refreshBefore = 0
	}

	hikarinagiTokenCache.clientID = clientID
	hikarinagiTokenCache.clientSecret = clientSecret
	hikarinagiTokenCache.token = token
	hikarinagiTokenCache.expiresAt = now.Add(expiresIn - refreshBefore)
	return token, nil
}

func (h HikarinagiInfoGetter) invalidateAccessToken() {
	hikarinagiTokenCache.mu.Lock()
	defer hikarinagiTokenCache.mu.Unlock()
	hikarinagiTokenCache.token = ""
	hikarinagiTokenCache.expiresAt = time.Time{}
}

func (h HikarinagiInfoGetter) doAuthorizedGet(reqURL, providedToken string) ([]byte, error) {
	providedToken = strings.TrimSpace(providedToken)
	if providedToken != "" {
		return h.doGetWithToken(reqURL, providedToken)
	}

	for attempt := 0; attempt < 2; attempt++ {
		accessToken, err := h.getAccessToken()
		if err != nil {
			return nil, fmt.Errorf("get Hikarinagi access token: %w", err)
		}

		bodyBytes, err := h.doGetWithToken(reqURL, accessToken)
		if errors.Is(err, ErrHikarinagiUnauthorized) && attempt == 0 {
			h.invalidateAccessToken()
			continue
		}
		if err != nil {
			return nil, err
		}
		return bodyBytes, nil
	}

	return nil, errors.New("Hikarinagi API authorization failed after token refresh")
}

func (h HikarinagiInfoGetter) doGetWithToken(reqURL, accessToken string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create Hikarinagi API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())

	statusCode, _, bodyBytes, err := doLimitedMetadataRequestBody(h.client, req, enums.Hikarinagi)
	if err != nil {
		return nil, err
	}
	if statusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w: %s", ErrHikarinagiUnauthorized, strings.TrimSpace(string(bodyBytes)))
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("Hikarinagi API returned status: %d, body: %s", statusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return bodyBytes, nil
}

func (h HikarinagiInfoGetter) convertToMetadataResult(data hikarinagiGame) MetadataResult {
	name := strings.TrimSpace(data.OriginTitle)
	if data.TransTitle != nil && strings.TrimSpace(*data.TransTitle) != "" {
		name = strings.TrimSpace(*data.TransTitle)
	}
	titleVariants := []string{data.OriginTitle}
	if data.TransTitle != nil {
		titleVariants = append(titleVariants, *data.TransTitle)
	}
	summary := ""
	if data.TransIntro != nil && strings.TrimSpace(*data.TransIntro) != "" {
		summary = strings.TrimSpace(*data.TransIntro)
	} else if data.OriginIntro != nil {
		summary = strings.TrimSpace(*data.OriginIntro)
	}
	releaseDate := ""
	if data.ReleaseDate != nil {
		releaseDate = normalizeHikarinagiDate(*data.ReleaseDate)
	}

	coverURL := bestHikarinagiCoverURL(data.Covers)
	rating := 0.0
	if data.Rating.Score != nil {
		rating = normalizeTenPointRating(*data.Rating.Score)
	}
	company := ""
	if data.Developer != nil {
		company = strings.TrimSpace(*data.Developer)
	}
	game := models.Game{
		Name:           name,
		Company:        company,
		Aliases:        normalizeMetadataAliases(name, titleVariants, data.Aliases),
		CoverURL:       coverURL,
		CoverSourceURL: coverURL,
		Summary:        summary,
		ReleaseDate:    releaseDate,
		Rating:         rating,
		IsNSFW:         data.NSFW,
		SourceType:     enums.Hikarinagi,
		SourceID:       strconv.FormatInt(data.ID, 10),
		CachedAt:       time.Now(),
	}
	return MetadataResult{
		Game:        game,
		Tags:        extractHikarinagiTags(data.Tags, h.tagLimit),
		Screenshots: hikarinagiScreenshotURLs(data.Images),
	}
}

// hikarinagiScreenshotURLs 取详情接口 images[] 的地址，去重并截断到来源上限。
func hikarinagiScreenshotURLs(images []hikarinagiCover) []string {
	if len(images) == 0 {
		return nil
	}
	urls := make([]string, 0, len(images))
	for _, image := range images {
		urls = append(urls, strings.TrimSpace(image.URL))
	}
	return normalizeMetadataScreenshots(urls)
}

func bestHikarinagiCoverURL(covers []hikarinagiCover) string {
	bestURL := ""
	bestVotes := -1
	for _, cover := range covers {
		coverURL := strings.TrimSpace(cover.URL)
		if coverURL == "" || cover.Votes < bestVotes {
			continue
		}
		bestURL = coverURL
		bestVotes = cover.Votes
	}
	return bestURL
}

func normalizeHikarinagiDate(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.Format(time.DateOnly)
	}
	return value
}

func extractHikarinagiTags(tags []hikarinagiTag, limit int) []TagItem {
	if limit == 0 {
		return nil
	}
	filtered := make([]hikarinagiTag, 0, len(tags))
	for _, tag := range tags {
		tag.Name = strings.TrimSpace(tag.Name)
		if tag.Name != "" {
			filtered = append(filtered, tag)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Likes > filtered[j].Likes
	})
	filtered = filtered[:tagItemsCapacity(len(filtered), limit)]
	maxLikes := filtered[0].Likes
	result := make([]TagItem, 0, len(filtered))
	for _, tag := range filtered {
		weight := 1.0
		if maxLikes > 0 {
			weight = float64(tag.Likes) / float64(maxLikes)
		}
		result = append(result, TagItem{
			Name:      tag.Name,
			Source:    string(enums.Hikarinagi),
			Weight:    weight,
			IsSpoiler: false,
		})
	}
	return result
}

func hikarinagiEnvelopeError(prefix string, message string, rawError json.RawMessage, requestID string) error {
	detail := strings.TrimSpace(message)
	if detail == "" && len(rawError) > 0 && string(rawError) != "null" {
		detail = strings.TrimSpace(string(rawError))
	}
	if detail == "" {
		detail = "unknown error"
	}
	if requestID != "" {
		return fmt.Errorf("%s error: %s (request_id: %s)", prefix, detail, requestID)
	}
	return fmt.Errorf("%s error: %s", prefix, detail)
}
