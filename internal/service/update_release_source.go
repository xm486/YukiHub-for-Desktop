package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"yukihub/internal/appconf"
	"yukihub/internal/applog"
	"yukihub/internal/utils/httputils"
	"yukihub/internal/version"

	"resty.dev/v3"
)

// 更新检查的来源：直接读取代码托管平台的发布，与 Android 手机版一致
// （不再依赖自建更新服务）。
//
// 默认 GitCode（国内访问稳定），可在设置里切换到 GitHub；选定的源请求失败时
// **自动改用另一个源**，两个都失败才报错。
const (
	UpdateSourceGitCode = "gitcode"
	UpdateSourceGitHub  = "github"

	// updateSourceCustom 表示用户填了自定义检查地址（沿用旧的 version.json 格式）。
	updateSourceCustom = "custom"
)

// 版本检查的通道：
//
//   - stable：读 `releases/latest`，平台只返回最新**正式版**；
//   - dev：读滚动开发版预发布 `dev-latest`（每次开发版构建覆盖同一个标签）。
//
// 自定义更新源（`update_check_url`）按 stable 处理 —— 那是用户自己的
// version.json，与托管平台的开发版标签无关。
const (
	UpdateChannelStable = "stable"
	UpdateChannelDev    = "dev"

	// devReleaseTag 是 `.github/workflows/autobuild.yml` 滚动覆盖的开发版标签。
	// 它的版本号不在 tag 里，见 extractDevReleaseVersion。
	devReleaseTag = "dev-latest"
)

var (
	errNoReleasePublished    = errors.New("该更新源上还没有发布任何版本")
	errNoDevReleasePublished = errors.New("该更新源上还没有发布任何开发版")
)

// NormalizeUpdateSource 把配置值归一化成受支持的更新源（默认 GitCode）。
func NormalizeUpdateSource(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), UpdateSourceGitHub) {
		return UpdateSourceGitHub
	}
	return UpdateSourceGitCode
}

func otherUpdateSource(source string) string {
	if NormalizeUpdateSource(source) == UpdateSourceGitHub {
		return UpdateSourceGitCode
	}
	return UpdateSourceGitHub
}

// repositorySlug 返回 `owner/repo`，取自 internal/version.RepositoryURL（单一来源）。
func repositorySlug() (string, error) {
	raw := strings.TrimSuffix(strings.TrimSpace(version.RepositoryURL), "/")
	raw = strings.TrimSuffix(raw, ".git")

	const prefix = "https://github.com/"
	if !strings.HasPrefix(raw, prefix) {
		return "", fmt.Errorf("unsupported repository url: %s", version.RepositoryURL)
	}

	slug := strings.TrimPrefix(raw, prefix)
	if strings.Count(slug, "/") != 1 || strings.HasPrefix(slug, "/") {
		return "", fmt.Errorf("invalid repository slug: %s", slug)
	}
	return slug, nil
}

func releaseAPIURL(source string, slug string) string {
	if NormalizeUpdateSource(source) == UpdateSourceGitHub {
		return "https://api.github.com/repos/" + slug + "/releases/latest"
	}
	return "https://gitcode.com/api/v5/repos/" + slug + "/releases/latest"
}

// releaseListAPIURL 是发布**列表**接口，含预发布（GitHub 的 `releases/latest`
// 会跳过预发布，GitCode 同理）。开发版通道只能用列表才能看到 `dev-latest`。
func releaseListAPIURL(source string, slug string) string {
	if NormalizeUpdateSource(source) == UpdateSourceGitHub {
		return "https://api.github.com/repos/" + slug + "/releases?per_page=30"
	}
	return "https://gitcode.com/api/v5/repos/" + slug + "/releases"
}

func releaseAPIAccept(source string) string {
	if NormalizeUpdateSource(source) == UpdateSourceGitHub {
		return "application/vnd.github+json"
	}
	return "application/json"
}

