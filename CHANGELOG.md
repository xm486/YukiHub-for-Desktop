# 更新日志

本文件记录 YukiHub Desktop 自身的变更。
上游 LunaBox 的历史变更日志完整保留在 [CHANGELOG.upstream.md](CHANGELOG.upstream.md)（未作修改）。

版本号规则：`主版本.次版本.修订号`，与 git tag（`v*.*.*`）一致。

---

## 未发布

0.1.0 基线之后、尚未打 tag 的进展。主线是**按 Android 手机版逐项对齐**。

### 新增

- **Linux（amd64）支持**：恢复原生 Linux 运行（GTK4 / WebKitGTK 6.0）。游戏启动支持
  原生 / Wine / Proton / Steam 四种策略；URL 协议注册、托盘、Wine/Proton 辅助工具
  （winecfg / winetricks / protontricks）一并恢复；发布 deb / rpm / AppImage
- **YukiHub 账号系统**：邮箱注册 / 登录 / 找回密码；未萌（NextMoe）、Hikarinagi
  第三方快捷登录（OAuth 授权码 + PKCE + 本地 loopback 回调，客户端不接触第三方令牌）；
  登录后默认开启「向好友展示正在玩」
- **云同步**：复用导出 / 导入链路上传下载快照，格式逐字段对齐手机版 schema 5
  （云同步：`created_at` 恒 0、`lightweight=true`、固定 note、带 profile 段；
  本地 `.ykbak`：`backup_type=local_full` + 当前时间）
- **好友与聊天**：好友列表按「正在游戏 / 在线 / 离线」分组；私聊、群聊、
  表情包与未萌贴纸、图片消息；消息操作菜单（复制 / 回复 / 举报 /
  管理员撤回删除）、@提及、用户资料页（等级、最近游玩、好友状态）
- **首页 Galgame 资讯**：数据来自未萌 `/v2/news`，6 条轮播（5 秒、可手动滑动）、
  2 小时磁盘缓存、失败回退过期缓存，详情弹窗标注来源并可跳转原文
- **首页用户区**：头像 + 在线状态点 + 时段问候，点击进入账号面板
- **内置兼容工具**：`build/compat-tools` 内置 Locale Emulator 2.5.0.1（LGPL-3.0）
  与 Magpie 0.12.1（GPL-3.0），附许可证全文，也允许替换成自己的版本
- **大屏模式（BigScreen）**：聚焦引擎、虚拟货架、本地 PV 播放
- **导入**：新增「从 LunaBox 导入」（ZIP 备份 + CSV）
- **侧栏**：好友聊天上移为主导航项；底部工具区 = 下载 / Gal 工具箱（外链）/
  社区（外链）/ 设置
- **游戏计时闭环**：进程识别失败的会话不再被当成垃圾丢弃 —— 改为降级继续计时，
  并以「回到 YukiHub 连续停留」作为结算信号（手机版靠 Activity 生命周期，
  桌面端等价于此）。另加「仅计时」入口，用于第三方启动器 / Steam / 双击启动
  这类 YukiHub 拉不起进程的场合
- **好友「开始玩游戏」通知**：全局浮层（屏幕右下角，不抢焦点，不打断游戏），
  点击进好友面板；卡片样式对齐 Steam（深色 + 头像 + 绿色游戏名，多人同游戏合并一条）
- **游戏内好友栏（overlay）**：全局快捷键呼出，贴在屏幕右侧，与主界面共用同一份
  好友列表推送；默认 `Shift + ~`，可在设置页录制改成任意组合
- **overlay 内直接私聊**：点好友即进入会话（历史 + 10 秒轮询新消息 + Enter 发送，
  支持图片与表情消息），Esc 退回列表；对齐 Steam —— 不必退出游戏就能回一句
- **好友申请列表**：拉 `/friends/requests`，分「收到的请求 / 已发出的请求」两段，
  可就地接受 / 拒绝；侧栏好友入口加**待处理申请角标**（收到申请立刻可见）
- **设置页**：新增「呼出好友栏快捷键」控件（录制式，避免手打 accelerator 出错）

### 变更

- 设置页瘦身：删除代理、键盘快捷键、CLI 与 MCP（桌面版不需要），
  备份收敛为本地自动备份；新增「当前资料源」；账户区只保留第三方授权
- 游戏详情页：删除与 NSFW 入口上移到顶部操作区（原先在编辑页最底部）
- 云同步快照的身份键（`gamehub_local_game_id` / `gaishi_local_game_id`）
  改为**省略而非写空串** —— 手机版 `optString` 会把空串当成「确认清空」，
  会把对端的身份键抹掉
- 快捷登录对外改称「未萌登录」（provider 值仍为 `kungal`，与手机版改名计划一致）；
  其回调端口此前误用 Bangumi 的 23679，已改回复用已登记的 `127.0.0.1:14792`
