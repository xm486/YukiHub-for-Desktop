package test

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"yukihub/internal/models/yukihub"
	"yukihub/internal/service/importer"
)

// 合并语义验证：契约要求「总时长取最大值，不做覆盖」。
//
// 桌面端没有游戏级 total_play_time 列，总时长由会话聚合而来，因此"取最大值"
// 通过两个机制组合实现：
//  1. 会话并集去重（按 game_id + start + end），两端相同的物理会话只留一份；
//  2. 聚合补偿（convertYukiHubSessions）：快照 total_play_time 超过已录会话
//     总和的差额，补成一条确定性 UUID 的聚合会话。
//
// 最终桌面总时长 = max(桌面已录时长, 快照 total_play_time)。

func buildMergeSnapshot(t *testing.T, dir string) string {
	t.Helper()

	createdAt := time.Date(2026, time.September, 28, 8, 0, 0, 0, time.Local)
	s10 := time.Date(2026, time.September, 28, 10, 0, 0, 0, time.Local)
	s11 := time.Date(2026, time.September, 28, 11, 0, 0, 0, time.Local)

	// 快照里的会话与桌面端已有的那条是同一次物理游玩（同 UUID 同时间）。
	backup := yukihub.Backup{
		App:       "YukiHub",
		Schema:    5,
		CreatedAt: createdAt.UnixMilli(),
		Games: []yukihub.Game{{
			LocalID:       7,
			Title:         "合并目标",
			PlayStatus:    "playing",
			TotalPlayTime: 150_000, // 150 秒：桌面端只有 60 秒，快照更长
			CreatedAt:     createdAt.UnixMilli(),
			UpdatedAt:     createdAt.UnixMilli(),
		}},
		PlaySessions: []yukihub.PlaySession{
			{SessionUUID: "aaaaaaaa-1111-2222-3333-444444444444", GameLocalID: 7, GameTitle: "合并目标",
				StartTime: s10.UnixMilli(), EndTime: s10.Add(60 * time.Second).UnixMilli(),
				Duration: 60_000, CreatedAt: s10.UnixMilli(), UpdatedAt: s10.UnixMilli()},
			{SessionUUID: "bbbbbbbb-1111-2222-3333-444444444444", GameLocalID: 7, GameTitle: "合并目标",
				StartTime: s11.UnixMilli(), EndTime: s11.Add(30 * time.Second).UnixMilli(),
				Duration: 30_000, CreatedAt: s11.UnixMilli(), UpdatedAt: s11.UnixMilli()},
		},
	}
	return writeSnapshotFile(t, dir, backup)
}

