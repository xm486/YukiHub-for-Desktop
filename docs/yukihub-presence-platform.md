# 在线状态「平台标识」契约（服务端 → 客户端）

> 服务端已在 `user_presence` 表加 `platform` 列，记录「这个人的心跳是从什么设备发出来的」，
> 目的是让好友列表 / 在线墙 / 个人主页能区分**手机在线**还是**电脑在线**。
> 手机端（Android）已按同一契约接完；本文档是 PC 端的执行与验收依据。
>
> 实现状态：**PC 端已接入**，见文末「六、PC 端接入情况」。

---

## 一、为什么必须做

**关键规则：PC 端不上报 `platform`，服务端会把它当成手机。**

服务端对空值做了兜底 —— 目前唯一会发心跳的客户端是 Android App，所以「没上报」＝
「旧版 App」＝「手机」。这个兜底是为了不破坏已发布的老版本 App。

**后果**：PC 端上线后如果不上报 `platform`，用户在电脑上登录，好友看到的会是
「手机在线」。**这是硬性要求，不是可选项。**

## 二、硬性规则（违反任何一条都算做错）

1. **每次心跳都必须带 `platform` 字段**，值固定为 `"pc"`。
2. **`platform` 只认三个值**：`android` / `pc` / `web`（小写）。传其它值会被服务端
   丢弃并保留原值。
3. **不要在心跳里传 `status: "offline"`**。离线有专用接口（见 3.3），
   `status` 只接受 `online` / `away` / `busy`。
4. **不要试图用 `platform` 做业务判断**。它纯粹是展示用的设备标识。
5. 统一前缀 `https://yukihub.zh.kg/api`。
6. 请求头固定：`Content-Type: application/json` + `Authorization: Bearer <accessToken>`。

## 三、接口契约

### 3.1 心跳上报