- BigScreen 与库页详情布局照手机版重排
- 好友状态轮询合并为一套：后端每 10 秒拉一次 `/friends/list`，**有实质变化才把整份
  列表推给前端**；此前前端自己 30 秒拉一次、后端另开一路 15 秒算通知，两边依据
  不是同一份快照，会出现「列表已经显示在玩、通知却没来」
- 手动构建/发布流水线新增「发布方式」开关，**默认「仅构建产物（不发布）」**：
  `release.yml` 可选仅构建产物 / 预览版 / 正式版，`autobuild.yml` 可选仅构建产物 /
  发布开发版；选「仅构建产物」时整块跳过发布作业，只留 Actions Artifacts。
  打标签触发的运行没有 input，仍按正式版处理
- `release.yml` 新增 **`version` 版本号输入**：填了就用它，可从**任意分支**直接发布
  （Release 建在 `v<版本号>` 上，标签不存在时由 GitHub 自动创建并指向本次提交）；
  留空才回退到所选的 `v*.*.*` 标签。此前版本号只能来自标签，而仓库在首个版本前
  一个标签都没有，手动发布路径实际走不通。版本号统一由 `validate` 作业解析成
  version / tag 两个输出下发，三者（输入、标签、`sync/version.json`）不一致都会明确报错
- Actions 页面里的工作流名、作业名、步骤名改为中文（Go / pnpm / NSIS / SignPath
  等技术专名保留）；PR 检查类作业名不动，避免破坏分支保护的必需检查项
- CI 增加 `actionlint` 门禁，静态检查 workflow 的 `needs` 引用、表达式属性名、
  shell 语法

### 修复（选）

- **Windows 打包一开始就失败**：`scripts/*.bat` 被 `.gitattributes` 的
  `* text=auto eol=lf` 规范成了 LF，而 `cmd.exe` 解析 LF-only 的 `.bat` 会把多行
  `( )` 块和 `for /f` 拆错（满屏「`'xx'` 不是内部或外部命令」），Windows 便携版 /
  安装版构建刚起步就退出。修复：`.gitattributes` 增加 `*.bat` / `*.cmd` →
  `text eol=crlf`，并在两个发布流水线的 Windows 作业里加了行尾校验（不对就早退并报错）
- 消息顺序错乱：消息 id 是纯数字字符串，原先按字典序排序（`"9" > "1000"`）
  导致整个列表错位，改为按时间 + 数值 id
- 图标大面积不可见：UnoCSS 图标是 mask + `width/height:1em`，放在 `<span>`
  （inline 元素）上尺寸无效 → 全局补 `display:inline-block`
- 聊天图片 / 头像框在本地看不见：服务端下发的是**相对路径**，直接塞 `img src`
  会被解析成 `wails.localhost/...` → 统一补全站点地址（手机版同一逻辑）
- 资讯题图「加载出来又消失」：多条复用同一个 `<img>`，失败回退标记挂在 DOM 上
  永不重置 → 改为每条独立元素层叠、各自持有加载状态
- 未萌图标显示成黑块：资料源 logo 统一套了单色滤镜（`brightness-0`），
  彩色官方头像被压黑 → 按来源判断是否单色化
- 封面源优先度此前只在刮削路径生效，已接到下载与图片代理两个出口
- 导入丢失 nextmoe 来源：「来源名 → SourceType」映射漏项会**静默归错**，
  已统一为单一实现并修掉另外 3 处同类漏项
- 图片代理限制同时回源数量（4），避免整包贴纸批量加载时部分超时裂图
- 侧栏展开后底部横排溢出把侧栏撑变形 → 底部改始终竖排
- 启动即闪退：全局快捷键必须**在 `App.Run()` 之前**注册（写在启动回调里会走
  「派发主线程并同步等待」的路径，直接把启动卡死，且没有任何报错输出）
- 带着登录态启动时进程静默崩溃：好友列表变化推送会调用一个**注入型**事件发送函数，
  而它在窗口建好之前还是 nil —— nil 调用会带走整个进程。已改为默认空实现
- 浮层窗口（游戏内好友栏 / 好友通知）此前完全打不开：资源服务器**没有 SPA
  fallback**，`/overlay` 与 `/notice` 拿到的是 404 白页，前端脚本根本没跑
- 浮层窗口位置不生效：`InitialPosition` 默认是屏幕居中，`X/Y` 会被忽略
- 通知浮层不再抢焦点（`WS_EX_NOACTIVATE`）：通知把正在玩的游戏踢到后台就本末倒置
- 浮层窗口是直角矩形、界面里画的却是圆角卡片，看上去像「圆角窗口外面还套了一层
  直角框」→ 用 DWM 给窗口本身加圆角（设置后回读确认，Windows 10 退回
  `SetWindowRgn` 裁剪），卡片同时铺满窗口，不再有第二层
