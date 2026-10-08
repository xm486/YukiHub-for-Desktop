# YukiHub Desktop 路线图

阶段划分以"可验收的结果"为单位，不给日期承诺。上一个阶段未通过验收，不进入下一个阶段。

## 现状（阶段 0 已完成）

- [x] 完成三条技术路线的评估并形成决策：[ADR-0001](decisions/0001-fork-lunabox-as-windows-baseline.md)
- [x] 以 LunaBox v1.13.0 建立硬分叉基线，仓库纳入版本控制
- [x] 完成去品牌化（模块名、应用标识、协议、数据目录、CLI、绑定路径、构建变量、安装器）
- [x] 移除上游硬编码凭据与指向上游更新服务/托管云的默认地址
- [x] 补齐 AGPL 合规材料（`NOTICE`、`THIRD_PARTY_LICENSES.md`、`third_party/`、合规说明）
- [x] 修复 CI 中"只编译不执行测试"的问题，新增 `gofmt` / `go vet` 门禁
- [x] 全仓库 Go 代码 `gofmt` 通过

已知遗留问题（进入阶段 1 前必须处理）：

- Go 测试从未被真实执行过，可能包含**已经失效的用例**，需要逐条修复或删除。
  在测试全绿之前，任何裁剪都缺少安全网。
- 产品界面、领域模型仍是上游形态，尚未替换为 YukiHub 形态。

---

## 阶段 1：可构建、可安装、测试可信

目标：得到一个"干净的 Windows-only 工程"——能构建、能安装、测试真实运行且通过。

验收标准：

1. `gofmt -l .` 输出为空；`go vet ./...` 无错误。**（已验证通过）**
2. `go test ./... -count=1` 与 `cd updater && go test ./... -count=1` 全部通过。**（已验证通过）**
   - 失效用例允许删除，但必须在提交信息中说明原因；不允许跳过或标记为 Skip 来"凑绿"。
3. `wails3 build` 在本机与 CI 上成功产出可执行文件。**（本机已验证通过）**
   - 注：初次验证只跑到 `wails3 build`，**没跑完整的 `pnpm run build`**。
     后来构建安装包时才发现 `AddGameModal.tsx` 引用了已被重命名的品牌图片
     （`luna1/luna2.webp` → `brand-1/brand-2.webp`，去品牌化时改名却没同步引用），
     导致 `vite build` 报 `Could not resolve`。**CI 的 `pnpm run build`
     步骤同样会失败**，属必现问题。已修复（提交 `53e08d3`），
     并用脚本扫描确认全部 36 个相对资源引用中仅这 2 处失效。
4. NSIS 安装包可以完成"安装 → 启动 → 卸载"，且卸载后用户数据目录按预期处理。
   **（进行中）**
   - 产物：`build/bin/YukiHub-0.1.0-windows-amd64-setup.exe`（37.9 MB）
   - 本机无法直接运行 `scripts/build.bat`（安全策略拦截 `wmic.exe`，
     且 `pnpm install` 触发安全删除 shim 超时），已用等价的分步脚本完成构建，
     流程与坑记录在 `docs/fork-setup.md`
   - [x] 安装：用户在 D 盘实测通过
   - [x] 启动：实测发现 2 个缺陷，均已修复（提交 `35070b7`）
         · 未配置更新源时弹出 `failed to fetch update info from all sources: %!w(<nil>)`
           —— 清空默认更新地址时漏了"无源可用"分支，把"没有源"误报成"所有源失败"
         · 标题栏与侧边栏显示 "LunaBox" —— 渲染的是上游文字 logo 图片，
           文本替换覆盖不到二进制资源
   - [ ] 用重新构建的安装包复测启动
   - [ ] 卸载流程，以及卸载后 `%APPDATA%\YukiHub` 与 `%LOCALAPPDATA%\YukiHub` 的处理
5. macOS / iOS / Linux 相关代码与 CI 矩阵移除完毕。**（已完成，见下方；
   Linux（amd64）部分已于 2026-10-06 恢复，见 [ADR-0004](decisions/0004-restore-linux-support.md)）**
6. 界面上的"通用跨平台"表述与残留的上游素材占位清理完毕。**（已完成）**
   - [x] `PortableSetupPanel`（5 处三元 + 1 处条件渲染）、`GameSettingsPanel`、
         `TopBar`、`routes/__root.tsx`、`routes/game.tsx`、`routes/settings.tsx`
         的平台分支；四语言删除 6 个 macOS 专属孤儿键
   - [x] `GameLaunchPanel` 约 20 处 `isDarwin` / `isLinux` 及配套的 Wine runner
         选择 UI——连同 Go 侧 Linux 专属的 Steam Proton 兼容层整链删除
         （提交 12e1337，982 行重写为 360 行，四语言再清 49 个孤儿键）
   - [x] `UpdateDialog` 的 3 个平台变量与恒不显示的下载按钮区块
   - [x] 替换上游品牌素材（文本替换覆盖不到的二进制资源）：
         `appicon.png` / `appicon-dark.png` 与 `build/windows/icon.ico`
         已换成手机版 YukiHub 图标（提交 17629df）；
         文字 logo（`topbar-title*.png`）已删除，改为代码渲染 "YukiHub"；
         侧边栏 logo 与托盘图标改为雪花标识。
  - [x] 添加游戏弹窗插画 `brand-1.webp` / `brand-2.webp`（2026-09-29）：
        改为内联 SVG（`components/branding/AddGameIllustration.tsx`，
        「本地导入」画窗口+播放三角，「远程导入」画云+下载箭头），位图已删除。
        与文字 logo、未萌图标同一套路：**不新增二进制资源**，颜色随主题
  - [x] 复查是否还有其它上游素材占位残留（2026-09-29）：扫描 `frontend/src/assets`
        下全部素材，按文件名反查引用（源码 + `uno.config.ts` + `build/`），
        只剩 4 个孤儿：`dlsite-logo` / `erogamescape-logo` / `steam-logo` /
        `touchgal-logo` —— 都是上一轮从可选清单裁掉的来源，图标映射也一并
        移除了，故随位图删除（若将来重新启用这些来源，图标需重新引入）。
        其余素材（应用图标、6 个来源图标、5 个导入器图标）均有引用

### 阶段 1 验证记录（2026-09-28）

本机装上 MinGW-w64 并启用 `CGO_ENABLED=1` 后完成了此前无法执行的验证：

| 检查 | 结果 |
| --- | --- |
| `gofmt -l .` | 无输出 |
| `go vet ./...` | 无输出（修复了 1 处上游遗留的 unsafeptr 告警） |
| `go build ./...` | 通过（含 DuckDB 的 CGO 与 Wails） |
| `go test ./... -count=1` | 28 个含测试的包全部 `ok`，0 个 FAIL |
| `cd updater && go test ./... -count=1` | 通过 |
| `wails3 build`（离线） | 通过，产出 `bin/YukiHub.exe`（144.7 MB，PE 头有效） |

`wails3 build` 的执行链路：读取 `build/config.yml` → 重新生成绑定（601 包 /
240 方法）→ 生成 Windows 资源 `syso` → `go build` → 落盘 `bin/YukiHub.exe`。
体积 144.7 MB 主要来自 DuckDB 静态链接（amd64 走静态链接，不需要随包分发
`duckdb.dll`）与嵌入的前端资源；该路径已在 `.gitignore` 中。

重要结论：**上游那 114 个测试文件第一次被真实执行，结果是全绿的。**
之前"可能已经失效"的担心不成立，平台裁剪也已有编译与测试双重保障。

注意：`go build` 需要 `frontend/dist` 存在（`main.go` 有 `//go:embed all:frontend/dist`）。
未构建前端时可在本地建一个占位目录临时绕过，该目录已在 `.gitignore` 中。

已完成的部分：

- [x] 删除 64 个 Windows 构建不参与的 Go 文件（darwin / linux 实现与对应测试）
- [x] 删除 `build/darwin`、`build/ios`、`build/linux` 与 `lib/{linuxamd64,linuxarm64,macarm64}`
- [x] 删除 `scripts/build.sh`、`scripts/patch-wails-linux-tray.sh`
- [x] `release.yml` / `autobuild.yml` 移除 macOS 与 Linux 构建作业，产物断言收敛为 4 个 Windows 产物
- [x] `Taskfile.yml` 移除 darwin / linux 分支，`.gitignore` 清理失效路径
- [x] `docs/workflow.md` 移除 Linux 渲染验证与 macOS 透明窗口两节

Wine / Proton / CrossOver 工具链清理（2026-09-28 完成）：

- [x] 删除 `internal/utils/protonutils/` 整包
- [x] 删除 `internal/service/compattools/` 整包
- [x] 删除 `internal/service/compatibility_tools.go`
      （`GameCompatibilityToolsInfo`、`GetGameCompatibilityTools`、`OpenGameCompatibilityTool`）
- [x] `integration_service.go` 移除 `LocalProtonTool` / `localProtonToolsFromUtils` /
      `GetLocalProtonTools`
- [x] `appconf.AppConfig` 移除 `WineRunnerPath`、`WinePrefix`、`WinetricksPath`、
      `ProtontricksPath`、`CrossOverRunnerPath`、`CrossOverBottle` 六个字段，
      删除 `MigrateLegacyCompatibilityConfig` 与 `wine_detect_*.go`
- [x] 删除 `launcher/strategy.go` 中已无调用者的 `newStrategyError`（Wine 缺配置错误路径的遗留）
- [x] 前端移除对应入口：`GameLaunchPanel` 的 Proton 工具发现与兼容层快捷工具面板、
      `GameSettingsPanel` 的 Wine / CrossOver / winetricks / protontricks 设置块、
      `useAppRuntimeEffects` 的 `wine_runner` 事件分支、`bindings/integration.ts` 的包装函数
- [x] 四语言文案清理 36 个孤儿键，四份文件键结构保持一致

明确保留（有意为之，不是遗漏）：

- `models.Game` 上的游戏级 `wine_runner` / `wine_args` / `wine_prefix`，以及对应的
  数据库列与 `cloudsync` 快照字段。它们是**数据契约**的一部分：导入 Playnite /
  PotatoVN / Vnite 等来源时可能带上这些值，云同步与备份也依赖这些字段做往返。
  删除它们属于数据语义变更，需要按 `docs/mobile-yukihub-migration.md` 的要求两端评审，
  不属于本轮范围。
- Steam 相关能力（`GetGameSteamCompatibility`、`SetGameSteamCompatibilityTool`、
  `OpenGameSteamProtonPrefix`、`RestartSteamClient`）在 Windows 上仍然有效，全部保留。

剩余的部分：

- [x] 执行 `wails3 generate bindings -clean=true -ts` 重新生成绑定 —— **已完成**。
      处理 601 个包 / 24 个服务 / 240 个方法 / 11 个枚举 / 96 个模型；
      已删除的 Wine、Proton 符号全部清除，`appconf/models.ts` 减少 30 行，
      `pnpm run typecheck` 通过。
      （上一轮记为"上游依赖有问题"是**误判**：真实原因是本机 Go 模块缓存被
      "半截解压"污染——zip 完整、解压目录只写了一半，因此报错会在不同包之间跳。
      清理后 `go install wails3@v3.0.0-beta.24` 仅 47 秒完成。
      排查脚本见 `~/.workbuddy/tools/gocheck/check_modcache.py`。）
- [x] 清除共享代码中的死分支 —— **已完成**（23 个文件，净减 410 行）。
      收敛 `runtime.GOOS` 恒真判断、删除恒假分支，并连带删除
      `IsMacAppBundlePath`（含 6 处调用点）、`repairStaleAppImageProtocolRegistration`
      与孤儿包 `internal/utils/tricksutils`。验证：gofmt / vet / build / test 全绿。
- [x] 复核游戏级 `wine_*` 字段的存废（2026-09-29）：**结论——保留列、不再单独处理，
      随阶段 6 的 cloudsync 收敛一并清理**
      - 事实：`wine_runner` / `wine_args` / `wine_prefix` **完全不在 YukiHub（Android）
        契约里**（`exporter/yukihub.go`、`importer/yukihub.go`、`models/yukihub` 均无引用）。
        它们只存在于三处上游遗留：数据库 `games` 列与迁移、上游 `cloudsync` 快照
        （桌面端 ↔ 桌面端）、以及由此牵出的 `models.Game` 与列表查询
      - 判断：删列是不可逆迁移，而 Android 方向本来就不读写这三个字段，
        现在删没有任何收益；上游 `cloudsync` 本身已列入阶段 6「移除墓碑/脏表、
        改用 Android 侧哈希比对」，到那时这些字段会随整链一起失活
      - 因此本项不作为独立待办：字段与列保持现状，不在 Windows-only 语境下
        为它们新增任何读写代码

## 阶段 2：领域模型统一与双向数据迁移

目标：桌面版与 Android 版之间的数据可以双向流动，且语义明确。

验收标准：

1. 统一的游戏领域模型落地：三语标题（中文 / 原名 / 罗马字）、别名、NSFW 标记、
   五态游玩状态、标签、封面来源、元数据来源。
2. Android 版备份（schema 5 JSON）导入桌面版：万级样例 0 丢失、0 重复。
3. 桌面版数据导出为 Android 版可识别的备份格式（可被 Android 版导入）。
4. 游玩记录的合并语义有测试覆盖：
   - 总时长取最大值而非覆盖
   - `playtime_reset_at` 之后的历史会话不再计入
   - 会话 UUID 幂等，重复导入不产生重复记录
   - 时区与毫秒/秒单位换算正确
5. 路径模型差异有明确处理：Android 的 SAF 树 URI 与 Windows 绝对路径之间的映射与
   不可达降级策略。
6. 冲突裁决只有一套实现（不允许桌面版与 Android 版各有一套合并算法长期并存）。

设计细节见 [mobile-yukihub-migration.md](mobile-yukihub-migration.md)。

### 进展

- [x] 契约字段落地（迁移 177，导入方向）：`legacy_local_id`、`source_device_id`、
      `playtime_reset_at`、`hidden` 四个列与 `models.Game` 字段，贯穿
      建表 / INSERT / SELECT / UPDATE / 云同步快照 / 测试 helper；导入器正确填充，
      `playtime_reset_at` 为 0 时落 NULL（提交 e520b1e）
- [x] 修正契约文档的单位错误：`play_sessions.duration` 与 `games.total_play_time`
      **均为毫秒**（原文误写为"duration 是秒、两者单位不同"），已按手机版
      `GameRepository` 源码更正并补记 Android 侧的清零过滤行为
- [x] 统一领域模型（三语标题 / 别名 / NSFW / 五态 / 标签 / 封面来源 / 元数据来源）复核
      （2026-09-28，见下方"领域模型复核结论"）
- [x] 导出方向：`internal/service/exporter/yukihub.go` 产出 schema 5 快照
      （`Build()` + gzip `Export()`）。单位毫秒换算、清零过滤、每游戏 30 条会话上限、
      6→5 态映射、`favorite` / `hidden` / `nsfw` / `local_id` 回填；
      `root_uri` 恒空（理由见迁移文档），无标题条目跳过。
      测试：单测 8 项 + 集成 3 项（含**导出→导入往返**，用真实 importer 消费快照）
