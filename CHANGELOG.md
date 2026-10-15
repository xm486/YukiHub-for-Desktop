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
- **好友与聊天**：好友列表按「未读消息 / 正在游戏 / 在线 / 离线」分组，
  组内按**最后上线时间倒序**（四级复合排序键，与手机版、网页端同一套：有未读的置顶 →
  在线状态分区 → 最后心跳倒序 → 显示名兜底）；好友行按手机版口径渲染
  **装备的昵称颜色 + 头像框**，离线/离开时显示「最后在线 X」（时间与排序走同一个解析器，
  「按什么排」和「显示什么」不会互相矛盾）；举报消息提供手机版同款
  **五项预设理由 + 自定义输入**（50 字上限是服务端字段上限，前端同步拦截避免被静默截断）。
  私聊、群聊、表情包与未萌贴纸、图片消息；消息操作菜单（复制 / 回复 / 举报 /
  管理员撤回删除）、@提及、用户资料页（等级、最近游玩、好友状态）
- **游戏库的隐藏游戏**：状态筛选芯片行末尾新增「隐藏」分类（就是手机版筛选栏里的
  「隐藏」），点它只看已隐藏的游戏、用于找回误隐藏的条目；整行是**单选**的互斥分类 ——
  点任意状态芯片会同时退出隐藏分类，再点一次「隐藏」退回默认（隐藏的游戏不进列表）。
  隐藏空分类下显示「没有已隐藏的游戏」而不是「库是空的」，避免劝用户去导入；
  隐藏 / 取消隐藏的入口放在**游戏详情页顶部操作区、与 NSFW 标记同排**
  （对齐手机版游戏「选项」菜单里的同名项，隐藏只改标志位、不删记录，可无损恢复）；
  批量模式下的隐藏 / 恢复保持不动
- **游玩时长上报账号服务端**：一次游玩会话结算后 `POST /game/play_report`
  （`sessionUuid` 直接用 `play_sessions.id`，服务端据此去重），用于发放游戏经验、
  并计入网页端「每日任务」的游玩时长。游戏库启动与大屏模式启动在桌面端本来就是
  同一条链路，所以挂在会话结算处一次即覆盖两个入口 —— 不像手机版大屏是独立实现、
  当初只落了本地记录而漏了上报。仅登录后上报，异步执行且失败只留日志，
  绝不拖慢「退出游戏 → 界面回到空闲」这条路径
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
- **更新检查新增「检测开发版更新」开关**（设置 → 关于与更新）：勾选后检查更新改读
  **滚动开发版预发布 `dev-latest`**，可以第一时间看到最新开发版构建。开发版的 tag 是
  滚动的、本身不带版本号，所以读的是**发布列表**（`releases/latest` 不含预发布），
  版本号从发布说明 / 发布标题 / 附件名三处依次提取。开发版通道**不提供应用内自动更新**
  （自建更新服务的 manifest 只发布正式版，拼出来必然 404），只给「打开发布页 / 下载」。
  自定义更新源（`update_check_url`）固定按正式版处理。新增配置项 `check_dev_updates`
  （默认关），检查结果新增 `update_channel`（`stable` / `dev`）。
  **开发版按「构建时间」判断是否有更新**：开发版没有固定版本号（CI 生成
  `<基础版本>-dev.<提交数>+<短提交>`，首个 tag 之前基础版本恒为 `0.0.0`，提交数还会被
  rebase / squash 弄回退），所以 ① 提交号相同即同一个构建、不提示；② 两边都有时间就比
  时间；③ 时间拿不到才退回版本号。构建脚本额外注入 `BuildTimeUTC`（RFC3339/UTC）
- **产物同步发布到 GitCode 镜像仓库**：GitCode 的托管运行器只有 Linux 镜像（AtomGit
  Action，工作流目录是 `.gitcode/workflows/`），编不出 Windows 那套产物，所以
  **构建仍留在 GitHub，产物推过去**。新增 `scripts/publish-gitcode-release.sh` 调
  GitCode OpenAPI v5 建 / 改 Release 并上传附件，接在 `release.yml`「创建 Release」与
  `autobuild.yml`「发布开发版」两步之后，标了 `continue-on-error` —— GitCode 出问题
  不会拦住 GitHub 的发布。**加一个 `GITCODE_TOKEN` Secret 即启用；不配则静默跳过。**