- 通知浮层此前外面那圈「紫框」：调试用的洋红底色没有清干净（注释删了、颜色还在）
- 浮层聊天里的图片 / 表情显示成碎图标：图片渲染原先在浮层与主界面各写了一份，
  浮层那份少了「图片代理失败退回直连」和「表情名 → URL 映射」两步
  → 抽成共享组件 `components/chat/ChatMessageMedia`，两边共用
- **好友申请不显示**（手机上有、PC 一片空白）：`/friends/list` 的 `pendingRequests`
  是**数字**（待处理条数），客户端却当数组解析；而且压根没有 `/friends/requests`
  这个接口。另外申请里的 `friendshipId` 也写成了 string，接受/拒绝发出去是空值
- **搜索用户后一律显示「加好友」**：`friendStatus` 没解析，已经是好友或已申请过的
  人也显示可点按钮 → 现在与手机版一致（已是好友 / 已发送请求 / 可加好友）

### 移除

- 云备份（第三方托管）相关设置与界面，备份只保留本地
- 设置页的代理、键盘快捷键、CLI、MCP（协议转发改由 `internal/ipc/` 承担）

---

## 0.1.0（未发布，开发中）

首个开发版本。当前里程碑是"干净的 Windows-only 工程基线"，
产品界面尚未重建，因此不提供安装包。

### 新增

- 以 LunaBox v1.13.0 为基线建立硬分叉，并保留完整上游历史与版权声明
- 技术路线决策记录：`docs/decisions/0001-fork-lunabox-as-windows-baseline.md`
- 路线图：`docs/ROADMAP.md`
- 与 Android 版 YukiHub 的数据迁移设计：`docs/mobile-yukihub-migration.md`
- 分叉配置清单（仓库、凭据、签名、更新服务）：`docs/fork-setup.md`
- AGPL 合规材料：`NOTICE`、`docs/AGPL-COMPLIANCE.md`、
  `THIRD_PARTY_LICENSES.md`、`third_party/` 许可证文本
- CI 新增 `gofmt` 与 `go vet` 门禁

### 变更

- 品牌与身份：Go 模块名 `lunabox` → `yukihub`，应用标识 →
  `com.yukihub.desktop`，URL 协议 `lunabox://` → `yukihub://`，
  数据目录与数据库 `LunaBox` / `lunabox.db` → `YukiHub` / `yukihub.db`，
  CLI `lunacli` → `yukihubcli`，更新器命令同步更名
- 前端工作区包 `@lunabox/desktop-shell-*` → `@yukihub/desktop-shell-*`，
  Wails 生成绑定目录 `frontend/bindings/lunabox/` → `frontend/bindings/yukihub/`
- 构建期环境变量 `LUNABOX_*` → `YUKIHUB_*`
- User-Agent 改为 YukiHub 自有标识，不再沿用上游仓库地址
- 默认云备份后端由上游绑定的托管服务改为 WebDAV（用户自持存储）
- 应用图标与界面品牌素材：EXE/ICO/启动窗口采用手机版 YukiHub 图标，
  侧边栏 logo 与托盘图标改为雪花标识（SVG 组件与位图同一几何参数生成）
- Android 版数据契约字段落地（迁移 177）：`legacy_local_id`、`source_device_id`、
  `playtime_reset_at`、`hidden` 四个列，导入 Android 备份时正确持久化
  （此前会静默丢弃，导致清零历史复活、跨设备去重退化）
- 契约文档更正：Android 的 `play_sessions.duration` 与 `games.total_play_time`
  均为毫秒（原文档误写为"duration 是秒"），已按手机版源码核实更正
- 版本号起点为 0.1.0，与上游版本线解耦
- 全仓库 Go 代码重新执行 `gofmt`（模块改名会影响导入排序）

### 修复

- CI 中 Go 测试此前实际只编译不执行（`go test -run '^$'`），现已改为真实执行
- 应用内更新检查不再默认请求上游更新服务地址
- 未配置更新源时启动会弹出 `failed to fetch update info from all sources: %!w(<nil>)`。
  清空默认更新地址时漏了"无源可用"的分支，把"没有源"误报成"所有源都失败"，
  还把 nil 传给 `%w`；已补上该分支并给错误加兜底
- `AddGameModal` 引用了被重命名的品牌图片（`luna1/luna2.webp` → `brand-1/brand-2.webp`），
  导致 `vite build` 报 `Could not resolve`——CI 的 `pnpm run build` 同样会失败
