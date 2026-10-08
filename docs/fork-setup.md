# 分叉配置清单

本仓库是 LunaBox 的硬分叉。代码层面的品牌替换已经完成，但**有些东西无法靠改代码解决**，
必须在真正发布前逐项配置。本文件就是这份清单。

## 1. 仓库与身份

| 项目 | 当前占位值 | 需要确认 |
| --- | --- | --- |
| 仓库地址 | `https://github.com/xm486/YukiHub` | 确认桌面版是复用该仓库还是新建独立仓库；若新建，需全局替换 |
| 应用标识 | `com.yukihub.desktop` | 确认命名空间符合预期（安装路径、单实例 ID、注册表协议键均依赖它） |
| URL 协议 | `yukihub://` | 若与 Android 版协议冲突需重新选定 |
| 用户数据目录 | `%APPDATA%\YukiHub`、`%LOCALAPPDATA%\YukiHub` | 确认与 Android 版不冲突（Android 为应用私有目录，不冲突） |

涉及文件中已统一使用该标识，如需变更请全局搜索：
`com.yukihub.desktop`、`yukihub://`、`xm486/YukiHub`。

## 2. 第三方服务凭据（必须自行申请）

上游硬编码了自家的凭据，本仓库不使用。账号授权的完整方案见
[ADR-0003](decisions/0003-account-authorization-strategy.md)：**Bangumi 走个人令牌**
（用户自己粘贴，我们不需要 OAuth 应用），**Hikarinagi / NextMoe 走原生 OAuth**
（native public client，默认复用 YukiHub Android 客户端的 id）。

以下凭据按需申请，并通过 CI Variables / Secrets 或本地 `.env.build` 注入：

| 服务 | 变量 | 用途 |
| --- | --- | --- |
| Bangumi | *不需要* | 改用用户自填的个人 Access Token（见 ADR-0003） |
| Hikarinagi | `YUKIHUB_HIKARINAGI_CLIENT_ID` | 账号登录、元数据。默认复用 YukiHub Android 客户端，**仅在另建桌面端 OAuth 应用时才需要注入** |
| Hikarinagi | `YUKIHUB_HIKARINAGI_SCOPES` | 授权范围，默认 `openid user:read`（= Android 客户端被授权的那一组）。**多要一项服务端就会以 `invalid_scope` 拒绝整个授权**；想启用「状态回写 / 令牌自动刷新」需先在后台给应用加权限，再注入覆盖 |
| Hikarinagi | `YUKIHUB_HIKARINAGI_METADATA_CLIENT_ID` / `_SECRET` | **元数据** API 的应用级凭据（client_credentials，与登录是**两个不同的 OAuth 应用**）。**已内置**（与 Android 端同源），仅在轮换密钥或改用自建应用时才需要注入 |
| TouchGAL | `YUKIHUB_TOUCHGAL_TOKEN` | 元数据接口 |
| Umbra | `YUKIHUB_UMBRA_CLIENT_ID` / `YUKIHUB_UMBRA_REGISTRATION_TOKEN` | 可选云备份后端 |
| 更新服务 | `YUKIHUB_UPDATE_SERVICE_URL` | 应用内更新检查地址 |

### 桌面端第三方 OAuth 回调地址

桌面端按 RFC 8252 走 loopback，**端口固定**，所以 redirect_uri 是确定的、注册一次长期有效：

| 服务 | 桌面端 redirect_uri | Android 版 redirect_uri |
| --- | --- | --- |
| Hikarinagi | `http://127.0.0.1:14791/callback` | `yukihub://hikarinagi/callback` |
| NextMoe | `http://127.0.0.1:14792/callback` | `yukihub://oauth/callback` |

去平台后台确认**能否给同一个 OAuth 应用追加多条 redirect_uri**：
能就复用现有应用（首选，少申请一次），不能就另建一个桌面端应用并把新 client id 注入。


