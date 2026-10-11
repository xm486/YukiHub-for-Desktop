# 上游项目与修改记录（LunaBox）

本文件用于满足 GNU AGPL v3 第 5(a) 条的要求：修改版本必须显著声明"已修改"，并给出相关日期。
同时保留上游项目的版权声明与出处。

## 上游项目

| 项目 | 说明 |
| --- | --- |
| 名称 | LunaBox |
| 仓库 | https://github.com/Saramanda9988/LunaBox |
| 版本 | v1.13.0 |
| 版权 | Copyright (c) LunaBox contributors |
| 许可证 | GNU Affero General Public License v3.0 |
| 许可证全文 | 本仓库根目录 `LICENSE`（与上游一致，未作修改） |

LunaBox 是一款开源 Galgame 库管理与启动工具，Windows 为其主支持平台。
本项目的 Windows 端已实现能力（进程识别与退出监听、游玩时长统计、Locale Emulator 启动、
Magpie 联动、托盘、URL 协议唤醒、系统代理、后台静音、NSIS 安装器、应用内增量更新与
Authenticode 校验等）源自该上游项目。

上游历史变更日志完整保存在 `CHANGELOG.upstream.md`（同样未作修改）。

## 本项目的定位

YukiHub Desktop 是 YukiHub 项目的桌面版本（Windows / Linux amd64）。
它与 Android 版 YukiHub（https://github.com/xm486/YukiHub ，GPL-3.0）属于同一产品家族，
但桌面版并非 Android 版的移植，而是以上游 LunaBox 为基线重新构建产品层。

## 修改记录

### 2026-09-27 — 建立硬分叉基线并完成去品牌化

基线提交：`chore: import LunaBox v1.13.0 as fork baseline`

在此基础上完成：

1. **去品牌化**
   - Go 模块名 `lunabox` → `yukihub`（含 `updater` 子模块 `lunabox/updater` → `yukihub/updater`）
   - 应用标识 `io.github.saramanda9988.lunabox` → `com.yukihub.desktop`
   - URL 协议 `lunabox://` → `yukihub://`
   - 数据目录 / 数据库 `LunaBox`、`lunabox.db` → `YukiHub`、`yukihub.db`
   - 更新器命令 `lunabox-updater` / `lunabox-update-builder`
     → `yukihub-updater` / `yukihub-update-builder`
   - 前端工作区包 `@lunabox/desktop-shell-*` → `@yukihub/desktop-shell-*`，
     Wails 生成绑定路径 `frontend/bindings/lunabox/` → `frontend/bindings/yukihub/`
   - 构建期环境变量 `LUNABOX_*` → `YUKIHUB_*`
   - 安装器 NSIS 脚本、Windows 版本资源、应用图标路径等同步调整
2. **身份与凭据**
   - User-Agent 由 `Saramanda9988/LunaBox/...` 改为 `xm486/YukiHub/...`
   - 移除源码中硬编码的上游 Hikarinagi OAuth Client ID，改为由构建期注入；
     所有第三方服务凭据必须由 YukiHub 自行申请
   - 移除应用内更新检查中指向上游更新服务的默认地址
3. **默认行为调整**
   - 默认云备份后端由上游绑定的托管服务改为 WebDAV（用户自持存储）；
     托管后端仍作为可选项保留
4. **工程与合规**
   - 修复 CI 中 "只编译不执行测试" 的写法（原为 `go test -run '^$'`），
     Go 测试改为真实执行，并新增 `gofmt` 与 `go vet` 门禁
   - 新增 `NOTICE`、`THIRD_PARTY_LICENSES.md`、`third_party/` 许可证文本
     （上游仓库中没有任何第三方许可证清单）
   - 新增本文件与 `docs/AGPL-COMPLIANCE.md`
   - 全仓库 Go 代码重新执行 `gofmt`（模块改名会影响导入排序，属于必要调整）
   - 版本号从上游的 `1.13.0` 改为 YukiHub 自己的 `0.1.0`

### 2026-10-06 — 恢复 Linux（amd64）支持

- 从本仓库历史（平台移除前的提交 `02158b5^`、`8a55b8c^`、`fb87c68^`）恢复
  Linux 平台实现：进程识别（/proc）、启动策略（原生 / Wine / Proton / Steam）、
  Steam 集成（兼容工具 VDF / Proton prefix / 客户端重启）、URL 协议注册、
  Wine/Proton 辅助工具、7zz 捆绑与 deb / rpm / AppImage 打包链
