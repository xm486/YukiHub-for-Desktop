package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
	"unicode"
	"yukihub/internal/appconf"
	"yukihub/internal/applog"
	"yukihub/internal/common/enums"
	"yukihub/internal/common/importpath"
	"yukihub/internal/common/vo"
	"yukihub/internal/models"
	"yukihub/internal/service/exporter"
	"yukihub/internal/service/gamehelper"
	"yukihub/internal/service/importer"
	"yukihub/internal/utils/apputils"
	"yukihub/internal/utils/metadata"

	"github.com/google/uuid"
	"yukihub/internal/wailsruntime"
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

type LibraryDirectorySelection struct {
	Directory               string `json:"directory"`
	InitialDirectoryInvalid bool   `json:"initial_directory_invalid"`
}

type ImportService struct {
	ctx               context.Context
	db                *sql.DB
	config            *appconf.AppConfig
	gameService       *GameService
	bangumiService    *BangumiService
	hikarinagiService *HikarinagiService
	nextMoeService    *NextMoeService
	sessionService    *SessionService
	runtime           wailsruntime.Runtime
}

func NewImportService() *ImportService {
	return &ImportService{runtime: wailsruntime.Unavailable()}
}

//wails:ignore
func (s *ImportService) Init(ctx context.Context, db *sql.DB, config *appconf.AppConfig) {
	s.ctx = ctx
	s.db = db
	s.config = config
}

//wails:ignore
func (s *ImportService) SetRuntime(runtime wailsruntime.Runtime) {
	if runtime != nil {
		s.runtime = runtime
	}
}

// SetGameService 设置 GameService（用于写入导入的游戏）。
//
//wails:ignore
func (s *ImportService) SetGameService(gameService *GameService) {
	s.gameService = gameService
}

// SetSessionService 设置 SessionService（用于导入游玩记录）。
//
//wails:ignore
func (s *ImportService) SetSessionService(sessionService *SessionService) {
	s.sessionService = sessionService
}

//wails:ignore
func (s *ImportService) SetBangumiService(bangumiService *BangumiService) {
	s.bangumiService = bangumiService
}

//wails:ignore
func (s *ImportService) SetHikarinagiService(hikarinagiService *HikarinagiService) {
	s.hikarinagiService = hikarinagiService
}

//wails:ignore
func (s *ImportService) SetNextMoeService(nextMoeService *NextMoeService) {
	s.nextMoeService = nextMoeService
}

func (s *ImportService) importerDependencies() importer.Dependencies {
	var addSessions func([]models.PlaySession) error
	if s.sessionService != nil {
		addSessions = s.sessionService.BatchAddPlaySessions
	}

	committer := s.importCommitter()
	return importer.Dependencies{
		Ctx:                          s.ctx,
		ListGames:                    committer.ListGames,
		AddGame:                      s.gameService.AddGameFromWebMetadata,
		AddItems:                     committer.AddItems,
		AddSessions:                  addSessions,
		AllowDuplicateMetadataImport: s.config != nil && s.config.AllowDuplicateMetadataImport,
	}
}

func (s *ImportService) importCommitter() *importer.Committer {
	deps := importer.CommitDependencies{
		Ctx:                    s.ctx,
		DB:                     s.db,
		AllowDuplicateMetadata: s.config != nil && s.config.AllowDuplicateMetadataImport,
	}
	if s.gameService != nil {
		deps.UpdateCoverURL = s.gameService.updateCoverURL
		if s.gameService.imageTaskStarter != nil {
			deps.StartCoverDownloads = func(items []importer.CoverDownloadItem) string {
				serviceItems := make([]CoverImageDownloadItem, 0, len(items))
				for _, item := range items {
					serviceItems = append(serviceItems, CoverImageDownloadItem{
						GameID:   item.GameID,
						GameName: item.GameName,
						CoverURL: item.CoverURL,
					})
				}
				return s.gameService.imageTaskStarter(serviceItems)
			}
		}
	}
	return importer.NewCommitter(deps)
}

func previewGamesFromImporter(previews []importer.PreviewGame) []PreviewGame {
	if previews == nil {
		return nil
	}

	result := make([]PreviewGame, 0, len(previews))
	for _, preview := range previews {
		result = append(result, PreviewGame(preview))
	}
	return result
}

// =================== PotatoVN 导入功能 ====================

// SelectZipFile 选择要导入的 ZIP 文件
func (s *ImportService) SelectZipFile() (string, error) {
	selection, err := s.runtime.OpenFile(wailsruntime.OpenDialogOptions{
		Title: "选择 PotatoVN 导出的 ZIP 文件",
		Filters: []wailsruntime.FileFilter{
			{
				DisplayName: "ZIP 文件",
				Pattern:     "*.zip",
			},
		},
	})
	return selection, err
}

// ImportFromPotatoVN 从 PotatoVN 导出的 ZIP 文件导入数据
func (s *ImportService) ImportFromPotatoVN(zipPath string, skipNoPath bool) (ImportResult, error) {
	return s.ImportFromPotatoVNWithOptions(zipPath, skipNoPath, importer.SamePathActionSkip)
}

func (s *ImportService) ImportFromPotatoVNWithOptions(zipPath string, skipNoPath bool, samePathAction string) (ImportResult, error) {
	result, err := importer.NewPotatoVNImporter(s.importerDependencies()).Import(zipPath, skipNoPath, samePathAction)
	return ImportResult(result), err
}

func (s *ImportService) ImportFromPotatoVNWithSelection(zipPath string, skipNoPath bool, samePathAction string, selections []vo.ImportSelection) (ImportResult, error) {
	if len(selections) == 0 {
		return emptyServiceImportResult(), nil
	}
	result, err := importer.NewPotatoVNImporter(s.importerDependencies()).ImportSelected(zipPath, skipNoPath, samePathAction, selections)
	return ImportResult(result), err
}

// PreviewImport 预览 PotatoVN 导入内容（不实际导入）
func (s *ImportService) PreviewImport(zipPath string) ([]PreviewGame, error) {
	previews, err := importer.NewPotatoVNImporter(s.importerDependencies()).Preview(zipPath)
	return previewGamesFromImporter(previews), err
}

// =================== YukiHub 导入功能 ====================

// SelectYukiHubExportPath 选择 YukiHub 备份的保存位置。
//
// 对手是手机版：文件名沿用手机版的 `yukihub_backup_<毫秒时间戳>.ykbak`，
// 这样用户在两端看到的是同一套命名。
func (s *ImportService) SelectYukiHubExportPath() (string, error) {
	defaultFileName := fmt.Sprintf("yukihub_backup_%d.ykbak", time.Now().UnixMilli())

	selection, err := s.runtime.SaveFile(wailsruntime.SaveDialogOptions{
		Title:    "选择 YukiHub 备份保存位置",
		Filename: defaultFileName,
		Filters: []wailsruntime.FileFilter{
			{
				DisplayName: "YukiHub 备份",
				Pattern:     "*.ykbak",
			},
		},
	})
	if err != nil {
		applog.LogErrorf(s.ctx, "failed to open save file dialog: %v", err)
	}
	return selection, err
}

