package yukihubaccount

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// errEmptyImageURL 表示图片上传接口没有返回可用地址。
var errEmptyImageURL = errors.New("图片上传没有返回地址")

// 消息类型（与手机版一致）。
const (
	MsgTypeText  = "text"
	MsgTypeEmoji = "emoji"
	MsgTypeImage = "image"
)

// 在线状态。
const (
	PresenceOnline  = "online"
	PresenceOffline = "offline"
)

// 设备平台标识（服务端 `user_presence.platform`）。
//
// 只认这三个小写值；服务端对空值兜底成 android（为了不破坏还没上报该字段的
// 老版本 App），所以**电脑端每次心跳都必须带 PresencePlatformPC**，
// 否则用户在电脑上登录、好友那边看到的是「手机在线」。
const (
	PresencePlatformAndroid = "android"
	PresencePlatformPC      = "pc"
	PresencePlatformWeb     = "web"
)

// Friend 是好友列表里的一项。
type Friend struct {
	ID        string `json:"id"`
	UID       int64  `json:"uid"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar,omitempty"`
	Signature string `json:"signature,omitempty"`
	// Status：online / away / offline（服务端按心跳时间判定，90s 内 online，
	// 90~300s away，超过 300s offline）
	Status string `json:"status,omitempty"`
	// Platform 是对方最近一次心跳的设备：android / pc / web。
	// 好友列表只在对方在线时才由服务端下发该字段；空串/未知值展示时按 android 兜底。
	Platform string `json:"platform,omitempty"`
	// Activity 是「正在玩：xxx」，对方关闭分享时为空
	Activity      string `json:"activity,omitempty"`
	Note          string `json:"note,omitempty"`
	LastMessage   string `json:"lastMessage,omitempty"`
	LastMessageAt string `json:"lastMessageAt,omitempty"`
	UnreadCount   int    `json:"unreadCount"`
	// FriendStatus：none（不是好友）/ pending（申请中）/ accepted（已是好友）。
	// 只有搜索结果会下发 —— 手机版据此把「加好友」按钮换成「已发送请求 / 已是好友」
	// （FriendsChatDialog.renderSearchResults）。以前这里没解析，界面就只能一直显示
	// 一个还能点的「加好友」，点了服务端也不理。
	FriendStatus string `json:"friendStatus,omitempty"`
	// FriendDirection 仅在 pending 时有意义：received（对方申请我）/ sent（我申请的）
	FriendDirection string `json:"friendDirection,omitempty"`
}

// FriendRequest 是一条好友申请。
//
// FriendshipID 是**数字**（手机版 `r.optInt("friendshipId", 0)`，接受/拒绝也按数字
// 发回服务端）。以前这里写成 string，`pickString` 读数字字段会拿到空串，
// 于是「接受」按钮发出去的 friendshipId 是空 —— 点了没反应。
type FriendRequest struct {
	FriendshipID int64 `json:"friendshipId,omitempty"`
	// UID 是申请里的**对方** uid：收到的申请取 fromUid，发出的取 toUid
	UID       int64  `json:"uid,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	Signature string `json:"signature,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	// Outgoing 为 true 表示这是「我发出去的」申请
	Outgoing bool `json:"outgoing,omitempty"`
}

// FriendRequests 是 `/friends/requests` 的结果：收到的 + 已发出的。
//
// **注意**：`/friends/list` 里那个 `pendingRequests` 只是一个**数字**（待处理条数，
// 手机版 `root.optInt("pendingRequests", 0)` 就是这么读的），真正的申请内容在这个
// 接口里。以前把 list 里的字段当数组解析，所以「有人加我」在界面上永远显示不出来。
type FriendRequests struct {
	Incoming []FriendRequest `json:"incoming,omitempty"`
	Outgoing []FriendRequest `json:"outgoing,omitempty"`
}

// FriendList 是好友列表 + 待处理申请数。
type FriendList struct {
	Friends         []Friend        `json:"friends"`
	PendingRequests []FriendRequest `json:"pendingRequests,omitempty"`
	PendingCount    int             `json:"pendingCount"`
}