- 回移上游 v1.13.0 之后对 Linux 进程识别的修复（忽略 Steam runtime helper）
- 恢复 `AppConfig` 的 Wine / CrossOver 字段与迁移逻辑；数据契约未变更
- 移除 CLI 后的适配：Linux 打包不再包含 `yukihubcli`
- 决策与范围见 `docs/decisions/0004-restore-linux-support.md`

### 2026-10-07 — 修复 Linux 退出监控误判

- Linux 游玩会话退出监控改为只依据被监控进程的 PID 与启动时间（`/proc/<pid>/stat`）：
  被监控进程存活期间不再因安装目录 / 进程路径不匹配而结束会话。
- 修复 Proton/Wine 游戏（Linux 安装目录与 `S:\...` 路径不一致）在启动宽限期
  （约 2 分钟）后被误判进程退出、游玩时长被错误截断的问题。
- 退出日志区分「进程退出」「PID 复用」与「进程退出前未能观测到」，便于排查。
- 新增回归测试 `internal/service/launcher/exit_watch_linux_test.go` 与
  `processutils.LinuxProcessTracker` 根进程身份断言。

### 2026-10-08 — 产品显示名统一、发布链路解耦与若干修复

- 产品显示名定为 **YukiHub Desktop**（Windows / Linux 桌面端）。
  `internal/version.AppDisplayName` 仍是唯一来源，并按 `AGENTS.md` 的约定收口了散落的
  硬编码：移除四语言文件里的 `settings.about.appName`（关于面板直接显示后端返回的
  `appName`），许可证正文与设置页页脚也不再各自复述产品名。
  仓库名与 git 远端地址当时保持不变（改显示名不动链接）；该仓库已于 2026-10-09
  更名为 `YukiHub-for-Desktop`，见下一条记录。
- `autobuild.yml` / `release.yml` 的发布作业不再 `needs: build-linux`：Linux 打包失败
  不再阻断 Windows 的预发布与正式发布。产物校验由「必须 7 个」改为「Windows 四件套
  必须齐全，Linux 三件套为可选增量（0 个或 3 个）」。
- 修复 `useAppRuntimeEffects.ts` 中重复的进出场动画：`animate-enter` / `animate-leave`
  在主题里并不存在（会被 UnoCSS 静默丢弃，并被 `uno:check` 拦下），而 `AppToaster`
  的堆叠容器已经统一负责 toast 的进场与离场动画。
- 修复前端平台门控的 fail-closed：`store.platformGOOS` 初值不再为空串。此前初值为空时
  写正判的 Windows 专属入口（应用内更新、管理员启动、导出快捷启动方式、
  Locale Emulator / Magpie、批量导入 Steam）会在 `GetGOOS` 返回前被隐藏，
  调用失败时更会永久隐藏。
- 修复 `importpath.Normalize` 丢失的 `.` / `..` 解析：跨平台的 Windows 路径实现不再走
  `filepath.Clean` 之后，`D:\Games\..\Other` 与 `D:\Other` 会被算成两个路径，
  重复检测漏判；已补回并加回归测试。
- 重新生成 `frontend/bindings` 并提交，使其与生成器输出严格一致
  （重跑 `wails3 generate bindings -clean=true -ts` 应为零差异）。

### 2026-10-09 — 仓库更名为 YukiHub for Desktop，补齐漏替换的仓库链接

- 仓库由 `YukiHub-for-Windows` 更名为 **`YukiHub-for-Desktop`**（GitHub 与 GitCode 同名，
  旧地址由平台 301 重定向）。产品定位与显示名未变：Windows 10/11 与 Linux amd64 桌面端，
  `internal/version.AppDisplayName` 仍为 `YukiHub Desktop`。
- 补齐建独立仓库时漏掉的一批链接替换。下列值的语义都是「**本仓库地址**」，
  原先却指向 Android 手机版仓库 `xm486/YukiHub`（`docs/fork-setup.md` 早就写明
  「若新建独立仓库，需全局替换」，当时只换了 README）：
  - `internal/version.RepositoryURL`（关于面板与 `GetVersionInfo` 的 `repo` 字段）
  - `internal/version` 中 User-Agent 尾部的仓库 URL
  - 设置页「开源仓库」按钮（`routes/settings.tsx`）
  - Linux 包元数据 `build/linux/nfpm/nfpm.yaml` 的 `homepage`
  - `sync/version.json` 的 `downloads.github`
