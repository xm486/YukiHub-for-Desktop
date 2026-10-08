# 第三方组件与许可证

本文件列出 YukiHub Desktop 分发或链接的第三方组件及其许可证。

> 说明：上游 LunaBox 仓库中没有任何第三方许可证清单。本文件是 YukiHub Desktop
> 新增的合规材料，用于满足随二进制分发第三方组件时的署名与许可证义务。
> 如果你发现遗漏或有误，请提交 Issue 或 PR。

## 一、随安装包分发或运行时加载的组件

这些组件会随安装包一起分发，或由应用在运行时从本机加载，因此有明确的署名与许可证义务。

### 1. 7-Zip（解压 7z / rar / zip 等归档）

- 随包文件：`lib/winamd64/7z/7z.dll`、`lib/winamd64/7z/7z.exe`（以及 arm64 版本）
- 版权：Copyright (C) 1999-2025 Igor Pavlov
- 许可证：GNU LGPL v2.1 或更高版本为主；部分代码为 BSD 3-clause / BSD 2-clause；
  RAR 解压部分附带 unRAR 授权限制
- 许可证全文：`third_party/7zip/LICENSE.txt`

义务提示：以二进制形式再分发时必须随附上述许可证信息。RAR 解压相关代码不得用于
开发 RAR（WinRAR）兼容的压缩器（仅限解压用途）。

### 2. DuckDB（游戏库数据库）

- 随包文件：`lib/winarm64/duckdb.dll`；amd64 版本由 `duckdb-go-bindings` 静态链接
- 版权：Copyright 2018-2026 Stichting DuckDB Foundation
- 许可证：MIT
- 许可证全文：`third_party/duckdb/LICENSE`

### 3. Microsoft Edge WebView2 Runtime（界面渲染宿主）

- 不由本仓库分发。安装器会引导安装微软官方运行时，应用运行时加载系统已安装的 WebView2。
- 许可证：Microsoft 软件许可条款（随 WebView2 Runtime 分发）
- 因此本产品不承担 WebView2 的再分发义务，但用户界面运行依赖该系统组件。

## 二、Go 直接依赖

以下为 `go.mod` 中声明的直接依赖。许可证以各项目仓库为准。

| 模块 | 版本 | 用途 | 许可证 |
| --- | --- | --- | --- |
| github.com/wailsapp/wails/v3 | v3.0.0-beta.24 | 桌面应用框架 | MIT |
| github.com/duckdb/duckdb-go/v2 | v2.5.6 | DuckDB 驱动 | MIT |
| github.com/aws/aws-sdk-go-v2（含 config/credentials/service/s3） | v1.41.5 等 | S3 兼容对象存储 | Apache-2.0 |
| github.com/PuerkitoBio/goquery | v1.10.3 | HTML 抓取 | BSD-3-Clause |
| github.com/cavaliergopher/grab/v3 | v3.0.1 | 分块下载 | MIT |
| github.com/gen2brain/webp | v0.5.5 | WebP 编解码 | MIT |
| github.com/google/uuid | v1.6.0 | UUID 生成 | BSD-3-Clause |
| github.com/joho/godotenv | v1.5.1 | 构建期环境变量加载 | MIT |
| github.com/labstack/gommon | v0.4.2 | 通用工具 | MIT |
| github.com/mattn/go-runewidth | v0.0.19 | 终端宽度计算 | MIT |
| github.com/mattn/go-sqlite3 | v1.14.48 | 读取第三方管理器数据库 | MIT |
| github.com/spf13/cobra | v1.10.2 | CLI 框架 | Apache-2.0 |
| github.com/syndtr/goleveldb | v1.0.0 | 读取 Vnite / ReinaManager 数据 | BSD-2-Clause |
| github.com/zeebo/blake3 | v0.2.4 | BLAKE3 哈希 | CC0-1.0 / Apache-2.0（需核实） |
| golang.org/x/image、golang.org/x/mod、golang.org/x/sys | — | Go 官方扩展库 | BSD-3-Clause |
| golift.io/xtractr | v0.3.0 | 归档解压封装 | MIT |
| resty.dev/v3 | v3.0.0-rc.3 | HTTP 客户端 | MIT |
| github.com/Umbrae-Labs/umbra-sdk/umbra-go | v0.3.0 | Umbra 云备份客户端（可选后端） | 需核实 |

此外，构建时还会引入 `apache/arrow-go`、`klauspost/compress`、`nwaples/rardecode` 等
间接依赖。

## 三、前端直接依赖

`frontend/package.json` 中的依赖以 MIT 为主，包括 React、TanStack Router/Query、
zustand、i18next、Chart.js、html2canvas、@headlessui/react、@radix-ui/react-switch、
emoji-picker-element、fast-average-color、react-image-crop、react-hot-toast、date-fns
以及 @wailsio/runtime 等；UnoCSS 与 ESLint 相关工具链为 MIT。

## 四、发布前必须完成的合规动作

以下事项在首次公开发布二进制**之前**必须完成：

1. **补充 GNU LGPL v2.1 全文**。`third_party/7zip/LICENSE.txt` 目前包含 7-Zip 官方
   许可证文件全文（其中引用 LGPL 并给出获取方式），但未内嵌 LGPL v2.1 完整条文。
   发布前请将 LGPL v2.1 全文加入 `third_party/lgpl-2.1.txt`。
2. **执行一次自动化许可证审计**，补齐上表中标注"需核实"的条目，并覆盖全部间接依赖：

   ```bash
   # Go 侧（需先安装：go install github.com/google/go-licenses@latest）
   go-licenses check ./... --allowed_licenses=MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,CC0-1.0,LGPL-2.1-only,LGPL-2.1-or-later

   # 前端侧
   cd frontend && pnpm dlx license-checker-rseidelsohn --summary
   ```

3. **在应用内提供开源许可入口**，展示 AGPL-3.0、上游署名与本文件内容
   （见 `docs/AGPL-COMPLIANCE.md` 与应用内"关于"面板）。
4. **安装包内附带 `LICENSE`、`NOTICE`、`THIRD_PARTY_LICENSES.md` 与 `third_party/` 目录**。