// ChatMessage 是一条聊天消息（私聊或群聊共用）。
type ChatMessage struct {
	ID           string `json:"id"`
	SenderID     string `json:"senderId,omitempty"`
	SenderUID    int64  `json:"senderUid"`
	SenderName   string `json:"senderName,omitempty"`
	SenderAvatar string `json:"senderAvatar,omitempty"`
	ReceiverID   string `json:"receiverId,omitempty"`
	GroupID      string `json:"groupId,omitempty"`
	Content      string `json:"content"`
	MsgType      string `json:"msgType"`
	CreatedAt    string `json:"createdAt,omitempty"`
	IsMine       bool   `json:"isMine"`
	ReplyToID    string `json:"replyToId,omitempty"`
	// 群聊里服务端可能会带上被回复的消息摘要
	ReplyPreview string `json:"replyPreview,omitempty"`

	// ===== 群聊专属字段（对齐手机版 GroupMessage）=====
	// SenderIsAdmin / SenderLevel / SenderNameColor / SenderFrame 只由群聊历史与
	// 轮询接口下发；私聊拿不到（手机版私聊也不画这些装饰）。
	SenderIsAdmin   bool         `json:"senderIsAdmin,omitempty"`
	SenderLevel     int          `json:"senderLevel,omitempty"`
	SenderNameColor string       `json:"senderNameColor,omitempty"`
	SenderFrame     *AvatarFrame `json:"senderFrame,omitempty"`
}

// AvatarFrame 是头像框（服务端下发的是图片地址 + 相对头像边长的位置参数）。
//
// 换算公式与手机版 AvatarFrame 一致：框边长 = 头像边长 × scale，
// 横/纵向位移 = 头像边长 × offset / 100。
type AvatarFrame struct {
	Key      string  `json:"key,omitempty"`
	Name     string  `json:"name,omitempty"`
	ImageURL string  `json:"imageUrl,omitempty"`
	Scale    float64 `json:"scale,omitempty"`
	OffsetX  float64 `json:"offsetX,omitempty"`
	OffsetY  float64 `json:"offsetY,omitempty"`
}

// ChatGroup 是一个群聊。
type ChatGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	// Icon 是群的 emoji 图标（手机版 GroupInfo.icon，默认 🏛）。
	Icon string `json:"icon,omitempty"`
	// Type 是群类型：chat=聊天室、notice=公告版（全体禁言，仅管理员可发言）。
	Type string `json:"type,omitempty"`
	// MemberRole 是当前用户在该群的角色：admin / member。
	MemberRole  string `json:"memberRole,omitempty"`
	MemberCount int    `json:"memberCount"`
	OnlineCount int    `json:"onlineCount"`
	UnreadCount int    `json:"unreadCount"`
}

// ChatEmoji 是一个本站表情。
type ChatEmoji struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ==================== 好友 ====================

// ListFriends 拉好友列表与待处理申请。
func (c *Client) ListFriends(ctx context.Context, token string) (FriendList, error) {
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/friends/list", token, nil)
	if err != nil {
		return FriendList{}, err
	}

	// 兼容 {friends:[...], pendingRequests:[...]} 与 {data:{...}} 两种外层
	payload := body
	if data, ok := body["data"].(map[string]any); ok {
		if _, hasFriends := data["friends"]; hasFriends {
			payload = data
		}
	}

	result := FriendList{}
	for _, raw := range toMapSlice(payload["friends"]) {
		result.Friends = append(result.Friends, parseFriend(raw))
	}
	// 有的服务端版本会把申请数组塞在 list 里，顺手也解析掉
	for _, raw := range toMapSlice(payload["pendingRequests"]) {
		result.PendingRequests = append(result.PendingRequests, parseFriendRequest(raw, false))
	}
	// 待处理数：现在服务端把 `pendingRequests` 当**数字**给（手机版就是这么读的），
	// 所以它必须在这个别名列表里，否则「有人加我」的角标永远是 0。
	result.PendingCount = int(pickInt64(
		payload, "pendingCount", "pendingRequests", "totalPending", "requestCount", "total",
	))
	if result.PendingCount == 0 && len(result.PendingRequests) > 0 {
		result.PendingCount = len(result.PendingRequests)
	}
	return result, nil
}

