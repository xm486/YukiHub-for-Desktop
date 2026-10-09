# YukiHub Desktop

Galgame / 视觉小说库管理、启动与游玩记录工具 —— YukiHub 的桌面版（Windows / Linux）。

<p align="center">
  <img src="https://img.shields.io/badge/Platform-Windows%2010%2F11-0078D6?logo=windows&logoColor=white" alt="Windows" />
  <img src="https://img.shields.io/badge/Platform-Linux%20amd64-FCC624?logo=linux&logoColor=black" alt="Linux" />
  <img src="https://img.shields.io/badge/Go-1.27.1-00ADD8?logo=go&logoColor=white" alt="Go" />
  <img src="https://img.shields.io/badge/Wails-v3%20beta-orange" alt="Wails v3" />
  <img src="https://img.shields.io/badge/License-AGPL--3.0-blue.svg" alt="AGPL-3.0" />
</p>

<p align="center">
  <a href="./README.md">简体中文</a> | <a href="./README.en.md">English</a>
</p>

> **开发状态：可用但未发行。** 代码可构建、可日常使用，界面与行为仍在按 Android 手机版
> 逐项对齐（见 [docs/ROADMAP.md](docs/ROADMAP.md)）。**尚未发布正式安装包**，
> 也还没有代码签名。

## 这是什么

YukiHub 有两个端：

| 端 | 仓库 | 许可证 |
| --- | --- | --- |
| Android 手机版 | https://github.com/xm486/YukiHub | GPL-3.0 |
| 桌面版（本仓库，Windows / Linux） | https://github.com/xm486/YukiHub-for-Desktop | AGPL-3.0 |
| 桌面版（GitCode 托管） | https://gitcode.com/xm486/YukiHub-for-Desktop | AGPL-3.0 |

桌面版同时托管在 GitHub 与 GitCode（国内代码托管平台）两个平台，两边内容一致。

桌面版的目标不是把手机版原样搬过来，而是与手机版共享**同一套数据语义和使用习惯**：
游戏库、游玩记录、资料刮削、同步与备份。两端的数据可以互相导入导出，
云同步走的是同一份快照格式（schema 5），详见
[迁移设计](docs/mobile-yukihub-migration.md)。

## 特性

### 游戏库

- 手动添加、目录批量扫描、拖入导入
- 从第三方迁移：Playnite、PotatoVN、Vnite、ReinaManager、LunaBox，以及 YukiHub 备份（`.ykbak`）
- 分类、收藏、多维筛选、大屏（BigScreen）浏览模式
- NSFW 标记、隐藏、封面源优先度

### 启动与游玩

- Windows 游戏进程识别与退出监听，自动统计时长
- **内置** Locale Emulator（日文游戏转区）与 Magpie（窗口缩放），也允许替换成自己的版本
- 存档备份（SaveData）、托盘常驻、开机自启、`yukihub://` 协议唤醒

### 资料刮削

- 多来源：VNDB、Bangumi（含镜像）、月幕 Gal、Hikarinagi、未萌 NextMoe、Steam、TouchGAL、DLsite、ErogameScape
- 刮削结果本地缓存，可离线查看；可在设置里指定首选来源与封面优先度

### 账号与社交（YukiHub 账号）

- 邮箱注册 / 登录 / 找回密码
- 第三方快捷登录：未萌（NextMoe）、Hikarinagi —— OAuth 授权码 + PKCE，本地 loopback 回调，
  客户端不接触第三方令牌
- 云端同步与自动备份（与手机版同构的 schema 5 快照）
- 好友列表（正在游戏 / 在线 / 离线）、私聊、群聊、表情包与图片消息、@提及、用户资料页

### 首页

- 游玩数据概览、快捷启动滑轨、游戏轮播
- **Galgame 资讯**轮播（数据来自未萌 `/v2/news`，可手动滑动、点开看详情与原文）

## 技术栈

### 桌面端

