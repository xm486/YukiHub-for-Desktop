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
)

// 合并导入（云同步 / WebDAV 自持同步）时的隐藏标记语义，必须与手机版
// `g.hidden = o.optBoolean("hidden", g.hidden)` 逐字一致：
//
//   - 快照**带了** hidden  → 采信对端（true / false 都算明确表态）
//   - 快照**没带** hidden  → 保留本地值
//
// 这条最容易被写坏的地方是「用 bool 承载」：缺键解码成 false，
// 于是「没信息」被当成「明确不隐藏」，一次同步就把本地藏好的游戏全部翻出来。
// 老快照、以及只带 games 段（没有 metadata_cache）的快照都不带这个键，
// 属于高发场景，所以这里用真库跑一遍合并语句，而不是只断言内存里的结构体。

func newHiddenSyncTestConn(t *testing.T) (*sql.Conn, context.Context) {
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
	return conn, ctx
}

// seedHiddenSyncGame 直接落一条游戏记录，hidden 由参数决定。
func seedHiddenSyncGame(t *testing.T, ctx context.Context, conn *sql.Conn, id string, hidden bool) {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO games (id, name, hidden, source_type, status, created_at, updated_at)
		VALUES (?, ?, ?, 'local', 'unplayed', ?, ?)
	`, id, "测试游戏 "+id, hidden, now, now); err != nil {
		t.Fatalf("seed game %s: %v", id, err)
	}
}

func readHiddenSyncFlag(t *testing.T, ctx context.Context, conn *sql.Conn, id string) bool {
	t.Helper()

	var hidden bool
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(hidden, FALSE) FROM games WHERE id = ?`, id).Scan(&hidden); err != nil {
		t.Fatalf("read hidden for %s: %v", id, err)
	}
	return hidden
}

func syncMergeItem(id string, hidden *bool) CommitItem {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return CommitItem{
		Game: models.Game{
			ID:         id,
			Name:       "测试游戏 " + id,
			Status:     enums.StatusUnplayed,
			SourceType: enums.Local,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		Source: enums.Local,
		Action: ImportActionSyncMerge,
		Hidden: hidden,
	}
}

func TestSyncMergeAppliesHiddenOnlyWhenSnapshotCarriesTheKey(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	// 三种组合一次跑完：
	//   absent 本地隐藏  → 必须保持隐藏（核心回归点）
	//   explicit false   → 取消隐藏
	//   explicit true    → 变成隐藏
	seedHiddenSyncGame(t, ctx, conn, "absent", true)
	seedHiddenSyncGame(t, ctx, conn, "to-visible", true)
	seedHiddenSyncGame(t, ctx, conn, "to-hidden", false)

	falseValue := false
	trueValue := true
	items := []CommitItem{
		syncMergeItem("absent", nil),
		syncMergeItem("to-visible", &falseValue),
		syncMergeItem("to-hidden", &trueValue),
	}

	if _, err := committer.updateImportedItemSyncMetadata(ctx, conn, items); err != nil {
		t.Fatalf("sync merge: %v", err)
	}

	if readHiddenSyncFlag(t, ctx, conn, "absent") {
		// 通过；下面这些断言写反了才是问题
	} else {
		t.Error("快照没带 hidden 键时本地必须保持隐藏 —— 否则一次云同步就把藏好的游戏全翻出来了")
	}
	if readHiddenSyncFlag(t, ctx, conn, "to-visible") {
		t.Error("快照明确带 hidden=false 时必须取消隐藏（否则「在手机端取消隐藏」同步不过来）")
	}
	if !readHiddenSyncFlag(t, ctx, conn, "to-hidden") {
		t.Error("快照明确带 hidden=true 时必须隐藏")
	}
}

// 其它导入格式（PotatoVN / Playnite / VNite / LunaBox …）没有隐藏概念，
// 它们的 Hidden 恒为 nil，**不能**因此把本地已隐藏的游戏翻出来。
func TestUpdateExistingKeepsLocalHiddenForFormatsWithoutTheConcept(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	seedHiddenSyncGame(t, ctx, conn, "potatovn", true)

	item := syncMergeItem("potatovn", nil)
	item.Action = ImportActionUpdateExisting
	if _, err := committer.updateImportedItemMetadata(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("update existing: %v", err)
	}

	if !readHiddenSyncFlag(t, ctx, conn, "potatovn") {
		t.Error("不带隐藏概念的导入格式不该改动本地 hidden")
	}
}

// 新建路径不受三态影响：快照没带键的新游戏落成「不隐藏」。
func TestCreateFromStagingDefaultsHiddenToFalse(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	item := syncMergeItem("brand-new", nil)
	item.Action = ImportActionCreate

	if _, err := committer.addImportedItems(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("create from staging: %v", err)
	}
	if readHiddenSyncFlag(t, ctx, conn, "brand-new") {
		t.Error("新建条目没带 hidden 时应落成不隐藏")
	}

	// 带了 hidden=true 的新条目要直接落成隐藏
	trueValue := true
	hiddenItem := syncMergeItem("brand-new-hidden", &trueValue)
	hiddenItem.Action = ImportActionCreate
	if _, err := committer.addImportedItems(ctx, conn, []CommitItem{hiddenItem}); err != nil {
		t.Fatalf("create hidden game from staging: %v", err)
	}
	if !readHiddenSyncFlag(t, ctx, conn, "brand-new-hidden") {
		t.Error("新建条目带 hidden=true 时应落成隐藏")
	}
}