// ListFriendRequests 拉取好友申请：收到的（incoming）与已发出的（outgoing）。
func (c *Client) ListFriendRequests(ctx context.Context, token string) (FriendRequests, error) {
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/friends/requests", token, nil)
	if err != nil {
		return FriendRequests{}, err
	}

	// 兼容 {incoming,outgoing} 与 {data:{incoming,outgoing}} 两种外层
	payload := body
	if data, ok := body["data"].(map[string]any); ok {
		if _, has := data["incoming"]; has {
			payload = data
		} else if _, has := data["outgoing"]; has {
			payload = data
		}
	}

	result := FriendRequests{}
	for _, raw := range toMapSlice(payload["incoming"]) {
		result.Incoming = append(result.Incoming, parseFriendRequest(raw, false))
	}
	for _, raw := range toMapSlice(payload["outgoing"]) {
		result.Outgoing = append(result.Outgoing, parseFriendRequest(raw, true))
	}
	return result, nil
}

// parseFriendRequest 解析一条好友申请。
//
// 两套别名都要列：服务端对「收到」和「发出」用的是不同的键
// （incoming 给 fromUid，outgoing 给 toUid），手机版 FriendsChatDialog.renderRequests
// 也是分开读的。漏一项就会把对方显示成 UID 0。
func parseFriendRequest(raw map[string]any, outgoing bool) FriendRequest {
	uidAliases := []string{"fromUid", "uid", "userId", "friendUid", "toUid"}
	if outgoing {
		uidAliases = []string{"toUid", "uid", "userId", "friendUid", "fromUid"}
	}
	return FriendRequest{
		FriendshipID: pickInt64(raw, "friendshipId", "friendship_id", "id"),
		UID:          pickInt64(raw, uidAliases...),
		Nickname:     pickString(raw, "nickname", "name", "fromNickname", "toNickname"),
		Avatar:       pickString(raw, "avatarUrl", "avatar_url", "avatar"),
		Signature:    pickString(raw, "signature"),
		CreatedAt:    pickString(raw, "createdAt", "created_at"),
		Outgoing:     outgoing,
	}
}

// SearchUsers 按关键词搜用户（返回的是好友列表同构的条目）。
func (c *Client) SearchUsers(ctx context.Context, token, keyword string) ([]Friend, error) {
	query := url.Values{"q": {strings.TrimSpace(keyword)}}
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/friends/search?"+query.Encode(), token, nil)
	if err != nil {
		return nil, err
	}
	raw := body["results"]
	if data, ok := body["data"].(map[string]any); ok {
		if _, has := data["results"]; has {
			raw = data["results"]
		}
	}
	results := make([]Friend, 0)
	for _, item := range toMapSlice(raw) {
		results = append(results, parseFriend(item))
	}
	return results, nil
}

// SendFriendRequest 发送好友申请。target 可以是 uid 或昵称。
func (c *Client) SendFriendRequest(ctx context.Context, token, target string) error {
	payload := map[string]string{"target": strings.TrimSpace(target)}
	return c.doEmpty(ctx, http.MethodPost, c.baseURL+"/friends/request", token, payload)
}

// AcceptFriendRequest 接受好友申请。
func (c *Client) AcceptFriendRequest(ctx context.Context, token string, friendshipID int64, uid int64) error {
	payload := map[string]any{}
	if friendshipID > 0 {
		payload["friendshipId"] = friendshipID
	}
	// 资料页那条路只拿得到 uid（手机版 acceptFriendRequestByUid 同样只发 uid）
	if uid > 0 {
		payload["uid"] = uid
	}
	return c.doEmpty(ctx, http.MethodPost, c.baseURL+"/friends/accept", token, payload)
}

// RejectFriendRequest 拒绝好友申请。
func (c *Client) RejectFriendRequest(ctx context.Context, token string, friendshipID int64) error {
	payload := map[string]any{"friendshipId": friendshipID}
	return c.doEmpty(ctx, http.MethodPost, c.baseURL+"/friends/reject", token, payload)
}

// RemoveFriend 删除好友。
func (c *Client) RemoveFriend(ctx context.Context, token, friendID string) error {
	payload := map[string]string{"friendId": strings.TrimSpace(friendID)}
	return c.doEmpty(ctx, http.MethodPost, c.baseURL+"/friends/remove", token, payload)
}