- [x] 合并语义测试：**总时长取最大值**（`merge_sessions` 动作：会话并集去重 +
      聚合补偿差额 → 最终 = max(桌面已录, 快照 total)，测试覆盖）、
      会话 UUID 幂等（skip 动作重导幂等，测试覆盖；数据库层无 UUID 唯一约束，
      两层近似保证已记录在迁移文档）。导入器此前丢弃 `samePathAction`
      （无合并路径）、标题匹配与预览不一致（预览判重、导入重复），均已修复
- [x] SAF 树 URI 与 Windows 绝对路径的映射与不可达降级：导出侧不写 Windows 路径
      （`root_uri` 恒空 → 对端按标题匹配）。遗留 `legacy_root_uri` 回填见迁移文档
- [x] 万级样例导入性能与 0 丢失验证（见下方"阶段 2 验证记录"）

**原阻塞项已决策**（见 [ADR-0002](../decisions/0002-android-authoritative-and-sync-backend.md)）：

- 数据权威源 = **Android 端**，schema 5 快照是权威契约
- 同步后端 = **本项目自有服务器**（Android 版已在用的 `/api/sync/*`），不接上游托管服务
- 冲突裁决收敛为**一套**（以 Android 侧哈希比对为准），
  上游 `cloudsync` 的墓碑/脏表长期要替换 —— **该验收项实际落到阶段 6**，
  阶段 2 只完成契约与文件级迁移
- 元数据缓存沿用 Android 版 `VnMetadata` 字段

结论：文件级导入导出（验收 2、3）**不依赖服务端，是本阶段主线**。

### 阶段 2 验证记录（2026-09-28）

万级样例验证落在 `internal/service/test/yukihub_scale_test.go`。
快照由测试现场生成：10,000 款游戏（每款一条唯一 vndb 元数据、2 条标签、
`i%3` 决定 2/1/0 条明细会话、`i%4` 秒的时长差额触发聚合补偿会话），
合计 17,501 条会话、20,000 条标签、10,000 条元数据源。
关键在于**走真实 Committer 落库**（`newTestImporterDependencies`）后再查库核对，
而不是只检查内存里的 `ImportItem`——后者不会暴露 staging 落库环节的字段丢失。

| 用例 | 覆盖点 | 结果 |
| --- | --- | --- |
| `TestYukiHubImportScaleNoLossNoDuplicates` | 10k 导入 0 丢失、0 重复 | 通过（0.76s） |
| `TestYukiHubImportScaleIsIdempotent` | 同一快照重复导入幂等 | 通过 |
| `TestYukiHubImportPersistsMobileContractFields` | 四个契约列落库 | 通过 |

`TestYukiHubImportScaleNoLossNoDuplicates` 逐项核对：games=10,000、play_sessions=17,501、
game_tags=20,000、game_metadata_sources=10,000；游戏名与 `legacy_local_id`
各自去重后仍为 10,000（0 丢失、0 重复）；`hidden` / `nsfw` / `playtime_reset_at`
非空计数与期望一致；并抽查首条游戏确认 `legacy_local_id` / `hidden` / `nsfw` /
`playtime_reset_at` 落到正确列上。耗时 0.76s（导入侧日志显示 games 落库 122ms、
标签 121ms、会话 84ms、整体提交 512ms），远低于 90s 护栏。

`TestYukiHubImportScaleIsIdempotent` 确认重复导入 success=0 / skipped=10,000 / failed=0，
`games` 与 `play_sessions` 行数不变。

**过程中修复的缺陷**：契约字段（`legacy_local_id`、`source_device_id`、
`playtime_reset_at`、`hidden`）此前只被填进 `models.Game`，但导入器的
staging 表（`temp_import_games` / `temp_update_import_games`）与
INSERT / UPDATE 语句都不含这些列，落库时被**静默丢弃**——这会让身份键
（`legacy_local_id`）与清零语义、隐藏标记在桌面端全部失效。
已补齐四列（`internal/service/importer/persistence.go`），
并对 `playtime_reset_at` 增加 `nullableTime` 处理，使 0 落 NULL 而非 1970。
这正是"只测内存对象测不出来"的一类缺陷。

### 领域模型复核结论（2026-09-28）

验收项 1 的七个子项逐条核对（映射细节见
[mobile-yukihub-migration.md](mobile-yukihub-migration.md) 的"字段映射"）：

| 子项 | 落点 | 结论 |
| --- | --- | --- |
| 三语标题 | `name` + `aliases`（原名 / 罗马字并入别名数组） | 信息不丢，但**不做结构化区分**（设计如此） |
| 别名 | `games.aliases`（JSON 数组） | 完整 |
| NSFW | `games.is_nsfw` | 完整（导入取快照 `nsfw`，导出回填） |
| 五态游玩状态 | `games.status`（与 Android 同为 5 态） | 完整，双向为恒等映射（`migration179`） |
| 标签 | `game_tags` 表 | 完整（快照 `tags` 文本拆分去重 + 元数据 `tagsText` 合并） |
| 封面来源 | `cover_url` / `cover_source_url` / `source_type` | 完整；本地封面按契约不迁移 |
| 元数据来源 | `source_type` / `source_id` / `game_metadata_sources` | 导入完整；导出侧 `metadata_cache` 待两端结构统一（见迁移文档） |

复核中发现并修复一处**真实缺陷**：`favorite` 在导入方向被完全丢弃
（导出侧读 `system:favorites`，导入侧不碰分类表），导致往返丢失收藏。
已补齐（`ImportItem.Favorite` → `game_categories`），语义为"只加不删"。
详见迁移文档"现状核对 ⑦"。

遗留（已在迁移文档记录，非本轮范围）：
- 三语标题不做结构化区分，`original_title` 导出时取第一个别名（对 VNDB / Bangumi
  等主源恰好是原名，属可用启发式）。
- `metadata_cache` 导出待两端元数据缓存结构统一后再补。

## 阶段 3：产品层重建

目标：界面与交互是 YukiHub，而不是"改了名字的 LunaBox"。

验收标准：

1. 首页 / 库 / 游戏详情 / 统计 / 设置五个主界面完成 YukiHub 化改造。**（已完成，见进展）**
2. 上游素材（应用图标、界面插画、启动图、截图）全部替换为 YukiHub 素材。**（已完成，见进展）**
3. 元数据来源合并为单一入口（Bangumi、VNDB、月幕、Hikarinagi、Steam 等），
   优先级与缓存策略统一。**（后端已合并，见进展；前端本已是单一注册表）**
4. 游玩启动与时长统计在 Windows 上按真实进程行为工作：
   - 启动、退出、崩溃、Switch 用户、开机自启场景均有验证
   - 后台/前台时长语义与 Android 版规则对齐
   **（静态部分：启动策略接线已补测；端到端真机验证待做，见进展）**
5. 同步协议落地：至少支持一种用户自持的同步后端（WebDAV 优先）。
   **（上游能力已具备且为默认项，端到端真机验证待做，见进展）**
6. 应用内"关于与开源许可"面板完成，展示版本、上游署名与 AGPL 声明。**（已完成）**

### 进展

- [x] 设计令牌对齐手机版（2026-09-28）：从手机版 030p 源码的
      `app/src/main/res/values/colors.xml` 提取原始调色板，落到
      `frontend/uno.config.ts`
      - `brand` 中性色板整体改为冷调深蓝：`brand-900 = #0B1020`（`yh_bg`）、
        `brand-800 = #171E33`（`yh_card`）、`brand-700 = #2D3658`（`yh_line`）、
        `brand-400 = #9AA4BF`（`yh_text_muted`）。暗色模式的主背景与卡片
        因此直接变成手机版那套深蓝，这是"像不像 YukiHub"的决定性一步
      - `primary` 改为手机版 `yh_primary` 柔和蓝：`primary-300 = #8AB4FF`
        （手机版原值，供暗色模式强调文字），`500/600` 压深一档
        （`#6E96E8` / `#5A7CC9`）以保住亮色模式"白字蓝底"按钮的对比度
      - 新增 `secondary`（`yh_secondary` 樱粉 `#FF8AB3`）与 `yh` 原始令牌命名空间
      - 新增两条签名渐变 rule：`yh-primary-gradient`（主按钮，
        取自 `bg_button_primary.xml`）与 `yh-hero-gradient`（首页横幅，
        取自 `bg_home_gradient.xml`）
      - 注意：手机版是**暗色优先**（`values-night/colors.xml` 为空），
        而桌面版默认主题是 `light`（`appconf/config.go`），
        故亮色模式按同色系做了浅色版，不是手机版的直接搬运
- [x] 侧边栏视觉对齐（2026-09-28）：导航项选中态改为手机版 `bg_sidebar_item` 的做法
      ——半透明蓝底（`primary-500/12`）+ 左侧强调条（`border-l-2`），
      取代上游的"整块实心灰底"；侧栏底色改用 `dark:bg-yh-sidebar`（`#10172A`）
- [x] 首页 YukiHub 化（2026-09-28）：按手机版首页信息架构重做，拆为四个组件
      ——`HomeHeroCard`（横幅渐变 + 继续游玩主胶囊）、
      `HomeQuickLaunchRail`（横向快速启动滑轨）、
      `HomeTodayStatsCard`（今日数据）、`HomeHeatmapCard`（热力图）；
      新增 `yh-glass` / `yh-glass-inner` / `yh-glass-chip` / `yh-chip` /
      `yh-hero-gradient-light` 等令牌，亮色模式补同色系浅色版；
      清理旧首页组件（`HomeGameRailPanel` 等）与失效 hook
- [x] 游戏库 YukiHub 化（2026-09-28）：对齐手机版"筛选芯片 + 网格 + 右侧详情"
      - 新增 `LibraryStatusChipRow`：状态筛选从抽屉里提到页面顶部，
        做成手机版 `bg_chip` 风格的横向胶囊行，选中态用 `yh-primary-pill`
      - 新增 `LibraryDetailPanel`：对应手机版 `dialog_game_detail`，
        桌面端改为**常驻右侧栏**（`lg` 以上显示，`sticky` 跟随滚动），
        含封面 + 状态徽标、名称/简介、厂商/评分/发售/最近游玩信息行、
        启动游戏与"完整详情"两个动作
      - `GameCard` 增加状态徽标、评分芯片、封面底部排序字段覆盖条与
        悬浮启动按钮；`onActivate` 回调贯通 `VirtualGameGrid` → 库页面，
        非多选态点击卡片即展开右侧详情；多选态自动收起详情面板
      - 状态徽标样式集中到 `consts/gameStatusBadge.ts`，卡片与详情面板共用
      - 顺带修复 `HomeQuickLaunchRail` 引用了不存在的 `common.details` 文案，
        并清理 10 个死键（`home.welcome` / `home.noPlayRecord` /
        `home.recentPlayed` / `home.expandCoverPicker` / `home.collapseCoverPicker` /
        `stats.library.*`），`i18n:check` 恢复通过
- [x] 游戏详情页 YukiHub 化（2026-09-28）：详情页头部、状态切换芯片行、
      标签页签改用 YukiHub 令牌——状态芯片复用 `consts/gameStatusBadge.ts` 的
      `GAME_STATUS_BADGE_STYLES`，选中态用 `yh-primary-pill`、未选中态用
      `yh-chip`；正文卡片容器改用 `yh-glass`，与库页/首页视觉统一
- [x] 统计页 YukiHub 化（2026-09-28）：页面头部改为 `yh-glass` 玻璃条
      （圆形渐变图标 + 标题），概览指标卡、排行榜/标签分布/时段分布/趋势图
      五块内容全部改用 `yh-glass` 承载，取代上游的实心 `brand-50`/`brand-800` 面板
- [x] 设置页 YukiHub 化（2026-09-28）：页面头部改为 `yh-glass` 玻璃条
      （新增 `settings.subtitle` 四语文案），`CollapsibleSection` 折叠卡由
      `glass-settings-section` + 实心底色改为 `yh-glass`，折叠头图标改用
      `primary-500/300`、悬停态改为白色半透明；底部 GitHub 按钮改为
      `yh-primary-pill` 同款圆角胶囊。设置项面板本身为透明布局，无需改动
- [x] 修复"全站图标空白"（2026-09-29）：页面上大量图标显示为空白方块
      - 根因：pnpm 严格 `node_modules` 布局下，`presetIcons()` 的 node loader
        位于 `.pnpm/@iconify+utils@.../`，无法解析到
        `frontend/node_modules/@iconify-json/mdi`（`@iconify-json` 不在
        `.pnpm/node_modules` 提升列表里），图标集探测**静默失败**——
        生产构建 CSS 中 `.i-mdi-*` 规则数为 **0**，即全站图标从未生成过
      - 修法：`uno.config.ts` 显式注册 mdi 图标集
        （`presetIcons({ collections: { mdi: () => mdiIcons } })`）。
        注意值**必须是函数**：`@iconify/utils` 的 `loadIcon` 只会对函数求值后
        按集合查找，直接传集合对象会被误当成 loader 调用而返回空
      - 顺带修正 3 个在 MDI 中并不存在的图标名：
        `application-search-outline` → `magnify-scan`、
        `content-save-clock-outline` → `content-save-outline`、
        `database-star-outline` → `database-outline`
      - 校验：源码 186 个唯一 `i-mdi-*` 图标现已全部生成，缺失 0
- [x] 元数据来源合并为单一入口（2026-09-29）：验收项 3 的后端部分
      - 问题：`GameService` 与 `ImportService` 各持一份
        `getConfiguredMetadataSearchSources`，两份实现逐行几乎相同
        （同样的 switch、同样的 getter 构造、同样的服务判空），
        任何新增来源或选项改动都要改两处，属"两套来源清单长期分叉"的隐患
      - 修法：抽出唯一构建器
        `internal/service/metadata_search_sources.go` 的
        `buildConfiguredMetadataSearchSources(deps)`，
        连同 `metadataSearchSource` 类型一并迁入；两个服务改为传入依赖的薄封装
        （`GameService` 直接调用，`ImportService.metadataSearchSources()` 委托）
      - 优先级：直接沿用 `gamehelper.ConfiguredMetadataSources` 返回的用户配置顺序，
        构建器**不做任何重排**，界面上勾选的顺序即实际尝试顺序
      - getter / 缓存策略：统一由 `gamehelper.MetadataGetterOptions` 生成
        （代理、tag 上限、ErogameScape 基址、各源封面来源），两处不再各持一份；
        各来源自身的 token 缓存（Hikarinagi token、Steam tag catalog）保持来源内聚
      - 前端侧本已是单一注册表 `frontend/src/utils/metadataSources.ts`
        （`ALL_METADATA_SOURCES` / 图标 / URL 解析），无重复清单，无需改动
      - 测试：新增 `metadata_search_sources_test.go` 锁定两条契约——
        配置顺序不被重排、未注入服务的来源被跳过，以及
        `ImportService` 清单与共享构建器逐项一致
      - 验证：`gofmt` / `go vet` / `go build ./...` 无输出，
        `go test ./... -count=1` 全绿（退出码 0）
