package test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"
	"yukihub/internal/models/yukihub"
	"yukihub/internal/service/importer"

	"github.com/google/uuid"
)

// 阶段 2 验收：Android 备份（schema 5 JSON）导入桌面端「万级样例 0 丢失、0 重复」。
//
// 这一组测试刻意走**真实的 Committer**（newTestImporterDependencies），
// 而不是只捕获内存里的 ImportItem —— 后者不会暴露 staging 落库环节的字段丢失。
// 见 TestYukiHubImportPersistsMobileContractFields。

const yukiHubScaleGames = 10000

type yukiHubScaleExpectation struct {
	path            string
	games           int
	sessions        int
	tags            int
	metadataSources int
	hidden          int
	nsfw            int
	playtimeReset   int
}

// buildYukiHubScaleSnapshot 生成一份万级规模的 schema 5 快照，并返回逐项期望值。
//
// 每款游戏都带一条唯一 vndb 元数据（source_id 唯一，避免被判为元数据重复），
// 会话数按 i%3 取 2/1/0，TotalPlayTime 额外加 i%4 秒的差额以触发「聚合补偿会话」。
func buildYukiHubScaleSnapshot(t *testing.T) yukiHubScaleExpectation {
	t.Helper()

	base := time.Date(2026, time.September, 28, 8, 0, 0, 0, time.Local)
	resetAt := base.Add(12 * time.Hour)
	statuses := []string{"unplayed", "playing", "completed", "onhold", "dropped"}

	expectation := yukiHubScaleExpectation{path: "", games: yukiHubScaleGames, metadataSources: yukiHubScaleGames}
	backup := yukihub.Backup{
		App:           "YukiHub",
		Schema:        5,
		CreatedAt:     base.UnixMilli(),
		Settings:      yukihub.BackupSettings{MetadataSource: "vndb"},
		Games:         make([]yukihub.Game, 0, yukiHubScaleGames),
		PlaySessions:  make([]yukihub.PlaySession, 0, yukiHubScaleGames*2),
		MetadataCache: make([]yukihub.MetadataCache, 0, yukiHubScaleGames),
	}

	for i := 0; i < yukiHubScaleGames; i++ {
		localID := int64(i + 1)
		title := fmt.Sprintf("规模测试游戏 %05d", i)
		originalTitle := fmt.Sprintf("Scale Game %05d", i)
		sessionCount := 2 - i%3
		if sessionCount < 0 {
			sessionCount = 0
		}
		bonusMillis := int64(i%4) * 1000

		game := yukihub.Game{
			LocalID:       localID,
			Title:         title,
			OriginalTitle: originalTitle,
			Tags:          "剧情,校园",
			PlayStatus:    statuses[i%len(statuses)],
			TotalPlayTime: int64(sessionCount)*60_000 + bonusMillis,
			Hidden:        boolPtr(i%7 == 0),
			NSFW:          i%11 == 0,
			CreatedAt:     base.UnixMilli(),
			UpdatedAt:     base.Add(time.Hour).UnixMilli(),
		}
		if i%5 == 0 {
			game.PlaytimeResetAt = resetAt.UnixMilli()
			expectation.playtimeReset++
		}
		if game.Hidden != nil && *game.Hidden {
			expectation.hidden++
		}
		if game.NSFW {
			expectation.nsfw++
		}
		backup.Games = append(backup.Games, game)
		expectation.tags += 2

		for j := 0; j < sessionCount; j++ {
			start := base.Add(time.Duration(i*120+j*30) * time.Minute)
			backup.PlaySessions = append(backup.PlaySessions, yukihub.PlaySession{
				SessionUUID: scaleSessionUUID(i, j),
				GameLocalID: localID,
				GameTitle:   title,
				StartTime:   start.UnixMilli(),
				EndTime:     start.Add(60 * time.Second).UnixMilli(),
				Duration:    60_000,
				CreatedAt:   start.UnixMilli(),
				UpdatedAt:   start.UnixMilli(),
			})
		}
		expectation.sessions += sessionCount
		// 快照总时长超出已录会话的部分会被补成一条聚合会话（convertYukiHubSessions）。
		if bonusMillis > 0 {
			expectation.sessions++
		}

		metadataJSON, err := json.Marshal(yukihub.Metadata{
			ID:            fmt.Sprintf("v%05d", i),
			ChineseTitle:  title,
			OriginalTitle: originalTitle,
			RomanTitle:    originalTitle,
			Developer:     fmt.Sprintf("Studio %03d", i%100),
			Released:      "2026-01-01",
			RatingText:    fmt.Sprintf("%.1f", float64(i%90)/10+1),
		})
		if err != nil {
			t.Fatalf("序列化元数据失败: %v", err)
		}
		backup.MetadataCache = append(backup.MetadataCache, yukihub.MetadataCache{
			GameLocalID: localID,
			Source:      "vndb",
			SourceID:    fmt.Sprintf("v%05d", i),
			JSON:        string(metadataJSON),
			UpdatedAt:   base.Add(time.Hour).UnixMilli(),
		})
	}

	expectation.path = writeSnapshotFile(t, t.TempDir(), backup)
	return expectation
}

