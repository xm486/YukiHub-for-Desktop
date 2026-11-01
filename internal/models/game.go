package models

import (
	"time"
	"yukihub/internal/common/enums"
)

type Game struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	Aliases            []string             `json:"aliases"`
	CoverURL           string               `json:"cover_url"`
	CoverSourceURL     string               `json:"cover_source_url"` // 刮削封面的远程源地址，本地封面不可用时用于回退
	Company            string               `json:"company"`
	Summary            string               `json:"summary"`
	Rating             float64              `json:"rating"`            // 游戏评分（统一按 10 分制存储）
	ReleaseDate        string               `json:"release_date"`      // 发售日期（源站原始日期字符串）
	Path               string               `json:"path"`              // 启动路径
	GameDirectory      string               `json:"game_directory"`    // 游戏根目录，不一定是启动文件的上一级
	SavePath           string               `json:"save_path"`         // 存档目录路径
	ProcessName        string               `json:"process_name"`      // 实际监控的进程名（当启动器和游戏进程不同时使用）
	WineRunner         string               `json:"wine_runner"`       // macOS/Linux：兼容层类型（system/crossover/custom）
	WineArgs           string               `json:"wine_args"`         // macOS/Linux：追加给 Wine/Proton 的启动参数
	WinePrefix         string               `json:"wine_prefix"`       // macOS/Linux：WINEPREFIX、CrossOver bottle 或 Proton prefix
	LaunchMode         enums.LaunchMode     `json:"launch_mode"`       // 启动方式: normal, admin, steam, compatibility
	SteamLaunchID      string               `json:"steam_launch_id"`   // 本机 Steam 启动标识：原生 AppID 或非 Steam 快捷方式的长 ID
	SteamLaunchKind    string               `json:"steam_launch_kind"` // 本机关联类型：native 或 shortcut
	SteamUserID        string               `json:"steam_user_id"`     // 非 Steam 快捷方式所属的 Steam account ID
	SteamLaunchOptions string               `json:"steam_launch_options"`
	Status             enums.GameStatus     `json:"status"`      // 游戏状态: unplayed, playing, completed, onhold, dropped
	SourceType         enums.SourceType     `json:"source_type"` // 默认元数据来源
	MetadataSources    []GameMetadataSource `json:"metadata_sources"`
	CachedAt           time.Time            `json:"cached_at"`
	SourceID           string               `json:"source_id"` // 默认元数据来源 ID
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
	UseLocaleEmulator  bool                 `json:"use_locale_emulator"`         // 是否使用 Locale Emulator 转区启动
	UseMagpie          bool                 `json:"use_magpie"`                  // 是否使用 Magpie 超分辨率缩放
	IsNSFW             bool                 `json:"is_nsfw"`                     // 是否为 NSFW 游戏
	MetadataLocked     bool                 `json:"metadata_locked"`             // 是否锁定远程元数据更新
	LegacyLocalID      string               `json:"legacy_local_id"`             // 手机版 YukiHub 的整数 local_id，用于回写与去重，不替代主键
	LegacyRootURI      string               `json:"legacy_root_uri"`             // 手机版 YukiHub 的 root_uri 原始值（跨端身份键），只回写不参与本机路径逻辑
	SourceDeviceID     string               `json:"source_device_id"`            // 条目来源设备，避免多设备互相覆盖
	PlaytimeResetAt    *time.Time           `json:"playtime_reset_at,omitempty"` // 清零时间点：该时间之前的历史会话不计入统计，记录本身保留
	Hidden             bool                 `json:"hidden"`                      // 手机版 YukiHub 的隐藏标记
	LastPlayedAt       *time.Time           `json:"last_played_at,omitempty"`    // 最近一次游玩开始时间（由 play_sessions 聚合）
	TrailerPath        string               `json:"trailer_path"`                // 本地预告片视频，/local/trailers/... 或空；依 mobile-yukihub-migration.md 不入同步快照
	LogoPath           string               `json:"logo_path"`                   // 大屏自定义标题图（Steam 式 logo），/local/bigscreen/art/... 或空；同样不入同步快照
	BgPath             string               `json:"bg_path"`                     // 大屏自定义背景图，替代封面做背景；同样不入同步快照
}

// GameBackup 游戏存档备份记录（基于文件系统，不使用数据库）
type GameBackup struct {
	Path      string    `json:"path"` // 备份文件路径（作为唯一标识）
	Name      string    `json:"name"` // 文件名
	GameID    string    `json:"game_id"`
	Size      int64     `json:"size"`       // 备份文件大小（字节）
	CreatedAt time.Time `json:"created_at"` // 创建时间（来自文件修改时间）
}
