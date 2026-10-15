package service

import (
	"context"
	"testing"
	"time"

	"yukihub/internal/appconf"
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
