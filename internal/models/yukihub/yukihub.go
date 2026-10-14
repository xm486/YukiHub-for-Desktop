package yukihub

// Backup represents a YukiHub backup archive payload.
type Backup struct {
	App       string `json:"app"`
	Schema    int    `json:"schema"`
	CreatedAt int64  `json:"created_at"`
	// Lightweight / Note 是 Android 快照的固定元信息（手机版两个方向都会写）。
	// 云同步快照恒为 lightweight=true；本地全量备份另外带 backup_type。
	Lightweight bool   `json:"lightweight,omitempty"`
	Note        string `json:"note,omitempty"`
	// BackupType 仅本地导出使用：手机版写 "local_full"（云同步快照不带该键）。
	BackupType string `json:"backup_type,omitempty"`
	// Profile 是账号资料段（昵称 / 签名 / 头像 URL）。手机版双向同步它，
	// 桌面端的昵称与头像来自 YukiHub 账号，同样在这里带上。
	Profile       *BackupProfile  `json:"profile,omitempty"`
	Settings      BackupSettings  `json:"settings"`
	Games         []Game          `json:"games"`
	PlaySessions  []PlaySession   `json:"play_sessions"`
	MetadataCache []MetadataCache `json:"metadata_cache"`
}

// BackupProfile 对应手机版快照的 profile 段。
type BackupProfile struct {
	Name string `json:"name,omitempty"`
	// Signature 桌面端没有对应概念，省略该键（手机端 optString(key, 本地值) 会保留原值）。
	Signature string `json:"signature,omitempty"`
	// AvatarURI 必须是 http(s) 地址：手机端会拒收本地 file:// 路径。
	AvatarURI string `json:"avatar_uri"`
}

type BackupSettings struct {
	MetadataSource string `json:"metadata_source"`
}

type Game struct {
	LocalID int64  `json:"local_id"`
	Title   string `json:"title"`
	// OriginalTitle / Description / Tags 必须是 omitempty。
	//
	// 手机版 importGamesJson 一律用 `optString(key, 本地值)`：
	// **键存在但为空串 = 确认清空**，键缺失才保留本地值。
	// 桌面端这三项经常是空的（没刮削的游戏没有简介 / 标签，别名数组也可能为空），
	// 早先写成 `""` 会把手机端同名字段直接抹掉。
	// 需要「桌面端删掉标签也能同步过去」时再引入显式清空语义，不要靠写空串。
	OriginalTitle string `json:"original_title,omitempty"`
	// Engine 是 Android 侧的引擎类型，桌面端不存储：导出时留空并省略，
	// 手机端读不到就保留自己那一条的值。
	Engine          string `json:"engine,omitempty"`
	RootUri         string `json:"root_uri"`
	CoverUri        string `json:"cover_uri,omitempty"`
	CoverPersistUri string `json:"cover_persist_uri,omitempty"`
	// CoverSourceType：0 表示「无来源信息」，省略而非写 0。
	CoverSourceType int `json:"cover_source_type,omitempty"`
	// 以下三个字段是 Android 侧的启动方式。桌面端没有对应概念，**必须省略**：
	// 手机端 importGamesJson 用的是 optString(key, 本地值)，字段存在但为空串
	// 会被当作「清空」，直接把对端的模拟器配置抹掉。
	EmulatorPackage    string `json:"emulator_package,omitempty"`
	LaunchTarget       string `json:"launch_target,omitempty"`
	WinlatorLaunchMode string `json:"winlator_launch_mode,omitempty"`
	Description        string `json:"description,omitempty"`
	Tags               string `json:"tags,omitempty"`
	// GamehubLocalGameId 是 Android 侧无本地目录条目的身份键，桌面端没有对应概念。
	// 同样必须省略：写成空串会把对端的身份键抹掉，破坏后续匹配。
	GamehubLocalGameId string `json:"gamehub_local_game_id,omitempty"`
	// GaishiLocalGameId 是 gamehub_local_game_id 的旧名。手机版导出时两个键写同值，
	// 导入时优先读 gamehub_*、回退 gaishi_*；桌面端同样两个都写，兼容旧版手机。
	GaishiLocalGameId string `json:"gaishi_local_game_id,omitempty"`
	GamehubLaunchMode string `json:"gamehub_launch_mode,omitempty"`
	PlayStatus        string `json:"play_status"`
	TotalPlayTime     int64  `json:"total_play_time"`
	LastPlayedAt      int64  `json:"last_played_at"`
	PlaytimeResetAt   int64  `json:"playtime_reset_at"` // 清零时间点（Unix 毫秒），0 表示从未清零
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
	// Hidden 必须是 *bool + omitempty，**不能**是 bool。
	//
	// 手机版导入这条字段是 `g.hidden = o.optBoolean("hidden", g.hidden)`：
	// **键缺失 = 保留对端原值**，只有明确写了 true / false 才算表态。
	// 用 bool 的话，缺键会被解码成 false，而合并导入会把 false 当成「明确不隐藏」
	// 直接写回 —— 一次同步就把对端藏好的游戏全部翻出来（老快照 / 只带 games 段
	// 的快照都不带这个键，正是高发场景）。
	//
	// 导出侧恒写这个键（指针始终非 nil）：false 也必须发出去，
	// 否则对端无法得知「这边把它取消隐藏了」。
	Hidden   *bool `json:"hidden,omitempty"`
	Favorite bool  `json:"favorite"`
	NSFW     bool  `json:"nsfw"`
}