- 界面上的 "LunaBox" 字样：`topbar-title.png` / `topbar-title-dark.png`
  是上游的文字 logo 图片，文本替换无法修改图片内容，已删除并改为代码渲染 "YukiHub"
- `main.go` 的 `//go:embed` 移除对已删除 macOS 资源的引用
  （否则 main 包无法编译）
- 消除 `internal/utils/processutils` 中 `unsafe.Pointer` 的 uintptr 往返转换，
  `go vet ./...` 现在无任何告警

### 验证

- `gofmt -l .` 无输出；`go vet ./...` 无输出；`go build ./...` 通过
- `go test ./... -count=1`：29 个含测试的包全部通过，0 失败
- `cd updater && go test ./... -count=1`：通过
- 上游遗留的 114 个测试文件首次被真实执行，结果全绿

### 移除

- 源码中硬编码的上游 Hikarinagi OAuth Client ID（改为构建期注入，
  第三方凭据必须自行申请）
- 上游默认更新服务地址
- 上游的多语言 README（`README.zh-CN.md`、`README.ja.md`），
  改为 `README.md`（中文）与 `README.en.md`（English）；
  历史版本仍可在 git 历史中查阅
- 全部 macOS / iOS / Linux 平台代码、构建资源与发布链路（本项目仅面向 Windows）：
  - 64 个 Windows 构建不参与的 Go 文件（darwin / linux 实现与对应测试）
  - `build/darwin`、`build/ios`、`build/linux`、`lib/{linuxamd64,linuxarm64,macarm64}`
  - `scripts/build.sh`、`scripts/patch-wails-linux-tray.sh`
  - `release.yml` / `autobuild.yml` 中的 macOS 与 Linux 构建作业
  - `Taskfile.yml` 中的 darwin / linux 构建、打包与补丁步骤
- Wine / Proton / CrossOver 工具链（Windows 上不存在对应概念）：
  - Go：`internal/utils/protonutils`、`internal/service/compattools`、
    `internal/service/compatibility_tools.go`，以及 `IntegrationService` 的
    `GetLocalProtonTools`、`AppConfig` 的 6 个全局 Wine/CrossOver 字段
  - 前端：游戏启动面板的 Proton 工具发现与兼容层快捷工具、游戏设置面板的
    Wine / CrossOver / winetricks / protontricks 设置块、`wine_runner` 事件分支
  - 四语言文案清理 36 个孤儿键
- 共享代码中的非 Windows 死分支（23 个文件，净减 410 行）：
  - 收敛恒真的 `runtime.GOOS` 判断：协议解析的 `allowLaunch` 参数、`Frameless`、
    `ShouldQuit`、`isLaunchableEntry`、路径打开与路径比较等
  - 删除恒假分支：macOS 的 Wine 前置校验、portable 的 Linux 启动器路径、
    AppImage 协议修复、导入目录的 goos 参数、测试中的平台 skip
  - `gamehelper.IsMacAppBundlePath`（macOS .app 概念）及 6 处调用点
  - `internal/utils/tricksutils` 整包（上一轮移除 compattools 后已无调用者，
    因 Go 不检查未使用的包而被遗漏）

- Linux 专属的 Steam Proton 兼容层（Windows 上全部为桩实现或恒报错）：
  - Go：`GetGameSteamCompatibility` / `SetGameSteamCompatibilityTool` /
    `RestartSteamClient` / `OpenGameSteamProtonPrefix` 四个服务方法，
    `integrator/steam_compat_other.go` 桩实现，相关类型与转换器
  - 前端：游戏启动面板的 Wine runner 选择 UI（982 行重写为 360 行）、
    Steam Proton 版本选择、Proton prefix 目录、重启 Steam 确认弹窗，
    更新弹窗的 macOS/Linux 手动下载区块
  - 四语言清理 49 个孤儿键；启动方式列表不再出现「兼容层启动」，
    历史数据带入该值时回落为普通启动

保留说明：游戏级的 `wine_runner` / `wine_args` / `wine_prefix` 属于导入与云同步的
数据契约，仍保留在数据模型与快照中；Steam 的 Windows 有效能力
（Steam 导入、启动状态、启动参数写入）全部保留。

### 已知问题

- 游戏级 `wine_runner` / `wine_args` / `wine_prefix` 在 Windows-only 语境下的
  存废待复核。它们属于导入与云同步的数据契约，删除是数据语义变更，
  需与 Android 版按 `docs/mobile-yukihub-migration.md` 两端评审
- 发布流水线 `release.yml` 依赖 SignPath 的代码签名资格。该资格属于上游项目，
  不随代码转移，YukiHub 需自行申请或改用自有证书，否则签名与更新校验环节会失败
- 界面与领域模型仍为上游形态
- 应用图标、界面插画、截图仍为上游占位素材