// SetFriendNote 设置好友备注。
func (c *Client) SetFriendNote(ctx context.Context, token, friendID, note string) error {
	payload := map[string]string{"friendId": strings.TrimSpace(friendID), "note": note}
	return c.doEmpty(ctx, http.MethodPost, c.baseURL+"/friends/note", token, payload)
}

// ==================== 私聊 ====================

// SendChatMessage 发私聊消息，返回落库后的消息（可能为空）。
func (c *Client) SendChatMessage(ctx context.Context, token, receiverID, content, msgType, replyToID string) (ChatMessage, error) {
	if strings.TrimSpace(msgType) == "" {
		msgType = MsgTypeText
	}
	payload := map[string]any{
		"receiverId": strings.TrimSpace(receiverID),
		"content":    content,
		"msgType":    msgType,
	}
	if strings.TrimSpace(replyToID) != "" {
		payload["replyToId"] = strings.TrimSpace(replyToID)
	}
	body, err := c.doJSON(ctx, http.MethodPost, c.baseURL+"/chat/send", token, payload)
	if err != nil {
		return ChatMessage{}, err
	}
	if message, ok := body["message"].(map[string]any); ok {
		return parseChatMessage(message), nil
	}
	return ChatMessage{}, nil
}

// ChatHistory 拉与某人的历史消息。
func (c *Client) ChatHistory(ctx context.Context, token, friendID string, offset, limit int) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 30
	}
	query := url.Values{
		"friendId": {strings.TrimSpace(friendID)},
		"offset":   {strconv.Itoa(offset)},
		"limit":    {strconv.Itoa(limit)},
	}
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/chat/history?"+query.Encode(), token, nil)
	if err != nil {
		return nil, err
	}
	return parseMessages(body["messages"]), nil
}

// ChatPoll 拉新消息（afterID 之后）。friendID 为空时拉所有会话的新消息。
func (c *Client) ChatPoll(ctx context.Context, token, afterID, friendID string, peek bool) ([]ChatMessage, error) {
	query := url.Values{"afterId": {strings.TrimSpace(afterID)}}
	if strings.TrimSpace(friendID) != "" {
		query.Set("friendId", strings.TrimSpace(friendID))
	}
	if peek {
		query.Set("peek", "1")
	}
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/chat/poll?"+query.Encode(), token, nil)
	if err != nil {
		return nil, err
	}
	return parseMessages(body["messages"]), nil
}

// UnreadTotal 返回未读消息总数。
func (c *Client) UnreadTotal(ctx context.Context, token string) (int, error) {
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/chat/unread", token, nil)
	if err != nil {
		return 0, err
	}
	return int(pickInt64(body, "totalUnread", "total", "count")), nil
}

// ListEmojis 拉本站表情列表。
func (c *Client) ListEmojis(ctx context.Context, token string) ([]ChatEmoji, error) {
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/chat/emojis", token, nil)
	if err != nil {
		return nil, err
	}
	emojis := make([]ChatEmoji, 0)
	for _, raw := range toMapSlice(body["emojis"]) {
		emojis = append(emojis, ChatEmoji{
			Name: pickString(raw, "name"),
			URL:  pickString(raw, "url"),
		})
	}
	return emojis, nil
}

// UploadChatImage 上传聊天图片，返回可直接放进消息 content 的地址。
func (c *Client) UploadChatImage(ctx context.Context, token, contentType string, data []byte) (string, error) {
	if strings.TrimSpace(contentType) == "" {
		contentType = "image/jpeg"
	}
	body, err := c.doRaw(ctx, http.MethodPost, c.baseURL+"/chat/upload_image", token, contentType, data)
	if err != nil {
		return "", err
	}
	imageURL := pickString(body, "url", "imageUrl", "image_url")
	if imageURL == "" {
		return "", errEmptyImageURL
	}
	return imageURL, nil
}

