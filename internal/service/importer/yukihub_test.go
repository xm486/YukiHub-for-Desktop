package importer

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"yukihub/internal/common/enums"
	"yukihub/internal/models"
	"yukihub/internal/models/yukihub"
)

func TestYukiHubImporterPreviewAndImport(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.Local)
	backup := yukihub.Backup{
		App:       "YukiHub",
		Schema:    5,
		CreatedAt: createdAt.UnixMilli(),
		Settings: yukihub.BackupSettings{
			MetadataSource: "vndb",
		},
		Games: []yukihub.Game{
			{
				LocalID:       42,
				Title:         "测试游戏",
				OriginalTitle: "テストゲーム",
				Tags:          "剧情  校园",
				PlayStatus:    "completed",
				NSFW:          true,
				TotalPlayTime: 120_000,
				LastPlayedAt:  createdAt.Add(2 * time.Hour).UnixMilli(),
				CreatedAt:     createdAt.UnixMilli(),
				UpdatedAt:     createdAt.Add(3 * time.Hour).UnixMilli(),
			},
		},
		PlaySessions: []yukihub.PlaySession{
			{
				SessionUUID: "475fb291-a302-4776-8063-7c344691792f",
				GameLocalID: 42,
				StartTime:   createdAt.Add(time.Hour).UnixMilli(),
				EndTime:     createdAt.Add(time.Hour + 30*time.Second).UnixMilli(),
				Duration:    30_000,
				UpdatedAt:   createdAt.Add(time.Hour + 30*time.Second).UnixMilli(),
			},
		},
		MetadataCache: []yukihub.MetadataCache{
			{
				GameLocalID: 42,
				Source:      "vndb",
				SourceID:    "v123",
				JSON:        `{"id":"v123","chineseTitle":"测试游戏","originalTitle":"テストゲーム","romanTitle":"Test Game","coverUrl":"https://example.com/cover.jpg","translatedDescription":"测试简介","released":"2026-08-14","developer":"Test Studio","tagsText":"校园  悬疑","ratingText":"评分：8.4/10"}`,
				UpdatedAt:   createdAt.Add(3 * time.Hour).UnixMilli(),
			},
			{
				GameLocalID: 42,
				Source:      "bangumi",
				SourceID:    "987",
				JSON:        `{"id":"987","developer":"Alternative Studio"}`,
				UpdatedAt:   createdAt.Add(2 * time.Hour).UnixMilli(),
			},
		},
	}
	backupPath := writeYukiHubTestBackup(t, backup, true)

	var captured []ImportItem
	deps := Dependencies{
		ListGames: func() ([]models.Game, error) { return nil, nil },
		AddItems: func(items []ImportItem) (ImportResult, error) {
			captured = items
			sessionCount := 0
			for _, item := range items {
				sessionCount += len(item.Sessions)
			}
			return ImportResult{Success: len(items), SessionsImported: sessionCount}, nil
		},
	}
	service := NewYukiHubImporter(deps)

	previews, err := service.Preview(backupPath)
	if err != nil {
		t.Fatalf("Preview returned an error: %v", err)
	}
	if len(previews) != 1 {
		t.Fatalf("Preview count = %d, want 1", len(previews))
	}
	preview := previews[0]
	if preview.Name != "测试游戏" || preview.Developer != "Test Studio" {
		t.Fatalf("Unexpected preview: %+v", preview)
	}
	if preview.SourceType != string(enums.VNDB) || preview.SourceID != "v123" {
		t.Fatalf("Unexpected preview identity: %s %s", preview.SourceType, preview.SourceID)
	}
	if preview.HasPath {
		t.Fatal("YukiHub Android path must remain unavailable to the Windows importer")
	}

	result, err := service.Import(backupPath, false, SamePathActionSkip)
	if err != nil {
		t.Fatalf("Import returned an error: %v", err)
	}
	if result.Success != 1 || result.SessionsImported != 2 {
		t.Fatalf("Unexpected import result: %+v", result)
	}
	if len(captured) != 1 {
		t.Fatalf("Captured item count = %d, want 1", len(captured))
	}

	item := captured[0]
	game := item.Source.Game
	if game.Path != "" || game.GameDirectory != "" {
		t.Fatalf("Android path leaked into launch fields: %+v", game)
	}
	if game.Company != "Test Studio" || game.Summary != "测试简介" || game.Rating != 8.4 {
		t.Fatalf("Metadata mapping failed: %+v", game)
	}
	if game.Status != enums.StatusCompleted || game.SourceType != enums.VNDB || game.SourceID != "v123" {
		t.Fatalf("Identity or status mapping failed: %+v", game)
	}
	if !game.IsNSFW {
		t.Fatal("YukiHub nsfw flag was not imported")
	}
	if len(game.Aliases) != 2 || game.Aliases[0] != "テストゲーム" || game.Aliases[1] != "Test Game" {
		t.Fatalf("Unexpected aliases: %#v", game.Aliases)
	}
	if len(game.MetadataSources) != 2 {
		t.Fatalf("Metadata source count = %d, want 2", len(game.MetadataSources))
	}
	// Android 的 metadata_cache 负载必须原样保留：桌面端没有截图、罗马音标题等列，
	// 丢掉这份 JSON 会让「Android → 桌面端 → Android」的往返损失这些字段。
	cacheBySource := make(map[enums.SourceType]string, len(game.MetadataSources))
	sourceIDBySource := make(map[enums.SourceType]string, len(game.MetadataSources))
	for _, source := range game.MetadataSources {
		cacheBySource[source.SourceType] = source.CacheJSON
		sourceIDBySource[source.SourceType] = source.SourceID
	}
	if cacheBySource[enums.VNDB] != backup.MetadataCache[0].JSON {
		t.Errorf("vndb cache payload = %q, want %q", cacheBySource[enums.VNDB], backup.MetadataCache[0].JSON)
	}
	if cacheBySource[enums.Bangumi] != backup.MetadataCache[1].JSON {
		t.Errorf("bangumi cache payload = %q, want %q", cacheBySource[enums.Bangumi], backup.MetadataCache[1].JSON)
	}
	if sourceIDBySource[enums.VNDB] != "v123" || sourceIDBySource[enums.Bangumi] != "987" {
		t.Errorf("metadata source ids = %+v, want vndb=v123 / bangumi=987", sourceIDBySource)
	}

	totalDuration := 0
	for _, session := range item.Sessions {
		totalDuration += session.Duration
	}
	if totalDuration != 120 {
		t.Fatalf("Imported duration = %d seconds, want 120", totalDuration)
	}
	if len(item.Source.Tags) != 3 {
		t.Fatalf("Imported tags = %#v, want 3 unique tags", item.Source.Tags)
	}
}

