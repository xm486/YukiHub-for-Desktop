# 3D 展厅（离线）桌面移植说明

> 状态：**离线个人展厅已移植**；音乐厅只有空间、没有曲目（桌面端尚无音乐库）；
> 在线多人展厅未开始。对应手机版 `app/src/main/assets/exhibition/` 与
> `com/yuki/yukihub/exhibition/ExhibitionActivity.java`、`ExhibitionBridge.java`。

## 一、为什么是「同源 iframe」而不是重画一遍

手机版展厅是一整套 three.js 页面（约 7000 行 JS：`exh.js` 场景与操作、`hall.js` 书架陈列、
`concert.js` 音乐厅、`picker.js` 选品浮层、`viewer.js` 展盒查看器、`gamepad.js` 手柄），
宿主侧只是一个很薄的桥接对象 —— 页面通过 `window.ExhibitionBridge` **同步**取数据。
桌面端 React 重写这套 3D 代码没有任何收益，所以：

```
frontend/public/exhibition-hall/   ← 手机版 assets/exhibition 的原样副本
        │ vite build 原封不动复制
        ▼
frontend/dist/exhibition-hall/    ← Wails asset server 提供
        │
        ▼
React 路由 /exhibition  →  <iframe src="/exhibition-hall/index.html">
```

**必须同源**，因为页面要读父窗口上的桥接对象（手机版是 `addJavascriptInterface`，
桌面端换成「父窗口预置 + 子页面同步读取」）。若换成 `file://` 或别的 origin，
ES module、纹理加载、`localStorage` 全会被 CORS 掐死（手机版注释里也记着同一个坑）。

Wails 不会往非入口 HTML 里注入 runtime，所以 iframe 里没有多余的 `wails` 全局。

## 二、桥接契约（与手机版 `ExhibitionBridge` 一一对应）

| 方法 | 手机版 | 桌面端 |
| --- | --- | --- |
| `onReady(stage)` / `onPerf(json)` / `log(msg)` / `toast(msg)` | 写 logcat / Toast | 开发构建走 `console.warn`（生产静默）/ `react-hot-toast` |
| `getMyLibrary()` → **String** | 读 `GameRepository.getAll()` + 封面 URI 索引 | `ExhibitionService.GetLibraryJSON()`（异步预取，再同步交出字符串） |
| `getMusicLibrary()` → **String** | 读 `MusicRepository` | 固定空集（桌面端没有音乐库），页面显示「音乐厅还没有专辑」 |
| `getDisplaySlots()` / `setDisplaySlots(csv)` | SharedPreferences | **不提供** → 页面自动退回 `localStorage`（陈列位是本地显示偏好，本来就不进同步） |
| `getAuthInfo()` | 在线展厅用 | 未提供（离线模式不需要） |

`getMyLibrary()` 为什么是"预取 + 同步返回"：页面里写的是
`JSON.parse(bridge.getMyLibrary())`，没有 `await`。Wails 绑定是 Promise，
所以 `routes/exhibition.tsx` 先把 JSON 取回来，**渲染期**把桥接挂到
`window.__yukihubExhibitionBridge` 上，页面文档一创建就能同步读到。
放进 `useEffect` 会晚一拍，页面直接退化成「桥接不可用」。

### 字段与单位（必须与手机版逐字一致）

`id / title / originalTitle / engine / playStatus / totalPlayTime / lastPlayedAt /
favorite / nsfw / tags / hasCover / cover`

- `totalPlayTime` 与 `lastPlayedAt` 都是**毫秒**（库内时长是秒，服务层 ×1000）；
- `tags` 是已经拼好的**一行文本**（`剧情 / 校园`）；
- `hidden` 的游戏**不进**展厅（与游戏库默认 exclude_hidden 同口径）；
- `cover` 归一化成同源 URL：`/local/...` 原样，`http(s)://` 走
  `/proxy/image?url=...`（国内直连 vndb/bgm 常常不通，与前端 `ProxyImage` 同一条通路），
  `http://wails.localhost/...` 去掉 host，其它（裸文件路径等）当作没有封面。