### 变更

- **默认资料源改为 NextMoe，Bangumi 镜像源下线**（对齐手机版）。NextMoe 的目录只读面
  **匿名即可调用**（与带凭据逐字节相同），且数据是六源对齐的 —— 一次调用就能拿到
  VNDB 评分 + Bangumi 中文名 + ErogameScape 时长，所以作默认源的填充质量最好、
  也不强制用户登录。配额分两档：匿名按**出口 IP** 计（50/min，客户端节流 1300ms），
  登录未萌账号后按**用户**计（100/min、10,000/UTC 日，节流 700ms）。
  **两档各持一份限速状态**，登录用户的额度不会被匿名档的历史请求占掉 ——
  「登录了额度还是一样」正是要避免的 bug，已用单测钉住（含负向验证）。
  日配额耗尽（`code=QUOTA_EXCEEDED`）不退避重试（等下去也不会恢复），
  分钟窗 429 按 `Retry-After`（上限 60s）**叠加**这一轮的指数退避（1200ms×n，
  与手机版同构 —— 写成「取两者较大值」会丢掉指数退避、比手机版更激进），
  5xx 与传输失败按同一套退避重试（合计最多 3 次尝试，与手机版 `MAX_RETRIES=2` 一致）。
  退避与节流都是为了不给对方服务器添压力；相关单测一律走**假 transport + 假时钟**，
  不会向真实服务器发任何请求，也不会真的睡够时间
  已下线的 `bangumi_mirror` 读取时**静默归一到 `bangumi`**（不是回落到默认源），
  从可选清单里移除，但保留常量用于迁移识别
- **在线状态上报设备平台**（服务端新增 `user_presence.platform`）：每次心跳与离线上报
  都带 `platform: "pc"`，好友那边不再把「在电脑上」显示成「手机在线」。服务端对空值
  兜底成 `android`（为了兼容还没上报该字段的老版本 App），所以这一条是硬性要求。
  好友列表与个人主页读入 `platform`，并在**好友列表项 / 浮层好友栏 / 个人主页状态行**
  显示对应的线性图标（手机 / 电脑 / 网页），**离线不显示**；图标为内联 SVG
  （24 格 viewBox、圆角线帽），不使用 emoji。
  另外补上「**关闭客户端时尽力上报一次下线**」（原先只有退出登录会调，
  关掉客户端要等服务端 10 分钟无心跳才判离线）：退出最多多等 2 秒，
  系统注销 / 关机时跳过。契约见 `docs/yukihub-presence-platform.md`
- **「启动配置」页的可执行文件改为可编辑**：原先这里只做只读展示（提示语还写着
  「这里是您一开始选择的路径」），只能去「编辑」页改 —— 同一份数据两处两种行为，
  容易让人以为它们是两个东西。现在与「编辑」页共用同一个输入控件与同一套路径换算
  （换算抽到 `frontend/src/utils/gamePath.ts`，两页都引用它，规则不会再分叉）：
  可直接手输、点右侧按钮重选、在资源管理器中定位。顺带把标签统一成「可执行文件」
- **仓库更名为 `YukiHub for Desktop`**（`xm486/YukiHub-for-Desktop`，GitHub 与 GitCode 同名，
  旧地址由平台 301 重定向）。同时补齐建独立仓库时漏掉的一批「本仓库地址」链接：
  关于面板的 `RepositoryURL`、User-Agent 尾部 URL、设置页「开源仓库」按钮、
  Linux 包元数据 `homepage`、`sync/version.json` 的 `downloads.github` —— 原先都指向
  Android 手机版仓库 `xm486/YukiHub`。文档与合规材料里的 `YukiHub for Windows`
  统一为 `YukiHub for Desktop`（User-Agent 前缀保持 `xm486/YukiHub/` 不变）
