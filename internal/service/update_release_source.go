package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"yukihub/internal/appconf"
	"yukihub/internal/applog"
	"yukihub/internal/utils/httputils"
	"yukihub/internal/version"

	"resty.dev/v3"
)

// 更新检查的来源：直接读取代码托管平台的 releases/latest，与 Android 手机版一致
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

var errNoReleasePublished = errors.New("该更新源上还没有发布任何版本")

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

// releaseResponse 同时兼容 GitHub 与 GitCode 的 releases/latest 返回。
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

// fetchLatestRelease 按配置的更新源拉取最新发布，失败自动改用另一个源。
// 返回实际生效的更新源标识。
func (s *UpdateService) fetchLatestRelease(appConfig *appconf.AppConfig) (*UpdateInfo, string, error) {
	primary := NormalizeUpdateSource(appConfig.UpdateSource)
	candidates := []string{primary, otherUpdateSource(primary)}

	var lastErr error
	for _, source := range candidates {
		info, err := s.fetchReleaseFrom(source, appConfig)
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

	client, _, err := httputils.NewRestyClient(httputils.ClientOptions{
		Timeout:     10 * time.Second,
		ProxyConfig: appConfig,
	})
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

	switch resp.StatusCode() {
	case http.StatusOK:
		// 继续解析
	case http.StatusNotFound, http.StatusBadRequest:
		// GitHub 用 404、GitCode 用 400 表示仓库里没有任何发布
		return nil, errNoReleasePublished
	default:
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	var release releaseResponse
	if err := json.Unmarshal(resp.Bytes(), &release); err != nil {
		return nil, err
	}

	return buildUpdateInfoFromRelease(source, slug, release)
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
			if info.UpdateManifestURL == "" {
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

	return info, nil
}

func isRealReleaseAsset(asset releaseAsset) bool {
	assetType := strings.TrimSpace(asset.Type)
	if assetType == "" {
		return true // GitHub 不带 type，一律视为附件
	}
	return strings.EqualFold(assetType, "attach")
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