// ExportToYukiHub 把桌面端库导出为 YukiHub schema 5 备份（.ykbak），手机版可直接导入。
//
// 与导入方向对称：导出完成后会把「多少个游戏 / 多少条游玩记录」回报给调用方，
// 前端好提示用户。契约细节见 docs/mobile-yukihub-migration.md。
func (s *ImportService) ExportToYukiHub(path string) (ImportResult, error) {
	result := ImportResult{}
	if strings.TrimSpace(path) == "" {
		return result, fmt.Errorf("导出路径为空")
	}

	exporterInstance := exporter.NewYukiHubExporter(s.ctx, s.db)
	// 与手机版本地备份一致：带上 profile（昵称 / 头像）与全局资料源。
	if s.config != nil {
		exporterInstance.SetProfile(s.config.YukiHubAccountNickname, s.config.YukiHubAccountAvatar)
		exporterInstance.SetMetadataSource(string(s.config.CurrentMetadataSource))
	}
	games, sessions, err := exporterInstance.ExportWithSummary(path)
	if err != nil {
		applog.LogErrorf(s.ctx, "ExportToYukiHub: failed: %v", err)
		return result, err
	}
	result.Success = games
	result.SessionsImported = sessions
	applog.LogInfof(s.ctx, "ExportToYukiHub: exported %d games / %d sessions to %s",
		games, sessions, path)
	return result, nil
}

// SelectYukiHubBackup 选择 YukiHub 导出的备份文件。
func (s *ImportService) SelectYukiHubBackup() (string, error) {
	selection, err := s.runtime.OpenFile(wailsruntime.OpenDialogOptions{
		Title: "选择 YukiHub 备份文件",
		Filters: []wailsruntime.FileFilter{
			{
				DisplayName: "YukiHub 备份",
				Pattern:     "*.ykbak;*.json",
			},
		},
	})
	return selection, err
}

// PreviewYukiHubImport 预览 YukiHub 备份中的游戏。
func (s *ImportService) PreviewYukiHubImport(backupPath string) ([]PreviewGame, error) {
	previews, err := importer.NewYukiHubImporter(s.importerDependencies()).Preview(backupPath)
	return previewGamesFromImporter(previews), err
}

// ImportFromYukiHub 从 YukiHub 备份导入游戏与游玩记录。
func (s *ImportService) ImportFromYukiHub(backupPath string, skipNoPath bool) (ImportResult, error) {
	return s.ImportFromYukiHubWithOptions(backupPath, skipNoPath, importer.SamePathActionSkip)
}

func (s *ImportService) ImportFromYukiHubWithOptions(backupPath string, skipNoPath bool, samePathAction string) (ImportResult, error) {
	yukiHubImporter := importer.NewYukiHubImporter(s.importerDependencies())
	result, err := yukiHubImporter.Import(backupPath, skipNoPath, samePathAction)
	s.applyImportedMetadataSource(yukiHubImporter.MetadataSource())
	return ImportResult(result), err
}

func (s *ImportService) ImportFromYukiHubWithSelection(backupPath string, skipNoPath bool, samePathAction string, selections []vo.ImportSelection) (ImportResult, error) {
	if len(selections) == 0 {
		return emptyServiceImportResult(), nil
	}
	yukiHubImporter := importer.NewYukiHubImporter(s.importerDependencies())
	result, err := yukiHubImporter.ImportSelected(backupPath, skipNoPath, samePathAction, selections)
	s.applyImportedMetadataSource(yukiHubImporter.MetadataSource())
	return ImportResult(result), err
}

// applyImportedMetadataSource 把快照声明的 settings.metadata_source 落回本地配置。
//
// 对齐手机版 SyncManager.importSnapshot：它读到 settings.metadata_source 时会
// `putString(KEY_METADATA_SOURCE, source)`，即**导入侧采纳对端的全局资料源**。
// 桌面端此前只导出不回写，于是「手机端改了资料源 → 同步到桌面端不变 → 桌面端再
// 上传把手机端的设置顶回去」，偏好会来回翻。
//
// 三条导入通道（账号云同步 / WebDAV 自持同步 / 本地 .ykbak）都走这里。
// 白名单外、缺失、与当前一致时一律不动，绝不覆盖用户设置。
func (s *ImportService) applyImportedMetadataSource(source string) {
	if s.config == nil {
		return
	}
	normalized, changed := resolveImportedMetadataSource(s.config.CurrentMetadataSource, source)
	if !changed {
		return
	}

	applog.LogInfof(s.ctx, "YukiHub 导入：采纳快照里的全局资料源 %s（原为 %s）",
		normalized, s.config.CurrentMetadataSource)
	s.config.CurrentMetadataSource = normalized
	if err := appconf.SaveConfig(s.config); err != nil {
		applog.LogWarningf(s.ctx, "YukiHub 导入：保存资料源设置失败: %v", err)
	}
	// 资料源是设置页草稿的一部分：不通知前端的话，用户接着点「保存」会把
	// 刚采纳的值用旧草稿写回去。复用同步已落库事件，前端会顺带 refreshConfig。
	s.runtime.Emit(selfSyncAppliedEvent, nil)
}

// resolveImportedMetadataSource 判断快照声明的全局资料源该不该被采纳。
//
// 返回采纳后的取值与「是否发生了变化」；changed=false 表示保持原值。
// 白名单外（steam / dlsite 等桌面端独有来源、以及任何拼写错误）一律不采纳 ——
// 手机版的 importSnapshot 也只接受 vndb / bangumi / ymgal / hikarinagi / nextmoe
// 这五个值。
//
// 已下线的 `bangumi_mirror` 先折叠成 bangumi 再判定（与手机版 SyncManager
// 落盘前的那次折叠一致），这样老快照不会因为「值已下线」而被整条忽略。
//
// 单独抽成纯函数是为了能直接单测：真正的落库路径会写 appconf.json。
func resolveImportedMetadataSource(current enums.SourceType, incoming string) (enums.SourceType, bool) {
	trimmed := appconf.NormalizeLegacyMetadataSource(incoming)
	if trimmed == "" || !appconf.IsSelectableMetadataSource(trimmed) {
		return current, false
	}
	normalized := enums.SourceType(trimmed)
	if normalized == current {
		return current, false
	}
	return normalized, true
}

// =================== Playnite 导入功能 ====================

// SelectJSONFile 选择要导入的 JSON 文件
func (s *ImportService) SelectJSONFile() (string, error) {
	selection, err := s.runtime.OpenFile(wailsruntime.OpenDialogOptions{
		Title: "选择 Playnite 导出的 JSON 文件",
		Filters: []wailsruntime.FileFilter{
			{
				DisplayName: "JSON 文件",
				Pattern:     "*.json",
			},
		},
	})
	return selection, err
}

// PreviewPlayniteImport 预览 Playnite 导入内容（不实际导入）
func (s *ImportService) PreviewPlayniteImport(jsonPath string) ([]PreviewGame, error) {
	previews, err := importer.NewPlayniteImporter(s.importerDependencies()).Preview(jsonPath)
	return previewGamesFromImporter(previews), err
}

