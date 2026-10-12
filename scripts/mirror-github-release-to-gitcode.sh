#!/usr/bin/env bash
#
# 在国内机器上把构建产物镜像到 GitCode 的 Release。
#
# 为什么需要它：GitHub Actions 的运行器在境外，GitCode 的文件服务在国内，
# 从 CI 直传是跨境上传，实测单连接只有 16-43 KB/s、6 路并发合计 ~160 KB/s
# （250 MB 要 26 分钟以上，还经常中途停顿）。而国内机器到 GitCode 下载能到
# 8.2 MB/s。所以正确做法是**把这一跳搬回国内**。
#
# 两种用法：
#   A. 推本地已有的产物（最快）：手头只要有那些安装包/压缩包就行，完全不碰 GitHub
#   B. 从 GitHub 拉：脚本从 GitHub Release 下载后再推（跨境下行 + 境内上行）
#
# 推送那一步直接复用 publish-gitcode-release.sh（建 Release / 上传附件 /
# 跳过已有同名附件都在里面），这里只负责「把产物凑齐」。
#
# 用法（在 Git Bash / WSL / Linux 里跑，全部通过环境变量）：
#   GITCODE_TOKEN       必填，GitCode 个人访问令牌（仓库写权限）
#   GH_TAG              可选，要镜像的 GitHub Release 标签，如 dev-latest / v0.2.5。
#                        留空则走「本地已有产物」模式（见下）
#   FILES_DIR           可选。给了 GH_TAG 时它是下载落地目录；不给 GH_TAG 时
#                        它就是要推送的目录
#   GITHUB_REPO         可选，默认 xm486/YukiHub-for-Desktop
#   GITCODE_TAG         可选，GitCode 侧的标签，默认与 GH_TAG 相同
#   GITHUB_TOKEN        可选，私有仓库或想避开 60 次/小时 的匿名限额时才需要
#   RELEASE_STATUS      可选，latest | pre，默认取 GitHub 那边是不是预发布
#   MIRROR_INCLUDE      可选，只推送文件名含这些子串的产物（空格分隔），默认全部
#   MIRROR_MAX_FILES    可选，最多推送几个文件（调试用），默认不限制
#   WORKDIR             可选，下载目录的父目录，默认系统临时目录
#
# 例一：本地已有产物，直接推上去（完全不碰 GitHub）
#   GITCODE_TOKEN=xxx FILES_DIR=~/Downloads GITCODE_TAG=dev-latest \
#     bash scripts/mirror-github-release-to-gitcode.sh
#
# 例二：从 GitHub 拉 dev-latest，只镜像 Windows amd64 那两个包
#   GITCODE_TOKEN=xxx GH_TAG=dev-latest MIRROR_INCLUDE=windows-amd64 \
#     bash scripts/mirror-github-release-to-gitcode.sh

set -euo pipefail

