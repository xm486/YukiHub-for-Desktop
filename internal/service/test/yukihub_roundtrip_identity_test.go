package test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"yukihub/internal/models/yukihub"
	"yukihub/internal/service/exporter"
	"yukihub/internal/service/importer"
)

// 这组测试锁的是「电脑同步完，手机里图标全变双份」那个线上问题。
//
// 手机版 GameRepository.findBySyncIdentity（:840）的匹配顺序是：
//
//	1) findByRootUri(root_uri)          —— normalizeRootUriKey 相等才算命中
//	2) findByGameHubLocalId(...)        —— gamehub_local_game_id / gaishi_local_game_id
//	3) findByTitleForEmptyRoot(title)   —— **仅当本地那一行的 root_uri 为空**
//
// 三档全落空时手机端 `db.insert("games", ...)`，也就是新建一条 —— 这就是重复。
//
// 桌面端原来导出 root_uri 恒为空串（为了不泄露 D:\ 路径），但 Android 侧扫描进来的
// 游戏几乎都有非空 root_uri，第 3 档又只对「本地 root_uri 为空」的行生效 ⇒
// 三档全落空 ⇒ PC 同步一次，手机整库翻倍。修法是新增 games.legacy_root_uri，
// 导入时保存对端原值、导出时原样回写。

// androidSeedRootURI 是手机端扫描目录得到的典型 root_uri（含百分号编码的 docId）。
const androidSeedRootURI = "content://com.android.externalstorage.documents/tree/primary%3AGames%2FSakuraNoUta"

// phoneRow 是手机端 games 表里参与身份匹配的那几列。
type phoneRow struct {
	rootURI   string
	gamehubID string
	title     string
}

// androidNormalizeRootURI 复刻 GameRepository.normalizeRootUriKey（:1020）里与本次
// 相关的部分：去 file:// 前缀、折叠重复斜杠、去尾部斜杠、转小写。
// content:// 的 DocumentsContract 取 docId 那一步需要 Android 运行时，这里不做；
// 因为测试里的值两端逐字相同，等价性不受影响。
func androidNormalizeRootURI(value string) string {
	s := strings.TrimSpace(value)
	s = strings.TrimPrefix(s, "file://")
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	for len(s) > 1 && strings.HasSuffix(s, "/") {
		s = strings.TrimSuffix(s, "/")
	}
	return strings.ToLower(s)
}

// androidFindBySyncIdentity 返回命中的手机端行下标，-1 表示「认不出来」（会新建）。
func androidFindBySyncIdentity(rows []phoneRow, incoming yukihub.Game) int {
	if strings.TrimSpace(incoming.RootUri) != "" {
		want := androidNormalizeRootURI(incoming.RootUri)
		for i, row := range rows {
			if androidNormalizeRootURI(row.rootURI) == want {
				return i
			}
		}
	}
	gamehubID := strings.TrimSpace(incoming.GamehubLocalGameId)
	if gamehubID == "" {
		gamehubID = strings.TrimSpace(incoming.GaishiLocalGameId)
	}
	if gamehubID != "" {
		for i, row := range rows {
			if row.gamehubID == gamehubID {
				return i
			}
		}
	}
	title := strings.TrimSpace(incoming.Title)
	if title != "" {
		for i, row := range rows {
			// findByTitleForEmptyRoot 的 WHERE 里带 IFNULL(root_uri,'')=''
			if strings.TrimSpace(row.rootURI) == "" && strings.TrimSpace(row.title) == title {
				return i
			}
		}
	}
	return -1
}

