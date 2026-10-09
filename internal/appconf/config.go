package appconf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	enums2 "yukihub/internal/common/enums"
	"yukihub/internal/utils"
	"yukihub/internal/utils/apputils"
	"yukihub/internal/utils/coverutils"
	"yukihub/internal/utils/proxyutils"
)

// configBackupSuffix 是配置文件的有效快照后缀（appconf.json.bak）。
//
// 主文件写坏（中断、断电、磁盘错误）时用它恢复，避免用户设置整份丢失。
const configBackupSuffix = ".bak"

// configCorruptSuffix 保存最后一次「主文件 + 快照都救不回来」时的损坏原文。
const configCorruptSuffix = ".corrupt"

// configFileMu 串行化本进程内的配置写入，避免两个 goroutine 同时替换主文件。
// 跨进程保护由 writeFileAtomic 的「临时文件 + 原子替换」保证。
var configFileMu sync.Mutex

var defaultMetadataSources = []string{
	string(enums2.VNDB),
	string(enums2.Bangumi),
	string(enums2.Ymgal),
	string(enums2.Hikarinagi),
}

// DefaultCurrentMetadataSource 是「当前资料源」的默认值。
// 与手机版一致：默认 VNDB（资料最全，且无需 Token）。
const DefaultCurrentMetadataSource = enums2.VNDB

var allowedMetadataSourceSet = map[string]struct{}{
	string(enums2.VNDB):          {},
	string(enums2.Bangumi):       {},
	string(enums2.BangumiMirror): {},
	string(enums2.Ymgal):         {},
	string(enums2.Hikarinagi):    {},
	string(enums2.NextMoe):       {},
}

const legacyOneDriveDefaultClientID = "26fcab6e-41ea-49ff-8ec9-063983cae3ef"

const DefaultMCPPort = 39200
const DefaultScrapedTagLimit = 10
const DefaultHomeGameCarouselIntervalSec = 6
const MinHomeGameCarouselIntervalSec = 4
const DefaultProcessDetectionTimeoutSec = 60
const MinProcessDetectionTimeoutSec = 60
const MaxProcessDetectionTimeoutSec = 600
const DefaultBatchImportScanPreset = "scan_parent"
const MaxBatchImportHierarchyDepth = 5
const DefaultGameCardLayout = "portrait"
const DefaultBigScreenDefaultCategory = "recent"
const DefaultBigScreenEffectLevel = "low"

// 大屏模式的偏好取值范围，与手机端 BigScreenPrefs 的 clamp 区间保持一致。
const (
	DefaultBigScreenSoundVolume    = 65
	DefaultBigScreenCardScale      = 112
	MinBigScreenCardScale          = 80
	MaxBigScreenCardScale          = 140
	DefaultBigScreenFocusScale     = 100
	MaxBigScreenFocusScale         = 150
	DefaultBigScreenKeyStyle       = "xbox"
	DefaultBigScreenHintMode       = "auto"
	DefaultBigScreenTrailerDelayMs = 2000
	MinBigScreenTrailerDelayMs     = 300
	MaxBigScreenTrailerDelayMs     = 5000
	DefaultBigScreenPVScrimPercent = 45
	DefaultBigScreenBannerHoldMs   = 2000
	MinBigScreenBannerHoldMs       = 800
	MaxBigScreenBannerHoldMs       = 6000
)
const DefaultUmbraBaseURL = "https://umbrae.cc"
const ScheduledDBBackupModeInterval = "interval"
const ScheduledDBBackupModeDaily = "daily"
const DefaultScheduledDBBackupIntervalMinutes = 60
const MinScheduledDBBackupIntervalMinutes = 15
const MaxScheduledDBBackupIntervalMinutes = 10080
const DefaultScheduledDBBackupTime = "03:00"
const DefaultLocalDBBackupRetention = 5

// 计时模式决定一次游玩会话「什么时候开始、什么时候结束、时长怎么算」。
//
//   - process（默认）：按被监控的游戏进程存活墙钟计时（可叠加「仅记录活跃时长」），
//     进程退出后自动结算。这是 LunaBox 血统的原始行为。
//   - manual（手动计时 / Yuki 式计时）：点「启动」即开始计时，全程**不做任何
//     进程监测**，回到 YukiHub 手动点「停止」才结算。与手机版一致（手机上本来
//     也监控不到进程），且完全不受进程识别失败 / 启动器套娃的影响。
const (
	PlayTimingModeProcess = "process"
	PlayTimingModeManual  = "manual"
	DefaultPlayTimingMode = PlayTimingModeProcess
)

