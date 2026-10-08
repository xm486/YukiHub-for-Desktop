package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"yukihub/internal/common/enums"
	"yukihub/internal/models"
	"yukihub/internal/version"
)

// NextMoe（未萌）目录 API 基址。使用用户级 OAuth 令牌（catalog:read）访问，
// 配额按用户计（100/min），限速与重试策略见 DefaultMetadataRateLimitPolicies。
const (
	nextMoeCatalogAPIBaseURL = "https://api.nextmoe.dev/v2"
	// 详情需要 include 块按需取；写错的 include 会被服务端 400 拒绝，不做降级。
	nextMoeDetailInclude = "titles,intros,covers,companies,tags,ratings,playtimes,screenshots"
)

var ErrNextMoeUnauthorized = errors.New("nextmoe unauthorized")

func IsNextMoeUnauthorizedError(err error) bool {
	return errors.Is(err, ErrNextMoeUnauthorized)
}

type NextMoeInfoGetter struct {
	client   *http.Client
	tagLimit int
}

func NewNextMoeInfoGetter(options ...GetterOption) *NextMoeInfoGetter {
	config := newGetterConfig(options)
	return &NextMoeInfoGetter{
		client:   config.client,
		tagLimit: config.tagLimit,
	}
}

var (
	_ Getter          = (*NextMoeInfoGetter)(nil)
	_ CandidateGetter = (*NextMoeInfoGetter)(nil)
)

type nextMoeLocalizedValue struct {
	Value string `json:"value"`
}

type nextMoeSearchHit struct {
	TargetObject         string                           `json:"target_object"`
	ID                   string                           `json:"id"`
	DisplayName          string                           `json:"display_name"`
	Latin                string                           `json:"latin"`
	Localized            map[string]nextMoeLocalizedValue `json:"localized"`
	ReleaseDate          string                           `json:"release_date"`
	ReleaseDatePrecision string                           `json:"release_date_precision"`
}

type nextMoeTitle struct {
	Lang      string `json:"lang"`
	Title     string `json:"title"`
	Latin     string `json:"latin"`
	IsMachine bool   `json:"is_machine"`
}

type nextMoeIntro struct {
	Lang  string `json:"lang"`
	Body  string `json:"body"`
	Text  string `json:"text"`
	Value string `json:"value"`
}

type nextMoeCover struct {
	URL    string `json:"url"`
	Sexual string `json:"sexual"`
}

type nextMoeCompany struct {
	DisplayName string `json:"display_name"`
}

type nextMoeTag struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Tag         *struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
	} `json:"tag"`
}

type nextMoeRating struct {
	Source string  `json:"source"`
	Score  float64 `json:"score"`
	Votes  int     `json:"vote_count"`
}

type nextMoeWork struct {
	Object      string                           `json:"object"`
	ID          string                           `json:"id"`
	DisplayName string                           `json:"display_name"`
	Latin       string                           `json:"latin"`
	Titles      []nextMoeTitle                   `json:"titles"`
	Localized   map[string]nextMoeLocalizedValue `json:"localized"`
	OLang       string                           `json:"olang"`
	Intros      []nextMoeIntro                   `json:"intros"`
	Cover       *nextMoeCover                    `json:"cover"`
	Covers      []nextMoeCover                   `json:"covers"`
	Companies   []nextMoeCompany                 `json:"companies"`
	Tags        []nextMoeTag                     `json:"tags"`
	Ratings     []nextMoeRating                  `json:"ratings"`
	// Screenshots 是详情接口返回的截图（include 块里的 screenshots）。
	// 对齐手机端 NextMoeClient 读的 screenshots[].url。
	Screenshots          []nextMoeCover `json:"screenshots"`
	ReleaseDate          string         `json:"release_date"`
	ReleaseDatePrecision string         `json:"release_date_precision"`
}

type nextMoeWorkListResponse struct {
	Items []nextMoeWork `json:"items"`
}

