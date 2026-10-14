package test

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yukihub/internal/models"
	"yukihub/internal/models/yukihub"
	"yukihub/internal/service/exporter"
	"yukihub/internal/service/gamehelper"
	"yukihub/internal/service/importer"
)

func TestYukiHubExporterBuildFromEmptyDatabase(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	backup, err := exporter.NewYukiHubExporter(context.Background(), db).Build()
	if err != nil {
		t.Fatalf("空库导出失败: %v", err)
	}
	if backup.App != "YukiHub" || backup.Schema != 5 {
		t.Fatalf("快照头部 = %s/schema %d, want YukiHub/5", backup.App, backup.Schema)
	}
	if len(backup.Games) != 0 || len(backup.PlaySessions) != 0 || len(backup.MetadataCache) != 0 {
		t.Fatalf("空库导出应为空快照, got %d games / %d sessions", len(backup.Games), len(backup.PlaySessions))
	}
}

func TestYukiHubExporterBuildMapsDesktopLibrary(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	createdAt := time.Date(2026, time.September, 25, 10, 0, 0, 0, time.Local)
	resetAt := createdAt.Add(24 * time.Hour)
	if _, err := db.Exec(`
		INSERT INTO games (id, name, aliases, cover_url, summary, path, game_directory, status, source_type, source_id,
			created_at, updated_at, legacy_local_id, playtime_reset_at, hidden, is_nsfw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"game-1", "清零过的游戏", `["Reset Game"]`, "D:\\covers\\local.jpg", "简介",
		"D:\\Games\\one\\game.exe", "D:\\Games\\one", "playing", "local", "",
		createdAt, createdAt.Add(time.Hour), "42", resetAt, true, true); err != nil {
		t.Fatalf("插入游戏失败: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO games (id, name, cover_url, path, status, source_type, source_id, created_at, updated_at, legacy_local_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"game-2", "收藏的游戏", "https://example.com/cover.jpg", "D:\\Games\\two\\game.exe",
		"unplayed", "local", "", createdAt, createdAt.Add(time.Hour), "not-a-number"); err != nil {
		t.Fatalf("插入游戏失败: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO game_tags (id, game_id, name, source, weight, is_spoiler, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"tag-1", "game-2", "剧情", "vndb", 1.0, false, createdAt, createdAt); err != nil {
		t.Fatalf("插入标签失败: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO game_tags (id, game_id, name, source, weight, is_spoiler, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"tag-2", "game-2", "校园", "vndb", 1.0, false, createdAt, createdAt); err != nil {
		t.Fatalf("插入标签失败: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO game_categories (game_id, category_id, updated_at) VALUES (?, ?, ?)`,
		"game-2", gamehelper.SystemFavoritesCategoryID, createdAt); err != nil {
		t.Fatalf("插入收藏分类失败: %v", err)
	}

	// 清零之前的会话（90 秒）不导出；清零之后的会话（90 秒）导出为 90000 毫秒。
	beforeStart := createdAt.Add(time.Hour)
	afterStart := resetAt.Add(time.Hour)
	for _, session := range []struct {
		id        string
		gameID    string
		startTime time.Time
		duration  int
	}{
		{"session-before", "game-1", beforeStart, 90},
		{"session-after", "game-1", afterStart, 90},
		{"session-favorite", "game-2", afterStart, 60},
	} {
		if _, err := db.Exec(
			`INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			session.id, session.gameID, session.startTime, session.startTime.Add(time.Duration(session.duration)*time.Second),
			session.duration, session.startTime); err != nil {
			t.Fatalf("插入游玩记录失败: %v", err)
		}
	}

	backup, err := exporter.NewYukiHubExporter(context.Background(), db).Build()
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if len(backup.Games) != 2 {
		t.Fatalf("导出游戏数 = %d, want 2", len(backup.Games))
	}
	resetGame := backup.Games[0]
	if resetGame.LocalID != 42 {
		t.Errorf("local_id = %d, want 42", resetGame.LocalID)
	}
	if resetGame.PlaytimeResetAt != resetAt.UnixMilli() {
		t.Errorf("playtime_reset_at = %d, want %d", resetGame.PlaytimeResetAt, resetAt.UnixMilli())
	}
	if resetGame.TotalPlayTime != 90_000 {
		t.Errorf("total_play_time = %d, want 90000（只有清零之后的会话）", resetGame.TotalPlayTime)
	}
	if resetGame.LastPlayedAt != afterStart.Add(90*time.Second).UnixMilli() {
		t.Errorf("last_played_at = %d, want 清零之后那次会话的 end_time", resetGame.LastPlayedAt)
	}
	// 本地封面不迁移。
	if resetGame.CoverUri != "" {
		t.Errorf("cover_uri = %q, want empty for a local cover", resetGame.CoverUri)
	}
	// Hidden 是三态指针：导出侧必须**写了**这个键（非 nil），
	// 省略键会被手机版当成「对端没说」而保留它的旧值。
	if resetGame.Hidden == nil || !*resetGame.Hidden || !resetGame.NSFW || resetGame.PlayStatus != "playing" {
		t.Errorf("hidden/nsfw/play_status = %v/%v/%q", resetGame.Hidden, resetGame.NSFW, resetGame.PlayStatus)
	}

	favoriteGame := backup.Games[1]
	if favoriteGame.LocalID != 0 {
		t.Errorf("local_id = %d, want 0 for an unparsable legacy_local_id", favoriteGame.LocalID)
	}
	if !favoriteGame.Favorite {
		t.Error("系统收藏分类中的游戏应导出 favorite = true")
	}
	if favoriteGame.Tags != "剧情,校园" {
		t.Errorf("tags = %q, want \"剧情,校园\"", favoriteGame.Tags)
	}
	// 两端状态已统一，未玩的游戏原样导出为 unplayed。
	if favoriteGame.PlayStatus != "unplayed" {
		t.Errorf("play_status = %q, want unplayed", favoriteGame.PlayStatus)
	}
	if favoriteGame.CoverUri != "https://example.com/cover.jpg" {
		t.Errorf("cover_uri = %q, want the remote cover", favoriteGame.CoverUri)
	}

	if len(backup.PlaySessions) != 2 {
		t.Fatalf("导出会话数 = %d, want 2（清零之前的会话被过滤）", len(backup.PlaySessions))
	}
	exportedIDs := map[string]int64{}
	for _, session := range backup.PlaySessions {
		exportedIDs[session.SessionUUID] = session.Duration
	}
	if exportedIDs["session-after"] != 90_000 {
		t.Errorf("session-after duration = %d, want 90000 毫秒", exportedIDs["session-after"])
	}
	if exportedIDs["session-favorite"] != 60_000 {
		t.Errorf("session-favorite duration = %d, want 60000 毫秒", exportedIDs["session-favorite"])
	}
	if _, exists := exportedIDs["session-before"]; exists {
		t.Error("清零之前的会话不应导出")
	}
}

func TestYukiHubExporterExportIsIdempotentAcrossRuns(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	createdAt := time.Date(2026, time.September, 26, 10, 0, 0, 0, time.Local)
	if _, err := db.Exec(`
		INSERT INTO games (id, name, status, source_type, source_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"game-1", "重复导出的游戏", "completed", "local", "", createdAt, createdAt); err != nil {
		t.Fatalf("插入游戏失败: %v", err)
	}
	for index := 0; index < 35; index++ {
		startTime := createdAt.Add(time.Duration(index) * time.Minute)
		if _, err := db.Exec(
			`INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("session-%02d", index), "game-1", startTime, startTime.Add(30*time.Second), 30, startTime); err != nil {
			t.Fatalf("插入游玩记录失败: %v", err)
		}
	}

	service := exporter.NewYukiHubExporter(context.Background(), db)
	firstPath := filepath.Join(t.TempDir(), "first.ykbak")
	secondPath := filepath.Join(t.TempDir(), "second.ykbak")
	if err := service.Export(firstPath); err != nil {
		t.Fatalf("第一次导出失败: %v", err)
	}
	if err := service.Export(secondPath); err != nil {
		t.Fatalf("第二次导出失败: %v", err)
	}

	first := readYukiHubSnapshot(t, firstPath)
	second := readYukiHubSnapshot(t, secondPath)
	if len(first.PlaySessions) != 30 || len(second.PlaySessions) != 30 {
		t.Fatalf("导出会话数 = %d/%d, want 30/30", len(first.PlaySessions), len(second.PlaySessions))
	}
	for index := range first.PlaySessions {
		if first.PlaySessions[index].SessionUUID != second.PlaySessions[index].SessionUUID {
			t.Fatalf("第 %d 条会话的 session_uuid 两次导出不一致: %q vs %q",
				index, first.PlaySessions[index].SessionUUID, second.PlaySessions[index].SessionUUID)
		}
	}
	if first.PlaySessions[0].SessionUUID != "session-05" {
		t.Errorf("最早导出的会话 = %q, want \"session-05\"（只保留最新的 30 条）", first.PlaySessions[0].SessionUUID)
	}
	if first.Games[0].TotalPlayTime != 35*30*1000 {
		t.Errorf("total_play_time = %d, want %d", first.Games[0].TotalPlayTime, 35*30*1000)
	}
}

// TestYukiHubExportThenImportRoundTrip 用真实 importer 消费导出器产出的快照，
// 验证导出/导入两个方向共用同一份契约。
//
// 重点验证 root_uri 恒空后的匹配路径：快照里的条目没有路径，Android 侧靠标题匹配
// （findByTitleForEmptyRoot），桌面端导入器同样按标题命中已有游戏；若标题无法匹配，
// 才会作为新游戏落库（此时会因「无路径」被跳过）。
func TestYukiHubExportThenImportRoundTrip(t *testing.T) {
	sourceDB, sourceCleanup := setupTestDB(t)
	defer sourceCleanup()
	// 导入目标库必须与源库分离，否则导出源本就会让导入器把每条都判为「已存在」。
	targetDB, targetCleanup := setupTestDB(t)
	defer targetCleanup()

	createdAt := time.Date(2026, time.September, 27, 10, 0, 0, 0, time.Local)
	if _, err := sourceDB.Exec(`
		INSERT INTO games (id, name, aliases, cover_url, summary, path, game_directory, status, source_type, source_id,
			created_at, updated_at, legacy_local_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"game-1", "往返测试游戏", `["Round Trip"]`, "https://example.com/rt.jpg", "简介",
		"D:\\Games\\rt\\game.exe", "D:\\Games\\rt", "completed", "vndb", "v9000",
		createdAt, createdAt.Add(time.Hour), "42"); err != nil {
		t.Fatalf("插入游戏失败: %v", err)
	}
	if _, err := sourceDB.Exec(
		`INSERT INTO game_tags (id, game_id, name, source, weight, is_spoiler, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"tag-1", "game-1", "剧情", "user", 1.0, false, createdAt, createdAt); err != nil {
		t.Fatalf("插入标签失败: %v", err)
	}
	startedAt := createdAt.Add(2 * time.Hour)
	if _, err := sourceDB.Exec(
		`INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"session-round-trip", "game-1", startedAt, startedAt.Add(90*time.Second), 90, startedAt); err != nil {
		t.Fatalf("插入游玩记录失败: %v", err)
	}

	snapshotPath := filepath.Join(t.TempDir(), "round-trip.ykbak")
	if err := exporter.NewYukiHubExporter(context.Background(), sourceDB).Export(snapshotPath); err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	// 快照里不应出现任何 Windows 绝对路径。
	snapshot := readYukiHubSnapshot(t, snapshotPath)
	if len(snapshot.Games) != 1 {
		t.Fatalf("导出游戏数 = %d, want 1", len(snapshot.Games))
	}
	if snapshot.Games[0].RootUri != "" {
		t.Fatalf("root_uri = %q, want empty", snapshot.Games[0].RootUri)
	}

	// 目标库用同名同身份的游戏占位，模拟「同一款游戏已经在桌面端存在」。
	if _, err := targetDB.Exec(`
		INSERT INTO games (id, name, source_type, source_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"existing-game", "往返测试游戏", "vndb", "v9000", createdAt, createdAt); err != nil {
		t.Fatalf("插入目标库游戏失败: %v", err)
	}

	imported, err := importer.NewYukiHubImporter(newTestImporterDependencies(targetDB)).Import(snapshotPath, false, importer.SamePathActionSkip)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if imported.Skipped != 1 || imported.Success != 0 {
		t.Fatalf("导入结果 = success %d / skipped %d (%v), want 0/1：同标题已存在应被跳过",
			imported.Success, imported.Skipped, imported.SkippedNames)
	}

	// 导入方向对称性：对端没有路径，桌面端不应凭空写入 path。
	var count int
	if err := targetDB.QueryRow(`SELECT COUNT(*) FROM games WHERE id = ? AND COALESCE(path, '') = ''`, "existing-game").Scan(&count); err != nil {
		t.Fatalf("查询目标库游戏失败: %v", err)
	}
	if count != 1 {
		t.Error("导入不应把对端路径写进桌面端 path")
	}
	if err := targetDB.QueryRow(`SELECT COUNT(*) FROM games WHERE id = 'existing-game'`).Scan(&count); err != nil {
		t.Fatalf("查询目标库游戏失败: %v", err)
	}
	if count != 1 {
		t.Errorf("导入后同标题不应产生重复条目, got %d", count)
	}
}

// TestYukiHubImportAppliesFavoriteToSystemCategory 验证导入方向把快照的 favorite
// 落到「收藏」系统分类。
//
// 导出方向早已读取该分类（loadFavorites），导入方向此前完全丢弃 favorite，
// 造成「桌面端导出 → 手机版 → 回导」丢失收藏的不对称。
func TestYukiHubImportAppliesFavoriteToSystemCategory(t *testing.T) {
	targetDB, cleanup := setupTestDB(t)
	defer cleanup()

	createdAt := time.Date(2026, time.September, 28, 9, 0, 0, 0, time.Local)
	backup := yukihub.Backup{
		App:       "YukiHub",
		Schema:    5,
		CreatedAt: createdAt.UnixMilli(),
		Games: []yukihub.Game{
			{LocalID: 1, Title: "收藏的游戏", Favorite: true, CreatedAt: createdAt.UnixMilli(), UpdatedAt: createdAt.UnixMilli()},
			{LocalID: 2, Title: "普通的游戏", CreatedAt: createdAt.UnixMilli(), UpdatedAt: createdAt.UnixMilli()},
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

	var favoriteID string
	if err := targetDB.QueryRow(`SELECT id FROM games WHERE name = ?`, "收藏的游戏").Scan(&favoriteID); err != nil {
		t.Fatalf("查询收藏游戏失败: %v", err)
	}
	var count int
	if err := targetDB.QueryRow(`SELECT COUNT(*) FROM game_categories WHERE game_id = ? AND category_id = ?`,
		favoriteID, gamehelper.SystemFavoritesCategoryID).Scan(&count); err != nil {
		t.Fatalf("查询收藏分类失败: %v", err)
	}
	if count != 1 {
		t.Errorf("收藏游戏在 system:favorites 中的记录数 = %d, want 1", count)
	}

	var plainID string
	if err := targetDB.QueryRow(`SELECT id FROM games WHERE name = ?`, "普通的游戏").Scan(&plainID); err != nil {
		t.Fatalf("查询普通游戏失败: %v", err)
	}
	if err := targetDB.QueryRow(`SELECT COUNT(*) FROM game_categories WHERE game_id = ?`, plainID).Scan(&count); err != nil {
		t.Fatalf("查询普通游戏分类失败: %v", err)
	}
	if count != 0 {
		t.Errorf("非收藏游戏不应产生分类记录, got %d", count)
	}
}

// TestYukiHubMetadataCacheRoundTrip 验证元数据缓存负载在两个方向都能原样往返。
//
// 往返语义：桌面端存量缓存（已是 Android VnMetadata 结构）→ 导出快照 → 导入另一个库
// → 再导出，JSON 必须逐字节一致。
//
// 条目关联分两档，与手机版一致：有 legacy_local_id 的按 local_id，没有的（桌面端自己
// 扫描进来的游戏）按游戏标题。早期版本把后者整段跳过，直接后果就是
// 「PC 端用 nextmoe 刮的资料同步到手机端后全没了、退回默认的 vndb」——
// 手机端是靠 metadata_cache 里 source='nextmoe' 那一行取资料的。
func TestYukiHubMetadataCacheRoundTrip(t *testing.T) {
	sourceDB, sourceCleanup := setupTestDB(t)
	defer sourceCleanup()
	targetDB, targetCleanup := setupTestDB(t)
	defer targetCleanup()

	payload := `{"id":"v9000","chineseTitle":"往返缓存游戏","romanTitle":"Round Trip Cache","screenshotUrls":["https://example.com/s1.jpg"]}`
	selfPayload := `{"id":"v1"}`
	createdAt := time.Date(2026, time.September, 28, 11, 0, 0, 0, time.Local)
	if _, err := sourceDB.Exec(`
		INSERT INTO games (id, name, status, source_type, source_id, created_at, updated_at, legacy_local_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"game-cache", "往返缓存游戏", "completed", "vndb", "v9000", createdAt, createdAt.Add(time.Hour), "42"); err != nil {
		t.Fatalf("插入游戏失败: %v", err)
	}
	// 桌面端自建游戏：没有 legacy_local_id，只能靠标题关联。
	if _, err := sourceDB.Exec(`
		INSERT INTO games (id, name, status, source_type, source_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"game-self", "自建条目", "unplayed", "vndb", "v1", createdAt, createdAt); err != nil {
		t.Fatalf("插入自建游戏失败: %v", err)
	}
	for _, entry := range []struct {
		gameID   string
		sourceID string
		payload  string
	}{
		{"game-cache", "v9000", payload},
		{"game-self", "v1", selfPayload},
	} {
		if _, err := sourceDB.Exec(`
			INSERT INTO game_metadata_sources (game_id, source_type, source_id, cache_json, cached_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			entry.gameID, "vndb", entry.sourceID, entry.payload, createdAt, createdAt, createdAt); err != nil {
			t.Fatalf("插入元数据缓存失败: %v", err)
		}
	}

	snapshotPath := filepath.Join(t.TempDir(), "metadata-cache.ykbak")
	if err := exporter.NewYukiHubExporter(context.Background(), sourceDB).Export(snapshotPath); err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	snapshot := readYukiHubSnapshot(t, snapshotPath)
	if len(snapshot.MetadataCache) != 2 {
		t.Fatalf("导出缓存条目数 = %d, want 2（自建条目应按标题关联，不再被跳过）", len(snapshot.MetadataCache))
	}
	bySourceID := make(map[string]yukihub.MetadataCache, len(snapshot.MetadataCache))
	for _, entry := range snapshot.MetadataCache {
		bySourceID[entry.SourceID] = entry
	}

	linked := bySourceID["v9000"]
	if linked.GameLocalID != 42 || linked.Source != "vndb" || linked.GameTitle != "往返缓存游戏" {
		t.Fatalf("本地 ID 条目 = %+v, want local_id 42 / vndb / 标题「往返缓存游戏」", linked)
	}
	if linked.JSON != payload {
		t.Fatalf("导出缓存负载 = %q, want %q", linked.JSON, payload)
	}
	// 自建条目：local_id 只能是 0，但必须带上标题，否则对端无从匹配。
	selfBuilt := bySourceID["v1"]
	if selfBuilt.GameLocalID != 0 || selfBuilt.GameTitle != "自建条目" {
		t.Fatalf("自建条目 = %+v, want local_id 0 + 标题「自建条目」", selfBuilt)
	}
	if selfBuilt.JSON != selfPayload {
		t.Fatalf("自建条目负载 = %q, want %q", selfBuilt.JSON, selfPayload)
	}

	imported, err := importer.NewYukiHubImporter(newTestImporterDependencies(targetDB)).
		Import(snapshotPath, false, importer.SamePathActionSkip)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if imported.Success != 2 || imported.Failed != 0 {
		t.Fatalf("导入结果 = success %d / failed %d (%v), want 2/0", imported.Success, imported.Failed, imported.FailedNames)
	}

	var persisted string
	if err := targetDB.QueryRow(`
		SELECT s.cache_json
		FROM game_metadata_sources s
		JOIN games g ON g.id = s.game_id
		WHERE g.legacy_local_id = '42' AND s.source_type = 'vndb'`).Scan(&persisted); err != nil {
		t.Fatalf("查询导入后的缓存失败: %v", err)
	}
	if persisted != payload {
		t.Fatalf("导入后的缓存负载 = %q, want %q", persisted, payload)
	}

	// 关键回归点：自建条目的缓存必须挂到同名游戏上（靠标题兜底）。
	// 旧实现下这里会「查询不到行」——缓存整段丢失。
	var selfPersisted string
	if err := targetDB.QueryRow(`
		SELECT s.cache_json
		FROM game_metadata_sources s
		JOIN games g ON g.id = s.game_id
		WHERE g.name = '自建条目' AND s.source_type = 'vndb'`).Scan(&selfPersisted); err != nil {
		t.Fatalf("自建条目的缓存没落到同名游戏上（标题兜底失效）: %v", err)
	}
	if selfPersisted != selfPayload {
		t.Fatalf("自建条目导入后的缓存 = %q, want %q", selfPersisted, selfPayload)
	}

	reExportedPath := filepath.Join(t.TempDir(), "metadata-cache-again.ykbak")
	if err := exporter.NewYukiHubExporter(context.Background(), targetDB).Export(reExportedPath); err != nil {
		t.Fatalf("再次导出失败: %v", err)
	}
	again := readYukiHubSnapshot(t, reExportedPath)
	if len(again.MetadataCache) != 2 {
		t.Fatalf("再次导出缓存条目数 = %d, want 2", len(again.MetadataCache))
	}
	againBySourceID := make(map[string]yukihub.MetadataCache, len(again.MetadataCache))
	for _, entry := range again.MetadataCache {
		againBySourceID[entry.SourceID] = entry
	}
	if againBySourceID["v9000"].JSON != payload || againBySourceID["v9000"].GameLocalID != 42 {
		t.Fatalf("往返后的本地 ID 缓存 = %+v, want 与首次导出一致", againBySourceID["v9000"])
	}
	if againBySourceID["v1"].JSON != selfPayload || againBySourceID["v1"].GameTitle != "自建条目" {
		t.Fatalf("往返后的自建缓存 = %+v, want 与首次导出一致", againBySourceID["v1"])
	}
}

// newTestImporterDependencies 构造最小可用的导入依赖：真实 Committer + 直写 play_sessions，
// 与 ImportService.importerDependencies 生产装配保持一致（仅裁掉封面下载与 Wails 相关部分）。
func newTestImporterDependencies(db *sql.DB) importer.Dependencies {
	ctx := context.Background()
	committer := importer.NewCommitter(importer.CommitDependencies{Ctx: ctx, DB: db})
	return importer.Dependencies{
		Ctx:       ctx,
		ListGames: committer.ListGames,
		AddItems:  committer.AddItems,
		AddSessions: func(sessions []models.PlaySession) error {
			for _, session := range sessions {
				if _, err := db.Exec(
					`INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
					session.ID, session.GameID, session.StartTime, session.EndTime, session.Duration, session.UpdatedAt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func readYukiHubSnapshot(t *testing.T, path string) yukihub.Backup {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开快照失败: %v", err)
	}
	defer file.Close()

	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("解压快照失败: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("读取快照失败: %v", err)
	}
	var backup yukihub.Backup
	if err := json.Unmarshal(data, &backup); err != nil {
		t.Fatalf("解析快照失败: %v", err)
	}
	return backup
}

// TestYukiHubThreeChannelsExportIdenticalPayload 钉住「三条通道导出的必须是同一个东西」。
//
// 账号云同步（yukihub.zh.kg）与 WebDAV 自持同步都走 exporter.Build()（云同步形态），
// 本地全量备份（.ykbak）走 exporter.BuildLocalBackup()（本地形态）。两者共用
// build(kind)，**负载必须逐字节相同**，只有顶层信封按手机版约定不同
// （created_at / note / backup_type，见 SyncManager.buildLocalSnapshot 与
// MainActivity.exportLocalBackup）。谁要在某一条通道上单独加字段，数据在手机端
// 与桌面端之间往返就会互相丢。
func TestYukiHubThreeChannelsExportIdenticalPayload(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	createdAt := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.Local)
	resetAt := createdAt.Add(2 * time.Hour)

	// 覆盖到每一条导出分支：带别名的清零游戏、带收藏+标签+元数据缓存的游戏、
	// 只有本地封面的游戏、以及会被跳过的无标题游戏。
	seedStatements := []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO games (id, name, aliases, cover_url, summary, path, game_directory, status,
				source_type, source_id, created_at, updated_at, legacy_local_id, playtime_reset_at, hidden, is_nsfw)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{"ch-1", "通道一致性 1", `["Alias One"]`, `D:/covers/local.jpg`, "简介",
				`D:/Games/one/game.exe`, `D:/Games/one`, "playing", "local", "",
				createdAt, createdAt.Add(time.Hour), "42", resetAt, true, true},
		},
		{
			`INSERT INTO games (id, name, cover_url, cover_source_url, path, status, source_type, source_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{"ch-2", "通道一致性 2", "https://example.com/b.jpg", "",
				`D:/Games/two/game.exe`, "completed", "local", "", createdAt, createdAt.Add(time.Hour)},
		},
		{
			`INSERT INTO games (id, name, cover_url, cover_source_url, path, status, source_type, source_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{"ch-3", "通道一致性 3（本地封面来源）", "https://example.com/c.jpg", `D:/covers/local-source.jpg`,
				`D:/Games/three/game.exe`, "unplayed", "local", "", createdAt, createdAt},
		},
		{
			`INSERT INTO games (id, name, path, status, source_type, source_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{"ch-empty", "   ", `D:/Games/empty/game.exe`, "unplayed", "local", "", createdAt, createdAt},
		},
		{
			`INSERT INTO game_tags (id, game_id, name, source, weight, is_spoiler, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			[]any{"ch-tag-1", "ch-2", "剧情", "vndb", 1.0, false, createdAt, createdAt},
		},
		{
			`INSERT INTO game_categories (game_id, category_id, updated_at) VALUES (?, ?, ?)`,
			[]any{"ch-2", gamehelper.SystemFavoritesCategoryID, createdAt},
		},
		{
			`INSERT INTO game_metadata_sources (game_id, source_type, source_id, cache_json, cached_at, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{"ch-2", "nextmoe", "12345", `{"chineseTitle":"通道一致性 2"}`, createdAt, createdAt, createdAt},
		},
	}
	for _, stmt := range seedStatements {
		if _, err := db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("准备测试数据失败: %v", err)
		}
	}

	// 清零之前 + 之后的会话各一条，两条通道都必须用同一套过滤规则。
	for _, session := range []struct {
		id        string
		gameID    string
		startTime time.Time
		duration  int
	}{
		{"ch-session-before", "ch-1", createdAt.Add(30 * time.Minute), 90},
		{"ch-session-after", "ch-1", resetAt.Add(time.Hour), 90},
		{"ch-session-two", "ch-2", createdAt.Add(3 * time.Hour), 60},
	} {
		if _, err := db.Exec(
			`INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			session.id, session.gameID, session.startTime,
			session.startTime.Add(time.Duration(session.duration)*time.Second),
			session.duration, session.startTime,
		); err != nil {
			t.Fatalf("准备游玩记录失败: %v", err)
		}
	}

	exporterInstance := exporter.NewYukiHubExporter(context.Background(), db)
	exporterInstance.SetProfile("Yuki", "https://yukihub.zh.kg/avatar.png")
	exporterInstance.SetMetadataSource("nextmoe")

	cloud, err := exporterInstance.Build()
	if err != nil {
		t.Fatalf("云同步快照导出失败: %v", err)
	}
	local, err := exporterInstance.BuildLocalBackup()
	if err != nil {
		t.Fatalf("本地备份快照导出失败: %v", err)
	}

	// 先确认负载确实非空，避免「两边都是空快照所以相等」的假阳性。
	if len(cloud.Games) != 3 {
		t.Fatalf("导出游戏数 = %d, want 3（无标题条目应被跳过）", len(cloud.Games))
	}
	if len(cloud.PlaySessions) != 2 {
		t.Fatalf("导出会话数 = %d, want 2（清零之前的会话被过滤）", len(cloud.PlaySessions))
	}
	if len(cloud.MetadataCache) != 1 {
		t.Fatalf("导出元数据缓存数 = %d, want 1", len(cloud.MetadataCache))
	}
	if cloud.Profile == nil || cloud.Settings.MetadataSource != "nextmoe" {
		t.Fatalf("profile/settings 缺失: %+v / %+v", cloud.Profile, cloud.Settings)
	}

	// 信封差异必须恰好是手机版约定的那三项。
	if cloud.CreatedAt != 0 || cloud.BackupType != "" {
		t.Errorf("云同步形态 created_at/backup_type = %d/%q, want 0/空", cloud.CreatedAt, cloud.BackupType)
	}
	if local.CreatedAt <= 0 || local.BackupType != "local_full" {
		t.Errorf("本地形态 created_at/backup_type = %d/%q, want 当前毫秒/local_full", local.CreatedAt, local.BackupType)
	}
	if local.Note == cloud.Note {
		t.Errorf("两种形态的 note 应不同，都等于 %q", local.Note)
	}

	// 把信封归一化后，两份快照必须逐字节相同。
	normalizedLocal := *local
	normalizedLocal.CreatedAt = cloud.CreatedAt
	normalizedLocal.Note = cloud.Note
	normalizedLocal.BackupType = cloud.BackupType

	cloudJSON, err := json.Marshal(cloud)
	if err != nil {
		t.Fatalf("序列化云同步快照失败: %v", err)
	}
	localJSON, err := json.Marshal(&normalizedLocal)
	if err != nil {
		t.Fatalf("序列化本地备份快照失败: %v", err)
	}
	if string(cloudJSON) != string(localJSON) {
		t.Errorf("三条通道的负载必须完全一致（差异只能出现在信封上）:\ncloud=%s\nlocal=%s", cloudJSON, localJSON)
	}

	// 云同步形态不能带上本地备份专属键。
	if strings.Contains(string(cloudJSON), "backup_type") {
		t.Errorf("云同步快照不应包含 backup_type：%s", cloudJSON)
	}
}