func TestYukiHubImportKeepsMobileContractFields(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.Local)
	resetAt := createdAt.Add(24 * time.Hour)
	backupPath := writeYukiHubTestBackup(t, yukihub.Backup{
		App:    "YukiHub",
		Schema: 5,
		Games: []yukihub.Game{
			{
				LocalID:         42,
				Title:           "清零过的游戏",
				PlayStatus:      "playing",
				PlaytimeResetAt: resetAt.UnixMilli(),
				// favorite 由系统收藏分类承载，不落库；这里只是确认它不会串到 hidden 上。
				Hidden:    boolPtr(true),
				Favorite:  true,
				CreatedAt: createdAt.UnixMilli(),
				UpdatedAt: createdAt.Add(time.Hour).UnixMilli(),
			},
			{
				LocalID:   7,
				Title:     "未清零的游戏",
				CreatedAt: createdAt.UnixMilli(),
				UpdatedAt: createdAt.UnixMilli(),
			},
		},
	}, true)

	var captured []ImportItem
	service := NewYukiHubImporter(Dependencies{
		ListGames: func() ([]models.Game, error) { return nil, nil },
		AddItems: func(items []ImportItem) (ImportResult, error) {
			captured = items
			return ImportResult{Success: len(items)}, nil
		},
	})

	if _, err := service.Import(backupPath, false, SamePathActionSkip); err != nil {
		t.Fatalf("Import returned an error: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("Captured item count = %d, want 2", len(captured))
	}

	resetGame := captured[0].Source.Game
	if resetGame.LegacyLocalID != "42" {
		t.Errorf("LegacyLocalID = %q, want \"42\"", resetGame.LegacyLocalID)
	}
	if resetGame.PlaytimeResetAt == nil || !resetGame.PlaytimeResetAt.Equal(resetAt) {
		t.Errorf("PlaytimeResetAt = %v, want %v", resetGame.PlaytimeResetAt, resetAt)
	}
	if !resetGame.Hidden {
		t.Error("Hidden flag was not imported")
	}
	// 三态：明确带了键 → 必须原样带下去（合并导入靠它区分「明确表态」与「没信息」）。
	if captured[0].Hidden == nil || !*captured[0].Hidden {
		t.Errorf("ImportItem.Hidden = %v, want 明确的 true", captured[0].Hidden)
	}
	if resetGame.SourceDeviceID != "" {
		t.Errorf("SourceDeviceID = %q, want empty (Android backup has no device id)", resetGame.SourceDeviceID)
	}

	plainGame := captured[1].Source.Game
	if plainGame.LegacyLocalID != "7" {
		t.Errorf("LegacyLocalID = %q, want \"7\"", plainGame.LegacyLocalID)
	}
	if plainGame.PlaytimeResetAt != nil {
		t.Errorf("PlaytimeResetAt = %v, want nil for a zero playtime_reset_at", plainGame.PlaytimeResetAt)
	}
	if plainGame.Hidden {
		t.Error("Hidden must stay false when the backup does not set it")
	}
	// 但「没带键」在合并更新路径上必须是 nil（= 保留本地），不能是 false。
	if captured[1].Hidden != nil {
		t.Errorf("快照没带 hidden 时 ImportItem.Hidden 应为 nil，实际 %v", *captured[1].Hidden)
	}
}

