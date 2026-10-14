package importer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
	"yukihub/internal/applog"
	"yukihub/internal/common/enums"
	"yukihub/internal/common/importpath"
	"yukihub/internal/common/vo"
	"yukihub/internal/models"
	"yukihub/internal/service/cloudsync"
	"yukihub/internal/service/gamehelper"
	"yukihub/internal/utils/dbutils"
	"yukihub/internal/utils/metadata"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/google/uuid"
)

const (
	importStatusNew               = "new"
	importStatusExistsPath        = "exists_path"
	importStatusExistsSource      = "exists_source"
	importStatusExistsNamePath    = "exists_name_path"
	importStatusPossibleDuplicate = "possible_duplicate"
	importCoverWorkerCount        = 4
)

type GameRef struct {
	ID         string
	Name       string
	Path       string
	SourceType enums.SourceType
	SourceID   string
	CreatedAt  time.Time
}

func gameRefFromModel(game models.Game) GameRef {
	return GameRef{
		ID:         game.ID,
		Name:       game.Name,
		Path:       game.Path,
		SourceType: game.SourceType,
		SourceID:   game.SourceID,
		CreatedAt:  game.CreatedAt,
	}
}

func (ref GameRef) game() models.Game {
	return models.Game{
		ID:         ref.ID,
		Name:       ref.Name,
		Path:       ref.Path,
		SourceType: ref.SourceType,
		SourceID:   ref.SourceID,
		CreatedAt:  ref.CreatedAt,
	}
}

type Index struct {
	byPath     map[string]GameRef
	bySource   map[string]GameRef
	byNamePath map[string]GameRef
	byName     map[string]GameRef
}

type CommitItem struct {
	Game                    models.Game
	Tags                    []metadata.TagItem
	Sessions                []models.PlaySession
	Source                  enums.SourceType
	Action                  string
	UpdateLocalLaunchFields bool
	Favorite                bool
	// Hidden 三态，语义见 ImportItem.Hidden：nil = 快照没带键（保留本地）。
	Hidden      *bool
	CoverLoader func(models.Game) (string, error)
}

type CoverDownloadItem struct {
	GameID   string
	GameName string
	CoverURL string
}

type CommitDependencies struct {
	Ctx                    context.Context
	DB                     *sql.DB
	AllowDuplicateMetadata bool
	StartCoverDownloads    func([]CoverDownloadItem) string
	UpdateCoverURL         func(gameID string, coverURL string) error
}

type Committer struct {
	ctx                    context.Context
	db                     *sql.DB
	allowDuplicateMetadata bool
	startCoverDownloads    func([]CoverDownloadItem) string
	updateCoverURL         func(gameID string, coverURL string) error
}

func NewCommitter(deps CommitDependencies) *Committer {
	return &Committer{
		ctx:                    deps.Ctx,
		db:                     deps.DB,
		allowDuplicateMetadata: deps.AllowDuplicateMetadata,
		startCoverDownloads:    deps.StartCoverDownloads,
		updateCoverURL:         deps.UpdateCoverURL,
	}
}

func importPathContainsNormalized(parentPath string, childPath string) bool {
	return importpath.ContainsNormalized(parentPath, childPath)
}

func normalizeImportName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// nullableTime 把可空的清零时间转成 appender 可写入的值：nil 落 NULL，而不是零值时间。
func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

// nullableBool 用于 hidden 这类三态布尔：NULL = 快照**没带这个键**（更新路径保留本地值），
// TRUE / FALSE = 对端明确表态。
//
// 为什么不能用 bool：手机版读的是 `optBoolean("hidden", 本地值)`，
// 键缺失必须保留；写成 bool 就把「没信息」和「明确 false（取消隐藏）」混成一个值，
// 一次合并导入就能把本地藏好的游戏全部翻出来。
func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func importSourceKey(source enums.SourceType, sourceID string) string {
	sourceID = strings.ToLower(strings.TrimSpace(sourceID))
	if source == "" || sourceID == "" {
		return ""
	}
	return strings.ToLower(string(source)) + "\x00" + sourceID
}

func importNamePathKey(name string, path string) string {
	nameKey := normalizeImportName(name)
	pathKey := normalizeImportPath(path)
	if nameKey == "" || pathKey == "" {
		return ""
	}
	return nameKey + "\x00" + pathKey
}

func NewIndex(refs []GameRef) Index {
	idx := Index{
		byPath:     make(map[string]GameRef, len(refs)),
		bySource:   make(map[string]GameRef, len(refs)),
		byNamePath: make(map[string]GameRef, len(refs)),
		byName:     make(map[string]GameRef, len(refs)),
	}

	for _, ref := range refs {
		idx.Add(ref)
	}
	return idx
}

func (idx Index) Add(ref GameRef) {
	if key := normalizeImportPath(ref.Path); key != "" {
		if _, exists := idx.byPath[key]; !exists {
			idx.byPath[key] = ref
		}
	}
	if key := importSourceKey(ref.SourceType, ref.SourceID); key != "" {
		if _, exists := idx.bySource[key]; !exists {
			idx.bySource[key] = ref
		}
	}
	if key := importNamePathKey(ref.Name, ref.Path); key != "" {
		if _, exists := idx.byNamePath[key]; !exists {
			idx.byNamePath[key] = ref
		}
	}
	if key := normalizeImportName(ref.Name); key != "" {
		if _, exists := idx.byName[key]; !exists {
			idx.byName[key] = ref
		}
	}
}

func (idx Index) FindByPath(path string) (GameRef, bool) {
	ref, ok := idx.byPath[normalizeImportPath(path)]
	return ref, ok
}

func (idx Index) FindByPathConflict(path string) (GameRef, bool) {
	pathKey := normalizeImportPath(path)
	if pathKey == "" {
		return GameRef{}, false
	}

	if ref, ok := idx.byPath[pathKey]; ok {
		return ref, true
	}

	for existingPath, ref := range idx.byPath {
		if importPathContainsNormalized(pathKey, existingPath) || importPathContainsNormalized(existingPath, pathKey) {
			return ref, true
		}
	}
	return GameRef{}, false
}

func (idx Index) FindBySource(source enums.SourceType, sourceID string) (GameRef, bool) {
	key := importSourceKey(source, sourceID)
	if key == "" {
		return GameRef{}, false
	}
	ref, ok := idx.bySource[key]
	return ref, ok
}

func (idx Index) FindByNamePath(name string, path string) (GameRef, bool) {
	key := importNamePathKey(name, path)
	if key == "" {
		return GameRef{}, false
	}
	ref, ok := idx.byNamePath[key]
	return ref, ok
}

func (idx Index) FindByName(name string) (GameRef, bool) {
	ref, ok := idx.byName[normalizeImportName(name)]
	return ref, ok
}

func (c *Committer) listGameRefs() ([]GameRef, error) {
	if c.db == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	rows, err := c.db.QueryContext(c.ctx, `
		SELECT
			g.id,
			COALESCE(g.name, ''),
			COALESCE(g.path, ''),
			COALESCE(s.source_type, g.source_type, ''),
			COALESCE(s.source_id, g.source_id, ''),
			COALESCE(g.created_at, g.cached_at, g.updated_at, CURRENT_TIMESTAMP)
		FROM games g
		LEFT JOIN game_metadata_sources s ON s.game_id = g.id
	`)
	if err != nil {
		return nil, fmt.Errorf("query import game refs: %w", err)
	}
	defer rows.Close()

	refs := make([]GameRef, 0)
	for rows.Next() {
		var ref GameRef
		var sourceType string
		if err := rows.Scan(&ref.ID, &ref.Name, &ref.Path, &sourceType, &ref.SourceID, &ref.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan import game ref: %w", err)
		}
		ref.SourceType = enums.SourceType(sourceType)
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate import game refs: %w", err)
	}
	return refs, nil
}