type nextMoeSearchResponse struct {
	Items []nextMoeSearchHit `json:"items"`
}

func (g NextMoeInfoGetter) FetchMetadata(id string, token string) (MetadataResult, error) {
	sourceID := strings.TrimSpace(id)
	if sourceID == "" {
		return MetadataResult{}, errors.New("nextmoe id is empty")
	}

	body, err := g.getJSON(
		"/catalog/works/"+url.PathEscape(sourceID),
		url.Values{"nsfw": {"true"}, "include": {nextMoeDetailInclude}},
		token,
	)
	if err != nil {
		return MetadataResult{}, err
	}

	var work nextMoeWork
	if err := json.Unmarshal(body, &work); err != nil {
		return MetadataResult{}, err
	}
	if object := strings.TrimSpace(work.Object); object != "" && object != "work" {
		return MetadataResult{}, errors.New("the provided ID does not correspond to a work")
	}
	if strings.TrimSpace(work.ID) == "" {
		work.ID = sourceID
	}
	return g.metadataResultFromWork(work), nil
}

func (g NextMoeInfoGetter) FetchMetadataByName(name string, token string) (MetadataResult, error) {
	results, err := g.FetchMetadataCandidatesByName(name, token)
	if err != nil {
		return MetadataResult{}, err
	}
	return results[0], nil
}

func (g NextMoeInfoGetter) FetchMetadataCandidatesByName(name string, token string) ([]MetadataResult, error) {
	query := strings.TrimSpace(name)
	if query == "" {
		return nil, errors.New("nextmoe search keyword is empty")
	}

	body, err := g.getJSON("/catalog/search", url.Values{
		"object": {"work"},
		"q":      {query},
		"nsfw":   {"true"},
		"limit":  {strconv.Itoa(metadataSearchCandidateLimit)},
	}, token)
	if err != nil {
		return nil, err
	}

	var searchResp nextMoeSearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, err
	}

	// 只处理 work 命中，其余家族（character / company 等）按开放词表容忍跳过。
	hits := make([]nextMoeSearchHit, 0, metadataSearchCandidateLimit)
	candidateNames := make([][]string, 0, metadataSearchCandidateLimit)
	for _, hit := range searchResp.Items {
		if len(hits) >= metadataSearchCandidateLimit {
			break
		}
		target := strings.TrimSpace(hit.TargetObject)
		if target != "" && target != "work" {
			continue
		}
		if strings.TrimSpace(hit.ID) == "" {
			continue
		}
		hits = append(hits, hit)
		candidateNames = append(candidateNames, []string{hit.DisplayName, hit.Localized["zh-Hans"].Value, hit.Latin})
	}
	if len(hits) == 0 {
		return nil, errors.New("no results found")
	}

	indexes := exactMetadataCandidateIndexes(query, candidateNames)
	if len(indexes) == 0 {
		indexes = []int{0}
	}

	// 搜索命中行不带封面，补水一次（失败静默忽略，只影响观感）。
	covers := g.hydrateCovers(hits, token)

	results := make([]MetadataResult, 0, len(indexes))
	for _, index := range indexes {
		hit := hits[index]
		result := MetadataResult{
			Game: models.Game{
				Name:        firstNonEmpty(strings.TrimSpace(hit.Localized["zh-Hans"].Value), strings.TrimSpace(hit.DisplayName)),
				Aliases:     normalizeMetadataAliases(hit.DisplayName, []string{hit.Latin}, []string{hit.Localized["zh-Hans"].Value}),
				ReleaseDate: trimNextMoeDateByPrecision(hit.ReleaseDate, hit.ReleaseDatePrecision),
				SourceType:  enums.NextMoe,
				SourceID:    strings.TrimSpace(hit.ID),
				CachedAt:    time.Now(),
			},
		}
		if cover, ok := covers[strings.TrimSpace(hit.ID)]; ok {
			result.Game.CoverURL = cover.URL
			result.Game.CoverSourceURL = cover.URL
			result.Game.IsNSFW = isNextMoeExplicitCover(cover.Sexual)
		}
		results = append(results, result)
	}
	return results, nil
}

