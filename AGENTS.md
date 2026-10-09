# YukiHub Agent 地图

> 这是一份**地图**，不是手册。先读这里，按需跳转专题文档。

## 项目概况

Wails v3 Alpha 桌面应用（**Windows + Linux amd64**）。
前端：React + TypeScript + UnoCSS（presetWind3）+ Zustand + TanStack Router。
后端：Go + DuckDB + 自研 migrations。

macOS / iOS 的平台实现已移除。Linux（amd64）支持已于 2026-10-06 恢复，
见 [ADR-0004](docs/decisions/0004-restore-linux-support.md)。
**新增代码 MUST NOT 引入 macOS 平台分支**；Linux 分支必须落在 `_linux.go`
文件或带显式 `runtime.GOOS == "linux"` 判断，界面侧使用 `platformGOOS`。

## 项目身份（影响每一次改动）

本仓库是 **YukiHub Desktop**（仓库名 `YukiHub-for-Desktop`，支持 Windows 10/11
与 Linux amd64），LunaBox v1.13.0 的硬分叉。

- 整体按 **AGPL-3.0** 授权（GPL-3.0 的 Android 版与本项目为同一产品家族，但本项目不能闭源）。
- 与上游定位为"硬分叉、不回灌"，不要试图与上游保持同步。
- 任何改动都可能成为"修改版本"的一部分，因此：
  - MUST 保留 `LICENSE` 原文与上游版权声明，不得删除或改写。
  - MUST 发布前同步更新 `NOTICE`、`docs/upstream-lunabox.md` 的修改记录。
- 产品与来源信息的唯一来源是 `internal/version`（`AppDisplayName`、`RepositoryURL`、
  `UpstreamProject` 等），MUST NOT 在别处再硬编码一份。
- 平台支持边界：**Windows 10/11（amd64/arm64）+ Linux（amd64）**。
  macOS / iOS 不在支持范围；Linux 构建依赖 GTK4 与 WebKitGTK 6.0。

## 关键词优先级

- **MUST**：必须遵守（违反即视为实现不合格）
- **SHOULD**：强烈建议（有明确原因可偏离，需说明）
- **MAY**：可选

当本文件与用户需求冲突时：**以用户需求为准**。

---

## 核心约束（每次任务都适用）

1. **最小可行改动**：优先改动已有文件，复用已有模式，不新增架构性模块。
2. **先搜再新建**：新增组件/函数/SQL 前，先搜索对应目录是否已有实现。
3. **跟随仓库风格**：路由组织、service 注入、migration 形态等，沿用当前写法。
4. **一次只解决一个问题域**：不顺手重构或格式化不相关代码。
5. **统一 User-Agent（MUST）**：后端 HTTP 请求不得硬编码应用 UA，默认值统一调用 `internal/version.UserAgent()`；仅当上游明确要求时才允许传入完整自定义 UA，版本号不得单独维护。
6. **测试必须真实通过（MUST）**：提交前 `go test ./... -count=1` 必须通过。
   禁止用 `-run '^$'`、`t.Skip` 或注释掉用例的方式让 CI 变绿。
7. **第三方凭据不得复用他人身份（MUST）**：所有 OAuth / API 凭据由构建期注入或用户配置，
   未配置时给出"未配置"提示，不得回退到上游或他人的应用身份。
8. **提交前格式与静态检查（MUST）**：`gofmt -l .` 无输出，`go vet ./...` 无错误。
9. **数据语义变更需两端评审（MUST）**：涉及游戏条目、游玩记录、同步格式的改动，
   必须同步更新 `docs/mobile-yukihub-migration.md`，因为 Android 版共用该语义。
10. **大型技术决策先写 ADR（SHOULD）**：放在 `docs/decisions/`，格式参考已有编号文件。

---

## 何时必须问用户

仅以下情况才追问（否则按最简单解释执行）：

- 需求影响数据结构/迁移策略（是否需要数据回填、是否允许破坏性迁移）
- UI/交互存在多种合理方案且影响用户使用习惯（如新增入口位置）
- 需要引入新依赖或大改目录结构

---

## 关键文件落点（快速定位）

详见 → [docs/anchors.md](docs/anchors.md)

| 类型 | 文件 |
|------|------|
| 路由注册 | `frontend/src/App.tsx` |
| 根布局 | `frontend/src/routes/__root.tsx` |
| 全局 Store | `frontend/src/store.ts` |
| UnoCSS 配置 | `frontend/uno.config.ts` |
| 后端启动/注入 | `main.go` |
| 初始建表 | `internal/migrations/init.go` |
| Migrations | `internal/migrations/migrations.go` |
| Services | `internal/service/*_service.go` |
| 后端工具函数 | `internal/utils/*`（按场景细分 package） |

---

## 渐进式披露阅读顺序

按任务复杂度逐层展开，不要一上来通读全部文档：

1. 先看本文件，只确定任务属于前端、后端还是流程问题
2. 涉及后端 service / DB / migration：先读 [backend.md](docs/backend.md)
3. 涉及文件、压缩包、图片、进程、代理、元数据抓取等辅助能力：再从 [backend-utils.md](docs/backend-utils.md) 进入对应 `internal/utils/*` 子包
4. 仍不确定文件落点：回到 [anchors.md](docs/anchors.md)

---

## 专题文档索引

按任务类型，读取对应文档：

| 任务类型 | 读取文档 |
|----------|----------|
| 新增/修改前端页面、组件、样式 | [frontend.md](docs/frontend.md) |
| 新增/修改后端 service、DB、migration | [backend.md](docs/backend.md) |
| 需要复用后端工具函数（`internal/utils/*`） | [backend-utils.md](docs/backend-utils.md) |
| 不确定文件落点 | [anchors.md](docs/anchors.md) |
| 提交前自检、变更流程 | [workflow.md](docs/workflow.md) |
| 了解项目从哪来、改了什么、为什么 | [upstream-lunabox.md](docs/upstream-lunabox.md)、[decisions/](docs/decisions/) |
| 许可证义务、发布前合规检查 | [AGPL-COMPLIANCE.md](docs/AGPL-COMPLIANCE.md) |
| 下一步做什么、验收标准 | [ROADMAP.md](docs/ROADMAP.md) |
| 发布前要配置的外部资源 | [fork-setup.md](docs/fork-setup.md) |
| 与 Android 版的数据契约 | [mobile-yukihub-migration.md](docs/mobile-yukihub-migration.md) |

---

## 交付要求

- 后端改动：`go build -tags dev` 不新增编译错误，`wails3 generate bindings -ts` 可运行。
- 前端改动：`pnpm build` 可通过。

## IMPORTANT

- MUST `frontend/bindings/` 是 Wails v3 自动生成的绑定，不要手改；业务代码通过具体 service 文件或 `frontend/src/bindings/` 兼容入口使用后端类型，不要依赖可能产生重复导出的 package 聚合 `index.ts`。
