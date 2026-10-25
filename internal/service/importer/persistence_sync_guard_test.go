package importer

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"yukihub/internal/common/enums"
	"yukihub/internal/migrations"
	"yukihub/internal/models"
	"yukihub/internal/utils/metadata"
)

// newCommitTestDB 提供 CommitItems 需要的 *sql.DB（内部会自己开连接）。
func newCommitTestDB(t *testing.T) (*sql.DB, *sql.Conn, context.Context) {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open DuckDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrations.InitSchema(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return db, conn, ctx
}

// 云同步（sync_merge）的「谁更新」门槛必须与手机版 importGamesJson 一致：
//
//	applyDetails = !exists || incomingUpdatedAt >= existingUpdatedAt
//	（GameRepository.java:682）
//
// 对端更旧时只补缺失的身份键、**不动任何字段**（:706-713）。
//
// 这条一旦缺失就是真实的数据丢失：本地刚改过、还没上传的字段会被服务器上的旧快照
// 整块覆盖回去，而且 games.updated_at 被写回旧值 —— 之后每一次比较都会判反，
// 本地要一直等到「时钟超过那个旧值」才能再被同步更新。
//
// 注意它**只该管 sync_merge**：文件导入（update_existing）是用户主动选了一个文件，
// 「照抄文件里的资料」本身就是意图，用本地修改时间挡住会变成「导入没反应」。

func syncMergeGameItem(id string, updatedAt time.Time, name string, company string) CommitItem {
	return CommitItem{
		Game: models.Game{
			ID:         id,
			Name:       name,
			Company:    company,
			Status:     enums.StatusUnplayed,
			SourceType: enums.Local,
			CreatedAt:  updatedAt,
			UpdatedAt:  updatedAt,
		},
		Source: enums.Local,
		Action: ImportActionSyncMerge,
	}
}

func seedGuardGame(t *testing.T, ctx context.Context, conn *sql.Conn, id, name, company string, updatedAt time.Time) {
	t.Helper()
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO games (id, name, company, hidden, source_type, status, created_at, updated_at)
		VALUES (?, ?, ?, FALSE, 'local', 'unplayed', ?, ?)
	`, id, name, company, updatedAt, updatedAt); err != nil {
		t.Fatalf("seed game %s: %v", id, err)
	}
}

func readGuardGame(t *testing.T, ctx context.Context, conn *sql.Conn, id string) (name, company string, updatedAt time.Time) {
	t.Helper()
	if err := conn.QueryRowContext(ctx, `SELECT name, COALESCE(company, ''), updated_at FROM games WHERE id = ?`, id).
		Scan(&name, &company, &updatedAt); err != nil {
		t.Fatalf("read game %s: %v", id, err)
	}
	return name, company, updatedAt
}

func TestSyncMergeIgnoresOlderSnapshot(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	base := time.Now().UTC().Truncate(time.Millisecond)
	localUpdatedAt := base.Add(2 * time.Hour)
	seedGuardGame(t, ctx, conn, "g-old", "本地新名字", "本地会社", localUpdatedAt)

	// 服务器上那份是两小时前的：名字 / 会社 / updated_at 全都不许动。
	item := syncMergeGameItem("g-old", base, "旧快照名字", "旧会社")
	if _, err := committer.updateImportedItemSyncMetadata(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("sync merge: %v", err)
	}

	name, company, updatedAt := readGuardGame(t, ctx, conn, "g-old")
	if name != "本地新名字" {
		t.Errorf("旧快照不得覆盖本地名称，实际 %q", name)
	}
	if company != "本地会社" {
		t.Errorf("旧快照不得覆盖本地会社，实际 %q", company)
	}
	if !updatedAt.Equal(localUpdatedAt) {
		t.Errorf("updated_at 不得被旧快照写回（写回会让之后每次比较都判反），实际 %v，期望 %v", updatedAt, localUpdatedAt)
	}
}

// 守卫是「对端更新才覆盖」而不是「一律不覆盖」：对端确实更晚时必须照常生效，
// 否则手机上的修改永远同步不过来。
func TestSyncMergeAppliesNewerSnapshot(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	base := time.Now().UTC().Truncate(time.Millisecond)
	seedGuardGame(t, ctx, conn, "g-new", "本地旧名字", "本地会社", base)

	newerUpdatedAt := base.Add(2 * time.Hour)
	item := syncMergeGameItem("g-new", newerUpdatedAt, "对端新名字", "对端会社")
	if _, err := committer.updateImportedItemSyncMetadata(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("sync merge: %v", err)
	}

	name, company, updatedAt := readGuardGame(t, ctx, conn, "g-new")
	if name != "对端新名字" {
		t.Errorf("对端更新更晚时应采用对端名称，实际 %q", name)
	}
	if company != "对端会社" {
		t.Errorf("对端更新更晚时应采用对端会社，实际 %q", company)
	}
	if !updatedAt.Equal(newerUpdatedAt) {
		t.Errorf("updated_at 应推进到对端的值，实际 %v，期望 %v", updatedAt, newerUpdatedAt)
	}
}

// 边界：两端 updated_at 完全相同必须**算更新**（手机版是 `>=`，不是 `>`），
// 否则「同一秒内两端都动过」时会出现谁都盖不动谁的死锁。
func TestSyncMergeAppliesWhenUpdatedAtEqual(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	same := time.Now().UTC().Truncate(time.Millisecond)
	seedGuardGame(t, ctx, conn, "g-eq", "本地名字", "", same)

	item := syncMergeGameItem("g-eq", same, "对端名字", "")
	if _, err := committer.updateImportedItemSyncMetadata(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("sync merge: %v", err)
	}

	if name, _, _ := readGuardGame(t, ctx, conn, "g-eq"); name != "对端名字" {
		t.Errorf("updated_at 相同时应采信对端（手机版用 >=），实际 %q", name)
	}
}

// 文件导入（update_existing）不受这条守卫影响：PotatoVN / Playnite / LunaBox
// 这类导入是用户主动选文件，「照抄文件里的资料」本身就是意图。
func TestUpdateExistingStillAppliesRegardlessOfTimestamp(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	base := time.Now().UTC().Truncate(time.Millisecond)
	seedGuardGame(t, ctx, conn, "g-file", "本地名字", "本地会社", base.Add(5*time.Hour))

	item := syncMergeGameItem("g-file", base, "文件里的名字", "文件里的会社")
	item.Action = ImportActionUpdateExisting
	if _, err := committer.updateImportedItemMetadata(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("update existing: %v", err)
	}

	name, company, _ := readGuardGame(t, ctx, conn, "g-file")
	if name != "文件里的名字" {
		t.Errorf("文件导入必须照抄文件里的名称，实际 %q", name)
	}
	if company != "文件里的会社" {
		t.Errorf("文件导入必须照抄文件里的会社，实际 %q", company)
	}
}

// 旧快照虽然不该改字段，但**本地缺失的身份键**仍应被补上 ——
// 手机版 importGamesJson 的 else 分支就是这么做的（GameRepository.java:706-713：
// 「Older card metadata must not overwrite newer local edits, but it may still fill
// identity fields that are missing locally」）。
//
// 桌面端对应 legacy_local_id / source_device_id：缺了它们，后续按身份匹配会退化，
// 只能靠标题模糊匹配，容易生出重复条目。
func TestOlderSnapshotStillFillsMissingIdentityFields(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	base := time.Now().UTC().Truncate(time.Millisecond)
	seedGuardGame(t, ctx, conn, "g-id", "本地名字", "本地会社", base.Add(2*time.Hour))

	item := syncMergeGameItem("g-id", base, "旧快照名字", "旧会社")
	item.Game.LegacyLocalID = "4242"
	item.Game.SourceDeviceID = "device-a"
	if _, err := committer.updateImportedItemSyncMetadata(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("sync merge: %v", err)
	}

	var legacyID, deviceID string
	if err := conn.QueryRowContext(ctx,
		`SELECT COALESCE(legacy_local_id, ''), COALESCE(source_device_id, '') FROM games WHERE id = 'g-id'`).
		Scan(&legacyID, &deviceID); err != nil {
		t.Fatalf("read identity fields: %v", err)
	}
	if legacyID != "4242" {
		t.Errorf("本地缺失 legacy_local_id 时旧快照应补上，实际 %q", legacyID)
	}
	if deviceID != "device-a" {
		t.Errorf("本地缺失 source_device_id 时旧快照应补上，实际 %q", deviceID)
	}
	if name, _, _ := readGuardGame(t, ctx, conn, "g-id"); name != "本地名字" {
		t.Errorf("补身份键的同时不得改名称，实际 %q", name)
	}
}

// 云同步必须把 metadata_cache 与 tags 一起并进来。
//
// 手机版 SyncManager 每次同步都执行
// `metaRepo.importMetadataJson(root.optJSONArray("metadata_cache"))`（SyncManager.java:388），
// 而 importGamesJson 会照抄 `tags`。桌面端早先把 sync_merge 从「元数据类步骤」里
// 排除了，于是「手机刮削 → 桌面同步」永远传不过来：games 段的 company / rating
// 看着对（那是内存里解析的），但 game_metadata_sources 一行都没有 ——
// 桌面端再导出时 metadata_cache 就是空数组，元数据链条在只剩一台桌面端时断掉。
func TestSyncMergeImportsMetadataCacheAndTags(t *testing.T) {
	db, conn, ctx := newCommitTestDB(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx, DB: db})

	now := time.Now().UTC().Truncate(time.Millisecond)
	seedGuardGame(t, ctx, conn, "g-meta", "本地名字", "", now)

	item := syncMergeGameItem("g-meta", now.Add(time.Hour), "对端名字", "")
	item.Game.SourceType = enums.VNDB
	item.Game.SourceID = "v12345"
	item.Game.MetadataSources = []models.GameMetadataSource{{
		SourceType: enums.VNDB,
		SourceID:   "v12345",
		CacheJSON:  `{"id":"v12345","title":"对端名字"}`,
		CachedAt:   now,
		UpdatedAt:  now,
	}}
	item.Tags = []metadata.TagItem{{Name: "剧情"}, {Name: "校园"}}

	if _, _, err := committer.CommitItems([]CommitItem{item}); err != nil {
		t.Fatalf("CommitItems: %v", err)
	}

	var cacheJSON, sourceID string
	if err := conn.QueryRowContext(ctx, `
		SELECT COALESCE(cache_json, ''), COALESCE(source_id, '')
		FROM game_metadata_sources
		WHERE game_id = 'g-meta' AND source_type = ?
	`, string(enums.VNDB)).Scan(&cacheJSON, &sourceID); err != nil {
		t.Fatalf("同步必须写入 game_metadata_sources（手机版每次同步都导 metadata_cache）: %v", err)
	}
	if cacheJSON == "" {
		t.Error("metadata_cache 负载不得为空 —— 否则桌面端再导出时整段 metadata_cache 会消失")
	}
	if sourceID != "v12345" {
		t.Errorf("source_id = %q, want v12345", sourceID)
	}

	var tagCount int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_tags WHERE game_id = 'g-meta'`).Scan(&tagCount); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if tagCount != 2 {
		t.Errorf("同步应导入对端 tags（手机版照抄 tags 字段），实际 %d 条", tagCount)
	}

	// 重复同步不得产生重复行（幂等）。
	if _, _, err := committer.CommitItems([]CommitItem{item}); err != nil {
		t.Fatalf("重复 CommitItems: %v", err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_tags WHERE game_id = 'g-meta'`).Scan(&tagCount); err != nil {
		t.Fatalf("count tags after repeat: %v", err)
	}
	if tagCount != 2 {
		t.Errorf("重复同步后 tags 仍应是 2 条，实际 %d", tagCount)
	}
}

// 后台同步不得触发封面下载，但文件导入必须触发 —— 两条通路要分开。
//
// downloadAndUpdateCoverImage 在 cover_source_url 与对端 URL 相同时仍会重新下载，
// 同步又是周期性运行的：一旦把 sync_merge 混进封面处理，等于每隔一会儿把整个
// 库的封面重下一遍。
func TestCoverDownloadsOnlyRunForFileImports(t *testing.T) {
	db, conn, ctx := newCommitTestDB(t)

	var downloadCalls int
	committer := NewCommitter(CommitDependencies{
		Ctx: ctx,
		DB:  db,
		StartCoverDownloads: func(items []CoverDownloadItem) string {
			downloadCalls += len(items)
			return "task-1"
		},
	})

	now := time.Now().UTC().Truncate(time.Millisecond)
	seedGuardGame(t, ctx, conn, "g-sync", "同步游戏", "", now)
	seedGuardGame(t, ctx, conn, "g-file", "文件游戏", "", now)

	syncItem := syncMergeGameItem("g-sync", now.Add(time.Hour), "同步游戏", "")
	syncItem.Game.CoverURL = "https://example.invalid/sync.webp"
	syncItem.Game.CoverSourceURL = syncItem.Game.CoverURL

	fileItem := syncMergeGameItem("g-file", now.Add(time.Hour), "文件游戏", "")
	fileItem.Action = ImportActionUpdateExisting
	fileItem.Game.CoverURL = "https://example.invalid/file.webp"
	fileItem.Game.CoverSourceURL = fileItem.Game.CoverURL

	if _, _, err := committer.CommitItems([]CommitItem{syncItem, fileItem}); err != nil {
		t.Fatalf("CommitItems: %v", err)
	}

	if downloadCalls != 1 {
		t.Errorf("封面下载应只覆盖文件导入那 1 个条目（后台同步不下载封面），实际 %d 个", downloadCalls)
	}
}
