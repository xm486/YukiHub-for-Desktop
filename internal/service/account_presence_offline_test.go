package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"yukihub/internal/appconf"
	"yukihub/internal/service/yukihubaccount"
)

// 契约：docs/yukihub-presence-platform.md §3.3
//
// 「退出登录 / **关闭客户端**时尽力调用一次下线」。之前只有退出登录会调，
// 关了客户端要等服务端 10 分钟无心跳才判离线 —— 好友那边就一直显示你在线。
//
// 这组测试钉死两件事：没登录时一个请求都不发；登录时要发且必须带 platform=pc
// （服务端对空值兜底成 android，漏传会把电脑端记录冲成手机）。
func TestNotifyOfflineReportsOnlyWhenLoggedIn(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	requests := make([]string, 0, 2)
	server := newPresenceRecordingServer(t, &mu, &requests)
	defer server.Close()

	// 没登录：静默返回，不发请求（否则每次退出都会打一个 401）。
	loggedOut := &AccountService{
		config: &appconf.AppConfig{},
		client: yukihubaccount.NewClient(server.URL),
	}
	if err := loggedOut.NotifyOffline(context.Background()); err != nil {
		t.Fatalf("未登录时应静默返回，实际报错：%v", err)
	}
	mu.Lock()
	count := len(requests)
	mu.Unlock()
	if count != 0 {
		t.Fatalf("未登录时不应发请求，实际发了 %d 次", count)
	}

	// 已登录：发一次，带 platform。
	loggedIn := &AccountService{
		config: &appconf.AppConfig{YukiHubAccountAccessToken: "token-1"},
		client: yukihubaccount.NewClient(server.URL),
	}
	if err := loggedIn.NotifyOffline(context.Background()); err != nil {
		t.Fatalf("NotifyOffline: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("请求次数 = %d, want 1（%v）", len(requests), requests)
	}
	request := requests[0]
	if !strings.HasPrefix(request, "/presence/offline ") {
		t.Errorf("应打到 /presence/offline，实际 %s", request)
	}
	if !strings.Contains(request, `"platform":"`+yukihubaccount.PresencePlatformPC+`"`) {
		t.Errorf("离线上报必须带 platform=pc，实际 %s", request)
	}
}

// TestNotifyOfflineStopsPresenceLoopFirst 上报离线前必须先停心跳循环。
//
// 否则一个刚好到点的 tick 会把状态又顶回在线，好友那边要再过 10 分钟
// 才看到你离开 —— 等于这次上报白做。
func TestNotifyOfflineStopsPresenceLoopFirst(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	requests := make([]string, 0, 2)
	server := newPresenceRecordingServer(t, &mu, &requests)
	defer server.Close()

	accountService := &AccountService{
		config:         &appconf.AppConfig{YukiHubAccountAccessToken: "token-1"},
		client:         yukihubaccount.NewClient(server.URL),
		presenceActive: true,
	}
	_, cancel := context.WithCancel(context.Background())
	accountService.presenceCancel = cancel

	if err := accountService.NotifyOffline(context.Background()); err != nil {
		t.Fatalf("NotifyOffline: %v", err)
	}

	if accountService.presenceCancel != nil || accountService.presenceActive {
		t.Error("上报离线前应先停掉心跳循环（presenceCancel/presenceActive 仍是活的状态）")
	}
}

// newPresenceRecordingServer 起一个把请求行（路径 + body）记下来的测试服务器。
func newPresenceRecordingServer(
	t *testing.T,
	mu *sync.Mutex,
	requests *[]string,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		*requests = append(*requests, r.URL.Path+" "+string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"status":"offline"}`))
	}))
}