func (c *Committer) LoadIndex() (Index, error) {
	refs, err := c.listGameRefs()
	if err != nil {
		return Index{}, err
	}
	return NewIndex(refs), nil
}

func (c *Committer) ListGames() ([]models.Game, error) {
	refs, err := c.listGameRefs()
	if err != nil {
		return nil, err
	}
	games := make([]models.Game, 0, len(refs))
	for _, ref := range refs {
		games = append(games, models.Game{
			ID:         ref.ID,
			Name:       ref.Name,
			Path:       ref.Path,
			SourceType: ref.SourceType,
			SourceID:   ref.SourceID,
			CreatedAt:  ref.CreatedAt,
		})
	}
	return games, nil
}

func (c *Committer) AddItems(items []ImportItem) (ImportResult, error) {
	result := ImportResult{
		FailedNames:  []string{},
		SkippedNames: []string{},
	}
	if len(items) == 0 {
		return result, nil
	}

	startedAt := time.Now()
	stepStartedAt := time.Now()
	idx, err := c.LoadIndex()
	if err != nil {
		return result, fmt.Errorf("加载导入索引失败: %w", err)
	}
	applog.LogInfof(c.ctx, "addImporterItems: loaded import index for items=%d elapsed=%s", len(items), time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	toCommit := make([]CommitItem, 0, len(items))
	for _, item := range items {
		game := item.Source.Game
		displayName := item.DisplayName
		if displayName == "" {
			displayName = game.Name
		}
		if item.Path == "" {
			item.Path = game.Path
		}

		source := item.Source.Source
		if source == "" {
			source = game.SourceType
		}
		if game.SourceType == "" {
			game.SourceType = source
		}
		if game.ID == "" {
			game.ID = uuid.New().String()
		}

		action := item.Action
		if action == "" {
			action = ImportActionCreate
		}
		if TargetsExistingGame(action) {
			if item.ExistingGameID == "" {
				result.Failed++
				result.FailedNames = append(result.FailedNames, displayName)
				continue
			}
			game.ID = item.ExistingGameID
			for i := range item.Sessions {
				item.Sessions[i].GameID = item.ExistingGameID
			}
		} else {
			if ref, ok := idx.FindByPathConflict(item.Path); ok {
				result.Skipped++
				result.SkippedNames = append(result.SkippedNames, displayName+" (路径已存在: "+ref.Name+")")
				continue
			}
		}

		if !c.allowDuplicateMetadata {
			sourceRefs := append([]models.GameMetadataSource(nil), game.MetadataSources...)
			if len(sourceRefs) == 0 {
				sourceRefs = append(sourceRefs, models.GameMetadataSource{SourceType: source, SourceID: game.SourceID})
			}
			duplicateFound := false
			for _, sourceRef := range sourceRefs {
				if ref, ok := idx.FindBySource(sourceRef.SourceType, sourceRef.SourceID); ok && (!TargetsExistingGame(action) || ref.ID != item.ExistingGameID) {
					result.Skipped++
					result.SkippedNames = append(result.SkippedNames, displayName+" (元数据已存在: "+ref.Name+")")
					duplicateFound = true
					break
				}
			}
			if duplicateFound {
				continue
			}
		}
		if ref, ok := idx.FindByNamePath(game.Name, item.Path); ok && (!TargetsExistingGame(action) || ref.ID != item.ExistingGameID) {
			result.Skipped++
			result.SkippedNames = append(result.SkippedNames, displayName+" (已存在: "+ref.Name+")")
			continue
		}

		converted := CommitItem{
			Game:                    game,
			Tags:                    item.Source.Tags,
			Sessions:                item.Sessions,
			Source:                  source,
			Action:                  action,
			UpdateLocalLaunchFields: item.UpdateLocalLaunchFields,
			Favorite:                item.Favorite,
			Hidden:                  item.Hidden,
			CoverLoader:             item.CoverLoader,
		}
		toCommit = append(toCommit, converted)
		if action == ImportActionCreate {
			baseRef := GameRef{
				ID:         game.ID,
				Name:       game.Name,
				Path:       game.Path,
				SourceType: game.SourceType,
				SourceID:   game.SourceID,
				CreatedAt:  game.CreatedAt,
			}
			idx.Add(baseRef)
			for _, sourceRef := range game.MetadataSources {
				baseRef.SourceType = sourceRef.SourceType
				baseRef.SourceID = sourceRef.SourceID
				idx.Add(baseRef)
			}
		}
	}
	applog.LogInfof(c.ctx, "addImporterItems: filtered commit_items=%d skipped=%d elapsed=%s", len(toCommit), result.Skipped, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	success, sessionsImported, err := c.CommitItems(toCommit)
	if err != nil {
		result.Failed += len(toCommit)
		for _, item := range toCommit {
			result.FailedNames = append(result.FailedNames, item.Game.Name)
		}
		return result, err
	}
	result.Success += success
	result.SessionsImported += sessionsImported
	applog.LogInfof(c.ctx, "addImporterItems: committed success=%d skipped=%d failed=%d sessions=%d elapsed=%s total=%s", result.Success, result.Skipped, result.Failed, result.SessionsImported, time.Since(stepStartedAt), time.Since(startedAt))
	return result, nil
}

func AnnotateScanCandidate(candidate vo.BatchImportCandidate, idx Index, allowDuplicateMetadata bool) vo.BatchImportCandidate {
	candidate.ImportStatus = importStatusNew
	candidate.IsSelected = true

	pathsToCheck := candidate.Executables
	if len(pathsToCheck) == 0 && candidate.SelectedExe != "" {
		pathsToCheck = []string{candidate.SelectedExe}
	}
	for _, exePath := range pathsToCheck {
		if ref, ok := idx.FindByPathConflict(exePath); ok {
			candidate.ImportStatus = importStatusExistsPath
			candidate.IsSelected = false
			candidate.ExistingID = ref.ID
			candidate.ExistingName = ref.Name
			candidate.SkipReason = "路径已存在: " + ref.Name
			return candidate
		}
	}

	if ref, ok := idx.FindByPathConflict(candidate.SelectedExe); ok {
		candidate.ImportStatus = importStatusExistsPath
		candidate.IsSelected = false
		candidate.ExistingID = ref.ID
		candidate.ExistingName = ref.Name
		candidate.SkipReason = "路径已存在: " + ref.Name
		return candidate
	}

	if ref, ok := idx.FindByNamePath(candidate.SearchName, candidate.SelectedExe); ok {
		candidate.ImportStatus = importStatusExistsNamePath
		candidate.IsSelected = false
		candidate.ExistingID = ref.ID
		candidate.ExistingName = ref.Name
		candidate.SkipReason = "已存在: " + ref.Name
		return candidate
	}

	if !allowDuplicateMetadata {
		if ref, ok := idx.FindBySource(candidate.SourceType, candidate.SourceID); ok {
			candidate.ImportStatus = importStatusExistsSource
			candidate.IsSelected = false
			candidate.ExistingID = ref.ID
			candidate.ExistingName = ref.Name
			candidate.SkipReason = "元数据已存在: " + ref.Name
			return candidate
		}
	}

	if ref, ok := idx.FindByName(candidate.SearchName); ok && normalizeImportPath(ref.Path) != normalizeImportPath(candidate.SelectedExe) {
		candidate.ImportStatus = importStatusPossibleDuplicate
		candidate.ExistingID = ref.ID
		candidate.ExistingName = ref.Name
		candidate.SkipReason = "存在同名游戏: " + ref.Name
	}

	return candidate
}

func SplitScanCandidates(candidates []vo.BatchImportCandidate, idx Index, allowDuplicateMetadata ...bool) vo.BatchImportScanResult {
	result := vo.BatchImportScanResult{
		Candidates:        make([]vo.BatchImportCandidate, 0, len(candidates)),
		SkippedCandidates: make([]vo.BatchImportCandidate, 0),
		TotalDetected:     len(candidates),
	}

	allowDuplicate := false
	if len(allowDuplicateMetadata) > 0 {
		allowDuplicate = allowDuplicateMetadata[0]
	}

	for _, candidate := range candidates {
		annotated := AnnotateScanCandidate(candidate, idx, allowDuplicate)
		switch annotated.ImportStatus {
		case importStatusExistsPath, importStatusExistsNamePath, importStatusExistsSource:
			result.SkippedCandidates = append(result.SkippedCandidates, annotated)
		default:
			result.Candidates = append(result.Candidates, annotated)
		}
	}
	result.Skipped = len(result.SkippedCandidates)
	return result
}

// splitCommitItemsByAction 按动作把条目分成三组：
//
//   - createItems：新建条目
//   - updateItems：update_existing（对端自带完整元数据，文本字段照抄）
//   - syncItems：sync_merge（云同步，文本字段非空才覆盖）
//
// updateItems 与 syncItems 必须分开：两者的字段级守卫不同，走同一条 UPDATE
// 会让其中一方拿到错误的语义。
func splitCommitItemsByAction(items []CommitItem) (createItems, updateItems, syncItems []CommitItem) {
	createItems = make([]CommitItem, 0, len(items))
	for _, item := range items {
		switch item.Action {
		case ImportActionUpdateExisting:
			updateItems = append(updateItems, item)
		case ImportActionSyncMerge:
			syncItems = append(syncItems, item)
		case ImportActionCreate:
			createItems = append(createItems, item)
		}
	}
	return createItems, updateItems, syncItems
}

// commitItemsWithMetadata 过滤掉「只并会话」的动作：它们不去写
// game_metadata_sources / tags / favorites，避免同步把桌面端自己的元数据来源
// 覆盖掉。
func commitItemsWithMetadata(items []CommitItem) []CommitItem {
	filtered := make([]CommitItem, 0, len(items))
	for _, item := range items {
		if item.Action != ImportActionMergeSessions && item.Action != ImportActionSyncMerge {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (c *Committer) addImportedItems(ctx context.Context, conn *sql.Conn, items []CommitItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp_import_games`); err != nil {
		return 0, fmt.Errorf("drop temp_import_games: %w", err)
	}
	_, err := conn.ExecContext(ctx, `CREATE TEMP TABLE temp_import_games (
		id TEXT,
		name TEXT,
		aliases TEXT,
		cover_url TEXT,
		cover_source_url TEXT,
		company TEXT,
		summary TEXT,
		rating DOUBLE,
		release_date TEXT,
		status TEXT,
		path TEXT,
		game_directory TEXT,
		save_path TEXT,
		process_name TEXT,
		wine_runner TEXT,
		wine_args TEXT,
		wine_prefix TEXT,
		launch_mode TEXT,
		steam_launch_id TEXT,
		steam_launch_kind TEXT,
		steam_user_id TEXT,
		steam_launch_options TEXT,
		source_type TEXT,
		cached_at TIMESTAMPTZ,
		source_id TEXT,
		created_at TIMESTAMPTZ,
		updated_at TIMESTAMPTZ,
		use_locale_emulator BOOLEAN,
		use_magpie BOOLEAN,
		is_nsfw BOOLEAN,
		legacy_local_id TEXT,
		source_device_id TEXT,
		playtime_reset_at TIMESTAMPTZ,
		hidden BOOLEAN
	)`)
	if err != nil {
		return 0, fmt.Errorf("create temp_import_games: %w", err)
	}

	now := time.Now()
	if err := dbutils.AppendRows(ctx, conn, "", "temp_import_games", func(appender *duckdb.Appender) error {
		for i := range items {
			game := items[i].Game
			if game.ID == "" {
				game.ID = uuid.New().String()
			}
			if game.CreatedAt.IsZero() {
				game.CreatedAt = now
			}
			if game.CachedAt.IsZero() {
				game.CachedAt = now
			}
			if game.UpdatedAt.IsZero() {
				game.UpdatedAt = now
			}
			if game.SourceType == "" {
				game.SourceType = items[i].Source
			}
			game.SourceType, game.SourceID = gamehelper.NormalizeDefaultMetadataSource(game.SourceType, game.SourceID)
			if game.GameDirectory == "" {
				game.GameDirectory = gamehelper.DefaultGameDirectory(game.Path)
			}
			if game.CoverSourceURL == "" && gamehelper.IsDownloadableCoverURL(game.CoverURL) {
				game.CoverSourceURL = strings.TrimSpace(game.CoverURL)
			}
			game.LaunchMode = enums.NormalizeLaunchMode(game.LaunchMode)
			// 状态与别名都必须显式落库：漏了 status 时全部条目会拿到列默认值
			// 「未玩」（手机版备份里的 completed/dropped 全部丢失）；漏了 aliases
			// 则原文名/罗马字标题丢失，按标题匹配与展示都会退化。
			if game.Status == "" {
				game.Status = enums.StatusUnplayed
			}
			items[i].Game = game

			if err := appender.AppendRow(
				game.ID,
				game.Name,
				gamehelper.EncodeAliases(game.Aliases),
				game.CoverURL,
				game.CoverSourceURL,
				game.Company,
				game.Summary,
				game.Rating,
				game.ReleaseDate,
				string(game.Status),
				game.Path,
				game.GameDirectory,
				game.SavePath,
				game.ProcessName,
				game.WineRunner,
				game.WineArgs,
				game.WinePrefix,
				string(game.LaunchMode),
				game.SteamLaunchID,
				game.SteamLaunchKind,
				game.SteamUserID,
				game.SteamLaunchOptions,
				string(game.SourceType),
				game.CachedAt,
				game.SourceID,
				game.CreatedAt,
				game.UpdatedAt,
				game.UseLocaleEmulator,
				game.UseMagpie,
				game.IsNSFW,
				game.LegacyLocalID,
				game.SourceDeviceID,
				nullableTime(game.PlaytimeResetAt),
				// hidden 三态：快照没带这个键就写 NULL，插入路径用
				// COALESCE(hidden, FALSE) 兜成「不隐藏」。
				nullableBool(items[i].Hidden),
			); err != nil {
				return fmt.Errorf("append imported game %s: %w", game.Name, err)
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}

	if _, err := conn.ExecContext(ctx, `INSERT INTO games (
		id, name, aliases, cover_url, cover_source_url, company, summary, rating, release_date, status, path, game_directory,
		save_path, process_name, wine_runner, wine_args, wine_prefix, launch_mode,
		steam_launch_id, steam_launch_kind, steam_user_id, steam_launch_options,
		source_type, cached_at, source_id, created_at, updated_at,
		use_locale_emulator, use_magpie, is_nsfw,
		legacy_local_id, source_device_id, playtime_reset_at, hidden
	)
	SELECT
		id, name, aliases, cover_url, cover_source_url, company, summary, rating, release_date, status, path, game_directory,
		save_path, process_name, wine_runner, wine_args, wine_prefix, launch_mode,
		steam_launch_id, steam_launch_kind, steam_user_id, steam_launch_options,
		source_type, cached_at, source_id, created_at, updated_at,
		use_locale_emulator, use_magpie, is_nsfw,
		legacy_local_id, source_device_id, playtime_reset_at,
		-- 新建条目没有「保留本地值」可言，没带键就是「不隐藏」
		COALESCE(hidden, FALSE)
	FROM temp_import_games`); err != nil {
		return 0, fmt.Errorf("insert imported games from staging: %w", err)
	}

	return len(items), nil
}

// updateImportedItemMetadata 处理 update_existing：对端自带完整元数据（PotatoVN /
// Playnite 等），文本字段照抄即可。
func (c *Committer) updateImportedItemMetadata(ctx context.Context, conn *sql.Conn, items []CommitItem) (int, error) {
	return c.applyImportedItemMetadata(ctx, conn, items, false)
}

// updateImportedItemSyncMetadata 处理 sync_merge：对端是云同步 / WebDAV 快照。
//
// 与 update_existing 的唯一区别是「文本字段非空才覆盖」。原因是对端的来源可能
// 只是另一端的 games 段，压根没有 metadata_cache——那时 company / summary /
// rating / release_date 全为空串，照抄会把桌面端刮削好的资料冲成空白。
// 逐字段判空后，语义与手机版 importGamesJson 的 optString(key, 本地值) 一致。
func (c *Committer) updateImportedItemSyncMetadata(ctx context.Context, conn *sql.Conn, items []CommitItem) (int, error) {
	return c.applyImportedItemMetadata(ctx, conn, items, true)
}

func (c *Committer) applyImportedItemMetadata(ctx context.Context, conn *sql.Conn, items []CommitItem, syncSafe bool) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp_update_import_games`); err != nil {
		return 0, fmt.Errorf("drop temp_update_import_games: %w", err)
	}
	_, err := conn.ExecContext(ctx, `CREATE TEMP TABLE temp_update_import_games (
		id TEXT,
		name TEXT,
		aliases TEXT,
		status TEXT,
		-- 守卫比较用毫秒整数：Appender 与普通 Exec 写 TIMESTAMPTZ 的时区解释
		-- 不一致（实测 Appender 那条会被当成 UTC 墙钟时间），比时间戳会恒真。
		updated_at_ms BIGINT,
		cover_url TEXT,
		cover_source_url TEXT,
		company TEXT,
		summary TEXT,
		rating DOUBLE,
		release_date TEXT,
		source_type TEXT,
		cached_at TIMESTAMPTZ,
		source_id TEXT,
		updated_at TIMESTAMPTZ,
		is_nsfw BOOLEAN,
		legacy_local_id TEXT,
		source_device_id TEXT,
		playtime_reset_at TIMESTAMPTZ,
		hidden BOOLEAN
	)`)
	if err != nil {
		return 0, fmt.Errorf("create temp_update_import_games: %w", err)
	}

	now := time.Now()
	inserted := 0
	if err := dbutils.AppendRows(ctx, conn, "", "temp_update_import_games", func(appender *duckdb.Appender) error {
		for i := range items {
			game := items[i].Game
			if game.ID == "" {
				continue
			}
			if game.CachedAt.IsZero() {
				game.CachedAt = now
			}
			if game.UpdatedAt.IsZero() {
				game.UpdatedAt = now
			}
			if game.SourceType == "" {
				game.SourceType = items[i].Source
			}
			game.SourceType, game.SourceID = gamehelper.NormalizeDefaultMetadataSource(game.SourceType, game.SourceID)
			if game.CoverSourceURL == "" && gamehelper.IsDownloadableCoverURL(game.CoverURL) {
				game.CoverSourceURL = strings.TrimSpace(game.CoverURL)
			}
			items[i].Game = game
			// 这里**不做**空值兜底：空状态代表「本次导入没带状态信息」
			// （PotatoVN / Playnite 这类格式就没有状态概念），下面的守卫靠它
			// 区分「没信息」与「明确未玩」。新建条目才需要在插入路径补默认值。
			if err := appender.AppendRow(
				game.ID,
				game.Name,
				gamehelper.EncodeAliases(game.Aliases),
				string(game.Status),
				game.UpdatedAt.UnixMilli(),
				game.CoverURL,
				game.CoverSourceURL,
				game.Company,
				game.Summary,
				game.Rating,
				game.ReleaseDate,
				string(game.SourceType),
				game.CachedAt,
				game.SourceID,
				game.UpdatedAt,
				game.IsNSFW,
				game.LegacyLocalID,
				game.SourceDeviceID,
				nullableTime(game.PlaytimeResetAt),
				// 更新路径必须保留「没带键」这个信息：NULL = 不覆盖本地值。
				nullableBool(items[i].Hidden),
			); err != nil {
				return fmt.Errorf("append import metadata update %s: %w", game.Name, err)
			}
			inserted++
		}
		return nil
	}); err != nil {
		return inserted, err
	}

	if inserted == 0 {
		return 0, nil
	}

	// 状态与别名单独一条语句，且必须跑在下面那条之前：
	// 手机版 importGamesJson 的规则是「对端 updated_at 不早于本地才覆盖状态」，
	// 而下面的语句会顺手把 games.updated_at 改成对端的值，之后再比就永远成立。
	// 也不能写成同一条 UPDATE 里的 CASE——`UPDATE ... FROM` 中同语句引用
	// 目标列的新旧值语义容易踩坑（实测会被当成已更新的值）。
	// 「非空 + 对端不早于本地」才覆盖，两条规则都要：
	//   * 非空：PotatoVN / Playnite 这类导入格式不带状态，空值等于「没有信息」，
	//     照抄会把用户标好的「已通关」冲成「未玩」（别名同理）；
	//   * 时间：与手机版 importGamesJson 对齐，旧快照不得覆盖本地更新。
	if _, err := conn.ExecContext(ctx, `
		UPDATE games
		SET status = temp_update_import_games.status
		FROM temp_update_import_games
		WHERE games.id = temp_update_import_games.id
		  AND TRIM(COALESCE(temp_update_import_games.status, '')) <> ''
		  AND temp_update_import_games.updated_at_ms >= epoch_ms(games.updated_at)
	`); err != nil {
		return inserted, fmt.Errorf("update imported game status: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `
		UPDATE games
		SET aliases = temp_update_import_games.aliases
		FROM temp_update_import_games
		WHERE games.id = temp_update_import_games.id
		  AND COALESCE(temp_update_import_games.aliases, '') NOT IN ('', '[]')
		  AND temp_update_import_games.updated_at_ms >= epoch_ms(games.updated_at)
	`); err != nil {
		return inserted, fmt.Errorf("update imported game aliases: %w", err)
	}

	nsfwSourcePlaceholders, nsfwSourceArgs := nsfwAuthoritativeSourceFilter()
	// syncSafe 下所有文本字段都是「非空才覆盖」：对端可能只带 games 段、没有任何
	// metadata_cache，那时这些列全是空串，照抄会把本地刮削好的资料冲成空白。
	textOr := func(column string) string {
		if !syncSafe {
			return "temp_update_import_games." + column
		}
		return fmt.Sprintf(
			"CASE WHEN temp_update_import_games.%s <> '' THEN temp_update_import_games.%s ELSE games.%s END",
			column, column, column,
		)
	}
	ratingExpr := "temp_update_import_games.rating"
	if syncSafe {
		ratingExpr = "CASE WHEN temp_update_import_games.rating > 0 THEN temp_update_import_games.rating ELSE games.rating END"
	}
	// 来源只在「对端确实带了元数据」时改写：convertYukiHubGame 在没有
	// metadata_cache 时给的是 local，采信它会把桌面端已有来源抹成 local。
	sourceGuard := "TRUE"
	sourceExpr := func(column string) string {
		if !syncSafe {
			return "temp_update_import_games." + column
		}
		return fmt.Sprintf(
			"CASE WHEN %s THEN temp_update_import_games.%s ELSE games.%s END",
			sourceGuard, column, column,
		)
	}
	if syncSafe {
		sourceGuard = "temp_update_import_games.source_type NOT IN ('', 'local')"
	}
	// 清零时间只允许「对端更晚且非空」时前进：手机版同款规则，避免旧快照把
	// 清零退回 0（退回会让已被有意排除的历史时长重新计入统计）。
	resetExpr := "temp_update_import_games.playtime_reset_at"
	if syncSafe {
		resetExpr = `CASE
			WHEN temp_update_import_games.playtime_reset_at IS NOT NULL
			 AND (games.playtime_reset_at IS NULL
			      OR temp_update_import_games.playtime_reset_at > games.playtime_reset_at)
			THEN temp_update_import_games.playtime_reset_at
			ELSE games.playtime_reset_at
		END`
	}

	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`
		UPDATE games
		SET
			name = %s,
			cover_url = CASE
				WHEN temp_update_import_games.cover_url <> '' THEN temp_update_import_games.cover_url
				ELSE games.cover_url
			END,
			cover_source_url = CASE
				WHEN temp_update_import_games.cover_source_url <> '' THEN temp_update_import_games.cover_source_url
				ELSE games.cover_source_url
			END,
			company = %s,
			summary = %s,
			rating = %s,
			release_date = %s,
			source_type = %s,
			cached_at = %s,
			source_id = %s,
			updated_at = temp_update_import_games.updated_at,
			-- 只有「会给出可信 NSFW 标记」的来源才采信对端的值，名单由
			-- gamehelper.NSFWAuthoritativeSources 生成（原先硬编码
			-- IN ('bangumi','vndb')，新增 hikarinagi / nextmoe 后没跟上，
			-- 这两个来源的游戏合并导入时 NSFW 永远不会更新）。
			is_nsfw = CASE
				WHEN temp_update_import_games.source_type IN (%s) THEN temp_update_import_games.is_nsfw
				ELSE games.is_nsfw
			END,
			-- 身份键与设备标识「非空才覆盖」，避免用空值抹掉桌面端已有的来源信息。
			legacy_local_id = CASE
				WHEN temp_update_import_games.legacy_local_id <> '' THEN temp_update_import_games.legacy_local_id
				ELSE games.legacy_local_id
			END,
			source_device_id = CASE
				WHEN temp_update_import_games.source_device_id <> '' THEN temp_update_import_games.source_device_id
				ELSE games.source_device_id
			END,
			-- 清零时间以权威源（Android）为准。
			playtime_reset_at = %s,
			-- 隐藏标记：**对端明确带了键才覆盖**（手机版 optBoolean("hidden", 本地值)）。
			-- 快照缺这个键时暂存表里是 NULL → 保留本地，否则一次同步会把
			-- 本地藏好的游戏全部翻出来（老快照与只带 games 段的快照都不带这个键）。
			hidden = CASE
				WHEN temp_update_import_games.hidden IS NULL THEN games.hidden
				ELSE temp_update_import_games.hidden
			END
		FROM temp_update_import_games
		WHERE games.id = temp_update_import_games.id
	`,
		textOr("name"),
		textOr("company"),
		textOr("summary"),
		ratingExpr,
		textOr("release_date"),
		sourceExpr("source_type"),
		sourceExpr("cached_at"),
		sourceExpr("source_id"),
		nsfwSourcePlaceholders,
		resetExpr,
	), nsfwSourceArgs...); err != nil {
		return inserted, fmt.Errorf("update imported game metadata from staging: %w", err)
	}

	return inserted, nil
}

// nsfwAuthoritativeSourceFilter 生成 SQL `IN (...)` 需要的占位符与参数。
//
// 来源名单取自 gamehelper.NSFWAuthoritativeSources()，这样「Go 侧判定」与
// 「SQL 侧判定」用的是同一个集合，不会像上游那样两处各写一份再漏项。
func nsfwAuthoritativeSourceFilter() (string, []any) {
	sources := gamehelper.NSFWAuthoritativeSources()
	placeholders := make([]string, 0, len(sources))
	args := make([]any, 0, len(sources))
	for _, source := range sources {
		placeholders = append(placeholders, "?")
		args = append(args, string(source))
	}
	return strings.Join(placeholders, ", "), args
}

func (c *Committer) upsertImportedItemMetadataSources(ctx context.Context, conn *sql.Conn, items []CommitItem) (int, error) {
	now := time.Now()
	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp_import_metadata_sources`); err != nil {
		return 0, fmt.Errorf("drop temp_import_metadata_sources: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TEMP TABLE temp_import_metadata_sources (
		game_id TEXT,
		source_type TEXT,
		source_id TEXT,
		cache_json TEXT,
		cached_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ,
		updated_at TIMESTAMPTZ,
		is_default BOOLEAN
	)`); err != nil {
		return 0, fmt.Errorf("create temp_import_metadata_sources: %w", err)
	}

	count := 0
	err := dbutils.AppendRows(ctx, conn, "", "temp_import_metadata_sources", func(appender *duckdb.Appender) error {
		for _, item := range items {
			game := item.Game
			sources := append([]models.GameMetadataSource(nil), game.MetadataSources...)
			if len(sources) == 0 && game.SourceType != "" && game.SourceType != enums.Local && strings.TrimSpace(game.SourceID) != "" {
				sources = append(sources, models.GameMetadataSource{SourceType: game.SourceType, SourceID: game.SourceID})
			}

			defaultSource := gamehelper.NormalizeMetadataSourceType(game.SourceType)
			if defaultSource == enums.Local {
				defaultSource = ""
			}
			seen := make(map[enums.SourceType]struct{}, len(sources))
			for _, source := range sources {
				sourceType, sourceID, normalizeErr := gamehelper.NormalizeMetadataSource(source.SourceType, source.SourceID)
				if normalizeErr != nil {
					continue
				}
				if _, exists := seen[sourceType]; exists {
					continue
				}
				seen[sourceType] = struct{}{}

				cachedAt := source.CachedAt
				if cachedAt.IsZero() {
					cachedAt = game.CachedAt
				}
				if cachedAt.IsZero() {
					cachedAt = now
				}
				if err := appender.AppendRow(
					game.ID,
					string(sourceType),
					sourceID,
					source.CacheJSON,
					cachedAt,
					now,
					now,
					sourceType == defaultSource,
				); err != nil {
					return fmt.Errorf("append imported metadata source %s for %s: %w", sourceType, game.Name, err)
				}
				count++
			}
		}
		return nil
	})
	if err != nil {
		return count, err
	}
	if count == 0 {
		return 0, nil
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO game_metadata_sources (
			game_id, source_type, source_id, cache_json, cached_at, created_at, updated_at
		)
		SELECT game_id, source_type, source_id, cache_json, cached_at, created_at, updated_at
		FROM temp_import_metadata_sources
		ON CONFLICT (game_id, source_type) DO UPDATE SET
			source_id = EXCLUDED.source_id,
			-- 空负载不覆盖已有缓存：没有 metadata_cache 元素的来源只是身份信息，
			-- 用它清空对端已有的缓存属于破坏性写入。
			cache_json = CASE
				WHEN EXCLUDED.cache_json <> '' THEN EXCLUDED.cache_json
				ELSE game_metadata_sources.cache_json
			END,
			cached_at = EXCLUDED.cached_at,
			updated_at = EXCLUDED.updated_at
	`); err != nil {
		return count, fmt.Errorf("upsert imported metadata sources from staging: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `
		UPDATE games AS game
		SET source_type = source.source_type,
			source_id = source.source_id
		FROM temp_import_metadata_sources AS source
		WHERE game.id = source.game_id
		  AND source.is_default
	`); err != nil {
		return count, fmt.Errorf("set imported default metadata sources from staging: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `
		DELETE FROM sync_tombstones
		WHERE entity_type = ?
		  AND EXISTS (
			SELECT 1
			FROM temp_import_metadata_sources AS source
			WHERE sync_tombstones.entity_id = source.game_id || '::' || source.source_type
		  )
	`, cloudsync.EntityGameMetadataSource); err != nil {
		return count, fmt.Errorf("delete imported metadata source tombstones from staging: %w", err)
	}
	return count, nil
}

func (c *Committer) updateImportedItemLocalLaunchFields(ctx context.Context, conn *sql.Conn, items []CommitItem) (int, error) {
	hasUpdates := false
	for _, item := range items {
		if item.UpdateLocalLaunchFields && item.Game.ID != "" {
			hasUpdates = true
			break
		}
	}
	if !hasUpdates {
		return 0, nil
	}

	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp_update_import_launch_fields`); err != nil {
		return 0, fmt.Errorf("drop temp_update_import_launch_fields: %w", err)
	}
	_, err := conn.ExecContext(ctx, `CREATE TEMP TABLE temp_update_import_launch_fields (
		id TEXT,
		path TEXT,
		game_directory TEXT,
		process_name TEXT,
		launch_mode TEXT,
		steam_launch_id TEXT,
		steam_launch_kind TEXT,
		steam_user_id TEXT
	)`)
	if err != nil {
		return 0, fmt.Errorf("create temp_update_import_launch_fields: %w", err)
	}

	inserted := 0
	if err := dbutils.AppendRows(ctx, conn, "", "temp_update_import_launch_fields", func(appender *duckdb.Appender) error {
		for _, item := range items {
			if !item.UpdateLocalLaunchFields || item.Game.ID == "" {
				continue
			}
			game := item.Game
			if game.GameDirectory == "" {
				game.GameDirectory = gamehelper.DefaultGameDirectory(game.Path)
			}
			game.LaunchMode = enums.NormalizeLaunchMode(game.LaunchMode)
			if err := appender.AppendRow(
				game.ID,
				game.Path,
				game.GameDirectory,
				game.ProcessName,
				string(game.LaunchMode),
				game.SteamLaunchID,
				game.SteamLaunchKind,
				game.SteamUserID,
			); err != nil {
				return fmt.Errorf("append import launch field update %s: %w", game.Name, err)
			}
			inserted++
		}
		return nil
	}); err != nil {
		return inserted, err
	}

	if inserted == 0 {
		return 0, nil
	}

	if _, err := conn.ExecContext(ctx, `
		UPDATE games
		SET
			path = temp_update_import_launch_fields.path,
			game_directory = temp_update_import_launch_fields.game_directory,
			process_name = temp_update_import_launch_fields.process_name,
			launch_mode = temp_update_import_launch_fields.launch_mode,
			steam_launch_id = temp_update_import_launch_fields.steam_launch_id,
			steam_launch_kind = temp_update_import_launch_fields.steam_launch_kind,
			steam_user_id = temp_update_import_launch_fields.steam_user_id
		FROM temp_update_import_launch_fields
		WHERE games.id = temp_update_import_launch_fields.id
		  AND COALESCE(TRIM(games.path), '') = ''
	`); err != nil {
		return inserted, fmt.Errorf("update imported game launch fields from staging: %w", err)
	}

	return inserted, nil
}

func (c *Committer) addImportedItemTags(ctx context.Context, conn *sql.Conn, items []CommitItem) (int, error) {
	total := 0
	for _, item := range items {
		total += len(item.Tags)
	}
	if total == 0 {
		return 0, nil
	}

	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp_import_game_tags`); err != nil {
		return 0, fmt.Errorf("drop temp_import_game_tags: %w", err)
	}
	_, err := conn.ExecContext(ctx, `CREATE TEMP TABLE temp_import_game_tags (
		id TEXT,
		game_id TEXT,
		name TEXT,
		source TEXT,
		weight DOUBLE,
		is_spoiler BOOLEAN,
		created_at TIMESTAMPTZ,
		updated_at TIMESTAMPTZ
	)`)
	if err != nil {
		return 0, fmt.Errorf("create temp_import_game_tags: %w", err)
	}

	now := time.Now()
	inserted := 0
	if err := dbutils.AppendRows(ctx, conn, "", "temp_import_game_tags", func(appender *duckdb.Appender) error {
		for _, item := range items {
			for _, tag := range item.Tags {
				name := strings.TrimSpace(tag.Name)
				source := strings.TrimSpace(tag.Source)
				if name == "" {
					continue
				}
				if source == "" {
					source = "user"
				}
				id := uuid.New().String()
				if err := appender.AppendRow(id, item.Game.ID, name, source, tag.Weight, tag.IsSpoiler, now, now); err != nil {
					return fmt.Errorf("append imported tag %s for %s: %w", name, item.Game.Name, err)
				}
				inserted++
			}
		}
		return nil
	}); err != nil {
		return inserted, err
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO game_tags (id, game_id, name, source, weight, is_spoiler, created_at, updated_at)
		SELECT id, game_id, name, source, weight, is_spoiler, created_at, updated_at
		FROM temp_import_game_tags
		ON CONFLICT (game_id, name, source) DO UPDATE SET
			id = EXCLUDED.id,
			weight = EXCLUDED.weight,
			is_spoiler = EXCLUDED.is_spoiler,
			updated_at = EXCLUDED.updated_at
	`); err != nil {
		return inserted, fmt.Errorf("insert imported tags from staging: %w", err)
	}
	return inserted, nil
}

// addImportedItemFavorites 把标记为收藏的条目写入「收藏」系统分类。
//
// 桌面端没有游戏级 favorite 列，收藏关系存在 game_categories；导入方向此前完全
// 丢弃快照的 favorite，导致「桌面端导出 → 手机版 → 回导」会丢失收藏，与导出方向不对称。
// 这里只做「加收藏」，不做「取消收藏」：快照里的 favorite=false 可能是对端未标记，
// 也可能只是本轮未同步，删除既有收藏属于破坏性操作，按合并语义「只增不减」处理。
func (c *Committer) addImportedItemFavorites(ctx context.Context, conn *sql.Conn, items []CommitItem) (int, error) {
	total := 0
	for _, item := range items {
		if item.Favorite && item.Game.ID != "" {
			total++
		}
	}
	if total == 0 {
		return 0, nil
	}

	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp_import_game_favorites`); err != nil {
		return 0, fmt.Errorf("drop temp_import_game_favorites: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TEMP TABLE temp_import_game_favorites (
		game_id TEXT,
		category_id TEXT,
		updated_at TIMESTAMPTZ
	)`); err != nil {
		return 0, fmt.Errorf("create temp_import_game_favorites: %w", err)
	}

	now := time.Now()
	inserted := 0
	if err := dbutils.AppendRows(ctx, conn, "", "temp_import_game_favorites", func(appender *duckdb.Appender) error {
		for _, item := range items {
			if !item.Favorite || item.Game.ID == "" {
				continue
			}
			if err := appender.AppendRow(item.Game.ID, gamehelper.SystemFavoritesCategoryID, now); err != nil {
				return fmt.Errorf("append imported favorite for %s: %w", item.Game.Name, err)
			}
			inserted++
		}
		return nil
	}); err != nil {
		return inserted, err
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO game_categories (game_id, category_id, updated_at)
		SELECT game_id, category_id, updated_at
		FROM temp_import_game_favorites
		ON CONFLICT (game_id, category_id) DO NOTHING
	`); err != nil {
		return inserted, fmt.Errorf("insert imported favorites from staging: %w", err)
	}
	return inserted, nil
}

func (c *Committer) addImportedItemSessions(ctx context.Context, conn *sql.Conn, items []CommitItem) (int, error) {
	total := 0
	for _, item := range items {
		total += len(item.Sessions)
	}
	if total == 0 {
		return 0, nil
	}

	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp_import_play_sessions`); err != nil {
		return 0, fmt.Errorf("drop temp_import_play_sessions: %w", err)
	}
	_, err := conn.ExecContext(ctx, `CREATE TEMP TABLE temp_import_play_sessions (
		id TEXT,
		game_id TEXT,
		start_time TIMESTAMPTZ,
		end_time TIMESTAMPTZ,
		duration INTEGER,
		updated_at TIMESTAMPTZ
	)`)
	if err != nil {
		return 0, fmt.Errorf("create temp_import_play_sessions: %w", err)
	}

	now := time.Now()
	inserted := 0
	if err := dbutils.AppendRows(ctx, conn, "", "temp_import_play_sessions", func(appender *duckdb.Appender) error {
		for itemIndex := range items {
			for sessionIndex := range items[itemIndex].Sessions {
				session := items[itemIndex].Sessions[sessionIndex]
				if session.ID == "" {
					session.ID = uuid.New().String()
				}
				if session.GameID == "" {
					session.GameID = items[itemIndex].Game.ID
				}
				if session.UpdatedAt.IsZero() {
					session.UpdatedAt = now
				}
				items[itemIndex].Sessions[sessionIndex] = session
				if err := appender.AppendRow(session.ID, session.GameID, session.StartTime, session.EndTime, int64(session.Duration), session.UpdatedAt); err != nil {
					return fmt.Errorf("append imported session for %s: %w", items[itemIndex].Game.Name, err)
				}
				inserted++
			}
		}
		return nil
	}); err != nil {
		return inserted, err
	}

	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp_import_play_sessions_dedup`); err != nil {
		return inserted, fmt.Errorf("drop temp_import_play_sessions_dedup: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TEMP TABLE temp_import_play_sessions_dedup AS
		SELECT id, game_id, start_time, end_time, duration, updated_at
		FROM (
			SELECT
				id,
				game_id,
				start_time,
				end_time,
				duration,
				updated_at,
				ROW_NUMBER() OVER (
					PARTITION BY game_id, start_time, end_time
					ORDER BY updated_at DESC, id
				) AS row_num
			FROM temp_import_play_sessions
		)
		WHERE row_num = 1
	`); err != nil {
		return inserted, fmt.Errorf("deduplicate imported sessions: %w", err)
	}

	var toInsert int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM temp_import_play_sessions_dedup t
		WHERE NOT EXISTS (
			SELECT 1
			FROM play_sessions p
			WHERE p.game_id = t.game_id
			  AND p.start_time = t.start_time
			  AND p.end_time = t.end_time
		)
	`).Scan(&toInsert); err != nil {
		return inserted, fmt.Errorf("count deduplicated imported sessions: %w", err)
	}
	if toInsert == 0 {
		return 0, nil
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at)
		SELECT id, game_id, start_time, end_time, duration, updated_at
		FROM temp_import_play_sessions_dedup t
		WHERE NOT EXISTS (
			SELECT 1
			FROM play_sessions p
			WHERE p.game_id = t.game_id
			  AND p.start_time = t.start_time
			  AND p.end_time = t.end_time
		)
	`); err != nil {
		return inserted, fmt.Errorf("insert imported sessions from staging: %w", err)
	}
	return toInsert, nil
}

func (c *Committer) deleteImportedItemTombstones(ctx context.Context, conn *sql.Conn, items []CommitItem) error {
	if len(items) == 0 {
		return nil
	}
	if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS temp_import_tombstones`); err != nil {
		return fmt.Errorf("drop temp_import_tombstones: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TEMP TABLE temp_import_tombstones (
		entity_type TEXT,
		entity_id TEXT
	)`); err != nil {
		return fmt.Errorf("create temp_import_tombstones: %w", err)
	}

	count := 0
	if err := dbutils.AppendRows(ctx, conn, "", "temp_import_tombstones", func(appender *duckdb.Appender) error {
		for _, item := range items {
			if item.Action != ImportActionMergeSessions {
				if item.Game.ID != "" {
					if err := appender.AppendRow(cloudsync.EntityGame, item.Game.ID); err != nil {
						return fmt.Errorf("append game tombstone %s: %w", item.Game.ID, err)
					}
					count++
				}
				for _, tag := range item.Tags {
					name := strings.TrimSpace(tag.Name)
					source := strings.TrimSpace(tag.Source)
					if name == "" {
						continue
					}
					if source == "" {
						source = "user"
					}
					if err := appender.AppendRow(cloudsync.EntityGameTag, cloudsync.TagTombstoneID(item.Game.ID, source, name)); err != nil {
						return fmt.Errorf("append tag tombstone %s/%s: %w", item.Game.ID, name, err)
					}
					count++
				}
				for _, source := range item.Game.MetadataSources {
					if source.SourceType == "" || strings.TrimSpace(source.SourceID) == "" {
						continue
					}
					if err := appender.AppendRow(cloudsync.EntityGameMetadataSource, cloudsync.MetadataSourceTombstoneID(item.Game.ID, string(source.SourceType))); err != nil {
						return fmt.Errorf("append metadata source tombstone %s/%s: %w", item.Game.ID, source.SourceType, err)
					}
					count++
				}
			}
			for _, session := range item.Sessions {
				if session.ID == "" {
					continue
				}
				if err := appender.AppendRow(cloudsync.EntityPlaySession, session.ID); err != nil {
					return fmt.Errorf("append session tombstone %s: %w", session.ID, err)
				}
				count++
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	if _, err := conn.ExecContext(ctx, `
		DELETE FROM sync_tombstones
		WHERE EXISTS (
			SELECT 1
			FROM temp_import_tombstones t
			WHERE t.entity_type = sync_tombstones.entity_type
			  AND t.entity_id = sync_tombstones.entity_id
		)
	`); err != nil {
		return fmt.Errorf("delete import tombstones from staging: %w", err)
	}
	return nil
}

func (c *Committer) CommitItems(items []CommitItem) (int, int, error) {
	if len(items) == 0 {
		return 0, 0, nil
	}

	startedAt := time.Now()
	applog.LogInfof(c.ctx, "commitImportedItems: start items=%d", len(items))

	conn, err := c.db.Conn(c.ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("获取导入数据库连接失败: %w", err)
	}
	defer conn.Close()
	defer CleanupStagingTables(c.ctx, conn)

	if _, err := conn.ExecContext(c.ctx, `BEGIN TRANSACTION`); err != nil {
		return 0, 0, fmt.Errorf("开始导入事务失败: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(c.ctx, `ROLLBACK`)
		}
	}()

	createItems, updateItems, syncItems := splitCommitItemsByAction(items)
	metadataItems := commitItemsWithMetadata(items)

	stepStartedAt := time.Now()
	insertedGames, err := c.addImportedItems(c.ctx, conn, createItems)
	if err != nil {
		return 0, 0, err
	}
	applog.LogInfof(c.ctx, "commitImportedItems: staged and inserted games=%d elapsed=%s", insertedGames, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	updatedGames, err := c.updateImportedItemMetadata(c.ctx, conn, updateItems)
	if err != nil {
		return insertedGames, 0, err
	}
	applog.LogInfof(c.ctx, "commitImportedItems: staged and updated games=%d elapsed=%s", updatedGames, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	syncedGames, err := c.updateImportedItemSyncMetadata(c.ctx, conn, syncItems)
	if err != nil {
		return insertedGames + updatedGames, 0, err
	}
	applog.LogInfof(c.ctx, "commitImportedItems: staged and sync-merged games=%d elapsed=%s", syncedGames, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	updatedLaunchFields, err := c.updateImportedItemLocalLaunchFields(c.ctx, conn, updateItems)
	if err != nil {
		return insertedGames + updatedGames, 0, err
	}
	applog.LogInfof(c.ctx, "commitImportedItems: staged local launch field updates=%d elapsed=%s", updatedLaunchFields, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	upsertedSources, err := c.upsertImportedItemMetadataSources(c.ctx, conn, metadataItems)
	if err != nil {
		return insertedGames + updatedGames, 0, err
	}
	applog.LogInfof(c.ctx, "commitImportedItems: upserted metadata sources=%d elapsed=%s", upsertedSources, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	insertedTags, err := c.addImportedItemTags(c.ctx, conn, metadataItems)
	if err != nil {
		return insertedGames + updatedGames, 0, err
	}
	applog.LogInfof(c.ctx, "commitImportedItems: staged and upserted tags=%d elapsed=%s", insertedTags, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	insertedSessions, err := c.addImportedItemSessions(c.ctx, conn, items)
	if err != nil {
		return insertedGames + updatedGames, 0, err
	}
	applog.LogInfof(c.ctx, "commitImportedItems: staged and inserted sessions=%d elapsed=%s", insertedSessions, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	insertedFavorites, err := c.addImportedItemFavorites(c.ctx, conn, items)
	if err != nil {
		return insertedGames + updatedGames, insertedSessions, err
	}
	applog.LogInfof(c.ctx, "commitImportedItems: staged and inserted favorites=%d elapsed=%s", insertedFavorites, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	if err := c.deleteImportedItemTombstones(c.ctx, conn, items); err != nil {
		return insertedGames + updatedGames, insertedSessions, err
	}
	applog.LogInfof(c.ctx, "commitImportedItems: deleted sync tombstones elapsed=%s", time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	if _, err := conn.ExecContext(c.ctx, `COMMIT`); err != nil {
		return 0, 0, fmt.Errorf("提交导入事务失败: %w", err)
	}
	committed = true
	applog.LogInfof(c.ctx, "commitImportedItems: committed elapsed=%s total=%s", time.Since(stepStartedAt), time.Since(startedAt))
	if err := dbutils.CheckpointDuckDB(c.ctx, c.db); err != nil {
		applog.LogWarningf(c.ctx, "commitImportedItems: checkpoint failed; committed import remains in WAL: %v", err)
	} else {
		applog.LogInfof(c.ctx, "commitImportedItems: checkpoint completed total=%s", time.Since(startedAt))
	}

	c.startImportCoverProcessing(metadataItems)
	// 计数必须逐类相加：create / update_existing / sync_merge 三类各自返回写入
	// 行数，merge_sessions 不在上面任何一类里（只并会话），单独数一遍。
	// 早先用的是 `len(items) - len(metadataItems)` 兜住 merge_sessions，加入
	// sync_merge 后这一项会把 sync_merge 重复计入。
	sessionsOnly := 0
	for _, item := range items {
		if item.Action == ImportActionMergeSessions {
			sessionsOnly++
		}
	}
	return insertedGames + updatedGames + syncedGames + sessionsOnly, insertedSessions, nil
}

func CleanupStagingTables(ctx context.Context, conn *sql.Conn) {
	for _, tableName := range stagingTableNames() {
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tableName)); err != nil {
			applog.LogWarningf(ctx, "cleanupImportStagingTables: failed to drop %s: %v", tableName, err)
		}
	}
}

func stagingTableNames() []string {
	return []string{
		"temp_import_games",
		"temp_update_import_games",
		"temp_import_metadata_sources",
		"temp_update_import_launch_fields",
		"temp_import_game_tags",
		"temp_import_game_favorites",
		"temp_import_play_sessions",
		"temp_import_play_sessions_dedup",
		"temp_import_tombstones",
	}
}

func (c *Committer) startImportCoverProcessing(items []CommitItem) {
	if c.startCoverDownloads == nil && c.updateCoverURL == nil {
		return
	}

	imageItems := make([]CoverDownloadItem, 0)
	loaderJobs := make([]CommitItem, 0)
	for _, item := range items {
		if gamehelper.IsDownloadableCoverURL(item.Game.CoverURL) {
			imageItems = append(imageItems, CoverDownloadItem{
				GameID:   item.Game.ID,
				GameName: item.Game.Name,
				CoverURL: item.Game.CoverURL,
			})
		}
		if item.CoverLoader != nil {
			loaderJobs = append(loaderJobs, item)
		}
	}

	if len(imageItems) > 0 {
		if c.startCoverDownloads != nil {
			taskID := c.startCoverDownloads(imageItems)
			if strings.TrimSpace(taskID) == "" {
				applog.LogWarningf(c.ctx, "import cover processing: failed to create image download task")
			}
		} else {
			applog.LogWarningf(c.ctx, "import cover processing: image download task starter is not initialized")
		}
	}

	if len(loaderJobs) == 0 {
		return
	}

	workerCount := importCoverWorkerCount
	if len(loaderJobs) < workerCount {
		workerCount = len(loaderJobs)
	}

	go func() {
		startedAt := time.Now()
		applog.LogInfof(c.ctx, "import cover processing: start loader_jobs=%d workers=%d", len(loaderJobs), workerCount)

		jobCh := make(chan CommitItem)
		var wg sync.WaitGroup
		for i := 0; i < workerCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for item := range jobCh {
					c.processImportCover(item)
				}
			}()
		}
		for _, item := range loaderJobs {
			jobCh <- item
		}
		close(jobCh)
		wg.Wait()

		applog.LogInfof(c.ctx, "import cover processing: complete loader_jobs=%d elapsed=%s", len(loaderJobs), time.Since(startedAt))
	}()
}

func (c *Committer) processImportCover(item CommitItem) {
	if item.CoverLoader == nil {
		return
	}

	coverURL, err := item.CoverLoader(item.Game)
	if err != nil {
		applog.LogWarningf(c.ctx, "import cover processing failed for %s: %v", item.Game.Name, err)
		return
	}
	if coverURL == "" {
		return
	}
	if c.updateCoverURL != nil {
		if err := c.updateCoverURL(item.Game.ID, coverURL); err != nil {
			applog.LogWarningf(c.ctx, "import cover update failed for %s: %v", item.Game.Name, err)
		}
	}
}