- **版本号段数不再固定**（对齐 Android 手机版）：`0.1`、`0.2.5`、`0.1.2.5` 都合法。
  版本比较改为**逐段数值比较**（段数不足一方按 0 补齐，`0.1` 与 `0.1.0` 等价）；
  发布流水线的 tag 触发从 `v*.*.*` 放宽到 `v*`，`sync/version.json` 的一致性校验
  也改为归一化后比较
- **检查更新改为读取代码托管平台的 `releases/latest`**（对齐手机版），不再依赖
  自建更新服务：GitHub 走 `api.github.com`、GitCode 走 `gitcode.com/api/v5`。
  新增设置项「更新源」（默认 **GitCode**，可切 GitHub），选定的源请求失败时
  **自动改用另一个源**。版本取自 `tag_name`、更新说明取自 `body`、发布日期取自
  `published_at`（GitCode 退回 `created_at`）。界面统一提供**「打开发布页」**；
  只有发布里带了更新清单附件且平台是 Windows 时，才额外提供应用内自动更新
  （本仓库暂无代码签名证书，所以默认只走「打开发布页」）
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

- **启动游戏后好友要等几十秒才看到「正在玩」**。手机版 `PresenceManager.updateActivity()`
  是「更新 activity 后**立刻**发一次心跳」，桌面端只在 45 秒的周期心跳里带上 activity ——
  最长要等一整个周期（叠加读侧 15 秒轮询，体感就是几十秒）。现在「未结束会话」集合变化时
  （开始游玩 / 结束游玩，含短会话被删除）会立即补发一次心跳：`SessionService` 提供回调，
  `AccountService.PushPresenceNow()` 消费。补发走容量 1 的信号槽（连发多次只留一次待发、
  调用不阻塞，结算路径不会被一次 HTTP 拖住），并且**心跳循环已停时直接丢弃** ——
  退出路径先停循环再报离线，此时若还补发会把刚报的离线又顶回在线
- **同步 / 导入会把「已隐藏」的游戏重新翻出来**。手机版那条字段是
  `g.hidden = o.optBoolean("hidden", g.hidden)` —— **键缺失 = 保留对端原值**，
  只有明确写 `true` / `false` 才算表态；桌面端却用 `bool` 承载，缺键被解码成 `false`、
  又被合并语句当成「明确不隐藏」写回。老快照、以及只带 `games` 段的轻量快照都不带
  这个键，属于高发场景：一次云同步 / WebDAV 同步 / `.ykbak` 导入就足以把所有隐藏游戏放出来。
  现在 `hidden` 全程三态（`yukihub.Game` / `ImportItem` / `CommitItem` 都是 `*bool`，
  暂存表写 NULL，合并语句 `CASE ... IS NULL THEN games.hidden`），导出侧**恒写**这个键
  （`false` 也要写，否则「取消隐藏」同步不过去）。同时修掉一个同源缺陷：
  PotatoVN / Playnite 这类没有隐藏概念的格式，原先合并更新时也会把本地 `hidden` 写成 `false`
- **详情页「在库中隐藏」与「NSFW 标记」两枚按钮长得一模一样**（都是 `eye-off` 描边、
  同样的圆形、同样大小，并排放在一起），鼠标不悬停就分不出哪个是哪个。
  根源是 NSFW 那枚借用了眼睛图标，而眼睛在本仓库本来是「可见性 / 隐藏」的专用语义
  （大屏菜单里的隐藏 / 取消隐藏也是它）。改法：NSFW 换成火苗（未标记 `fire`、
  已标记 `fire-alert`，红色实心），眼睛从此只表示隐藏 —— 图标本身也带状态，
  不必悬停就知道自己在哪一档
- **`uno:check` 新增图标名校验**（`i-<集合>-<图标名>` 必须真实存在于已安装的
  `@iconify-json/<集合>` 里）。图标类名写错和色阶 / 动画写错一样是**静默失效**：
  UnoCSS 不生成任何 CSS、构建照过，界面上就是一块空白，比色阶错了更难发现。
  校验用已安装集合名做锚点，普通字符串（`i-xxx` 之类）不会被误判