type PlaySession struct {
	SessionUUID string `json:"session_uuid"`
	GameLocalID int64  `json:"game_local_id"`
	GameRootUri string `json:"game_root_uri"`
	// 桌面端没有 GameHub/gaishi 本地 id 概念，两个键都省略：
	// 写成空串会被手机端 optString(key, 默认值) 命中并当成「确认清空」，
	// 把对端的身份键抹掉（Game 段同理，必须 omitempty）。
	GamehubLocalGameId  string `json:"gamehub_local_game_id,omitempty"`
	GaishiLocalGameId   string `json:"gaishi_local_game_id,omitempty"`
	GameTitle           string `json:"game_title"`
	GameEngine          string `json:"game_engine"`
	GameEmulatorPackage string `json:"game_emulator_package"`
	StartTime           int64  `json:"start_time"`
	// EndTime 必须是 omitempty：手机版 exportPlaySessionsJson 只在 end_time
	// 非 null 时才写这个键，导入时用 `o.has("end_time") && !o.isNull(...)` 判断
	// 「是否已结束」，未结束的会话落库为 NULL。
	// 桌面端早期恒写 0，对端会把「还在玩」的会话当成「1970 年就结束了」，
	// 并且 end_time=0 在手机端 IS NOT NULL，会被算进聚合统计。
	EndTime    int64  `json:"end_time,omitempty"`
	Duration   int64  `json:"duration"` // 毫秒
	LaunchType string `json:"launch_type"`
	DeviceID   string `json:"device_id"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// MetadataCache 是 metadata_cache 段的一个元素。
//
// GameRootUri / GameTitle 不是可选的装饰字段：手机版 importMetadataJson
// （MetadataRepository.java）先按 game_root_uri 匹配、为空时再按 game_title
// 精确匹配，**只有 title 非空**才会回退到 game_local_id。
// 桌面端不写这两个键 → 前两步都失败、第三步被「title 为空」挡住 →
// 整个 metadata_cache 段在手机端被逐条静默丢弃。
type MetadataCache struct {
	GameLocalID int64  `json:"game_local_id"`
	GameRootUri string `json:"game_root_uri"`
	GameTitle   string `json:"game_title"`
	Source      string `json:"source"`
	SourceID    string `json:"source_id"`
	JSON        string `json:"json"`
	UpdatedAt   int64  `json:"updated_at"`
}

type Metadata struct {
	ID                    string   `json:"id"`
	ChineseTitle          string   `json:"chineseTitle"`
	OriginalTitle         string   `json:"originalTitle"`
	RomanTitle            string   `json:"romanTitle"`
	CoverURL              string   `json:"coverUrl"`
	Description           string   `json:"description"`
	TranslatedDescription string   `json:"translatedDescription"`
	Released              string   `json:"released"`
	Developer             string   `json:"developer"`
	TagsText              string   `json:"tagsText"`
	RatingText            string   `json:"ratingText"`
	CoverSexual           int      `json:"coverSexual"`
	ScreenshotURLs        []string `json:"screenshotUrls"`
}
