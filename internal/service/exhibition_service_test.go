package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yukihub/internal/applog"
	"yukihub/internal/migrations"
	"yukihub/internal/service/gamehelper"

	_ "github.com/duckdb/duckdb-go/v2"
)

// newExhibitionTestService 建一个真库（跑完整迁移）的展厅数据服务。
func newExhibitionTestService(t *testing.T) (*ExhibitionService, *sql.DB) {
	t.Helper()

	applog.SetMode(applog.ModeCLI)

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open DuckDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := migrations.InitSchema(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	svc := NewExhibitionService()
	svc.Init(context.Background(), db)
	return svc, db
}

func seedExhibitionGame(t *testing.T, db *sql.DB, id, name, aliases, cover string, status string, nsfw, hidden bool) {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO games (id, name, aliases, cover_url, status, source_type, is_nsfw, hidden, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'local', ?, ?, ?, ?)
	`, id, name, aliases, cover, status, nsfw, hidden, now, now); err != nil {
		t.Fatalf("insert game %s: %v", id, err)
	}
}

func decodeExhibitionLibrary(t *testing.T, raw string) map[string]exhibitionGame {
	t.Helper()

	var payload exhibitionLibrary
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("解析 JSON 失败: %v\npayload=%s", err, raw)
	}
	if payload.Count != len(payload.Games) {
		t.Fatalf("count 必须等于 games 长度：count=%d len=%d", payload.Count, len(payload.Games))
	}
	byID := make(map[string]exhibitionGame, len(payload.Games))
	for _, game := range payload.Games {
		byID[game.ID] = game
	}
	return byID
}

// 展厅页面（hall.js / picker.js / viewer.js）按手机版的键名取值，
// 字段名、单位、封面地址任何一处不一致都会表现为"展厅里空空如也"。
func TestExhibitionLibraryMatchesMobileContract(t *testing.T) {
	svc, db := newExhibitionTestService(t)
	ctx := context.Background()

	// 1 号：有别名、有会话、收藏、NSFW、标签、本地封面
	seedExhibitionGame(t, db, "g-1", "千恋万花", `["Senren Banka"]`,
		"/local/covers/g-1.webp", "playing", true, false)
	// 2 号：隐藏游戏，不该进展厅
	seedExhibitionGame(t, db, "g-hidden", "藏起来的", `[]`, "", "completed", false, true)
	// 3 号：无封面、无会话、远端封面
	seedExhibitionGame(t, db, "g-3", "远端封面", `[]`,
		"https://t.vndb.org/cv/12/345.jpg", "onhold", false, false)

	now := time.Now().UTC().Truncate(time.Millisecond)
	start := now.Add(-2 * time.Hour)
	end := start.Add(90 * time.Minute)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at)
		VALUES ('s-1', 'g-1', ?, ?, 5400, ?)
	`, start, end, now); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO game_categories (game_id, category_id) VALUES ('g-1', ?)
	`, gamehelper.SystemFavoritesCategoryID); err != nil {
		t.Fatalf("insert favorite: %v", err)
	}

	for _, name := range []string{"剧情", "校园"} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO game_tags (id, game_id, name, source) VALUES (?, 'g-1', ?, 'vndb')
		`, "tag-"+name, name); err != nil {
			t.Fatalf("insert tag %s: %v", name, err)
		}
	}

	raw, err := svc.GetLibraryJSON()
	if err != nil {
		t.Fatalf("GetLibraryJSON: %v", err)
	}
	games := decodeExhibitionLibrary(t, raw)

	if len(games) != 2 {
		t.Fatalf("隐藏游戏不该进展厅：期望 2 条，实际 %d 条", len(games))
	}
	if _, ok := games["g-hidden"]; ok {
		t.Fatal("hidden 游戏出现在了展厅库存里")
	}

	first, ok := games["g-1"]
	if !ok {
		t.Fatal("缺少 g-1")
	}
	if first.Title != "千恋万花" {
		t.Errorf("title = %q", first.Title)
	}
	if first.OriginalTitle != "Senren Banka" {
		t.Errorf("originalTitle 应取第一个别名，实际 %q", first.OriginalTitle)
	}
	if first.PlayStatus != "playing" {
		t.Errorf("playStatus = %q", first.PlayStatus)
	}
	if first.TotalPlayTime != 5400*1000 {
		t.Errorf("totalPlayTime 必须是毫秒：期望 %d，实际 %d", 5400*1000, first.TotalPlayTime)
	}
	if first.LastPlayedAt != start.UnixMilli() {
		t.Errorf("lastPlayedAt 必须是毫秒时间戳：期望 %d，实际 %d", start.UnixMilli(), first.LastPlayedAt)
	}
	if !first.Favorite {
		t.Error("收藏标记丢失（展厅的 C 位与金色灯效都靠它）")
	}
	if !first.NSFW {
		t.Error("NSFW 标记丢失（封面打码靠它）")
	}
	if first.Tags != "剧情 / 校园" {
		t.Errorf("tags = %q，want %q", first.Tags, "剧情 / 校园")
	}
	if !first.HasCover || first.Cover != "/local/covers/g-1.webp" {
		t.Errorf("本地封面应原样透出：hasCover=%v cover=%q", first.HasCover, first.Cover)
	}

	third := games["g-3"]
	if !strings.HasPrefix(third.Cover, "/proxy/image?url=") {
		t.Errorf("远端封面必须走 /proxy/image（否则 iframe 里直接跨域拉 vndb）: %q", third.Cover)
	}
	if third.TotalPlayTime != 0 || third.LastPlayedAt != 0 {
		t.Errorf("没有会话的游戏时长/最近时间应为 0：%+v", third)
	}
	if third.PlayStatus != "onhold" {
		t.Errorf("playStatus = %q, want onhold", third.PlayStatus)
	}
}