- **同步到 GitCode 时上传大产物一次次等满超时**（真机三次：
  第一次 `Operation timed out after 300001 ms with 0 bytes received`、`%{http_code}` 停在 `100`；
  第二次仍是 `timed out after 1800002 ms`；第三次 7 个文件全部失败，已发出 11–30 MB 不等）。
  **结论：不是脚本姿势问题，是「跨境」那一跳的物理上限**，实测数据：

  | 方向 | 速率 |
  |---|---|
  | GitHub Actions → `file.gitcode.com` 上传 | 单连接 16–43 KB/s，6 路并发合计 ~160 KB/s |
  | 国内机器 → `file.gitcode.com` 下载 | 8.2 MB/s（126 MB / 15.3s） |

  差 50 倍。`file.gitcode.com` 解析到国内 IP（本机 TCP 建连 2.8 ms），瓶颈全在
  GitHub 运行器到国内这一段：RTT 几百毫秒 + 丢包，TCP 拥塞窗口涨不起来。
  已排除：接口用法（官方文档与 SDK 一致）、curl 姿势（`--http1.1` / 去 `Expect` 都试过，
  速率没变）、并发没生效（日志墙钟与各请求时长完全对得上）。
  **第四次（7 个只成 1 个）**：这次**不是超时，是服务端 `502 Bad Gateway`** ——
  12 次尝试里只有 1 次跑完（2335s / 15 KB/s），其余全部是服务端中途掐断。
  两个结论：① 「不到一小时就结束」不是 5 小时预算造成的，而是 `UPLOAD_RETRY=1`
  只让每个文件试 2 次，很快全部放弃；② **并发越高越糟** —— 并发最满时连接死得最早
  （12s / 72s / 74s），等其它文件都放弃、只剩一两条连接时反而跑出全场最快的
  20 KB/s。`file.gitcode.com` 前面有网关，判断是并发/长连接把网关压出了 502。

  于是**两手抓**：
  - **降并发 + 多试几次**：`PARALLEL` → **2**（`autobuild.yml` 里可在界面调），
    `UPLOAD_RETRY` → **30**（真正的时间约束是下面的预算，这个数只是防呆上限），
    重试之间退避 10s/20s/30s…封顶 120s。汇总里给出总尝试次数，并区分
    「实际发出去多少字节」与「落到 GitCode 的多少字节」；
  - **把时间给足**：单文件上限 `UPLOAD_MAX_TIME` 900s → **10800s（3 小时）**、
    总预算 `UPLOAD_DEADLINE` → **18000s（5 小时）**。5 小时是上限：GitHub 官方文档
    写死托管运行器单作业最长 **6 小时**且不可提额。`--speed-limit` **默认关闭** ——
    预签名 PUT 不能断点续传，它把已发出的 11.8 MB 白扔过；
  - **失败语义**：一个文件都没上去 → 该步骤**变红**（去掉 `continue-on-error`）；
    部分成功 → 只 warning。`release.yml` 里这一步挪到作业末尾，
    变红不会连累稳定更新通道的写入。重跑是**增量**的（同名附件跳过），
    多跑几轮就能补齐；
  - **正解：新增 `scripts/mirror-github-release-to-gitcode.sh`**，两种用法：
    ① **推本地已有的产物**（最快）—— 手头有那些安装包/压缩包时，
      直接推上去，完全不碰 GitHub，纯境内上传；
    ② 从 GitHub 拉 —— 先从 GitHub Release 下载，再调 `publish-gitcode-release.sh`
    上传到 GitCode（建/更新 Release、并发上传、跳过同名附件全复用同一套逻辑）。
    CI 里那一步保留，作为手边没有国内机器时的兜底
  - 沿用：逐文件上报实际发出字节数与均速、已有同名附件跳过、出错不中断最后汇总；
    `autobuild.yml` 的 `gitcode_mirror` 可选 全部产物 / **仅 Windows amd64（约 78 MB）** / 不同步
