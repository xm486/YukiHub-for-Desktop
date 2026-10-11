#!/usr/bin/env bash
#
# 把本次构建的产物同步发布到 GitCode。
#
# 背景：本仓库的 CI 跑在 GitHub Actions 上（GitCode 的托管运行器只有 Linux，
# 没有 Windows，编不出 portable / installer），所以 GitCode 那边不做构建，
# 只作为「发布落点」——让从 GitCode 进来的用户也能下到包。
#
# GitCode 的 Release API 与 GitHub 不同，没有 softprops/action-gh-release 这种
# 现成 action 可用，只能自己调 OpenAPI v5：
#   1. GET  /repos/{owner}/{repo}/releases/tags/{tag}      判断是否已存在
#   2. POST /repos/{owner}/{repo}/releases                  不存在则创建
#      或 PATCH /repos/{owner}/{repo}/releases/{tag}        已存在则改名称与说明
#   3. GET  /repos/{owner}/{repo}/releases/{tag}/upload_url?file_name=xxx
#      拿到 OBS 预签名地址与必需请求头，再 PUT 文件本体
#
# 注意：GitCode 没有「删除 Release / 删除附件」的接口，所以滚动预发布
# （dev-latest）反复构建会让附件越积越多，需要偶尔去网页端删掉那个 Release
# 重来一次。见 docs/fork-setup.md。
#
# 用法（全部通过环境变量）：
#   GITCODE_TOKEN       必填，GitCode 个人访问令牌（仓库写权限）
#   TAG                 必填，Release 标签，如 v0.2.5 / dev-latest
#   NAME                可选，Release 标题，默认取 TAG
#   BODY_FILE           可选，发布说明 Markdown 文件
#   RELEASE_STATUS      可选，latest（正式）| pre（预发布），默认 latest
#   TARGET_COMMITISH    可选，仅当标签还不存在时用它建标签
#   FILES_DIR           可选，待上传目录，默认 release
#   GITCODE_REPO        可选，默认 xm486/YukiHub-for-Desktop
#   GITCODE_API_BASE    可选，默认 https://api.gitcode.com/api/v5
#   PYTHON_BIN          可选，默认 python3
#
# 未配置 GITCODE_TOKEN 时**静默跳过并返回 0** —— 令牌是可选的，没配就不该
# 让发布流程失败。

set -euo pipefail

info() { echo "[gitcode] $*"; }
fail() { echo "::error::[gitcode] $*" >&2; exit 1; }

if [ -z "${GITCODE_TOKEN:-}" ]; then
  info "未配置 GITCODE_TOKEN，跳过同步到 GitCode"
  exit 0
fi

: "${TAG:?需要 TAG（Release 标签）}"

PYTHON_BIN="${PYTHON_BIN:-python3}"
command -v "$PYTHON_BIN" > /dev/null 2>&1 || fail "找不到 $PYTHON_BIN（解析 GitCode 返回值需要它）"

GITCODE_REPO="${GITCODE_REPO:-xm486/YukiHub-for-Desktop}"
GITCODE_API_BASE="${GITCODE_API_BASE:-https://api.gitcode.com/api/v5}"
NAME="${NAME:-$TAG}"
BODY_FILE="${BODY_FILE:-}"
RELEASE_STATUS="${RELEASE_STATUS:-latest}"
TARGET_COMMITISH="${TARGET_COMMITISH:-}"
FILES_DIR="${FILES_DIR:-release}"

case "$RELEASE_STATUS" in
  latest | pre) ;;
  *) fail "RELEASE_STATUS 只能是 latest 或 pre，收到：$RELEASE_STATUS" ;;
esac

REPO_API="$GITCODE_API_BASE/repos/$GITCODE_REPO"
RELEASE_PAGE="https://gitcode.com/$GITCODE_REPO/releases"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

# 发请求并回显 HTTP 状态码，正文写到 $3。
#
# 令牌只能放 query（GitCode 不认 Authorization 头），于是分成两个入口：
#   api_get   用 -G + --data-urlencode 拼 query（curl 的 -G 只对 -d 家族生效，
#             **一旦同时给了 -F / --data-binary，-G 会把整个请求退化成 GET**，
#             带着写请求的正文发出去 —— 所以写请求必须走 api_write）；
#   api_write 把令牌直接拼在 URL 上。两种情况都**不要打印完整 URL**。
api_get() { # path outfile [额外的 curl 参数...]
  local path="$1" out="$2"
  shift 2
  curl -sS --max-time 300 -o "$out" -w '%{http_code}' -X GET \
    -G --data-urlencode "access_token=$GITCODE_TOKEN" "$@" "$REPO_API$path"
}

api_write() { # method path outfile [额外的 curl 参数...]
  local method="$1" path="$2" out="$3"
  shift 3
  curl -sS --max-time 300 -o "$out" -w '%{http_code}' -X "$method" \
    "$@" "$REPO_API$path?access_token=$GITCODE_TOKEN"
}

# 取 JSON 里的单个字段（字符串）
#
# 一律用 sys.stdout.buffer 写字节：python 在 Windows 上会把 print 的 \n 翻成 \r\n，
# 而 bash 的 $(...) 只吃掉结尾的 \n、**不吃 \r**。多出来的 \r 会被拼进 curl 参数，
# 而 curl 把 -H 值里的 CR 当作「一条头结束、后面还有一条」的分隔符 ——
# 结果就是请求头被提前截断、请求体发不出去。踩过一次。
json_field() { # file key
  "$PYTHON_BIN" - "$1" "$2" << 'PY'
import json, sys

with open(sys.argv[1], encoding="utf-8") as handle:
    try:
        data = json.load(handle)
    except json.JSONDecodeError:
        data = {}

value = data
for part in sys.argv[2].split("."):
    if not isinstance(value, dict):
        value = None
        break
    value = value.get(part)

text = "" if value is None else str(value)
sys.stdout.buffer.write((text.replace("\r", "") + "\n").encode("utf-8"))
PY
}

