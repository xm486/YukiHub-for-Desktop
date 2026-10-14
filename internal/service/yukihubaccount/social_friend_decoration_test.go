package yukihubaccount

import (
	"encoding/json"
	"testing"
)

// 好友项的装饰字段：昵称颜色与头像框。
//
// 这两项以前完全没解析，好友列表里的昵称恒为默认色、头像也没有框 ——
// 用户在商店里买的「萌萌点」颜色和头像框在自己好友列表里看不到。
// 对齐手机版 FriendInfo.nameColor / frame（SocialApiClient 解析、FriendsChatDialog 渲染）。
func TestParseFriendDecorations(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		nameColor string
		frameURL  string
	}{
		{
			name:      "驼峰键名",
			body:      `{"id":"1","nickname":"Yuki","nameColor":"#FF88AA","frame":{"key":"sakura","name":"樱花","imageUrl":"/frames/sakura.png","scale":1.5,"offsetX":2,"offsetY":-3}}`,
			nameColor: "#FF88AA",
			frameURL:  "/frames/sakura.png",
		},
		{
			name:      "下划线键名",
			body:      `{"id":"2","nickname":"Yuki","name_color":"#123456","frame":{"image_url":"/frames/star.png"}}`,
			nameColor: "#123456",
			frameURL:  "/frames/star.png",
		},
		{
			// 服务端没戴框时下发 JSON null —— 必须当成「没有」，不能崩也不能造一个空框
			name:      "frame 为 null",
			body:      `{"id":"3","nickname":"Yuki","nameColor":"#AABBCC","frame":null}`,
			nameColor: "#AABBCC",
			frameURL:  "",
		},
		{
			// 残缺数据（只有 key 没有图）不是有效框，手机版 AvatarFrame.isValid() 同规则
			name:      "frame 缺 imageUrl",
			body:      `{"id":"4","nickname":"Yuki","frame":{"key":"sakura","name":"樱花"}}`,
			nameColor: "",
			frameURL:  "",
		},
		{
			name:      "完全没有装饰字段",
			body:      `{"id":"5","nickname":"Yuki"}`,
			nameColor: "",
			frameURL:  "",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal([]byte(testCase.body), &raw); err != nil {
				t.Fatalf("解析测试数据失败: %v", err)
			}

			friend := parseFriend(raw)
			if friend.NameColor != testCase.nameColor {
				t.Errorf("NameColor = %q，期望 %q", friend.NameColor, testCase.nameColor)
			}
			gotFrame := ""
			if friend.Frame != nil {
				gotFrame = friend.Frame.ImageURL
			}
			if gotFrame != testCase.frameURL {
				t.Errorf("Frame.ImageURL = %q，期望 %q", gotFrame, testCase.frameURL)
			}
		})
	}
}

// 头像框解析在三个调用点共用（群聊消息 / 资料页 / 好友项），键名各不相同。
// 这里锁住「按顺序取第一个有效值」的语义：资料页会先试 senderFrame 再试 frame，
// 前者是 JSON null 时必须**继续往后试**，而不是就此放弃。
func TestParseSocialAvatarFramePrefersFirstValidKey(t *testing.T) {
	// senderFrame 是 null、frame 有效 → 必须取到 frame
	profileLike := map[string]any{
		"senderFrame": nil,
		"frame": map[string]any{
			"imageUrl": "/frames/star.png",
		},
	}
	frame := parseSocialAvatarFrame(profileLike, "senderFrame", "frame")
	if frame == nil || frame.ImageURL != "/frames/star.png" {
		t.Fatalf("senderFrame 为 null 时应继续尝试 frame，实际 %+v", frame)
	}

	// 第一个有效就返回，不再看后面的键
	both := map[string]any{
		"senderFrame": map[string]any{"imageUrl": "/frames/first.png"},
		"frame":       map[string]any{"imageUrl": "/frames/second.png"},
	}
	if got := parseSocialAvatarFrame(both, "senderFrame", "frame"); got == nil || got.ImageURL != "/frames/first.png" {
		t.Fatalf("应取第一个有效键，实际 %+v", got)
	}

	// 全都无效 → nil
	if got := parseSocialAvatarFrame(map[string]any{"frame": "not-an-object"}, "frame"); got != nil {
		t.Fatalf("非对象值应被忽略，实际 %+v", got)
	}
	if got := parseSocialAvatarFrame(map[string]any{}, "frame"); got != nil {
		t.Fatalf("缺键应返回 nil，实际 %+v", got)
	}
}
