package exporter

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"yukihub/internal/applog"
	"yukihub/internal/common/enums"
	"yukihub/internal/models"
	"yukihub/internal/models/yukihub"
	"yukihub/internal/service/gamehelper"
)

const (
	// yukiHubBackupApp / yukiHubBackupSchema 与 Android 侧快照契约一致。
	yukiHubBackupApp    = "YukiHub"
	yukiHubBackupSchema = 5

	// yukiHubSessionsPerGame 是每个游戏导出的游玩记录上限，与契约的「同步与本地备份均 30 条」一致。
	yukiHubSessionsPerGame = 30

	// Android 侧 launch_type / device_id 的缺省值（见 GameRepository.importPlaySessionsJson）。
	yukiHubLaunchType = "external"
	yukiHubDeviceID   = "desktop"

	// 手机版快照里写死的两段 note（SyncManager.buildLocalSnapshot /
	// MainActivity.exportLocalBackup）。逐字对齐，便于对端识别快照来源。
	yukiHubCloudNote = "Only text metadata is synced. No game files, save files, or binary cover images are embedded."
	yukiHubLocalNote = "Local backup keeps the latest 30 play sessions. Uses gzip compression."

	// 本地全量备份的 backup_type（手机版 MainActivity 写 "local_full"）。
	yukiHubLocalBackupType = "local_full"
)

// YukiHubExporter 把桌面端库导出为 Android 版 YukiHub 的 schema 5 快照。
//
// 与 importer 对称：只读 DuckDB，不写库，因此不需要 dbutils.WithDuckDBWriteLock。
type YukiHubExporter struct {
	ctx context.Context
	db  *sql.DB
	// profile 是账号资料段（昵称 / 头像 URL），由调用方注入；
	// 为空时快照里不含 profile 段（手机端会保留自己那份）。
	profile *yukihub.BackupProfile
	// metadataSource 是桌面端的全局首选资料源，写进 settings.metadata_source。
	// 手机端只认识 vndb/bangumi/bangumi_mirror/ymgal/hikarinagi/nextmoe 六个值，
	// 其余值会被它忽略（不会覆盖手机端设置）。
	metadataSource string
}

func NewYukiHubExporter(ctx context.Context, db *sql.DB) *YukiHubExporter {
	return &YukiHubExporter{ctx: ctx, db: db}
}

// SetProfile 注入账号资料（对齐手机版快照的 profile 段）。
//
// 只带 http(s) 头像：手机端会把非 http(s) 的 avatar_uri 直接丢掉。
func (e *YukiHubExporter) SetProfile(name, avatarURI string) {
	trimmedAvatar := strings.TrimSpace(avatarURI)
	if !strings.HasPrefix(trimmedAvatar, "http://") && !strings.HasPrefix(trimmedAvatar, "https://") {
		trimmedAvatar = ""
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" && trimmedAvatar == "" {
		e.profile = nil
		return
	}
	e.profile = &yukihub.BackupProfile{
		Name:      trimmedName,
		AvatarURI: trimmedAvatar,
	}
}

// SetMetadataSource 注入全局首选资料源（写进快照的 settings.metadata_source）。
func (e *YukiHubExporter) SetMetadataSource(source string) {
	e.metadataSource = strings.TrimSpace(source)
}

// Build 生成**云同步**形态的 schema 5 快照。
//
// 刻意不导出：扫描目录配置、自定义背景图/视频、trailer/logo/bg 路径、音乐厅数据、
// 游戏本体与存档、二进制封面图（契约明确排除）。
func (e *YukiHubExporter) Build() (*yukihub.Backup, error) {
	return e.build(snapshotCloud)
}

// BuildLocalBackup 生成**本地全量备份**形态的快照（导出 .ykbak 用）。
//
// 与云同步快照的差异完全照抄手机版：
//   - created_at 写当前时间（云同步恒为 0）
//   - 多一个 backup_type = "local_full"
//   - note 换成本地备份文案
func (e *YukiHubExporter) BuildLocalBackup() (*yukihub.Backup, error) {
	return e.build(snapshotLocalBackup)
}