# 把 upload_url 返回的 headers 展开成一行一个 "键\t值"，供 bash 转成 -H 参数
json_headers() { # file
  "$PYTHON_BIN" - "$1" << 'PY'
import json, sys

with open(sys.argv[1], encoding="utf-8") as handle:
    try:
        data = json.load(handle)
    except json.JSONDecodeError:
        sys.exit(0)

headers = data.get("headers") or {}
if not isinstance(headers, dict):
    sys.exit(0)

out = sys.stdout.buffer
for key, value in headers.items():
    # 头里的 CR/LF 会撑破「一行一个头」的约定（curl 还会把 CR 当多头的分隔符），
    # 这种头直接扔掉 —— OBS 的预签名头用不到。
    text = "" if value is None else str(value).replace("\r", "").replace("\n", "")
    out.write(f"{key}\t{text}\n".encode("utf-8"))
PY
}

# 生成 PATCH 的 JSON 正文。发布说明是多行 Markdown，用手拼 JSON 极易出错，
# 交给 python 做转义。
build_patch_json() { # outfile
  "$PYTHON_BIN" - "$1" "$NAME" "$BODY_FILE" "$RELEASE_STATUS" << 'PY'
import json, sys

out_path, name, body_file, status = sys.argv[1:5]

body = ""
if body_file:
    try:
        with open(body_file, encoding="utf-8") as handle:
            body = handle.read()
    except FileNotFoundError:
        body = ""

payload = {"name": name, "body": body, "release_status": status}
with open(out_path, "w", encoding="utf-8", newline="\n") as handle:
    json.dump(payload, handle, ensure_ascii=False)
PY
}

info "目标仓库：$GITCODE_REPO，标签：$TAG，状态：$RELEASE_STATUS"

# ── 1. 判断 Release 是否已存在 ────────────────────────────────────────────────
status="$(api_get "/releases/tags/$TAG" "$workdir/existing.json" || true)"

case "$status" in
  200)
    info "已存在 $TAG 的 Release，改为更新名称与说明（附件走下面的上传）"
    build_patch_json "$workdir/patch.json"
    patch_status="$(api_write PATCH "/releases/$TAG" "$workdir/patch_resp.json" \
      -H "Content-Type: application/json" \
      --data-binary "@$workdir/patch.json" || true)"
    case "$patch_status" in
      2*) info "Release 说明已更新（HTTP $patch_status）" ;;
      *) fail "更新 Release 失败：HTTP $patch_status $(head -c 300 "$workdir/patch_resp.json")" ;;
    esac
    ;;
  404)
    info "GitCode 上还没有 $TAG 的 Release，创建它"
    form_args=(-F "tag_name=$TAG" -F "name=$NAME" -F "release_status=$RELEASE_STATUS")
    if [ -n "$BODY_FILE" ] && [ -f "$BODY_FILE" ]; then
      # -F "字段=<文件" 表示「用文件内容当字段值」，绕开多行文本的转义问题
      form_args+=(-F "body=<$BODY_FILE")
    fi
    if [ -n "$TARGET_COMMITISH" ]; then
      form_args+=(-F "target_commitish=$TARGET_COMMITISH")
    fi

    create_status="$(api_write POST "/releases" "$workdir/create.json" "${form_args[@]}" || true)"
    case "$create_status" in
      2*) info "Release 已创建（HTTP $create_status）" ;;
      *) fail "创建 Release 失败：HTTP $create_status $(head -c 300 "$workdir/create.json")" ;;
    esac
    ;;
  *)
    fail "查询 Release 失败：HTTP $status $(head -c 300 "$workdir/existing.json")"
    ;;
esac

# ── 2. 上传产物 ───────────────────────────────────────────────────────────────
[ -d "$FILES_DIR" ] || fail "找不到产物目录：$FILES_DIR"

shopt -s nullglob
files=("$FILES_DIR"/*)
shopt -u nullglob

uploaded=0
for path in "${files[@]}"; do
  [ -f "$path" ] || continue
  base="$(basename "$path")"

  upload_status="$(api_get "/releases/$TAG/upload_url" "$workdir/upload.json" \
    --data-urlencode "file_name=$base" || true)"
  case "$upload_status" in
    2*) ;;
    *) fail "获取 $base 的上传地址失败：HTTP $upload_status $(head -c 300 "$workdir/upload.json")" ;;
  esac

  upload_url="$(json_field "$workdir/upload.json" url)"
  upload_url="${upload_url%$'\r'}"
  [ -n "$upload_url" ] || fail "上传地址为空：$(head -c 300 "$workdir/upload.json")"

  header_args=()
  while IFS=$'\t' read -r key value; do
    value="${value%$'\r'}"
    [ -n "$key" ] && header_args+=(-H "$key: $value")
  done < <(json_headers "$workdir/upload.json")

  # 预签名地址不能再加任何 query（会破坏签名），也不要带令牌
  put_status="$(curl -sS --max-time 300 -o "$workdir/put_resp.bin" -w '%{http_code}' \
    -X PUT "${header_args[@]}" --upload-file "$path" "$upload_url" || true)"
  case "$put_status" in
    2*) info "已上传 $base（$(du -h "$path" | cut -f1)）"; uploaded=$((uploaded + 1)) ;;
    *) fail "上传 $base 失败：HTTP $put_status $(head -c 300 "$workdir/put_resp.bin")" ;;
  esac
done

[ "$uploaded" -gt 0 ] || fail "$FILES_DIR 里没有可上传的文件"

info "完成：共上传 $uploaded 个文件 → $RELEASE_PAGE"
