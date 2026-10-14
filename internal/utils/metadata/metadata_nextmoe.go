package metadata

import (
	"context"
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

// NextMoe（未萌）目录 API 基址。
//
// 身份与配额（对齐手机版 NextMoeClient）：
//   - 匿名（不带 Authorization）：配额按**出口 IP** 计，50/min。目录只读面对
//     匿名与登录身份返回**逐字节相同**的结果，所以未登录一样能用本源。
//   - 登录（带用户级 OAuth 令牌，catalog:read）：配额按**用户**计，100/min、
//     10,000/UTC 日，不受同 IP 他人挤占。
//
// 客户端节流间隔（700ms 登录 / 1300ms 匿名）与重试退避见 DefaultMetadataRateLimitPolicies。
// 这套限速是「不给对方服务器添压力」的第一道闸，任何批量路径都必须先过它再发请求。
const (
	nextMoeCatalogAPIBaseURL = "https://api.nextmoe.dev/v2"
	// 详情需要 include 块按需取；写错的 include 会被服务端 400 拒绝，不做降级。
	nextMoeDetailInclude = "titles,intros,covers,companies,tags,ratings,playtimes,screenshots"

	// 重试次数与退避起点对齐手机版 NextMoeClient.MAX_RETRIES / RETRY_DELAY_MS。
	nextMoeMaxRetries = 2
	nextMoeRetryDelay = 1200 * time.Millisecond
	// 分钟窗打满时按 Retry-After 等，上限 60s（手机版 Math.min(60, Retry-After)）。
	nextMoeMaxRetryAfter = 60 * time.Second
	// 错误体是 RFC 9457 problem+json，很小；读取上限只为防御异常响应。
	nextMoeMaxErrorBodyBytes = 64 * 1024
	// 合并重定向最多跟随一次，防止服务端连环改 id 把请求打成死循环。
	nextMoeMaxMergedFollows = 1
)

var (
	ErrNextMoeUnauthorized = errors.New("nextmoe unauthorized")
	// ErrNextMoeQuotaExceeded 表示 UTC 日配额已耗尽。
	// 手机版对此明确**不退避重试**（等下去没有意义），错误文案要能直接给用户看。
	ErrNextMoeQuotaExceeded = errors.New("nextmoe daily quota exceeded")
)

func IsNextMoeUnauthorizedError(err error) bool {
	return errors.Is(err, ErrNextMoeUnauthorized)
}

func IsNextMoeQuotaExceededError(err error) bool {
	return errors.Is(err, ErrNextMoeQuotaExceeded)
}

type NextMoeInfoGetter struct {
	client   *http.Client
	tagLimit int
	limiter  *metadataRateLimiter
	baseURL  string
}

func NewNextMoeInfoGetter(options ...GetterOption) *NextMoeInfoGetter {
	config := newGetterConfig(options)
	return &NextMoeInfoGetter{
		client:   config.client,
		tagLimit: config.tagLimit,
		limiter:  config.rateLimiter,
		baseURL:  config.nextMoeBaseURL,
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
	trimmedToken := strings.TrimSpace(token)

	var lastErr error
	mergeFollows := 0
	for attempt := 0; attempt <= nextMoeMaxRetries; attempt++ {
		result := g.getJSONOnce(g.buildCatalogURL(path, params), trimmedToken)
		if result.MergedID != "" {
			// 目录把该 id 合并到了新 id。按 current_id 换新地址再打一次，
			// 这条**不计入重试次数**（手机版同样处理）—— 让它吃掉一次真正的重试机会，
			// 在限流场景下会白白少一次退避重试。
			mergedPath := nextMoeMergedWorksPath(path, result.MergedID)
			if mergedPath != "" && mergeFollows < nextMoeMaxMergedFollows {
				mergeFollows++
				path = mergedPath
				attempt--
				continue
			}
			result.Err = fmt.Errorf("nextmoe work 已被合并到 %s，但无法自动跟随（来源路径 %s）", result.MergedID, path)
		}
		if result.Err == nil {
			return result.Body, nil
		}

		lastErr = result.Err
		// 401（令牌失效）与日配额耗尽都不适合在这里重试：
		// 前者由 NextMoeService 刷新令牌后重放，后者等下去也不会恢复。
		if errors.Is(result.Err, ErrNextMoeUnauthorized) || errors.Is(result.Err, ErrNextMoeQuotaExceeded) {
			return nil, result.Err
		}
		if !result.Retryable || attempt >= nextMoeMaxRetries {
			return nil, result.Err
		}

		// 退避 = 服务端要求的 Retry-After（若有）**加上**这一轮的指数退避。
		//
		// 手机版就是叠加的：429 分支先 `sleep(Retry-After + 250ms)`，抛出后再由
		// 外层 catch 睡 `RETRY_DELAY_MS * (attempt+1)`。不要写成「取两者较大值」——
		// 那样在服务端给了 Retry-After 时会**丢掉**指数退避，比手机版更激进，
		// 对「不给对方服务器添压力」不利。
		delay := nextMoeRetryDelay * time.Duration(attempt+1)
		if result.RetryAfter > 0 {
			delay += result.RetryAfter
		}
		if err := g.rateLimiter().wait(context.Background(), delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// rateLimiter 兜底成进程级共享限速器，避免零值 getter 直接 panic。
func (g NextMoeInfoGetter) rateLimiter() *metadataRateLimiter {
	if g.limiter != nil {
		return g.limiter
	}
	return sharedMetadataRateLimiter
}

func (g NextMoeInfoGetter) buildCatalogURL(path string, params url.Values) string {
	baseURL := g.baseURL
	if baseURL == "" {
		baseURL = nextMoeCatalogAPIBaseURL
	}
	reqURL := baseURL + path
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}
	return reqURL
}

// nextMoeMergedWorksPath 只在「详情路径」上跟随合并重定向；其它路径返回空串表示跟不了。
func nextMoeMergedWorksPath(path string, currentID string) string {
	if currentID == "" || !strings.Contains(path, "/catalog/works/") {
		return ""
	}
	return "/catalog/works/" + url.PathEscape(currentID)
}

// nextMoeHTTPResult 是一次请求的分类结果，供 getJSON 决定「重试 / 换地址 / 直接上报」。
type nextMoeHTTPResult struct {
	Body       []byte
	Retryable  bool
	RetryAfter time.Duration
	// MergedID 非空表示 404 ENTITY_MERGED，这是目录给出的新 id。
	MergedID string
	Err      error
}

func (g NextMoeInfoGetter) getJSONOnce(reqURL string, token string) nextMoeHTTPResult {
	// 登录可选：有令牌就带上（按用户计配额），没有就走匿名（按来源 IP 计配额）。
	// 注意这**不是**错误路径 —— 匿名是正常可用档位，只是额度低一些。
	anonymous := token == ""
	if err := g.rateLimiter().AcquireForTier(context.Background(), enums.NextMoe, anonymous); err != nil {
		return nextMoeHTTPResult{Err: fmt.Errorf("nextmoe rate limit wait failed: %w", err)}
	}

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nextMoeHTTPResult{Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	// 未登录时不带 Authorization —— 服务端按来源 IP 计匿名配额。
	if !anonymous {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		// 传输层失败（超时 / 连接被断）值得按同一套退避再试。
		return nextMoeHTTPResult{Retryable: true, Err: err}
	}
	defer closeResponseBody(resp.Body)

	body, err := io.ReadAll(io.LimitReader(resp.Body, nextMoeMaxErrorBodyBytes))
	if err != nil {
		return nextMoeHTTPResult{Retryable: true, Err: err}
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nextMoeHTTPResult{Body: body}
	case resp.StatusCode == http.StatusUnauthorized:
		// 令牌无效/过期。这里不自行刷新：刷新与重放统一由 NextMoeService 负责，
		// 免得元数据层和账号层各刷一次，把 refresh token 轮换打乱。
		return nextMoeHTTPResult{Err: fmt.Errorf("%w: %s", ErrNextMoeUnauthorized, nextMoeProblemSummary(body))}
	case resp.StatusCode == http.StatusTooManyRequests:
		if code := nextMoeProblemCode(body); code == "QUOTA_EXCEEDED" {
			return nextMoeHTTPResult{
				Err: fmt.Errorf("%w：今日抓取配额已用完（UTC 零点归零）%s", ErrNextMoeQuotaExceeded, nextMoeAnonymousQuotaHint(anonymous)),
			}
		}
		return nextMoeHTTPResult{
			Retryable:  true,
			RetryAfter: nextMoeRetryAfter(resp.Header.Get("Retry-After")),
			Err:        fmt.Errorf("nextmoe API rate limited: status 429%s", nextMoeAnonymousQuotaHint(anonymous)),
		}
	case resp.StatusCode == http.StatusNotFound && nextMoeProblemCode(body) == "ENTITY_MERGED":
		return nextMoeHTTPResult{MergedID: nextMoeMergedCurrentID(body)}
	case resp.StatusCode >= 500:
		return nextMoeHTTPResult{
			Retryable: true,
			Err:       fmt.Errorf("nextmoe API returned status: %d, body: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	default:
		return nextMoeHTTPResult{
			Err: fmt.Errorf("nextmoe API returned status: %d, body: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}
}

// nextMoeAnonymousQuotaHint 只在匿名档受限时追加「怎么提额」的提示。
// 登录档受限就是账号自己的额度用完了，不该再劝他去登录。
func nextMoeAnonymousQuotaHint(anonymous bool) string {
	if !anonymous {
		return ""
	}
	return "（当前为匿名配额，按网络出口 IP 计；登录未萌账号可提升抓取配额）"
}

// nextMoeRetryAfter 读 429 的 Retry-After（秒），按手机版口径截到 60s。
// 服务端没给或给了非法值就退回 2s。
func nextMoeRetryAfter(raw string) time.Duration {
	seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || seconds <= 0 {
		return 2 * time.Second
	}
	delay := time.Duration(seconds) * time.Second
	if delay > nextMoeMaxRetryAfter {
		return nextMoeMaxRetryAfter
	}
	return delay
}

// nextMoeProblemCode 取 RFC 9457 problem+json 里的 code；解析不出来返回空串。
func nextMoeProblemCode(body []byte) string {
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &problem); err != nil {
		return ""
	}
	return strings.TrimSpace(problem.Code)
}

func nextMoeMergedCurrentID(body []byte) string {
	var problem struct {
		CurrentID string `json:"current_id"`
	}
	if err := json.Unmarshal(body, &problem); err != nil {
		return ""
	}
	return strings.TrimSpace(problem.CurrentID)
}

// nextMoeProblemSummary 把错误体压成一行，避免把整段 HTML/JSON 塞进错误消息。
func nextMoeProblemSummary(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "empty response body"
	}
	if strings.HasPrefix(trimmed, "<") {
		// 网关返回 HTML（Cloudflare/反代）时不要把它当成 API 契约的一部分。
		return "non-JSON response body"
	}
	if len(trimmed) > 512 {
		return trimmed[:512] + "…"
	}
	return trimmed
}