// UserProfile 是用户资料页的数据（GET /user/profile）。
//
// 字段名与手机版 renderUserProfile 的读取保持一致（avatarUrl / totalGames /
// totalPlayTime / activity）。
type UserProfile struct {
	UID           int64  `json:"uid"`
	Nickname      string `json:"nickname"`
	Signature     string `json:"signature,omitempty"`
	Avatar        string `json:"avatar,omitempty"`
	Status        string `json:"status,omitempty"`
	Platform      string `json:"platform,omitempty"` // android / pc / web；对方离线时为空串
	Activity      string `json:"activity,omitempty"`
	TotalGames    int    `json:"totalGames"`
	TotalPlayTime int64  `json:"totalPlayTime"`
	// Level 是社区等级（手机版资料页在昵称旁挂 Lv.N 徽章）。
	Level int `json:"level"`
	// FriendSince 是「成为好友」的时间文案（服务端可能直接下发格式化结果）。
	FriendSince string `json:"friendSince,omitempty"`
	// FriendStatus：accepted（已是好友）/ pending（申请中）/ none。
	// FriendDirection 仅在 pending 时有意义：received（对方申请我）/ sent。
	FriendStatus    string           `json:"friendStatus,omitempty"`
	FriendDirection string           `json:"friendDirection,omitempty"`
	RecentGames     []UserRecentGame `json:"recentGames,omitempty"`
	Frame           *AvatarFrame     `json:"frame,omitempty"`
	// PlayingGame / PlayingStartedAt 是「正在玩」的结构化形式。
	//
	// 服务端当前只下发 activity 这一句拼好的文案（"正在玩：xxx"），没有开始
	// 时间，客户端算不出「已玩多久」。这两个字段先按别名列表预留：服务端补上
	// 之后资料卡立刻能显示实时时长，不需要再改客户端。
	PlayingGame      string `json:"playingGame,omitempty"`
	PlayingStartedAt int64  `json:"playingStartedAt,omitempty"`
}

// UserRecentGame 是资料页「最近游玩」里的一条。
type UserRecentGame struct {
	Title        string `json:"title"`
	PlayTime     int64  `json:"playTime"`
	LastPlayedAt int64  `json:"lastPlayedAt"`
}

// UserProfile 拉取指定 UID 的用户资料（点头像进资料页用）。
func (c *Client) UserProfile(ctx context.Context, token string, uid int64) (UserProfile, error) {
	query := url.Values{"uid": {strconv.FormatInt(uid, 10)}}
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/user/profile?"+query.Encode(), token, nil)
	if err != nil {
		return UserProfile{}, err
	}
	profile := UserProfile{
		UID:           pickInt64(body, "uid"),
		Nickname:      pickString(body, "nickname"),
		Signature:     pickString(body, "signature"),
		Avatar:        pickString(body, "avatarUrl", "avatar_url", "avatar"),
		Status:        pickString(body, "status"),
		Platform:      pickString(body, "platform"),
		Activity:      pickString(body, "activity"),
		TotalGames:    int(pickInt64(body, "totalGames", "total_games")),
		TotalPlayTime: pickInt64(body, "totalPlayTime", "total_play_time"),
		Level:         int(pickInt64(body, "level")),
		FriendSince:   pickString(body, "friendSince", "friend_since"),
		FriendStatus:  pickString(body, "friendStatus", "friend_status"),
		FriendDirection: pickString(
			body, "friendDirection", "friend_direction",
		),
		// 「正在玩」的结构化字段：认驼峰与下划线两种键名，另外容忍 playStartedAt
		// / startedAt 这类同义命名，服务端用哪个都能取到。
		PlayingGame: pickString(
			body, "playingGame", "playing_game", "currentGame", "current_game",
		),
		PlayingStartedAt: pickInt64(
			body,
			"playingStartedAt", "playing_started_at",
			"playStartedAt", "play_started_at",
			"activityStartedAt", "activity_started_at",
			"startedAt", "started_at",
		),
	}
	// 最近游玩：手机版读 title / playTime / lastPlayedAt
	for _, item := range toMapSlice(body["recentGames"]) {
		title := pickString(item, "title", "name")
		if strings.TrimSpace(title) == "" {
			continue
		}
		profile.RecentGames = append(profile.RecentGames, UserRecentGame{
			Title:        title,
			PlayTime:     pickInt64(item, "playTime", "play_time"),
			LastPlayedAt: pickInt64(item, "lastPlayedAt", "last_played_at"),
		})
	}
	if profile.UID == 0 {
		profile.UID = uid
	}
	// 资料页的头像框字段名以 senderFrame 同款结构下发；两种键名都容忍。
	for _, key := range []string{"senderFrame", "frame"} {
		if frameRaw, ok := body[key].(map[string]any); ok {
			imageURL := pickString(frameRaw, "imageUrl", "image_url", "image")
			if strings.TrimSpace(imageURL) == "" {
				continue
			}
			profile.Frame = &AvatarFrame{
				Key:      pickString(frameRaw, "key"),
				Name:     pickString(frameRaw, "name"),
				ImageURL: imageURL,
				Scale:    pickFloat64(frameRaw, "scale"),
				OffsetX:  pickFloat64(frameRaw, "offsetX", "offset_x"),
				OffsetY:  pickFloat64(frameRaw, "offsetY", "offset_y"),
			}
			break
		}
	}
	return profile, nil
}