func releasePageURL(source string, slug string, tag string) string {
	host := "https://gitcode.com/"
	if NormalizeUpdateSource(source) == UpdateSourceGitHub {
		host = "https://github.com/"
	}
	if strings.TrimSpace(tag) == "" {
		return host + slug + "/releases"
	}
	return host + slug + "/releases/tag/" + url.PathEscape(tag)
}

// releaseAsset 是两个平台的附件结构。GitCode 会额外带上源码包（type = "source"），
// 只认 type = "attach" 的真附件；GitHub 不带 type 字段，一律视为附件。
type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Type string `json:"type"`
}

// releaseResponse 同时兼容 GitHub 与 GitCode 的 releases 返回。
// 差异：GitCode 没有 html_url / published_at（用 created_at），也没有 assets 的 size。
type releaseResponse struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt string         `json:"published_at"`
	CreatedAt   string         `json:"created_at"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	Assets      []releaseAsset `json:"assets"`
}

func newReleaseSourceClient(appConfig *appconf.AppConfig) (*resty.Client, error) {
	client, _, err := httputils.NewRestyClient(httputils.ClientOptions{
		Timeout:     10 * time.Second,
		ProxyConfig: appConfig,
	})
	return client, err
}

// classifyReleaseStatus 把「仓库里一个发布都没有」与其它错误分开，
// 便于上层给出「该更新源上还没有发布任何版本 / 开发版」这类可读提示。
func classifyReleaseStatus(status int, emptyErr error) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusNotFound, http.StatusBadRequest:
		// GitHub 用 404、GitCode 用 400 表示仓库里没有任何发布
		return emptyErr
	default:
		return fmt.Errorf("unexpected status code: %d", status)
	}
}

// fetchLatestRelease 按配置的更新源拉取最新正式版，失败自动改用另一个源。
// 返回实际生效的更新源标识。
func (s *UpdateService) fetchLatestRelease(appConfig *appconf.AppConfig) (*UpdateInfo, string, error) {
	return s.fetchFromSources(appConfig, s.fetchReleaseFrom)
}

// fetchDevRelease 拉取开发版通道（滚动预发布 `dev-latest`），同样带自动换源。
func (s *UpdateService) fetchDevRelease(appConfig *appconf.AppConfig) (*UpdateInfo, string, error) {
	return s.fetchFromSources(appConfig, s.fetchDevReleaseFrom)
}

// fetchFromSources 依次尝试选定源与另一个源，返回实际生效的源。
func (s *UpdateService) fetchFromSources(
	appConfig *appconf.AppConfig,
	fetch func(string, *appconf.AppConfig) (*UpdateInfo, error),
) (*UpdateInfo, string, error) {
	primary := NormalizeUpdateSource(appConfig.UpdateSource)
	candidates := []string{primary, otherUpdateSource(primary)}

	var lastErr error
	for _, source := range candidates {
		info, err := fetch(source, appConfig)
		if err == nil {
			return info, source, nil
		}
		lastErr = err
		applog.LogWarningf(s.ctx, "[UpdateService] %s 更新源不可用：%v", source, err)
	}

	return nil, "", fmt.Errorf("GitHub 与 GitCode 更新源均不可用：%w", lastErr)
}

func (s *UpdateService) fetchReleaseFrom(source string, appConfig *appconf.AppConfig) (*UpdateInfo, error) {
	slug, err := repositorySlug()
	if err != nil {
		return nil, err
	}

	client, err := newReleaseSourceClient(appConfig)
	if err != nil {
		return nil, err
	}

	resp, err := client.R().
		SetHeader("Accept", releaseAPIAccept(source)).
		SetRetryCount(2).
		AddRetryConditions(
			resty.RetryConditionStatusTooManyRequests,
			resty.RetryConditionStatus5XX,
		).
		Get(releaseAPIURL(source, slug))
	if err != nil {
		return nil, err
	}

	if err := classifyReleaseStatus(resp.StatusCode(), errNoReleasePublished); err != nil {
		return nil, err
	}

	var release releaseResponse
	if err := json.Unmarshal(resp.Bytes(), &release); err != nil {
		return nil, err
	}

	return buildUpdateInfoFromRelease(source, slug, release)
}

// fetchDevReleaseFrom 读发布列表并挑出滚动开发版预发布。
//
// GitCode 上这个标签通常不存在（开发版只发到 GitHub），列表会返回空数组；
// 调用方（fetchFromSources）会因此自动改用另一个源。
func (s *UpdateService) fetchDevReleaseFrom(source string, appConfig *appconf.AppConfig) (*UpdateInfo, error) {
	slug, err := repositorySlug()
	if err != nil {
		return nil, err
	}

	client, err := newReleaseSourceClient(appConfig)
	if err != nil {
		return nil, err
	}

	resp, err := client.R().
		SetHeader("Accept", releaseAPIAccept(source)).
		SetRetryCount(2).
		AddRetryConditions(
			resty.RetryConditionStatusTooManyRequests,
			resty.RetryConditionStatus5XX,
		).
		Get(releaseListAPIURL(source, slug))
	if err != nil {
		return nil, err
	}

	if err := classifyReleaseStatus(resp.StatusCode(), errNoDevReleasePublished); err != nil {
		return nil, err
	}

	var releases []releaseResponse
	if err := json.Unmarshal(resp.Bytes(), &releases); err != nil {
		return nil, err
	}

	release, found := pickDevRelease(releases)
	if !found {
		return nil, errNoDevReleasePublished
	}

	return buildDevUpdateInfo(source, slug, release)
}

// pickDevRelease 从发布列表里挑出滚动开发版。按标签精确匹配，不依赖列表顺序
// （两个平台都按发布时间倒序返回，但没必要赌这一点）。
func pickDevRelease(releases []releaseResponse) (releaseResponse, bool) {
	for _, release := range releases {
		if release.Draft {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(release.TagName), devReleaseTag) {
			return release, true
		}
	}
	return releaseResponse{}, false
}

func buildUpdateInfoFromRelease(source string, slug string, release releaseResponse) (*UpdateInfo, error) {
	tagName := strings.TrimSpace(release.TagName)
	if tagName == "" {
		return nil, fmt.Errorf("%s 未返回有效版本号（tag_name 为空）", source)
	}

	releaseURL := strings.TrimSpace(release.HTMLURL)
	if releaseURL == "" {
		releaseURL = releasePageURL(source, slug, tagName)
	}

	info := &UpdateInfo{
		Version:     strings.TrimPrefix(tagName, "v"),
		ReleaseDate: releaseDateOf(release),
		Changelog:   splitChangelog(release.Body),
		Downloads:   map[string]string{"release_page": releaseURL},
		ReleaseURL:  releaseURL,
	}
	applyReleaseAssets(info, release, true)

	return info, nil
}

// buildDevUpdateInfo 把 `dev-latest` 预发布转成更新信息。
//
// 与正式版有两点不同：
//   - 版本号不在 tag 里（tag 是滚动的 `dev-latest`），改从发布说明/标题/附件名提取；
//   - **不带更新清单**：自建更新服务的 manifest 只发布正式版，拼出来一定是 404，
//     所以开发版只提供「打开发布页 / 下载」。
func buildDevUpdateInfo(source string, slug string, release releaseResponse) (*UpdateInfo, error) {
	version, err := extractDevReleaseVersion(release)
	if err != nil {
		return nil, fmt.Errorf("%s 的 %s 发布无法识别版本号：%w", source, devReleaseTag, err)
	}

	releaseURL := strings.TrimSpace(release.HTMLURL)
	if releaseURL == "" {
		releaseURL = releasePageURL(source, slug, strings.TrimSpace(release.TagName))
	}

	info := &UpdateInfo{
		Version:     version,
		ReleaseDate: releaseDateOf(release),
		Changelog:   splitChangelog(release.Body),
		Downloads:   map[string]string{"release_page": releaseURL},
		ReleaseURL:  releaseURL,
	}
	applyReleaseAssets(info, release, false)

	return info, nil
}

// applyReleaseAssets 把发布附件映射成下载入口。includeManifest 为假时忽略清单附件
// （开发版通道用），永远只提供「打开发布页」。
func applyReleaseAssets(info *UpdateInfo, release releaseResponse, includeManifest bool) {
	for _, asset := range release.Assets {
		if !isRealReleaseAsset(asset) {
			continue
		}
		assetURL := strings.TrimSpace(asset.URL)
		if assetURL == "" {
			continue
		}

		name := strings.ToLower(strings.TrimSpace(asset.Name))
		switch {
		case strings.Contains(name, "manifest"):
			// 发布里带了更新清单才提供「应用内自动更新」入口
			if includeManifest && info.UpdateManifestURL == "" {
				info.UpdateManifestURL = assetURL
			}
		case strings.Contains(name, "windows"):
			if info.Downloads["windows"] == "" {
				info.Downloads["windows"] = assetURL
			}
		case strings.Contains(name, "linux"):
			if info.Downloads["linux"] == "" {
				info.Downloads["linux"] = assetURL
			}
		}
	}
}

func isRealReleaseAsset(asset releaseAsset) bool {
	assetType := strings.TrimSpace(asset.Type)
	if assetType == "" {
		return true // GitHub 不带 type，一律视为附件
	}
	return strings.EqualFold(assetType, "attach")
}

// devVersionPatterns 按可靠度排列的开发版版本号来源，都要求取出来的值
// 确实能当版本号解析（见 normalizeDevVersion），免得误抓正文里的其它数字。
var devVersionPatterns = []*regexp.Regexp{
	// <!-- yukihub-version: 0.2.5-dev.130+abc1234 -->（预留的机器可读标记）
	regexp.MustCompile(`(?i)yukihub-version:\s*v?([0-9][0-9A-Za-z.+-]*)`),
	// - **版本号**: `0.2.5-dev.130+abc1234`
	regexp.MustCompile(`版本号[^0-9]*v?([0-9][0-9A-Za-z.+-]*)`),
	// YukiHub-0.2.5-dev.130+abc1234-windows-amd64-portable.zip
	regexp.MustCompile(`(?i)yukihub-v?([0-9][0-9A-Za-z.+-]*?)-(?:windows|linux)-`),
}

// extractDevReleaseVersion 取开发版的真实版本号。
//
// 滚动标签 `dev-latest` 本身不带版本号，真版本号（形如 `0.2.5-dev.130+abc1234`）
// 由 CI 写在发布说明的「版本号」一行，同时也出现在发布标题与附件名里；
// 三处依次尝试，都能拿到才算这个版本可用。
func extractDevReleaseVersion(release releaseResponse) (string, error) {
	if version := firstDevVersionMatch(release.Body); version != "" {
		return version, nil
	}

	// 发布标题形如「开发版 - 0.2.5-dev.130+abc1234」
	if name := strings.TrimSpace(release.Name); name != "" {
		if index := strings.LastIndex(name, " - "); index >= 0 {
			if version := normalizeDevVersion(name[index+len(" - "):]); version != "" {
				return version, nil
			}
		}
	}

	for _, asset := range release.Assets {
		if version := firstDevVersionMatch(asset.Name); version != "" {
			return version, nil
		}
	}

	return "", errors.New("发布说明、标题与附件名里都没有可识别的版本号")
}

func firstDevVersionMatch(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	for _, pattern := range devVersionPatterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			if version := normalizeDevVersion(match[1]); version != "" {
				return version
			}
		}
	}
	return ""
}

// normalizeDevVersion 去掉 `v` 前缀并校验确实是能比较的版本号。
func normalizeDevVersion(value string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(value), "v")
	if trimmed == "" {
		return ""
	}
	if _, err := parseVersion(trimmed); err != nil {
		return ""
	}
	return trimmed
}

func releaseDateOf(release releaseResponse) string {
	const dateLength = len("2006-01-02")
	for _, value := range []string{release.PublishedAt, release.CreatedAt} {
		trimmed := strings.TrimSpace(value)
		if len(trimmed) >= dateLength {
			return trimmed[:dateLength]
		}
	}
	return ""
}

func splitChangelog(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}

	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return lines
}
