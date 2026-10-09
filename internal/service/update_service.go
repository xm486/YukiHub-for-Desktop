package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
	"yukihub/internal/appconf"
	"yukihub/internal/applog"
	"yukihub/internal/updateclient"
	"yukihub/internal/utils/httputils"

	"yukihub/internal/version"

	"resty.dev/v3"
	"yukihub/internal/wailsruntime"
)

// UpdateInfo 版本信息结构
type UpdateInfo struct {
	Version           string            `json:"version"`             // 版本号，如 0.2.5
	ReleaseDate       string            `json:"release_date"`        // 发布日期，如 2024-01-15
	Changelog         []string          `json:"changelog"`           // 更新日志内容数组
	Downloads         map[string]string `json:"downloads"`           // 下载链接：release_page / windows / linux
	ReleaseURL        string            `json:"release_url"`         // 发布页地址
	UpdateManifestURL string            `json:"update_manifest_url"` // 应用内更新清单（有才提供自动更新）
}

// UpdateCheckResult 更新检查结果
type UpdateCheckResult struct {
	HasUpdate         bool              `json:"has_update"`          // 是否有更新
	CurrentVer        string            `json:"current_ver"`         // 当前版本
	LatestVer         string            `json:"latest_ver"`          // 最新版本
	ReleaseDate       string            `json:"release_date"`        // 发布日期
	Changelog         []string          `json:"changelog"`           // 更新日志内容
	Downloads         map[string]string `json:"downloads"`           // 下载链接
	ReleaseURL        string            `json:"release_url"`         // 发布页地址
	UpdateManifestURL string            `json:"update_manifest_url"` // 应用内更新清单
	UpdateSource      string            `json:"update_source"`       // 本次实际生效的更新源：gitcode / github / custom
}

// UpdateService 更新服务
type UpdateService struct {
	ctx         context.Context
	config      *ConfigService
	quitHandler func()
	applyMu     sync.Mutex
	runtime     wailsruntime.Runtime
}

func NewUpdateService(quitHandlers ...func()) *UpdateService {
	service := &UpdateService{runtime: wailsruntime.Unavailable()}
	if len(quitHandlers) > 0 {
		service.quitHandler = quitHandlers[0]
	}
	return service
}

//wails:ignore
func (s *UpdateService) Init(ctx context.Context) {
	s.ctx = ctx
}

//wails:ignore
func (s *UpdateService) SetRuntime(runtime wailsruntime.Runtime) {
	if runtime != nil {
		s.runtime = runtime
	}
}

// SetConfigService 设置 ConfigService（用于读取和更新应用配置）。
//
//wails:ignore
func (s *UpdateService) SetConfigService(configService *ConfigService) {
	s.config = configService
	if s.ctx == nil || configService == nil {
		return
	}
	appConfig, err := configService.GetAppConfig()
	if err != nil {
		return
	}
	go func() {
		if err := updateclient.ReportPendingResult(s.ctx, &appConfig, version.UserAgent()); err != nil {
			applog.LogWarningf(s.ctx, "Failed to report pending update result: %v", err)
		}
	}()
}

// CheckForUpdates 手动检查更新（忽略跳过版本设置，总是检查最新版本）
func (s *UpdateService) CheckForUpdates() (*UpdateCheckResult, error) {
	return s.checkUpdates(false)
}

// CheckForUpdatesOnStartup 启动时自动检查更新
func (s *UpdateService) CheckForUpdatesOnStartup() (*UpdateCheckResult, error) {
	return s.checkUpdates(true)
}