// ReportChatMessage 举报一条聊天消息（POST /community/report）。
//
// scene 取 "chat"（私聊）或 "group"（群聊）；scene=group 时 groupID 必填。
func (c *Client) ReportChatMessage(ctx context.Context, token, scene, messageID, groupID, reason string) error {
	payload := map[string]string{
		"scene":     strings.TrimSpace(scene),
		"messageId": strings.TrimSpace(messageID),
		"reason":    strings.TrimSpace(reason),
	}
	if strings.TrimSpace(groupID) != "" {
		payload["groupId"] = strings.TrimSpace(groupID)
	}
	return c.doEmpty(ctx, http.MethodPost, c.baseURL+"/community/report", token, payload)
}

// ==================== 未萌贴纸（服务端代理，密钥在服务端） ====================

// ListStickerPacks 拉未萌贴纸包列表。
//
// 服务端对 NextMoe 做缓存代理，enabled=false 表示服务未启用（界面隐藏入口）。
// 首次访问（缓存冷）服务端要现查上游，放宽读超时到 40 秒（与手机版一致）。
// 返回 nil packs = 服务未启用。
func (c *Client) ListStickerPacks(ctx context.Context, token string) (bool, []map[string]any, error) {
	query := url.Values{"action": {"packs"}, "page": {"1"}}
	body, err := c.doRawWithTimeout(ctx, http.MethodGet, c.baseURL+"/chat/nextmoe_stickers.php?"+query.Encode(), token, "", nil, 40*time.Second)
	if err != nil {
		return false, nil, err
	}
	if !pickBool(body, "enabled") {
		return false, nil, nil
	}
	return true, toMapSlice(body["packs"]), nil
}

// ListStickerURLs 拉某个贴纸包里的全部表情地址（320px webp，可直接作为消息 URL）。
//
// 服务端返回 [{id,url},...] 对象数组，容忍纯字符串数组（防御式，与手机版一致）。
func (c *Client) ListStickerURLs(ctx context.Context, token, packID string) ([]string, error) {
	query := url.Values{"action": {"pack"}, "pack_id": {strings.TrimSpace(packID)}}
	body, err := c.doRawWithTimeout(ctx, http.MethodGet, c.baseURL+"/chat/nextmoe_stickers.php?"+query.Encode(), token, "", nil, 40*time.Second)
	if err != nil {
		return nil, err
	}
	if !pickBool(body, "enabled") {
		return nil, nil
	}
	urls := make([]string, 0)
	if typed, ok := body["stickers"].([]any); ok {
		for _, item := range typed {
			switch entry := item.(type) {
			case map[string]any:
				if value := pickString(entry, "url"); value != "" {
					urls = append(urls, value)
				}
			case string:
				if entry != "" {
					urls = append(urls, entry)
				}
			}
		}
	}
	return urls, nil
}

// ==================== 群聊 ====================

// ListGroups 拉群列表。
func (c *Client) ListGroups(ctx context.Context, token string) ([]ChatGroup, error) {
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/groups/list", token, nil)
	if err != nil {
		return nil, err
	}
	groups := make([]ChatGroup, 0)
	for _, raw := range toMapSlice(body["groups"]) {
		groups = append(groups, parseGroup(raw))
	}
	return groups, nil
}