info() { echo "[mirror] $*"; }
fail() { echo "::error::[mirror] $*" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PUBLISH_SCRIPT="$SCRIPT_DIR/publish-gitcode-release.sh"
[ -f "$PUBLISH_SCRIPT" ] || fail "找不到 $PUBLISH_SCRIPT"

# ── 判定模式 ────────────────────────────────────────────────────────────────
if [ -n "${GH_TAG:-}" ]; then
  LOCAL_MODE=0
elif [ -n "${FILES_DIR:-}" ] && [ -d "$FILES_DIR" ] \
  && [ -n "$(ls -A "$FILES_DIR" 2>/dev/null)" ]; then
  LOCAL_MODE=1
  info "未提供 GH_TAG，但 FILES_DIR 里有文件 → 走「本地已有产物」模式，不从 GitHub 拉"
else
  fail "需要 GH_TAG（要镜像的 GitHub Release 标签），或提供已有产物的 FILES_DIR"
fi

[ -n "${GITCODE_TOKEN:-}" ] || fail "需要 GITCODE_TOKEN（GitCode 个人访问令牌）"

# ── 找个能解析 JSON 的 python ────────────────────────────────────────────────
# Windows 上常常只有 `python` 没有 `python3`，所以逐个试。
PYTHON_BIN="${PYTHON_BIN:-}"
if [ -z "$PYTHON_BIN" ]; then
  for candidate in python3 python py; do
    if command -v "$candidate" > /dev/null 2>&1; then
      if "$candidate" -c 'import json,sys' > /dev/null 2>&1; then
        PYTHON_BIN="$candidate"
        break
      fi
    fi
  done
fi
[ -n "$PYTHON_BIN" ] || fail "找不到可用的 python（python3 / python / py 都不行）"
info "使用 $PYTHON_BIN 解析 JSON"

GITHUB_REPO="${GITHUB_REPO:-xm486/YukiHub-for-Desktop}"
GITCODE_TAG="${GITCODE_TAG:-${GH_TAG:-本地推送}}"
MIRROR_INCLUDE="${MIRROR_INCLUDE:-}"
MIRROR_MAX_FILES="${MIRROR_MAX_FILES:-}"
WORKDIR="${WORKDIR:-$(mktemp -d)}"
RELEASE_STATUS="${RELEASE_STATUS:-}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# 产物目录与 GitCode 侧该标的状态
dest=""
status="pre"

if [ "$LOCAL_MODE" = "1" ]; then
  dest="$FILES_DIR"
  info "本地模式：推送 $dest 下的产物，不读 GitHub"

  # 本地模式的说明写到临时目录，不往用户自己的 FILES_DIR 里塞文件
  printf '> 本 Release 的产物为本地推送，未从 GitHub 拉取。\n' > "$work/notes.md"
else
  # ── 1. 读 GitHub 那个 Release 的元信息 ─────────────────────────────────────
  auth_args=()
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    auth_args=(-H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/vnd.github+json")
  fi

  info "读取 GitHub Release：$GITHUB_REPO @ $GH_TAG"
  gh_status="$(curl -sS --connect-timeout 20 --max-time 120 --retry 3 --retry-delay 3 \
    -o "$work/release.json" -w '%{http_code}' "${auth_args[@]}" \
    "https://api.github.com/repos/$GITHUB_REPO/releases/tags/$GH_TAG" || true)"

  case "$gh_status" in
    200) ;;
    404) fail "GitHub 上找不到 $GH_TAG 的 Release（仓库：$GITHUB_REPO）" ;;
    *) fail "读取 GitHub Release 失败：HTTP $gh_status" ;;
  esac

  # 把要搬的产物列出来（名字 + 下载地址 + 体积），按体积从小到大排；
  # 同时算出 GitCode 侧该用的 RELEASE_STATUS。都在 python 里做，别在 bash 里解析 JSON。
  "$PYTHON_BIN" - "$work/release.json" "$MIRROR_INCLUDE" "$MIRROR_MAX_FILES" \
    "$work/files.tsv" "$work/meta.env" << 'PY'
import json
import sys

release_path, include, max_files, files_path, meta_path = sys.argv[1:6]

with open(release_path, encoding="utf-8") as handle:
    release = json.load(handle)

assets = release.get("assets") or []
rows = []
for asset in assets:
    name = asset.get("name") or ""
    url = asset.get("browser_download_url") or ""
    size = int(asset.get("size") or 0)
    if not name or not url:
        continue
    if include and not any(token in name for token in include.split()):
        continue
    rows.append((size, name, url))

rows.sort(key=lambda row: row[0])
if max_files:
    rows = rows[: int(max_files)]

with open(files_path, "w", encoding="utf-8", newline="\n") as handle:
    for size, name, url in rows:
        handle.write(f"{size}\t{name}\t{url}\n")

prerelease = "1" if release.get("prerelease") else "0"
with open(meta_path, "w", encoding="utf-8", newline="\n") as handle:
    handle.write(f"prerelease={prerelease}\n")
    handle.write("status=pre\n" if prerelease == "1" else "status=latest\n")