// checkUpdates 检查更新的核心逻辑
// isAutoCheck: true 表示启动时自动检查，会检查频率限制和跳过版本
// isAutoCheck: false 表示手动检查，忽略跳过版本（因为在调用前已清空）
func (s *UpdateService) checkUpdates(isAutoCheck bool) (*UpdateCheckResult, error) {
	// 获取应用配置（手动检查时，SkipVersion 已在 CheckForUpdates 中被清空）
	appConfig, err := s.config.GetAppConfig()
	if err != nil {
		applog.LogError(s.ctx, "[UpdateService]获取应用配置失败: "+err.Error())
		return nil, fmt.Errorf("failed to get app config: %w", err)
	}

	// 如果是启动时自动检查且未启用，直接返回
	if isAutoCheck && !appConfig.CheckUpdateOnStartup {
		return nil, nil
	}

	// 限制启动时检查的频率（最多每天一次）
	if isAutoCheck {
		if appConfig.LastUpdateCheck != "" {
			lastCheck, err := time.Parse(time.RFC3339, appConfig.LastUpdateCheck)
			if err == nil && time.Since(lastCheck) < 24*time.Hour {
				// 24小时内已检查过，跳过
				return nil, nil
			}
		}
	}

	// 取更新信息：填了自定义地址就按旧格式读，否则查代码托管平台的 releases/latest。
	var (
		updateInfo   *UpdateInfo
		updateSource string
	)

	if customURL := strings.TrimSpace(appConfig.UpdateCheckURL); customURL != "" {
		fetched, fetchErr := s.fetchUpdateInfo(customURL, &appConfig)
		if fetchErr != nil {
			applog.LogWarningf(s.ctx, "[UpdateService] 自定义更新源不可用：%v", fetchErr)
			return nil, fmt.Errorf("[UpdateService] 自定义更新源不可用: %w", fetchErr)
		}
		updateInfo, updateSource = fetched, updateSourceCustom
	} else {
		fetched, source, fetchErr := s.fetchLatestRelease(&appConfig)
		if fetchErr != nil {
			applog.LogWarningf(s.ctx, "[UpdateService] 获取最新发布失败：%v", fetchErr)
			return nil, fmt.Errorf("[UpdateService] 检查更新失败: %w", fetchErr)
		}
		updateInfo, updateSource = fetched, source
	}

	// 更新最后检查时间
	s.updateLastCheckTime()

	// 比较版本
	currentVer := version.Version
	hasUpdate, err := compareVersions(currentVer, updateInfo.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to compare versions: %w", err)
	}

	// 只有发布里带了更新清单、或构建期注入了自建更新服务时，才拼装自动更新地址；
	// 都没有时留空 → 界面只提供「打开发布页」。
	if strings.TrimSpace(updateInfo.UpdateManifestURL) == "" &&
		updateSource != updateSourceCustom &&
		goruntime.GOOS == "windows" {
		manifestURL, manifestErr := buildOfficialUpdateManifestURL(version.UpdateServiceURL, updateInfo.Version)
		if manifestErr != nil {
			return nil, fmt.Errorf("failed to build update manifest url: %w", manifestErr)
		}
		updateInfo.UpdateManifestURL = manifestURL
	}

	// 只有自动检查时才检查跳过版本（手动检查时 SkipVersion 已被清空）
	if isAutoCheck && hasUpdate {
		skipVersionNormalized := strings.TrimSpace(strings.TrimPrefix(appConfig.SkipVersion, "v"))
		latestVersionNormalized := strings.TrimSpace(strings.TrimPrefix(updateInfo.Version, "v"))
		if skipVersionNormalized != "" && skipVersionNormalized == latestVersionNormalized {
			hasUpdate = false
		}
	}

	result := &UpdateCheckResult{
		HasUpdate:         hasUpdate,
		CurrentVer:        currentVer,
		LatestVer:         updateInfo.Version,
		ReleaseDate:       updateInfo.ReleaseDate,
		Changelog:         updateInfo.Changelog,
		Downloads:         updateInfo.Downloads,
		ReleaseURL:        updateInfo.ReleaseURL,
		UpdateManifestURL: updateInfo.UpdateManifestURL,
		UpdateSource:      updateSource,
	}

	return result, nil
}

func buildOfficialUpdateManifestURL(serviceURL string, releaseVersion string) (string, error) {
	serviceURL = strings.TrimRight(strings.TrimSpace(serviceURL), "/")
	if serviceURL == "" {
		return "", nil
	}
	normalizedVersion, err := normalizeComparableVersion(releaseVersion)
	if err != nil {
		return "", err
	}
	versionWithoutPrefix := strings.TrimPrefix(normalizedVersion, "v")
	return serviceURL + "/v1/releases/" + url.PathEscape(versionWithoutPrefix) + "/manifest", nil
}

// fetchUpdateInfo 从指定 URL 获取版本信息
func (s *UpdateService) fetchUpdateInfo(url string, appConfig *appconf.AppConfig) (*UpdateInfo, error) {
	client, _, err := httputils.NewRestyClient(httputils.ClientOptions{
		Timeout:     10 * time.Second,
		ProxyConfig: appConfig,
	})
	if err != nil {
		return nil, err
	}
	resp, err := client.R().
		SetRetryCount(3).
		AddRetryConditions(
			resty.RetryConditionStatusTooManyRequests,
			resty.RetryConditionStatus5XX,
		).
		Get(url)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	var info UpdateInfo
	if err := json.Unmarshal(resp.Bytes(), &info); err != nil {
		return nil, err
	}

	// 验证必填字段
	if info.Version == "" {
		return nil, fmt.Errorf("missing version field")
	}

	return &info, nil
}

// updateLastCheckTime 更新最后检查时间
func (s *UpdateService) updateLastCheckTime() {
	appConfig, err := s.config.GetAppConfig()
	if err != nil {
		return
	}
	appConfig.LastUpdateCheck = time.Now().Format(time.RFC3339)
	s.config.UpdateAppConfig(appConfig)
}

// SkipVersion 跳过指定版本的更新
func (s *UpdateService) SkipVersion(ver string) error {
	appConfig, err := s.config.GetAppConfig()
	if err != nil {
		return err
	}
	// 统一移除 v 前缀，确保存储格式一致
	appConfig.SkipVersion = strings.TrimSpace(strings.TrimPrefix(ver, "v"))
	return s.config.UpdateAppConfig(appConfig)
}

// OpenDownloadURL 打开下载页面（已废弃，请在前端使用 @wailsio/runtime 的 Browser.OpenURL）。
func (s *UpdateService) OpenDownloadURL(url string) error {
	return s.runtime.OpenURL(url)
}
