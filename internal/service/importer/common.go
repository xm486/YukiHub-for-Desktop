package importer

import (
	"context"
	"fmt"
	"strings"
	"time"
	"yukihub/internal/applog"
	"yukihub/internal/common/enums"
	"yukihub/internal/common/importpath"
	"yukihub/internal/common/vo"
	"yukihub/internal/models"
	"yukihub/internal/utils/metadata"
)

// ImportResult 导入结果
type ImportResult struct {
	Success          int      `json:"success"`           // 成功导入数量
	Skipped          int      `json:"skipped"`           // 跳过数量（已存在）
	Failed           int      `json:"failed"`            // 失败数量
	FailedNames      []string `json:"failed_names"`      // 失败的游戏名称
	SkippedNames     []string `json:"skipped_names"`     // 跳过的游戏名称
	SessionsImported int      `json:"sessions_imported"` // 导入的游玩记录数量
}

// PreviewGame 预览导入的游戏信息
type PreviewGame struct {
	Name         string    `json:"name"`
	Developer    string    `json:"developer"`
	SourceType   string    `json:"source_type"`
	SourceID     string    `json:"source_id"`
	Path         string    `json:"path"`
	Exists       bool      `json:"exists"`
	ConflictType string    `json:"conflict_type"`
	ExistingID   string    `json:"existing_id"`
	ExistingName string    `json:"existing_name"`
	AddTime      time.Time `json:"add_time"`
	HasPath      bool      `json:"has_path"`
}

type Dependencies struct {
	Ctx                          context.Context
	ListGames                    func() ([]models.Game, error)
	AddGame                      func(vo.GameMetadataFromWebVO) error
	AddItems                     func([]ImportItem) (ImportResult, error)
	AddSessions                  func([]models.PlaySession) error
	AllowDuplicateMetadataImport bool
}

type ImportItem struct {
	Source                  vo.GameMetadataFromWebVO
	Sessions                []models.PlaySession
	DisplayName             string
	Path                    string
	Action                  string
	ExistingGameID          string
	UpdateLocalLaunchFields bool
	// Favorite 为真时把游戏加入「收藏」系统分类。桌面端没有游戏级收藏列，
	// 收藏由 game_categories 承载，因此不能塞进 models.Game。
	Favorite bool
	// Hidden 是三态：nil = 快照**没带**这个键（保留本地原值），
	// 非 nil = 对端明确表态（true 隐藏 / false 取消隐藏）。
	//
	// 与 Favorite 同理，不能塞进 models.Game —— 那里的 bool 表达不了「没信息」，
	// 而手机版是 `optBoolean("hidden", 本地值)`：缺键必须保留，
	// 否则一次合并导入就把本地藏好的游戏全部翻出来。
	// 其它导入格式（PotatoVN / Playnite / VNite …）没有隐藏概念，恒为 nil。
	Hidden      *bool
	CoverLoader func(models.Game) (string, error)
}

const (
	ImportActionCreate         = "create"
	ImportActionUpdateExisting = "update_existing"
	ImportActionMergeSessions  = "merge_sessions"
	// ImportActionSyncMerge 是云同步 / WebDAV 自持同步专用的「合并」动作。
	//
	// 它同时做两件事：并集去重地并入对端会话（与 merge_sessions 相同），以及按
	// 手机版 importGamesJson 的规则更新已有游戏的字段——「键存在且非空 + 对端
	// updated_at 不早于本地」才覆盖。merge_sessions 只做前者，于是手机端改过的
	// 状态 / 隐藏 / NSFW / 标题同步回桌面端时会被全部丢掉。
	//
	// 与 update_existing 的区别：后者给 PotatoVN / Playnite 这类自带完整元数据的
	// 导入格式用，会无条件写入 summary/company/rating 等文本字段（对端空值会冲掉
	// 本地）；云同步的对端可能只有 games 段而没有任何 metadata_cache，必须逐字段
	// 判空，所以单独一条动作。
	ImportActionSyncMerge = "sync_merge"

	SamePathActionSkip          = "skip"
	SamePathActionMerge         = "merge"
	SamePathActionMergeSessions = "merge_sessions"
	SamePathActionSyncMerge     = "sync_merge"

	ConflictTypeNone        = ""
	ConflictTypeSamePath    = "same_path"
	ConflictTypePath        = "path"
	ConflictTypeSource      = "source"
	ConflictTypeNameAndPath = "name_path"
)

type existingGameConflict struct {
	Type string
	Game models.Game
}

type importSelectionFilter struct {
	enabled bool
	keys    map[string]struct{}
}

func NormalizeSamePathAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case SamePathActionMerge:
		return SamePathActionMerge
	case SamePathActionMergeSessions:
		return SamePathActionMergeSessions
	case SamePathActionSyncMerge:
		return SamePathActionSyncMerge
	}
	return SamePathActionSkip
}

func IsSamePathMergeAction(action string) bool {
	return action == SamePathActionMerge ||
		action == SamePathActionMergeSessions ||
		action == SamePathActionSyncMerge
}

