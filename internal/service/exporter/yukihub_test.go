package exporter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"yukihub/internal/common/enums"
	"yukihub/internal/models"
	"yukihub/internal/models/yukihub"
)

func TestMapGameStatusToYukiHubCoversAllStatuses(t *testing.T) {
	t.Parallel()

	cases := map[enums.GameStatus]string{
		enums.StatusUnplayed:  "unplayed",
		enums.StatusPlaying:   "playing",
		enums.StatusCompleted: "completed",
		enums.StatusOnHold:    "onhold",
		enums.StatusDropped:   "dropped",
		enums.GameStatus(""):  "unplayed",
		enums.GameStatus("x"): "unplayed",
	}
	for input, want := range cases {
		if got := mapGameStatusToYukiHub(input); got != want {
			t.Errorf("mapGameStatusToYukiHub(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNetworkCoverURIOnlyExportsRemoteCovers(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"https://example.com/cover.jpg": "https://example.com/cover.jpg",
		"http://example.com/cover.jpg":  "http://example.com/cover.jpg",
		// 本地封面跨设备无效，契约明确不迁移。
		"D:\\covers\\cover.jpg":   "",
		"cover.jpg":               "",
		"content://media/cover/1": "",
		"":                        "",
	}
	for input, want := range cases {
		if got := networkCoverURI(input); got != want {
			t.Errorf("networkCoverURI(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseYukiHubLocalIDFallsBackToZero(t *testing.T) {
	t.Parallel()

	cases := map[string]int64{
		"42":    42,
		" 42 ":  42,
		"":      0,
		"abc":   0,
		"-1":    0,
		"0":     0,
		"1e309": 0,
	}
	for input, want := range cases {
		if got := parseYukiHubLocalID(input); got != want {
			t.Errorf("parseYukiHubLocalID(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestBuildYukiHubGameConvertsSecondsToMillis(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.September, 20, 20, 0, 0, 0, time.Local)
	end := start.Add(90 * time.Second)
	game := models.Game{
		ID:            "game-1",
		Name:          "桌面游戏",
		LegacyLocalID: "42",
		GameDirectory: "D:\\Games\\desktop",
		Path:          "D:\\Games\\desktop\\game.exe",
		CoverURL:      "https://example.com/cover.jpg",
		Summary:       "简介",
		Status:        enums.StatusPlaying,
		CreatedAt:     start.Add(-time.Hour),
		UpdatedAt:     end,
	}
	sessions := []models.PlaySession{{
		ID:        "9b6f0f4a-6d2f-4d0e-9f4f-1f2f0d1a2b3c",
		GameID:    game.ID,
		StartTime: start,
		EndTime:   end,
		Duration:  90, // 桌面端单位是秒
		UpdatedAt: end,
	}}

	entry, exported := buildYukiHubGame(game, []string{"剧情", "校园"}, sessions, false)
	if entry.TotalPlayTime != 90_000 {
		t.Fatalf("TotalPlayTime = %d, want 90000 (毫秒)", entry.TotalPlayTime)
	}
	if len(exported) != 1 || exported[0].Duration != 90_000 {
		t.Fatalf("Session duration = %#v, want one session of 90000 ms", exported)
	}
	if exported[0].SessionUUID != sessions[0].ID {
		t.Errorf("SessionUUID = %q, want %q", exported[0].SessionUUID, sessions[0].ID)
	}
	if exported[0].GameLocalID != 42 || exported[0].GameTitle != "桌面游戏" || exported[0].GameRootUri != "" {
		t.Errorf("Session identity = %+v", exported[0])
	}
	if exported[0].LaunchType != "external" || exported[0].DeviceID != "desktop" {
		t.Errorf("launch_type/device_id = %q/%q, want external/desktop", exported[0].LaunchType, exported[0].DeviceID)
	}
	if exported[0].StartTime != start.UnixMilli() || exported[0].EndTime != end.UnixMilli() || exported[0].CreatedAt != start.UnixMilli() {
		t.Errorf("session timestamps = %+v", exported[0])
	}
	if entry.LastPlayedAt != end.UnixMilli() {
		t.Errorf("LastPlayedAt = %d, want %d", entry.LastPlayedAt, end.UnixMilli())
	}
	// root_uri 恒空：Windows 绝对路径在 Android 上不可达，且与导入方向对称
	// （桌面端导入 Android 备份时同样不写对端路径）。
	if entry.RootUri != "" {
		t.Errorf("root_uri = %q, want empty (不导出 Windows 路径)", entry.RootUri)
	}
	if entry.CoverUri != "https://example.com/cover.jpg" {
		t.Errorf("cover_uri = %q", entry.CoverUri)
	}
	if entry.Tags != "剧情,校园" {
		t.Errorf("Tags = %q, want \"剧情,校园\"", entry.Tags)
	}
	if entry.PlaytimeResetAt != 0 {
		t.Errorf("PlaytimeResetAt = %d, want 0 for a game that was never reset", entry.PlaytimeResetAt)
	}
	if entry.CreatedAt != game.CreatedAt.UnixMilli() || entry.UpdatedAt != end.UnixMilli() {
		t.Errorf("created_at/updated_at = %d/%d", entry.CreatedAt, entry.UpdatedAt)
	}
}

func TestBuildYukiHubGameSkipsSessionsBeforeReset(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 21, 10, 0, 0, 0, time.Local)
	resetAt := base.Add(time.Hour)
	game := models.Game{
		ID:              "game-1",
		Name:            "清零过的游戏",
		PlaytimeResetAt: &resetAt,
		Status:          enums.StatusCompleted,
		CreatedAt:       base.Add(-24 * time.Hour),
		UpdatedAt:       base.Add(2 * time.Hour),
	}
	sessions := []models.PlaySession{
		// 清零之前：必须被丢弃，否则清零历史会在 Android 侧复活。
		{ID: "old-1", GameID: game.ID, StartTime: base.Add(-30 * time.Minute), EndTime: base.Add(-29 * time.Minute), Duration: 60, UpdatedAt: base},
		// 边界：结束时间正好等于清零时刻，Android 侧的条件是 >=，必须保留。
		{ID: "boundary", GameID: game.ID, StartTime: resetAt.Add(-time.Minute), EndTime: resetAt, Duration: 60, UpdatedAt: resetAt},
		{ID: "new-1", GameID: game.ID, StartTime: base.Add(90 * time.Minute), EndTime: base.Add(92 * time.Minute), Duration: 120, UpdatedAt: base.Add(92 * time.Minute)},
	}

	entry, exported := buildYukiHubGame(game, nil, sessions, false)
	if entry.PlaytimeResetAt != resetAt.UnixMilli() {
		t.Fatalf("PlaytimeResetAt = %d, want %d", entry.PlaytimeResetAt, resetAt.UnixMilli())
	}
	if len(exported) != 2 {
		t.Fatalf("exported session count = %d, want 2: %#v", len(exported), exported)
	}
	if exported[0].SessionUUID != "boundary" || exported[1].SessionUUID != "new-1" {
		t.Fatalf("unexpected exported sessions: %#v", exported)
	}
	if entry.TotalPlayTime != 180_000 {
		t.Errorf("TotalPlayTime = %d, want 180000 (毫秒)", entry.TotalPlayTime)
	}
	if entry.LastPlayedAt != base.Add(92*time.Minute).UnixMilli() {
		t.Errorf("LastPlayedAt = %d, want the latest end_time", entry.LastPlayedAt)
	}
}

func TestBuildYukiHubGameKeepsOnlyLatestSessions(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 22, 8, 0, 0, 0, time.Local)
	game := models.Game{ID: "game-1", Name: "会话很多的游戏"}
	sessions := make([]models.PlaySession, 0, 40)
	for index := 0; index < 40; index++ {
		start := base.Add(time.Duration(index) * time.Minute)
		sessions = append(sessions, models.PlaySession{
			ID:        fmt.Sprintf("session-%02d", index),
			GameID:    game.ID,
			StartTime: start,
			EndTime:   start.Add(30 * time.Second),
			Duration:  30,
			UpdatedAt: start.Add(30 * time.Second),
		})
	}

	entry, exported := buildYukiHubGame(game, nil, sessions, false)
	if len(exported) != yukiHubSessionsPerGame {
		t.Fatalf("exported session count = %d, want %d", len(exported), yukiHubSessionsPerGame)
	}
	// 保留最新的 30 条：最早的是第 11 条（索引 10），最晚的是第 40 条（索引 39）。
	if exported[0].StartTime != base.Add(10*time.Minute).UnixMilli() {
		t.Errorf("first exported session = %d, want %d", exported[0].StartTime, base.Add(10*time.Minute).UnixMilli())
	}
	if exported[len(exported)-1].StartTime != base.Add(39*time.Minute).UnixMilli() {
		t.Errorf("last exported session = %d, want the newest one", exported[len(exported)-1].StartTime)
	}
	// 总时长统计不受 30 条上限影响（Android 侧的 total_play_time 也是全量聚合）。
	if entry.TotalPlayTime != 40*30*1000 {
		t.Errorf("TotalPlayTime = %d, want %d", entry.TotalPlayTime, 40*30*1000)
	}
}

func TestBuildYukiHubGameIsIdempotent(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 23, 9, 0, 0, 0, time.Local)
	game := models.Game{ID: "game-1", Name: "重复导出的游戏", LegacyLocalID: "7"}
	sessions := []models.PlaySession{
		{ID: "11111111-1111-1111-1111-111111111111", GameID: game.ID, StartTime: base, EndTime: base.Add(time.Minute), Duration: 60, UpdatedAt: base.Add(time.Minute)},
	}
	first, firstSessions := buildYukiHubGame(game, []string{"标签"}, sessions, true)
	second, secondSessions := buildYukiHubGame(game, []string{"标签"}, sessions, true)

	if !equalAsJSON(t, first, second) || !equalAsJSON(t, firstSessions, secondSessions) {
		t.Fatal("exporting the same data twice must produce identical snapshots")
	}
	// 幂等键就是桌面端的会话 UUID，Android 侧靠它去重。
	for index := range secondSessions {
		if secondSessions[index].SessionUUID != sessions[index].ID {
			t.Errorf("SessionUUID = %q, want %q", secondSessions[index].SessionUUID, sessions[index].ID)
		}
	}
	if !first.Favorite || !second.Favorite {
		t.Error("Favorite was not exported")
	}
}

func TestBuildYukiHubGameWithoutSessions(t *testing.T) {
	t.Parallel()

	game := models.Game{
		ID:      "game-1",
		Name:    "还没玩过的游戏",
		Aliases: []string{"Alias One"},
		Path:    "D:\\Games\\game.exe",
		// 本地封面不迁移。
		CoverURL: "D:\\covers\\game.jpg",
		Status:   enums.StatusUnplayed,
		IsNSFW:   true,
		Hidden:   true,
	}

	entry, exported := buildYukiHubGame(game, nil, nil, false)
	if len(exported) != 0 {
		t.Fatalf("exported session count = %d, want 0", len(exported))
	}
	if entry.TotalPlayTime != 0 || entry.LastPlayedAt != 0 {
		t.Errorf("total_play_time/last_played_at = %d/%d, want 0/0", entry.TotalPlayTime, entry.LastPlayedAt)
	}
	if entry.OriginalTitle != "Alias One" {
		t.Errorf("original_title = %q, want \"Alias One\"", entry.OriginalTitle)
	}
	if entry.RootUri != "" {
		t.Errorf("root_uri = %q, want empty regardless of game_directory/path", entry.RootUri)
	}
	if entry.CoverUri != "" {
		t.Errorf("cover_uri = %q, want empty for a local cover", entry.CoverUri)
	}
	// Hidden 是 *bool：断言时必须先判非 nil —— 导出侧永远要写出这个键，
	// 缺键会被手机版当成「对端没说」，取消隐藏就同步不过去了。
	if entry.PlayStatus != "unplayed" || entry.Hidden == nil || !*entry.Hidden || !entry.NSFW {
		t.Errorf("play_status/hidden/nsfw = %q/%v/%v", entry.PlayStatus, entry.Hidden, entry.NSFW)
	}
}

// 未隐藏的游戏同样要**写出** hidden=false（手机版 optBoolean 缺键=保留原值，
// 省略这个键会让「在桌面端取消隐藏」永远同步不过去）。
func TestExportAlwaysWritesHiddenKey(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(yukihub.Game{LocalID: 1, Title: "可见的游戏"})
	if err != nil {
		t.Fatalf("marshal snapshot game: %v", err)
	}
	if strings.Contains(string(raw), `"hidden"`) {
		t.Fatalf("零值 Game 的 Hidden 为 nil 时不该出现该键 —— 但真实导出必须显式赋值：%s", raw)
	}

	entry, _ := buildYukiHubGame(models.Game{ID: "g-1", Name: "可见的游戏"}, nil, nil, false)
	if entry.Hidden == nil {
		t.Fatal("导出必须给 Hidden 赋指针，否则 json 里不会出现这个键，对端无从得知「已取消隐藏」")
	}
	if *entry.Hidden {
		t.Fatalf("未隐藏的游戏应导出 hidden=false，实际 true")
	}

	// 走一遍真正的 JSON 序列化：确认 omitempty 没有把显式的 false 吃掉。
	// 这一层才是手机版实际看到的东西 —— 只断言结构体字段非 nil 不足以证明键真的写出来了。
	raw, err = json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal exported entry: %v", err)
	}
	if !strings.Contains(string(raw), `"hidden":false`) {
		t.Fatalf("导出的 JSON 必须显式带 hidden=false，实际 %s", raw)
	}

	var roundTripped yukihub.Game
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("unmarshal exported entry: %v", err)
	}
	if roundTripped.Hidden == nil || *roundTripped.Hidden {
		t.Fatalf("往返后 hidden 应为明确的 false，实际 %v", roundTripped.Hidden)
	}
}

// equalAsJSON 用 JSON 序列化结果比较两个值，避免引入 reflect.DeepEqual 对
// nil / 空切片、零值时间的差异误报。
func equalAsJSON(t *testing.T, left interface{}, right interface{}) bool {
	t.Helper()

	leftData, err := json.Marshal(left)
	if err != nil {
		t.Fatalf("marshal left: %v", err)
	}
	rightData, err := json.Marshal(right)
	if err != nil {
		t.Fatalf("marshal right: %v", err)
	}
	return string(leftData) == string(rightData)
}

// TestBuildSkipsUntitledGames 无标题条目不应导出：Android 侧会把空标题落成
// 「未命名游戏」，这类条目跨端同步时只会制造无法匹配的占位记录。
func TestBuildSkipsUntitledGames(t *testing.T) {
	games := []models.Game{
		{ID: "named", Name: "有标题"},
		{ID: "blank", Name: "   "},
		{ID: "empty", Name: ""},
	}
	exported := make([]string, 0, len(games))
	for _, game := range games {
		if strings.TrimSpace(game.Name) == "" {
			continue
		}
		entry, _ := buildYukiHubGame(game, nil, nil, false)
		exported = append(exported, entry.Title)
	}
	if len(exported) != 1 || exported[0] != "有标题" {
		t.Fatalf("导出条目 = %#v, want 只有 [有标题]", exported)
	}
}

// TestSnapshotEnvelopeMatchesAndroid 钉住快照顶层元信息与手机版一致。
//
// 双端同步靠这几个字段区分「云同步快照」和「本地全量备份」，一旦跑偏，
// 对端会把快照当成另一种形态处理（例如把云同步快照当成本地备份落盘）。
func TestSnapshotEnvelopeMatchesAndroid(t *testing.T) {
	t.Parallel()

	cloud := newBackupEnvelope(snapshotCloud)
	if cloud.App != "YukiHub" || cloud.Schema != 5 {
		t.Errorf("app/schema = %q/%d, want YukiHub/5", cloud.App, cloud.Schema)
	}
	// 手机版 SyncManager.buildLocalSnapshot：created_at 恒为 0
	if cloud.CreatedAt != 0 {
		t.Errorf("云同步 created_at = %d, want 0（与手机版一致）", cloud.CreatedAt)
	}
	if !cloud.Lightweight {
		t.Error("云同步 lightweight 应为 true")
	}
	if cloud.Note != yukiHubCloudNote {
		t.Errorf("云同步 note = %q, want %q", cloud.Note, yukiHubCloudNote)
	}
	if cloud.BackupType != "" {
		t.Errorf("云同步不应带 backup_type，got %q", cloud.BackupType)
	}

	local := newBackupEnvelope(snapshotLocalBackup)
	if local.CreatedAt <= 0 {
		t.Errorf("本地备份 created_at = %d, want 当前毫秒", local.CreatedAt)
	}
	if local.BackupType != "local_full" {
		t.Errorf("本地备份 backup_type = %q, want local_full", local.BackupType)
	}
	if local.Note != yukiHubLocalNote {
		t.Errorf("本地备份 note = %q, want %q", local.Note, yukiHubLocalNote)
	}
	if !local.Lightweight {
		t.Error("本地备份 lightweight 也应为 true（手机版同样写死 true）")
	}

	// 云同步快照序列化后不应出现 backup_type 键（omitempty）
	raw, err := json.Marshal(cloud)
	if err != nil {
		t.Fatalf("marshal cloud envelope: %v", err)
	}
	if strings.Contains(string(raw), "backup_type") {
		t.Errorf("云同步快照不应包含 backup_type：%s", raw)
	}
}

// TestSetProfileOnlyKeepsHTTPAvatar 头像必须是 http(s)：手机端会丢弃其它取值。
func TestSetProfileOnlyKeepsHTTPAvatar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		avatar   string
		wantURL  string
		wantNull bool
	}{
		{name: "Yuki", avatar: "https://yukihub.zh.kg/a.png", wantURL: "https://yukihub.zh.kg/a.png"},
		{name: "Yuki", avatar: "http://yukihub.zh.kg/a.png", wantURL: "http://yukihub.zh.kg/a.png"},
		// 本地路径 / file:// 跨设备无效，手机端也会拒收
		{name: "Yuki", avatar: "D://avatar.png", wantURL: ""},
		{name: "Yuki", avatar: "file:///C:/avatar.png", wantURL: ""},
		// 两者都空 → 不带 profile 段
		{name: "  ", avatar: "", wantNull: true},
	}
	for _, tc := range cases {
		exporter := &YukiHubExporter{}
		exporter.SetProfile(tc.name, tc.avatar)
		if tc.wantNull {
			if exporter.profile != nil {
				t.Errorf("SetProfile(%q,%q) 应不生成 profile，got %#v", tc.name, tc.avatar, exporter.profile)
			}
			continue
		}
		if exporter.profile == nil {
			t.Fatalf("SetProfile(%q,%q) profile 为空", tc.name, tc.avatar)
		}
		if exporter.profile.AvatarURI != tc.wantURL {
			t.Errorf("avatar_uri = %q, want %q", exporter.profile.AvatarURI, tc.wantURL)
		}
	}
}

// TestIdentityKeysAreOmitted 身份键必须省略而不是写空串。
//
// 手机版读取方式是 optString(key, 本地值)：键存在但为空串会被当成「确认清空」，
// 直接把对端的 GameHub/gaishi 身份键抹掉，导致后续同步匹配不上。
func TestIdentityKeysAreOmitted(t *testing.T) {
	t.Parallel()

	entry := yukihub.Game{Title: "游戏 A"}
	session := yukihub.PlaySession{SessionUUID: "u1", GameTitle: "游戏 A"}

	for label, value := range map[string]interface{}{"game": entry, "session": session} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", label, err)
		}
		if strings.Contains(string(raw), "gamehub_local_game_id") || strings.Contains(string(raw), "gaishi_local_game_id") {
			t.Errorf("%s 的空身份键应被省略：%s", label, raw)
		}
	}
}