```
POST /api/presence/heartbeat
Body:
  {
    "status":   "online",        // online | away | busy（不要传 offline）
    "activity": "正在玩：xxx",    // 可选，空字符串=清除
    "platform": "pc"             // ★ 固定 "pc"
  }
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `status` | 是 | `online` / `away` / `busy`，非法值服务端兜底成 `online` |
| `activity` | 否 | 「正在玩」文本，最长 200 字，超长服务端截断。**始终带上**，空串表示清除 |
| `platform` | **是** | ★ PC 端固定传 `"pc"` |

`platform` 的服务端处理语义：

```
上报非空值    → 覆盖库里的值
上报空串/不传 → 保留库里的原值不动
```

这是为了让**旧版本客户端**（不发这个字段）不会把 PC 端记录冲掉。

### 3.2 心跳频率

- **每 45 秒一次**（与 Android 一致）。
- 服务端判定阈值：`≤ 2 分钟` 保持上报状态；`2~10 分钟` 自动降为「离开」；
  `> 10 分钟` 判定「离线」。
- 网络抖动漏一两次无影响，**不要加重试风暴**。

### 3.3 离线上报

用户退出登录 / 关闭客户端时**尽力**调用一次（调不通也没关系，服务端会超时判离线）：

```
POST /api/presence/offline
Body: { "platform": "pc" }     // 可选；不传则保留原值
```

### 3.4 读侧接口（返回体新增 `platform`）

| 接口 | `platform` 位置 |
|---|---|
| `GET /api/friends/list` | `friends[].platform` |
| `GET /api/community/online` | `onlineUsers[].platform` |
| `GET /api/community/user?uid=` | 顶层 `platform` |
| `GET /api/user/profile?uid=` | 顶层 `platform` |

**语义注意**：
- 用户**离线**时个人主页接口的 `platform` 返回空串 `""`。
- 好友列表 / 在线墙只列在线用户，`platform` 总是有值。
- 展示时**自己做一次兜底**：空串或未知值一律当 `"android"`，与网页端口径一致。

## 四、展示规范

| `platform` | 图标 | 文案 |
|---|---|---|
| `android` | 手机 | 手机 |
| `pc` | 显示器 | 电脑 |
| `web` | 地球 | 网页 |
| 空 / 未知 | 手机 | 手机 |

图标风格：**线性图标（outline）、24 格 viewBox、圆角线帽**，不要用 emoji。

| 位置 | 形式 |
|---|---|
| 好友列表项 | 状态点旁一行内小图标（13px 左右） |
| 在线墙 / 在线用户列表 | 同上 |
| 用户个人主页 | 状态行：线性图标 + 状态文字 |

**不要显示的情况**：
- 用户离线 → 不显示平台图标
- **「正在玩的人」这种头像横条 → 不显示**（视觉上会显得杂乱）

## 五、不要做的事

- ❌ 漏传 `platform`（会被当成手机）
- ❌ 传 `platform: "PC"` / `"windows"` / `"desktop"` 之类（只认小写 `pc`）
- ❌ 在心跳里传 `status: "offline"`（用 `/presence/offline`）
- ❌ 用平台字段做权限 / 业务逻辑
- ❌ 一次心跳失败就重试风暴
- ❌ 在「正在玩的人」横条上显示平台图标

### 已知限制（当前设计的固有取舍）

`user_presence` 表是**一个用户一行**，所以同一账号在手机和电脑上同时在线时，
**只有最近一次心跳的设备会被记录**，平台标识会随心跳来回切换。
这是「零新增数据、不建新表」换来的取舍，客户端不需要做任何额外处理。

---

## 六、PC 端接入情况

### 6.1 上报侧

| 位置 | 改动 |
|---|---|
| `internal/service/yukihubaccount/client.go` `Heartbeat` | body 增加 `platform: PresencePlatformPC` |
| 同上 `MarkOffline` | body 增加 `platform: PresencePlatformPC`（同时保留原有 `status` / `activity`，与旧服务端兼容） |
| `internal/service/yukihubaccount/social.go` | 新增常量 `PresencePlatformAndroid` / `PresencePlatformPC` / `PresencePlatformWeb` |

心跳沿用既有的 `AccountService.startPresence()`：登录后**立刻发一次**，
之后每 45 秒一次（`accountPresenceInterval`），登出 / 退出时尽力调一次 `MarkOffline`。

### 6.2 读侧

| 位置 | 改动 |
|---|---|
| `Friend`（`friends/list`） | 新增 `Platform` 字段，`parseFriend` 解析 `platform` |
| `UserProfile`（`user/profile`） | 新增 `Platform` 字段，`UserProfile()` 解析 `platform` |

PC 端目前只用到 `friends/list` 与 `user/profile`；`community/online` 与
`community/user` 没有对应界面，未接入（将来做在线墙时按同一字段读取即可）。

### 6.3 展示侧

| 位置 | 实现 |
|---|---|
| 好友列表项（`FriendsChatModal`） | 状态/活动那一行的行首，13px 线性图标 |
| 浮层好友栏（`FriendsOverlay`） | 同上，12px（浮层字号整体更小） |
| 个人主页状态行（`UserProfileModal`） | 状态徽章内：图标 + 状态文字，12px |

- 图标组件：`frontend/src/components/ui/PresencePlatformIcon.tsx`
  （内联 SVG，24 格 viewBox，`stroke-linecap/linejoin="round"`），
  三端风格对齐，不使用 emoji。
- 归一化与显示判定：`frontend/src/utils/presencePlatform.ts`
  - `normalizePresencePlatform`：空串 / 未知值 → `android`
  - `showsPresencePlatform`：只有 `online` / `away` / `busy` 才显示
    （PC 端好友列表**离线好友也在列表里**，所以不能只看 `platform` 有没有值）
- 「正在玩的人」横条在 PC 端不存在（首页没有好友头像横条），
  好友开播通知浮层也**未加**平台图标，符合契约的「不要显示」要求。

### 6.4 测试

`internal/service/yukihubaccount/presence_platform_test.go`：

- 心跳体必须含 `platform:"pc"`，且**不得**含 `status:"offline"`
- 心跳体必须始终带 `activity`（空串用于清除）
- 离线上报体必须含 `platform:"pc"`
- `parseFriend` 对 `pc` / `android` / `web` / 空串 / 缺字段五种情况的取值
- `UserProfile` 解析顶层 `platform`

> 这组测试是防回归闸门：谁把 `platform` 从心跳里删掉，测试立刻失败
> （已做负向验证）。