- 文档与合规材料里的 `YukiHub for Windows` 统一为 `YukiHub for Desktop`：
  `NOTICE` 首行与修改声明、ADR-0001 标题、`AGENTS.md` 项目身份段、
  `sync/version.json` 的 changelog。
- **User-Agent 前缀（`xm486/YukiHub/<版本>`）保持不变**：它是发往第三方服务的客户端
  标识，服务端可能据此识别与限流，改动风险大于收益；UA 尾部仍带 `(desktop)` 以区分两端。
- 本地工作目录同步更名为 `YukiHub for Desktop`。

### 2026-10-09 — 更新检查改为读 releases/latest，版本号段数放开（对齐手机版）

- **版本号段数不再固定**：`0.1` / `0.2.5` / `0.1.2.5` 都合法（手机版历史上用过
  `v0.1.0`、`v0.2`、`v0.1.2.5`、`v0.2.6.1`）。原先版本比较走严格 SemVer
  （`semver.IsValid("0.1")` 为 false），两段版本号会直接被判成非法。现改为
  `internal/service/version_compare.go` 的**逐段数值比较**：段数不足一方按 0 补齐、
  构建元数据忽略、预发布沿用 SemVer 优先级，并保留两条既有约定
  （同核心版本下 dev 构建优先于正式版；裸 `dev` 不提示更新）。
- `release.yml` 的 tag 触发由 `v*.*.*` 放宽到 `v*`；`validate` 的正则改为
  「至少两段、每段数字、最多 6 段」；`sync/version.json` 的一致性校验
  （`release.yml` / `update-test.yml`）改为**归一化后比较**，`0.1` 与 `0.1.0` 不再误报。
- **检查更新改为读代码托管平台的 `releases/latest`**（与手机版同一形态），
  不再依赖自建更新服务：GitHub 走 `api.github.com`，GitCode 走 `gitcode.com/api/v5`。
  新增 `internal/service/update_release_source.go`；版本取 `tag_name`、更新说明取
  `body`、发布日期取 `published_at`（GitCode 无该字段则退回 `created_at`）。
  GitCode 的 `assets` 会附带四个源码包（`type=source`），只认 `type=attach` 的真附件。
- 新增设置项 **`update_source`（更新源）**：单选，默认 **GitCode**（与手机版一致），
  可切 GitHub；**选定的源失败时自动改用另一个源**（手机版是提示用户手动换源）。
  设置页在「启动时自动检查更新」上方新增该项。
- **应用内自动更新降级为可选**：只有 release 里带了更新清单附件（文件名含 `manifest`）
  且平台是 Windows 时才提供「立即更新」，否则界面只给「打开发布页」。
  原先 Windows 在没有清单时**一个按钮都没有**（只剩「跳过此版本」），一并修掉；
  同时移除只指向 Gitee 的下载按钮（`downloads.gitee` 不再产生）。
- 移除 `getUpdateURLs` / `defaultUpdateURLs`：填充源改为托管平台后它们不再被调用。

### 2026-10-09 — 修复 Windows 安装版构建（上游 `build.bat` 的 CLI 残留）

Windows 安装版在 CI 上直接退出，报
`ERROR: Missing installer CLI payload: ...\build\windows\payload\amd64\yukihubcli.exe`。
编译与便携版都是好的，卡在打包前的前置校验。

- `scripts/build.bat` 的 `:build_installer_package` 仍在校验并拷贝
  **`yukihubcli.exe`**。上游 LunaBox 的载荷里确实带 CLI（`lunacli.exe`），
  我们是后来把 CLI 整条链路下线时才删掉构建步骤与载荷打包的，
  这里的校验被漏掉 —— 结果是 `installer` / `all` 模式与流水线的
  `installer-package` 三种入口**都会在 NSIS 之前就退出**。已移除该校验与拷贝；
  `YukiHubUpdater.exe` 的提示也不再声称 "signed"（签名现在是可选的）
- `scripts/build.bat` 改为**纯 ASCII**：`cmd.exe` 按系统 ANSI 代码页解析批处理，
  中文注释在多字节代码页下属于隐患，连上游带来的两处注释也一并换成英文