// ImportFromPlaynite 从 Playnite 导出的 JSON 文件导入数据
func (s *ImportService) ImportFromPlaynite(jsonPath string, skipNoPath bool) (ImportResult, error) {
	return s.ImportFromPlayniteWithOptions(jsonPath, skipNoPath, importer.SamePathActionSkip)
}

func (s *ImportService) ImportFromPlayniteWithOptions(jsonPath string, skipNoPath bool, samePathAction string) (ImportResult, error) {
	result, err := importer.NewPlayniteImporter(s.importerDependencies()).Import(jsonPath, skipNoPath, samePathAction)
	return ImportResult(result), err
}

func (s *ImportService) ImportFromPlayniteWithSelection(jsonPath string, skipNoPath bool, samePathAction string, selections []vo.ImportSelection) (ImportResult, error) {
	if len(selections) == 0 {
		return emptyServiceImportResult(), nil
	}
	result, err := importer.NewPlayniteImporter(s.importerDependencies()).ImportSelected(jsonPath, skipNoPath, samePathAction, selections)
	return ImportResult(result), err
}

// =================== Vnite 导入功能 ====================

// SelectVniteDirectory 选择 Vnite 导出的数据库目录
func (s *ImportService) SelectVniteDirectory() (string, error) {
	selection, err := s.runtime.OpenDirectory(wailsruntime.OpenDialogOptions{
		Title: "选择 Vnite 导出的数据库目录",
	})
	return selection, err
}

// PreviewVniteImport 预览 Vnite 导入内容（不实际导入）
func (s *ImportService) PreviewVniteImport(vniteDir string) ([]PreviewGame, error) {
	previews, err := importer.NewVniteImporter(s.importerDependencies()).Preview(vniteDir)
	return previewGamesFromImporter(previews), err
}

// ImportFromVnite 从 Vnite 导出的数据库目录导入数据
func (s *ImportService) ImportFromVnite(vniteDir string, skipNoPath bool) (ImportResult, error) {
	return s.ImportFromVniteWithOptions(vniteDir, skipNoPath, importer.SamePathActionSkip)
}

func (s *ImportService) ImportFromVniteWithOptions(vniteDir string, skipNoPath bool, samePathAction string) (ImportResult, error) {
	result, err := importer.NewVniteImporter(s.importerDependencies()).Import(vniteDir, skipNoPath, samePathAction)
	return ImportResult(result), err
}

func (s *ImportService) ImportFromVniteWithSelection(vniteDir string, skipNoPath bool, samePathAction string, selections []vo.ImportSelection) (ImportResult, error) {
	if len(selections) == 0 {
		return emptyServiceImportResult(), nil
	}
	result, err := importer.NewVniteImporter(s.importerDependencies()).ImportSelected(vniteDir, skipNoPath, samePathAction, selections)
	return ImportResult(result), err
}

// =================== ReinaManager 导入功能 ====================

// SelectReinaManagerDatabase 选择 ReinaManager 导出的 SQLite 数据库备份。
func (s *ImportService) SelectReinaManagerDatabase() (string, error) {
	selection, err := s.runtime.OpenFile(wailsruntime.OpenDialogOptions{
		Title: "选择 ReinaManager 数据库备份",
		Filters: []wailsruntime.FileFilter{
			{
				DisplayName: "SQLite 数据库",
				Pattern:     "*.db;*.sqlite;*.sqlite3",
			},
		},
	})
	return selection, err
}

// PreviewReinaManagerImport 预览 ReinaManager 数据库备份中的游戏。
func (s *ImportService) PreviewReinaManagerImport(dbPath string) ([]PreviewGame, error) {
	previews, err := importer.NewReinaManagerImporter(s.importerDependencies()).Preview(dbPath)
	return previewGamesFromImporter(previews), err
}

// ImportFromReinaManager 从 ReinaManager SQLite 数据库备份导入数据。
func (s *ImportService) ImportFromReinaManager(dbPath string, skipNoPath bool) (ImportResult, error) {
	return s.ImportFromReinaManagerWithOptions(dbPath, skipNoPath, importer.SamePathActionSkip)
}

func (s *ImportService) ImportFromReinaManagerWithOptions(dbPath string, skipNoPath bool, samePathAction string) (ImportResult, error) {
	result, err := importer.NewReinaManagerImporter(s.importerDependencies()).Import(dbPath, skipNoPath, samePathAction)
	return ImportResult(result), err
}

func (s *ImportService) ImportFromReinaManagerWithSelection(dbPath string, skipNoPath bool, samePathAction string, selections []vo.ImportSelection) (ImportResult, error) {
	if len(selections) == 0 {
		return emptyServiceImportResult(), nil
	}
	result, err := importer.NewReinaManagerImporter(s.importerDependencies()).ImportSelected(dbPath, skipNoPath, samePathAction, selections)
	return ImportResult(result), err
}

// =================== 上游 LunaBox 备份导入 ====================

// SelectLunaBoxBackup 选择上游 LunaBox 的 ZIP 备份文件。
//
// LunaBox 的备份是 ZIP（默认落在 %APPDATA%\LunaBox\backups\database\），
// 对话框存在该目录时直接以它为起始目录。
func (s *ImportService) SelectLunaBoxBackup() (string, error) {
	selection, err := s.runtime.OpenFile(wailsruntime.OpenDialogOptions{
		Title:     "选择 LunaBox 备份文件",
		Directory: lunaBoxDefaultBackupDir(),
		Filters: []wailsruntime.FileFilter{
			{
				DisplayName: "LunaBox 备份 (*.zip)",
				Pattern:     "*.zip",
			},
		},
	})
	return selection, err
}

// lunaBoxDefaultBackupDir 返回 LunaBox 的备份目录，不存在时返回空串。
func lunaBoxDefaultBackupDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(configDir, "LunaBox", "backups", "database")
	if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
		return dir
	}
	return ""
}

// PreviewLunaBoxImport 预览 LunaBox 备份中的游戏。
func (s *ImportService) PreviewLunaBoxImport(zipPath string) ([]PreviewGame, error) {
	previews, err := importer.NewLunaBoxImporter(s.importerDependencies()).Preview(zipPath)
	return previewGamesFromImporter(previews), err
}

// ImportFromLunaBox 从上游 LunaBox 备份导入游戏与游玩记录。
func (s *ImportService) ImportFromLunaBox(zipPath string, skipNoPath bool) (ImportResult, error) {
	return s.ImportFromLunaBoxWithOptions(zipPath, skipNoPath, importer.SamePathActionSkip)
}

// ImportFromLunaBoxWithOptions 按同名路径处理策略导入 LunaBox 数据。
func (s *ImportService) ImportFromLunaBoxWithOptions(zipPath string, skipNoPath bool, samePathAction string) (ImportResult, error) {
	result, err := importer.NewLunaBoxImporter(s.importerDependencies()).Import(zipPath, skipNoPath, samePathAction)
	return ImportResult(result), err
}