// TestYukiHubRoundTripKeepsAndroidRootUri 覆盖「手机 → 桌面 → 手机」的往返：
// 导出必须把 root_uri 原样带回，否则手机端认不出、整库翻倍。
func TestYukiHubRoundTripKeepsAndroidRootUri(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	createdAt := time.Date(2026, time.October, 26, 9, 0, 0, 0, time.Local)
	if _, err := db.Exec(`
		INSERT INTO games (id, name, path, game_directory, status, source_type, source_id,
			created_at, updated_at, legacy_local_id, legacy_root_uri)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"game-android", "サクラノ詩", `D:\Games\SakuraNoUta\game.exe`, `D:\Games\SakuraNoUta`,
		"playing", "local", "", createdAt, createdAt, "42", androidSeedRootURI); err != nil {
		t.Fatalf("插入游戏失败: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"session-1", "game-android", createdAt, createdAt.Add(30*time.Minute), 1800, createdAt); err != nil {
		t.Fatalf("插入会话失败: %v", err)
	}

	backup, err := exporter.NewYukiHubExporter(context.Background(), db).Build()
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if len(backup.Games) != 1 {
		t.Fatalf("导出游戏数 = %d, want 1", len(backup.Games))
	}
	entry := backup.Games[0]
	if entry.RootUri != androidSeedRootURI {
		t.Fatalf("games[0].root_uri = %q, want %q\n"+
			"丢失 root_uri ⇒ 手机端 findBySyncIdentity 三档全落空 ⇒ 新建一条重复游戏",
			entry.RootUri, androidSeedRootURI)
	}
	// 会话也要带上同一个 root_uri：手机端 importPlaySessionsJson 先 findByRootUri，
	// 认不出就 continue —— 会话会被整批丢掉（或挂到那条重复条目上）。
	if len(backup.PlaySessions) != 1 {
		t.Fatalf("导出会话数 = %d, want 1", len(backup.PlaySessions))
	}
	if backup.PlaySessions[0].GameRootUri != androidSeedRootURI {
		t.Errorf("play_sessions[0].game_root_uri = %q, want %q",
			backup.PlaySessions[0].GameRootUri, androidSeedRootURI)
	}

	// 用手机端的匹配算法再验一次：应命中它自己那一行（0），而不是新建（-1）。
	phone := []phoneRow{{rootURI: androidSeedRootURI, title: "サクラノ詩"}}
	if idx := androidFindBySyncIdentity(phone, entry); idx != 0 {
		t.Errorf("手机端没有认领这条游戏（返回 %d），会插入重复条目", idx)
	}

	// 反向自检：把 root_uri 抹掉必须让它「认不出来」—— 否则这套断言没在验证任何东西。
	mangled := entry
	mangled.RootUri = ""
	if idx := androidFindBySyncIdentity(phone, mangled); idx != -1 {
		t.Errorf("root_uri 为空时手机端本应认不出来，实际命中 %d（测试夹具失效）", idx)
	}
}

// TestYukiHubImportStoresAndroidRootUri 覆盖导入侧保存 + 再次导出回写。
//
// 这条是新升级用户的恢复路径：桌面端已有的行没有 legacy_root_uri，
// 但只要**再导入一次手机上传的快照**，值就会被补上，之后再导出手机端就能认领。
func TestYukiHubImportStoresAndroidRootUri(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	createdAt := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.Local)
	snapshot := yukihub.Backup{
		App:       "YukiHub",
		Schema:    5,
		CreatedAt: createdAt.UnixMilli(),
		Games: []yukihub.Game{{
			LocalID:    7,
			Title:      "手机扫描进来的游戏",
			RootUri:    androidSeedRootURI,
			PlayStatus: "playing",
			CreatedAt:  createdAt.UnixMilli(),
			UpdatedAt:  createdAt.UnixMilli(),
		}},
	}
	path := writeSnapshotFile(t, t.TempDir(), snapshot)

	if _, err := importer.NewYukiHubImporter(newTestImporterDependencies(db)).
		Import(path, false, importer.SamePathActionSyncMerge); err != nil {
		t.Fatalf("导入失败: %v", err)
	}

	var stored string
	if err := db.QueryRow(`SELECT COALESCE(legacy_root_uri, '') FROM games`).Scan(&stored); err != nil {
		t.Fatalf("读取 legacy_root_uri 失败: %v", err)
	}
	if stored != androidSeedRootURI {
		t.Fatalf("导入后 legacy_root_uri = %q, want %q", stored, androidSeedRootURI)
	}

	// path 必须保持桌面端自己的（空），不能被 Android 路径污染。
	var path0, dir string
	if err := db.QueryRow(`SELECT COALESCE(path, ''), COALESCE(game_directory, '') FROM games`).Scan(&path0, &dir); err != nil {
		t.Fatalf("读取 path 失败: %v", err)
	}
	if path0 != "" || dir != "" {
		t.Errorf("对端 root_uri 不得写进本机路径：path=%q dir=%q", path0, dir)
	}

	backup, err := exporter.NewYukiHubExporter(context.Background(), db).Build()
	if err != nil {
		t.Fatalf("回导失败: %v", err)
	}
	if got := backup.Games[0].RootUri; got != androidSeedRootURI {
		t.Errorf("再次导出的 root_uri = %q, want %q（往返丢身份键 ⇒ 手机端重复）", got, androidSeedRootURI)
	}
}

// TestYukiHubSyncMergeNeverClearsLegacyRootUri 保证「没有这条信息的快照」不会把
// 已保存的身份键抹掉 —— 老版本桌面端 / 手机端导出的快照都是 root_uri: ""。
func TestYukiHubSyncMergeNeverClearsLegacyRootUri(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	createdAt := time.Date(2026, time.October, 26, 11, 0, 0, 0, time.Local)
	if _, err := db.Exec(`
		INSERT INTO games (id, name, status, source_type, source_id, created_at, updated_at, legacy_root_uri)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"game-1", "已存身份键的游戏", "playing", "local", "", createdAt, createdAt, androidSeedRootURI); err != nil {
		t.Fatalf("插入游戏失败: %v", err)
	}

	// 快照更旧、且 root_uri 为空（= 只说「没有这条信息」）。
	snapshot := yukihub.Backup{
		App:       "YukiHub",
		Schema:    5,
		CreatedAt: createdAt.UnixMilli(),
		Games: []yukihub.Game{{
			Title:      "已存身份键的游戏",
			RootUri:    "",
			PlayStatus: "playing",
			CreatedAt:  createdAt.UnixMilli(),
			UpdatedAt:  createdAt.Add(-2 * time.Hour).UnixMilli(),
		}},
	}
	path := writeSnapshotFile(t, t.TempDir(), snapshot)
	if _, err := importer.NewYukiHubImporter(newTestImporterDependencies(db)).
		Import(path, false, importer.SamePathActionSyncMerge); err != nil {
		t.Fatalf("导入失败: %v", err)
	}

	var stored string
	if err := db.QueryRow(`SELECT COALESCE(legacy_root_uri, '') FROM games`).Scan(&stored); err != nil {
		t.Fatalf("读取 legacy_root_uri 失败: %v", err)
	}
	if stored != androidSeedRootURI {
		t.Fatalf("空 root_uri 的快照抹掉了身份键：%q", stored)
	}
}