func TargetsExistingGame(action string) bool {
	return action == ImportActionUpdateExisting ||
		action == ImportActionMergeSessions ||
		action == ImportActionSyncMerge
}

func newImportSelectionFilter(selections []vo.ImportSelection) importSelectionFilter {
	if len(selections) == 0 {
		return importSelectionFilter{}
	}

	filter := importSelectionFilter{
		enabled: true,
		keys:    make(map[string]struct{}, len(selections)*3),
	}
	for _, selection := range selections {
		filter.add(selection.Name, selection.Path, string(selection.SourceType), selection.SourceID)
	}
	return filter
}

func (f importSelectionFilter) add(name string, path string, sourceType string, sourceID string) {
	for _, key := range importSelectionKeys(name, path, sourceType, sourceID) {
		if key != "" {
			f.keys[key] = struct{}{}
		}
	}
}

func (f importSelectionFilter) includes(name string, path string, sourceType string, sourceID string) bool {
	if !f.enabled {
		return true
	}
	for _, key := range importSelectionKeys(name, path, sourceType, sourceID) {
		if _, ok := f.keys[key]; ok {
			return true
		}
	}
	return false
}

func importSelectionKeys(name string, path string, sourceType string, sourceID string) []string {
	if sourceKey := previewSourceKey(sourceType, sourceID); sourceKey != "" {
		return []string{"source\x00" + sourceKey}
	}
	if pathKey := normalizeImportPath(path); pathKey != "" {
		return []string{"path\x00" + pathKey}
	}
	if nameKey := strings.ToLower(strings.TrimSpace(name)); nameKey != "" {
		return []string{"name\x00" + nameKey}
	}
	return nil
}

func newImportResult() ImportResult {
	return ImportResult{
		FailedNames:  []string{},
		SkippedNames: []string{},
	}
}

func (d Dependencies) existingGames(logPrefix string) ([]models.Game, map[string]string, map[string]string, error) {
	if d.ListGames == nil {
		return nil, nil, nil, fmt.Errorf("缺少游戏列表依赖")
	}

	existingGames, err := d.ListGames()
	if err != nil {
		applog.LogErrorf(d.Ctx, "%s: failed to get existing games: %v", logPrefix, err)
		return nil, nil, nil, fmt.Errorf("获取现有游戏列表失败: %w", err)
	}

	existingNames := make(map[string]string)
	existingPaths := make(map[string]string)
	for _, g := range existingGames {
		if g.Name != "" {
			existingNames[strings.ToLower(g.Name)] = g.ID
		}
		if g.Path != "" {
			existingPaths[normalizeImportPath(g.Path)] = g.Name
		}
	}

	return existingGames, existingNames, existingPaths, nil
}

func (d Dependencies) existingNameSet(logPrefix string) (map[string]bool, error) {
	if d.ListGames == nil {
		return nil, fmt.Errorf("缺少游戏列表依赖")
	}

	existingGames, err := d.ListGames()
	if err != nil {
		applog.LogErrorf(d.Ctx, "%s: failed to get existing games: %v", logPrefix, err)
		return nil, fmt.Errorf("获取现有游戏列表失败: %w", err)
	}

	existingNames := make(map[string]bool)
	for _, g := range existingGames {
		existingNames[strings.ToLower(g.Name)] = true
	}
	return existingNames, nil
}

func newExistingPreviewIndex(existingGames []models.Game) Index {
	refs := make([]GameRef, 0, len(existingGames))
	for _, game := range existingGames {
		refs = append(refs, gameRefFromModel(game))
	}
	return NewIndex(refs)
}

func previewSourceKey(sourceType string, sourceID string) string {
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	sourceID = strings.ToLower(strings.TrimSpace(sourceID))
	if sourceType == "" || sourceID == "" {
		return ""
	}
	return sourceType + "\x00" + sourceID
}

func previewNamePathKey(gameName string, exePath string) string {
	nameKey := strings.ToLower(strings.TrimSpace(gameName))
	pathKey := normalizeImportPath(exePath)
	if nameKey == "" || pathKey == "" {
		return ""
	}
	return nameKey + "\x00" + pathKey
}

func previewExists(idx Index, gameName string, exePath string, sourceType string, sourceID string) bool {
	return previewConflict(idx, gameName, exePath, sourceType, sourceID).Type != ConflictTypeNone
}

func previewConflict(idx Index, gameName string, exePath string, sourceType string, sourceID string) existingGameConflict {
	if pathKey := normalizeImportPath(exePath); pathKey != "" {
		if ref, exists := idx.FindByPath(exePath); exists {
			return existingGameConflict{Type: ConflictTypeSamePath, Game: ref.game()}
		}
		if ref, exists := idx.FindByPathConflict(exePath); exists {
			return existingGameConflict{Type: ConflictTypePath, Game: ref.game()}
		}
	}
	if ref, exists := idx.FindBySource(enums.SourceType(sourceType), sourceID); exists {
		return existingGameConflict{Type: ConflictTypeSource, Game: ref.game()}
	}
	if ref, exists := idx.FindByNamePath(gameName, exePath); exists {
		return existingGameConflict{Type: ConflictTypeNameAndPath, Game: ref.game()}
	}
	return existingGameConflict{}
}