// boolPtr 供测试构造三态布尔。
func boolPtr(value bool) *bool {
	return &value
}

func TestYukiHubLocalIDOnlyEncodesPositiveIDs(t *testing.T) {
	t.Parallel()

	cases := map[int64]string{
		0:    "",
		-1:   "",
		1:    "1",
		1024: "1024",
	}
	for input, want := range cases {
		if got := yukiHubLocalID(input); got != want {
			t.Errorf("yukiHubLocalID(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestLoadYukiHubBackupSupportsPlainJSONAndRejectsOtherApps(t *testing.T) {
	t.Parallel()

	path := writeYukiHubTestBackup(t, yukihub.Backup{App: "YukiHub", Schema: 5}, false)
	backup, err := loadYukiHubBackup(path)
	if err != nil {
		t.Fatalf("Plain JSON backup returned an error: %v", err)
	}
	if backup.Schema != 5 {
		t.Fatalf("Schema = %d, want 5", backup.Schema)
	}

	invalidPath := writeYukiHubTestBackup(t, yukihub.Backup{App: "OtherApp", Schema: 5}, true)
	if _, err := loadYukiHubBackup(invalidPath); err == nil {
		t.Fatal("Expected a validation error for another app")
	}
}

func TestYukiHubPreviewMatchesExistingGameByNameWithoutPath(t *testing.T) {
	t.Parallel()

	backupPath := writeYukiHubTestBackup(t, yukihub.Backup{
		App:    "YukiHub",
		Schema: 5,
		Games:  []yukihub.Game{{LocalID: 1, Title: "Existing Game"}},
	}, true)
	service := NewYukiHubImporter(Dependencies{
		ListGames: func() ([]models.Game, error) {
			return []models.Game{{ID: "existing-id", Name: "existing game"}}, nil
		},
	})

	previews, err := service.Preview(backupPath)
	if err != nil {
		t.Fatalf("Preview returned an error: %v", err)
	}
	if len(previews) != 1 || !previews[0].Exists || previews[0].ExistingID != "existing-id" {
		t.Fatalf("Existing game was not detected: %+v", previews)
	}
}

func TestMapYukiHubGameStatusCoversAllPlayStatuses(t *testing.T) {
	t.Parallel()

	cases := map[string]enums.GameStatus{
		"":             enums.StatusUnplayed,
		"unplayed":     enums.StatusUnplayed,
		"not_started":  enums.StatusUnplayed,
		"want_to_play": enums.StatusUnplayed,
		"playing":      enums.StatusPlaying,
		"completed":    enums.StatusCompleted,
		"onhold":       enums.StatusOnHold,
		"on_hold":      enums.StatusOnHold,
		"shelved":      enums.StatusOnHold,
		"dropped":      enums.StatusDropped,
		"abandoned":    enums.StatusDropped,
	}
	for input, want := range cases {
		if got := mapYukiHubGameStatus(input); got != want {
			t.Errorf("mapYukiHubGameStatus(%q) = %q, want %q", input, got, want)
		}
	}
}

func writeYukiHubTestBackup(t *testing.T, backup yukihub.Backup, compressed bool) string {
	t.Helper()

	data, err := json.Marshal(backup)
	if err != nil {
		t.Fatalf("Marshal backup: %v", err)
	}
	path := filepath.Join(t.TempDir(), "backup.ykbak")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create backup: %v", err)
	}
	if compressed {
		writer := gzip.NewWriter(file)
		if _, err := writer.Write(data); err != nil {
			file.Close()
			t.Fatalf("Write compressed backup: %v", err)
		}
		if err := writer.Close(); err != nil {
			file.Close()
			t.Fatalf("Close compressed backup: %v", err)
		}
	} else if _, err := file.Write(data); err != nil {
		file.Close()
		t.Fatalf("Write backup: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close backup: %v", err)
	}
	return path
}

// TestYukiHubImporterReportsSnapshotMetadataSource 快照里的 settings.metadata_source
// 必须能被调用方读到。
//
// 手机版 SyncManager.importSnapshot 读到这个键会落回本地设置；桌面端 service 层
// 同样要采纳（否则「手机端改了资料源」会被桌面端下次上传顶回去）。导入器本身不碰
// 配置，只负责原样透出。
func TestYukiHubImporterReportsSnapshotMetadataSource(t *testing.T) {
	t.Parallel()

	backup := yukihub.Backup{
		App:      "YukiHub",
		Schema:   5,
		Settings: yukihub.BackupSettings{MetadataSource: "nextmoe"},
	}
	backupPath := writeYukiHubTestBackup(t, backup, true)

	service := NewYukiHubImporter(Dependencies{
		ListGames: func() ([]models.Game, error) { return nil, nil },
		AddItems: func(items []ImportItem) (ImportResult, error) {
			return ImportResult{Success: len(items)}, nil
		},
	})

	if got := service.MetadataSource(); got != "" {
		t.Errorf("加载前的 MetadataSource() = %q, want 空串", got)
	}
	if _, err := service.Import(backupPath, false, SamePathActionSyncMerge); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := service.MetadataSource(); got != "nextmoe" {
		t.Errorf("MetadataSource() = %q, want nextmoe", got)
	}
}

// TestYukiHubImporterReportsEmptyMetadataSource 快照没写 settings 段时不能凭空造值。
func TestYukiHubImporterReportsEmptyMetadataSource(t *testing.T) {
	t.Parallel()

	backupPath := writeYukiHubTestBackup(t, yukihub.Backup{App: "YukiHub", Schema: 5}, false)

	service := NewYukiHubImporter(Dependencies{
		ListGames: func() ([]models.Game, error) { return nil, nil },
		AddItems: func(items []ImportItem) (ImportResult, error) {
			return ImportResult{Success: len(items)}, nil
		},
	})
	if _, err := service.Import(backupPath, false, SamePathActionSyncMerge); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := service.MetadataSource(); got != "" {
		t.Errorf("MetadataSource() = %q, want 空串", got)
	}
}