// AppConfig 应用配置结构体
type AppConfig struct {
	BangumiAccessToken            string   `json:"access_token,omitempty"`
	BangumiRefreshToken           string   `json:"bangumi_refresh_token,omitempty"`
	BangumiTokenExpiresAt         string   `json:"bangumi_token_expires_at,omitempty"`
	BangumiAuthorizedUserID       string   `json:"bangumi_authorized_user_id,omitempty"`
	BangumiAuthorizedUsername     string   `json:"bangumi_authorized_username,omitempty"`
	BangumiAuthorizedAvatarURL    string   `json:"bangumi_authorized_avatar_url,omitempty"`
	BangumiAuthError              string   `json:"bangumi_auth_error,omitempty"`
	BangumiStatusPushEnabled      *bool    `json:"bangumi_status_push_enabled,omitempty"`
	HikarinagiAccessToken         string   `json:"hikarinagi_access_token,omitempty"`
	HikarinagiRefreshToken        string   `json:"hikarinagi_refresh_token,omitempty"`
	HikarinagiTokenExpiresAt      string   `json:"hikarinagi_token_expires_at,omitempty"`
	HikarinagiAuthorizedUserID    string   `json:"hikarinagi_authorized_user_id,omitempty"`
	HikarinagiAuthorizedUsername  string   `json:"hikarinagi_authorized_username,omitempty"`
	HikarinagiAuthorizedAvatarURL string   `json:"hikarinagi_authorized_avatar_url,omitempty"`
	HikarinagiAuthError           string   `json:"hikarinagi_auth_error,omitempty"`
	HikarinagiStatusPushEnabled   *bool    `json:"hikarinagi_status_push_enabled,omitempty"`
	NextMoeAccessToken            string   `json:"nextmoe_access_token,omitempty"`
	NextMoeRefreshToken           string   `json:"nextmoe_refresh_token,omitempty"`
	NextMoeTokenExpiresAt         string   `json:"nextmoe_token_expires_at,omitempty"`
	NextMoeAccountLabel           string   `json:"nextmoe_account_label,omitempty"`
	VNDBAccessToken               string   `json:"vndb_access_token,omitempty"`
	MetadataSources               []string `json:"metadata_sources,omitempty"` // 元数据拉取来源列表（vndb/bangumi/bangumi_mirror/ymgal/hikarinagi/nextmoe）
	// CurrentMetadataSource 是「当前资料源」：游戏资料优先展示/抓取哪个来源。
	// 对齐手机版设置里的「右侧资料源」（`metadata_source`）。单个取值，
	// 与上面「启用了哪些来源」的多选相互独立。
	CurrentMetadataSource        enums2.SourceType          `json:"current_metadata_source,omitempty"`
	AllowDuplicateMetadataImport bool                       `json:"allow_duplicate_metadata_import"` // 批量/外部导入时允许相同 source_type + source_id
	BangumiCoverSource           enums2.MetadataCoverSource `json:"bangumi_cover_source,omitempty"`  // Bangumi 封面来源
	VNDBCoverSource              enums2.MetadataCoverSource `json:"vndb_cover_source,omitempty"`     // VNDB 封面来源
	Theme                        string                     `json:"theme"`                           // light or dark
	Language                     string                     `json:"language"`                        // zh, en, etc.
	SidebarOpen                  bool                       `json:"sidebar_open"`                    // 侧边栏是否展开
	CloseToTray                  bool                       `json:"close_to_tray"`                   // 关闭时最小化到托盘
	// AI 配置
	AIProvider     string `json:"ai_provider,omitempty"`      // openai, deepseek, etc.
	AIBaseURL      string `json:"ai_base_url,omitempty"`      // API base URL
	AIAPIKey       string `json:"ai_api_key,omitempty"`       // API key
	AIModel        string `json:"ai_model,omitempty"`         // model name
	AISystemPrompt string `json:"ai_system_prompt,omitempty"` // AI 系统提示语
	// AI 高级配置（防剧透 / WebSearch / 上下文）
	AISpoilerLevel      string `json:"ai_spoiler_level,omitempty"`  // none | mild | full，全局防剧透默认等级
	AIWebSearchEnabled  bool   `json:"ai_web_search"`               // 是否启用 WebSearch 工具调用
	AIContextWindowSize int    `json:"ai_context_window,omitempty"` // 送入的历史 session 数量上限（0=默认10）
	TavilyAPIKey        string `json:"tavily_api_key,omitempty"`    // Tavily Search API Key（WebSearch）
	MCPEnabled          bool   `json:"mcp_enabled"`                 // 是否启用 GUI 内嵌 MCP HTTP 服务
	MCPPort             int    `json:"mcp_port,omitempty"`          // MCP HTTP 服务监听端口（仅绑定 127.0.0.1）
	// 云备份配置
	CloudBackupEnabled   bool   `json:"cloud_backup_enabled"`             // 是否启用云备份
	CloudBackupProvider  string `json:"cloud_backup_provider,omitempty"`  // 云备份提供商: s3, onedrive, umbra, webdav
	BackupPassword       string `json:"backup_password,omitempty"`        // 备份密码（用于生成 user-id 和加密）
	BackupUserID         string `json:"backup_user_id,omitempty"`         // 云端用户标识（由备份密码 hash 生成）
	CloudSyncEnabled     bool   `json:"cloud_sync_enabled"`               // 是否启用云同步
	AutoCloudSyncEnabled bool   `json:"auto_cloud_sync_enabled"`          // 是否启用自动云同步（启动时 + 定时）
	CloudSyncIntervalSec int    `json:"cloud_sync_interval_sec"`          // 定时全量同步间隔（秒）
	LastCloudSyncTime    string `json:"last_cloud_sync_time,omitempty"`   // 上次云同步时间
	LastCloudSyncStatus  string `json:"last_cloud_sync_status,omitempty"` // 上次云同步状态: idle/syncing/success/failed
	LastCloudSyncError   string `json:"last_cloud_sync_error,omitempty"`  // 上次云同步错误
	S3Endpoint           string `json:"s3_endpoint,omitempty"`            // S3 兼容端点
	S3Region             string `json:"s3_region,omitempty"`              // S3 区域
	S3Bucket             string `json:"s3_bucket,omitempty"`              // S3 存储桶
	S3AccessKey          string `json:"s3_access_key,omitempty"`          // S3 Access Key
	S3SecretKey          string `json:"s3_secret_key,omitempty"`          // S3 Secret Key
	CloudBackupRetention int    `json:"cloud_backup_retention,omitempty"` // 云端每个游戏保留的存档备份数量

	CloudDBBackupRetention int `json:"cloud_db_backup_retention,omitempty"` // 云端保留的数据库备份数量

	// YukiHub 账号（自建账号服务 yukihub.zh.kg）
	//
	// 令牌与第三方授权一样明文存在 appconf.json（与 Bangumi 个人令牌同一处理方式），
	// 桌面端没有更可靠的本地密钥存储可用。
	YukiHubAccountAccessToken     string `json:"yukihub_account_access_token,omitempty"`
	YukiHubAccountRefreshToken    string `json:"yukihub_account_refresh_token,omitempty"`
	YukiHubAccountUserID          string `json:"yukihub_account_user_id,omitempty"`
	YukiHubAccountUID             int64  `json:"yukihub_account_uid,omitempty"`
	YukiHubAccountNickname        string `json:"yukihub_account_nickname,omitempty"`
	YukiHubAccountEmail           string `json:"yukihub_account_email,omitempty"`
	YukiHubAccountAvatar          string `json:"yukihub_account_avatar,omitempty"`
	YukiHubAccountKungalBound     bool   `json:"yukihub_account_kungal_bound,omitempty"`
	YukiHubAccountHikarinagiBound bool   `json:"yukihub_account_hikarinagi_bound,omitempty"`
	// YukiHubAccountCloudSyncEnabled 控制「登录后自动同步游戏库」。
	YukiHubAccountCloudSyncEnabled bool `json:"yukihub_account_cloud_sync_enabled"`
	// YukiHubAccountSharePlaying 为 false 时仍然上报心跳，但不带上「正在玩」。
	YukiHubAccountSharePlaying bool `json:"yukihub_account_share_playing"`
	// YukiHubAccountFriendPlayNotify 控制「好友开始玩游戏时弹通知」。
	//
	// 与手机版 PresenceManager.KEY_FRIEND_PLAY_NOTIFY 同义，默认开。
	// 用指针是为了区分「配置里没有这个字段」（nil，按默认开）和「用户主动
	// 关掉」（false）—— 普通 bool 的零值会把「老配置里没这个字段」误判成
	// 「用户关了通知」，于是新功能在已登录用户那里静默失效。
	YukiHubAccountFriendPlayNotify *bool `json:"yukihub_account_friend_play_notify,omitempty"`
	// LastYukiHubAccountSyncHash 是上次同步的快照哈希，用于判断两边有没有改动。
	LastYukiHubAccountSyncHash string `json:"last_yukihub_account_sync_hash,omitempty"`
	LastYukiHubAccountSyncAt   string `json:"last_yukihub_account_sync_at,omitempty"`

	// OneDrive OAuth 配置
	OneDriveClientID     string `json:"onedrive_client_id,omitempty"`     // OneDrive Client ID
	OneDriveRefreshToken string `json:"onedrive_refresh_token,omitempty"` // OneDrive Refresh Token（OAuth 授权后获得）
	// WebDAV 配置（游戏存档 / 数据库备份用的云存储）
	WebDAVURL      string `json:"webdav_url,omitempty"`      // WebDAV 服务地址（可含子路径）
	WebDAVUsername string `json:"webdav_username,omitempty"` // WebDAV 用户名
	WebDAVPassword string `json:"webdav_password,omitempty"` // WebDAV 密码

	// 自持同步（WebDAV）：把与手机版**完全相同**的 schema 5 快照同步到用户自己的
	// WebDAV 网盘，对应手机版 SyncManager 的 `sync()`（云端文件 YukiHub/YukiHub_sync.json）。
	//
	// 与上面那组 WebDAV 配置是两回事：那组是「游戏存档备份」的云存储后端（LunaBox 血统），
	// 这组是「游戏库整体同步」的传输通道（手机版血统）。两者互不影响，可以只配一个。
	// 密码同样明文存 appconf.json —— 与手机版存 SharedPreferences、以及本文件其它凭据一致。
	SelfSyncURL      string `json:"self_sync_url,omitempty"`      // WebDAV 服务地址
	SelfSyncUsername string `json:"self_sync_username,omitempty"` // WebDAV 用户名
	SelfSyncPassword string `json:"self_sync_password,omitempty"` // WebDAV 密码 / 应用密码
	// SelfSyncAutoSync 对应手机版的「自动同步」开关（启动时同步一次）。
	SelfSyncAutoSync bool `json:"self_sync_auto_sync"`
	// SelfSyncLastHash 是上次同步时本地快照的哈希，用于判断两边有没有改动。
	SelfSyncLastHash string `json:"self_sync_last_hash,omitempty"`
	SelfSyncLastAt   string `json:"self_sync_last_at,omitempty"`
	// Umbra OAuth 配置（token 与设备密钥由 DPAPI 加密存储，不写入配置文件）
	UmbraBaseURL       string `json:"umbra_base_url,omitempty"`      // Umbra 服务地址
	UmbraAuthenticated bool   `json:"umbra_authenticated,omitempty"` // 是否已完成 OAuth 与设备注册
	// 数据库备份
	LastDBBackupTime   string `json:"last_db_backup_time,omitempty"`   // 上次数据库备份时间
	PendingDBRestore   string `json:"pending_db_restore,omitempty"`    // 待恢复的数据库备份路径（重启后执行）
	LastFullBackupTime string `json:"last_full_backup_time,omitempty"` // 上次全量数据备份时间
	PendingFullRestore string `json:"pending_full_restore,omitempty"`  // 待恢复的全量数据备份路径（重启后执行）
	// 自动备份配置
	AutoBackupDB          bool `json:"auto_backup_db"`                 // 退出时自动备份数据库
	AutoBackupGameSave    bool `json:"auto_backup_game_save"`          // 游戏退出时自动备份存档
	AutoUploadDBToCloud   bool `json:"auto_upload_db_to_cloud"`        // 自动上传数据库备份到云端
	AutoUploadSaveToCloud bool `json:"auto_upload_game_save_to_cloud"` // 自动上传游戏存档备份到云端

	AutoRestoreCloudSave             bool   `json:"auto_restore_cloud_save_before_launch"` // 启动游戏前自动恢复较新的云端存档
	ScheduledDBBackupEnabled         bool   `json:"scheduled_db_backup_enabled"`           // 是否启用定时数据库备份
	ScheduledDBBackupMode            string `json:"scheduled_db_backup_mode,omitempty"`    // interval / daily
	ScheduledDBBackupIntervalMinutes int    `json:"scheduled_db_backup_interval_minutes"`  // 固定间隔分钟数
	ScheduledDBBackupTime            string `json:"scheduled_db_backup_time,omitempty"`    // 每日备份时间 HH:mm
	// 备份保留策略
	LocalBackupRetention   int `json:"local_backup_retention"`    // 本地游戏备份保留数量
	LocalDBBackupRetention int `json:"local_db_backup_retention"` // 本地数据库备份保留数量
	// 窗口尺寸记忆
	WindowWidth      int     `json:"window_width"`       // 窗口宽度
	WindowHeight     int     `json:"window_height"`      // 窗口高度
	WindowMaximised  bool    `json:"window_maximised"`   // 窗口是否最大化
	WindowZoomFactor float64 `json:"window_zoom_factor"` // 应用界面缩放倍率
	LaunchAtLogin    bool    `json:"launch_at_login"`    // Windows 登录后自动启动应用
	// 活跃时间追踪配置
	RecordActiveTimeOnly       bool `json:"record_active_time_only"`       // 仅记录活跃游玩时长（窗口在前台时）
	MuteGameInBackground       bool `json:"mute_game_in_background"`       // 游戏窗口进入后台时静音
	ProcessDetectionTimeoutSec int  `json:"process_detection_timeout_sec"` // 启动后检测实际游戏进程的最长等待时间
	// PlayTimingMode 是全局计时模式：process（进程监测，默认）/ manual（手动计时）。
	// 空串（老配置里没有这个字段）按默认处理，见 NormalizePlayTimingMode。
	PlayTimingMode string `json:"play_timing_mode,omitempty"`
	// 自动更新配置
	CheckUpdateOnStartup bool   `json:"check_update_on_startup"`     // 启动时自动检查更新
	UpdateSource         string `json:"update_source,omitempty"`     // 更新源：gitcode（默认）/ github
	UpdateCheckURL       string `json:"update_check_url,omitempty"`  // 自定义更新检查 URL
	LastUpdateCheck      string `json:"last_update_check,omitempty"` // 上次更新检查时间
	SkipVersion          string `json:"skip_version,omitempty"`      // 跳过的版本号（用户选择忽略的更新）
	// 背景图配置
	BackgroundImage             string  `json:"background_image,omitempty"`      // 自定义背景图路径
	BackgroundBlur              int     `json:"background_blur"`                 // 背景模糊度 (0-20)
	BackgroundOpacity           float64 `json:"background_opacity"`              // 背景不透明度 (0-1)
	BackgroundEnabled           bool    `json:"background_enabled"`              // 是否启用自定义背景
	BackgroundHideGameCover     bool    `json:"background_hide_game_cover"`      // 启用自定义背景时隐藏首页游戏封面
	BackgroundHideGameHeroCover bool    `json:"background_hide_game_hero_cover"` // 启用自定义背景时隐藏首页游戏封面大图
	BackgroundIsLight           bool    `json:"background_is_light"`             // 记录自定义背景是不是浅色调
	HomeGameCarouselEnabled     bool    `json:"home_game_carousel_enabled"`      // 首页游戏封面是否自动轮播
	HomeGameCarouselIntervalSec int     `json:"home_game_carousel_interval_sec"` // 首页游戏封面轮播间隔（秒）
	// Locale Emulator 和 Magpie 配置
	LocaleEmulatorPath       string `json:"locale_emulator_path,omitempty"`  // Locale Emulator 可执行文件路径
	MagpiePath               string `json:"magpie_path,omitempty"`           // Magpie 可执行文件路径
	DefaultUseLocaleEmulator bool   `json:"default_use_locale_emulator"`     // 新添加的游戏默认启用 Locale Emulator
	DefaultUseMagpie         bool   `json:"default_use_magpie"`              // 新添加的游戏默认启用 Magpie
	WineRunnerPath           string `json:"wine_runner_path,omitempty"`      // Linux Wine 可执行文件路径
	WinePrefix               string `json:"wine_prefix,omitempty"`           // Linux 默认 WINEPREFIX 或 Proton prefix
	WinetricksPath           string `json:"winetricks_path,omitempty"`       // Linux winetricks 可执行文件路径
	ProtontricksPath         string `json:"protontricks_path,omitempty"`     // Linux protontricks 可执行文件路径
	CrossOverRunnerPath      string `json:"crossover_runner_path,omitempty"` // 历史字段：CrossOver bundle 内的 wine 可执行文件路径
	CrossOverBottle          string `json:"crossover_bottle,omitempty"`      // 历史字段：CrossOver bottle 名
	// 时区配置
	TimeZone string `json:"time_zone,omitempty"` // 数据库使用的 IANA 时区名称（如 "Asia/Shanghai"）
	// 游戏库路径配置
	GameLibraryPath string `json:"game_library_path,omitempty"` // 游戏库主目录（下载的游戏将解压到此）
	// 批量导入偏好
	BatchImportScanPreset      string `json:"batch_import_scan_preset,omitempty"` // 批量导入扫描预设
	BatchImportHierarchyDepth  int    `json:"batch_import_hierarchy_depth"`       // 批量导入按目录导入层级
	BatchImportPreferredSource string `json:"batch_import_preferred_source,omitempty"`
	BatchImportLastDirectory   string `json:"batch_import_last_directory,omitempty"` // 批量导入目录选择器上次打开的目录
	// 网络代理配置
	NetworkProxyMode string `json:"network_proxy_mode,omitempty"` // 全局网络代理模式：system / manual / direct
	NetworkProxyURL  string `json:"network_proxy_url,omitempty"`  // 全局手动代理 URL
	// Tag 配置
	ShowNSFWTags         bool `json:"show_nsfw_tags"`         // 是否在详情页展示 NSFW tag，默认 false
	EnableTagTranslation bool `json:"enable_tag_translation"` // 是否显示 VNDB tag 中文翻译，默认 true
	ScrapedTagLimit      int  `json:"scraped_tag_limit"`      // 刮削 tag 数量上限，-1 表示不限制，0 表示不刮削 tag
	// 游戏卡片显示
	GameCardLayout       string `json:"game_card_layout,omitempty"` // 游戏库卡片布局：portrait / landscape
	ShowSortFieldOnCover bool   `json:"show_sort_field_on_cover"`   // 是否在游戏卡片封面底部展示当前排序字段对应的值
	BlurNSFWGameCovers   bool   `json:"blur_nsfw_game_covers"`      // 是否模糊 NSFW 游戏封面
	// 大屏模式配置（对齐手机端 BigScreenPrefs 的 bigscreen_* 键）
	BigScreenShowHiddenGame  bool   `json:"bigscreen_show_hidden_game"`           // 大屏模式是否展示已隐藏的游戏，默认 false
	BigScreenDefaultCategory string `json:"bigscreen_default_category,omitempty"` // 大屏模式默认分类，默认 recent
	BigScreenEffectLevel     string `json:"bigscreen_effect_level,omitempty"`     // 大屏氛围特效档位：off / low / high，默认 low
	BigScreenSoundEnabled    bool   `json:"bigscreen_sound_enabled"`              // 大屏界面音效开关，默认 true
	BigScreenSoundVolume     int    `json:"bigscreen_sound_volume"`               // 大屏界面音效音量（0-100），默认 65
	BigScreenFocusTicks      bool   `json:"bigscreen_focus_ticks"`                // 焦点移动音，默认 true
	BigScreenIntroEnabled    bool   `json:"bigscreen_intro_enabled"`              // 入场动画，默认 true
	BigScreenIntroVideo      string `json:"bigscreen_intro_video,omitempty"`      // 自选入场视频（/local/intro/...）；空 = 内置动画（对齐手机端 M18-2）
	BigScreenShowTitles      bool   `json:"bigscreen_show_titles"`                // 卡片上再显示游戏名（默认 false，名字已在信息浮层）
	BigScreenCardScale       int    `json:"bigscreen_card_scale"`                 // 卡片大小倍率（×100），默认 112
	BigScreenFocusScale      int    `json:"bigscreen_focus_scale"`                // 焦点缩放幅度（%），0 表示只描边，默认 100
	BigScreenKeyStyle        string `json:"bigscreen_key_style,omitempty"`        // 按键图标风格：xbox / ps，默认 xbox
	BigScreenHintMode        string `json:"bigscreen_hint_mode,omitempty"`        // 按键提示条：auto（4s 后淡出）/ always / off，默认 auto
	BigScreenRailExpanded    bool   `json:"bigscreen_rail_expanded"`              // 侧栏钉住展开，默认 false
	BigScreenTrailerEnabled  bool   `json:"bigscreen_trailer_enabled"`            // 背景预告片总开关，默认 true
	BigScreenTrailerMuted    bool   `json:"bigscreen_trailer_muted"`              // 预告片静音，默认 false
	BigScreenTrailerDelayMs  int    `json:"bigscreen_trailer_delay_ms"`           // 焦点停留多久后起播预告片（毫秒），默认 2000
	BigScreenPVFit           bool   `json:"bigscreen_pv_fit"`                     // 预告片显示方式：false=铺满裁切 / true=原比例留黑边，默认 false
	BigScreenPVScrim         bool   `json:"bigscreen_pv_scrim"`                   // 预告片遮罩，默认 true
	BigScreenPVScrimPercent  int    `json:"bigscreen_pv_scrim_percent"`           // 预告片遮罩强度（0-100），默认 45
	BigScreenBannerHoldMs    int    `json:"bigscreen_banner_hold_ms"`             // 顶部提示条停留时长（毫秒），默认 2000
	BigScreenSnowEnabled     bool   `json:"bigscreen_snow_enabled"`               // 背景氛围层（雪花 / 极光），默认 true
	// 只在详情层播放预告片（对齐手机端 bigscreen_trailer_details_only，中性能档默认行为）
	BigScreenTrailerDetailsOnly bool   `json:"bigscreen_trailer_details_only"`
	BigScreenRememberFilter     bool   `json:"bigscreen_remember_filter"`         // 记住上次的分类筛选，默认 true
	BigScreenLastCategory       string `json:"bigscreen_last_category,omitempty"` // 上次停留的分类（仅在记住筛选打开时写入）

	// OverlayShortcut 是「呼出游戏内好友栏」的全局快捷键，accelerator 形式
	// （如 "shift+`"）。空字符串表示用默认值（service.DefaultOverlayShortcut）；
	// 界面展示成 “Shift + ~”，转换在 service.FormatOverlayShortcut。
	OverlayShortcut string `json:"overlay_shortcut,omitempty"`
}

