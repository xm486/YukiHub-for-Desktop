package version

import "strings"

const (
	userAgentPrefix = "xm486/YukiHub/"
	userAgentSuffix = " (desktop) (https://github.com/xm486/YukiHub-for-Desktop)"
)

// 产品与来源信息。
//
// 界面"关于"面板与合规材料统一引用这里，避免同一条信息在多个文件里各写一份。
// 修改这些值前请同步更新 NOTICE 与 docs/AGPL-COMPLIANCE.md。
const (
	AppDisplayName  = "YukiHub Desktop"
	LicenseName     = "AGPL-3.0"
	RepositoryURL   = "https://github.com/xm486/YukiHub-for-Desktop"
	UpstreamProject = "LunaBox"
	UpstreamVersion = "v1.13.0"
	UpstreamRepoURL = "https://github.com/Saramanda9988/LunaBox"
)

// 版本信息，通过 ldflags 在编译时注入。
//
// 说明：以下第三方服务的凭证必须由 YukiHub 自行申请，不复用上游 LunaBox 的凭据。
// 留空表示"未配置"，相应功能会给出未配置提示而不是偷偷使用他人的应用身份。
var (
	Version                        = "0.1.0-dev" // 版本号，正式构建由 ldflags 覆盖
	GitCommit                      = "unknown"   // Git commit hash
	BuildTime                      = "unknown"   // 构建时间
	BuildMode                      = "portable"  // 构建模式：portable、installer 或 appimage
	UpdateServiceURL               = ""          // 更新服务根地址，由正式构建注入
	BangumiOAuthClientID           = ""          // Bangumi OAuth Client ID
	BangumiOAuthClientSecret       = ""          // Bangumi OAuth Client Secret
	HikarinagiOAuthClientID        = ""          // Hikarinagi public/native OAuth Client ID（登录用）
	HikarinagiOAuthClientSecret    = ""          // Hikarinagi OAuth Client Secret（登录用 public client 不需要）
	HikarinagiOAuthScopes          = ""          // Hikarinagi 登录 OAuth scope，留空时用内置默认（与 Android 客户端一致）
	HikarinagiMetadataClientID     = ""          // Hikarinagi 元数据 API（client_credentials）Client ID
	HikarinagiMetadataClientSecret = ""          // Hikarinagi 元数据 API（client_credentials）Client Secret
	UmbraOAuthClientID             = ""          // Umbra public/native OAuth Client ID
	UmbraRegistrationToken         = ""          // Umbra device installation/registration token
	TouchGalAPIToken               = ""          // TouchGAL API Bearer token
)

// GetVersion 返回版本信息
func GetVersion() string {
	return Version
}

// GetFullVersion 返回完整版本信息
func GetFullVersion() string {
	return Version + " (" + GitCommit + ")"
}

// GetBuildMode 返回构建模式
func GetBuildMode() string {
	return BuildMode
}

// UserAgent 返回包含当前构建版本的应用 User-Agent。
func UserAgent() string {
	appVersion := strings.TrimSpace(Version)
	if appVersion == "" {
		appVersion = "unknown"
	}
	return userAgentPrefix + appVersion + userAgentSuffix
}
