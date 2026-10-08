package service

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"yukihub/internal/appconf"
	"yukihub/internal/common/enums"
	"yukihub/internal/common/vo"
	"yukihub/internal/models"
	"yukihub/internal/models/yukihub"
	"yukihub/internal/service/gamehelper"
	"yukihub/internal/utils/metadata"
)

func TestApplyRemoteMetadataMergesAliases(t *testing.T) {
	db := setupImportServiceTestDB(t)
	service := NewGameService()
	service.Init(context.Background(), db, &appconf.AppConfig{})

	existing := models.Game{
		ID:      "alias-refresh-game",
		Name:    "本地名称",
		Aliases: []string{"手动简称", "SubaHibi"},
	}
	if err := service.AddGameFromWebMetadata(vo.GameMetadataFromWebVO{Game: existing}); err != nil {
		t.Fatalf("add game: %v", err)
	}
	existing, err := service.GetGameByID(existing.ID)
	if err != nil {
		t.Fatalf("get game: %v", err)
	}

	fields := gamehelper.NormalizeMetadataUpdateFields([]enums.MetadataUpdateField{
		enums.MetadataUpdateFieldAliases,
	})
	_, err = service.applyRemoteMetadataResult(existing, metadata.MetadataResult{
		Game: models.Game{
			Name:    "远端名称",
			Aliases: []string{"subahibi", "素晴らしき日々～不連続存在～"},
		},
	}, false, fields)
	if err != nil {
		t.Fatalf("apply remote metadata: %v", err)
	}

	saved, err := service.GetGameByID(existing.ID)
	if err != nil {
		t.Fatalf("get updated game: %v", err)
	}
	wantAliases := []string{"手动简称", "SubaHibi", "素晴らしき日々～不連続存在～"}
	if !reflect.DeepEqual(saved.Aliases, wantAliases) {
		t.Fatalf("aliases: got %#v want %#v", saved.Aliases, wantAliases)
	}
	if saved.Name != existing.Name {
		t.Fatalf("unselected name changed: got %q want %q", saved.Name, existing.Name)
	}
}

// TestApplyRemoteMetadataCachesSourcePayload 验证刮削结果会按来源写入元数据缓存。
//
// 缓存负载使用 Android 版 VnMetadata 结构，桌面端没有对应列的字段不做猜测性填充；
// 两个方向（导出到 Android / 从 Android 导入）共用同一份结构。
func TestApplyRemoteMetadataCachesSourcePayload(t *testing.T) {
	db := setupImportServiceTestDB(t)
	service := NewGameService()
	service.Init(context.Background(), db, &appconf.AppConfig{})

	existing := models.Game{
		ID:         "cache-payload-game",
		Name:       "本地名称",
		SourceType: enums.VNDB,
		SourceID:   "v123",
	}
	if err := service.AddGameFromWebMetadata(vo.GameMetadataFromWebVO{
		Game: existing,
		Tags: []metadata.TagItem{{Name: "校园", Source: "vndb"}},
	}); err != nil {
		t.Fatalf("add game: %v", err)
	}
	existing, err := service.GetGameByID(existing.ID)
	if err != nil {
		t.Fatalf("get game: %v", err)
	}

	fields := gamehelper.NormalizeMetadataUpdateFields([]enums.MetadataUpdateField{
		enums.MetadataUpdateFieldName,
		enums.MetadataUpdateFieldCompany,
		enums.MetadataUpdateFieldRating,
	})
	if _, err := service.applyRemoteMetadataResult(existing, metadata.MetadataResult{
		Game: models.Game{
			Name:     "远端名称",
			Aliases:  []string{"遠端名稱"},
			Company:  "Test Studio",
			Rating:   8.4,
			Summary:  "远端简介",
			CoverURL: "https://example.com/cover.jpg",
		},
		Tags: []metadata.TagItem{
			{Name: "剧情", Source: "vndb"},
			{Name: "悬疑", Source: "vndb"},
		},
		Screenshots: []string{
			"https://example.com/s1.jpg",
			"https://example.com/s1.jpg", // 重复项应被去掉
			"https://example.com/s2.jpg",
		},
	}, false, fields); err != nil {
		t.Fatalf("apply remote metadata: %v", err)
	}

	var payload string
	if err := db.QueryRow(`
		SELECT COALESCE(cache_json, '')
		FROM game_metadata_sources
		WHERE game_id = ? AND source_type = ?`,
		existing.ID, string(enums.VNDB)).Scan(&payload); err != nil {
		t.Fatalf("query cached payload: %v", err)
	}
	if payload == "" {
		t.Fatal("刮削结果没有写入元数据缓存")
	}
	var cached yukihub.Metadata
	if err := json.Unmarshal([]byte(payload), &cached); err != nil {
		t.Fatalf("解析缓存负载失败: %v", err)
	}
	if cached.ID != "v123" {
		t.Errorf("cached id = %q, want v123", cached.ID)
	}
	if cached.ChineseTitle != "远端名称" || cached.OriginalTitle != "遠端名稱" {
		t.Errorf("cached title = %q / %q, want 远端名称 / 遠端名稱", cached.ChineseTitle, cached.OriginalTitle)
	}
	if cached.Developer != "Test Studio" || cached.Description != "远端简介" {
		t.Errorf("cached developer/description = %q/%q", cached.Developer, cached.Description)
	}
	if cached.CoverURL != "https://example.com/cover.jpg" {
		t.Errorf("cached cover = %q", cached.CoverURL)
	}
	if cached.RatingText != "8.4" {
		t.Errorf("cached rating = %q, want \"8.4\"", cached.RatingText)
	}
	if cached.TagsText != "剧情,悬疑" {
		t.Errorf("cached tags = %q, want \"剧情,悬疑\"", cached.TagsText)
	}
	// 截图要跟着进缓存：手机端 BigScreenMeta 就是从这份负载里取画带的
	wantScreenshots := []string{"https://example.com/s1.jpg", "https://example.com/s2.jpg"}
	if !reflect.DeepEqual(cached.ScreenshotURLs, wantScreenshots) {
		t.Errorf("cached screenshots = %v, want %v", cached.ScreenshotURLs, wantScreenshots)
	}
}