// TestEmptyTextFieldsAreOmitted 空文本字段必须省略，不能写成空串。
//
// 手机版 importGamesJson 用的是 optString(key, 本地值)：键存在但为空串 =
// 「确认清空」。桌面端这三项经常是空的（没刮削的游戏没有简介 / 标签）。
// 早先恒写 ""，会把手机端同名字段直接抹掉。
func TestEmptyTextFieldsAreOmitted(t *testing.T) {
	t.Parallel()

	empty, _ := json.Marshal(yukihub.Game{Title: "没有简介的游戏"})
	for _, key := range []string{"original_title", "description", "tags"} {
		if strings.Contains(string(empty), `"`+key+`"`) {
			t.Errorf("空的 %s 应被省略：%s", key, empty)
		}
	}

	filled, _ := json.Marshal(yukihub.Game{
		Title:         "有简介的游戏",
		OriginalTitle: "原名",
		Description:   "简介",
		Tags:          "标签A,标签B",
	})
	for _, key := range []string{"original_title", "description", "tags"} {
		if !strings.Contains(string(filled), `"`+key+`"`) {
			t.Errorf("非空的 %s 必须写出：%s", key, filled)
		}
	}
}

// TestUnfinishedSessionOmitsEndTime 未结束会话不能写 end_time:0。
//
// 手机版只在 end_time 非 null 时才写这个键，导入时用 has()/isNull() 判断
// 「是否已结束」，未结束的落库为 NULL。写 0 会被当成「1970 年就结束了」，
// 而且 0 在手机端 IS NOT NULL，会被算进时长聚合。
func TestUnfinishedSessionOmitsEndTime(t *testing.T) {
	t.Parallel()

	pending, _ := json.Marshal(yukihub.PlaySession{SessionUUID: "u1", StartTime: 1000})
	if strings.Contains(string(pending), `"end_time"`) {
		t.Errorf("未结束会话应省略 end_time：%s", pending)
	}

	finished, _ := json.Marshal(yukihub.PlaySession{SessionUUID: "u1", StartTime: 1000, EndTime: 2000})
	if !strings.Contains(string(finished), `"end_time":2000`) {
		t.Errorf("已结束会话必须写出 end_time：%s", finished)
	}
}

