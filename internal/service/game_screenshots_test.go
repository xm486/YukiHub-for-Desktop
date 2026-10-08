package service

import (
	"context"
	"reflect"
	"testing"

	"yukihub/internal/appconf"
	"yukihub/internal/common/enums"
)

// 大屏详情层的 INTRODUCTION 画带读的是 game_metadata_sources.cache_json 里的
// screenshotUrls（手机版 VnMetadata 结构）。合并规则逐字对齐手机端
// BigScreenMeta.load：NextMoe → VNDB → Bangumi → Ymgal → Hikarinagi，
// 取**第一个非空来源**的整组，不跨来源拼接。
func TestGetGameScreenshotsUsesMobileSourcePriority(t *testing.T) {
	db := setupImportServiceTestDB(t)
	service := NewGameService()
	service.Init(context.Background(), db, &appconf.AppConfig{})

	gameID := "screenshots-priority-game"
	insertCache := func(source enums.SourceType, payload string) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO game_metadata_sources (game_id, source_type, source_id, cache_json)
			VALUES (?, ?, ?, ?)`,
			gameID, string(source), "id-"+string(source), payload); err != nil {
			t.Fatalf("插入 %s 缓存失败: %v", source, err)
		}
	}

	// VNDB 有图，但 NextMoe 优先级更高 → 应该拿到 NextMoe 的那组
	insertCache(enums.VNDB, `{"id":"v1","screenshotUrls":["https://vndb/1.jpg","https://vndb/2.jpg"]}`)
	insertCache(enums.NextMoe, `{"id":"nm1","screenshotUrls":["https://nm/1.jpg"]}`)

	got, err := service.GetGameScreenshots(gameID)
	if err != nil {
		t.Fatalf("GetGameScreenshots: %v", err)
	}
	want := []string{"https://nm/1.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("screenshots = %v, want %v（NextMoe 优先级高于 VNDB）", got, want)
	}
}

// 高优先级来源「有缓存但没有截图」时要继续往下找，而不是就此返回空。
func TestGetGameScreenshotsFallsThroughEmptySources(t *testing.T) {
	db := setupImportServiceTestDB(t)
	service := NewGameService()
	service.Init(context.Background(), db, &appconf.AppConfig{})

	gameID := "screenshots-fallthrough-game"
	for source, payload := range map[enums.SourceType]string{
		enums.NextMoe:    `{"id":"nm1"}`,
		enums.VNDB:       `{"id":"v1","screenshotUrls":[]}`,
		enums.Hikarinagi: `{"id":"h1","screenshotUrls":["https://hk/1.jpg","https://hk/2.jpg"]}`,
	} {
		if _, err := db.Exec(`
			INSERT INTO game_metadata_sources (game_id, source_type, source_id, cache_json)
			VALUES (?, ?, ?, ?)`,
			gameID, string(source), "id-"+string(source), payload); err != nil {
			t.Fatalf("插入 %s 缓存失败: %v", source, err)
		}
	}

	got, err := service.GetGameScreenshots(gameID)
	if err != nil {
		t.Fatalf("GetGameScreenshots: %v", err)
	}
	want := []string{"https://hk/1.jpg", "https://hk/2.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("screenshots = %v, want %v", got, want)
	}
}

// 没有任何来源带截图（或游戏根本没有来源行）时返回空切片，前端据此整块隐藏画带。
func TestGetGameScreenshotsReturnsEmptyWhenAbsent(t *testing.T) {
	db := setupImportServiceTestDB(t)
	service := NewGameService()
	service.Init(context.Background(), db, &appconf.AppConfig{})

	for _, gameID := range []string{"screenshots-none-game", ""} {
		got, err := service.GetGameScreenshots(gameID)
		if err != nil {
			t.Fatalf("GetGameScreenshots(%q): %v", gameID, err)
		}
		if len(got) != 0 {
			t.Fatalf("GetGameScreenshots(%q) = %v, want empty", gameID, got)
		}
	}
}

// 单个来源的缓存坏了（非法 JSON）不该让整条画带失败：跳过它继续看后面的来源。
func TestGetGameScreenshotsSkipsBrokenPayload(t *testing.T) {
	db := setupImportServiceTestDB(t)
	service := NewGameService()
	service.Init(context.Background(), db, &appconf.AppConfig{})

	gameID := "screenshots-broken-game"
	for source, payload := range map[enums.SourceType]string{
		enums.NextMoe: `{ this is not json`,
		enums.VNDB:    `{"id":"v1","screenshotUrls":["https://vndb/1.jpg"]}`,
	} {
		if _, err := db.Exec(`
			INSERT INTO game_metadata_sources (game_id, source_type, source_id, cache_json)
			VALUES (?, ?, ?, ?)`,
			gameID, string(source), "id-"+string(source), payload); err != nil {
			t.Fatalf("插入 %s 缓存失败: %v", source, err)
		}
	}

	got, err := service.GetGameScreenshots(gameID)
	if err != nil {
		t.Fatalf("GetGameScreenshots: %v", err)
	}
	want := []string{"https://vndb/1.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("screenshots = %v, want %v（坏缓存应被跳过）", got, want)
	}
}