// newBackupEnvelope 生成快照骨架（顶层元信息），负载由调用方填充。
//
// 字段取值逐字对齐手机版：
//   - 云同步（SyncManager.buildLocalSnapshot）：created_at 恒为 0、lightweight=true、
//     固定 note，且**不带** backup_type；
//   - 本地全量备份（MainActivity.exportLocalBackup）：created_at=当前毫秒、
//     追加 backup_type=local_full、note 换成本地备份文案。
//
// 抽出来是为了能用单元测试钉住这几项——双端同步一旦这里跑偏，
// 对端会把快照当成另一种形态处理。
func newBackupEnvelope(kind snapshotKind) *yukihub.Backup {
	envelope := &yukihub.Backup{
		App:         yukiHubBackupApp,
		Schema:      yukiHubBackupSchema,
		Lightweight: true,
		CreatedAt:   0,
		Note:        yukiHubCloudNote,
	}
	if kind == snapshotLocalBackup {
		envelope.CreatedAt = unixMilli(time.Now())
		envelope.Note = yukiHubLocalNote
		envelope.BackupType = yukiHubLocalBackupType
	}
	return envelope
}

// snapshotKind 区分两种快照形态（仅元信息不同，负载完全一致）。
type snapshotKind int

const (
	snapshotCloud snapshotKind = iota
	snapshotLocalBackup
)

func (e *YukiHubExporter) build(kind snapshotKind) (*yukihub.Backup, error) {
	games, err := e.loadGames()
	if err != nil {
		return nil, err
	}
	tags, err := e.loadTags()
	if err != nil {
		return nil, err
	}
	sessions, err := e.loadSessions()
	if err != nil {
		return nil, err
	}
	favorites, err := e.loadFavorites()
	if err != nil {
		return nil, err
	}
	metadataCache, err := e.loadMetadataCache()
	if err != nil {
		return nil, err
	}

	backup := newBackupEnvelope(kind)
	backup.Profile = e.profile
	backup.Settings = yukihub.BackupSettings{
		// 只写这一项：手机版导入 settings 是「键存在就覆盖」，桌面端没有对应
		// 概念的设置项一律省略，避免把手机端的设置冲掉。
		MetadataSource: e.metadataSource,
	}
	backup.Games = make([]yukihub.Game, 0, len(games))
	// metadata_cache 取自 game_metadata_sources.cache_json，负载沿用 Android 的
	// VnMetadata 结构，两个方向都能原样往返。
	backup.MetadataCache = metadataCache
	// 预先建成空切片，序列化时才是 `[]` 而不是 `null`（手机版恒写 `[]`）。
	backup.PlaySessions = make([]yukihub.PlaySession, 0)
	for _, game := range games {
		// 跳过无标题条目：Android 侧会把空标题落成「未命名游戏」，
		// 这类条目在跨端同步时只会制造无法匹配的占位记录。
		if strings.TrimSpace(game.Name) == "" {
			applog.LogWarningf(e.ctx, "跳过无标题游戏（ID=%s），不导出到 YukiHub 快照", game.ID)
			continue
		}
		gameSessions := sessions[game.ID]
		entry, sessionEntries := buildYukiHubGame(game, tags[game.ID], gameSessions, favorites[game.ID])
		backup.Games = append(backup.Games, entry)
		backup.PlaySessions = append(backup.PlaySessions, sessionEntries...)
	}
	return backup, nil
}

// Export 生成快照并以 gzip 写入文件（与 importer 的 gzip 读取对称）。
func (e *YukiHubExporter) Export(path string) error {
	_, _, err := e.ExportWithSummary(path)
	return err
}