// hydrateCovers 用 ids= 批量拉一次封面；任何失败都静默忽略。
func (g NextMoeInfoGetter) hydrateCovers(hits []nextMoeSearchHit, token string) map[string]nextMoeCover {
	covers := make(map[string]nextMoeCover, len(hits))
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, strings.TrimSpace(hit.ID))
	}

	body, err := g.getJSON("/catalog/works", url.Values{
		"ids":     {strings.Join(ids, ",")},
		"nsfw":    {"true"},
		"include": {"covers"},
		"limit":   {"100"},
	}, token)
	if err != nil {
		return covers
	}

	var listResp nextMoeWorkListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return covers
	}
	for _, work := range listResp.Items {
		id := strings.TrimSpace(work.ID)
		if id == "" {
			continue
		}
		if cover, ok := pickNextMoeCover(work.Cover, work.Covers); ok {
			covers[id] = cover
		}
	}
	return covers
}

func (g NextMoeInfoGetter) metadataResultFromWork(work nextMoeWork) MetadataResult {
	title := resolveNextMoeTitle(work)
	name := firstNonEmpty(title.chinese, title.original, strings.TrimSpace(work.DisplayName))
	cover, _ := pickNextMoeCover(work.Cover, work.Covers)

	result := MetadataResult{
		Game: models.Game{
			Name:           name,
			Aliases:        normalizeMetadataAliases(name, []string{work.DisplayName, title.original, title.roman, title.chinese}),
			CoverURL:       cover.URL,
			CoverSourceURL: cover.URL,
			Company:        resolveNextMoeCompany(work.Companies),
			Summary:        resolveNextMoeSummary(work),
			Rating:         resolveNextMoeRating(work.Ratings),
			ReleaseDate:    trimNextMoeDateByPrecision(work.ReleaseDate, work.ReleaseDatePrecision),
			IsNSFW:         isNextMoeExplicitCover(cover.Sexual),
			SourceType:     enums.NextMoe,
			SourceID:       strings.TrimSpace(work.ID),
			CachedAt:       time.Now(),
		},
		Tags: g.extractNextMoeTags(work.Tags),
	}
	result.Screenshots = nextMoeScreenshotURLs(work.Screenshots)
	return result
}

// nextMoeScreenshotURLs 取 screenshots[] 的地址，去重并截断到来源上限。
func nextMoeScreenshotURLs(shots []nextMoeCover) []string {
	if len(shots) == 0 {
		return nil
	}
	urls := make([]string, 0, len(shots))
	for _, shot := range shots {
		urls = append(urls, strings.TrimSpace(shot.URL))
	}
	return normalizeMetadataScreenshots(urls)
}

type nextMoeResolvedTitle struct {
	chinese  string
	original string
	roman    string
}

// resolveNextMoeTitle 取官方标题：localized 裁定值 > titles[] 里的 zh-Hans（机翻排后）> display_name / ja。
func resolveNextMoeTitle(work nextMoeWork) nextMoeResolvedTitle {
	resolved := nextMoeResolvedTitle{
		original: strings.TrimSpace(work.DisplayName),
		roman:    strings.TrimSpace(work.Latin),
	}

	machineChinese := ""
	for _, title := range work.Titles {
		value := strings.TrimSpace(title.Title)
		if value == "" {
			continue
		}
		switch strings.TrimSpace(title.Lang) {
		case "zh-Hans":
			if !title.IsMachine && resolved.chinese == "" {
				resolved.chinese = value
			} else if title.IsMachine && machineChinese == "" {
				machineChinese = value
			}
		case "ja":
			if resolved.original == "" {
				resolved.original = value
			}
		}
		if resolved.roman == "" {
			resolved.roman = strings.TrimSpace(title.Latin)
		}
	}

	if localized := strings.TrimSpace(work.Localized["zh-Hans"].Value); localized != "" {
		resolved.chinese = localized
	}
	if resolved.chinese == "" {
		resolved.chinese = machineChinese
	}
	return resolved
}

