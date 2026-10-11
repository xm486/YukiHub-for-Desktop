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
#      拿到预签名地址与必需请求头（x-obs-meta-project-id / x-obs-acl /
#      x-obs-callback / Content-Type），再 PUT 文件本体
#
# ⚠️ 上传慢是这条链路的固有属性，不是脚本 bug
#
# GitHub 托管运行器在境外，GitCode 的对象存储在国内。实测（见 docs/fork-setup.md）：
# 单连接上传吞吐只有十几 KB/s 量级，250 MB 的产物单连接要 4 小时以上。所以：
#   - 默认**多路并发**（PARALLEL，默认 4），把单连接吞吐叠加起来；
#   - 每个文件有单次尝试上限（UPLOAD_MAX_TIME），整体有总预算（UPLOAD_DEADLINE），
#     超了就把剩下的文件记为「未上传」，**不会让作业卡到天荒地老**；
#   - 每个文件都打印**实际发出的字节数与均速**，让"到底是慢还是卡"一眼可见。
#
# ⚠️ 补充：curl 的 --speed-limit 只在**真的停滞**（连不上速度）时才会断开。
# 实测过 —— 本地造一个"服务器不收也不回"的场景，`--speed-limit 4096 --speed-time 10`
# 在 17 秒就报 "Operation too slow"；而同场景只给 --max-time 就只能干等。
# 所以它在 CI 里连 60 秒都没触发，恰恰**证明数据一直在流**（>4 KB/s），是吞吐问题。
# 这条别删，否则「慢」和「死」在日志里长得一模一样。
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
#   GITCODE_INCLUDE     可选，只上传文件名含这些子串的文件（空格分隔），默认全部
#   PARALLEL            可选，并发上传路数，1 = 串行，默认 4
#   UPLOAD_MAX_TIME     可选，单个文件单次尝试的最长秒数，默认 900
#   UPLOAD_DEADLINE     可选，整个上传阶段的总预算秒数，默认 2400
#   UPLOAD_RETRY        可选，单个文件失败后重试次数，默认 1（第二次换协议栈试）
#   API_MAX_TIME        可选，单次 API 调用的最长秒数，默认 120
#   UPLOAD_SPEED_LIMIT  可选，低于这个速率（字节/秒）视为停滞，默认 4096
#   UPLOAD_SPEED_TIME   可选，低速持续多少秒判定为停滞，默认 60
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
GITCODE_INCLUDE="${GITCODE_INCLUDE:-}"
PARALLEL="${PARALLEL:-4}"
UPLOAD_MAX_TIME="${UPLOAD_MAX_TIME:-900}"
UPLOAD_DEADLINE="${UPLOAD_DEADLINE:-2400}"
UPLOAD_RETRY="${UPLOAD_RETRY:-1}"
API_MAX_TIME="${API_MAX_TIME:-120}"
UPLOAD_SPEED_LIMIT="${UPLOAD_SPEED_LIMIT:-4096}"
UPLOAD_SPEED_TIME="${UPLOAD_SPEED_TIME:-60}"

case "$RELEASE_STATUS" in
  latest | pre) ;;
  *) fail "RELEASE_STATUS 只能是 latest 或 pre，收到：$RELEASE_STATUS" ;;
esac

case "$PARALLEL" in
  '' | *[!0-9]*) fail "PARALLEL 必须是正整数，收到：$PARALLEL" ;;
esac
[ "$PARALLEL" -ge 1 ] || fail "PARALLEL 至少为 1"

REPO_API="$GITCODE_API_BASE/repos/$GITCODE_REPO"
RELEASE_PAGE="https://gitcode.com/$GITCODE_REPO/releases"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

now_epoch() { date +%s; }

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
  # GET 是幂等的，可以放心重试；POST / PATCH 不行（重试会重复建 Release），见 api_write
  curl -sS --connect-timeout 20 --max-time "$API_MAX_TIME" \
    --retry 3 --retry-delay 5 --retry-connrefused \
    -o "$out" -w '%{http_code}' -X GET \
    -G --data-urlencode "access_token=$GITCODE_TOKEN" "$@" "$REPO_API$path"
}

api_write() { # method path outfile [额外的 curl 参数...]
  local method="$1" path="$2" out="$3"
  shift 3
  curl -sS --connect-timeout 20 --max-time "$API_MAX_TIME" \
    -o "$out" -w '%{http_code}' -X "$method" \
    "$@" "$REPO_API$path?access_token=$GITCODE_TOKEN"
}