func TestUpdateDownloadedCoverURLSkipsSupersededSource(t *testing.T) {
	db := setupImportServiceTestDB(t)
	service := NewGameService()
	service.Init(context.Background(), db, &appconf.AppConfig{})

	if _, err := db.Exec(`
		INSERT INTO games (
			id, name, cover_url, cover_source_url, status, source_type,
			cached_at, created_at, updated_at
		) VALUES ('cover-race', 'Cover Race', '/local/covers/current.webp',
			'https://example.com/current.webp', 'unplayed', 'local',
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("insert game: %v", err)
	}

	updated, err := service.updateDownloadedCoverURL(
		context.Background(),
		"cover-race",
		"/local/covers/stale.webp",
		"https://example.com/stale.webp",
	)
	if err != nil {
		t.Fatalf("update stale cover: %v", err)
	}
	if updated {
		t.Fatal("stale cover update unexpectedly changed the game")
	}

	updated, err = service.updateDownloadedCoverURL(
		context.Background(),
		"cover-race",
		"/local/covers/current-new.webp",
		"https://example.com/current.webp",
	)
	if err != nil {
		t.Fatalf("update current cover: %v", err)
	}
	if !updated {
		t.Fatal("current cover update was skipped")
	}

	var coverURL string
	if err := db.QueryRow(`SELECT cover_url FROM games WHERE id = 'cover-race'`).Scan(&coverURL); err != nil {
		t.Fatalf("query cover URL: %v", err)
	}
	if coverURL != "/local/covers/current-new.webp" {
		t.Fatalf("cover URL = %q", coverURL)
	}

	if err := service.updateCoverURL("cover-race", "/local/covers/manual.webp"); err != nil {
		t.Fatalf("update manual cover: %v", err)
	}
	var coverSourceURL string
	if err := db.QueryRow(`
		SELECT cover_url, COALESCE(cover_source_url, '')
		FROM games WHERE id = 'cover-race'
	`).Scan(&coverURL, &coverSourceURL); err != nil {
		t.Fatalf("query manual cover: %v", err)
	}
	if coverURL != "/local/covers/manual.webp" || coverSourceURL != "" {
		t.Fatalf("manual cover = %q source = %q", coverURL, coverSourceURL)
	}
}