// GroupHistory 拉群历史消息。
func (c *Client) GroupHistory(ctx context.Context, token, groupID string, offset, limit int) ([]ChatMessage, int, error) {
	if limit <= 0 {
		limit = 30
	}
	query := url.Values{
		"groupId": {strings.TrimSpace(groupID)},
		"offset":  {strconv.Itoa(offset)},
		"limit":   {strconv.Itoa(limit)},
	}
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/groups/messages?"+query.Encode(), token, nil)
	if err != nil {
		return nil, 0, err
	}
	return parseMessages(body["messages"]), int(pickInt64(body, "onlineCount")), nil
}

// SendGroupMessage 发群消息。
func (c *Client) SendGroupMessage(ctx context.Context, token, groupID, content, msgType, replyToID string) (ChatMessage, error) {
	if strings.TrimSpace(msgType) == "" {
		msgType = MsgTypeText
	}
	payload := map[string]any{
		"groupId": strings.TrimSpace(groupID),
		"content": content,
		"msgType": msgType,
	}
	if strings.TrimSpace(replyToID) != "" {
		payload["replyToId"] = strings.TrimSpace(replyToID)
	}
	body, err := c.doJSON(ctx, http.MethodPost, c.baseURL+"/groups/send", token, payload)
	if err != nil {
		return ChatMessage{}, err
	}
	if message, ok := body["message"].(map[string]any); ok {
		return parseChatMessage(message), nil
	}
	return ChatMessage{}, nil
}

// PollGroup 拉群新消息，同时返回当前在线人数。
func (c *Client) PollGroup(ctx context.Context, token, groupID, afterID string) ([]ChatMessage, int, error) {
	query := url.Values{
		"groupId": {strings.TrimSpace(groupID)},
		"afterId": {strings.TrimSpace(afterID)},
	}
	body, err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/groups/poll?"+query.Encode(), token, nil)
	if err != nil {
		return nil, 0, err
	}
	return parseMessages(body["messages"]), int(pickInt64(body, "onlineCount")), nil
}

// Action 是群消息管理动作（撤回 / 删除）。
type GroupMessageAction string

const (
	GroupActionRecall GroupMessageAction = "recall"
	GroupActionDelete GroupMessageAction = "delete"
)

// ManageGroupMessage 撤回或删除群消息。
func (c *Client) ManageGroupMessage(ctx context.Context, token, messageID string, action GroupMessageAction) error {
	payload := map[string]string{
		"messageId": strings.TrimSpace(messageID),
		"action":    string(action),
	}
	return c.doEmpty(ctx, http.MethodPost, c.baseURL+"/groups/manage", token, payload)
}

// ==================== 解析辅助 ====================

func parseFriend(raw map[string]any) Friend {
	friend := Friend{
		ID:            pickString(raw, "id", "friendId", "friend_id", "userId"),
		UID:           pickInt64(raw, "uid", "userId"),
		Nickname:      pickString(raw, "nickname", "name"),
		Avatar:        pickString(raw, "avatarUrl", "avatar_url", "avatar"),
		Signature:     pickString(raw, "signature"),
		Status:        pickString(raw, "status"),
		Platform:      pickString(raw, "platform"),
		Activity:      pickString(raw, "activity"),
		Note:          pickString(raw, "note"),
		LastMessage:   pickString(raw, "lastMessage", "last_message"),
		LastMessageAt: pickString(raw, "lastMessageAt", "last_message_at", "updatedAt"),
		UnreadCount:   int(pickInt64(raw, "unreadCount", "unread")),

		FriendStatus:    pickString(raw, "friendStatus", "friend_status"),
		FriendDirection: pickString(raw, "friendDirection", "friend_direction"),
	}
	// 有些实现把 uid 放在嵌套的 user 里
	if friend.UID == 0 || friend.Nickname == "" {
		if nested, ok := raw["user"].(map[string]any); ok {
			if friend.UID == 0 {
				friend.UID = pickInt64(nested, "uid", "id")
			}
			if friend.Nickname == "" {
				friend.Nickname = pickString(nested, "nickname", "name")
			}
			if friend.Avatar == "" {
				friend.Avatar = pickString(nested, "avatarUrl", "avatar_url", "avatar")
			}
		}
	}
	return friend
}