未配置时相关功能应给出"未配置"提示，而不是回退到他人的应用身份。

## 3. 代码签名

上游依赖 SignPath 的开源项目免费签名，**该资格属于上游项目，不随代码转移**。

需要做其中一项：

- 自行向 SignPath 申请开源签名资格（需证明仓库归属与开源属性）
- 或购买普通代码签名证书（OV / EV）

无论选哪种，都要同步检查：

- `updater/updateutils/signature_windows.go` 的 Authenticode 校验逻辑
- `.github/workflows/release.yml` 中的签名步骤与相关 Variables
- NSIS 安装器的签名配置

## 4. 更新服务

上游的更新链路包含：S3 兼容对象存储 + Cloudflare Worker（`update-server/`）+ 各 channel 清单。

需要决定：

- **方案 A**：自建 S3 兼容存储 + 自建清单服务，复用现有 `updater/` 与 CI 流程
- **方案 B**：暂不做应用内更新，移除 `update-server/` 与相关 CI，仅通过 Releases 分发

注意：`internal/service/update_service.go` 的默认更新地址列表已置空，
在配置 `YUKIHUB_UPDATE_SERVICE_URL` 之前，更新检查会直接跳过（不会请求任何第三方域名）。

## 5. CI Variables / Secrets

发布相关工作流（`release.yml`、`autobuild.yml`、`update-test.yml`）需要以下配置：

**Variables**

- `UPDATE_PUBLIC_BASE_URL`
- `UPDATE_S3_ENDPOINT`、`UPDATE_S3_REGION`、`UPDATE_S3_BUCKET`
- `YUKIHUB_BANGUMI_CLIENT_ID`、`YUKIHUB_HIKARINAGI_CLIENT_ID`、`YUKIHUB_UMBRA_CLIENT_ID`
- `SIGNPATH_*`（若使用 SignPath）

**Secrets**

- `UPDATE_S3_ACCESS_KEY_ID`、`UPDATE_S3_SECRET_ACCESS_KEY`
- `YUKIHUB_BANGUMI_CLIENT_SECRET`、`YUKIHUB_TOUCHGAL_TOKEN`、`YUKIHUB_UMBRA_REGISTRATION_TOKEN`
- `SIGNPATH_API_TOKEN`（若使用 SignPath）

这些工作流目前仅支持手动触发或 tag 触发，在配置完成前不会产生失败的自动构建。

## 手动触发构建（workflow_dispatch）

四个流水线支持手动运行，**都带平台勾选**；Actions 页面里的工作流名、作业名、
步骤名都是中文，日志和发布方式都会在开头用 notice 说明。

| 工作流 | 用途 | 手动输入（粗体为默认值） |
| --- | --- | --- |
| `release.yml`（发布版本） | 发布一个真实版本 | `version`（可选，留空则用所选标签）、`release_channel`（**仅构建产物**）、`build_windows`、`build_linux`（都开） |
| `autobuild.yml`（开发版快照） | 把当前 main 编成开发版，覆盖滚动预发布 `dev-latest` | `publish`（**仅构建产物**）、`build_windows`、`build_linux`（都开） |
| `linux-package.yml`（Linux 安装包） | 只打 Linux 包并上传制品，不碰 S3 / Release | `version`（留空自动生成） |
| `update-test.yml`（更新链路测试） | 更新链路验证，需自建更新服务后才有意义 | `version`（必填）、`previous_tag` |

### 两个发布流水线有什么区别

|  | 发布版本（`release.yml`） | 开发版快照（`autobuild.yml`） |
| --- | --- | --- |
| 版本号来源 | 你在 `version` 里填的版本号，或（留空时）所选的 **git 标签**（`v1.2.3`）→ `1.2.3` | 自动生成 `<最近标签>-dev.<提交数>+<短SHA>` |
| 产物落到哪 | 该标签自己的 GitHub Release | 固定覆盖滚动预发布 `dev-latest` |
| 会不会进稳定更新通道 | 只有选「正式版」才会 | 永远不会 |
| 什么时候用 | 要正式发一个版本 | 只想编一个能装的包自己测 / 给人试 |