// getConfigPath 获取配置文件路径
func getConfigPath() (string, error) {
	configDir, err := apputils.GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "appconf.json"), nil
}

// defaultAppConfig 构建一份全新的默认配置（不读盘、不落盘）。
//
// 单独抽出来是为了让配置恢复路径（主文件与 .bak 都损坏时）能拿到一份干净的默认值。
func defaultAppConfig() *AppConfig {
	config := &AppConfig{
		BangumiAccessToken:            "",
		BangumiRefreshToken:           "",
		BangumiTokenExpiresAt:         "",
		BangumiAuthorizedUserID:       "",
		BangumiAuthorizedUsername:     "",
		BangumiAuthorizedAvatarURL:    "",
		BangumiAuthError:              "",
		BangumiStatusPushEnabled:      boolPtr(true),
		HikarinagiAccessToken:         "",
		HikarinagiRefreshToken:        "",
		HikarinagiTokenExpiresAt:      "",
		HikarinagiAuthorizedUserID:    "",
		HikarinagiAuthorizedUsername:  "",
		HikarinagiAuthorizedAvatarURL: "",
		HikarinagiAuthError:           "",
		HikarinagiStatusPushEnabled:   boolPtr(true),
		NextMoeAccessToken:            "",
		NextMoeRefreshToken:           "",
		NextMoeTokenExpiresAt:         "",
		NextMoeAccountLabel:           "",
		VNDBAccessToken:               "",
		MetadataSources:               cloneStringSlice(defaultMetadataSources),
		CurrentMetadataSource:         DefaultCurrentMetadataSource,
		AllowDuplicateMetadataImport:  false,
		BangumiCoverSource:            enums2.MetadataCoverSourceHikarinagi,
		VNDBCoverSource:               enums2.MetadataCoverSourceHikarinagi,
		Theme:                         "light",
		Language:                      "zh-CN",
		SidebarOpen:                   true,
		CloseToTray:                   false,
		AIProvider:                    "",
		AIBaseURL:                     "",
		AIAPIKey:                      "",
		AIModel:                       "",
		AISystemPrompt:                string(enums2.DefaultSystemPrompt),
		MCPEnabled:                    false,
		MCPPort:                       DefaultMCPPort,
		CloudBackupEnabled:            false,
		CloudBackupProvider:           "webdav",
		BackupPassword:                "",
		BackupUserID:                  "",
		CloudSyncEnabled:              false,
		AutoCloudSyncEnabled:          false,
		CloudSyncIntervalSec:          60,
		LastCloudSyncTime:             "",
		LastCloudSyncStatus:           "idle",
		LastCloudSyncError:            "",
		S3Endpoint:                    "",
		TimeZone:                      "",
		S3Region:                      "",
		S3Bucket:                      "",
		S3AccessKey:                   "",
		S3SecretKey:                   "",
		CloudBackupRetention:          5,
		OneDriveClientID:              "",
		OneDriveRefreshToken:          "",
		WebDAVURL:                     "",
		WebDAVUsername:                "",
		WebDAVPassword:                "",
		SelfSyncURL:                   "",
		SelfSyncUsername:              "",
		SelfSyncPassword:              "",
		SelfSyncAutoSync:              false,
		SelfSyncLastHash:              "",
		SelfSyncLastAt:                "",
		UmbraBaseURL:                  DefaultUmbraBaseURL,
		UmbraAuthenticated:            false,
		LastDBBackupTime:              "",
		PendingDBRestore:              "",
		LastFullBackupTime:            "",
		PendingFullRestore:            "",
		AutoBackupDB:                  false,
		AutoBackupGameSave:            false,

		CloudDBBackupRetention:           5,
		AutoRestoreCloudSave:             false,
		ScheduledDBBackupEnabled:         false,
		ScheduledDBBackupMode:            ScheduledDBBackupModeInterval,
		ScheduledDBBackupIntervalMinutes: DefaultScheduledDBBackupIntervalMinutes,
		ScheduledDBBackupTime:            DefaultScheduledDBBackupTime,

		LocalBackupRetention:       10,
		LocalDBBackupRetention:     DefaultLocalDBBackupRetention,
		WindowWidth:                1230,
		WindowHeight:               800,
		WindowMaximised:            false,
		WindowZoomFactor:           1.0,
		LaunchAtLogin:              false,
		RecordActiveTimeOnly:       false, // 默认关闭，向后兼容
		MuteGameInBackground:       false,
		ProcessDetectionTimeoutSec: DefaultProcessDetectionTimeoutSec,
		PlayTimingMode:             DefaultPlayTimingMode,
		CheckUpdateOnStartup:       true,      // 默认开启启动时检查更新
		UpdateSource:               "gitcode", // 默认 GitCode（与手机版一致），可在设置里切换 GitHub
		UpdateCheckURL:             "",
		LastUpdateCheck:            "",
		SkipVersion:                "",
		// 背景图配置默认值
		BackgroundImage:             "",
		BackgroundBlur:              10,   // 默认模糊度
		BackgroundOpacity:           0.85, // 默认不透明度
		BackgroundEnabled:           false,
		BackgroundHideGameCover:     false, // 默认显示游戏封面
		BackgroundHideGameHeroCover: false, // 默认显示首页游戏封面大图
		BackgroundIsLight:           true,  // 默认是浅色调
		HomeGameCarouselEnabled:     true,
		HomeGameCarouselIntervalSec: DefaultHomeGameCarouselIntervalSec,
		LocaleEmulatorPath:          "",
		MagpiePath:                  "",
		DefaultUseLocaleEmulator:    false,
		DefaultUseMagpie:            false,
		WineRunnerPath:              "",
		WinePrefix:                  "",
		WinetricksPath:              "",
		ProtontricksPath:            "",
		CrossOverRunnerPath:         "",
		CrossOverBottle:             "",
		GameLibraryPath:             "",
		BatchImportScanPreset:       DefaultBatchImportScanPreset,
		BatchImportHierarchyDepth:   0,
		BatchImportPreferredSource:  "",
		BatchImportLastDirectory:    "",
		NetworkProxyMode:            "system",
		NetworkProxyURL:             "",
		EnableTagTranslation:        true,
		ScrapedTagLimit:             DefaultScrapedTagLimit,
		GameCardLayout:              DefaultGameCardLayout,
		ShowSortFieldOnCover:        false,
		BlurNSFWGameCovers:          true,
		BigScreenShowHiddenGame:     false,
		BigScreenDefaultCategory:    DefaultBigScreenDefaultCategory,
		BigScreenEffectLevel:        DefaultBigScreenEffectLevel,
		BigScreenSoundEnabled:       true,
		BigScreenSoundVolume:        DefaultBigScreenSoundVolume,
		BigScreenFocusTicks:         true,
		BigScreenIntroEnabled:       true,
		BigScreenShowTitles:         false,
		BigScreenCardScale:          DefaultBigScreenCardScale,
		BigScreenFocusScale:         DefaultBigScreenFocusScale,
		BigScreenKeyStyle:           DefaultBigScreenKeyStyle,
		BigScreenHintMode:           DefaultBigScreenHintMode,
		BigScreenRailExpanded:       false,
		BigScreenTrailerEnabled:     true,
		BigScreenTrailerMuted:       false,
		BigScreenTrailerDelayMs:     DefaultBigScreenTrailerDelayMs,
		BigScreenPVFit:              false,
		BigScreenPVScrim:            true,
		BigScreenPVScrimPercent:     DefaultBigScreenPVScrimPercent,
		BigScreenBannerHoldMs:       DefaultBigScreenBannerHoldMs,
		BigScreenSnowEnabled:        true,
		BigScreenTrailerDetailsOnly: false,
		BigScreenRememberFilter:     true,
	}
	return config
}