- [x] 元数据来源对齐手机版与设置清理（2026-09-29）：验收项 3 的收口
      - 问题：桌面端可选元数据来源有 8 个（含 Steam / DLsite / TouchGAL /
        ErogameScape），与手机版的 6 个（VNDB / Bangumi / Bangumi 镜像 /
        月幕 / Hikarinagi / 未萌）长期分叉；设置里还留着随来源收敛失效的
        `erogamescape_base_url`、`steam_cover_orientation`，
        以及无任何读写方的 `auto_upload_to_cloud`
      - 来源收敛：`SourceType` 新增 `bangumi_mirror` 与 `nextmoe`，
        可选清单统一为 6 项。`bangumi_mirror` 复用 Bangumi 的令牌与刷新逻辑、
        仅替换 API 基址（`https://api.bangumi.pro`）；`nextmoe` 走
        OAuth 授权码 + PKCE S256 与本地回环回调（固定端口 14792），
        catalog 请求带 Bearer 并按 1100ms 限速，令牌每次刷新轮换
      - 被裁来源（Steam / DLsite / TouchGAL / ErogameScape）只从可选清单摘掉，
        getter 实现、`SourceType` 枚举与展示映射全部保留，
        历史数据里的来源名称仍能正确显示
      - 设置清理：删除 `erogamescape_base_url`、`steam_cover_orientation`、
        `auto_upload_to_cloud` 三项配置及其 UI 与归一化函数；
        `window_maximised` 因 `main.go` 仍在读写（窗口尺寸记忆）而保留
      - 默认值统一：`config.go` / `gamehelper.ConfiguredMetadataSources` /
        前端三处对齐为 `[VNDB, Bangumi, Ymgal, Hikarinagi]`
      - 无法实现项：`www.nextmoe.com` 目前只是品牌门户，目录平台无 Web 前台，
        故 `nextmoe` 不提供来源页跳转（走 URL 解析的 default 分支）
      - 验证：`gofmt` / `go vet ./...` / `go build ./...` 无输出，
        `go test ./... -count=1` 全绿；前端 `pnpm typecheck` / `pnpm build` /
        `pnpm i18n:check` 通过，改动文件 eslint 0 error
- [x] 补齐 Windows 启动策略的测试覆盖（2026-09-29）：验收项 4 的静态部分
      - 背景：Magpie（超分）与 Locale Emulator（转区）是 **Windows 独有能力**，
        后端策略（`launcher/strategy_windows.go`）与前端 UI
        （`GameLaunchPanel` 每游戏开关 + `GameSettingsPanel` 路径配置）都已具备，
        但"字段 → 启动计划"的接线此前没有任何测试
      - 新增 5 条用例（`strategy_windows_test.go`）：
        游戏字段开启 Magpie 进入计划、单次启动开关可正反向覆盖 Magpie、
        未配置 LE 路径时**回落到原生启动**（不把不存在的 exe 写进计划）、
        单次开关可临时启用转区、转区与超分可叠加（LE 计划仍保留 Magpie 标记）
      - 验证：`gofmt` 无输出；`go test ./internal/service/launcher/... -count=1`
        11 条用例全通过；`go vet` / `go build ./...` / `go test ./... -count=1` 全绿
      - 时长语义核对（静态，2026-09-29）：`config.record_active_time_only`
        **默认 `false`**，即按墙钟计时（整段会话时长），与 Android 版
        "启动建会话、退出结算时长"的语义一致；开启后切换为
        `utils/timerutils/active_time_tracker.go` 的窗口焦点活跃计时，
        作为桌面端增强项，且已有 12 条单测覆盖
      - 仍待真机验证（不在本轮范围）：启动/退出/崩溃/切换用户/开机自启的
        端到端行为
- [x] 批量游玩时长接口（2026-09-29）：收口「列表不带时长」的缺口
      - 问题：库页右侧详情面板与大屏信息浮层都要显示总游玩时长，而
        `vo.GameListResponse` 只有条目本身没有时长，前端只能逐条走
        `GetGameStats`。大屏货架最多 500 条，逐条单查就是几百次往返
      - 后端：`internal/service/gamehelper/playtime.go` 新增
        `QueryGamesPlayTime`（批量、按 game_id 聚合）；`GameListRequest`
        加 `with_play_time`，`GameListResponse` 加 `play_times`
        （`game_id → 秒`），**默认关闭**，只有显式请求的调用方才多一次聚合查询
      - 语义对齐导出方向：单位沿用桌面端的**秒**；清零（`playtime_reset_at`）
        之前的会话不计入，判定与 `exporter` 的 `sessionsAfterReset` 一致
        （`COALESCE(end_time, start_time) >= playtime_reset_at`）
      - 排序：`GameListSortBy` 新增 `play_time`，列表查询在需要时 LEFT JOIN
        同一套会话汇总（1:1，不改变计数）；时长为 0 的游戏恒排末尾
      - 前端：`useGamePlaytime` 从 `bigscreen/` 移到 `hooks/`（库页也要用），
        新增 `primeGamePlaytimes` 用批量结果预填缓存，单查只在未命中时发生；
        大屏信息浮层副行补时长、库页详情面板补「游玩时长」行、
        排序下拉新增「游玩时长」（卡片封面覆盖条不重复显示时长）
      - 测试：`TestQueryGamesPlayTimeSumsAfterReset`（清零前后与边界时刻）、
        `TestQueryGameListWithPlayTime`（本页每条都有条目、未请求时不返回）、
        `TestQueryGameListSortsByPlayTime`（顺序与计数不变）。
        **写测试时抓到一个真实 bug**：`WHERE reset_at IS NULL OR 时间 >= reset_at AND id IN (...)`
        里 AND 优先级高于 OR，id 过滤只作用在清零判定上，导致返回了全部游戏，
        已用括号修正
      - 已知不一致（不本轮改）：`StatsService.GetGameStats` 仍按全量会话求和，
        不含清零过滤。桌面端不写入清零前的会话（导入方向已过滤），
        故实际取值等价；若将来桌面端提供清零入口，需一并收敛
      - 验证：`gofmt` 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        `wails3 generate bindings`（枚举与两个 vo 模型更新）；前端
        `pnpm typecheck` / `build` / `i18n:check` 通过，改动文件 eslint 0 error
- [x] 内置转区/超分工具 + YukiHub 账号系统（登录 / 云同步 / 社交）（2026-10-02 第二轮）
      - 用户诉求：① 转区（Locale Emulator）与超分（Magpie）要**直接内置**、用户也能自行换新版本；
        ② 把 YukiHub 的账号登录系统接上（范围选定「全套」：登录 + 云同步 + 在线状态 + 好友聊天）
      - **内置工具**（`build/compat-tools/`，随包分发）：
        · Locale Emulator 2.5.0.1（151 KB 压缩 / 0.7 MB 解压，LGPL-3.0）
        · Magpie 0.12.1 x64（10.3 MB 压缩 / 28.8 MB 解压，GPL-3.0；**自带 WinUI 3 组件，
          不需要另装 .NET 桌面运行时**，系统要求只有 Win10 1903+ / DX11）
        · 合规：两者均为**聚合分发**（独立程序，未修改），随包附 `SOURCE.txt`
          （版本、作者、仓库、许可证、源码获取方式）与 `gpl-3.0.txt` / `lgpl-3.0.txt` 全文
        · 打包链路：`scripts/build.bat` 的便携版与 installer-payload 两处 xcopy；
          NSIS 安装 `File /r`（卸载随 `$INSTDIR` 一并清除）
        · **用户可自行替换**：`apputils` 的检测新增「扫 compat-tools 下一层子目录」，
          一个工具一个目录；`ApplyDetectedCompatTools` 从「仅在路径为空时写入」
          改为「路径为空**或已失效**（换过安装目录/删了旧工具）时重新检测」
      - **账号系统**（后端 `https://yukihub.zh.kg/api`，接口契约与 Android 版一致）：
        · 新增 `internal/service/yukihubaccount`：完整 API 客户端（登录/注册/发码/重置密码/
          刷新令牌/资料/等级/头像上传 + 云同步 + 在线状态 + 好友/私聊/群聊 40 余个端点）
          - 登录注册走 GET + query（后端既有设计）；认证 `Authorization: Bearer`
          - 必带正常 UA 与 `Referer`（Cloudflare 按 UA 指纹拦）
          - 云同步收发 gzip 原始字节，`/sync/download` 的 404 = 云端无数据（不是错误）
          - 响应字段走「别名列表」解析（后端历史上换过 accessToken/access_token/token 等名字）
        · 新增 `internal/service/account_service.go`：会话与令牌集中在 appconf；
          **401 自动刷新一次**再重试（刷新失败才清会话）；45 秒心跳（与手机版一致），
          「正在玩」取自未结束的游玩记录；退出登录时尽力通知服务端下线
        · **云同步完全复用现成能力**：上传用 `YukiHubExporter.Build()`（产出的就是
          Android 侧 schema 5 快照），下载用 `YukiHubImporter` 导入；
          冲突按手机版策略做「云端优先合并」，同步后有 60 秒冷却
        · 前端：设置 → 账户授权区新增 **YukiHub 账号卡片**（3 张卡片布局），
          未登录是登录/注册/找回密码三种表单（含 60 秒发码倒计时），
          已登录显示头像昵称 UID、云同步开关、分享正在玩开关、立即同步、改昵称、退出
        · i18n 新增 43 键 × 4 语言；`SetRuntime`/`Init` 标 `//wails:ignore` 避免误暴露
      - **尚未完成**：好友/私聊/群聊的**界面**（后端 30 个方法与前端绑定都已就绪，
        只差 UI）；下一步单独做
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        typecheck 通过、i18n:check 干净、改动文件 eslint 0 error

- [x] LunaBox 导入改读 **ZIP 备份**（上一轮读 .db 是错的）+ 换 LunaBox 图标（2026-10-02）
      - 用户指出两处：① 导入菜单的图标不对，要用 LunaBox 的 logo；
        ② 「lunabox 备份的是这种」—— 给出的实际路径是
        `%APPDATA%\LunaBox\backups\database\lunabox_<时间戳>.zip`
      - **上一轮的实现方向错了**：LunaBox 的备份**不是 .db 数据库文件**，而是
        **ZIP**（DuckDB 的 `COPY TO csv` 导出 + 封面图片），结构为：
        `database/games.csv`、`play_sessions.csv`、`game_tags.csv`、`categories.csv` 等，
        以及 `covers/<游戏 ID>.webp`
      - 手机版早就这么做了（`LunaBoxImporter.java`），本仓库照搬其结构并做两处增强：
        · **保留启动路径**：桌面端 path 是有效的 Windows 路径（手机版因路径不可达而丢弃）
        · 主键由本机生成（与其它导入器一致），游玩记录 / 标签 / 封面一律按 LunaBox 的 id 关联
      - 读取改用 `archive/zip` + `encoding/csv`：
        · 按**列名**取值（LunaBox 各版本列数不同，v1.12.1 才加 `is_nsfw` / `aliases` 等）
        · 手工剥掉 BOM（否则第一列名会带 `\ufeff`，取不到值）
        · 引号内的逗号与换行交给标准库（简介就是多行的）
        · 时间戳是 PostgreSQL 风格 `2026-07-16 11:56:43.468941+08`（含时区偏移）
        · `duration` 单位是**秒**，与桌面端库内一致，直接透传（不做毫秒换算）
      - 封面：从 ZIP 的 `covers/` 取字节，经 `imageutils.SaveCoverImageBytes` 落成本地封面
        （走 `ImportItem.CoverLoader`，由后台 worker 异步写回）
      - 选择对话框改 `*.zip`，起始目录默认 `%APPDATA%\LunaBox\backups\database`
      - 图标换成 LunaBox 官方 appicon（从上游仓库提取到
        `frontend/src/assets/importers/lunabox.png`，1024×1024、透明底，与其它导入器图标一致）
      - 测试：8 个单测（含造 ZIP 的端到端解析：引号内逗号与换行、缺列容忍、
        非 ZIP 报错、游玩记录 rekey）+ 1 个真实备份核对（环境变量触发，CI 自动跳过）。
        **实测用户真实备份**：5 个游戏、2 条游玩记录、3 个本地封面全部命中无缺失，
        `on_hold` → `onhold` 映射生效
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        i18n:check 干净（无增删键）、typecheck 通过、改动文件 eslint 0 error

- [x] 删 Hikarinagi 授权卡片 + 限速补足 + 新增「从 LunaBox 导入」（2026-10-01 第七轮）
      - 用户确认四个渠道都能用了，接着提了三件事
      - ① **删掉 Hikarinagi 授权卡片**：元数据已走内置应用级凭据，登录只剩「显示账号名」，
        而它唯一的实质功能（状态回写）需要拿不到的 `status:write`。
        账户授权区 3 张卡片 → 2 张（`accountGridColumns` 三列模板改两列）；
        连带删掉已无使用者的 `utils/hikarinagiAuth.ts`，以及 `GameReviewPanel` 里
        「同步到 Hikarinagi」按钮 —— 那条链路既依赖登录状态又需要 `status:write`，
        留着就是永远禁用的死按钮；i18n 清掉 18 个失效键/语言
      - ② **Hikarinagi 限速**：上游按**应用**限 60 次/分钟，而这个配额是所有装了
        YukiHub 的机器**共用**的。原策略没配 `RateLimitRetryDelay` / `MaxRetryDelay` /
        `MaxRateLimitRetries`，退避落到默认的「等 1 秒重试 1 次」——
        对 1 分钟窗口毫无意义，撞上即失败。补成等 1 分钟、封顶 2 分钟、最多 2 次
      - ③ **新增「从 LunaBox 导入」**（用户选：手动选 .db + 游戏与游玩记录）
        · 表结构与本仓库基本一致（13 张表同名，硬分叉），两边 DuckDB 都是
          `duckdb-go/v2 v2.5.6` → 存储格式兼容，`sql.Open("duckdb", path)` 直接读
        · **两处必须显式换算，否则静默归错**：`games.status`
          （`not_started` / `want_to_play` → `unplayed`，`on_hold` → `onhold`，
          没有「想玩」这一态）；`games` 少 5 列（`legacy_local_id`、`source_device_id`、
          `playtime_reset_at`、`hidden`、`trailer_path`）按零值处理
        · 读取刻意按**列名**取值而非固定列顺序 Scan：既不怕 LunaBox 少那 5 列，
          也能直接读本仓库自己的库文件（互为备份）
        · 复用 ReinaManager 的链路（选文件 → 预览 → 导入），`ImportService` 新增
          5 个方法并重新生成绑定；前端 `GameImportModal` 与游戏库菜单新增入口
        · 回归测试 3 个：status 映射全覆盖、来源透传与兜底、按列名取值容忍缺列
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        typecheck 通过、i18n:check 干净