两者的「pre-release」是**同一个 GitHub 机制**（不进 Releases 的 latest、不占正式版名额），
区别在版本号：前者是「真版本先标成预览」，后者压根不是一个版本号。

### 只想产出产物、不想发布

两个发布流水线的**发布方式默认都是「仅构建产物（不发布）」**：不创建 / 覆盖任何
Release，也不写稳定更新通道，产物只落在本次运行的 **Artifacts** 区域。

| 发布方式 | 发布版本（`release.yml`） | 开发版快照（`autobuild.yml`） |
| --- | --- | --- |
| 仅构建产物 | 不创建 Release，不写稳定通道 | 不创建 / 不覆盖 `dev-latest` |
| 预览版 / 开发版 | 该标签标为 GitHub pre-release，不写稳定通道 | 覆盖滚动 `dev-latest` |
| 正式版 | GitHub Release + 稳定通道 `channels/stable/version.json` | —（开发版没有正式版模式） |

打标签（`v*.*.*`）触发的运行没有 input，一律按**正式版**处理 —— 「打标签即发布」
的语义不变。

### 发布一个版本（不需要先建标签）

`release.yml` 的版本号有两种给法，**直接填 `version` 输入最省事**：

1. Actions → **发布版本** → Run workflow。
2. 在 **version** 里填版本号（如 `1.2.3`，可带 `v`）—— 这样从**任意分支**都能发起，
   不必先 `git tag && git push`；Release 建在 `v1.2.3` 上，标签不存在时由 GitHub
   自动创建并指向本次提交。也可以**留空** version，改在右上角 **Use workflow from**
   里选一个已有的 `v*.*.*` 标签（两者都给且不一致会被 `validate` 拦下）。
3. 勾选要构建的平台，**发布方式**选「预览版」或「正式版」。
4. 结果：选**预览版**则 GitHub 上是 **pre-release**，`channels/stable/version.json`
   **不会**被更新，应用内稳定更新通道不受影响；选**正式版**则同时更新稳定通道。

> ⚠️ 版本号必须和仓库里的 `sync/version.json` 一致（它是稳定更新通道的版本信息源）。
> 不一致时发布作业会明确报错，提示你先把 `sync/version.json` 的 `version` 改成新版本
> 并提交，再重新发布。

> 只想拿一个能装的开发版、不想动标签和更新通道，用**开发版快照**：
> 发布方式选「发布开发版（覆盖 dev-latest）」，它产出 `dev-latest` 滚动预发布，
> 天然不碰稳定通道。

### 只构建一个平台

取消勾选另一个平台即可（**至少留一个**，否则 `validate` / `version` 直接报错）。
未勾选的平台整块跳过，产物数量断言（Windows 四件套 / Linux 三件套）也只在
勾选的平台上执行。快速验 Linux 打包链、又不想等 Windows ARM64 交叉编译时很有用。

`release.yml` 的 Windows 与 Linux **仍然不互相阻塞**：`create-release` 只依赖
`build-release`，Linux 链路出问题不会让正式版发不出去（产物按「0 个或 3 个」放行）。

> **批处理文件必须用 CRLF**：`.gitattributes` 的 `* text=auto eol=lf` 会把所有文件规范成
> LF，但 `cmd.exe` 解析 **LF-only 的 `.bat`** 时会把多行 `( )` 块和 `for /f` 拆错
> （满屏「`'xx'` 不是内部或外部命令」），Windows 打包会中途静默失败。因此
> `.gitattributes` 里额外有 `*.bat / *.cmd → text eol=crlf`；两个发布流水线也会在
> 构建前校验行尾，行尾不对直接报错而不是白等一轮构建。

## 6. 品牌素材（待替换）

以下位置仍是上游占位素材，需要替换为 YukiHub 素材：