func LoadConfig() (*AppConfig, error) {
	config := defaultAppConfig()

	// 获取配置文件路径
	configPath, err := getConfigPath()
	if err != nil {
		return config, err
	}

	// 检查配置文件是否存在
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		err := SaveConfig(config)
		return config, err
	}

	// 读取配置文件。读失败（被杀毒/索引器短暂独占等）也先试快照，别让应用起不来。
	loadOutcome := configLoadPrimary
	data, err := os.ReadFile(configPath)
	if err != nil {
		backupData, backupErr := os.ReadFile(configPath + configBackupSuffix)
		if backupErr != nil {
			return config, err
		}
		loadOutcome, config = applyConfigFallback(backupData, config, err, configPath+configBackupSuffix)
	} else {
		var parsed *AppConfig
		loadOutcome, parsed = parseConfigWithBackup(data, configPath, config)
		*config = *parsed
	}
	config.MetadataSources = normalizeMetadataSources(config.MetadataSources)
	NormalizeMetadataCoverSources(config)
	NormalizeCurrentMetadataSource(config)

	if config.WindowZoomFactor <= 0 {
		config.WindowZoomFactor = 1.0
	}

	if config.CloudSyncIntervalSec <= 0 {
		config.CloudSyncIntervalSec = 60
	}

	config.MCPPort = NormalizeMCPPort(config.MCPPort)
	config.ScrapedTagLimit = NormalizeScrapedTagLimit(config.ScrapedTagLimit)
	config.HomeGameCarouselIntervalSec = NormalizeHomeGameCarouselIntervalSec(config.HomeGameCarouselIntervalSec)
	config.ProcessDetectionTimeoutSec = NormalizeProcessDetectionTimeoutSec(config.ProcessDetectionTimeoutSec)
	config.PlayTimingMode = NormalizePlayTimingMode(config.PlayTimingMode)
	config.GameCardLayout = NormalizeGameCardLayout(config.GameCardLayout)
	NormalizeBigScreenPreferences(config)
	NormalizeBatchImportPreferences(config)

	// 只有「从快照恢复」才回写主文件（顺便修好被截断的 appconf.json）。
	// 退到默认配置时**不**回写：那会把损坏原文顶掉，现场证据都没了。
	shouldSaveSanitizedConfig := loadOutcome == configLoadRecoveredFromBackup
	if normalizedRetention := NormalizeLocalDBBackupRetention(config.LocalDBBackupRetention); config.LocalDBBackupRetention != normalizedRetention {
		config.LocalDBBackupRetention = normalizedRetention
		shouldSaveSanitizedConfig = true
	}
	if NormalizeScheduledDBBackup(config) {
		shouldSaveSanitizedConfig = true
	}
	if SanitizeBangumiOAuthConfig(config) {
		shouldSaveSanitizedConfig = true
	}
	if SanitizeHikarinagiOAuthConfig(config) {
		shouldSaveSanitizedConfig = true
	}
	if SanitizeNextMoeOAuthConfig(config) {
		shouldSaveSanitizedConfig = true
	}
	if MigrateLegacyCompatibilityConfig(config) {
		shouldSaveSanitizedConfig = true
	}
	if detectDefaultCrossOverRunnerPath(config) {
		shouldSaveSanitizedConfig = true
	}
	if detectDefaultWineRunnerPath(config) {
		shouldSaveSanitizedConfig = true
	}
	if NormalizeProxySettings(config) {
		shouldSaveSanitizedConfig = true
	}
	if SanitizeOneDriveOAuthConfig(config) {
		shouldSaveSanitizedConfig = true
	}
	if SanitizeUmbraConfig(config) {
		shouldSaveSanitizedConfig = true
	}

	// 备份口令只在初始化时使用，不应长期明文落盘。
	if config.BackupPassword != "" {
		if config.BackupUserID == "" {
			config.BackupUserID = utils.GenerateUserID(config.BackupPassword)
		}
		config.BackupPassword = ""
		shouldSaveSanitizedConfig = true
	}

	if shouldSaveSanitizedConfig {
		if err := SaveConfig(config); err != nil {
			log.Printf("Failed to save sanitized backup config: %v", err)
		}
	}

	return config, err
}