- [x] Hikarinagi **有两个 OAuth 应用**：元数据用独立的 client_credentials 凭据（2026-10-01 第六轮）
      - 用户提醒「手机上是有两个 hikar 的 id……一个是直接嵌入 app 的硬编码直接用，
        另一个只有 read 的才是登录」→ **核实后用户说得对**，手机版确实是两套：
        · 用户登录：`hkn_qtmXMJfBoxcNLA-a`，授权码 + PKCE（public，无 secret），scope `openid user:read`
          （在 `AuthActivity.java`）
        · 元数据：`hkn_4poXX7v37j_iM2-o`，**Client Credentials**（Basic 认证，有 secret），
          scope `catalog:read`（在 `metadata/HikarinagiClient.java`）
      - **桌面端三处都错**（上一轮只修了「登录」那次 scope）：
        ① 元数据的 client_credentials 用的是**登录 client 的凭据**
           （`version.HikarinagiOAuthClientID` + `_SECRET`，后者从来没人注入过）；
        ② scope 写的是上游的 `catalog:full`，本 client 并未被授权这一项；
        ③ `fetchMetadataByID` / `fetchMetadataCandidatesByName` **优先传用户令牌** ——
           于是「已登录」用户读元数据时带的是 scope 只有 `openid user:read` 的登录令牌，
           反而比未登录时更容易被拒
      - 修：
        · 新增元数据专用凭据常量（与 Android 端同源）→ 元数据**开箱可用，不再要求构建注入**
        · scope `catalog:full` → `catalog:read`
        · `fetchMetadata*` **不再取用户令牌**，直接走应用级凭据
        · 新增 `version.HikarinagiMetadataClientID` / `_Secret` + env 覆盖点（便于轮换密钥）
        · 删掉因此失效的 `IsHikarinagiUnauthorizedError`
      - **本机联网实测**：内置凭据 + `scope=catalog:read` 向 `id.hikarinagi.org/oidc/token`
        换令牌返回 **200**（expires_in 3600）；用该令牌读
        `api.hikarinagi.org/v3/galgames/371` 返回 **200**（CLANNAD 详情）。
        过程中发现 **Cloudflare 会按 User-Agent 拦截**（Error 1010），
        请求必须带正常的应用 UA
      - 测试：`metadata_hikarinagi_test.go` 改为断言「用元数据凭据 + scope=catalog:read」；
        `TestHikarinagiGetterRequiresInjectedCredentials` 改名为
        `TestHikarinagiMetadataCredentialsAreBuiltInAndInjectable`
        （语义从「必须注入」改成「内置可用 + 注入优先」）
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿（31 包）

- [x] Hikarinagi 授权失败 `invalid_scope`：scope 必须与该 client 被授权的一致（2026-10-01 第五轮）
      - 现象：用户点 Hikarinagi「去授权」→ 浏览器一页「授权失败 /
        `invalid_scope: requested scope is not allowed`」。用户已确认回调地址登记过了，
        且 NextMoe 授权成功（说明本轮问题与回调、与 client id 都无关）
      - **根因：scope 不匹配**。上游 LunaBox 写的是
        `openid catalog:full user:read status:write offline_access`，
        那是配**它自己申请的 client id** 的；YukiHub 复用的是 Android 客户端 `hkn_...`，
        只被授权了 `openid user:read`（Android 端 `AuthActivity.HIKARINAGI_OAUTH_SCOPE` 正是这个），
        多要一项服务端就直接拒绝整个授权
      - **关键认知**：OAuth 的 scope 由**服务端按 client 授权**决定，不是"想要就能要"；
        而且被拒时错误只出现在浏览器那一页，**客户端拿不到任何错误信息**
      - 修：默认 scope 改为 `openid user:read`（与 Android 完全一致）。
        `hikarinagiOAuthScopes` → `hikarinagiOAuthDefaultScopes`，服务实例持有 `scopes` 字段，
        `buildHikarinagiAuthURL` 增加 scopes 参数
      - 留口子：新增 `version.HikarinagiOAuthScopes` + 环境变量 `YUKIHUB_HIKARINAGI_SCOPES`，
        注入值优先于默认值 —— 以后在后台给应用加了权限，注入即可拿回完整功能，不用改代码
      - 已知代价（已写进 ADR-0003）：没有 `status:write` → 「同步游戏状态到 Hikarinagi」
        会被服务端拒绝；没有 `offline_access` → 可能拿不到 refresh token，
        access token 过期后需要用户重新授权
      - 回归测试 `TestHikarinagiDefaultScopesStayWithinGrantedSet`：钉住默认值，
        并显式禁止 `catalog:full` / `status:write` / `offline_access` 混进默认值
      - 验证：gofmt 无输出、`go build ./...` 通过、
        `go test ./internal/service/ -run Hikarinagi -count=1` 4 个用例全过

- [x] 授权方式对齐手机版：Bangumi 改个人令牌、Hikarinagi 修「构建时注入」报错（2026-10-01 第四轮）
      - 用户诉求：三个来源的授权怎么做？倾向手机版做法（Bangumi/镜像 = token，Hikarinagi 保留授权，
        NextMoe 同手机版）；并问「PC 与手机要不要去后台各开一个项目 id」
      - 摸清手机版的真实做法（`AuthActivity.java`）：
        ① Bangumi/镜像 = **用户自填 Access Token**（`KEY_BANGUMI_TOKEN`），完全不做 OAuth；
        ② Hikarinagi = OAuth（client id `hkn_qtmXMJfBoxcNLA-a`，redirect `yukihub://hikarinagi/callback`）；
        ③ NextMoe/鲲 = OAuth（client id `16cc006913d6b666c6b1a1a115f644de`，redirect `yukihub://oauth/callback`）；
        ④ 另有自建账号后端 `yukihub.zh.kg`（邮箱/密码 + 云同步），与第三方授权无关，PC 端尚未接入
      - **报错根因**：`version.BangumiOAuthClientID` / `HikarinagiOAuthClientID` 默认空串，
        CI 里 `vars.YUKIHUB_*` 也没配，于是 `StartAuth` 直接返回「请在构建时通过 XXX 注入」
      - **修 1（Bangumi 改 token，后端零改动）**：`getValidAccessToken` 本来就有
        「access_token 有值 + refresh_token 为空 → 直接当 Bearer 用」的 legacy 通路，
        `buildAuthStatusLocked` 也把它判为 authorized（`legacy_token` 标志），
        `Disconnect` 清的正是指标字段，语义天然吻合。
        只改前端：账户卡片「去授权」换成令牌输入框 + 保存（令牌不回显，换令牌走「断开」再填），
        保存后发一次真实 `GetProfile` 确认令牌确实可用。**不再需要任何 OAuth 应用**
      - **修 2（Hikarinagi 开箱可授权）**：新增默认 client id 常量
        `hikarinagiOAuthDefaultClientID = "hkn_qtmXMJfBoxcNLA-a"`（与 Android 同源的公开标识，
        native public client、非机密），构建注入值优先于它；错误文案去掉「构建时注入」措辞
      - **回答「要不要新开项目 id」**：Bangumi **不用开**（改 token 后不需要 OAuth 应用）；
        Hikarinagi / NextMoe **优先复用现有应用 + 后台追加 loopback 回调地址**
        （桌面端端口固定：Hikarinagi 14791 / NextMoe 14792，redirect_uri 确定、注册一次长期有效），
        只有后台限制「一个应用只能一条 redirect_uri」时才新开桌面端应用
      - 新增 `docs/decisions/0003-account-authorization-strategy.md` 记录该决策；
        `docs/fork-setup.md` 的凭据表与新增的「回调地址表」同步更新
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        typecheck 通过、i18n:clean 每语言 -3 个失效键、改动文件 eslint 0 error

- [x] 修账户授权展开布局 + 备份彻底简化（2026-10-01 第三轮）
      - **账户授权卡片展开后布局散架**（用户三张截图）：
        根因是 `BasicSettingsPanel` 的 `accountGridColumns` 把**三张卡片塞进两列网格**
        （展开 Bangumi 用 `7fr_3fr`），第三张只能换行 —— 于是「Bangumi 撑满、
        Hikarinagi 贴右边、未萌掉到下一行」，换一张展开又是另一副样子。
        改成**始终 3 列**、展开的那张占 60%（`3fr_1fr_1fr` / `1fr_3fr_1fr` / `1fr_1fr_3fr`），
        三张卡片排在同一行。动画仍然走 `grid-template-columns` 过渡，不受影响
      - **备份简化**（用户：「云备份删了」「最多搞个自动备份就得了」）：
        - 删掉 `CloudBackupSettingsPanel` / `AutoBackupSettingsPanel` / `DBBackupPanel`
          三个面板（合计约 68KB），新建**一个** `BackupSettingsPanel`
        - 现在「数据与备份 → 备份」里只有四项：**自动备份开关**（一次写
          `auto_backup_db` + `auto_backup_game_save`，不再拆两个开关）、
          **保留份数**（一次写两个 retention）、**立即备份一次**、**本机备份列表（恢复/删除）**
        - **云端备份整块移除**（等账号系统上线后单独做）。注意：云同步的*运行时*
          （侧栏状态、定时任务）保留，只是不再在设置里暴露配置入口；
          已配置过的用户配置仍然生效
        - i18n 清掉 145 个失效键/语言
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        i18n:check 干净、typecheck 通过、改动文件 eslint 0 error