func resolveNextMoeCompany(companies []nextMoeCompany) string {
	for _, company := range companies {
		if name := strings.TrimSpace(company.DisplayName); name != "" {
			return name
		}
	}
	return ""
}

// resolveNextMoeSummary 优先中文简介，其次原文简介。
func resolveNextMoeSummary(work nextMoeWork) string {
	original := ""
	for _, intro := range work.Intros {
		text := firstNonEmpty(intro.Body, intro.Text, intro.Value)
		if text == "" {
			continue
		}
		lang := strings.TrimSpace(intro.Lang)
		if lang == "zh-Hans" || lang == "zh" {
			return text
		}
		if original == "" && (lang == strings.TrimSpace(work.OLang) || lang == "ja") {
			original = text
		}
	}
	return original
}

// resolveNextMoeRating 分源并列、刻度原生，优先 VNDB → Bangumi → ErogameScape。
func resolveNextMoeRating(ratings []nextMoeRating) float64 {
	scores := make(map[string]float64, len(ratings))
	for _, rating := range ratings {
		if rating.Score <= 0 {
			continue
		}
		source := strings.TrimSpace(rating.Source)
		if _, exists := scores[source]; exists {
			continue
		}
		scores[source] = rating.Score
	}
	for _, source := range []string{"vndb", "bangumi", "erogamescape"} {
		if score, ok := scores[source]; ok {
			return normalizeTenPointRating(score)
		}
	}
	return 0
}

func (g NextMoeInfoGetter) extractNextMoeTags(tags []nextMoeTag) []TagItem {
	if g.tagLimit == 0 {
		return nil
	}

	result := make([]TagItem, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		name := nextMoeTagName(tag)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, TagItem{Name: name, Source: string(enums.NextMoe), Weight: 1})
		if hasReachedTagLimit(len(result), g.tagLimit) {
			break
		}
	}
	return result
}

func nextMoeTagName(tag nextMoeTag) string {
	name := firstNonEmpty(tag.Name, tag.DisplayName)
	if name != "" {
		return name
	}
	if tag.Tag != nil {
		return firstNonEmpty(tag.Tag.Name, tag.Tag.DisplayName)
	}
	return ""
}

func pickNextMoeCover(base *nextMoeCover, covers []nextMoeCover) (nextMoeCover, bool) {
	if base != nil && strings.TrimSpace(base.URL) != "" {
		return *base, true
	}
	for _, cover := range covers {
		if strings.TrimSpace(cover.URL) != "" {
			return cover, true
		}
	}
	return nextMoeCover{}, false
}

// isNextMoeExplicitCover 把封面分级（封闭词表 safe / suggestive / explicit）映射为 NSFW 标记。
func isNextMoeExplicitCover(sexual string) bool {
	return strings.TrimSpace(sexual) == "explicit"
}

// trimNextMoeDateByPrecision 按发售日精度裁剪：month → yyyy-MM，year → yyyy，未知精度原样返回。
func trimNextMoeDateByPrecision(date string, precision string) string {
	value := strings.TrimSpace(date)
	if value == "" {
		return ""
	}
	switch strings.TrimSpace(precision) {
	case "month":
		if len(value) >= 7 {
			return value[:7]
		}
	case "year":
		if len(value) >= 4 {
			return value[:4]
		}
	}
	return value
}

func (g NextMoeInfoGetter) getJSON(path string, params url.Values, token string) ([]byte, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("nextmoe API requires Bearer token")
	}

	reqURL := nextMoeCatalogAPIBaseURL + path
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := doLimitedMetadataRequest(g.client, req, enums.NextMoe)
	if err != nil {
		return nil, err
	}
	defer closeResponseBody(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w: %s", ErrNextMoeUnauthorized, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("nextmoe API returned status: %d, body: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}
