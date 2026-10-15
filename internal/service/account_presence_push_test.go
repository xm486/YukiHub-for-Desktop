package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"yukihub/internal/appconf"
	"yukihub/internal/applog"
	"yukihub/internal/service/yukihubaccount"
)

// 契约：docs/yukihub-presence-platform.md §3.2
//
// 「正在玩」发生变化时必须**立即**补发一次心跳 —— 手机版
// `PresenceManager.updateActivity()` 的注释就是「更新后立即发一次心跳」。
// 桌面端原来只在 45 秒的周期心跳里带上 activity，好友最长要等一整个周期
// 才看到「正在玩」，这正是用户反馈的「启动游戏后延迟几十秒」。

func newPresencePushTestService(t *testing.T, serverURL string) *AccountService {
	t.Helper()
	applog.SetMode(applog.ModeCLI)
	return &AccountService{
		ctx:    context.Background(),
		config: &appconf.AppConfig{YukiHubAccountAccessToken: "token-1"},
		client: yukihubaccount.NewClient(serverURL),
	}
}

// waitForHeartbeatCount 等到记录到的请求数达到 want，超时报错。
func waitForHeartbeatCount(t *testing.T, mu *sync.Mutex, requests *[]string, want int, timeout time.Duration) []string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		mu.Lock()
		count := len(*requests)
		snapshot := append([]string(nil), (*requests)...)
		mu.Unlock()
		if count >= want {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 %d 次心跳超时（%s），实际 %d 次：%v", want, timeout, count, snapshot)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPushPresenceNowSendsHeartbeatWithoutWaitingForTheCycle(t *testing.T) {
	var mu sync.Mutex
	requests := make([]string, 0, 4)
	server := newPresenceRecordingServer(t, &mu, &requests)
	defer server.Close()

	service := newPresencePushTestService(t, server.URL)
	service.startPresence()
	defer service.stopPresence()

	// startPresence 会立刻发一次「上线」心跳。
	first := waitForHeartbeatCount(t, &mu, &requests, 1, 3*time.Second)
	if !strings.HasPrefix(first[0], "/presence/heartbeat ") {
		t.Fatalf("上线心跳应打到 /presence/heartbeat，实际 %s", first[0])
	}

	// 关键断言：不推进 45 秒周期，直接补发。
	service.PushPresenceNow()

	second := waitForHeartbeatCount(t, &mu, &requests, 2, 2*time.Second)
	if !strings.Contains(second[1], `"platform":"`+yukihubaccount.PresencePlatformPC+`"`) {
		t.Errorf("补发的心跳也必须带 platform=pc，实际 %s", second[1])
	}
	// activity 必须**始终带上**（空串=清除）：补发时没带就等于告诉服务端「别动」。
	if !strings.Contains(second[1], `"activity"`) {
		t.Errorf("补发的心跳必须带 activity 字段，实际 %s", second[1])
	}
}

// 心跳循环已停（未登录 / 正在退出）时补发必须被丢弃。
//
// 退出路径先 stopPresence 再上报离线；此时若还允许补发，就会把刚报的离线
// 又顶回在线，好友那边要再等 10 分钟才看到你离开 —— NotifyOffline 的注释里
// 已经为「到点的 tick」踩过同一个坑。
func TestPushPresenceNowIsDroppedWhenPresenceLoopIsStopped(t *testing.T) {
	var mu sync.Mutex
	requests := make([]string, 0, 2)
	server := newPresenceRecordingServer(t, &mu, &requests)
	defer server.Close()

	service := newPresencePushTestService(t, server.URL)
	// 不 startPresence：presenceActive 为 false，等价于已退出/未登录。
	for i := 0; i < 5; i++ {
		service.PushPresenceNow()
	}
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 0 {
		t.Fatalf("心跳循环未运行时不得补发，实际发了 %d 次：%v", len(requests), requests)
	}
}

// 突发必须合并：容量 1 的信号槽 + 非阻塞投递。
//
// 退出时逐个结算未完成会话会连着触发多次，契约 §3.2 明确「不要加重试风暴」；
// 同时补发绝不能阻塞调用方（它在「开始游戏」与「退出结算」路径上）。
func TestPushPresenceNowCoalescesBurstsAndNeverBlocks(t *testing.T) {
	service := newPresencePushTestService(t, "http://127.0.0.1:1")
	// 手工摆好状态：只验信号槽语义，不启动循环，避免与消费方赛跑。
	service.presenceActive = true
	service.presencePush = make(chan struct{}, 1)

	for i := 0; i < 50; i++ {
		service.PushPresenceNow()
	}
	if pending := len(service.presencePush); pending != 1 {
		t.Fatalf("50 次补发应合并成 1 次待发信号，实际 %d", pending)
	}

	done := make(chan struct{})
	go func() {
		service.PushPresenceNow()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PushPresenceNow 不得阻塞调用方（信号槽已满时应直接丢弃）")
	}
}