// MigrateLegacyCompatibilityConfig splits the previous shared Wine/CrossOver
// fields when the configured runner clearly belongs to CrossOver.
func MigrateLegacyCompatibilityConfig(config *AppConfig) bool {
	if config == nil {
		return false
	}

	winePath := strings.TrimSpace(config.WineRunnerPath)
	if winePath == "" || strings.TrimSpace(config.CrossOverRunnerPath) != "" {
		return false
	}
	normalizedPath := strings.ToLower(filepath.ToSlash(winePath))
	if !strings.Contains(normalizedPath, "/crossover.app/") {
		return false
	}

	config.CrossOverRunnerPath = winePath
	config.WineRunnerPath = ""
	if strings.TrimSpace(config.CrossOverBottle) == "" {
		config.CrossOverBottle = strings.TrimSpace(config.WinePrefix)
		config.WinePrefix = ""
	}
	return true
}

func SaveConfig(config *AppConfig) error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}
	config.MetadataSources = normalizeMetadataSources(config.MetadataSources)
	NormalizeMetadataCoverSources(config)
	NormalizeCurrentMetadataSource(config)
	NormalizeProxySettings(config)
	SanitizeBangumiOAuthConfig(config)
	SanitizeHikarinagiOAuthConfig(config)
	SanitizeNextMoeOAuthConfig(config)
	SanitizeOneDriveOAuthConfig(config)
	SanitizeUmbraConfig(config)
	config.MCPPort = NormalizeMCPPort(config.MCPPort)
	config.ScrapedTagLimit = NormalizeScrapedTagLimit(config.ScrapedTagLimit)
	config.HomeGameCarouselIntervalSec = NormalizeHomeGameCarouselIntervalSec(config.HomeGameCarouselIntervalSec)
	config.ProcessDetectionTimeoutSec = NormalizeProcessDetectionTimeoutSec(config.ProcessDetectionTimeoutSec)
	config.PlayTimingMode = NormalizePlayTimingMode(config.PlayTimingMode)
	config.GameCardLayout = NormalizeGameCardLayout(config.GameCardLayout)
	NormalizeBigScreenPreferences(config)
	NormalizeBatchImportPreferences(config)
	config.LocalDBBackupRetention = NormalizeLocalDBBackupRetention(config.LocalDBBackupRetention)
	NormalizeScheduledDBBackup(config)
	configCopy := *config
	configCopy.BackupPassword = ""
	data, err := json.MarshalIndent(&configCopy, "", "  ")
	if err != nil {
		return err
	}

	configFileMu.Lock()
	defer configFileMu.Unlock()

	// 保留一份有效快照用于恢复。快照有效时后续保存不再重写，只替换主文件。
	ensureConfigBackup(configPath)

	// 先写同目录临时文件并 fsync，再原子替换主文件。写入中途失败不会把
	// appconf.json 截断成半截 JSON——这是老实现最容易丢配置的地方。
	if err := writeFileAtomic(configPath, data, 0644); err != nil {
		return fmt.Errorf("write app config atomically: %w", err)
	}
	// 全新安装或刚从备份恢复时还没有快照，主文件写好后再补一份。
	ensureConfigBackup(configPath)
	return nil
}

