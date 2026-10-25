package importer

import (
	"testing"
	"time"

	"yukihub/internal/common/enums"
	"yukihub/internal/models"
)

// 云同步 / WebDAV 导入游玩会话的幂等性。
//
// 线上故障（用户反馈）：
//
//	导入快照失败: insert imported sessions from staging: Constraint Error:
//	Duplicate key "id: 022c07de-4fce-51cd-9123-f00399c09bfb" violates primary key constraint.
//
// 根因：快照里的 `session_uuid` 就是本地 `play_sessions.id`
// （见 convertYukiHubSessions），但写入前只按 `(game_id, start_time, end_time)`
// 判重，**完全没看主键**。同一场游玩在两端各存一份、end_time 略有差异
// （本地按墙钟结算，快照按 duration 反推）就会「判重认为不存在 → INSERT → 撞主键」，
// 连同一次同步的其它数据一起回滚，整次同步失败。
//
// 手机版的对应实现是按 `session_uuid` 查重后 UPDATE（GameRepository.java:764-808），
// 这里用真 DuckDB 把三条语义都钉住。

func sessionsCommitItem(gameID string, sessions ...models.PlaySession) CommitItem {
	return CommitItem{
		Game:     models.Game{ID: gameID, Name: "测试游戏 " + gameID},
		Source:   enums.Local,
		Action:   ImportActionMergeSessions,
		Sessions: sessions,
	}
}

func TestSessionImportUpsertsWhenSameIDHasDifferentEndTime(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	base := time.Now().UTC().Truncate(time.Second)
	start := base.Add(-time.Hour)
	localEnd := start.Add(600 * time.Second)

	// 本地：同一场游玩已按墙钟结算。
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at)
		VALUES ('sess-1', 'game-1', ?, ?, 600, ?)
	`, start, localEnd, localEnd); err != nil {
		t.Fatalf("seed local session: %v", err)
	}

	// 快照：同一个 session_uuid，end_time 按 duration 反推，差了 5 秒。
	remoteEnd := start.Add(605 * time.Second)
	item := sessionsCommitItem("game-1", models.PlaySession{
		ID:        "sess-1",
		GameID:    "game-1",
		StartTime: start,
		EndTime:   remoteEnd,
		Duration:  605,
		UpdatedAt: remoteEnd,
	})

	if _, err := committer.addImportedItemSessions(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("同一个 session_uuid 的更新版本不该报错（线上就是这里抛 Duplicate key）: %v", err)
	}

	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM play_sessions WHERE id = 'sess-1'`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("同一个 session_uuid 只该留一行，实际 %d 行", count)
	}

	var duration int
	if err := conn.QueryRowContext(ctx, `SELECT duration FROM play_sessions WHERE id = 'sess-1'`).Scan(&duration); err != nil {
		t.Fatalf("read duration: %v", err)
	}
	if duration != 605 {
		t.Errorf("对端 updated_at 更晚时应采用对端时长，实际 %d", duration)
	}

	// 不变量：落库的 updated_at 必须**逐毫秒**等于对端的值。
	// 暂存表的 updated_at 是 Appender 写的 TIMESTAMPTZ，时区解释与普通 Exec 不同
	// （这张表的历史注释里提过），一旦写歪，下一次同步的「谁更新」判断就会错到底：
	// 写偏到未来 ⇒ 对端永远被判成旧快照，会话从此再也更新不进来。
	var storedMs int64
	if err := conn.QueryRowContext(ctx, `SELECT epoch_ms(updated_at) FROM play_sessions WHERE id = 'sess-1'`).Scan(&storedMs); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	if storedMs != remoteEnd.UnixMilli() {
		t.Errorf("落库的 updated_at 必须等于对端的值，实际 %d，期望 %d", storedMs, remoteEnd.UnixMilli())
	}
}