func scaleSessionUUID(gameIndex int, sessionIndex int) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("yukihub-scale:%d:%d", gameIndex, sessionIndex))).String()
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("统计失败 (%s): %v", query, err)
	}
	return count
}

// TestYukiHubImportScaleNoLossNoDuplicates 验证万级导入 0 丢失、0 重复，并记录耗时。
func TestYukiHubImportScaleNoLossNoDuplicates(t *testing.T) {
	targetDB, cleanup := setupTestDB(t)
	defer cleanup()

	expectation := buildYukiHubScaleSnapshot(t)

	startedAt := time.Now()
	result, err := importer.NewYukiHubImporter(newTestImporterDependencies(targetDB)).
		Import(expectation.path, false, importer.SamePathActionSkip)
	elapsed := time.Since(startedAt)
	if err != nil {
		t.Fatalf("万级导入失败: %v", err)
	}
	t.Logf("万级导入耗时: %s（%d 游戏 / %d 会话）", elapsed, expectation.games, expectation.sessions)

	if result.Success != expectation.games || result.Failed != 0 || result.Skipped != 0 {
		t.Fatalf("导入结果 = success %d / failed %d / skipped %d, want %d/0/0",
			result.Success, result.Failed, result.Skipped, expectation.games)
	}
	if result.SessionsImported != expectation.sessions {
		t.Errorf("导入会话数 = %d, want %d", result.SessionsImported, expectation.sessions)
	}
	// 性能护栏：万级导入应为秒级；给一个宽松上限，防止出现 O(n²) 级别的退化。
	if elapsed > 90*time.Second {
		t.Errorf("万级导入耗时 %s，超出 90s 上限", elapsed)
	}

	if got := countRows(t, targetDB, `SELECT COUNT(*) FROM games`); got != expectation.games {
		t.Errorf("games 行数 = %d, want %d（0 丢失）", got, expectation.games)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(DISTINCT name) FROM games`); got != expectation.games {
		t.Errorf("去重后的游戏名数 = %d, want %d（0 重复）", got, expectation.games)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(DISTINCT legacy_local_id) FROM games`); got != expectation.games {
		t.Errorf("去重后的 legacy_local_id 数 = %d, want %d（身份键不得丢失/重复）", got, expectation.games)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(*) FROM play_sessions`); got != expectation.sessions {
		t.Errorf("play_sessions 行数 = %d, want %d", got, expectation.sessions)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(*) FROM game_tags`); got != expectation.tags {
		t.Errorf("game_tags 行数 = %d, want %d", got, expectation.tags)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(*) FROM game_metadata_sources`); got != expectation.metadataSources {
		t.Errorf("game_metadata_sources 行数 = %d, want %d", got, expectation.metadataSources)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(*) FROM games WHERE COALESCE(hidden, FALSE)`); got != expectation.hidden {
		t.Errorf("hidden 游戏数 = %d, want %d", got, expectation.hidden)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(*) FROM games WHERE COALESCE(is_nsfw, FALSE)`); got != expectation.nsfw {
		t.Errorf("nsfw 游戏数 = %d, want %d", got, expectation.nsfw)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(*) FROM games WHERE playtime_reset_at IS NOT NULL`); got != expectation.playtimeReset {
		t.Errorf("playtime_reset_at 非空数 = %d, want %d", got, expectation.playtimeReset)
	}

	// 抽查第一条（i=0）：hidden / nsfw / 清零时间都应落在对应列上。
	var (
		legacyID string
		hidden   bool
		nsfw     bool
		resetAt  time.Time
	)
	if err := targetDB.QueryRow(
		`SELECT legacy_local_id, COALESCE(hidden, FALSE), COALESCE(is_nsfw, FALSE), playtime_reset_at FROM games WHERE name = ?`,
		"规模测试游戏 00000").
		Scan(&legacyID, &hidden, &nsfw, &resetAt); err != nil {
		t.Fatalf("抽查首条游戏失败: %v", err)
	}
	if legacyID != "1" || !hidden || !nsfw {
		t.Errorf("首条游戏 legacy_local_id/hidden/nsfw = %q/%v/%v, want \"1\"/true/true", legacyID, hidden, nsfw)
	}
	if !resetAt.Equal(time.Date(2026, time.September, 28, 20, 0, 0, 0, time.Local)) {
		t.Errorf("首条游戏 playtime_reset_at = %v, want 2026-09-28 20:00", resetAt)
	}
}