func writeSnapshotFile(t *testing.T, dir string, backup yukihub.Backup) string {
	t.Helper()

	// 直接序列化为 gzip 文件，绕过导出器（导出器只读 DuckDB），
	// 便于构造导入侧的边界用例。
	data, err := json.Marshal(backup)
	if err != nil {
		t.Fatalf("序列化快照失败: %v", err)
	}
	path := filepath.Join(dir, "merge.ykbak")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建快照文件失败: %v", err)
	}
	writer := gzip.NewWriter(file)
	if _, err := writer.Write(data); err != nil {
		file.Close()
		t.Fatalf("写入快照失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		file.Close()
		t.Fatalf("关闭快照失败: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("保存快照失败: %v", err)
	}
	return path
}

// TestYukiHubImportMergeSessionsTakesMaxPlaytime 验证 merge_sessions 动作下
// 两端时长合并结果为取最大值。
func TestYukiHubImportMergeSessionsTakesMaxPlaytime(t *testing.T) {
	targetDB, targetCleanup := setupTestDB(t)
	defer targetCleanup()

	// 桌面端已有同名游戏，已录 60 秒会话（与快照中的第一条是同一次物理游玩）。
	createdAt := time.Date(2026, time.September, 28, 8, 0, 0, 0, time.Local)
	s10 := time.Date(2026, time.September, 28, 10, 0, 0, 0, time.Local)
	if _, err := targetDB.Exec(`
		INSERT INTO games (id, name, summary, status, source_type, source_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"existing-1", "合并目标", "桌面端简介", "playing", "local", "", createdAt, createdAt); err != nil {
		t.Fatalf("插入已有游戏失败: %v", err)
	}
	if _, err := targetDB.Exec(
		`INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"aaaaaaaa-1111-2222-3333-444444444444", "existing-1",
		s10, s10.Add(60*time.Second), 60, s10); err != nil {
		t.Fatalf("插入已有会话失败: %v", err)
	}

	snapshotPath := buildMergeSnapshot(t, t.TempDir())
	result, err := importer.NewYukiHubImporter(newTestImporterDependencies(targetDB)).
		Import(snapshotPath, false, importer.SamePathActionMergeSessions)
	if err != nil {
		t.Fatalf("合并导入失败: %v", err)
	}
	if result.Success != 1 || result.Skipped != 0 {
		t.Fatalf("合并导入结果 = success %d / skipped %d (%v), want 1/0",
			result.Success, result.Skipped, result.SkippedNames)
	}

	// 会话并集：60s（去重保留）+ 30s（新增）+ 60s（聚合补偿 150-90）= 150 秒。
	var totalSeconds int
	if err := targetDB.QueryRow(
		`SELECT COALESCE(SUM(duration), 0) FROM play_sessions WHERE game_id = 'existing-1'`).Scan(&totalSeconds); err != nil {
		t.Fatalf("统计时长失败: %v", err)
	}
	if totalSeconds != 150 {
		t.Errorf("合并后总时长 = %d 秒, want 150（取最大值）", totalSeconds)
	}
	var sessionCount int
	if err := targetDB.QueryRow(
		`SELECT COUNT(*) FROM play_sessions WHERE game_id = 'existing-1'`).Scan(&sessionCount); err != nil {
		t.Fatalf("统计会话失败: %v", err)
	}
	if sessionCount != 3 {
		t.Errorf("合并后会话数 = %d, want 3（并集去重 + 聚合补偿）", sessionCount)
	}
}

// TestYukiHubImportMergeUpdatesMetadata 验证 merge 动作下元数据被 Android 侧
// 权威数据覆盖，但桌面端本机字段（path）不受影响；游玩状态按手机版
// importGamesJson 的规则处理——**对端 updated_at 不早于本地才覆盖**。
func TestYukiHubImportMergeUpdatesMetadata(t *testing.T) {
	targetDB, targetCleanup := setupTestDB(t)
	defer targetCleanup()

	createdAt := time.Date(2026, time.September, 28, 8, 0, 0, 0, time.Local)
	localNewerAt := createdAt.Add(2 * time.Hour)
	if _, err := targetDB.Exec(`
		INSERT INTO games (id, name, summary, status, source_type, source_id, created_at, updated_at, path)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?),
			(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"existing-1", "合并目标", "桌面端旧简介", "completed", "local", "", createdAt, createdAt,
		// 用正斜杠写路径：这里只验证「不被导入覆盖」，不必掺进反斜杠转义
		"D:/Games/local/game.exe",
		// 本地更晚更新过：对端是旧快照，状态应保留本地值
		"existing-2", "本地更新过", "桌面端简介", "dropped", "local", "", createdAt, localNewerAt,
		"D:/Games/local/game2.exe"); err != nil {
		t.Fatalf("插入已有游戏失败: %v", err)
	}

	backup := yukihub.Backup{
		App:       "YukiHub",
		Schema:    5,
		CreatedAt: createdAt.UnixMilli(),
		Games: []yukihub.Game{
			{
				LocalID:       7,
				Title:         "合并目标",
				Description:   "Android 侧新简介",
				PlayStatus:    "playing",
				TotalPlayTime: 0,
				CreatedAt:     createdAt.UnixMilli(),
				UpdatedAt:     createdAt.UnixMilli(),
			},
			{
				LocalID:       8,
				Title:         "本地更新过",
				Description:   "Android 侧简介",
				PlayStatus:    "playing",
				TotalPlayTime: 0,
				CreatedAt:     createdAt.UnixMilli(),
				UpdatedAt:     createdAt.UnixMilli(),
			},
		},
	}
	snapshotPath := writeSnapshotFile(t, t.TempDir(), backup)

	result, err := importer.NewYukiHubImporter(newTestImporterDependencies(targetDB)).
		Import(snapshotPath, false, importer.SamePathActionMerge)
	if err != nil {
		t.Fatalf("合并导入失败: %v", err)
	}
	if result.Success != 2 {
		t.Fatalf("合并导入结果 = success %d / skipped %d (%v), want 2/0",
			result.Success, result.Skipped, result.SkippedNames)
	}

	var summary, gamePath, status string
	if err := targetDB.QueryRow(
		`SELECT summary, COALESCE(path, ''), status FROM games WHERE id = 'existing-1'`).
		Scan(&summary, &gamePath, &status); err != nil {
		t.Fatalf("查询合并结果失败: %v", err)
	}
	if summary != "Android 侧新简介" {
		t.Errorf("summary = %q, want Android 侧新简介（Android 为权威源）", summary)
	}
	if gamePath != `D:/Games/local/game.exe` {
		t.Errorf("path = %q, want 保留桌面端本机路径", gamePath)
	}
	// 时间戳相等时按手机版规则覆盖（incoming.updated_at >= local.updated_at）。
	if status != "playing" {
		t.Errorf("status = %q, want playing（对端不早于本地时应覆盖）", status)
	}

	// 本地 updated_at 更晚：对端是旧快照，状态保留本地值。
	if err := targetDB.QueryRow(
		`SELECT status FROM games WHERE id = 'existing-2'`).Scan(&status); err != nil {
		t.Fatalf("查询 existing-2 失败: %v", err)
	}
	if status != "dropped" {
		t.Errorf("status = %q, want dropped（本地更新更晚时不得被旧快照覆盖）", status)
	}
}

// TestYukiHubSyncMergeAppliesNewerFieldsOnly 验证 sync_merge（云同步 / WebDAV
// 自持同步）对已有游戏的两条规则：
//
//  1. 对端 updated_at 不早于本地，才更新字段（与手机版 importGamesJson 一致）；
//  2. 文本字段「非空才覆盖」——对端可能只带 games 段、没有任何 metadata_cache，
//     那时 summary / company 全是空串，照抄会把桌面端刮削好的资料冲成空白。
//
// 这条规则只能用 sync_merge：merge_sessions 根本不碰游戏记录（手机端改过的
// 状态同步不回来），merge 又会无条件照抄空值（桌面端资料被抹平）。
func TestYukiHubSyncMergeAppliesNewerFieldsOnly(t *testing.T) {
	targetDB, targetCleanup := setupTestDB(t)
	defer targetCleanup()

	createdAt := time.Date(2026, time.September, 28, 8, 0, 0, 0, time.Local)
	localNewerAt := createdAt.Add(2 * time.Hour)
	if _, err := targetDB.Exec(`
		INSERT INTO games (id, name, summary, company, status, source_type, source_id, is_nsfw, hidden, path, created_at, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		// 对端更新：状态/隐藏应被采纳，空的文本字段不得抹掉桌面端资料
		"sync-1", "同步目标", "桌面端简介", "桌面端会社", "unplayed", "vndb", "v9999", false, false,
		"D:/Games/sync/game.exe", createdAt, createdAt,
		// 本地更晚更新过：对端是旧快照，状态应保留本地值
		"sync-2", "本地更新过", "桌面端简介", "", "dropped", "local", "", false, false,
		"D:/Games/sync/game2.exe", createdAt, localNewerAt,
		// 本地已隐藏，且快照**没带** hidden 键（老快照 / 只带 games 段的轻量快照）
		"sync-3", "本地已隐藏", "桌面端简介", "", "unplayed", "local", "", false, true,
		"D:/Games/sync/game3.exe", createdAt, createdAt); err != nil {
		t.Fatalf("插入已有游戏失败: %v", err)
	}

	backup := yukihub.Backup{
		App:       "YukiHub",
		Schema:    5,
		CreatedAt: createdAt.UnixMilli(),
		Games: []yukihub.Game{
			{
				LocalID: 1,
				Title:   "同步目标",
				// 对端没有 metadata_cache，所以这几个文本字段是空的
				Description:   "",
				Tags:          "",
				OriginalTitle: "",
				PlayStatus:    "completed",
				Hidden:        boolPtr(true),
				TotalPlayTime: 90_000,
				CreatedAt:     createdAt.UnixMilli(),
				UpdatedAt:     createdAt.Add(time.Hour).UnixMilli(),
			},
			{
				LocalID:       2,
				Title:         "本地更新过",
				PlayStatus:    "playing",
				TotalPlayTime: 0,
				CreatedAt:     createdAt.UnixMilli(),
				UpdatedAt:     createdAt.UnixMilli(),
			},
			{
				LocalID:       3,
				Title:         "本地已隐藏",
				PlayStatus:    "unplayed",
				TotalPlayTime: 0,
				// Hidden 保持 nil → JSON 里不会出现这个键，模拟老快照 / 轻量快照。
				// 手机版是 optBoolean("hidden", 本地值)：缺键必须保留本地隐藏状态。
				CreatedAt: createdAt.UnixMilli(),
				UpdatedAt: createdAt.UnixMilli(),
			},
		},
	}
	snapshotPath := writeSnapshotFile(t, t.TempDir(), backup)

	result, err := importer.NewYukiHubImporter(newTestImporterDependencies(targetDB)).
		Import(snapshotPath, false, importer.SamePathActionSyncMerge)
	if err != nil {
		t.Fatalf("同步合并导入失败: %v", err)
	}
	if result.Success != 3 {
		t.Fatalf("同步合并结果 = success %d / skipped %d (%v), want 3/0",
			result.Success, result.Skipped, result.SkippedNames)
	}

	var summary, company, status, sourceType, sourceID, gamePath string
	var hidden bool
	if err := targetDB.QueryRow(
		`SELECT summary, company, status, source_type, COALESCE(source_id, ''), COALESCE(path, ''), hidden
		 FROM games WHERE id = 'sync-1'`).
		Scan(&summary, &company, &status, &sourceType, &sourceID, &gamePath, &hidden); err != nil {
		t.Fatalf("查询同步合并结果失败: %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want completed（对端更新更晚时应采纳）", status)
	}
	if !hidden {
		t.Errorf("hidden = %v, want true（对端更新更晚时应采纳）", hidden)
	}
	if summary != "桌面端简介" {
		t.Errorf("summary = %q, want 保留桌面端简介（对端空值不得覆盖）", summary)
	}
	if company != "桌面端会社" {
		t.Errorf("company = %q, want 保留桌面端会社（对端空值不得覆盖）", company)
	}
	if sourceType != "vndb" || sourceID != "v9999" {
		t.Errorf("来源 = %s/%s, want 保留 vndb/v9999（对端没有元数据时不得抹成 local）", sourceType, sourceID)
	}
	if gamePath != "D:/Games/sync/game.exe" {
		t.Errorf("path = %q, want 保留桌面端本机路径", gamePath)
	}

	if err := targetDB.QueryRow(
		`SELECT status FROM games WHERE id = 'sync-2'`).Scan(&status); err != nil {
		t.Fatalf("查询 sync-2 失败: %v", err)
	}
	if status != "dropped" {
		t.Errorf("status = %q, want dropped（本地更新更晚时不得被旧快照覆盖）", status)
	}

	// 快照没带 hidden 键 → 本地已隐藏的状态必须保住。
	// 这是「同步一次就把藏好的游戏全翻出来」的直接回归点：
	// 用 bool 承载时缺键会解码成 false，被当成「明确不隐藏」写回。
	if err := targetDB.QueryRow(
		`SELECT COALESCE(hidden, FALSE) FROM games WHERE id = 'sync-3'`).Scan(&hidden); err != nil {
		t.Fatalf("查询 sync-3 失败: %v", err)
	}
	if !hidden {
		t.Error("快照缺 hidden 键时不得改动本地隐藏状态（否则一次同步就把藏好的游戏全翻出来）")
	}

	// 会话仍然并集去重 + 聚合补偿（total_play_time 90s → 一条聚合会话）。
	var totalSeconds int
	if err := targetDB.QueryRow(
		`SELECT COALESCE(SUM(duration), 0) FROM play_sessions WHERE game_id = 'sync-1'`).Scan(&totalSeconds); err != nil {
		t.Fatalf("统计时长失败: %v", err)
	}
	if totalSeconds != 90 {
		t.Errorf("同步合并后总时长 = %d 秒, want 90（会话并集 + 聚合补偿）", totalSeconds)
	}
}