- [x] 设置继续瘦身 + 删除 CLI + 转区/超分工具零配置（2026-10-01 第二轮）
      - 用户诉求：截图指「超分和转区工具还得自己加，不自带的吗」；删掉「键盘快捷键」；
        「同步与备份 / 数据迁移 / 应用数据」合并成一个；CLI 不要了
      - **设置页**（本轮再 11 → 9 个顶层分区）：
        ① 删掉「键盘快捷键」分区与 `ShortcutsSettingsPanel`（速查弹窗仍可用
           Ctrl+Shift+/ 打开，不受影响）；
        ② 把「同步与备份 / 数据迁移 / 应用数据」合并成一个 **「数据与备份」**，
           内部子分组：云服务 / 自动备份 / 数据库备份 / 数据迁移 / 应用数据（含本地集成）
      - **删除 yukihubcli 命令行工具**（整条链路）：
        - 删 `internal/cli/`（16 文件）、`cmd/yukihubcli/`、`utils/apputils/app_cli_path*.go`
        - 仍需要的**协议转发通道**搬到 `internal/ipc/`：`ipc/core`（客户端）+`ipc/server`
          （本地端点）。`StartServer` 从 `*cli.CoreApp` 瘦成 `(ctx, *service.StartService, runtime)`，
          并删掉只服务 CLI 的 `/run` 端点；`yukihub://` 已运行实例转发（/ping /install /launch）保持不变
        - `PortableSetupService` 去掉 CLI 状态与 Register/UnregisterCLIPath；前端「本地集成」同步删掉 CLI 区块
        - 构建链路：`scripts/build.bat`（删 :build_cli、portable 与 installer-payload 重新编号）、
          NSIS（移除 CLI 选项页，标志位固定 0，保留旧版本 PATH 清理的兼容代码）、
          三个 workflow（payload 校验与签名清单去掉 cli）、文档 4 篇
        - **注意**：`yukihub://` 协议注册本来就走主 GUI exe（`RegisterPortableURLScheme` 用
          `os.Executable()`），与 CLI 无关，所以删 CLI 不影响深链
      - **转区 / 超分工具零配置**（不随包分发，改为自动识别）：
        - 为什么不自带：Locale Emulator 与 Magpie 都是第三方程序（Magpie 是 GPL-3.0
          且依赖 .NET 桌面运行时，整包上百 MB），塞进 AGPL 安装包不合适
        - 新增 `apputils.DetectLocaleEmulator/DetectMagpie`：扫程序目录（含约定的
          `compat-tools\`、`tools\`，以及 `Magpie-0.11.0\` 这类带版本号的解压目录）、
          `%LOCALAPPDATA%\Programs`、`%PROGRAMFILES%` 等常见位置
        - 启动时 `service.ApplyDetectedCompatTools(config)` 自动补齐空路径并落盘
          （只在原本为空时写，不覆盖用户手选）；设置页新增「自动检测」按钮，
          找不到时提示把工具放到 `compat-tools\`
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        i18n:check 干净（4 语言各 -13 键）、typecheck 通过、改动文件 eslint 0 error

- [x] 设置面瘦身 + 「当前资料源」对齐手机版（2026-10-01）
      - 用户诉求：删掉 LunaBox 带来的「奇奇怪怪」的设置（备份有好多个、有歧义；代理没用），
        对齐 YukiHub 手机版；游戏库右侧详情要显示当前用的是哪个源；设置里要有「当前游戏源」
      - **先摸清手机版的模型**（`MainActivity.showSettingsDialog`）：
        设置里只有一个**「右侧资料源」单选**（VNDB/Bangumi/镜像/月幕/Hikarinagi/NextMoe）
        + Bangumi Token + 一键重扫 + 重扫间隔；详情面板顶部有**来源徽标**，
        对不上可点「重新匹配 <源>」；手机版**没有代理设置**；
        备份只有「同步中心」与「数据迁移」两组
      - 设置页原本 **16 个顶层分区**、其中**四个**是备份（云配置 / 同步与备份 /
        数据库备份 / 全量数据备份）→ 合并成两个，对齐手机版：
        **「同步与备份」**（子分组：云服务 / 自动备份 / 数据库备份）+
        **「数据迁移」**（原全量数据备份面板）
        另：「应用更新」并入「关于与更新」，「本地集成设置」并入「应用数据」
      - **删掉「代理配置」分区**并删除 `ProxySettingsPanel.tsx`。
        配置字段与后端行为保留（默认跟随系统代理），只是不再暴露给用户
      - 新增 `frontend/src/components/ui/SettingsSubSection.tsx`：分区内部的子分组标题，
        被合并的面板靠它分层，避免又变成一坨
      - **「当前资料源」**（`appconf.CurrentMetadataSource`，默认 vndb）：
        设置 → 元数据里的单选下拉，对齐手机版「右侧资料源」。
        行为：`gamehelper.ConfiguredMetadataSources` 把它提到最前（影响手动搜索顺序、
        以及来源被删时的替补挑选）；`NormalizeCurrentMetadataSource` 校验非法取值
      - **游戏库右侧详情**（`LibraryDetailPanel`）：标题下新增「资料源」一行，
        显示该游戏当前使用的源（`games.source_type`）并带来源图标；
        缓存过多个来源时点开即可切换（`SetDefaultMetadataSource` + 失效列表缓存），
        即手机版的「重新匹配」。注意**列表行不带 `metadata_sources`**（那是单条查询才补的），
        所以面板在选中游戏后单独 `GetGameMetadataSources` 取一次
      - i18n：4 个语言各 +22 个新键（`settings.subSections.*`、`settings.metadata.currentSource*`、
        `library.detailSource*`），`pnpm run i18n:clean` 清掉 17 个失效键
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        i18n:check 干净、typecheck 通过、改动文件 eslint 0 error

- [x] 游戏详情页选中状态胶囊「和背景融合看不见」（2026-09-30，用户截图）
      - **根因不在组件，在构建配置**：UnoCSS 默认的 `content.pipeline.include` 是
        `/\.(vue|svelte|[jt]sx|vine.ts|mdx?|astro|elm|php|phtml|marko|html)($|\?)/`
        —— 只有 jsx/tsx，**不含纯 .ts**。`src/consts/gameStatusBadge.ts` 里的状态徽标
        配色（`bg-brand-900/55`、`bg-success-600/70`、`bg-warning-600/80`、
        `bg-orange-600/70`、`bg-error-600/70`）**一个都没进产物**，而
        `activeChipClass` 里写死了 `text-white` —— 于是选中的状态胶囊在浅色玻璃
        背景上只剩白色图标和白色文字，整颗看不见（用户圈出来的那颗）
      - 静默：构建成功、无告警，运行时只是「样式没生效」，纯靠产物比对才能发现
      - 修：`uno.config.ts` 的 `content.pipeline.include` 在 UnoCSS 默认值基础上加 `ts`。
        注意 **`content.filesystem` 对 vite 插件是空操作**（`@unocss/vite` 只读
        `pipeline`），先加 filesystem 试过没生效，产物体积纹丝不动，才定位到这一点
      - 顺带发现并一起修好：`src/utils/cloudSync.ts` 的云同步状态配色（`ring-*`、
        `dark:bg-*-900/40` 等）同样缺失 —— 同一个根因
      - 验证：把 `src/**/*.ts` 里所有形如工具类的 token（43 个）逐个与产物比对，
        修复前缺 5+7 个、修复后 0 缺失；CSS 体积 271.2 → 272.6 KB

- [x] 全量审计「按来源名映射 / 来源名单」的所有位置（2026-09-30，用户要求「别只看 nextmoe」）
      - 手机版支持的来源全集（`MetadataController` / `SyncManager`）：**vndb / bangumi /
        bangumi_mirror / ymgal / hikarinagi / nextmoe**。逐个位置比对，又查出 3 处同类漏项
      - **修 1（Playnite）**：`stringToSourceType` 只认 bangumi/vndb/ymgal/steam，
        其余静默落 local。Playnite 的来源是**用户手填的自由字符串**，桌面端自己支持的
        hikarinagi / nextmoe / bangumi_mirror 在那边全被判成 local。改为与 YukiHub 备份
        共用同一份实现 `mapExternalSourceName`（新文件 `importer/source_mapping.go`），
        删掉两处各自维护的 switch
      - **修 2（合并导入的 NSFW）**：`updateImportedItemMetadata` 里
        `is_nsfw = CASE WHEN source_type IN ('bangumi','vndb') ...` 是上游基线带来的硬编码
        名单。实际 bangumi/vndb/**hikarinagi/nextmoe** 都会把来源侧 NSFW 写进
        `models.Game.IsNSFW`（ymgal/steam 不会，照抄反而会清掉本地标记）。结果是
        hikarinagi / nextmoe 的游戏合并导入时 NSFW 永远不更新。名单提成
        `gamehelper.NSFWAuthoritativeSources()`，SQL 占位符由它生成，
        Go 侧与 SQL 侧共用同一个集合
      - **修 3（来源挑选不确定序）**：`game_metadata_source.go` 兜底挑「当前元数据来源」时
        `for source, sourceID := range available` 直接遍历 map —— Go 的 map 顺序随机，
        同一个游戏的来源标签会在不同次调用之间跳变。改为排序后取第一个
      - **核实不是问题的（附证据）**：
        ① `bangumi_mirror` 折叠成 `bangumi` 是**必须的**：手机版从不把它写成
           `metadata_cache.source`（缓存里恒为 `bangumi`，只有设置项 `metadata_source`
           会出现 `bangumi_mirror`），不折叠反而匹配不上偏好来源；
        ② ID 补全的 4 源清单（`game_id_enrichment.go` 3 处）：内嵌
           `game_id_mapper.db` 的 `id_map` 表只有 vndb_id / bangumi_id / steam_id /
           hikarinagiid 四列，名单是数据驱动的，不是漏项；
        ③ PotatoVN（`RssType` 枚举一一对应）、Vnite（字段式，与其模型字段一致）、
           Steam（固定）映射完整；
        ④ 前端 `ALL_METADATA_SOURCES` / 图标表 / `getMetadataSourceURL` 与手机版 6 源一致，
           且都有安全兜底；
        ⑤ 云同步 snapshot / mapper 直接存枚举，没有名称映射；
        ⑥ `gamehelper.IsSupportedMetadataSource`（10 个）/ `ConfiguredMetadataSources` /
           手动搜索的 getter switch 覆盖完整
      - **留给用户拍板的两点（未改，避免猜错引入新错映射）**：
        ① ReinaManager 的 `kun` 源在 6 个字段优先级里被使用，但 `mapReinaManagerSource`
           没有对应分支 → 不会被记成元数据来源（它不在 `reinaIdentityPriority` 里，
           所以不会产生「Local + 有 id」的坏身份）。`kun` 是否就是 NextMoe（未萌）待确认：
           NextMoe 的图片域名是 `image.kungal.iloveren.link`，看着同源但不猜；
        ② 两套「支持集合」不一致：配置/UI 是 6 个（`allowedMetadataSourceSet`、
           `ALL_METADATA_SOURCES`），记录级是 10 个
           （`gamehelper.IsSupportedMetadataSource`）。Steam / DLsite / TouchGal /
           ErogameScape 有 getter 但**开不出**（`normalizeMetadataSources` 会静默丢弃）。
           属能力未开放，不是数据损坏
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        真实备份探针复跑：nextmoe 身份 4 个、「本该 nextmoe 却错落」为空

- [x] 导入丢失 nextmoe 来源 + 首页轮播手动滑动（2026-09-30，第二次复测）
      - 现象①：用户报「好几个 nextmoe 源的游戏，导进来变成 vndb」
      - 根因（两处漏项叠加）：备份 `settings.metadata_source = "nextmoe"`，但
        ① `mapYukiHubSourceType` 的 switch 没有 nextmoe → 映射成 `local`，
        使 `pickYukiHubMetadata` 里 `preferred != Local` 的守卫整体失效；
        ② 兜底优先级名单硬编码为 `[VNDB, Bangumi, Ymgal, Hikarinagi]`，同样漏了 nextmoe。
        结果有 nextmoe 条目的游戏被归到名单里靠前的 vndb
      - 修：补 `NextMoe` 映射（并把 steam / dlsite / touchgal / erogamescape 一并补上，
        免得对端新增来源时又静默退化成 local）；兜底名单提成具名变量
        `yukiHubFallbackSourceOrder` 并补全
      - 回归测试：`yukihub_source_test.go`（映射覆盖全部手机端来源、兜底名单完整性、
        nextmoe 偏好与唯一来源两种情形）
      - 实测（真实备份探针新增两个查询）：4 个游戏（local_id 1/10/74/98）落成 nextmoe，
        「本该 nextmoe 却错落」集合为空
      - 现象②：用户澄清「轮播图不能**我手动**滑动」（不是自动播放）
      - 修：`HomeHeroCard` 接 pointer 事件做拖拽切换
        （`SWIPE_THRESHOLD_PX = 48`，越过阈值立刻切、一次手势只切一次，
        避免把卡片内按钮的点击吃掉），`index.tsx` 加 `handleCarouselSwipe`
        （首尾循环，顺带 `pauseCarouselBriefly`）
      - **备注**：用户本次附的是手机版截图（侧栏为「首页/游戏库/大屏/好友聊天/翻译」，
        PC 端 SideBar 是「首页/游戏库/统计/分类」）。这条按 PC 首页 hero 实现，
        已在回复中向用户确认口径
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        前端 typecheck / build / i18n:check 通过

- [x] 封面源优先度 + 首页轮播（2026-09-30，用户上机复测后报的两个问题）
      - 现象①：导完 32 个游戏后多张封面空白，切页就重新加载；用户判断「缓存的源和
        app 里实际用的源不一样」，并建议做封面源优先度
      - 根因：`vndb_cover_source` / `bangumi_cover_source`（默认/用户都设为
        hikarinagi）这个设置**本来就存在**，但只作用在**刮削**路径
        （`metadata.resolveMetadataCoverURL`）。从手机版备份导进来的封面是原始
        `t.vndb.org` / `lain.bgm.tv` 地址，绕过了改写；实测 `lain.bgm.tv` 本机
        IPv4/IPv6 都连不通，`t.vndb.org` 也出现过 IPv6 拨号超时（日志里每次
        28–38 秒），所以「空白 + 每次切页重新超时一遍」
      - 修：新建 `internal/utils/coverutils`（`Preference` / `ResolveURL` /
        `ResolveURLByHost` / `DetectSource` / `IsProxiedURL`），镜像前缀常量集中于此；
        改写接到**两个出口**——封面下载
        （`DownloadAndSaveCoverImageWithProxyConfigContext`）与图片代理
        （`RemoteImageProxyHandler`），后者才能覆盖库里已有的旧数据。注入用接口
        `imageutils.CoverSourcePreferenceProvider`（由 `appconf.AppConfig` 实现），
        imageutils 不反向依赖 appconf。刮削路径行为不变
      - 同时修拨号：`downloadutils.resolveAllowedAddress` 只取解析结果第一条，
        系统解析器在双栈主机上常把 IPv6 排前；改为 `resolveAllowedAddresses`
        （IPv4 优先）+ `dialWithFallback`（250ms 提前回退，RFC 8305 简化版）
      - 现象②：首页轮播不会自动滑动
      - 根因：`routes/index.tsx` 里 `setIsCarouselPaused(true)` **没有任何地方置回
        false**，点过一次轮播点或快速启动栏就永久停住
      - 修：悬停暂停（离开恢复）+ 手动点选后 15 秒恢复；`HomeHeroCard` 加 `onHoverChange`
      - 前端另加「封面失败记忆」（`utils/imageProxy.ts`，5 分钟 TTL / 512 上限）：
        `ProxyImage` 过滤已知失败的候选、全失败则不再发请求，
        `GameCoverImage` 立即出占位；首页「刷新」按钮清空记忆
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿
        （含新增的 `coverutils` 单测）；前端 typecheck / build / i18n:check 通过

- [x] 用真实手机版备份核对导入导出（2026-09-30）
      - 输入：用户手机版本地备份 `yukihub_backup_1790751445692.ykbak`
        （gzip + schema 5，32 游戏 / 30 会话 / 42 条元数据缓存）
      - 结论：**导入方向此前有 3 个「只映射、没落库」的缺陷**，导出方向**根本没有入口**
      - 修 1：落库的 `INSERT INTO games` 列清单缺 `status`，32 条状态全被写成列默认值
        「未玩」（备份里是 completed 21 / unplayed 9 / playing 1 / dropped 1）
      - 修 2：同一条路径也缺 `aliases`，原文名/罗马字标题全丢（32 条里 30 条本该有）
      - 修 3：合并路径（`updateImportedItemMetadata`）同样缺这两列，按手机版
        `importGamesJson` 的规则补上——只有对端 `updated_at` 不早于本地时才覆盖
      - 新增回归测试 `TestYukiHubImportPersistsStatusAndAliases`；
        另加按环境变量触发的真实备份探针 `TestYukiHubImportRealBackup`
      - 导出接线：`exporter` 此前只被测试引用，界面无法导出 `.ykbak`。新增
        `ImportService.SelectYukiHubExportPath` / `ExportToYukiHub`
        （`ExportWithSummary` 只 Build 一次），入口放在「设置 → 全量数据备份 →
        YukiHub 手机版迁移」，4 语言文案同步
      - 顺手修掉一个会**抹掉手机端数据**的隐患：手机版用
        `optString(key, 本地值)`，字段存在但为空串等于「清空」。桌面端没有对应概念的
        字段（engine / emulator_package / launch_target / winlator_launch_mode /
        gamehub_local_game_id / gamehub_launch_mode / cover_persist_uri /
        cover_source_type）改为 `omitempty` 省略；`cover_uri` 改为优先取
        `cover_source_url`（本地缓存文件对端拿不到）
      - 核对通过：32 游戏 0 失败、收藏、NSFW、隐藏、清零、元数据来源（vndb 30 /
        bangumi 3 / ymgal 3 / hikarinagi 2）、标签 199、总时长换算（26347745 ms →
        26347 秒）、会话 60 行 = 30 真实 + 30 聚合补偿
      - 有意保留的差异（已写进 `docs/mobile-yukihub-migration.md`）：
        ① 会话上限桌面端是「每游戏 30 条」，Android 是「合计 30 条」；
        ② 不写 `profile` / `lightweight` / `note` / `backup_type` 头字段
        （Android 导入只校验 `app`，不影响可用性）；
        ③ `settings` 只写 `metadata_source`，其余是设备本地偏好
      - 验证：gofmt 无输出、`go vet ./...` 干净、`go test ./... -count=1` 全绿；
        前端 typecheck / i18n:check 通过

- [x] 核对验收项 5（WebDAV 自持同步）现状（2026-09-29）：**无需新实现**
      - 结论：上游已内建 WebDAV 云备份后端，且**已是默认 provider**
        （`appconf` 默认 `cloud_backup_provider = "webdav"`），全链路已接线：
        `appconf`（URL/用户名/密码）→ `cloudprovider/factory.go`
        （`NewCloudProvider` / `TestConnection` / `IsConfigured` 三处均已登记
        `ProviderWebDAV`）→ `backup_service.go`（`TestWebDAVConnection`）→
        前端 `CloudBackupSettingsPanel`（配置表单 + 连接测试）与
        `cloudSync.ts` / `useCloudSync.ts`，四语言文案齐备
      - 实现质量：`webdav/webdav_provider.go` 有地址合法性校验、代理支持、
        429/Retry-After 重试、BasicAuth、PROPFIND 区分文件与集合、
        父目录缺失时补建后重试；`webdav_provider_test.go` 覆盖了
        重试 + 认证 + 代理 + User-Agent
      - 因此验收项 5 的缺口不在实现，而在**真机端到端验证**
        （对真实 WebDAV 服务端跑通 上传 → 列表 → 下载 → 恢复）
      - 验证：`go test ./internal/service/cloudprovider/... -count=1` 通过

手机版参考源码的提取方式（归档在 `参考文件/YukiHub手机版030p...7z`，
用 7z 按需抽取，不要整包解压）：

```powershell
$7z = "$env:APPDATA\TRAE SOLO CN\ModularData\ai-agent\vm\tools\bin\7z.exe"
$arc = "参考文件\YukiHub手机版030p离线展厅成型，进度条跳转.7z"
& $7z x $arc -o".tmp_ref" -y -r "*res/values/colors.xml" "*res/layout/activity_main.xml"
```

关键结论：手机版主界面（`activity_main.xml`）本身就是**三栏**结构
——左侧 76dp 竖栏（头像/导航/底部统计胶囊）+ 中间网格 + 右侧 152dp 详情面板。
也就是说它的信息架构已经接近桌面形态，桌面版要做的是**放大并适配鼠标键盘**，
而不是重新设计一套信息架构。

## 阶段 4：Android 版差异化能力迁移

目标：把桌面版做成"手机版的桌面延伸"，而不是一个通用管理器。

按优先级：

1. **游玩记录与数据同步协议**（已在阶段 2 完成）
2. **多源元数据与本地缓存离线可用**
3. **AI 游玩报告**（复用上游已有的 AI 服务与防剧透配置）→ **已移至阶段 6**（依赖服务端）
4. ~~**OCR 与多引擎翻译工作流**~~ → **已移除（2026-09-29）**
   - 理由：截图 OCR + 悬浮翻译层是**手机独有**的能力形态（Android 无障碍 /
     MediaProjection 取词）；Windows 侧可用的翻译工具（Textractor、Translator++、
     各类屏幕翻译）已足够成熟，桌面端再内建一套 OCR 只是重复造轮子
   - 仓库现状：本项目从未移植过该能力（无 OCR 代码、无 `assets/ppocrv6/` 素材），
     此前仅作为计划项列出，现已删除，不再是待办
5. **社区与好友功能**（REST 契约可复用，界面重做）→ **已移至阶段 6**（依赖服务端）
6. **大屏模式**（对齐手机端的大屏形态；桌面端 UI 应做得更好）
   → **要做，2026-09-29 立项并出方案**，首期做 M0+M1（见下方"大屏模式设计"）
   - 注：**音乐厅 = 离线 3D 展厅**（手机端 `assets/exhibition/`）是**同一项**，
     且 **2026-09-29 决定不做**，详见下方"不做：离线 3D 展厅（音乐厅）"。
     此前条目里"音乐厅 / 大屏模式"并列写法有误导，已拆分。

进展：

- [x] 多源元数据与本地缓存离线可用（2026-09-29）：验收项 2 的缓存 / 离线部分
      - 问题：桌面端把刮削结果散落在 `games` / `game_tags`，没有保留来源侧的原始负载；
        Android 的 `metadata_cache`（`VnMetadata` JSON）两个方向都未被搬运——导入只解析
        身份与少量字段后丢弃 blob，导出完全不产出 `metadata_cache`。结果是
        「Android → 桌面端 → Android」静默丢失桌面端没有对应列的字段（截图、罗马音标题、
        封面分级），且离线时没有可复用的本地副本
      - 修法：`game_metadata_sources` 新增 `cache_json TEXT`（`migration178`，历史行为空串），
        负载沿用 Android 的 `VnMetadata` 结构，两端原样往返
        （`models/yukihub` 的 `MetadataCache.JSON` ↔ `cache_json`）
      - 写入点：导入时原样写入 `entry.json`；刮削时在 `applyRemoteMetadataResult` 统一写入
        （该函数同时服务单条更新与批量刷新两条路径）；导入 upsert 采用"空负载不覆盖已有缓存"，
        避免快照缺少 `metadata_cache` 元素时把对端已有缓存抹成空
      - 导出：`loadMetadataCache` 只导出带 `legacy_local_id`（可被 Android 关联）且标题非空的
        条目，桌面端自建条目跳过，避免产出孤儿缓存
      - 测试：`TestYukiHubImporterPreviewAndImport`（导入原样保留）、
        `TestApplyRemoteMetadataCachesSourcePayload`（刮削写入的负载字段）、
        `TestYukiHubMetadataCacheRoundTrip`（导出 → 导入 → 再导出逐字节一致 +
        无 `legacy_local_id` 条目被跳过）
      - 验证：`gofmt` 无输出；`go vet ./...` 退出码 0；`go test ./... -count=1` 全绿
      - 契约文档已同步：`docs/mobile-yukihub-migration.md`（`metadata_cache` 由"暂不导出"
        改为已导出，待决策问题 4 标记为已决策）

明确排除（**手机独有形态，桌面端不做**）：

- Android 内置游戏引擎（Kirikiri、ONS、Tyrano、Artemis、PSP）
- 模拟器启动适配（Winlator、GameHub、盖世）
- 截图 OCR 与悬浮翻译层（理由见上）
- Shizuku、SAF 镜像存档、触控光标注入、`.nomedia` 管理

明确保留（**Windows 独有能力，属于"功能以电脑侧为准"，不得随平台裁剪误删**）：

- **Magpie 超分**：`config.magpie_path` / `default_use_magpie`、
  `game.use_magpie`；启动时以托盘模式拉起（`start_service.go`），
  策略在 `launcher/strategy_windows.go`
- **Locale Emulator 转区**：`config.locale_emulator_path` /
  `default_use_locale_emulator`、`game.use_locale_emulator`；
  未配置路径时回落到原生启动
- **以管理员身份运行**：`game.launch_mode = admin`
- **Steam 直启**：按注册表定位安装目录，`steam://rungameid/<id>`
- **进程树/窗口焦点活跃时长统计**：`utils/timerutils/active_time_tracker.go`