// configLoadOutcome 说明这次配置是从哪儿来的，决定要不要回写主文件。
type configLoadOutcome int

const (
	// configLoadPrimary：主文件本身可用。
	configLoadPrimary configLoadOutcome = iota
	// configLoadRecoveredFromBackup：主文件坏了，用快照恢复。应当回写修好主文件。
	configLoadRecoveredFromBackup
	// configLoadFellBackToDefaults：主文件和快照都不可用。**不要**回写 ——
	// 损坏原文已另存为 .corrupt，那是唯一的现场证据。
	configLoadFellBackToDefaults
)

// applyConfigFallback 主文件读不了时的回退决策：快照可用就用快照；
// 快照也解析不了只能用默认值，且**不回写** —— 主文件这次只是读不到，
// 不代表它坏了，覆盖掉可能毁掉唯一一份可用配置。
func applyConfigFallback(backupData []byte, defaults *AppConfig, readErr error, backupPath string) (configLoadOutcome, *AppConfig) {
	if parsed := parseConfigBytes(backupData, defaults); parsed != nil {
		log.Printf("failed to read appconf (%v), falling back to %s", readErr, backupPath)
		return configLoadRecoveredFromBackup, parsed
	}
	log.Printf("failed to read appconf (%v) and %s is unusable; using defaults without writing back", readErr, backupPath)
	return configLoadFellBackToDefaults, defaultAppConfig()
}

