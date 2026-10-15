package service

import (
	"context"
	"testing"
	"time"

	"yukihub/internal/appconf"
	"yukihub/internal/models"
)

// 「正在玩」变化的回调：开始游玩、结束游玩（含短会话被删除）都必须触发一次。
//
// 这是好友能**立刻**看到「正在玩」的前提：AccountService 收到回调后补发一次心跳，
// 而不是等下一个 45 秒周期。回调本身是异步的，所以用带超时的等待而不是直接断言。
func TestPlayingChangeHookFiresOnStartAndFinish(t *testing.T) {
	db := setupSessionServiceTestDB(t)
	sessions := NewSessionService()
	sessions.Init(context.Background(), db, &appconf.AppConfig{})

	signals := make(chan struct{}, 8)
	sessions.SetPlayingChangeHook(func() { signals <- struct{}{} })

	sessionID, err := sessions.CreatePendingSession("game-1", time.Now())
	if err != nil {
		t.Fatalf("CreatePendingSession: %v", err)
	}
	expectPlayingSignal(t, signals, "开始游玩")

	if err := sessions.completeUnfinishedSessionWithDuration(sessionID, time.Now(), 120); err != nil {
		t.Fatalf("completeUnfinishedSessionWithDuration: %v", err)
	}
	expectPlayingSignal(t, signals, "结束游玩（正常完成）")

	// 短会话走「删除」分支，同样不再是「在玩」，也必须触发一次。
	shortID, err := sessions.CreatePendingSession("game-2", time.Now())
	if err != nil {
		t.Fatalf("CreatePendingSession: %v", err)
	}
	expectPlayingSignal(t, signals, "开始游玩（第二条会话）")

	if err := sessions.completeUnfinishedSessionWithDuration(shortID, time.Now(), 10); err != nil {
		t.Fatalf("completeUnfinishedSessionWithDuration(short): %v", err)
	}
	expectPlayingSignal(t, signals, "结束游玩（短会话被删除）")
}

func expectPlayingSignal(t *testing.T, signals <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s 应触发「正在玩」变化回调，2 秒内没有收到", what)
	}
}

func expectNoPlayingSignal(t *testing.T, signals <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signals:
		t.Fatalf("%s 不该触发「正在玩」变化回调", what)
	case <-time.After(300 * time.Millisecond):
	}
}

// 「退出游戏」在桌面端**不走** completeUnfinishedSession，而是走
// finalizePlaySessionOnce → UpdatePlaySession（正常结算）或 DeletePlaySession
// （不足 1 分钟的短会话）。这两条也必须触发补发，否则就是「启动快了、退出还是慢」——
// 这正是用户第二轮反馈的现象。
//
// 手机版对应的是 clearPlayingGame()（PresenceManager.java:169）：
// 清空 activity 后**立即**发一次心跳，不等周期。
func TestPlayingChangeHookFiresOnSessionEndPaths(t *testing.T) {
	db := setupSessionServiceTestDB(t)
	sessions := NewSessionService()
	sessions.Init(context.Background(), db, &appconf.AppConfig{})

	signals := make(chan struct{}, 8)
	sessions.SetPlayingChangeHook(func() { signals <- struct{}{} })

	// 结束路径 A：正常结算 → UpdatePlaySession
	sessionID, err := sessions.CreatePendingSession("game-1", time.Now())
	if err != nil {
		t.Fatalf("CreatePendingSession: %v", err)
	}
	expectPlayingSignal(t, signals, "开始游玩")

	if err := sessions.UpdatePlaySession(models.PlaySession{
		ID:        sessionID,
		GameID:    "game-1",
		StartTime: time.Now(),
		Duration:  300,
	}); err != nil {
		t.Fatalf("UpdatePlaySession: %v", err)
	}
	expectPlayingSignal(t, signals, "结束游玩（UpdatePlaySession）")

	// 结束路径 B：短会话 / 启动被取消 → DeletePlaySession
	shortID, err := sessions.CreatePendingSession("game-2", time.Now())
	if err != nil {
		t.Fatalf("CreatePendingSession: %v", err)
	}
	expectPlayingSignal(t, signals, "开始游玩（第二条会话）")

	if err := sessions.DeletePlaySession(shortID); err != nil {
		t.Fatalf("DeletePlaySession: %v", err)
	}
	expectPlayingSignal(t, signals, "结束游玩（DeletePlaySession）")
}

// 反向断言：动**已结束**的会话不该补发心跳。
//
// 前端统计面板可以改 / 删历史记录（GameStatsPanel → DeletePlaySession），
// 那与「正在玩」无关；若无脑补发，用户每改一条历史就往服务端打一次心跳。
func TestPlayingChangeHookSilentForFinishedSessions(t *testing.T) {
	db := setupSessionServiceTestDB(t)
	sessions := NewSessionService()
	sessions.Init(context.Background(), db, &appconf.AppConfig{})

	signals := make(chan struct{}, 8)
	sessions.SetPlayingChangeHook(func() { signals <- struct{}{} })

	sessionID, err := sessions.CreatePendingSession("game-1", time.Now())
	if err != nil {
		t.Fatalf("CreatePendingSession: %v", err)
	}
	expectPlayingSignal(t, signals, "开始游玩")

	// 先正常结束，这条会话从此是「历史记录」。
	if err := sessions.UpdatePlaySession(models.PlaySession{
		ID:        sessionID,
		GameID:    "game-1",
		StartTime: time.Now(),
		Duration:  300,
	}); err != nil {
		t.Fatalf("UpdatePlaySession: %v", err)
	}
	expectPlayingSignal(t, signals, "结束游玩")

	// 再改一次、再删掉 —— 都已经结束了，都不该触发。
	if err := sessions.UpdatePlaySession(models.PlaySession{
		ID:        sessionID,
		GameID:    "game-1",
		StartTime: time.Now(),
		Duration:  600,
	}); err != nil {
		t.Fatalf("UpdatePlaySession（第二次）: %v", err)
	}
	if err := sessions.DeletePlaySession(sessionID); err != nil {
		t.Fatalf("DeletePlaySession: %v", err)
	}
	expectNoPlayingSignal(t, signals, "改动 / 删除已结束的会话")
}

// 没注册回调时不得 panic：服务装配顺序不保证，只读路径（MCP）与部分测试
// 都不会注册这个回调。
func TestPlayingChangeHookIsOptional(t *testing.T) {
	db := setupSessionServiceTestDB(t)
	sessions := NewSessionService()
	sessions.Init(context.Background(), db, &appconf.AppConfig{})

	if _, err := sessions.CreatePendingSession("game-3", time.Now()); err != nil {
		t.Fatalf("CreatePendingSession: %v", err)
	}
}