## 大屏模式设计（阶段 4 第 6 项）

2026-09-29 立项。对齐手机端 `com.yuki.yukihub.bigscreen`（20 个 Java 文件），
但**桌面端重做视觉与输入层**：手机端那套是为遥控器 + 横屏小屏设计的，
桌面端有更大的画布、鼠标键盘手柄三种输入与成熟的 Web 动效能力。

### 定位与形态

横屏沉浸式「浏览 + 原地启动」界面，不是启动器。手机端是独立 Activity
（`sensorLandscape`、隐藏状态栏/导航栏、常亮），三个入口：游戏库底部导航、
首页按钮、开机直达。

### 已决策

| 决策 | 结论 | 理由 |
| --- | --- | --- |
| 承载形式 | **同窗口无外壳全屏路由** `/bigscreen` | 复用现有 store、`game-runtime:changed` 事件与单例后端，改动最小；`RootLayout` 按 pathname 跳过 TopBar+SideBar。独立全屏窗口（双屏场景）留作后续，`StartupWindow` 已有先例 |
| 首期范围 | **M0 + M1** | 先做到"能在大屏里真的浏览和启动游戏"，看效果再定后续 |
| 输入 | 键盘 + 鼠标优先，手柄（Gamepad API）后补 | 桌面端全仓目前无任何手柄代码 |

### 手机端事实基线（迁移参考）

结构（`activity_bigscreen.xml`）：双背景 `bsBgA/B`（当前焦点封面，交叉淡入 600ms
+ KenBurns）→ 左右/底部渐变遮罩 → 氛围层 `bsSnow` → PV 层 `bsBgVideo` →
顶栏（时钟/手柄状态）→ 侧栏 `bsRail`（6 分类，72dp ⇄ 展开）→ 单排卡片货架
（上限 500）→ 底栏按键提示（4s 后淡到 28%）→ 信息浮层 `bsInfoBar`（LOGO/标题/
标签/元数据/操作按钮排）→ 浮层容器（详情层 → 设置 → 菜单，后者盖前者）→
提示条 `bsBanner` → 入场层。

交互：`InputRouter` 把按键翻译成意图（A 确认 / B 返回 / X 收藏 / Y 详情 /
LB·RB 切分类 / START 菜单），长按 400ms 后每 80ms 连发；`FocusEngine` 是
**不持有视图的纯逻辑二维焦点引擎**（支持网格与"每行不等长"两种模型、
跨行夹紧列、边界回调、按分类记忆焦点），滚动/动效/音效全在上层——这部分可近乎
零改动复刻到 TS。焦点区三个且互斥：内容区 / 侧栏 / 按钮排。

数据：`GameRepository.getAll()` 全量进内存；分类 ALL/FAV/RECENT/PLAYING/DONE/TODO
（排除 hidden）；排序 recent（默认）/newest/name；无分页。

详情层 `BigScreenDetailsLayer`：大标题 / 副行（原文名·开发商·发行日期）/
标签 chips（≤3 + R18）/ 统计块（时长、上次游玩、状态、评分）/ 简介 /
截图画带（≤8，整块失败即隐藏）/ 封面；操作仅三个：**游玩 / 观看 PV / 详细**。
大屏化取舍：←→ 只移按钮不切游戏，评分先瘦身，chips 只放标签。

### 视觉参考（`BigScreenSizes` / `colors_bigscreen.xml`）

手机端全部尺寸按横屏短边 `hDp` 等比缩放（夹取区间用于防极端屏幕）：

| 项 | 公式 | 夹取 |
| --- | --- | --- |
| 顶栏高 / 底栏高 | h×0.075 / h×0.042 | 36–54 / 20–26 |
| 行总高 rowTotal | 内容区 / 1.35 | ≥96 |
| 行标题高 headerH | rowTotal×0.20 | 20–34 |
| 卡片高 / 宽 | rowTotal−headerH−gap×2 / 高×0.75 | 88–200 / ≤w×0.17 |
| 行间距 gap | h×0.018 | 4–10 |
| 侧栏条目 / 图标 | (内容区−12)/6 / item×0.92 | 28–52 / 24–46 |
| 侧栏收起 / 展开宽 | item+14 / w×0.24 | 50–78 / 168–260 |
| 大标题 / 标签 / 副行字号 | h×0.052 / 0.027 / 0.030 | 16.5–26 / 9–11 / 9.5–12 |

配色：`bs_bg #0B1020`、`bs_bg2 #111936`、`bs_card #171E33`、`bs_card_focus #222B49`、
`bs_primary #8AB4FF`、**焦点洋红 `bs_focus #FF8AB3`**、`bs_line #2D3658`、
`bs_text #F5F7FF`、`bs_text_muted #9AA4BF`。

动效：卡片焦点 140ms（缩放 1.045 + 描边）、侧栏展开 220ms/收起 200ms、
背景交叉淡入 600ms、提示条进 200ms/出 180ms、入场错峰 42ms×idx。

### 桌面端架构落法

- 路由：新建 `frontend/src/routes/bigscreen.tsx` 导出 `Route`，
  在 `frontend/src/App.tsx` 的 `rootRoute.addChildren([...])` 注册；
  `frontend/src/routes/__root.tsx` 用已有的 `useLocation` 按 pathname 跳过外壳。
- **先验风险（M0 已解除）**：`wailsruntime.Runtime`（`internal/.../runtime.go`）
  只有 Show/Restore/对话框，**没有全屏 API**；结论是**不扩展 Go 侧接口**，
  直接用 `@wailsio/runtime` 的 `Window` 全屏 API（详见下方「M0 进展」）。
- 输入：键盘方向键/Enter/Esc；鼠标 hover 预览、滚轮横滑、点击中转（桌面端增强）。
- 焦点：TS 侧复刻 `FocusEngine` 逻辑模型，视觉动效交给 CSS。
- 数据：复用 `GameService.GetGames`（`internal/service/game_service.go`）已有的
  筛选/排序/分页，比手机端更强。~~缺口：`vo.GameListResponse` 不含游玩时长~~
  **已补齐（2026-09-29）**：请求带 `with_play_time` 即随列表返回 `play_times`
  （见阶段 3 进展「批量游玩时长接口」），信息浮层与库页详情面板都能显示时长，
  详情层（M2）的单查退化为缓存未命中时的兜底。
- 设置：`internal/appconf/config.go` 的 `AppConfig` 加 `bigscreen_*` 字段
  （snake_case，默认值写在 `LoadConfig`），设置页新增分区，4 个语言文件同步。
- i18n：i18next，新增顶层键组 `bigScreen.*`，4 文件同层补齐。

### 分期

| 期 | 内容 | 风险 |
| --- | --- | --- |
| **M0** | 技术验证：窗口全屏 API、横向虚拟货架一屏、键盘焦点环 | 全屏 API 缺口 |
| **M1** | 可用骨架（2026-09-29 完成）：路由+绕外壳、双背景+遮罩、侧栏 6 分类、单排虚拟货架、信息浮层、启动/收藏/详情按钮、键盘操作、焦点记忆、i18n、设置分区 | 低（基本全靠复用） |
| **M2** | 详情层（2026-09-29 完成）：截图画带因桌面端无截图能力降级为封面大图；操作收敛为「游玩 / 详细」（PV 属 M3） | 中 |
| **M3** | PV/预告片（2026-09-29 完成）：`trailer_path` 本地视频选择（受管复制）+ 详情层全屏播放器 + 悬停 1.2s 背景起播；不入手机版同步快照 | 高（全链路从零） |
| **M4** | 氛围打磨（2026-09-29 完成）：特效档位、入场错峰动画、界面音效、手柄与图标、提示条 | 低 |

### M0 进展（2026-09-29 完成）

技术验证全部通过，未改动任何 Go 代码。

- **M0.1 全屏方案**：不扩展 `wailsruntime.Runtime`。`@wailsio/runtime` 的
  `Window` 已提供 `Fullscreen()` / `UnFullscreen()` / `IsFullscreen()` /
  `ToggleFullscreen()`，且前端全仓已有直连先例（`useAppRuntimeEffects.ts`、
  `StartupWindow.tsx`），因此**无需重新生成 Wails 绑定**。
  落地文件：`frontend/src/bigscreen/useBigScreenFullscreen.ts`
  （进入时先 `IsFullscreen()` 探测，本来不是全屏才自己开，卸载时只还原自己改过的那次）。
- **M0.2 横向虚拟货架**：`frontend/src/bigscreen/VirtualGameShelf.tsx`。
  用 `@tanstack/react-virtual` 的 `horizontal` 模式 + `overscan: 4`，卡片复用
  `GameCard`；卡片宽度由货架可视高度反推（对齐手机端 `BigScreenSizes` 的等比思路），
  焦点卡片 `scale(1.045)` + `ring-secondary-500`（= `bs_focus #FF8AB3`），
  140ms 过渡与手机端一致。
- **M0.3 二维焦点引擎**：`frontend/src/bigscreen/focusEngine.ts`（纯逻辑类，
  多区 / 网格与每行不等长 / 跨行夹紧列 / 边界回调 / 按分类记忆）+
  `frontend/src/bigscreen/useFocusEngine.ts`（React 接线，引擎实例只建一次）。
- **承载与入口**：`frontend/src/routes/bigscreen.tsx` 导出 `Route`，在 `App.tsx`
  注册；`__root.tsx` 按 pathname 跳过顶栏/侧栏/背景层。入口先收敛为 TopBar 上的
  一个「大屏模式」按钮（手机端的三个入口在桌面端不需要）。
- **i18n**：4 个语言文件新增顶层 `bigScreen.*`：`enter` / `empty` /
  `hintMove` / `hintConfirm` / `hintExit`。

**M0 未覆盖、留给 M1 的部分**：`GetGames` 目前没有「排除隐藏」过滤，M0 先在
前端剔除 `game.hidden`（服务端过滤记入 M1）；双背景+遮罩、侧栏 6 分类、
信息浮层、收藏按钮、焦点记忆接线（引擎已支持，UI 未用）、设置分区
`AppConfig.bigscreen_*` 全部属 M1。

**验证**：`pnpm build`（`build:desktop` + `typecheck` + `vite build`）通过；
eslint `--max-warnings 0` 干净；`pnpm i18n:check` 通过。大屏本身的观感与输入
手感仍需实机确认，归入阶段 3 延后的实机测试范围。

### M1 进展（2026-09-29 完成）

M1 全部落地，视觉与输入层按桌面端重做（鼠标可点、键盘可走、动效走 CSS）。

- **M1.1 数据层**：`frontend/src/bigscreen/categories.ts`。6 分类 → 后端查询的映射：
  「全部」按名称正序、「最近」按最近游玩时间倒序（未玩过的排末尾），其余按状态筛选；
  **「收藏」不是游戏字段而是系统分类**，走 `GetCategoryGames`，分类 id 由
  `GetCategories()` 的 `is_system` 标记解析（前端不硬编码 id）。
  后端单次查询上限 `MaxGameListLimit = 240`，前端按 `BIG_SCREEN_SHELF_LIMIT = 500` 逐页补齐。
- **M1.2 双背景**：`frontend/src/bigscreen/BigScreenBackground.tsx`。双 slot + `opacity`
  600ms 交叉淡入，KenBurns 放在两层共同的父容器上（避免换图重挂载导致缩放跳变），
  叠左/下双向渐变遮罩；复用 `ProxyImage`（NSFW 模糊沿用全局配置）。
- **M1.3 侧栏**：`frontend/src/bigscreen/BigScreenRail.tsx`，收起 76 / 展开 216，
  `transition-[width] 220ms`；展开由「焦点进入侧栏 或 鼠标悬停」驱动，不加额外按键。
  `BIG_SCREEN_FOCUS_ORDER` **刻意不含侧栏**：否则从货架向上会被引擎跨区夹紧丢到
  分类栏最后一项；侧栏只由货架的「左 / 上」边界显式进入，返回用「下 / 右 / Enter」。
- **M1.4 信息浮层**：`frontend/src/bigscreen/BigScreenInfoBar.tsx`，标题 / 副行
  （开发商 + 状态）/ 标签（≤3，复用 `getTagDisplayName`）/ 按钮排；标签按 gameId
  缓存（`useGameTags.ts`），左右切游戏不重复请求。
- **M1.5 操作按钮**：启动 / 收藏 / 详情。收藏走 `AddGameToCategory` /
  `RemoveGameFromCategory`（`useGameFavorite.ts` 按 gameId 缓存并在本地覆盖状态）；
  在「收藏」分类里取消收藏会触发货架重载。