func findExistingGameConflict(existingGames []models.Game, existingNames map[string]string, existingPaths map[string]string, gameName string, exePath string) (existingGameConflict, bool) {
	pathKey := normalizeImportPath(exePath)
	if pathKey != "" {
		for _, game := range existingGames {
			if normalizeImportPath(game.Path) == pathKey {
				return existingGameConflict{Type: ConflictTypeSamePath, Game: game}, true
			}
		}
		if existingName, exists := findExistingPathConflict(existingPaths, exePath); exists {
			return existingGameConflict{
				Type: ConflictTypePath,
				Game: models.Game{Name: existingName},
			}, true
		}
	}

	if existingID, exists := existingNames[strings.ToLower(gameName)]; exists {
		for _, game := range existingGames {
			if game.ID == existingID && normalizeImportPath(game.Path) == pathKey {
				return existingGameConflict{Type: ConflictTypeNameAndPath, Game: game}, true
			}
		}
	}

	return existingGameConflict{}, false
}

func skipExistingGame(
	ctx context.Context,
	logPrefix string,
	result *ImportResult,
	existingGames []models.Game,
	existingNames map[string]string,
	existingPaths map[string]string,
	gameName string,
	exePath string,
) bool {
	if conflict, exists := findExistingGameConflict(existingGames, existingNames, existingPaths, gameName, exePath); exists {
		result.Skipped++
		if conflict.Type == ConflictTypeNameAndPath {
			result.SkippedNames = append(result.SkippedNames, gameName+" (已存在)")
			return true
		}
		result.SkippedNames = append(result.SkippedNames, gameName+" (路径已存在: "+conflict.Game.Name+")")
		return true
	}

	if _, exists := existingNames[strings.ToLower(gameName)]; exists {
		applog.LogInfof(ctx, "%s: importing duplicate name %s with different path: %s", logPrefix, gameName, exePath)
	}

	return false
}

func addImportedGame(deps Dependencies, source vo.GameMetadataFromWebVO) error {
	if deps.AddGame == nil {
		return fmt.Errorf("缺少游戏导入依赖")
	}
	return deps.AddGame(source)
}

func addImportedItems(deps Dependencies, items []ImportItem) (ImportResult, error) {
	if deps.AddItems != nil {
		return deps.AddItems(items)
	}

	result := newImportResult()
	for _, item := range items {
		// merge_sessions / sync_merge 都是「命中已有条目」的动作：前者只并会话，
		// 后者还要按字段级守卫更新元数据。没有 AddItems 依赖时做不了字段级合并，
		// 这里退化成只并会话（宁可少改，也不能把本地字段写坏）。
		if item.Action == ImportActionMergeSessions || item.Action == ImportActionSyncMerge {
			addPlaySessions(deps, "ImportItems", &result, item.DisplayName, item.Sessions)
			result.Success++
			continue
		}
		if err := addImportedGame(deps, item.Source); err != nil {
			result.Failed++
			result.FailedNames = append(result.FailedNames, item.DisplayName)
			continue
		}
		addPlaySessions(deps, "ImportItems", &result, item.DisplayName, item.Sessions)
		result.Success++
	}
	return result, nil
}

func addPlaySessions(deps Dependencies, logPrefix string, result *ImportResult, gameName string, sessions []models.PlaySession) {
	if len(sessions) == 0 || deps.AddSessions == nil {
		return
	}

	if err := deps.AddSessions(sessions); err != nil {
		applog.LogWarningf(deps.Ctx, "%s: failed to import play sessions for game %s: %v", logPrefix, gameName, err)
		return
	}

	applog.LogInfof(deps.Ctx, "%s: imported %d play sessions for game %s", logPrefix, len(sessions), gameName)
	result.SessionsImported += len(sessions)
}

func tagsFromNames(names []string) []metadata.TagItem {
	if len(names) == 0 {
		return nil
	}

	tags := make([]metadata.TagItem, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		tags = append(tags, metadata.TagItem{
			Name:      name,
			Source:    "user",
			Weight:    1.0,
			IsSpoiler: false,
		})
	}
	return tags
}

func updateExistingIndexes(existingNames map[string]string, existingPaths map[string]string, game models.Game, gameName string, exePath string) {
	existingNames[strings.ToLower(gameName)] = game.ID
	if exePath != "" {
		existingPaths[normalizeImportPath(exePath)] = gameName
	}
}

func normalizeImportPath(path string) string {
	return importpath.Normalize(path)
}

func findExistingPathConflict(existingPaths map[string]string, path string) (string, bool) {
	pathKey := normalizeImportPath(path)
	if pathKey == "" {
		return "", false
	}
	if existingName, exists := existingPaths[pathKey]; exists {
		return existingName, true
	}
	for existingPath, existingName := range existingPaths {
		if importpath.Conflicts(pathKey, existingPath) {
			return existingName, true
		}
	}
	return "", false
}