// ImportFromLunaBoxWithSelection 只导入 LunaBox 备份里被选中的条目。
func (s *ImportService) ImportFromLunaBoxWithSelection(zipPath string, skipNoPath bool, samePathAction string, selections []vo.ImportSelection) (ImportResult, error) {
	if len(selections) == 0 {
		return emptyServiceImportResult(), nil
	}
	result, err := importer.NewLunaBoxImporter(s.importerDependencies()).ImportSelected(zipPath, skipNoPath, samePathAction, selections)
	return ImportResult(result), err
}

// =================== Steam 本地库导入功能 ====================

// PreviewSteamLocalImport 扫描本机已安装 Steam 游戏并预览导入内容。
func (s *ImportService) PreviewSteamLocalImport() ([]PreviewGame, error) {
	previews, err := importer.NewSteamImporter(s.importerDependencies()).Preview()
	return previewGamesFromImporter(previews), err
}

// ImportFromSteamLocal 从本机 Steam 库导入已安装游戏。
func (s *ImportService) ImportFromSteamLocal(skipNoPath bool) (ImportResult, error) {
	return s.ImportFromSteamLocalWithOptions(skipNoPath, importer.SamePathActionSkip)
}

func (s *ImportService) ImportFromSteamLocalWithOptions(skipNoPath bool, samePathAction string) (ImportResult, error) {
	language := ""
	if s.config != nil {
		language = s.config.Language
	}
	result, err := importer.NewSteamImporter(s.importerDependencies()).Import(
		skipNoPath,
		samePathAction,
		language,
		gamehelper.MetadataGetterOptions(s.config)...,
	)
	return ImportResult(result), err
}

func (s *ImportService) ImportFromSteamLocalWithSelection(skipNoPath bool, samePathAction string, selections []vo.ImportSelection) (ImportResult, error) {
	if len(selections) == 0 {
		return emptyServiceImportResult(), nil
	}
	language := ""
	if s.config != nil {
		language = s.config.Language
	}
	result, err := importer.NewSteamImporter(s.importerDependencies()).ImportSelected(
		skipNoPath,
		samePathAction,
		selections,
		language,
		gamehelper.MetadataGetterOptions(s.config)...,
	)
	return ImportResult(result), err
}

func emptyServiceImportResult() ImportResult {
	return ImportResult{
		FailedNames:  []string{},
		SkippedNames: []string{},
	}
}

// ==================== 批量导入功能 ====================

// SelectLibraryDirectory 选择游戏库目录，并仅将当前系统可访问的目录用于初始化对话框。
func (s *ImportService) SelectLibraryDirectory(initialDirectory string) (LibraryDirectorySelection, error) {
	initialDirectory = strings.TrimSpace(initialDirectory)
	validInitialDirectory := batchImportInitialDirectory(initialDirectory, goruntime.GOOS)
	selection, err := s.runtime.OpenDirectory(wailsruntime.OpenDialogOptions{
		Title:     "选择游戏库目录",
		Directory: validInitialDirectory,
	})
	return LibraryDirectorySelection{
		Directory:               selection,
		InitialDirectoryInvalid: initialDirectory != "" && validInitialDirectory == "",
	}, err
}