- **M1.6 焦点记忆**：切分类前 `saveMemory(activeCategory)`，切回时 `restoreMemory`，
  无记忆则回落第一张（对齐手机端）。焦点进侧栏 / 按钮排时单独记一份货架下标，
  信息浮层仍显示当前选中的游戏。
- **M1.7 提示条**：底栏按键提示 4s 后淡到 28%（`bigscreen-hint-dim`），
  任何方向键 / Enter 重置。
- **M1.8 服务端排除隐藏**：`vo.GameListRequest` 新增 `exclude_hidden`（顺带补
  `metadata_source` / `exclude_metadata_source` / `tags`），`QueryGameList` 补
  `COALESCE(g.hidden, FALSE) = FALSE`，新增 `TestQueryGameListExcludesHidden`。
- **M1.9 设置分区**：`AppConfig` 新增 `bigscreen_show_hidden_game`（默认 `false`）与
  `bigscreen_default_category`（默认 `recent`，`NormalizeBigScreenDefaultCategory`
  做白名单校验），设置页新增「大屏模式」分区（`BigScreenSettingsPanel.tsx`），
  4 个语言文件同步。
- **i18n**：4 个语言文件补齐 `bigScreen.categoryAll` / `categoryRecent` /
  `favorite` / `unfavorite` / `favoriteAdded` / `favoriteRemoved` / `favoriteFailed`
  与 `settings.bigScreen.*`。`categoryAll` / `categoryRecent` 只作为 `labelKey`
  数据引用，已加入 `i18next.config.ts` 的 `preservePatterns`。

**验证**：重新生成 Wails 绑定（仅 `appconf/models.ts` 与 `vo/models.ts` 变动）；
`pnpm build`（`build:desktop` + `typecheck` + `vite build`）通过；
eslint `--max-warnings 0` 在大屏相关文件上干净（`routes/settings.tsx` 有 1 个
**既有** warning，非本轮引入）；`i18n:check` 通过；`gofmt` 无输出、
`go vet ./internal/...` 干净、`go test ./internal/appconf/... ./internal/service/gamehelper/...`
通过。大屏本身的手感与观感仍需实机确认，归入阶段 3 延后的实机测试范围。

**M1 未覆盖、留给后续**：详情层（M2）、截图画带（依赖桌面端截图能力）、
PV/预告片（M3）、氛围特效与手柄（M4）。

### M2 进展（2026-09-29 完成）

- **M2.1 详情层**：`frontend/src/bigscreen/BigScreenDetailsLayer.tsx`。整屏遮罩
  （`bg-brand-950/85` + `backdrop-blur`）上左侧封面大图、右侧标题 / 副行（原文名 ·
  开发商 · 发行日期）/ 标签 chips（≤3 + R18）/ 4 格统计块（时长 / 上次游玩 / 状态 /
  评分）/ 可滚动简介 / 按钮排 / 底部按键提示；点击遮罩或 Esc 关闭。
- **M2.2 操作收敛为两个**：「游玩」直接启动，「详细」跳完整详情页 `/game/:id`。
  货架与信息浮层的「详情」改为展开详情层，进完整页要再点一次「详细」。
- **M2.3 游玩时长**：`frontend/src/bigscreen/useGamePlaytime.ts`，按 game id 走
  `GetGameStats({ dimension: "all" })` 取 `total_play_time` 并缓存。
  `vo.GameListResponse` 仍不含时长，故只有详情层按需单查一次，信息浮层照旧不显示。
- **M2.4 焦点交接**：详情层打开时把 rail / shelf / actions 三区 `rowLengths` 置 0、
  DETAILS 区置 `[2]`，让它成为唯一有内容的区域：否则上下键会顺着 `verticalNeighbor`
  从详情层跳回货架，`firstPosition()` 也不会选中它。开关瞬间再显式 `focus` 一次——
  打开进 DETAILS，关闭回到进入前的货架下标（`useFocusEngine` 的 `setZones` 只会退回首项）。
- **i18n**：4 个语言文件补 `bigScreen.playTime` / `hintSwitchButton` / `hintBack`。

**与手机端的差异**（依据同下节「已核实的缺口」）：截图画带需要桌面端先有截图能力，
本轮降级为封面大图；当时「观看 PV」属 M3，故按钮只有两个而非三个（M3 已补齐为三个）。

**验证**：`pnpm build`（`build:desktop` + `typecheck` + `vite build`，965 modules）通过；
大屏相关文件 eslint `--max-warnings 0` 干净；`i18n:check` 通过；Go 侧 `gofmt` 无输出、
`go vet ./internal/...` 干净、`go test ./... -count=1` 全通过。详情层的观感与键鼠手感
仍需实机确认，归入阶段 3 延后的实机测试范围。

### M3 进展（2026-09-29 完成）

M3 预告片落地：数据层加字段、本地视频受管复制、详情层全屏播放器、悬停延迟背景起播。

- **M3.1 数据层**：`models.Game` 新增 `trailer_path`；`migration180`（`ALTER TABLE games
  ADD COLUMN IF NOT EXISTS trailer_path TEXT DEFAULT ''`）与 `init.go` 建表语句同步；
  列表查询（`gamehelper/list_query.go`）与 `GetGameByID` 的 SELECT / Scan 补列。
  **`trailer_path` 不进同步白名单**：桌面版同步走显式字段映射（`cloudsync/mapper.go`、
  `exporter/yukihub.go`），依 `docs/mobile-yukihub-migration.md:64-72` 所述「快照中刻意
  不包含的内容」，加字段不会泄漏进手机版快照。
- **M3.2 视频文件管理**：新包 `internal/utils/mediautils`。按「复制进数据目录」方案，
  `SaveTrailer` 把外部文件复制为 `trailers/<gameID><ext>`，换扩展名时清掉旧的其它扩展名，
  返回 `/local/trailers/...` 地址复用既有的 `LocalFileHandler`（`http.ServeFile`，原生支持
  HTTP Range，可拖动进度条）；受管目录与测试隔离分别用 `TrailersDir()` /
  `SetTrailersDirForTest()`。
- **M3.3 服务层**：`GameService` 新增 `SelectGameTrailer`（系统文件选择器，取消返回空串
  不报错）、`RemoveGameTrailer`（同时删文件与清列）与 `game-trailer:changed` 事件；路径写入
  走独立的 `updateGameTrailerPath`，不并入 `UpdateGame` 的 SET。
- **M3.4 前端设置入口**：per-game 的 `GameEditPanel.tsx` 新增预告片行（只读显示文件名，
  选择 / 清除两个动作），`routes/game.tsx` 接线 `SelectGameTrailer` / `RemoveGameTrailer`。
- **M3.5 全屏播放**：`frontend/src/bigscreen/BigScreenTrailerPlayer.tsx` 整屏黑底
  `object-contain` 播放，`onEnded` 关闭、`onError` 时 toast 并回退封面。详情层操作补齐为
  三个（游玩 / 详细 / 观看 PV），无本地 PV 时按钮**禁用而非隐藏**以保持焦点索引稳定；
  播放期把 rail / shelf / actions / details 四区 `rowLengths` 置 0、新增 TRAILER 区置 `[1]`
  并吞掉全部方向键，关闭后焦点回到详情层的「观看 PV」。
- **M3.6 悬停延迟起播**：`useTrailerHover.ts` + `BackgroundTrailerVideo.tsx`。焦点停留
  `BIG_SCREEN_TRAILER_HOVER_DELAY_MS = 1200` 后才在封面之上叠一层静音循环预告片，移开即
  卸载（DOM 里 `<video>` 归零）；`bigscreen_effect_level = off` 或浮层打开时一律不起播。
  TRAILER 区刻意不加入 `BIG_SCREEN_FOCUS_ORDER`，避免方向键被夹进播放器。
- **i18n**：4 个语言文件补 `gameEdit.trailer*` 与 `bigScreen.trailer` /
  `trailerUnavailable` / `trailerPlayFailed`。
- **WebView2 解码限制**：容器 / 编码支持有限（mkv、HEVC、AC3 常无法播放），文件选择器
  优先 `*.mp4;*.m4v;*.webm`，解码失败时 toast 并回退封面，推荐 H.264/AAC 的 mp4。

**与手机端的差异**（依据同下节「已核实的缺口」）：手机端 PV 来自抓取流程，桌面端只支持
手动选择本地视频；截图画带仍未补，桌面端沿用封面大图。

**验证**：`gofmt` 无输出、`go vet ./internal/...` 干净、`go build ./...` 通过、
`go test ./internal/migrations/... ./internal/service/... -count=1` 全通过（含
`TestMigration180AddsTrailerPath`、mediautils 单测、`TestGameService_RemoveGameTrailerClearsPath`）；
`wails3 generate bindings` 重新生成（`models.ts` 出现 `trailer_path`）；`pnpm i18n:clean`
（4 文件 +0 -0）→ `pnpm i18n:check` 干净、`pnpm typecheck` / `pnpm build` 通过、大屏与
`GameEditPanel` 相关文件 eslint 干净。注：`src/routes` 全目录存在既有 warning，
`--max-warnings 0` 在改动前的基线即不通过，本轮未新增。视频拖动 Range、mkv 解码、悬停 1.2s
起播、无 PV 时按钮禁用态、Esc/播完关闭后焦点回落与内存曲线仍需实机确认，归入阶段 3 延后的
实机测试范围。

### M4 进展（2026-09-29 完成）

M4 氛围打磨落地：特效档位、入场错峰动画、界面音效、手柄与图标、提示条按设备切换。

- **M4.1 氛围特效**：`frontend/src/bigscreen/BigScreenAtmosphere.tsx`。Canvas 粒子层，
  档位 `off` 直接返回 `null`，`low` / `high` 分别对应 `{38 粒, 半径 0.8–2.6, 速度 0.7}`
  与 `{96 粒, 半径 1–3.4, 速度 1.1}`；粒子主色从封面 24×24 采样求平均并提亮
  （`lift = min(255, round(avg*0.55 + 96))`），跨域 tainted 或加载失败时按 gameId
  派生色相兜底；`high` 额外叠同色 radial-gradient 柔光层。rAF 循环单帧更新，
  `devicePixelRatio` 封顶 2。
- **M4.2 手柄支持**：`frontend/src/bigscreen/useGamepad.ts`。Web 无按键事件，用 rAF
  轮询 `navigator.getGamepads()` 走标准映射（0=A / 1=B / 2=X / 3=Y / 4=LB / 5=RB /
  12–15=十字键，左摇杆阈值 0.5）；上升沿立即触发，长按 400ms 后每 80ms 连发，
  仅方向与切分类参与连发。返回 `connected` 供提示条判断默认设备。
- **M4.3 共用意图分发**：`routes/bigscreen.tsx` 抽出 `dispatchIntent(intent)` 与
  `activateFocused()` / `goBack()`，键盘与手柄都把输入翻译成同一组
  `BigScreenIntent`（move / category / back / confirm / details / favorite）再分发，
  避免两套行为分叉。
- **M4.4 提示条按设备切换**：`frontend/src/bigscreen/BigScreenHintBar.tsx` 新增
  `inputDevice` prop 与 `main` / `details` 两个变体；手柄显示 `✚ / A / X / Y / LB·RB / B`
  徽标，键盘显示 `← → / Enter / Esc`。设备取「最近一次实际使用过的输入」，从未使用
  时按手柄是否接入回落。
- **M4.5 界面音效**：`frontend/src/bigscreen/bigScreenSound.ts`。Web Audio 合成
  （`OscillatorNode` + 指数衰减 `GainNode`），预设 `move` / `confirm` / `back` / `toggle`；
  不新增任何二进制音频资源。受 `bigscreen_sound_enabled` 门控，懒创建 `AudioContext`
  并在 `suspended` 时 resume。
- **M4.6 入场错峰**：`constants.ts` 新增 `BIG_SCREEN_ENTER_STAGGER_MS = 42`（上限 12 项）
  与 `resolveBigScreenEnterDelay(index)`；`uno.config.ts` 新增 `bigscreen-enter` 动画令牌；
  动画挂在货架卡片的**外层绝对定位 div**（避免与内层焦点 `scale(1.045)` 的 transform
  冲突）。只播一次：900ms 后摘掉动画类，滚动时新挂载的卡片不重放。
- **M4.7 设置分区**：`AppConfig` 新增 `bigscreen_effect_level`（`off` / `low` / `high`，
  默认 `low`，`NormalizeBigScreenEffectLevel` 做白名单校验）与 `bigscreen_sound_enabled`
  （默认 `true`）；`BigScreenSettingsPanel.tsx` 新增特效档位选择与音效开关，4 个语言
  文件同步。

**与手机端的差异**（依据同下节「已核实的缺口」）：手机端手柄有独立的 `InputRouter`
与按键提示映射，桌面端按 Web Gamepad API 重写等价逻辑；手机端音效是随包的音频资源，
桌面端不新增二进制资源，改为 Web Audio 运行时合成。

**验证**：绑定重新生成（`appconf/models.ts` 新增两个字段）；`gofmt -l .` 无输出、
`go vet ./internal/...` 干净、`go test ./... -count=1` 全通过（含新增
`TestNormalizeBigScreenEffectLevel`）；`pnpm i18n:check` 通过、`pnpm typecheck` 通过、
`pnpm build`（`build:desktop` + `typecheck` + `vite build`，969 modules）通过；
改动文件 eslint `--max-warnings 0` 干净。氛围特效、音效与手柄 / 键鼠手感仍需实机确认，
归入阶段 3 延后的实机测试范围。

**M4 未覆盖、留给后续**：PV / 预告片播放属 M3；截图画带依赖桌面端截图能力；
游戏库等其余页面仍无手柄与全局快捷键。

### 实机反馈与修复（2026-09-29，用户截图驱动）

M0–M4 一直记着「待实机确认」，本轮首次真正上机跑，一次性暴露 5 个缺陷
（前 3 个是「一屏只看得见一张卡、右侧全是空白」的真正原因）：

1. **横向货架丢了定位偏移**（最严重）：`VirtualGameShelf` 的虚拟条目只有
   `absolute left-0 top-0`，**没有用 `virtualItem.start` 做 `translateX`**，
   于是所有卡片都叠在原点，只有焦点卡（`zIndex: 10`）可见。
   这是「一屏一张卡」的根因，与卡片尺寸无关
2. **卡片尺寸没有夹取**：`resolveBigScreenCardWidth` 直接把货架可视高度
   当成卡片高度，1080p 全屏下封面被放大到近 700px 宽、占满整屏。
   改为「整卡占货架高度 0.42，封面高度夹取 140–520」的纯比例 + 夹取写法
3. **入场动画与定位抢 transform**：`bigscreen-enter` 的关键帧也写 `transform`，
   且 `animation-fill-mode: both`，同层会**永久**覆盖定位用的 `translateX`。
   修法是把「定位」与「入场动画」拆到两层元素
4. **侧栏收起逻辑**：展开态是「鼠标悬停 或 焦点在侧栏」，而悬停侧栏条目会把
   焦点带进去（`onMouseEnter` → `focus(rail)`），鼠标离开时只清了 hover 标记 →
   焦点留在侧栏 → 永久展开。**空分类下没有卡片可悬停**，所以表现为
   「有游戏的分类会收起、空的不会」。修法：鼠标离开侧栏时把焦点交还货架，
   空分类退回底部按钮排