// TestYukiHubImportScaleIsIdempotent 验证同一份万级快照重复导入不产生任何新条目或新会话。
func TestYukiHubImportScaleIsIdempotent(t *testing.T) {
	targetDB, cleanup := setupTestDB(t)
	defer cleanup()

	expectation := buildYukiHubScaleSnapshot(t)
	service := importer.NewYukiHubImporter(newTestImporterDependencies(targetDB))

	if _, err := service.Import(expectation.path, false, importer.SamePathActionSkip); err != nil {
		t.Fatalf("首次导入失败: %v", err)
	}
	startedAt := time.Now()
	second, err := service.Import(expectation.path, false, importer.SamePathActionSkip)
	elapsed := time.Since(startedAt)
	if err != nil {
		t.Fatalf("重复导入失败: %v", err)
	}
	t.Logf("万级重复导入耗时: %s", elapsed)

	if second.Success != 0 || second.Skipped != expectation.games || second.Failed != 0 {
		t.Fatalf("重复导入结果 = success %d / skipped %d / failed %d, want 0/%d/0",
			second.Success, second.Skipped, second.Failed, expectation.games)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(*) FROM games`); got != expectation.games {
		t.Errorf("重复导入后 games 行数 = %d, want %d（不得产生重复）", got, expectation.games)
	}
	if got := countRows(t, targetDB, `SELECT COUNT(*) FROM play_sessions`); got != expectation.sessions {
		t.Errorf("重复导入后 play_sessions 行数 = %d, want %d（会话幂等）", got, expectation.sessions)
	}
	if elapsed > 90*time.Second {
		t.Errorf("万级重复导入耗时 %s，超出 90s 上限", elapsed)
	}
}

// TestYukiHubImportPersistsMobileContractFields 走真实 Committer 落库，验证
// legacy_local_id / source_device_id / playtime_reset_at / hidden 四个契约列确实写入数据库。
//
// 契约字段在导入器里被填进 models.Game 只是第一步；若 staging 表与 INSERT 不含这些列，
// 它们会在落库时被静默丢弃 —— 这会破坏「回写与去重」的身份键（legacy_local_id），
// 并让清零语义、隐藏标记在桌面端丢失。
func TestYukiHubImportPersistsMobileContractFields(t *testing.T) {
	targetDB, cleanup := setupTestDB(t)
	defer cleanup()

	base := time.Date(2026, time.September, 28, 8, 0, 0, 0, time.Local)
	resetAt := base.Add(24 * time.Hour)
	backup := yukihub.Backup{
		App:       "YukiHub",
		Schema:    5,
		CreatedAt: base.UnixMilli(),
		Games: []yukihub.Game{
			{
				LocalID:         42,
				Title:           "契约字段游戏",
				PlayStatus:      "playing",
				PlaytimeResetAt: resetAt.UnixMilli(),
				Hidden:          boolPtr(true),
				CreatedAt:       base.UnixMilli(),
				UpdatedAt:       base.Add(time.Hour).UnixMilli(),
			},
			{
				LocalID:   7,
				Title:     "普通游戏",
				CreatedAt: base.UnixMilli(),
				UpdatedAt: base.UnixMilli(),
			},
		},
	}
	snapshotPath := writeSnapshotFile(t, t.TempDir(), backup)

	result, err := importer.NewYukiHubImporter(newTestImporterDependencies(targetDB)).
		Import(snapshotPath, false, importer.SamePathActionSkip)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if result.Success != 2 || result.Failed != 0 {
		t.Fatalf("导入结果 = success %d / failed %d, want 2/0", result.Success, result.Failed)
	}

	var (
		legacyID string
		deviceID string
		hidden   bool
		reset    time.Time
	)
	if err := targetDB.QueryRow(
		`SELECT legacy_local_id, source_device_id, COALESCE(hidden, FALSE), playtime_reset_at FROM games WHERE name = ?`,
		"契约字段游戏").
		Scan(&legacyID, &deviceID, &hidden, &reset); err != nil {
		t.Fatalf("查询契约字段游戏失败: %v", err)
	}
	if legacyID != "42" {
		t.Errorf("legacy_local_id = %q, want \"42\"", legacyID)
	}
	if deviceID != "" {
		t.Errorf("source_device_id = %q, want empty（Android 备份无设备标识）", deviceID)
	}
	if !hidden {
		t.Error("hidden 未落库")
	}
	if !reset.Equal(resetAt) {
		t.Errorf("playtime_reset_at = %v, want %v", reset, resetAt)
	}

	var plainReset *time.Time
	var plainLegacy string
	if err := targetDB.QueryRow(
		`SELECT legacy_local_id, playtime_reset_at FROM games WHERE name = ?`, "普通游戏").
		Scan(&plainLegacy, &plainReset); err != nil {
		t.Fatalf("查询普通游戏失败: %v", err)
	}
	if plainLegacy != "7" {
		t.Errorf("legacy_local_id = %q, want \"7\"", plainLegacy)
	}
	if plainReset != nil {
		t.Errorf("playtime_reset_at = %v, want NULL（0 不应落成 1970）", plainReset)
	}
}