## 三、与手机版的差异（有意为之）

| 差异 | 原因 |
| --- | --- |
| 槽位（陈列位）id 按**字符串**处理 | 桌面端主键是 UUID，手机版是自增整数。原实现 `parseInt` 会把 UUID 解析成 `NaN` → 槽位永远读成空台，玩家选完也摆不上去（已用无头浏览器验证：回退成 `parseInt` 后 5 条断言立刻变红） |
| 陈列位存 `localStorage`，不落配置 | 手机版用 SharedPreferences（清缓存不丢）；桌面端 WebView2 的 localStorage 同样跨启动保留，且这份数据本来就不参与云同步。省一套设置项读写 |
| 输入提示与触屏按键 | 桌面端用 WASD / 鼠标拖动 / Space / C / Shift，所以 `#actions`（跳跃/蹲下/奔跑圆钮）与摇杆只在**粗指针**设备上显示；底部提示文案随之切换 |
| 退出方式 | 手机版靠系统返回键；桌面端在**顶部居中**加常驻「← 返回」按钮，`Esc` 同效（`postMessage` 回宿主，宿主 `router.history.back()`）。按钮的绑定**刻意写在 `index.html` 里**而不是 `exh.js`：3D 初始化失败时它是唯一出口 |
| `BLUR_NSFW` 跟随设置 | 手机版是常量 `true`；桌面端由宿主通过 `window.__YH_BLUR_NSFW` 传入 `blur_nsfw_game_covers`，缺省仍打码 |
| 「常玩引擎」显示 `—` | 桌面端不记录引擎（手机版有 `engine` 列），全是「未知」时不假装统计 |

## 四、还没做

- **音乐厅**：空间、前厅感应门、走廊都随页面一起搬过来了，但墙上没有专辑 ——
  曲目要等桌面端有音乐库（手机版是 `MusicRepository` + `MusicLibraryActivity`）。
  `getMusicLibrary()` 现在返回空集，页面会显示引导文案。
- **在线多人展厅**：手机版是服务器页面（`https://yukihub.zh.kg/community/exhibition/`），
  页面自己 `fetch` 同源 API，需要 `getAuthInfo()` 提供身份。桌面端的 iframe 在不同 origin，
  需要另定一套鉴权/代理方案。
- **鼠标锁定视角**：现在是「按住拖动转视角」（手机版同款）。桌面端可以加
  `requestPointerLock` 让转视角更跟手，但要和「轻点选展品」的手势判定一起改，留待后续。

## 五、验证方式

1. **Go 单测**（`internal/service/exhibition_service_test.go`）：字段名/单位/收藏/标签/
   隐藏过滤/封面归一化/清零时间点/空库。负向验证过三处：去掉 hidden 过滤、去掉 ×1000、
   远端封面不走代理，各自都会让断言变红。
2. **无头浏览器冒烟**（临时脚本，未入库）：本地静态服务器 + 一个注入桥接的宿主页，
   用 headless Edge 加载 `/exhibition-hall/index.html`，确认
   - 桥接被调用、`hall.setGames` 跑完（日志：`分区完备性：6 / 6 ✅ 无遗漏`）；
   - 3D 场景真的渲染出房间 / 中央展台 / 旋转展架，且**旋转展架上的封面是真实图片**
     （证明 `cover` URL → 纹理这条链路通）；
   - 全程无 `IFRAME ERROR` / `console.error`。
   > 注：无头软件渲染只有 ~0.5 FPS，rAF 几帧后就不再被服务，书架剔除循环（0.4s 一次）
   > 跑不到，所以「书架贴封面」这一步无法在无头环境验证，需要真机看一眼。
3. **picker 单元验证**（同上，未入库）：把 `picker.js` 单独拉进一个带骨架 DOM 的页面，
   15 条断言覆盖空槽位、UUID 往返、卡片数量、当前陈列高亮、R18 角标、点击写槽位、
   自动关闭、清空此展台、脏值规整。回退成 `parseInt` 后 5 条断言变红（负向验证成立）。