func parseChatMessage(raw map[string]any) ChatMessage {
	message := ChatMessage{
		ID:        pickString(raw, "id", "messageId"),
		SenderID:  pickString(raw, "senderId", "sender_id"),
		SenderUID: pickInt64(raw, "senderUid", "sender_uid", "uid"),
		// 群聊接口下发的字段名是 senderNickname（手机版 GroupMessage 同名字段）；
		// 漏掉它会让群聊里所有昵称退化成「UID xxx」。
		SenderName: pickString(
			raw,
			"senderNickname", "sender_nickname",
			"senderName", "sender_name",
			"nickname",
		),
		SenderAvatar: pickString(raw, "senderAvatar", "sender_avatar", "avatarUrl", "avatar"),
		ReceiverID:   pickString(raw, "receiverId", "receiver_id"),
		GroupID:      pickString(raw, "groupId", "group_id"),
		Content:      pickString(raw, "content"),
		MsgType:      pickString(raw, "msgType", "msg_type", "type"),
		CreatedAt:    pickString(raw, "createdAt", "created_at", "time"),
		IsMine:       pickBool(raw, "isMine", "is_mine"),
		ReplyToID:    pickString(raw, "replyToId", "reply_to_id"),
		ReplyPreview: pickString(raw, "replyPreview", "reply_preview", "replyContent"),

		SenderIsAdmin:   pickBool(raw, "senderIsAdmin", "sender_is_admin"),
		SenderLevel:     int(pickInt64(raw, "senderLevel", "sender_level")),
		SenderNameColor: pickString(raw, "senderNameColor", "sender_name_color"),
	}
	// senderFrame：JSON null / 缺失都表示「没戴框」（桌面端每次都实时拉取，
	// 不做手机版那种「确认摘下」的覆盖语义，所以不需要哨兵值）。
	if frameRaw, ok := raw["senderFrame"].(map[string]any); ok {
		frame := &AvatarFrame{
			Key:      pickString(frameRaw, "key"),
			Name:     pickString(frameRaw, "name"),
			ImageURL: pickString(frameRaw, "imageUrl", "image_url", "image"),
			Scale:    pickFloat64(frameRaw, "scale"),
			OffsetX:  pickFloat64(frameRaw, "offsetX", "offset_x"),
			OffsetY:  pickFloat64(frameRaw, "offsetY", "offset_y"),
		}
		// 只有真的带图才算框（手机版 isValid 同规则）
		if strings.TrimSpace(frame.ImageURL) != "" {
			message.SenderFrame = frame
		}
	}
	if message.MsgType == "" {
		message.MsgType = MsgTypeText
	}
	return message
}

func parseGroup(raw map[string]any) ChatGroup {
	return ChatGroup{
		ID:          pickString(raw, "id", "groupId", "group_id"),
		Name:        pickString(raw, "name", "title"),
		Description: pickString(raw, "description", "intro"),
		Avatar:      pickString(raw, "avatarUrl", "avatar_url", "avatar"),
		Icon:        pickString(raw, "icon"),
		// 手机版 GroupInfo 的字段名：type（chat/notice）、memberRole（admin/member）
		Type:        pickString(raw, "type"),
		MemberRole:  pickString(raw, "memberRole", "member_role"),
		MemberCount: int(pickInt64(raw, "memberCount", "member_count")),
		OnlineCount: int(pickInt64(raw, "onlineCount", "online_count")),
		UnreadCount: int(pickInt64(raw, "unreadCount", "unread")),
	}
}

func parseMessages(raw any) []ChatMessage {
	items := toMapSlice(raw)
	messages := make([]ChatMessage, 0, len(items))
	for _, item := range items {
		messages = append(messages, parseChatMessage(item))
	}
	return messages
}

// toMapSlice 把 any 归一成 []map[string]any，容忍 nil / 单对象 / 类型不符。
func toMapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if record, ok := item.(map[string]any); ok {
				result = append(result, record)
			}
		}
		return result
	case map[string]any:
		return []map[string]any{typed}
	default:
		return nil
	}
}