// parseConfigBytes 把一段 JSON 解析进 defaults 的副本，剥掉 UTF-8 BOM。
//
// 只认顶层是 JSON 对象的输入：`null` 会被 json.Unmarshal 静默接受且不改动任何
// 字段，若放过去就等于「配置凭空变成默认值」。
func parseConfigBytes(data []byte, defaults *AppConfig) *AppConfig {
	parsed := *defaults
	parsed.MetadataSources = cloneStringSlice(defaults.MetadataSources)
	trimmed := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return nil
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &shape); err != nil || shape == nil {
		return nil
	}
	return &parsed
}

// isUsableConfigJSON 判断一段内容能不能当配置快照：必须是顶层 JSON 对象。
func isUsableConfigJSON(data []byte) bool {
	var shape map[string]json.RawMessage
	trimmed := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(trimmed, &shape); err != nil {
		return false
	}
	return shape != nil
}

// parseConfigWithBackup 解析 appconf 主文件；失败时依次回退 .bak 快照、默认配置。
//
// 无论用哪条路径，损坏的原文都会先被另存为 appconf.json.corrupt —— 之前这里
// 直接用默认值覆盖，是把用户唯一一份现场证据也一起毁掉。
func parseConfigWithBackup(data []byte, configPath string, defaults *AppConfig) (configLoadOutcome, *AppConfig) {
	if parsed := parseConfigBytes(data, defaults); parsed != nil {
		// 每次启动刷新一次恢复快照；SaveConfig 在高频保存时会保持该快照稳定。
		configFileMu.Lock()
		writeConfigBackup(configPath, data)
		configFileMu.Unlock()
		return configLoadPrimary, parsed
	}

	primaryErr := errors.New("appconf is not a JSON object")
	preserveCorruptConfig(configPath, data)

	backupPath := configPath + configBackupSuffix
	if backupData, backupReadErr := os.ReadFile(backupPath); backupReadErr == nil {
		if parsed := parseConfigBytes(backupData, defaults); parsed != nil {
			log.Printf("appconf is invalid (%v), recovered from %s", primaryErr, backupPath)
			return configLoadRecoveredFromBackup, parsed
		}
	}
	log.Printf("appconf is unusable (%v) and no valid %s; using defaults", primaryErr, backupPath)
	return configLoadFellBackToDefaults, defaultAppConfig()
}