5. **亮色主题下卡片是白底**：大屏整体是硬编码暗色皮肤，而复用的 `GameCard`
   是主题相关的，主题为 light 时卡片在暗色大屏里非常突兀。
   修法：大屏根节点加 `dark` 类（UnoCSS 的 `dark: 'class'` 是祖先选择器
   `.dark .dark\:*`），让整棵子树按暗色渲染
6. 顺带：封面加载失败时会露出浏览器自带的「碎图」图标，
   `GameCoverImage` 增加失败兜底占位

新增的行标题（`rowTotal` 里那份 `headerH`）同时补上了分类名与数量。

### 照手机版重排布局（2026-09-29 第二轮实机反馈）

第一轮修完「看得见货架」后，用户指出**布局本身没照手机版做**。这次把手机的
`activity_bigscreen.xml` 与 `BigScreenSizes.java` 抽出来逐条对照，重排如下：

| 手机版事实 | 桌面端原实现 | 现在 |
| --- | --- | --- |
| `bsShelfContainer` 是 `vertical` + `gravity="bottom"`，卡片排贴底 | 卡片在可用区**垂直居中** | 卡片排贴底，上面依次是行标题与信息浮层 |
| `bsInfoBar` 是 `start\|center_vertical` 的浮层，压在背景大图上；顺序为 标题 → chips → 副行 → 按钮 | 底部一整行，标题在左、按钮在右 | 左侧竖向堆叠的浮层，顺序照抄手机版 |
| `bsRail` 固定 72dp + `elevation=10dp`，展开靠叠放层 | 侧栏宽度动画直接**推挤货架** | 侧栏固定占 76px，nav 绝对定位浮在内容之上 |
| `infoReserveH = clamp(h×0.34, 100, 150)` 预留信息浮层空间 | 信息浮层占布局高度 | 按 `clamp(内容区×0.30, 120, 190)` 预留 |
| `cardW = min(cardH×0.75, wDp×0.17)` | 只夹了高度，没有宽度上限 | 两个约束取小（宽度上限 0.17） |

尺寸预算现在是 `resolveBigScreenShelfMetrics(area)` 一个纯函数，输入内容区尺寸、
输出卡片宽 / 行标题高 / 信息预留高 / 行高（含焦点缩放余量 `CARD_SCALE_HEADROOM`）。
几个窗口的实测（脚本核对，非实机）：1920×1080 → 卡片 303px、一屏 5 张、底部整块
占 66%；1366×768 → 208px、5 张、78%；3840×2160 → 600px、6 张、49%。

**游戏库右侧详情面板同样照手机版重做**（`activity_main.xml` 的 `detailPanel`）：

- 手机版封面是一条 **82dp 高的 centerCrop 横幅**，不是竖版大海报；
  桌面端原来用 `aspect-[3/4]`，光封面就 400px 高，把按钮挤到视口外
- 手机版按钮**紧跟标题**（标题 → 按钮 → 简介 → 元信息），桌面端原来用
  `mt-auto` 贴底 → 现在改为紧跟标题，并加 `max-h` + 内部滚动兜底
- 面板改为**常驻**（未选中显示「从左侧选择一款游戏」占位，多选时也是占位）：
  面板随选中显隐会让网格宽度变化 → 列数变化 → 卡片被拉伸，
  这正是用户报的「选中游戏后卡片会放大」（实测 1250px 窗口下卡片会从
  144px 跳到 175px，+21%）。常驻面板让网格宽度恒定，卡片尺寸不再变

**教训**：这两轮都是「只看自己写的代码」出的问题。参考实现在手边
（`参考文件/*.7z`），涉及布局就应该先把对方的 layout XML 抽出来逐条对齐，
而不是凭印象做「加强版」。

### 第三轮反馈：贴底没生效 + PC 尺寸要更小（2026-09-29）

- **贴底仍失效**：货架根节点写的是 `h-full`，它作为 flex column 的子项会
  `flex-shrink` 吃掉整个剩余空间 → 「内容总高 == 容器高」→ 父级的
  `justify-end` 无事可做，卡片停在区域顶部。改成显式 `height: rowHeight`
  后剩余空间才落到信息浮层上方。（`h-full` 会静默破坏 `justify-*`/`align-*`，
  flex 里做对齐时子项高度必须显式给值。）
- **卡片过大**：手机端 `cardW ≤ wDp × 0.17` 是**小屏的妥协**（手机要尽量少露几张
  才能看清封面）。桌面端视距远、画布大，照抄 0.17 会得到 300px 一张、占屏高 4 成。
  改为 **0.12**，并把封面高度上限从固定值改为 `clamp(内容区高 × 0.28, 220, 400)`：
  1920×1080 下卡片从 303px 降到 212px、占屏高从 39% 降到 29%、一屏 7 张。
  **PC 端可以比手机端更小更干净，不要照抄手机的尺寸。**
- **「卡片左边看不见」**：货架是 `overflow-x-auto`，第一张卡放大 1.045 后
  会向左溢出约 4px 被裁掉（焦点环一起被裁）。给滚动容器加 `px-2` 留余量，
  外层再用 `-mx-2` 抵消，保证卡片左沿仍与标题左沿对齐。
- 内容层左右边距从 `px-8` 放宽到 `px-10`，PC 上不必像手机那样贴着边。

### 已核实的缺口

- 桌面端**无截图字段**（`screenshot` 只存在于 Android 契约模型与测试里）
- 桌面端**只有本地预告片能力**（M3 已落地）：仅支持手动选择的本地视频文件，
  `trailer_path` 依 `docs/mobile-yukihub-migration.md:64-72` **不入手机版同步快照**，
  无在线 PV 抓取
- ~~**无多尺寸封面**：本地封面最长边 1600px（`image_covers_optimize.go`），
  4K 背景会糊~~ **已处理（2026-09-29）**：大屏背景改为「本地封面打底 +
  `cover_source_url` 原图加载完成后 600ms 淡入替换」（`BigScreenBackground`
  的 `HiResBackgroundCover`，用 `key={url}` 让加载状态随焦点切换重置）。
  其余页面（卡片、网格）尺寸小，不需要原图
- **无手柄支持**（大屏模式已在 M4 支持手柄，游戏库等其余页面仍无）
- ~~**无全局快捷键系统**~~ **已补齐（2026-09-29）**：见下方「全局快捷键」
- 现有 `VirtualGameGrid` 是**纵向**虚拟化，横向单排货架需新写

### 可复用清单

- 后端：`GetGames` / `GetGameByID` / `GetHomePageData` / `StartGameWithTracking` /
  `StartGameWithOptions`（含 Magpie、转区）、`game-runtime:changed` 事件、
  收藏（`system:favorites` 分类，不是字段）、筛选排序
- 前端：`GameCard`、`GameCoverImage` / `ProxyImage`（含 NSFW 模糊，
  开关为 `config.blur_nsfw_game_covers`）、`GameTags`、UnoCSS 全部令牌与玻璃态
  shortcuts、`CollapsibleSection` / `BetterSwitch`、i18n 基础设施

### 验证方式

`gofmt` / `go vet` / `go test ./... -count=1` / `pnpm build`（含 typecheck）。
大屏模式本身以实机运行为准（属阶段 3 延后的实机测试范围）。

## 全局快捷键（2026-09-29 落地）

桌面端此前只有 Ctrl±/0 缩放，页面间只能靠点击侧栏。补齐一套**统一带
Ctrl/Cmd + Shift** 的导航快捷键：

| 组合 | 作用 |
| --- | --- |
| Ctrl+Shift+H / L / S / F / D / P | 首页 / 游戏库 / 统计 / 收藏分类 / 下载 / 设置 |
| Ctrl+Shift+B | 进入大屏模式 |
| Ctrl+Shift+/ | 打开快捷键速查弹窗 |

- 约定：一律带 Ctrl/Cmd + Shift，与缩放的 Ctrl+±/0 互不干扰；
  **在输入框内不触发**，免得打字被导航打断
- 分类页用 `F`（侧栏叫「收藏」）而不是 `C`，避开 Chromium 的检查元素
- 斜杠键按 `event.code`（`Slash`）匹配：美式键盘上 Shift+/ 的 `event.key`
  是 `?`，按 key 匹配会失效
- 速查弹窗用自定义事件 `yukihub:show-shortcuts` 打开，不往上提 open 状态
  （快捷键在任何页面都能触发，走 props 要把状态一路挂到根路由）
- 钩子挂在 `__root.tsx`，且**必须在大屏分支的提前 return 之前调用**，
  否则大屏模式下快捷键失效
- 设置页新增「键盘快捷键」分区（只读列表 + 打开速查表按钮），4 语言同步，
  `i18next.config.ts` 的 `preservePatterns` 加 `shortcuts.*`
  （文案键由 `GLOBAL_SHORTCUTS` 的 id 拼出来，提取器看不到）

## 不做：离线 3D 展厅（音乐厅）

Android 版的 Three.js 离线展厅（`assets/exhibition/`，约 7200 行纯 Web）可近乎零改动地
由 WebView2 承载，但 **2026-09-29 决定不做**——它就是产品里的"音乐厅"，
两者是同一项功能，本次一并确定不迁移。
（此前写作"暂缓"，措辞已更正为"不做"。）

若将来重启该项，前置工作是：

- 把 `ExhibitionBridge` 的 JS 桥接改写为 Wails 绑定
- 更换为 YukiHub 的展厅美术与数据接口
- 验证 WebGL 在 WebView2 下的兼容性与低端显卡降级策略

## 阶段 5：发布链路

- 自建更新服务（或改为不做应用内更新），替换上游 S3 / Cloudflare 方案
- 自备代码签名证书（上游依赖的 SignPath 开源免费签名资格不适用于本项目）
- 安装包、便携版、增量补丁、回滚的干净机验证
- 发布检查清单见 [AGPL-COMPLIANCE.md](AGPL-COMPLIANCE.md)

## 阶段 6：服务端相关（已按 ADR-0002 排后）

目标：把依赖服务端的能力接上本项目自有的服务器，并把冲突裁决收敛为一套实现。

依据 [ADR-0002](../decisions/0002-android-authoritative-and-sync-backend.md)：
权威源为 Android 端，同步后端为本项目自有服务器（不接上游托管服务）。
以下各项**均依赖服务端**，故整体排在阶段 1–5 之后。

- 服务端确认接受桌面端客户端（版本、User-Agent、协议版本），确定桌面端 client 标识
- 同步协议对接：桌面端接入 `/api/sync/*`（Android 版已在用的自有服务）
- 冲突裁决收敛：移除上游 `cloudsync` 的墓碑/脏表机制，改用 Android 侧的哈希比对
  （**不得两端各有一份合并算法长期并存**）
- AI 游玩报告（依赖服务端；上游的 AI 服务与防剧透配置可复用）
- 社区与好友功能（REST 契约可复用，界面重做）
- 在线账号与跨设备身份（`source_device_id` 的价值在此阶段才真正体现）

注：阶段 3 的 **WebDAV 用户自持同步保留原位**——它是用户自己的存储，
与"我们的服务器"不冲突，不属于本节。

## 本地开发与预览

本机的 Go / wails3 / pnpm / MinGW 都是便携版（在 `%USERPROFILE%\.workbuddy\tools` 下），
**没有加入系统 PATH**。为避免每次手工拼环境变量，新增了可双击的预览脚本：

- `scripts/preview.bat`：双击即可 —— 临时注入工具链环境 → 打包前端 → `wails3 build`
  → 关闭已在运行的旧实例 → 启动 `bin\YukiHub.exe`
- `scripts/preview.ps1`：实际逻辑（bat 只是调用壳），支持：
  - `-Check`：只检查工具链是否可用，不构建不启动
  - `-RunOnly`：跳过构建，直接启动已有的 `bin\YukiHub.exe`
  - `-SkipFrontend`：跳过前端打包（前端未改动时更快）
- 两个关键坑：
  1. 前端资源通过 `//go:embed all:frontend/dist` **打进二进制**，所以改完前端
     必须重新 `wails3 build`，否则直接跑 exe 看到的仍是旧界面；
     日常调 UI 建议用 `wails3 dev`（Vite 热更新 + Go 改动自动重编译）
  2. 系统里的 `pnpm` 是坏的（corepack 缓存缺失），脚本会通过 `COREPACK_HOME`
     指向 `.workbuddy\tools\corepack` 并优先使用 `corepack-bin\pnpm.cmd`

## 持续事项

- 每次发布前执行第三方依赖许可证审计
- `THIRD_PARTY_LICENSES.md` 与 `NOTICE` 随依赖变化更新
- 与上游保持"硬分叉不回灌"的定位；如需变更，先写 ADR

---

## Linux（amd64）支持恢复（2026-10-06）

范围与决策见 [ADR-0004](decisions/0004-restore-linux-support.md)。已完成：

- [x] 从移除前提交取回 Linux 平台实现并适配当前共享代码：
      进程识别（`processutils/process_linux.go`）、启动策略
      （原生 / Wine / Proton / Steam）、退出监听、URL 协议注册、
      Steam 集成（兼容工具 VDF / Proton prefix / 客户端重启）、
      Wine/Proton 辅助工具（`compattools` + `protonutils` + `tricksutils`）
- [x] 恢复 `appconf` 的 Wine / CrossOver 配置字段与迁移逻辑（旧配置零值兼容，无 DB 迁移）
- [x] 回移上游修复：`detector_linux.go` 忽略 Steam runtime helper（f484c02）
- [x] 恢复打包链：`scripts/build.sh`、`scripts/patch-wails-linux-tray.sh`、
      `build/linux/*`、`lib/linuxamd64/7z/7zz`；产物为 deb / rpm / AppImage
- [x] CI：`autobuild.yml` / `release.yml` 新增 Linux amd64 作业，
      `internal.yml` 新增 Linux 检查作业
- [x] 前端：恢复 Wine/Proton 启动面板与设置项、平台分支与四语言文案
- [x] `wails3 generate bindings -clean=true -ts` 在 Windows 构建机重跑并提交
      （此前提交的生成物与生成器输出有偏差：`index.ts` 少导出 4 个新类型、
      `$$createTypeN` 别名编号不同 —— 现在重跑一次应为零差异，可当"生成物是否被手改"
      的探测器用）
- [x] Windows 侧全量核验（2026-10-08）：`go build ./...`、`gofmt -l .`、`go vet ./...`、
      `go test ./... -count=1 -p 1` 全绿；updater 独立模块 build + test 全绿；
      前端 `tsc` / `i18n:check` / `uno:check` 通过，ESLint 与合并前同量
- [ ] 待办：在装有 Linux 构建依赖的机器/CI 上完成端到端核验
      （CGO + DuckDB 的完整 `go build`、真机启动 / 计时 / 托盘 / 协议唤醒 /
      deb·rpm·AppImage 安装）

本机验证记录（无 GTK / CGO 工具链的开发机，2026-10-06）：

- `gofmt -l .` 无输出；可编译包 `go vet` 干净
- Linux + Windows 双平台：平台层 15 个包 `go test ./... -count=1` 全绿；
  updater 模块 Linux 下 vet/test 全绿
- 前端 `pnpm typecheck` / `pnpm build` / `pnpm i18n:check`（四语言 1625 键一致）通过
- 受 cgo（DuckDB）与 GTK 开发库限制，`internal/service`、`internal/service/importer`
  与主包未能本机编译，留待 CI / 构建机核验