# 人类可读的体积
human_size() { # bytes
  awk -v n="$1" 'BEGIN {
    if (n >= 1073741824) printf "%.2f GB", n / 1073741824
    else if (n >= 1048576) printf "%.1f MB", n / 1048576
    else if (n >= 1024) printf "%.0f KB", n / 1024
    else printf "%d B", n
  }'
}

# 人类可读的速率（字节/秒）
human_speed() { # bytes_per_second
  awk -v n="$1" 'BEGIN {
    if (n >= 1048576) printf "%.1f MB/s", n / 1048576
    else if (n >= 1024) printf "%.0f KB/s", n / 1024
    else printf "%d B/s", n
  }'
}

# 保留两位小数
fmt2() { awk -v n="${1:-0}" 'BEGIN { printf "%.2f", n }'; }

# 出错时把响应正文带进日志。文件可能压根没建出来（curl 超时被 kill 的情况），
# 直接 head 会额外甩一句 "head: cannot open ..."，盖住真正的错误。
resp_body() { # file
  if [ -f "$1" ]; then
    head -c 300 "$1"
  else
    echo "(无响应正文)"
  fi
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

# 列出某个 Release 已有的附件名（一行一个）。重跑同一批产物时用它跳过，
# 已经传上去的就不再传一遍 —— 在这条链路上这能省掉几十分钟。
json_asset_names() { # file
  "$PYTHON_BIN" - "$1" << 'PY'
import json, sys

with open(sys.argv[1], encoding="utf-8") as handle:
    try:
        data = json.load(handle)
    except json.JSONDecodeError:
        sys.exit(0)

assets = data.get("assets") or []
if not isinstance(assets, list):
    sys.exit(0)

out = sys.stdout.buffer
for asset in assets:
    if isinstance(asset, dict) and asset.get("name"):
        name = str(asset["name"]).replace("\r", "").replace("\n", "")
        out.write((name + "\n").encode("utf-8"))
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

existing_assets=""
case "$status" in
  200)
    info "已存在 $TAG 的 Release，改为更新名称与说明（附件走下面的上传）"
    existing_assets="$(json_asset_names "$workdir/existing.json" || true)"
    build_patch_json "$workdir/patch.json"
    patch_status="$(api_write PATCH "/releases/$TAG" "$workdir/patch_resp.json" \
      -H "Content-Type: application/json" \
      --data-binary "@$workdir/patch.json" || true)"
    case "$patch_status" in
      2*) info "Release 说明已更新（HTTP $patch_status）" ;;
      *) fail "更新 Release 失败：HTTP $patch_status $(resp_body "$workdir/patch_resp.json")" ;;
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
      *) fail "创建 Release 失败：HTTP $create_status $(resp_body "$workdir/create.json")" ;;
    esac
    ;;
  *)
    fail "查询 Release 失败：HTTP $status $(resp_body "$workdir/existing.json")"
    ;;
esac

# ── 2. 挑出要上传的文件 ───────────────────────────────────────────────────────
[ -d "$FILES_DIR" ] || fail "找不到产物目录：$FILES_DIR"

# 先按体积**从小到大**排：小文件先落地，最快速度看出「这条路到底通不通」。
# 产物名由构建脚本生成、不含空格，所以这里的按键排序 + cut 取回路径是安全的。
file_list=()
total_bytes=0
skipped=0
skipped_bytes=0
filtered=0
while IFS= read -r path; do
  [ -f "$path" ] || continue
  base="$(basename "$path")"
  size="$(wc -c < "$path" | tr -d '[:space:]')"

  if [ -n "$GITCODE_INCLUDE" ]; then
    keep=0
    for token in $GITCODE_INCLUDE; do
      case "$base" in *"$token"*) keep=1 ;; esac
    done
    if [ "$keep" -eq 0 ]; then
      filtered=$((filtered + 1))
      continue
    fi
  fi

  if [ -n "$existing_assets" ] && printf '%s\n' "$existing_assets" | grep -Fxq "$base"; then
    info "跳过 $base：GitCode 上已经有同名附件"
    skipped=$((skipped + 1))
    skipped_bytes=$((skipped_bytes + size))
    continue
  fi

  file_list+=("$path")
  total_bytes=$((total_bytes + size))