// preserveCorruptConfig 把损坏原文挪到 .corrupt，保证它不会被后续保存覆盖掉。
func preserveCorruptConfig(configPath string, data []byte) {
	corruptPath := configPath + configCorruptSuffix
	if existing, err := os.ReadFile(corruptPath); err == nil && bytes.Equal(existing, data) {
		return
	}
	if err := os.WriteFile(corruptPath, data, 0644); err != nil {
		log.Printf("failed to preserve corrupt appconf: %v", err)
	}
}

// ensureConfigBackup 保证存在一份「可解析的」配置快照。
//
// 快照只在缺失或损坏时从当前主文件重建，因此高频保存不会反复写 .bak，
// 也就不会在主文件刚被写坏前把好快照覆盖掉。
func ensureConfigBackup(configPath string) {
	backupPath := configPath + configBackupSuffix
	if backup, err := os.ReadFile(backupPath); err == nil && isUsableConfigJSON(backup) {
		return
	}

	previous, err := os.ReadFile(configPath)
	if err != nil || !isUsableConfigJSON(previous) {
		return
	}
	writeConfigBackup(configPath, previous)
}

func writeConfigBackup(configPath string, data []byte) {
	if !isUsableConfigJSON(data) {
		return
	}
	if err := writeFileAtomic(configPath+configBackupSuffix, data, 0644); err != nil {
		log.Printf("failed to create appconf backup: %v", err)
	}
}

// writeFileAtomic 原子写入文件：同目录临时文件 → fsync → 替换目标。
//
// 不引入第三方依赖（上游用 natefinch/atomic）；Windows 上 os.Rename 走
// MoveFileEx(MOVEFILE_REPLACE_EXISTING)，可以直接覆盖已存在的目标文件。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".appconf-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	discard := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		discard()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		discard()
		return err
	}
	if err := tmp.Sync(); err != nil {
		discard()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func IsBangumiStatusPushEnabled(config *AppConfig) bool {
	if config == nil || config.BangumiStatusPushEnabled == nil {
		return true
	}

	return *config.BangumiStatusPushEnabled
}

func IsHikarinagiStatusPushEnabled(config *AppConfig) bool {
	if config == nil || config.HikarinagiStatusPushEnabled == nil {
		return true
	}

	return *config.HikarinagiStatusPushEnabled
}

func (config *AppConfig) NetworkProxyConfig() (string, string) {
	if config == nil {
		return proxyutils.ProxyModeSystem, ""
	}
	return config.NetworkProxyMode, config.NetworkProxyURL
}

// CoverSourcePreference 返回 Bangumi / VNDB 两个来源各自取用的封面源。
//
// 放在这里是为了让 utils 层（图片下载、图片代理）能在不反向依赖 appconf 的
// 前提下读到设置，见 imageutils.CoverSourcePreferenceProvider 与
// coverutils.ResolveURL。
func (config *AppConfig) CoverSourcePreference() coverutils.Preference {
	if config == nil {
		return coverutils.OriginalPreference
	}
	return coverutils.Preference{
		Bangumi: config.BangumiCoverSource,
		VNDB:    config.VNDBCoverSource,
	}
}