// 清零时间点之前的历史会话不计入时长 —— 与游戏库/统计页同一口径，
// 否则展厅里的"馆藏总时长"会和统计页对不上。
func TestExhibitionPlaytimeRespectsResetPoint(t *testing.T) {
	svc, db := newExhibitionTestService(t)
	ctx := context.Background()

	seedExhibitionGame(t, db, "g-reset", "清零过的游戏", `[]`, "", "unplayed", false, false)

	now := time.Now().UTC().Truncate(time.Millisecond)
	resetAt := now.Add(-24 * time.Hour)
	if _, err := db.ExecContext(ctx, `UPDATE games SET playtime_reset_at = ? WHERE id = 'g-reset'`, resetAt); err != nil {
		t.Fatalf("set reset point: %v", err)
	}

	oldStart := resetAt.Add(-10 * time.Hour)
	newStart := resetAt.Add(1 * time.Hour)
	sessions := []struct {
		id     string
		start  time.Time
		end    time.Time
		second int
	}{
		{"s-old", oldStart, oldStart.Add(time.Hour), 3600}, // 清零点之前：不计
		{"s-new", newStart, newStart.Add(time.Hour), 1200}, // 清零点之后：计 1200 秒
	}
	for _, session := range sessions {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at)
			VALUES (?, 'g-reset', ?, ?, ?, ?)
		`, session.id, session.start, session.end, session.second, now); err != nil {
			t.Fatalf("insert session %s: %v", session.id, err)
		}
	}

	raw, err := svc.GetLibraryJSON()
	if err != nil {
		t.Fatalf("GetLibraryJSON: %v", err)
	}
	game := decodeExhibitionLibrary(t, raw)["g-reset"]
	if game.TotalPlayTime != 1200*1000 {
		t.Errorf("清零点之前的会话不该计入：期望 %d 毫秒，实际 %d", 1200*1000, game.TotalPlayTime)
	}
	// lastPlayedAt 沿用列表页口径（MAX(start_time)，不过滤清零点），这里只锁时长。
}

func TestExhibitionStatusNormalization(t *testing.T) {
	cases := map[string]string{
		"playing":   "playing",
		"PLAYING":   "playing",
		"completed": "completed",
		" onhold ":  "onhold",
		"dropped":   "dropped",
		"unplayed":  "unplayed",
		"":          "unplayed",
		"weird":     "unplayed",
	}
	for input, want := range cases {
		if got := normalizeExhibitionStatus(input); got != want {
			t.Errorf("normalizeExhibitionStatus(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExhibitionCoverURLNormalization(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "   ", ""},
		{"local", "/local/covers/a.webp", "/local/covers/a.webp"},
		{"https", "https://t.vndb.org/cv/12/345.jpg", "/proxy/image?url=https%3A%2F%2Ft.vndb.org%2Fcv%2F12%2F345.jpg"},
		{"query kept", "https://a.com/x.jpg?size=big", "/proxy/image?url=https%3A%2F%2Fa.com%2Fx.jpg%3Fsize%3Dbig"},
		{"wails host stripped", "http://wails.localhost/local/covers/b.png", "/local/covers/b.png"},
		{"bare path rejected", `D:\covers\a.png`, ""},
		{"relative rejected", "covers/a.png", ""},
	}
	for _, testCase := range cases {
		if got := normalizeExhibitionCoverURL(testCase.in); got != testCase.want {
			t.Errorf("%s: normalizeExhibitionCoverURL(%q) = %q, want %q",
				testCase.name, testCase.in, got, testCase.want)
		}
	}

	// 空格必须编成 +（与前端 URLSearchParams 一致），Go 侧 Query 能解回来
	got := normalizeExhibitionCoverURL("https://a.com/a b.jpg")
	if got != "/proxy/image?url=https%3A%2F%2Fa.com%2Fa+b.jpg" {
		t.Errorf("空格编码不一致：%q", got)
	}
}

// 空库不该报错，页面要能显示"库里还没有游戏"而不是一堆错误。
func TestExhibitionEmptyLibrary(t *testing.T) {
	svc, _ := newExhibitionTestService(t)

	raw, err := svc.GetLibraryJSON()
	if err != nil {
		t.Fatalf("GetLibraryJSON: %v", err)
	}
	if raw != `{"games":[],"count":0}` {
		t.Errorf("空库应输出空数组（不是 null），实际 %s", raw)
	}
}