| 位置 | 说明 |
| --- | --- |
| `build/appicon.png` | 应用图标（源图） |
| `build/windows/icon.ico` | Windows 可执行文件与安装器图标 |
| `build/windows/tray.png` | 系统托盘图标 |
| `frontend/src/assets/branding/brand-1.webp`、`brand-2.webp` | 新增/导入弹窗中的品牌插画 |
| `frontend/src/assets/branding/appicon.png`、`topbar-title.png` | 侧边栏与顶栏品牌图 |
| `screenshot/**` | README 截图与宣传图（当前为上游界面截图） |

## 7. 版本号

- 版本号由构建期注入：默认取自 git tag（`v0.1.0` → `0.1.0`），
  手动发布时也可以在 `release.yml` 的 `version` 输入里直接指定。
- 需要同步维护的地方：
  - `build/config.yml` 的 `info.version`
  - `build/windows/info.json`、`build/windows/nsis/wails_tools.nsh`
  - `sync/version.json`（发布工作流会校验其 `version` 与 tag 一致）
- `scripts/update-build-assets.*` 可在本地批量同步这些文件。

## 8. 本地开发环境

- Go 版本见 `go.mod`（当前 1.27.1）
- Node.js 24 + pnpm 9（前端）
- Wails v3 CLI，版本必须与 `go.mod` 中的 `github.com/wailsapp/wails/v3` 完全一致
  （CI 会用 `go list -m` 自动安装同版本）

常用命令：

```bash
# 安装前端依赖
cd frontend && pnpm install

# 生成 Wails 绑定（修改后端 service 方法签名后必须执行）
wails3 generate bindings -clean=true -ts

# 本地开发运行
wails3 dev -config ./build/config.yml -port 9245

# 检查
gofmt -l . && go vet ./... && go test ./... -count=1

# 构建
wails3 build
```

> 注意：仓库目录名包含空格（`YukiHub for Windows`），部分脚本对含空格路径敏感。
> 如果构建脚本报路径错误，可把仓库检出到无空格路径下（例如 `D:\work\yukihub`）。

## 9. Linux 构建（deb / rpm / AppImage）

CI 的 `autobuild` / `release` 的 Linux 作业会自动完成下列步骤；本地构建需要：

```bash
sudo apt-get install -y build-essential pkg-config desktop-file-utils \
  libayatana-appindicator3-dev libgtk-4-dev libwebkitgtk-6.0-dev
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
# AppImage 另需 appimagetool（https://github.com/AppImage/AppImageKit/releases）
# 凭据通过环境变量注入（与 Windows 同名）：YUKIHUB_UPDATE_SERVICE_URL、
# YUKIHUB_BANGUMI_CLIENT_ID/SECRET、YUKIHUB_HIKARINAGI_CLIENT_ID/SECRET、
# YUKIHUB_TOUCHGAL_TOKEN、YUKIHUB_UMBRA_CLIENT_ID/REGISTRATION_TOKEN

./scripts/build.sh all <version> amd64
```

- 构建脚本会自动调用 `scripts/patch-wails-linux-tray.sh`（Wails beta.24 的
  Linux 托盘 / WebKitGTK 兼容补丁）；直接 `go build` 前也需手动运行一次
- 产物在 `build/bin/`：`YukiHub-<version>-linux-amd64.{deb,rpm,AppImage}`
- 运行库依赖：GTK4、WebKitGTK 6.0、xdg-utils（AppImage 不捆绑这些系统库）
- 仅支持 amd64；代码中保留的上游 arm64 分支不构建、不验证

## 本机构建安装包（WorkBuddy 沙箱环境）

CI 上直接跑 `scripts/build.bat installer <version> amd64` 即可。
但**本机无法直接运行该脚本**，原因有两个环境限制：

