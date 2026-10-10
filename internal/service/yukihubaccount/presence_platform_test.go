package yukihubaccount

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// 契约文档：docs/yukihub-presence-platform.md
//
// 这一组测试钉死在线状态的**平台标识**：
// 服务端对空值兜底成 android（为了兼容还没上报该字段的老版本 App），
// 所以电脑端只要漏传 platform，好友那边就会把「在电脑上」显示成「手机在线」。
// 谁把 platform 从心跳里删掉，这些测试会立刻失败。

const okPresenceBody = `{"success":true,"status":"online","timestamp":"2026-10-10T21:30:00+08:00"}`

// TestHeartbeatReportsPCPlatform 心跳必须带 platform=pc。
func TestHeartbeatReportsPCPlatform(t *testing.T) {
	t.Parallel()

	server, requests, mu := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, okPresenceBody)
	})

	client := NewClient(server.URL)
	if err := client.Heartbeat(context.Background(), "token-1", PresenceOnline, "正在玩：CLANNAD"); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*requests) != 1 {
		t.Fatalf("请求次数 = %d, want 1", len(*requests))
	}
	request := (*requests)[0]
	assertJSONRequest(t, request, http.MethodPost, "/presence/heartbeat")
	if !strings.Contains(request.Body, `"platform":"`+PresencePlatformPC+`"`) {
		t.Errorf("心跳体里必须带 platform=%q，实际 body = %s", PresencePlatformPC, request.Body)
	}
	if !strings.Contains(request.Body, `"status":"`+PresenceOnline+`"`) {
		t.Errorf("心跳体里应带 status，实际 body = %s", request.Body)
	}
	// activity 必须始终带上（空串表示清除）。
	if !strings.Contains(request.Body, `"activity":"`) {
		t.Errorf("心跳体里应始终带 activity，实际 body = %s", request.Body)
	}
	// 离线走专用接口，心跳里不能出现 offline。
	if strings.Contains(request.Body, `"status":"`+PresenceOffline+`"`) {
		t.Errorf("心跳里不应传 status=offline，实际 body = %s", request.Body)
	}
}

// TestHeartbeatSendsEmptyActivityForClearing 没有在玩游戏时 activity 传空串，
// 而不是省略字段（省略会让服务端保留上一次的「正在玩」）。
func TestHeartbeatSendsEmptyActivityForClearing(t *testing.T) {
	t.Parallel()

	server, requests, mu := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, okPresenceBody)
	})

	client := NewClient(server.URL)
	if err := client.Heartbeat(context.Background(), "token-1", PresenceOnline, ""); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if body := (*requests)[0].Body; !strings.Contains(body, `"activity":""`) {
		t.Errorf("activity 应传空串，实际 body = %s", body)
	}
}

// TestMarkOfflineReportsPCPlatform 离线上报同样要带 platform，别把平台冲掉。
func TestMarkOfflineReportsPCPlatform(t *testing.T) {
	t.Parallel()

	server, requests, mu := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"success":true,"status":"offline"}`)
	})

	client := NewClient(server.URL)
	if err := client.MarkOffline(context.Background(), "token-1"); err != nil {
		t.Fatalf("MarkOffline: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	request := (*requests)[0]
	assertJSONRequest(t, request, http.MethodPost, "/presence/offline")
	if !strings.Contains(request.Body, `"platform":"`+PresencePlatformPC+`"`) {
		t.Errorf("离线上报体里必须带 platform=%q，实际 body = %s", PresencePlatformPC, request.Body)
	}
}

// TestParseFriendKeepsPlatform 好友列表把 platform 原样带出来（不在这里做兜底，
// 归一化交给展示层，界面上离线的好友本来就不显示平台图标）。
func TestParseFriendKeepsPlatform(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{name: "电脑", raw: map[string]any{"uid": float64(1), "status": "online", "platform": "pc"}, want: "pc"},
		{name: "手机", raw: map[string]any{"uid": float64(1), "status": "online", "platform": "android"}, want: "android"},
		{name: "网页", raw: map[string]any{"uid": float64(1), "status": "online", "platform": "web"}, want: "web"},
		{name: "离线下发空串", raw: map[string]any{"uid": float64(1), "status": "offline", "platform": ""}, want: ""},
		{name: "老服务端没有该字段", raw: map[string]any{"uid": float64(1), "status": "online"}, want: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := parseFriend(testCase.raw).Platform; got != testCase.want {
				t.Errorf("parseFriend().Platform = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestUserProfileParsesPlatform 个人主页的顶层 platform 要解析出来。
func TestUserProfileParsesPlatform(t *testing.T) {
	t.Parallel()

	server, _, _ := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"uid":123,"nickname":"Yuki","status":"online","platform":"pc"}`)
	})

	client := NewClient(server.URL)
	profile, err := client.UserProfile(context.Background(), "token-1", 123)
	if err != nil {
		t.Fatalf("UserProfile: %v", err)
	}
	if profile.Platform != PresencePlatformPC {
		t.Errorf("UserProfile().Platform = %q, want %q", profile.Platform, PresencePlatformPC)
	}
}