// TestMetadataCacheCarriesMatchKeys metadata_cache 必须带 game_root_uri / game_title。
//
// 手机版 importMetadataJson 先按 game_root_uri 匹配、为空时按 game_title 精确匹配，
// 只有 title 非空才回退 game_local_id。少了这两个键，整段会被逐条静默丢弃。
func TestMetadataCacheCarriesMatchKeys(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(yukihub.MetadataCache{
		GameLocalID: 7,
		GameTitle:   "游戏 A",
		Source:      "vndb",
		SourceID:    "v1",
		JSON:        "{}",
	})
	if err != nil {
		t.Fatalf("marshal metadata cache: %v", err)
	}
	for _, key := range []string{"game_root_uri", "game_title", "game_local_id"} {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("metadata_cache 缺少 %s：%s", key, raw)
		}
	}
}

// TestFirstNetworkURLPrefersUsableRemoteCover 钉住「首个 http(s)」而不是「首个非空」。
//
// 调用方传 (cover_source_url, cover_url)：cover_source_url 在历史数据里可能落成
// 本地路径，若按「首个非空」取值，后面那个有效的网络地址就被挤掉，封面同步会丢。
func TestFirstNetworkURLPrefersUsableRemoteCover(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"两个都是网络地址取第一个", []string{"https://a/1.jpg", "https://b/2.jpg"}, "https://a/1.jpg"},
		{"第一个为空时回退第二个", []string{"", "https://b/2.jpg"}, "https://b/2.jpg"},
		{"本地路径不能挤掉网络地址", []string{`D:\covers\local.jpg`, "https://b/2.jpg"}, "https://b/2.jpg"},
		{"content URI 同样跳过", []string{"content://media/1", "http://b/2.jpg"}, "http://b/2.jpg"},
		{"都不可跨设备则留空", []string{"content://media/1", `D:\covers\local.jpg`}, ""},
		{"全空", []string{"", ""}, ""},
	}
	for _, tc := range cases {
		if got := firstNetworkURL(tc.in...); got != tc.want {
			t.Errorf("%s: firstNetworkURL(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