// TestYukiHubImportMatchesByRootUriWhenTitleChanged 覆盖「手机端改过标题」的情况：
// 标题那一档会落空，只有 root_uri 还能认出是同一条，桌面上不该再长出一条。
func TestYukiHubImportMatchesByRootUriWhenTitleChanged(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	createdAt := time.Date(2026, time.October, 26, 13, 0, 0, 0, time.Local)
	if _, err := db.Exec(`
		INSERT INTO games (id, name, status, source_type, source_id, created_at, updated_at, legacy_root_uri)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"game-1", "旧标题", "playing", "local", "", createdAt, createdAt, androidSeedRootURI); err != nil {
		t.Fatalf("插入游戏失败: %v", err)
	}

	snapshot := yukihub.Backup{
		App:       "YukiHub",
		Schema:    5,
		CreatedAt: createdAt.UnixMilli(),
		Games: []yukihub.Game{{
			Title:      "新标题", // 手机端改过标题
			RootUri:    androidSeedRootURI,
			PlayStatus: "playing",
			CreatedAt:  createdAt.UnixMilli(),
			UpdatedAt:  createdAt.Add(time.Hour).UnixMilli(), // 对端更新
		}},
	}
	path := writeSnapshotFile(t, t.TempDir(), snapshot)
	result, err := importer.NewYukiHubImporter(newTestImporterDependencies(db)).
		Import(path, false, importer.SamePathActionSyncMerge)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if result.Success != 1 {
		t.Fatalf("应合并到已有条目，实际 success=%d skipped=%d (%v)", result.Success, result.Skipped, result.SkippedNames)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM games`).Scan(&count); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("游戏数 = %d, want 1（标题变了但 root_uri 相同，不得新建）", count)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM games WHERE id = 'game-1'`).Scan(&name); err != nil {
		t.Fatalf("读取名称失败: %v", err)
	}
	if name != "新标题" {
		t.Errorf("标题应被对端更新为「新标题」，实际 %q", name)
	}
}

// TestYukiHubExportDoesNotLeakDesktopPath 守住原来的意图：桌面端自己扫描的游戏
// 没有 legacy_root_uri，导出必须仍是空串（不能把 D:\... 发给手机）。
func TestYukiHubExportDoesNotLeakDesktopPath(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	createdAt := time.Date(2026, time.October, 26, 12, 0, 0, 0, time.Local)
	if _, err := db.Exec(`
		INSERT INTO games (id, name, path, game_directory, status, source_type, source_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"game-local", "桌面端自己的游戏", `D:\Games\Local\game.exe`, `D:\Games\Local`,
		"unplayed", "local", "", createdAt, createdAt); err != nil {
		t.Fatalf("插入游戏失败: %v", err)
	}

	backup, err := exporter.NewYukiHubExporter(context.Background(), db).Build()
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if got := backup.Games[0].RootUri; got != "" {
		t.Fatalf("桌面端自建游戏不得导出本机路径，实际 root_uri = %q", got)
	}

	// 键本身必须存在（手机端读 optString("root_uri","")，缺键与空串等价，
	// 但保留这个键能让快照结构稳定、也便于人工核对）。
	raw, err := json.Marshal(backup.Games[0])
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(raw), `"root_uri":""`) {
		t.Errorf("快照里应显式带 root_uri 空串，实际 %s", string(raw))
	}
}