- `scripts/build.sh` 的过时提示 `Building GUI and CLI...` 改为 `Building GUI...`
  （该函数实际只构建 GUI 二进制）
- Windows 矩阵 `strategy.fail-fast` 改为 `false`：见 `docs/fork-setup.md` 第 5 节
- 清理 CLI 残留：删除 `skills/yukihub/`（教 AI 调用已下线的 `yukihubcli`，
  未被任何代码或流水线引用）；`frontend/i18next.config.ts` 的 `preservePatterns`
  里 `settings.portableSetup.toast.*` 通配收窄成两个真正动态引用的键，
  顺带清掉 `cliRegistered` / `cliUnregistered` / `cliRegisterFailed` /
  `cliUnregisterFailed` 四个死键

### 2026-10-11 — 更新检查新增开发版通道；启动配置页可执行文件可编辑

- **更新检查新增「检测开发版更新」开关**（`check_dev_updates`，默认关）：勾选后改读
  托管平台的**滚动开发版预发布 `dev-latest`**（`autobuild.yml` 每次构建覆盖同一标签，
  永远标 pre-release，不影响正式版通道）。`releases/latest` 会跳过预发布，所以开发版
  通道改走**发布列表**接口；开发版 tag 本身不含版本号，客户端从发布说明的「版本号」
  一行、发布标题、附件名三处依次提取（取出的值还必须能被版本比较器解析，避免误抓
  正文里的其它数字）。开发版通道**不提供应用内自动更新** —— 自建更新服务的 manifest
  只发布正式版，拼出来必然 404 —— 只给「打开发布页 / 下载」。检查结果新增
  `update_channel`（`stable` / `dev`），界面据此标注本次走的是哪个通道。
  自定义更新源（`update_check_url`）不受该开关影响，固定按正式版处理。
- **开发版按「构建时间」判断是否有更新**（用户提出：开发版没有固定版本号，拿它比版本
  不可靠）。CI 生成的是 `<基础版本>-dev.<提交数>+<短提交>`，打第一个 tag 之前基础版本
  恒为 `0.0.0`，提交数在 rebase / squash 之后还会回退 —— 所以改成：① 提交号相同即
  同一个构建，不提示（挡掉「先编译、几秒后才发布」的误报）；② 两边都有时间比时间；
  ③ 时间拿不到才退回版本号比较。为此 `scripts/build.sh` / `build.bat` 额外注入
  **`version.BuildTimeUTC`**（RFC3339/UTC）—— 原来的 `BuildTime` 是给人看的本地时间
  裸字符串，跨时区没法跟发布平台的 `published_at`（UTC）比较。
- **「启动配置」页的可执行文件改为可编辑**：原先这里只做只读展示，改 `game.path`
  必须切到「编辑」页 —— 同一份数据两处两种行为。现在两页共用同一个输入控件与
  同一套路径换算（`frontend/src/utils/gamePath.ts`，「编辑」页改为引用它，规则不再
  分叉）：可直接手输、点按钮重选、在资源管理器中定位。标签也统一成「可执行文件」。

### 2026-10-11 — 产物同步发布到 GitCode（镜像仓库）

GitCode 只做**发布落点**，CI 仍在 GitHub 上跑。原因是 GitCode 的 CI（AtomGit Action）
接不了这套流水线：

- 工作流目录是 `.gitcode/workflows/`，**不读 `.github/workflows/`**；`uses` 要写成
  `checkout` / `setup-go` 这种 GitCode 插件名，`actions/checkout@v4` 这类 GitHub
  命名空间解析不到；上下文从 `github.*` 变成 `atomgit.*`，写输出用 `$ATOMGIT_OUTPUT`
- **托管运行器只有 Linux**（`ubuntu-latest` / `euler-latest`，三段式 `[ubuntu-latest, x64, small]`），
  没有 Windows —— 便携版 / 安装版 / NSIS / MinGW / SignPath 全跑不了
  （能搬的只有 `frontend.yml`、`internal.yml` 的 `checks-linux`、`linux-package.yml`、
  `migration-e2e.yml` 这几条 Linux 侧的）
- 该能力还需要先向 AtomGit 客服申请开通

