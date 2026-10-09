# ADR-0001：以 LunaBox 硬分叉作为 YukiHub for Desktop 的基线

- 状态：已采纳
- 日期：2026-09-27
- 决策者：YukiHub 项目组

## 背景

YukiHub 目前只有 Android 版本（`com.yuki.yukihub`，Java 11 + 传统 View，约 8 万行，无自动化测试）。
我们需要一个 Windows 桌面版，能力定位与 Android 版一致：Galgame / 视觉小说库管理、启动、游玩时长统计、
资料刮削、数据同步、备份恢复。

候选路线有三条：

1. 从零自研 Windows 桌面版
2. 直接分叉 LunaBox 并换皮
3. 硬分叉 LunaBox 作为内核基线，重写 YukiHub 的产品层

已确认的关键事实：

- Android 版约 8 万行代码中，Android API（`Context` / SAF / Intent / `SQLiteDatabase` / `SharedPreferences`）
  与 "在手机上运行游戏" 的能力（Kirikiri、ONS、Tyrano、Artemis、Winlator、Shizuku、触控光标注入）
  占很大比重，这部分在 Windows 上没有对应物或本身就是负资产。
- Android 版没有任何单元测试，且存在 1.1 万行的 `MainActivity`，无法作为可移植的代码底座。
- LunaBox 已经实现了 Windows 桌面版最难的部分：进程识别与退出监听、前台窗口时长统计、
  Locale Emulator 启动、Magpie 联动、托盘、URL 协议、代理、后台静音、
  NSIS 安装器、应用内增量更新与签名校验。
- 两个项目的备份格式已经互相打通：LunaBox 有 YukiHub 备份导入器，
  Android 版有经真实备份包验证的 LunaBox 导入器。
- 两个项目使用同源元数据渠道（VNDB、Bangumi、月幕、Hikarinagi）。

## 决策

**采用路线 3：在固定提交上硬分叉 LunaBox，保留其 Windows 基础设施与工程规范，
重写 YukiHub 的产品界面与领域模型，并以契约形式迁入 Android 版的差异化能力。**

### 具体含义

- 基线：LunaBox v1.13.0 源码快照，作为本仓库的第一个提交，且**不跟随上游 rebase**。
- 保留：`internal/utils` 下的 Windows 能力、`internal/migrations`、`build/windows/**`、
  `updater/`、`internal/protocol`、`internal/wailsruntime`、service 分层与依赖注入纪律。
- 重写：前端产品界面、品牌与身份标识、更新与云服务端点、游戏领域模型、社交与聊天界面。
- 以契约迁入（不移植 Java 实现）：
  - YukiHub schema 5 备份 / 同步协议
  - 游玩记录合并语义（总时长取 max、`playtime_reset_at`、会话 UUID 幂等）
  - 元数据、社交、AI 接口契约
  - 离线 3D 展厅（纯 Web，本项目暂缓实现）
- 不迁入：Android 内置游戏引擎、模拟器启动适配、Shizuku、SAF、触控光标、`.nomedia` 管理。

## 理由

- **工期**：从零实现 Windows 进程监控、LE 启动、增量更新、签名校验与安装器，
  在同等质量下远超产品层改造的成本。
- **风险**：Windows 系统编程细节多且难以靠文档补齐，LunaBox 的实现已经过真实用户验证。
- **可维护性**：LunaBox 的 service 分层、单向依赖约束与 migration 幂等要求，
  使后续裁剪与重写有明确边界。
- **数据迁移**：双向导入路径已存在并被真实数据验证，迁移风险可控。

## 后果

### 正面

- 首版可以直接获得成熟的 Windows 集成与安装更新链路。
- Android 版的数据契约可以完整承接，用户从手机迁移到 PC 有明确路径。
- 上游已有的元数据、AI、云同步能力可直接复用。

### 负面与需要接受的成本

- **许可证升级为 AGPL-3.0**。上游 LunaBox 使用 AGPL-3.0，本项目因此整体按 AGPL-3.0 授权。
  Android 版为 GPL-3.0，桌面版不能回退到 GPL-3.0，也不能闭源。
  （GPLv3 第 13 节允许 GPL 作品与 AGPL 作品组合，反向不成立。）
- 需要承担约 180 个 migration、DuckDB 数据层与既有领域模型的阅读与裁剪成本。
- 依赖 Wails v3 beta，需要锁定版本并以 `internal/wailsruntime` 作为唯一隔离层。
- 上游 CI 曾用 `go test -run '^$'` 跳过全部测试执行，本项目必须先建立真实测试基线。
- 品牌、更新服务、云服务与代码签名在最后一公里集中爆发，需要提前准备。

## 被否决的方案

### 从零自研

品牌与架构自由度最高，但 Windows 系统能力全部需要重新验证，且没有测试网兜底，
无法在合理周期内达到可用质量。

### 直接换皮

初期出包最快，但会遇到三个必然问题：

1. LunaBox 的领域模型是 "PC 游戏库" 语义，YukiHub 是 "视觉小说条目" 语义，
   单向一次性导入器无法支撑双向持续同步。
2. 品牌与更新/云服务端点在发布链路上硬耦合，且前端生成绑定随模块名变化，
   换皮本质上仍需重写。
3. 在无测试保护的前提下改脏代码，后期不可维护。

## 追加约束

- 若将来需要闭源或商业双授权，本决策不再成立，必须改为参考实现重写，
  并保证不残留任何 LunaBox 代码痕迹。
- 与上游的关系定位为 "硬分叉、不回灌"，如需与上游保持同步，应重新评估。
- Android 版与桌面版互为同一产品家族的两端，数据格式变更需两端同步评审。