func batchImportInitialDirectory(directory string, goos string) string {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return ""
	}

	if goos == "windows" {
		if strings.HasPrefix(directory, "/") && !strings.HasPrefix(directory, "//") {
			return ""
		}
		directory = strings.ReplaceAll(directory, "/", `\`)
	} else if strings.Contains(directory, `\`) {
		return ""
	}

	directory = filepath.Clean(directory)
	if !filepath.IsAbs(directory) {
		return ""
	}

	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return ""
	}
	return directory
}

// ScanLibraryDirectory 扫描游戏库目录，返回默认待导入候选项和路径阶段跳过项。
func (s *ImportService) ScanLibraryDirectory(libraryPath string) (vo.BatchImportScanResult, error) {
	return s.scanLibraryDirectory(libraryPath, vo.BatchImportScanOptions{})
}

// ScanLibraryDirectoryWithOptions 按指定模式扫描游戏库目录。
func (s *ImportService) ScanLibraryDirectoryWithOptions(libraryPath string, options vo.BatchImportScanOptions) (vo.BatchImportScanResult, error) {
	return s.scanLibraryDirectory(libraryPath, options)
}

func (s *ImportService) scanLibraryDirectory(libraryPath string, options vo.BatchImportScanOptions) (vo.BatchImportScanResult, error) {
	var candidates []vo.BatchImportCandidate
	var result vo.BatchImportScanResult

	excludeKeywords := defaultImportExcludeKeywords()
	const maxDepth = 7
	candidatesMap := make(map[string]vo.BatchImportCandidate)

	scanOptions := normalizeBatchImportScanOptions(options)
	var err error
	if scanOptions.ScanMode == "hierarchy" {
		err = s.scanDirectoryByHierarchy(libraryPath, scanOptions.HierarchyDepth, candidatesMap)
	} else {
		err = s.scanDirectoryRecursive(libraryPath, libraryPath, 0, maxDepth, excludeKeywords, scanOptions.ScanNameMode, scanOptions.NameDepth, candidatesMap)
	}
	if err != nil {
		applog.LogErrorf(s.ctx, "ScanLibraryDirectory: failed to scan directory: %v", err)
		return result, fmt.Errorf("扫描目录失败: %w", err)
	}

	for _, candidate := range candidatesMap {
		candidates = append(candidates, candidate)
	}

	idx, err := s.importCommitter().LoadIndex()
	if err != nil {
		applog.LogErrorf(s.ctx, "ScanLibraryDirectory: failed to load import index: %v", err)
		return result, fmt.Errorf("加载导入索引失败: %w", err)
	}

	result = importer.SplitScanCandidates(candidates, idx, s.config != nil && s.config.AllowDuplicateMetadataImport)
	applog.LogInfof(s.ctx, "ScanLibraryDirectory: found %d game candidates, %d importable, %d skipped", len(candidates), len(result.Candidates), result.Skipped)
	return result, nil
}

func normalizeBatchImportScanOptions(options vo.BatchImportScanOptions) vo.BatchImportScanOptions {
	if options.ScanMode != "hierarchy" {
		options.ScanMode = "scan"
	}
	if options.ScanNameMode != "parent" {
		options.ScanNameMode = "depth"
	}
	if options.NameDepth < 0 {
		options.NameDepth = 0
	}
	if options.HierarchyDepth < 0 {
		options.HierarchyDepth = 0
	}
	return options
}

// scanDirectoryRecursive 递归扫描目录，找到所有包含可执行文件的目录
func (s *ImportService) scanDirectoryRecursive(
	rootPath string,
	currentPath string,
	currentDepth int,
	maxDepth int,
	excludeKeywords []string,
	scanNameMode string,
	nameDepth int,
	candidatesMap map[string]vo.BatchImportCandidate,
) error {
	if currentDepth > maxDepth {
		return nil
	}

	entries, err := os.ReadDir(currentPath)
	if err != nil {
		applog.LogWarningf(s.ctx, "scanDirectoryRecursive: failed to read dir %s: %v", currentPath, err)
		return nil
	}

	executables := apputils.FindExecutables(currentPath, excludeKeywords)
	if len(executables) > 0 {
		relativePath, _ := filepath.Rel(rootPath, currentPath)
		folderName := filepath.Base(currentPath)
		if relativePath != "." && relativePath != "" {
			folderName = relativePath
		}

		selectedExe := apputils.SelectBestExecutable(executables, folderName)
		searchName := scanSearchName(rootPath, currentPath, scanNameMode, nameDepth)
		candidatesMap[currentPath] = vo.BatchImportCandidate{
			FolderPath:    currentPath,
			GameDirectory: scanGameDirectory(rootPath, currentPath, scanNameMode, nameDepth),
			FolderName:    folderName,
			Executables:   executables,
			SelectedExe:   selectedExe,
			SearchName:    searchName,
			IsSelected:    true,
			MatchStatus:   "pending",
		}
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		if shouldSkipImportDirectory(entry.Name()) {
			continue
		}

		subPath := filepath.Join(currentPath, entry.Name())
		if err := s.scanDirectoryRecursive(rootPath, subPath, currentDepth+1, maxDepth, excludeKeywords, scanNameMode, nameDepth, candidatesMap); err != nil {
			continue
		}
	}

	return nil
}

func (s *ImportService) scanDirectoryByHierarchy(
	rootPath string,
	hierarchyDepth int,
	candidatesMap map[string]vo.BatchImportCandidate,
) error {
	currentDirs := []string{rootPath}
	for depth := 0; depth <= hierarchyDepth; depth++ {
		nextDirs := make([]string, 0)
		for _, dir := range currentDirs {
			entries, err := os.ReadDir(dir)
			if err != nil {
				applog.LogWarningf(s.ctx, "scanDirectoryByHierarchy: failed to read dir %s: %v", dir, err)
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() || shouldSkipImportDirectory(entry.Name()) {
					continue
				}
				nextDirs = append(nextDirs, filepath.Join(dir, entry.Name()))
			}
		}
		currentDirs = nextDirs
		if len(currentDirs) == 0 {
			break
		}
	}

	for _, dir := range currentDirs {
		relativePath, _ := filepath.Rel(rootPath, dir)
		folderName := filepath.Base(dir)
		if relativePath != "." && relativePath != "" {
			folderName = relativePath
		}
		candidatesMap[dir] = vo.BatchImportCandidate{
			FolderPath:    dir,
			GameDirectory: dir,
			FolderName:    folderName,
			Executables:   []string{},
			SelectedExe:   dir,
			SearchName:    filepath.Base(dir),
			IsSelected:    true,
			MatchStatus:   "pending",
		}
	}
	return nil
}

func scanSearchName(rootPath string, currentPath string, scanNameMode string, nameDepth int) string {
	if scanNameMode == "parent" {
		return filepath.Base(currentPath)
	}

	relativePath, err := filepath.Rel(rootPath, currentPath)
	if err != nil || relativePath == "." || relativePath == "" {
		return filepath.Base(currentPath)
	}

	parts := strings.FieldsFunc(relativePath, func(r rune) bool {
		return r == filepath.Separator || r == '/' || r == '\\'
	})
	if nameDepth >= 0 && nameDepth < len(parts) && strings.TrimSpace(parts[nameDepth]) != "" {
		return parts[nameDepth]
	}
	return filepath.Base(currentPath)
}

func scanGameDirectory(rootPath string, currentPath string, scanNameMode string, nameDepth int) string {
	if scanNameMode != "depth" {
		return currentPath
	}

	relativePath, err := filepath.Rel(rootPath, currentPath)
	if err != nil || relativePath == "." || relativePath == "" {
		return currentPath
	}

	parts := strings.FieldsFunc(relativePath, func(r rune) bool {
		return r == filepath.Separator || r == '/' || r == '\\'
	})
	if nameDepth < 0 || nameDepth >= len(parts) {
		return currentPath
	}
	return filepath.Join(append([]string{rootPath}, parts[:nameDepth+1]...)...)
}

func shouldSkipImportDirectory(name string) bool {
	lowerName := strings.ToLower(name)
	return lowerName == "system" || lowerName == "windows" ||
		lowerName == "program files" || lowerName == "program files (x86)" ||
		strings.HasPrefix(lowerName, ".") ||
		lowerName == "node_modules" || lowerName == "__pycache__"
}

// ==================== 元数据获取与批量导入 ====================

// FetchMetadataForCandidate 为单个候选项获取元数据（带限流）
func (s *ImportService) FetchMetadataForCandidate(searchName string) (vo.BatchImportCandidate, error) {
	result := vo.BatchImportCandidate{
		SearchName:  searchName,
		MatchStatus: "not_found",
	}
	sources := s.metadataSearchSources()

	for _, src := range sources {
		metaResults, err := src.fetchCandidates(searchName)
		if err == nil && len(metaResults) > 0 && metaResults[0].Game.Name != "" {
			game := metaResults[0].Game
			result.MatchedGame = &game
			result.MatchedTags = metaResults[0].Tags
			result.MatchSource = src.source
			result.MatchStatus = "matched"
			return result, nil
		}
		if err != nil {
			applog.LogWarningf(s.ctx, "FetchMetadataForCandidate: failed to fetch metadata from %v for %s: %v", src.source, searchName, err)
		}
	}

	applog.LogWarningf(s.ctx, "FetchMetadataForCandidate: no metadata found for %s", searchName)
	return result, nil
}

const importMetadataNameSimilarityThreshold = 0.75

// FetchMetadataForCandidateWithPreference fetches and filters metadata for bulk import.
// A configured preferred source is authoritative. Without one, the search name is
// used as the reference for every enabled source.
func (s *ImportService) FetchMetadataForCandidateWithPreference(searchName string, preferredSource enums.SourceType) (vo.BatchImportMetadataMatchResult, error) {
	searchName = strings.TrimSpace(searchName)
	result := vo.BatchImportMetadataMatchResult{
		SearchName:      searchName,
		PreferredSource: gamehelper.NormalizeMetadataSourceType(preferredSource),
		Matches:         []vo.GameMetadataFromWebVO{},
		SourceErrors:    []vo.BatchImportMetadataSourceError{},
	}
	if searchName == "" {
		result.PreferredError = "搜索名称为空"
		return result, nil
	}

	sources := s.metadataSearchSources()
	if len(sources) == 0 {
		result.PreferredError = "没有可用的数据源"
		return result, nil
	}

	if result.PreferredSource == "" || result.PreferredSource == enums.Local {
		fetched := fetchImportMetadataSources(sources, searchName)
		selected := make([]vo.GameMetadataFromWebVO, 0, len(fetched))
		for _, sourceResult := range fetched {
			if sourceResult.sourceErr != nil {
				result.SourceErrors = append(result.SourceErrors, *sourceResult.sourceErr)
				applog.LogWarningf(s.ctx, "FetchMetadataForCandidateWithPreference: source %s failed for %s: %s", sourceResult.source, searchName, sourceResult.sourceErr.Error)
				continue
			}
			match, ok := selectBestImportMetadataMatch(sourceResult.matches, []string{searchName}, importMetadataNameSimilarityThreshold)
			if ok {
				selected = append(selected, match)
			}
		}
		result.Matches = attachImportMetadataSources(selected)
		return result, nil
	}

	preferredGetter, ok := findMetadataSearchSource(sources, result.PreferredSource)
	if !ok {
		result.PreferredError = fmt.Sprintf("偏好数据源 %s 未启用或不可用", result.PreferredSource)
		return result, nil
	}

	preferredMatches, preferredErr := fetchImportMetadataSource(preferredGetter, searchName)
	if preferredErr != nil {
		result.PreferredError = preferredErr.Error
		result.PreferredRateLimited = preferredErr.RateLimited
		result.SourceErrors = append(result.SourceErrors, *preferredErr)
		applog.LogWarningf(s.ctx, "FetchMetadataForCandidateWithPreference: preferred source %s failed for %s: %s", result.PreferredSource, searchName, preferredErr.Error)
		return result, nil
	}

	preferredMatch, ok := selectBestImportMetadataMatch(preferredMatches, []string{searchName}, 0)
	if !ok {
		result.PreferredNoResult = true
		result.PreferredError = "偏好数据源未找到匹配结果"
		return result, nil
	}

	result.PreferredMatched = true
	selected := []vo.GameMetadataFromWebVO{preferredMatch}
	referenceNames := importMetadataMatchNames(preferredMatch)
	remainingSources := make([]metadataSearchSource, 0, len(sources)-1)
	for _, src := range sources {
		if gamehelper.NormalizeMetadataSourceType(src.source) != result.PreferredSource {
			remainingSources = append(remainingSources, src)
		}
	}

	for _, sourceResult := range fetchImportMetadataSources(remainingSources, searchName) {
		if sourceResult.sourceErr != nil {
			result.SourceErrors = append(result.SourceErrors, *sourceResult.sourceErr)
			applog.LogWarningf(s.ctx, "FetchMetadataForCandidateWithPreference: source %s failed for %s: %s", sourceResult.source, searchName, sourceResult.sourceErr.Error)
			continue
		}
		match, matched := selectBestImportMetadataMatch(sourceResult.matches, referenceNames, importMetadataNameSimilarityThreshold)
		if matched {
			selected = append(selected, match)
		}
	}

	result.Matches = attachImportMetadataSources(selected)
	return result, nil
}

type importMetadataSourceResult struct {
	source    enums.SourceType
	matches   []vo.GameMetadataFromWebVO
	sourceErr *vo.BatchImportMetadataSourceError
}

func fetchImportMetadataSources(sources []metadataSearchSource, searchName string) []importMetadataSourceResult {
	results := make([]importMetadataSourceResult, len(sources))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(sources))
	for index := range sources {
		go func(index int) {
			defer waitGroup.Done()
			matches, sourceErr := fetchImportMetadataSource(sources[index], searchName)
			results[index] = importMetadataSourceResult{
				source:    sources[index].source,
				matches:   matches,
				sourceErr: sourceErr,
			}
		}(index)
	}
	waitGroup.Wait()
	return results
}

func selectBestImportMetadataMatch(matches []vo.GameMetadataFromWebVO, referenceNames []string, minimumSimilarity float64) (vo.GameMetadataFromWebVO, bool) {
	bestIndex := -1
	bestScore := -1.0
	for index, match := range matches {
		if strings.TrimSpace(match.Game.SourceID) == "" {
			continue
		}
		score := importMetadataGameNameSimilarity(referenceNames, match.Game)
		if score > bestScore {
			bestIndex = index
			bestScore = score
		}
	}
	if bestIndex < 0 || bestScore < minimumSimilarity {
		return vo.GameMetadataFromWebVO{}, false
	}
	return matches[bestIndex], true
}

func importMetadataGameNameSimilarity(referenceNames []string, game models.Game) float64 {
	candidateNames := append([]string{game.Name}, game.Aliases...)
	bestScore := 0.0
	for _, referenceName := range referenceNames {
		for _, candidateName := range candidateNames {
			score := normalizedImportMetadataNameSimilarity(referenceName, candidateName)
			if score > bestScore {
				bestScore = score
			}
		}
	}
	return bestScore
}

func importMetadataMatchNames(match vo.GameMetadataFromWebVO) []string {
	return append([]string{match.Game.Name}, match.Game.Aliases...)
}

func normalizedImportMetadataNameSimilarity(left string, right string) float64 {
	leftRunes := []rune(normalizeImportMetadataName(left))
	rightRunes := []rune(normalizeImportMetadataName(right))
	if len(leftRunes) == 0 || len(rightRunes) == 0 {
		return 0
	}
	if string(leftRunes) == string(rightRunes) {
		return 1
	}
	maximumLength := len(leftRunes)
	if len(rightRunes) > maximumLength {
		maximumLength = len(rightRunes)
	}
	return 1 - float64(importMetadataLevenshteinDistance(leftRunes, rightRunes))/float64(maximumLength)
}

func normalizeImportMetadataName(name string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func importMetadataLevenshteinDistance(left []rune, right []rune) int {
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftChar := range left {
		current[0] = leftIndex + 1
		for rightIndex, rightChar := range right {
			cost := 0
			if leftChar != rightChar {
				cost = 1
			}
			deletion := previous[rightIndex+1] + 1
			insertion := current[rightIndex] + 1
			substitution := previous[rightIndex] + cost
			current[rightIndex+1] = min(deletion, insertion, substitution)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

func attachImportMetadataSources(matches []vo.GameMetadataFromWebVO) []vo.GameMetadataFromWebVO {
	sources := make([]models.GameMetadataSource, 0, len(matches))
	seen := make(map[enums.SourceType]struct{}, len(matches))
	for _, match := range matches {
		sourceType := gamehelper.NormalizeMetadataSourceType(match.Source)
		sourceID := strings.TrimSpace(match.Game.SourceID)
		if sourceType == "" || sourceType == enums.Local || sourceID == "" {
			continue
		}
		if _, exists := seen[sourceType]; exists {
			continue
		}
		seen[sourceType] = struct{}{}
		sources = append(sources, models.GameMetadataSource{
			SourceType: sourceType,
			SourceID:   sourceID,
			CachedAt:   match.Game.CachedAt,
		})
	}

	for index := range matches {
		matches[index].Game.MetadataSources = append([]models.GameMetadataSource(nil), sources...)
	}
	return matches
}

func findMetadataSearchSource(sources []metadataSearchSource, source enums.SourceType) (metadataSearchSource, bool) {
	source = gamehelper.NormalizeMetadataSourceType(source)
	for _, item := range sources {
		if gamehelper.NormalizeMetadataSourceType(item.source) == source {
			return item, true
		}
	}
	return metadataSearchSource{}, false
}

func fetchImportMetadataSource(src metadataSearchSource, searchName string) ([]vo.GameMetadataFromWebVO, *vo.BatchImportMetadataSourceError) {
	metaResults, err := src.fetchCandidates(searchName)
	if err != nil {
		if isMetadataNoResultError(err) {
			return nil, nil
		}
		return nil, &vo.BatchImportMetadataSourceError{
			Source:      src.source,
			Error:       err.Error(),
			RateLimited: metadata.IsRateLimitError(err),
		}
	}
	matches := make([]vo.GameMetadataFromWebVO, 0, len(metaResults))
	for _, metaResult := range metaResults {
		if gamehelper.IsEmptyGame(metaResult.Game) {
			continue
		}
		matches = append(matches, vo.GameMetadataFromWebVO{
			Source: src.source,
			Game:   metaResult.Game,
			Tags:   metaResult.Tags,
		})
	}
	return matches, nil
}

func isMetadataNoResultError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no results found") ||
		strings.Contains(message, "returned no game data") ||
		strings.Contains(message, "returned no data")
}

// metadataSearchSources 返回当前配置下的元数据来源清单。
// 装配逻辑统一在 buildConfiguredMetadataSearchSources，避免与 GameService 各持一份。
func (s *ImportService) metadataSearchSources() []metadataSearchSource {
	return buildConfiguredMetadataSearchSources(metadataSourceDeps{
		ctx:               s.ctx,
		config:            s.config,
		bangumiService:    s.bangumiService,
		hikarinagiService: s.hikarinagiService,
		nextMoeService:    s.nextMoeService,
	})
}

// CheckImportMetadataDuplicates 批量检查元数据 source/id 是否已存在。
func (s *ImportService) CheckImportMetadataDuplicates(requests []vo.ImportMetadataDuplicateRequest) ([]vo.ImportMetadataDuplicateResult, error) {
	results := make([]vo.ImportMetadataDuplicateResult, 0, len(requests))
	if len(requests) == 0 {
		return results, nil
	}

	idx, err := s.importCommitter().LoadIndex()
	if err != nil {
		applog.LogErrorf(s.ctx, "CheckImportMetadataDuplicates: failed to load import index: %v", err)
		return results, fmt.Errorf("加载导入索引失败: %w", err)
	}

	for _, request := range requests {
		result := vo.ImportMetadataDuplicateResult{
			Source:   request.Source,
			SourceID: request.SourceID,
		}
		if ref, ok := idx.FindBySource(request.Source, request.SourceID); ok {
			result.Exists = true
			result.ExistingID = ref.ID
			result.ExistingName = ref.Name
		}
		results = append(results, result)
	}

	return results, nil
}

// BatchImportGames 批量导入游戏
func (s *ImportService) BatchImportGames(candidates []vo.BatchImportCandidate) (ImportResult, error) {
	result := ImportResult{
		FailedNames:  []string{},
		SkippedNames: []string{},
	}

	startedAt := time.Now()
	stepStartedAt := time.Now()
	committer := s.importCommitter()
	idx, err := committer.LoadIndex()
	if err != nil {
		applog.LogErrorf(s.ctx, "BatchImportGames: failed to load import index: %v", err)
		return result, fmt.Errorf("加载导入索引失败: %w", err)
	}
	applog.LogInfof(s.ctx, "BatchImportGames: loaded import index for candidates=%d elapsed=%s", len(candidates), time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	items := make([]importer.CommitItem, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.IsSelected {
			continue
		}

		if ref, exists := idx.FindByPathConflict(candidate.SelectedExe); exists {
			applog.LogWarningf(s.ctx, "BatchImportGames: path already exists for game %s, skipping: %s", ref.Name, candidate.SelectedExe)
			result.Skipped++
			result.SkippedNames = append(result.SkippedNames, candidate.SearchName+" (路径已存在: "+ref.Name+")")
			continue
		}

		gameName := candidate.SearchName
		if candidate.MatchedGame != nil && candidate.MatchedGame.Name != "" {
			gameName = candidate.MatchedGame.Name
		}

		if ref, exists := idx.FindByNamePath(gameName, candidate.SelectedExe); exists {
			applog.LogWarningf(s.ctx, "BatchImportGames: game already exists with same path, skipping: %s", gameName)
			result.Skipped++
			result.SkippedNames = append(result.SkippedNames, gameName+" (已存在: "+ref.Name+")")
			continue
		}
		if ref, exists := idx.FindByName(gameName); exists && importpath.Normalize(ref.Path) != importpath.Normalize(candidate.SelectedExe) {
			applog.LogInfof(s.ctx, "BatchImportGames: importing duplicate name %s with different path: %s", gameName, candidate.SelectedExe)
		}

		var game models.Game
		if candidate.MatchedGame != nil {
			game = *candidate.MatchedGame
		} else {
			game = models.Game{
				Name:       candidate.SearchName,
				SourceType: enums.Local,
			}
		}

		game.ID = uuid.New().String()
		game.Path = candidate.SelectedExe
		game.GameDirectory = strings.TrimSpace(candidate.GameDirectory)
		if game.GameDirectory == "" {
			game.GameDirectory = strings.TrimSpace(candidate.FolderPath)
		}
		game.CreatedAt = time.Now()
		game.CachedAt = time.Now()
		game.UpdatedAt = time.Now()

		source := candidate.MatchSource
		if source == "" {
			source = game.SourceType
		}
		if game.SourceType == "" {
			game.SourceType = source
		}
		sourceRefs := append([]models.GameMetadataSource(nil), game.MetadataSources...)
		if len(sourceRefs) == 0 && source != "" && source != enums.Local && strings.TrimSpace(game.SourceID) != "" {
			sourceRefs = append(sourceRefs, models.GameMetadataSource{SourceType: source, SourceID: game.SourceID})
		}
		if s.config == nil || !s.config.AllowDuplicateMetadataImport {
			duplicateName := ""
			for _, metadataSource := range sourceRefs {
				if sourceRef, exists := idx.FindBySource(metadataSource.SourceType, metadataSource.SourceID); exists {
					duplicateName = sourceRef.Name
					applog.LogWarningf(s.ctx, "BatchImportGames: source already exists for game %s, skipping: %s/%s", sourceRef.Name, metadataSource.SourceType, metadataSource.SourceID)
					break
				}
			}
			if duplicateName != "" {
				result.Skipped++
				result.SkippedNames = append(result.SkippedNames, gameName+" (元数据已存在: "+duplicateName+")")
				continue
			}
		}

		item := importer.CommitItem{
			Game:   game,
			Tags:   candidate.MatchedTags,
			Source: source,
			Action: importer.ImportActionCreate,
		}
		items = append(items, item)
		baseRef := importer.GameRef{
			ID:         game.ID,
			Name:       game.Name,
			Path:       game.Path,
			SourceType: game.SourceType,
			SourceID:   game.SourceID,
			CreatedAt:  game.CreatedAt,
		}
		idx.Add(baseRef)
		for _, metadataSource := range sourceRefs {
			baseRef.SourceType = metadataSource.SourceType
			baseRef.SourceID = metadataSource.SourceID
			idx.Add(baseRef)
		}
	}
	applog.LogInfof(s.ctx, "BatchImportGames: built commit items=%d skipped=%d elapsed=%s", len(items), result.Skipped, time.Since(stepStartedAt))

	stepStartedAt = time.Now()
	success, sessionsImported, err := committer.CommitItems(items)
	if err != nil {
		applog.LogErrorf(s.ctx, "BatchImportGames: batch import failed: %v", err)
		result.Failed += len(items)
		for _, item := range items {
			result.FailedNames = append(result.FailedNames, item.Game.Name)
		}
		return result, err
	}
	result.Success += success
	result.SessionsImported += sessionsImported
	applog.LogInfof(s.ctx, "BatchImportGames: complete success=%d skipped=%d failed=%d sessions=%d commit_elapsed=%s total=%s", result.Success, result.Skipped, result.Failed, result.SessionsImported, time.Since(stepStartedAt), time.Since(startedAt))

	return result, nil
}

// ProcessDroppedPaths 处理拖拽导入的路径，支持文件夹和可执行文件
// 返回候选游戏列表供前端展示和确认
func (s *ImportService) ProcessDroppedPaths(paths []string) (vo.BatchImportScanResult, error) {
	return s.ProcessDroppedPathsWithOptions(paths, vo.BatchImportScanOptions{})
}

// ProcessDroppedPathsWithOptions 处理拖拽导入的路径，并复用批量导入的扫描命名选项。
func (s *ImportService) ProcessDroppedPathsWithOptions(paths []string, options vo.BatchImportScanOptions) (vo.BatchImportScanResult, error) {
	var candidates []vo.BatchImportCandidate
	var result vo.BatchImportScanResult

	excludeKeywords := defaultImportExcludeKeywords()
	const maxDepth = 3
	candidatesMap := make(map[string]vo.BatchImportCandidate)
	scanOptions := normalizeBatchImportScanOptions(options)

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			applog.LogWarningf(s.ctx, "ProcessDroppedPaths: failed to stat path %s: %v", path, err)
			continue
		}

		if info.IsDir() {
			beforeCount := len(candidatesMap)
			if scanOptions.ScanMode == "hierarchy" {
				s.scanDroppedDirectoryByHierarchy(path, scanOptions.HierarchyDepth, candidatesMap)
			} else if err := s.scanDroppedDirectoryRecursive(path, maxDepth, excludeKeywords, scanOptions, candidatesMap); err != nil {
				applog.LogWarningf(s.ctx, "ProcessDroppedPaths: failed to scan directory %s: %v", path, err)
				continue
			}

			if len(candidatesMap) == beforeCount {
				applog.LogInfof(s.ctx, "ProcessDroppedPaths: no executable found in folder %s", path)
			}
			continue
		}

		lowerName := strings.ToLower(path)
		if !strings.HasSuffix(lowerName, ".exe") && !strings.HasSuffix(lowerName, ".bat") {
			applog.LogInfof(s.ctx, "ProcessDroppedPaths: skipping non-executable file %s", path)
			continue
		}

		fileName := filepath.Base(path)
		if shouldExcludeExecutable(fileName, excludeKeywords) {
			applog.LogInfof(s.ctx, "ProcessDroppedPaths: skipping excluded file %s", path)
			continue
		}

		folderPath := filepath.Dir(path)
		candidatesMap[folderPath] = vo.BatchImportCandidate{
			FolderPath:    folderPath,
			GameDirectory: folderPath,
			FolderName:    filepath.Base(folderPath),
			Executables:   []string{path},
			SelectedExe:   path,
			SearchName:    searchNameForExecutable(fileName, folderPath),
			IsSelected:    true,
			MatchStatus:   "pending",
		}
	}

	for _, candidate := range candidatesMap {
		candidates = append(candidates, candidate)
	}

	idx, err := s.importCommitter().LoadIndex()
	if err != nil {
		applog.LogErrorf(s.ctx, "ProcessDroppedPaths: failed to load import index: %v", err)
		return result, fmt.Errorf("加载导入索引失败: %w", err)
	}

	result = importer.SplitScanCandidates(candidates, idx, s.config != nil && s.config.AllowDuplicateMetadataImport)
	applog.LogInfof(s.ctx, "ProcessDroppedPaths: processed %d paths, found %d candidates, %d importable, %d skipped", len(paths), len(candidates), len(result.Candidates), result.Skipped)
	return result, nil
}

func (s *ImportService) scanDroppedDirectoryRecursive(
	path string,
	maxDepth int,
	excludeKeywords []string,
	options vo.BatchImportScanOptions,
	candidatesMap map[string]vo.BatchImportCandidate,
) error {
	rootPath := path
	if options.ScanNameMode == "depth" {
		// A dragged directory is usually the user's top-level game folder.
		// Using its parent as the naming root makes name_depth=0 resolve to
		// that dropped folder instead of an inner executable directory.
		rootPath = filepath.Dir(path)
	}
	return s.scanDirectoryRecursive(rootPath, path, 0, maxDepth, excludeKeywords, options.ScanNameMode, options.NameDepth, candidatesMap)
}

func (s *ImportService) scanDroppedDirectoryByHierarchy(
	path string,
	hierarchyDepth int,
	candidatesMap map[string]vo.BatchImportCandidate,
) {
	currentDirs := []string{path}
	for depth := 0; depth < hierarchyDepth; depth++ {
		nextDirs := make([]string, 0)
		for _, dir := range currentDirs {
			entries, err := os.ReadDir(dir)
			if err != nil {
				applog.LogWarningf(s.ctx, "scanDroppedDirectoryByHierarchy: failed to read dir %s: %v", dir, err)
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() || shouldSkipImportDirectory(entry.Name()) {
					continue
				}
				nextDirs = append(nextDirs, filepath.Join(dir, entry.Name()))
			}
		}
		currentDirs = nextDirs
		if len(currentDirs) == 0 {
			return
		}
	}

	for _, dir := range currentDirs {
		candidatesMap[dir] = vo.BatchImportCandidate{
			FolderPath:    dir,
			GameDirectory: dir,
			FolderName:    filepath.Base(dir),
			Executables:   []string{},
			SelectedExe:   dir,
			SearchName:    filepath.Base(dir),
			IsSelected:    true,
			MatchStatus:   "pending",
		}
	}
}

func defaultImportExcludeKeywords() []string {
	return []string{
		"unins", "setup", "config", "patch", "update", "crashpad",
		"vc_redist", "dxwebsetup", "directx", "vcredist", "dotnet",
		"redistributable", "installer", "launcher_helper", "crashreporter",
		"updater", "uninstall", "删除", "卸载",
	}
}

func shouldExcludeExecutable(fileName string, excludeKeywords []string) bool {
	lowerFileName := strings.ToLower(fileName)
	for _, keyword := range excludeKeywords {
		if strings.Contains(lowerFileName, keyword) {
			return true
		}
	}
	return false
}

func searchNameForExecutable(fileName string, folderPath string) string {
	searchName := filepath.Base(folderPath)
	exeName := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	genericNames := []string{"game", "main", "start", "launch", "run", "play"}
	for _, generic := range genericNames {
		if strings.ToLower(exeName) == generic {
			return searchName
		}
	}
	if len(exeName) > 3 {
		return exeName
	}
	return searchName
}