| 组件 | 说明 |
| --- | --- |
| [Go](https://go.dev) 1.27.1 | 后端语言 |
| [Wails](https://v3alpha.wails.io) v3 beta（`v3.0.0-beta.24`） | Go + WebView 桌面应用框架，项目内统一走 `internal/wailsruntime` 这层隔离 |
| [DuckDB](https://duckdb.org)（`duckdb-go/v2`） | 游戏库与统计的主存储（预编译库在 `lib/`） |
| SQLite | 第三方数据导入时读取 |
| WebView2 | Windows 端渲染运行时 |

### 前端（`frontend/`）

| 组件 | 说明 |
| --- | --- |
| [React](https://react.dev) 18 + TypeScript | UI |
| [Vite](https://vite.dev) | 构建 |
| [UnoCSS](https://unocss.dev)（含 Material Design Icons 图标集） | 原子化样式 |
| [Zustand](https://zustand-demo.pmnd.rs) | 应用状态 |
| [TanStack Router](https://tanstack.com/router) / [Query](https://tanstack.com/query) | 路由与异步数据 |
| [i18next](https://i18next.com) | 多语言（简体中文 / 繁體中文 / English / 日本語） |
| [Chart.js](https://www.chartjs.org) | 统计图表 |
| `@wailsio/runtime` | 与 Go 后端的绑定调用 |

> `frontend/bindings/` 由 `wails3 generate bindings` 生成，**不要手改**。

### 打包与内置工具

- NSIS 3.13 安装器（`build/windows/nsis`），前端产物通过 `go:embed frontend/dist` 打进 exe
- 内置兼容工具（`build/compat-tools`，附许可证全文）：
  - [Locale Emulator](https://github.com/xupefei/Locale-Emulator) 2.5.0.1（LGPL-3.0）
  - [Magpie](https://github.com/Blinue/Magpie) 0.12.1（GPL-3.0）

### 外部服务

- 资料与元数据：VNDB、Bangumi、月幕 Gal、Hikarinagi、未萌 NextMoe、Steam、TouchGAL、DLsite、ErogameScape
- 账号 / 好友 / 聊天 / 云同步：YukiHub 账号服务
- 首页资讯：未萌 `api.nextmoe.dev/v2/news`（免密钥，经本地后端代理并落盘缓存）

### 身份标识

- Go 模块 `yukihub`、应用 ID `com.yukihub.desktop`、URL 协议 `yukihub://`
- 数据目录 `%APPDATA%\YukiHub`（便携版放在 exe 同级的 `bin/`），库文件 `yukihub.db`

## 从源码构建

> 支持平台：**Windows 10/11** 与 **Linux（amd64）**。macOS / iOS 不在支持范围。

环境要求：

- Go（版本见 `go.mod`，当前 **1.27.1**）
- **CGO 编译器**：DuckDB 依赖 CGO。Windows 需要 MinGW-w64 的 gcc；
  Linux 需要 `build-essential`
- Node.js **22 或更高** 与 **pnpm 12**（`frontend/package.json` 的 `packageManager` 已固定版本，
  用 `corepack enable` 自动获取即可）
- Wails v3 CLI，版本必须与 `go.mod` 中的 `github.com/wailsapp/wails/v3` **完全一致**

**Linux（amd64）额外依赖与补丁脚本：**

```bash
sudo apt-get install -y build-essential pkg-config desktop-file-utils \
  libayatana-appindicator3-dev libgtk-4-dev libwebkitgtk-6.0-dev
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest   # deb / rpm 打包
# AppImage 打包另需 appimagetool（见 CI / docs/fork-setup.md）

# ⚠️ 直接 go build 前必须先执行 Wails 补丁（托盘 / WebKitGTK 渲染兼容）；
#    wails3 dev、Taskfile 与 scripts/build.sh 已内置该步骤
./scripts/patch-wails-linux-tray.sh

./scripts/build.sh all <版本号> amd64   # 产出 deb / rpm / AppImage
```

通用开发流程：

```bash
# 1. 前端依赖
cd frontend && pnpm install && cd ..

# 2. 生成 Wails 绑定（后端 vo / 枚举 / 方法签名变更后必须重跑）
wails3 generate bindings -clean=true -ts

# 3. 开发模式
wails3 dev -config ./build/config.yml -port 9245

# 4. 构建
pnpm --dir frontend build   # ⚠️ 必须：wails3 build 不会构建前端
wails3 build
```

> **坑：`wails3 build` 只编译 Go，不会重新构建前端。** 它只是把 `frontend/dist`
> 用 `go:embed` 打进去。改了前端代码却只跑 `wails3 build`，结果会是「exe 是新的、
> 界面还是旧的」。正确顺序永远是：**改前端 → `vite build` → `wails3 build`**。

完整打包：Windows 见 `scripts/build.bat`，Linux 见 `scripts/build.sh`。

提交前自检：

```bash
gofmt -l .                 # 应无输出
go vet ./...
go test ./... -count=1
cd frontend && pnpm typecheck && pnpm lint && pnpm i18n:check
```

## 项目结构

```
internal/    后端：service（业务）、models（数据模型）、utils、migrations
frontend/    前端：src（源码）、bindings（生成物，勿手改）
build/       打包：windows/nsis、compat-tools（内置兼容工具）
lib/         DuckDB 等预编译二进制
docs/        设计文档与流程，技术决策放 docs/decisions/（ADR）
scripts/     构建脚本
```

## 分叉说明与待配置项

本仓库是 [LunaBox](https://github.com/Saramanda9988/LunaBox) v1.13.0 的**硬分叉**：
不回灌上游、不跟随上游 rebase，支持 Windows 与 Linux（amd64），不面向 macOS。

代码层面的品牌替换与功能重建已完成，但**仓库地址、代码签名与更新服务**
必须由 YukiHub 自行配置后才能发布，完整清单见
[docs/fork-setup.md](docs/fork-setup.md)。

上游版权与修改记录见 [docs/upstream-lunabox.md](docs/upstream-lunabox.md)。

## 开源许可

本项目采用 **GNU Affero General Public License v3.0（AGPL-3.0）**，全文见 [LICENSE](LICENSE)。

- 本项目是修改版本，基于 LunaBox 开发，**并非上游官方发行版**，上游不提供担保或支持。
- 上游项目版权归 LunaBox contributors 所有，同样采用 AGPL-3.0。
- 版权与修改声明见 [NOTICE](NOTICE)，合规义务说明见 [docs/AGPL-COMPLIANCE.md](docs/AGPL-COMPLIANCE.md)。
- 第三方组件许可证见 [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md)。

## 免责声明

本项目仅用于管理和启动你**有权使用**的游戏、应用或资源。

本项目不提供游戏本体、破解资源或任何绕过授权的能力，也不为违规用途提供支持。

## 参与贡献

- 提交前请确保 `gofmt`、`go vet`、`go test` 均通过。
- 后端改动请遵循 [docs/backend.md](docs/backend.md) 的分层与依赖注入约束。
- 前端改动请遵循 [docs/frontend.md](docs/frontend.md)。
- 涉及数据结构变更时，**必须**同步更新 [迁移设计](docs/mobile-yukihub-migration.md) ——
  Android 版需要与桌面版保持数据语义一致，否则双端同步会错乱。
- 大型技术决策请先写 ADR，放在 `docs/decisions/` 下。