1. 脚本会执行 `pnpm --dir frontend install --frozen-lockfile`，而本机的
   WorkBuddy 安全删除 shim（`genie-trash`）在 pnpm 清理 store 时超时：
   `[safe-delete] 操作失败: ... ETIMEDOUT`
2. 安全策略把 `wmic.exe` 列入程序黑名单，并明确**禁止绕过**

依赖已用 `pnpm install --ignore-scripts` 装好，因此可跳过 install 步骤，
手动执行等价流程（以下命令均在仓库根目录）：

```bash
export PATH="<go>/bin:<wails3>/bin:<mingw>/bin:<nsis>/bin:<node>:$PATH"
export GOROOT=... GOPATH=... GOMODCACHE=... GOCACHE=...
export CGO_ENABLED=1 CC=<mingw>/gcc.exe
export GOPROXY=off   # 强制离线：缺东西立即报错，而不是静默下载

VERSION=0.1.0
COMMIT=$(git rev-parse --short HEAD)
BASE="-s -w -X 'yukihub/internal/version.Version=$VERSION' \
      -X 'yukihub/internal/version.GitCommit=$COMMIT' \
      -X 'yukihub/internal/version.BuildTime=<时间>' \
      -X 'yukihub/internal/version.BuildMode=installer'"

# 1. 绑定与前端
wails3 generate bindings -clean=true -ts
pnpm --dir frontend build        # 只跑 typecheck 发现不了失效的资源引用

# 2. GUI（installer 模式需要 -H windowsgui）
wails3 generate syso -arch amd64 -icon build/windows/icon.ico \
  -manifest build/windows/wails.exe.manifest \
  -info build/windows/info.json -out wails_windows_amd64.syso
go build -tags production -trimpath -buildvcs=false \
  -ldflags "$BASE -H windowsgui" -o build/windows/payload/amd64/YukiHub.exe .
rm -f wails_windows_amd64.syso

# 3. 独立更新器
go build -tags production -trimpath -buildvcs=false -ldflags "$BASE" \
go -C updater build -trimpath -buildvcs=false \
  -ldflags "-s -w -H windowsgui" -o "../build/bin/YukiHubUpdater.exe" ./cmd/yukihub-updater

# 4. 运行库
rm -rf build/bin/7z && mkdir -p build/bin/7z
cp lib/winamd64/7z/7z.exe lib/winamd64/7z/7z.dll build/bin/7z/

# 5. WebView2 引导器（内嵌在 wails3 二进制里，不需要联网）
wails3 generate webview2bootstrapper -dir build/windows/webview2bootstrapper
cp build/windows/webview2bootstrapper/MicrosoftEdgeWebview2Setup.exe build/windows/nsis/

# 6. 打包
cd build/windows/nsis
MSYS2_ARG_CONV_EXCL='*' makensis \
  '/DARG_WAILS_AMD64_BINARY=..\payload\amd64\YukiHub.exe' project.nsi
cd ../../..
mv -f build/bin/YukiHub-amd64-installer.exe \
      "build/bin/YukiHub-$VERSION-windows-amd64-setup.exe"
```

三个必须注意的坑：

1. **`/D` 参数会被 git-bash 做路径转换**：`/DARG_...=..\payload\...` 里的反斜杠
   会被转成正斜杠，makensis 收不到正确路径而报 `no files found`。
   用 `MSYS2_ARG_CONV_EXCL='*'` 只对这一条命令排除转换。
   **不要**全局 `export MSYS_NO_PATHCONV=1`——那会把传给 node 的 PATH
   也搞坏，导致 `MODULE_NOT_FOUND`。
2. **`build/bin` 下两样东西都要在**：`YukiHubUpdater.exe` 与 `7z/{7z.exe,7z.dll}`。
   CI 上由 `build.bat` 自动准备，手动构建最容易漏。
3. **`duckdb.dll` 对 amd64 不是必需的**：amd64 走 DuckDB 静态链接，
   `project.nsi` 用 `!if /FileExists` 判断，缺失不会报错。