- **构建时间的时区**：发布说明里的「构建时间」与「关于」面板的构建时间统一按 **UTC+8**
  显示（CI 运行器是 UTC，之前差 8 小时；`scripts/build.sh` / `build.bat` 与两条发布流水线
  都改了，本地构建结果不变）。格式没动 —— 开发版更新检查的兜底解析认的是
  `2006-01-02 15:04:05`
- **大屏模式：菜单里的「游玩状态」能手动选了，也不会再把人丢回详情层**
  - 它原来是「点一次循环一档」，而且只循环三档 —— **「搁置 / 抛弃」在大屏里根本设不了**。
    现在打开一个五档选择面板（当前档打勾、带状态图标），选完写库并弹顶部提示条
  - 从详情层的「详细」打开操作菜单时，详情层只是被浮层挡住了
    （渲染条件是 `detailsOpen && !overlayOpen`），菜单一关它又冒出来 ——
    点了「游玩状态」却发现自己回到了详情层，看起来像「跳进了游戏详情」。
    现在打开菜单时就把详情层收起来，关掉菜单回到货架
  - 菜单里那一行的状态名现在五档都认（以前「搁置 / 抛弃」被显示成「未玩」）
- **Windows 安装版构建失败：`build.bat` 还在要求已下线的 CLI**
  （`ERROR: Missing installer CLI payload: ...\yukihubcli.exe`）。
  删 CLI 那次只清了构建步骤与载荷打包，漏了 `:build_installer_package` 里
  对 `yukihubcli.exe` 的存在性校验与拷贝 —— 于是 `installer` / `all` /
  流水线的 `installer-package` 三种模式**都会在打包前直接退出**，
  `YukiHubUpdater.exe` 的提示顺带改成不再声称"signed"（签名现在是可选的）
- **Windows 矩阵关掉 fail-fast**：任一组合作业失败会连坐取消其它
  平台/形态（arm64 就一直是被取消后伪装成失败），导致
  「便携版明明构建成功却拿不到产物」，也让日志里的真正失败点被淹没。
  发布作业仍要求所有勾选平台成功，不会放行半成品
- **`scripts/build.bat` 改为纯 ASCII**：cmd.exe 按系统 ANSI 代码页解析 `.bat`，
  里面的中文注释在多字节代码页下属于隐患，已全部换成英文（含两处既存注释）
- **`scripts/build.sh` 的过时提示**：仍打印 `Building GUI and CLI...`，实际只构建 GUI
- **清理 CLI 遗留**：删除 `skills/yukihub/`（整份技能都在教 AI 调用已下线的
  `yukihubcli`，未被执行任何代码/流水线引用）；`i18next.config.ts` 的
  `preservePatterns` 里 `settings.portableSetup.toast.*` 这条通配把 4 个 CLI 死键
  （`cliRegistered` / `cliUnregistered` / `cliRegisterFailed` / `cliUnregisterFailed`）
  一并保住了，收窄成实际需要的两个动态键后由 `i18n:clean` 清掉
- **未配置代码签名导致 Windows 打包全挂**：三个流水线都无条件提交 SignPath 签名，
  而本仓库没有 SignPath 资格（`organization-id` 为空），签名步骤直接报
  `Input required and not supplied: organization-id` 失败（arm64 是被 fail-fast 连坐取消的）。
  现改为**签名可选**：只有配了 `vars.SIGNPATH_ORGANIZATION_ID` 才提交签名，
  否则跳过并直接产出未签名产物，走同一套上传路径
- **`release.yml` 的 Windows 步骤被拼坏**：安装版载荷那步里混进了便携版收尾逻辑，
  `throw "...".Stop"` 少了闭合 `}` 与引号（PowerShell 直接语法错），且缺便携版的
  未签名上传 / 签名 / 收尾三步 —— 已按 `autobuild.yml` 的正确结构重排
- **`$SignedCli` 引用未定义变量**：CLI 早已删除，三个流水线的「基于已签名载荷构建安装包」
  仍在校验 `$SignedCli`，签名一旦配通也会立刻炸 —— 已移除
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