PY

  # shellcheck disable=SC1091
  source "$work/meta.env"

  file_count="$(wc -l < "$work/files.tsv" | tr -d '[:space:]')"
  [ "$file_count" -gt 0 ] || fail "没有符合条件的产物（MIRROR_INCLUDE=\"$MIRROR_INCLUDE\"）"
  info "GitHub 侧 $GH_TAG：prerelease=$prerelease → GitCode 标 $status，待搬 $file_count 个文件"

  # ── 2. 下载 ────────────────────────────────────────────────────────────────
  # 用带时间戳的唯一目录，不做 rm -rf：既避免误删用户自己指定的 WORKDIR，
  # 也避开某些环境对 rm 的拦截。
  dest="$WORKDIR/mirror-$GH_TAG-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$dest"

  total=0
  while IFS=$'\t' read -r size name url; do
    [ -n "${name:-}" ] || continue
    out_path="$dest/$name"
    info "下载 $name（$size 字节）…"
    dl_status="$(curl -sS -L --connect-timeout 20 --max-time 3600 --retry 3 --retry-delay 5 \
      -o "$out_path" -w '%{http_code}' "${auth_args[@]}" "$url" || true)"
    case "$dl_status" in
      2*) ;;
      *) fail "下载 $name 失败：HTTP $dl_status" ;;
    esac
    actual="$(wc -c < "$out_path" | tr -d '[:space:]')"
    [ "$actual" = "$size" ] || fail "$name 大小不符：期望 $size，实际 $actual"
    total=$((total + actual))
  done < "$work/files.tsv"

  info "下载完成：$file_count 个文件，共 $total 字节 → $dest"

  # ── 3. 发布说明 ────────────────────────────────────────────────────────────
  # GitHub 的 body 里可能有相对链接（如 ./compare/...），搬到 GitCode 后指向不对，
  # 所以加一行说明，保留原文供人参考。
  "$PYTHON_BIN" - "$work/release.json" "$work/notes.md" "$GH_TAG" "$GITHUB_REPO" << 'PY'
import json
import sys

release_path, out_path, tag, repo = sys.argv[1:5]
with open(release_path, encoding="utf-8") as handle:
    release = json.load(handle)

body = (release.get("body") or "").strip()
header = (
    f"> 本 Release 的产物由 GitHub 镜像而来："
    f"https://github.com/{repo}/releases/tag/{tag}\n>\n"
    "> 原文中的相对链接在 GitCode 上可能无法点击，请以上面的地址为准。\n\n"
)
with open(out_path, "w", encoding="utf-8", newline="\n") as handle:
    handle.write(header + body)
PY
fi

[ -d "$dest" ] || fail "产物目录不存在：$dest"

# ── 4. 推给 GitCode（复用既有脚本：建/更新 Release + 并发上传 + 跳过同名）──────
RELEASE_STATUS="${RELEASE_STATUS:-$status}"
info "推送到 GitCode：$GITCODE_TAG（$RELEASE_STATUS），产物目录 $dest"

# 注意：本脚本开了 set -u，凡是「可能没被用户设置」的变量一律用 ${VAR:-} 兜底，
# 否则整个脚本会以 "unbound variable" 退出 —— 第一版就在这里翻过车。
GITCODE_TOKEN="$GITCODE_TOKEN" \
GITCODE_REPO="${GITCODE_REPO:-$GITHUB_REPO}" \
GITCODE_API_BASE="${GITCODE_API_BASE:-}" \
TAG="$GITCODE_TAG" \
NAME="${NAME:-$GITCODE_TAG}" \
BODY_FILE="$work/notes.md" \
RELEASE_STATUS="$RELEASE_STATUS" \
FILES_DIR="$dest" \
GITCODE_INCLUDE="$MIRROR_INCLUDE" \
PARALLEL="${PARALLEL:-4}" \
UPLOAD_MAX_TIME="${UPLOAD_MAX_TIME:-2700}" \
UPLOAD_DEADLINE="${UPLOAD_DEADLINE:-5400}" \
PYTHON_BIN="$PYTHON_BIN" \
  bash "$PUBLISH_SCRIPT"

info "镜像完成：https://gitcode.com/${GITCODE_REPO:-$GITHUB_REPO}/releases"