done < <(
  for path in "$FILES_DIR"/*; do
    if [ -f "$path" ]; then
      printf '%s\t%s\n' "$(wc -c < "$path" | tr -d '[:space:]')" "$path"
    fi
  done | sort -n -k1,1 | cut -f2-
)

if [ "${#file_list[@]}" -eq 0 ]; then
  if [ "$skipped" -gt 0 ]; then
    info "没有需要新上传的文件（已跳过 $skipped 个同名附件）→ $RELEASE_PAGE"
    exit 0
  fi
  if [ "$filtered" -gt 0 ]; then
    # 过滤条件什么都没命中：多半是这次构建没出那类产物（例如没勾 Linux），
    # 这不算错误，但也别静默 —— 免得以后改错 GITCODE_INCLUDE 时毫无察觉。
    echo "::warning::[gitcode] GITCODE_INCLUDE=\"$GITCODE_INCLUDE\" 没有匹配到任何产物（$filtered 个文件被排除），本次不镜像"
    exit 0
  fi
  fail "$FILES_DIR 里没有可上传的文件"
fi

FILE_COUNT="${#file_list[@]}"
DEADLINE_AT=$(( $(now_epoch) + UPLOAD_DEADLINE ))
UPLOAD_STARTED_AT="$(now_epoch)"

info "待上传 $FILE_COUNT 个文件，合计 $(human_size "$total_bytes")"
info "并发 $PARALLEL 路，单文件单次上限 ${UPLOAD_MAX_TIME}s，整体预算 ${UPLOAD_DEADLINE}s"
[ "$skipped" -gt 0 ] && info "另有 $skipped 个同名附件已跳过（$(human_size "$skipped_bytes")）"

# 单个文件的上传过程写在自己的日志/结果文件里：并行跑的时候不能让多个 curl
# 往同一个 stdout 里挤，否则日志会交错成一团。
upload_one() { # idx path
  local idx="$1" path="$2"
  local base size log res
  base="$(basename "$path")"
  size="$(wc -c < "$path" | tr -d '[:space:]')"
  log="$workdir/log.$idx"
  res="$workdir/res.$idx"

  echo "[$idx/$FILE_COUNT] $base（$(human_size "$size")）" >> "$log"

  local attempt=1
  local max_attempts=$((UPLOAD_RETRY + 1))
  local last_detail=""

  while [ "$attempt" -le "$max_attempts" ]; do
    local remaining=$((DEADLINE_AT - $(now_epoch)))
    if [ "$remaining" -le 30 ]; then
      echo "[$idx/$FILE_COUNT] 放弃 $base：整体预算已用尽" >> "$log"
      echo "timeout|$base|0|0|0|整体预算已用尽" > "$res"
      return 0
    fi

    local limit="$UPLOAD_MAX_TIME"
    if [ "$limit" -gt "$remaining" ]; then
      limit="$remaining"
    fi

    # 每次都重新取预签名地址：它带有效期，跨长时间重试时旧的很可能已经过期。
    local url_status
    url_status="$(api_get "/releases/$TAG/upload_url" "$workdir/upload.$idx.json" \
      --data-urlencode "file_name=$base" || true)"
    case "$url_status" in
      2*) ;;
      *)
        last_detail="取上传地址失败：HTTP $url_status $(resp_body "$workdir/upload.$idx.json")"
        echo "[$idx/$FILE_COUNT] 第 $attempt 次：$last_detail" >> "$log"
        attempt=$((attempt + 1))
        sleep 5
        continue
        ;;
    esac

    local upload_url host
    upload_url="$(json_field "$workdir/upload.$idx.json" url)"
    upload_url="${upload_url%$'\r'}"
    if [ -z "$upload_url" ]; then
      last_detail="上传地址为空：$(resp_body "$workdir/upload.$idx.json")"
      echo "[$idx/$FILE_COUNT] 第 $attempt 次：$last_detail" >> "$log"
      attempt=$((attempt + 1))
      sleep 5
      continue
    fi

    # 只打印主机名：预签名串里带着凭证，不能进日志。
    host="$(printf '%s' "$upload_url" | sed -E 's#^[a-zA-Z]+://([^/]+).*#\1#' | cut -c1-60)"
    if [ "$attempt" -eq 1 ]; then
      echo "[$idx/$FILE_COUNT] 目标主机 $host" >> "$log"
    fi

    local header_args=()
    while IFS=$'\t' read -r key value; do
      value="${value%$'\r'}"
      [ -n "$key" ] && header_args+=(-H "$key: $value")
    done < <(json_headers "$workdir/upload.$idx.json")

    # 首次用 HTTP/1.1（排除 h2 流控把大文件拖死的可能），重试换回 curl 默认协议。
    local proto_args=(--http1.1)
    if [ "$attempt" -gt 1 ]; then
      proto_args=()
    fi

    local put_out put_status put_rest put_time put_size put_speed
    # 预签名地址不能再加任何 query（会破坏签名），也不要带令牌。
    #
    # --speed-limit/--speed-time：真**停滞**时一分钟内报错；注意它只在完全不动时
    # 才触发，慢速持续传输是不会被它打断的（那要靠 --max-time / 总预算来兜）。
    put_out="$(curl -sS "${proto_args[@]}" --connect-timeout 30 --max-time "$limit" \
      --speed-limit "$UPLOAD_SPEED_LIMIT" --speed-time "$UPLOAD_SPEED_TIME" -H "Expect:" \
      -o "$workdir/put_resp.$idx.bin" \
      -w '%{http_code} %{time_total} %{size_upload} %{speed_upload}' \
      -X PUT "${header_args[@]}" --upload-file "$path" "$upload_url" || true)"

    put_status="${put_out%% *}"
    put_rest="${put_out#* }"
    put_time="${put_rest%% *}"
    put_rest="${put_rest#* }"
    put_size="${put_rest%% *}"
    put_speed="${put_rest#* }"

    case "$put_status" in
      2*)
        echo "[$idx/$FILE_COUNT] 已上传 $base：耗时 $(fmt2 "$put_time")s，均速 $(human_speed "${put_speed:-0}")" >> "$log"
        echo "ok|$base|$put_time|$put_size|$put_speed|" > "$res"
        return 0
        ;;
      *)
        last_detail="HTTP ${put_status:-无响应}，已发出 $(human_size "${put_size:-0}") / $(human_size "$size")（均速 $(human_speed "${put_speed:-0}")），耗时 $(fmt2 "$put_time")s $(resp_body "$workdir/put_resp.$idx.bin")"
        echo "[$idx/$FILE_COUNT] 第 $attempt 次失败：$last_detail" >> "$log"
        attempt=$((attempt + 1))
        sleep 5
        ;;
    esac
  done

  echo "[$idx/$FILE_COUNT] ✗ 放弃 $base：$last_detail" >> "$log"
  echo "fail|$base|0|0|0|$last_detail" > "$res"
  return 0
}

# 一批一批地跑：批内并行、批与批之间等齐。比维护信号量简单，也不会让某个
# 掉队的文件拖坏后面的调度 —— 每批各自受总预算约束。
flush_batch() { # 数组元素（文件名）
  local -a batch=("$@")
  local path idx
  for path in "${batch[@]}"; do
    idx="$BATCH_NEXT_INDEX"
    BATCH_NEXT_INDEX=$((BATCH_NEXT_INDEX + 1))
    upload_one "$idx" "$path" &
  done
  wait || true
}

BATCH_NEXT_INDEX=1
batch=()
for path in "${file_list[@]}"; do
  batch+=("$path")
  if [ "${#batch[@]}" -ge "$PARALLEL" ]; then
    flush_batch "${batch[@]}"
    batch=()
  fi
done
if [ "${#batch[@]}" -gt 0 ]; then
  flush_batch "${batch[@]}"
fi

# ── 3. 汇总 ───────────────────────────────────────────────────────────────────
uploaded=0
failed=0
timed_out=0
moved_bytes=0
index=0
for path in "${file_list[@]}"; do
  index=$((index + 1))
  [ -f "$workdir/log.$index" ] && cat "$workdir/log.$index"
  if [ -f "$workdir/res.$index" ]; then
    IFS='|' read -r kind name seconds size speed detail < "$workdir/res.$index"
    case "$kind" in
      ok)
        uploaded=$((uploaded + 1))
        moved_bytes=$((moved_bytes + ${size:-0}))
        ;;
      fail)
        failed=$((failed + 1))
        echo "::error::[gitcode] 上传 $name 失败：$detail"
        ;;
      timeout)
        timed_out=$((timed_out + 1))
        echo "::warning::[gitcode] 未上传 $name：$detail"
        ;;
    esac
  fi
done

elapsed=$(( $(now_epoch) - UPLOAD_STARTED_AT ))
if [ "$elapsed" -gt 0 ]; then
  info "上传阶段耗时 ${elapsed}s，实际发出 $(human_size "$moved_bytes")，平均 $(human_speed $((moved_bytes / elapsed)))"
fi

if [ "$failed" -gt 0 ]; then
  fail "完成：成功 $uploaded，失败 $failed，超预算未传 $timed_out（跳过 $skipped）→ $RELEASE_PAGE"
fi

info "完成：成功 $uploaded，超预算未传 $timed_out（跳过 $skipped）→ $RELEASE_PAGE"