实现：`scripts/publish-gitcode-release.sh` 调 GitCode OpenAPI v5 —— 查 Release 是否存在，
不存在则 `POST /releases`、已存在则 `PATCH /releases/{tag}` 更新名称与说明，再逐个文件
`GET /releases/{tag}/upload_url` 拿 OBS 预签名地址、`PUT` 上传。接入 `release.yml` 的
「创建 Release」作业与 `autobuild.yml` 的「发布开发版」作业，都是 `continue-on-error`，
GitCode 故障不拦发布。**未配置 `GITCODE_TOKEN` 时脚本自己跳过并正常退出**，所以不配也
不影响现有流水线。`update-test.yml` 刻意不同步（发的是 `1.11.0-test.1` 这类一次性测试版）。

两个由接口能力决定的限制：① GitCode **没有删除 Release / 附件的接口**，滚动预发布
`dev-latest` 每构建一次就多攒一批附件，需要偶尔去网页端删掉重建；② **`dev-latest` 标签
不会跟着移动**（GitHub 那边靠 `git push -f`，API 做不到，`target_commitish` 只在标签
不存在时生效），但下载到的产物与发布说明始终是最新的。

### 2026-10-11 — 修 GitCode 上传卡死；构建时间统一成 UTC+8

**GitCode 上传卡死**（真机报 `curl: (28) ... with 0 bytes received`、`%{http_code}` 停在 100）：
Release 建得出来，卡在附件上传。对照官方 `gitcode-api` SDK 的实现（PUT + 原始字节 +
原样转发 `upload_url` 返回的 headers，httpx 客户端）确认接口用法无误，问题在 curl 侧：

- 强制 `--http1.1`（curl 默认协商 HTTP/2，大文件走 h2 时对方流控不开就会「一个字都不回」）
- 显式 `-H "Expect:"` 去掉 `Expect: 100-continue`（curl 对较大 PUT 会发，httpx 不发）
- 单个文件上限 5 分钟 → 30 分钟，另加「低于 4 KB/s 持续 60 秒判定卡住」
- 上传前打印总量总览；每个文件打印开始/完成 + 耗时 + 均速；按体积从小到大传

**构建时间统一 UTC+8**：发布说明里的构建时间（`autobuild.yml` / `update-test.yml`）与
「关于」面板用的 `version.BuildTime`（`scripts/build.sh` / `build.bat`）都改成按 UTC+8 生成
（CI 运行器是 UTC，之前显示差 8 小时）。**格式保持 `%Y-%m-%d %H:%M:%S` 不变** ——
开发版更新检查的兜底解析认的就是这个格式，加时区后缀会破坏它。

- **GitCode 镜像上传：确认慢在链路，不在脚本**（用户给了真机日志）。两次都卡在第一批
  大文件上（第一次 5 分钟超时、`%{http_code}` 停在 `100`；改正协议与超时后第二次仍是
  30 分钟超时）。翻官方文档确认接口就是「GET `upload_url` → 按返回的 `url` 与
  `headers` 发 PUT」，官方 `gitcode-api` SDK 也是这么写的；写了个本地 mock 把上传端
  做成「收下就不回」，实测 `--speed-limit 4096 --speed-time 60` 在 60 秒内就会报
  `Operation too slow` —— 它在真实日志里 30 分钟一次都没触发，说明数据一直在流，
  只是吞吐只有十几 KB/s（GitHub 运行器在境外 → GitCode 对象存储在国内）。
  于是改成多路并发 + 整体时间预算 + 逐文件上报实际发出字节数 + 跳过已有同名附件，
  并给出 `gitcode_mirror` 输入（可只镜像 Windows amd64，约 78 MB）

### 尚未修改、计划修改


- 产品界面与交互仍为上游形态，尚未替换为 YukiHub 的视觉与信息架构。
- 游戏领域模型仍为上游语义，尚未合并 Android 版的视觉小说条目语义
  （三语标题、NSFW、`play_status` 五态等）。
- macOS / iOS 不在支持范围内（不恢复其平台代码与 CI 矩阵）；
  Linux（amd64）已于 2026-10-06 恢复，见 `docs/decisions/0004-restore-linux-support.md`。
- 应用图标、启动图、界面内品牌插画仍为上游占位资源，需要替换为 YukiHub 素材。
- 应用内尚无独立的"开源许可"展示界面。

## 与上游的关系

本项目定位为**硬分叉，不与上游保持同步**，也不向上游回灌代码。
理由与取舍见 `docs/decisions/0001-fork-lunabox-as-windows-baseline.md`。

上游项目不对本产品提供任何担保、支持或背书。