// 同一份快照里，同一个 session_uuid 出现两次且起止时间不同 —— 这时连
// `ON CONFLICT` 都会在一条语句里撞两次主键，所以必须先按 id 去重。
func TestSessionImportHandlesSameIDTwiceInOneSnapshot(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	base := time.Now().UTC().Truncate(time.Second)
	start := base.Add(-time.Hour)

	item := sessionsCommitItem("game-1",
		models.PlaySession{
			ID: "sess-dup", GameID: "game-1",
			StartTime: start, EndTime: start.Add(300 * time.Second),
			Duration: 300, UpdatedAt: start.Add(300 * time.Second),
		},
		models.PlaySession{
			ID: "sess-dup", GameID: "game-1",
			StartTime: start, EndTime: start.Add(400 * time.Second),
			Duration: 400, UpdatedAt: start.Add(400 * time.Second),
		},
	)

	if _, err := committer.addImportedItemSessions(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("同一 session_uuid 在快照里出现两次不该报错: %v", err)
	}

	var count, duration int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM play_sessions WHERE id = 'sess-dup'`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("同一 session_uuid 只该留一行，实际 %d 行", count)
	}
	if err := conn.QueryRowContext(ctx, `SELECT duration FROM play_sessions WHERE id = 'sess-dup'`).Scan(&duration); err != nil {
		t.Fatalf("read duration: %v", err)
	}
	if duration != 400 {
		t.Errorf("重复的 session_uuid 应保留 updated_at 更新的那条（400 秒），实际 %d", duration)
	}
}

// 本地正在游玩的会话（end_time IS NULL，心跳刚刷新过 updated_at）
// 不能被旧快照的「已结束」版本覆盖回去 —— 否则好友那边会突然收起「正在玩」，
// 本地计时也被提前结掉。与手机版 `incomingUpdatedAt < existingUpdatedAt → continue` 一致。
func TestSessionImportKeepsLocalRunningSessionOverStaleSnapshot(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-30 * time.Minute)

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at)
		VALUES ('sess-running', 'game-1', ?, NULL, 1800, ?)
	`, start, now); err != nil {
		t.Fatalf("seed running session: %v", err)
	}

	// 快照是同一场游玩 10 分钟前抓的，那时还没结束。
	staleEnd := now.Add(-20 * time.Minute)
	item := sessionsCommitItem("game-1", models.PlaySession{
		ID:        "sess-running",
		GameID:    "game-1",
		StartTime: start,
		EndTime:   staleEnd,
		Duration:  600,
		UpdatedAt: staleEnd,
	})

	if _, err := committer.addImportedItemSessions(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("旧快照不该让导入失败: %v", err)
	}

	var endNull bool
	if err := conn.QueryRowContext(ctx, `SELECT end_time IS NULL FROM play_sessions WHERE id = 'sess-running'`).Scan(&endNull); err != nil {
		t.Fatalf("read end_time: %v", err)
	}
	if !endNull {
		t.Error("本地正在游玩的会话被旧快照结掉了 —— 好友会突然看不到「正在玩」")
	}

	var duration int
	if err := conn.QueryRowContext(ctx, `SELECT duration FROM play_sessions WHERE id = 'sess-running'`).Scan(&duration); err != nil {
		t.Fatalf("read duration: %v", err)
	}
	if duration != 1800 {
		t.Errorf("旧快照不该覆盖本地时长，实际 %d（应为 1800）", duration)
	}
}

// 原有语义不能丢：同一场游玩被两端各记了一条（uuid 不同、起止时间相同）时
// 只保留一条，避免时长被重复累计。
func TestSessionImportSkipsDifferentIDWithSameTimeRange(t *testing.T) {
	conn, ctx := newHiddenSyncTestConn(t)
	committer := NewCommitter(CommitDependencies{Ctx: ctx})

	base := time.Now().UTC().Truncate(time.Second)
	start := base.Add(-time.Hour)
	end := start.Add(900 * time.Second)

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO play_sessions (id, game_id, start_time, end_time, duration, updated_at)
		VALUES ('sess-local', 'game-1', ?, ?, 900, ?)
	`, start, end, end); err != nil {
		t.Fatalf("seed local session: %v", err)
	}

	item := sessionsCommitItem("game-1", models.PlaySession{
		ID:        "sess-remote",
		GameID:    "game-1",
		StartTime: start,
		EndTime:   end,
		Duration:  900,
		UpdatedAt: end,
	})

	if _, err := committer.addImportedItemSessions(ctx, conn, []CommitItem{item}); err != nil {
		t.Fatalf("同起止时间的另一条记录不该让导入失败: %v", err)
	}

	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM play_sessions WHERE game_id = 'game-1'`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("同一次游玩的记录不该重复累计，实际 %d 行", count)
	}
}