// ExportWithSummary 与 Export 相同，另外回报写出的游戏数与游玩记录数，
// 供界面提示用（只 Build 一次，不为了拿计数多跑一遍全库查询）。
func (e *YukiHubExporter) ExportWithSummary(path string) (int, int, error) {
	backup, err := e.BuildLocalBackup()
	if err != nil {
		return 0, 0, err
	}
	data, err := json.Marshal(backup)
	if err != nil {
		return 0, 0, fmt.Errorf("序列化 YukiHub 快照失败: %w", err)
	}

	file, err := os.Create(path)
	if err != nil {
		return 0, 0, fmt.Errorf("创建 YukiHub 快照文件失败: %w", err)
	}
	writer := gzip.NewWriter(file)
	if _, err := writer.Write(data); err != nil {
		writer.Close()
		file.Close()
		return 0, 0, fmt.Errorf("写入 YukiHub 快照失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		file.Close()
		return 0, 0, fmt.Errorf("关闭 YukiHub 快照失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return 0, 0, fmt.Errorf("保存 YukiHub 快照失败: %w", err)
	}
	games := len(backup.Games)
	sessions := len(backup.PlaySessions)
	applog.LogInfof(e.ctx, "ExportYukiHub: wrote %d games and %d play sessions", games, sessions)
	return games, sessions, nil
}

func (e *YukiHubExporter) loadGames() ([]models.Game, error) {
	query := `SELECT
		g.id,
		COALESCE(g.name, '') as name,
		COALESCE(g.aliases, '[]') as aliases,
		COALESCE(g.cover_url, '') as cover_url,
		COALESCE(g.cover_source_url, '') as cover_source_url,
		COALESCE(g.summary, '') as summary,
		COALESCE(g.path, '') as path,
		COALESCE(g.game_directory, '') as game_directory,
		COALESCE(g.status, 'unplayed') as status,
		COALESCE(g.is_nsfw, FALSE) as is_nsfw,
		COALESCE(g.created_at, CURRENT_TIMESTAMP) as created_at,
		COALESCE(g.updated_at, g.created_at, CURRENT_TIMESTAMP) as updated_at,
		COALESCE(g.legacy_local_id, '') as legacy_local_id,
		g.playtime_reset_at,
		COALESCE(g.hidden, FALSE) as hidden
	FROM games g
	ORDER BY g.created_at ASC, g.id ASC`

	rows, err := e.db.QueryContext(e.ctx, query)
	if err != nil {
		applog.LogErrorf(e.ctx, "ExportYukiHub: failed to query games: %v", err)
		return nil, fmt.Errorf("查询游戏列表失败: %w", err)
	}
	defer rows.Close()

	games := make([]models.Game, 0)
	for rows.Next() {
		var game models.Game
		var aliasesJSON string
		var status string
		var playtimeResetAt sql.NullTime
		if err := rows.Scan(
			&game.ID,
			&game.Name,
			&aliasesJSON,
			&game.CoverURL,
			&game.CoverSourceURL,
			&game.Summary,
			&game.Path,
			&game.GameDirectory,
			&status,
			&game.IsNSFW,
			&game.CreatedAt,
			&game.UpdatedAt,
			&game.LegacyLocalID,
			&playtimeResetAt,
			&game.Hidden,
		); err != nil {
			applog.LogErrorf(e.ctx, "ExportYukiHub: failed to scan game: %v", err)
			return nil, fmt.Errorf("读取游戏记录失败: %w", err)
		}
		game.Aliases, err = gamehelper.DecodeAliases(aliasesJSON)
		if err != nil {
			return nil, fmt.Errorf("解析游戏别名失败: %w", err)
		}
		game.Status = enums.GameStatus(status)
		if playtimeResetAt.Valid {
			resetAt := playtimeResetAt.Time
			game.PlaytimeResetAt = &resetAt
		}
		games = append(games, game)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历游戏列表失败: %w", err)
	}
	return games, nil
}

func (e *YukiHubExporter) loadTags() (map[string][]string, error) {
	rows, err := e.db.QueryContext(e.ctx, "SELECT game_id, name FROM game_tags ORDER BY game_id ASC, name ASC")
	if err != nil {
		applog.LogErrorf(e.ctx, "ExportYukiHub: failed to query game_tags: %v", err)
		return nil, fmt.Errorf("查询游戏标签失败: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]string)
	for rows.Next() {
		var gameID string
		var name string
		if err := rows.Scan(&gameID, &name); err != nil {
			return nil, fmt.Errorf("读取游戏标签失败: %w", err)
		}
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		result[gameID] = append(result[gameID], name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历游戏标签失败: %w", err)
	}
	return result, nil
}

func (e *YukiHubExporter) loadSessions() (map[string][]models.PlaySession, error) {
	rows, err := e.db.QueryContext(e.ctx, `SELECT
		id, game_id, COALESCE(start_time, CURRENT_TIMESTAMP), end_time, COALESCE(duration, 0), COALESCE(updated_at, start_time, CURRENT_TIMESTAMP)
	FROM play_sessions
	ORDER BY game_id ASC, start_time ASC, id ASC`)
	if err != nil {
		applog.LogErrorf(e.ctx, "ExportYukiHub: failed to query play_sessions: %v", err)
		return nil, fmt.Errorf("查询游玩记录失败: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]models.PlaySession)
	for rows.Next() {
		var session models.PlaySession
		var endTime sql.NullTime
		if err := rows.Scan(&session.ID, &session.GameID, &session.StartTime, &endTime, &session.Duration, &session.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读取游玩记录失败: %w", err)
		}
		if endTime.Valid {
			session.EndTime = endTime.Time
		}
		result[session.GameID] = append(result[session.GameID], session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历游玩记录失败: %w", err)
	}
	return result, nil
}

// loadFavorites 读取系统收藏分类下的游戏。favorite 在桌面端由「收藏」系统分类承载。
func (e *YukiHubExporter) loadFavorites() (map[string]bool, error) {
	rows, err := e.db.QueryContext(e.ctx, "SELECT game_id FROM game_categories WHERE category_id = ?", gamehelper.SystemFavoritesCategoryID)
	if err != nil {
		applog.LogErrorf(e.ctx, "ExportYukiHub: failed to query favorites: %v", err)
		return nil, fmt.Errorf("查询收藏分类失败: %w", err)
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var gameID string
		if err := rows.Scan(&gameID); err != nil {
			return nil, fmt.Errorf("读取收藏分类失败: %w", err)
		}
		result[gameID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历收藏分类失败: %w", err)
	}
	return result, nil
}

// loadMetadataCache 读取每个来源的元数据负载，映射为快照的 metadata_cache 元素。
//
// game_root_uri / game_title 必须带上：手机版 importMetadataJson 先按 root_uri 匹配、
// 为空时按 title 精确匹配，只有 title 非空才回退 local_id。少了这两个键，
// 整段 metadata_cache 会在手机端被逐条丢弃。
//
// **不能因为 local_id 为 0 就跳过**：桌面端自己扫描进来的游戏没有 Android 侧的整数
// ID（games.legacy_local_id 为空 → local_id 恒为 0），早期版本把它们整段跳过，
// 结果是「PC 上用 nextmoe 刮的资料同步到手机后全没了、退回默认的 vndb」——
// 手机端是靠 metadata_cache 里 source='nextmoe' 那一行取资料的。
// 手机端本来就不依赖 local_id（标题能匹配上就用标题），所以照常导出即可。
func (e *YukiHubExporter) loadMetadataCache() ([]yukihub.MetadataCache, error) {
	rows, err := e.db.QueryContext(e.ctx, `SELECT
		COALESCE(g.legacy_local_id, ''),
		COALESCE(g.name, ''),
		s.source_type,
		COALESCE(s.source_id, ''),
		COALESCE(s.cache_json, ''),
		COALESCE(s.updated_at, s.cached_at, g.updated_at, CURRENT_TIMESTAMP)
	FROM game_metadata_sources s
	JOIN games g ON g.id = s.game_id
	WHERE COALESCE(s.cache_json, '') <> ''
	  AND TRIM(COALESCE(g.name, '')) <> ''
	ORDER BY g.created_at ASC, g.id ASC, s.source_type ASC`)
	if err != nil {
		applog.LogErrorf(e.ctx, "ExportYukiHub: failed to query metadata cache: %v", err)
		return nil, fmt.Errorf("查询元数据缓存失败: %w", err)
	}
	defer rows.Close()

	entries := make([]yukihub.MetadataCache, 0)
	for rows.Next() {
		var legacyLocalID string
		var title string
		var sourceType string
		var sourceID string
		var payload string
		var updatedAt time.Time
		if err := rows.Scan(&legacyLocalID, &title, &sourceType, &sourceID, &payload, &updatedAt); err != nil {
			return nil, fmt.Errorf("读取元数据缓存失败: %w", err)
		}
		entries = append(entries, yukihub.MetadataCache{
			// 桌面端自建游戏没有对端整数 ID，写 0；对端与桌面端导入器都会改用标题匹配。
			GameLocalID: parseYukiHubLocalID(legacyLocalID),
			// 与 games 段同口径：桌面端不导出本机绝对路径，恒为空串。
			// 手机端对空 root_uri 会转向 game_title 精确匹配。
			GameRootUri: "",
			GameTitle:   strings.TrimSpace(title),
			Source:      string(gamehelper.NormalizeMetadataSourceType(enums.SourceType(sourceType))),
			SourceID:    strings.TrimSpace(sourceID),
			JSON:        payload,
			UpdatedAt:   unixMilli(updatedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历元数据缓存失败: %w", err)
	}
	return entries, nil
}

// buildYukiHubGame 把一个桌面端游戏转换为快照的 games 元素，并生成它的游玩记录。
func buildYukiHubGame(game models.Game, tags []string, sessions []models.PlaySession, favorite bool) (yukihub.Game, []yukihub.PlaySession) {
	resetAt := time.Time{}
	if game.PlaytimeResetAt != nil {
		resetAt = *game.PlaytimeResetAt
	}
	// 与 Android 侧 exportPlaySessionsJson 对称：只保留清零之后的会话，否则清零历史会在 Android 侧复活。
	kept := sessionsAfterReset(sessions, resetAt)

	var totalPlayTime int64
	var lastPlayedAt int64
	for _, session := range kept {
		totalPlayTime += int64(session.Duration) * 1000 // 桌面端单位是秒，Android 侧是毫秒
		if playedAt := sessionTimestamp(session); playedAt > lastPlayedAt {
			lastPlayedAt = playedAt
		}
	}

	entry := yukihub.Game{
		LocalID:       parseYukiHubLocalID(game.LegacyLocalID),
		Title:         game.Name,
		OriginalTitle: firstYukiHubAlias(game.Aliases),
		Engine:        "", // Android 专用（引擎类型），桌面端不存储
		// RootUri 恒为空串，不导出 Windows 绝对路径。原因：
		// 1) 这些路径在 Android 上不可达，写过去只会让对端存一堆无意义的 D:\... ；
		// 2) 与 importer 方向对称——桌面端导入 Android 备份时同样不把对端的
		//    root_uri 写进桌面端 path/game_directory；
		// 3) 契约已明确「两侧规范化后的路径字符串不等价，跨端匹配以标题为主」。
		// 空 root_uri 时 Android 会走 findByTitleForEmptyRoot 按标题匹配。
		// TODO(round-trip)：Android 源生的条目（其本地 root_uri 非空）经桌面端
		// 回导时仍可能在对端产生重复。要彻底解决需新增 legacy_root_uri 列保存
		// 对端原始路径并在导出时回填，见 ROADMAP 阶段 2。
		RootUri: "",
		// 优先用元数据来源的原始网络地址：本地封面可能只是桌面端的缓存文件，
		// 对端拿不到；两者都不是 http(s) 时留空（omitempty 会省略该字段），
		// 手机端便会保留自己已有的封面。
		CoverUri:        firstNetworkURL(game.CoverSourceURL, game.CoverURL),
		CoverPersistUri: "",
		CoverSourceType: 0,
		Description:     game.Summary,
		Tags:            strings.Join(tags, ","),
		PlayStatus:      mapGameStatusToYukiHub(game.Status),
		TotalPlayTime:   totalPlayTime,
		LastPlayedAt:    lastPlayedAt,
		PlaytimeResetAt: unixMilli(resetAt),
		CreatedAt:       unixMilli(game.CreatedAt),
		UpdatedAt:       unixMilli(game.UpdatedAt),
		Hidden:          yukiHubBoolPtr(game.Hidden),
		Favorite:        favorite,
		NSFW:            game.IsNSFW,
	}
	return entry, buildYukiHubSessions(entry, game, kept)
}

// yukiHubBoolPtr 恒返回非 nil 指针：快照里这些布尔键必须**总是出现**。
//
// 手机版读的是 `optBoolean(k, 本地值)` —— 键缺失等于「对端没说」，会保留自己的值。
// 所以「取消隐藏」这类状态变化只有把 false 明确发出去才能生效；省略键等于没同步。
func yukiHubBoolPtr(value bool) *bool {
	return &value
}

func buildYukiHubSessions(entry yukihub.Game, game models.Game, sessions []models.PlaySession) []yukihub.PlaySession {
	// 只导出最新的 yukiHubSessionsPerGame 条（契约规定按时间取尾部），sessions 已按开始时间升序。
	exported := sessions
	if len(exported) > yukiHubSessionsPerGame {
		exported = exported[len(exported)-yukiHubSessionsPerGame:]
	}

	result := make([]yukihub.PlaySession, 0, len(exported))
	for _, session := range exported {
		result = append(result, yukihub.PlaySession{
			SessionUUID: session.ID,
			GameLocalID: entry.LocalID,
			GameRootUri: entry.RootUri,
			GameTitle:   game.Name,
			StartTime:   unixMilli(session.StartTime),
			EndTime:     unixMilli(session.EndTime),
			Duration:    int64(session.Duration) * 1000, // 秒 → 毫秒
			LaunchType:  yukiHubLaunchType,
			DeviceID:    yukiHubDeviceID,
			CreatedAt:   unixMilli(session.StartTime),
			UpdatedAt:   unixMilli(session.UpdatedAt),
		})
	}
	return result
}

// sessionsAfterReset 复刻 Android 侧 exportPlaySessionsJson 的
// COALESCE(ps.end_time, ps.start_time, 0) >= IFNULL(g.playtime_reset_at, 0) 条件。
func sessionsAfterReset(sessions []models.PlaySession, resetAt time.Time) []models.PlaySession {
	resetMillis := unixMilli(resetAt)
	result := make([]models.PlaySession, 0, len(sessions))
	for _, session := range sessions {
		if sessionTimestamp(session) < resetMillis {
			continue
		}
		result = append(result, session)
	}
	// 保证「取最新若干条」与「会话顺序」都稳定，避免同秒会话在不同机器上落到不同结果。
	sort.SliceStable(result, func(i, j int) bool {
		left := sessionTimestamp(result[i])
		right := sessionTimestamp(result[j])
		if left != right {
			return left < right
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// sessionTimestamp 取会话的时间点：优先 end_time，缺失时回退 start_time。
func sessionTimestamp(session models.PlaySession) int64 {
	if !session.EndTime.IsZero() {
		return unixMilli(session.EndTime)
	}
	return unixMilli(session.StartTime)
}

// mapGameStatusToYukiHub 输出 Android 侧的 play_status。
//
// 两端状态已统一为同一套五态字符串（见 docs/mobile-yukihub-migration.md），
// 这里只做原样透出；未知值回落 unplayed。
func mapGameStatusToYukiHub(status enums.GameStatus) string {
	switch status {
	case enums.StatusPlaying:
		return "playing"
	case enums.StatusCompleted:
		return "completed"
	case enums.StatusOnHold:
		return "onhold"
	case enums.StatusDropped:
		return "dropped"
	default:
		return "unplayed"
	}
}

// firstNetworkURL 返回首个**可跨设备使用**的网络封面地址（http/https），
// 都没有则返回空串。调用方通常传 (cover_source_url, cover_url) 两个候选。
//
// 是「首个 http(s)」而不是「首个非空」：cover_source_url 在历史数据里可能落成
// 本地路径（本地封面缓存 / content:// 之类），先取非空会把后面那个有效的网络
// cover_url 直接挤掉，结果是封面明明有网络地址却被同步丢了。
func firstNetworkURL(candidates ...string) string {
	for _, candidate := range candidates {
		if coverURL := networkCoverURI(candidate); coverURL != "" {
			return coverURL
		}
	}
	return ""
}

// networkCoverURI 只导出网络封面，本地封面文件跨设备无效（契约明确不迁移）。
func networkCoverURI(coverURL string) string {
	coverURL = strings.TrimSpace(coverURL)
	if !strings.HasPrefix(coverURL, "http://") && !strings.HasPrefix(coverURL, "https://") {
		return ""
	}
	return coverURL
}

func firstYukiHubAlias(aliases []string) string {
	for _, alias := range aliases {
		if alias = strings.TrimSpace(alias); alias != "" {
			return alias
		}
	}
	return ""
}

// parseYukiHubLocalID 把保留的 Android 侧整数 ID 还原为 local_id，缺失或非法时为 0。
// Android 侧把 0 当作「无身份」，不会用它做匹配。
func parseYukiHubLocalID(value string) int64 {
	localID, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || localID <= 0 {
		return 0
	}
	return localID
}

func unixMilli(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}
