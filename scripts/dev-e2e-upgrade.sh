#!/usr/bin/env bash
# xray 版本升级管理端到端验收（设计文档 §18）：
#   本地 release 镜像（zip + .dgst）→ 非法版本 failed 且二进制原样 →
#   指定版本升级 acked、二进制替换、面板版本号经 telemetry 刷新。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）。无需外网。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18107"
API="127.0.0.1:14207"
REL_ADDR="127.0.0.1:18200"
XRAY_CONFIG="$WORK/xray-config.json"
TEST_XRAY="$WORK/xray" # agent 将替换该二进制，不碰系统原件
JAR="$WORK/cookies.txt"
CSRF=""
VER_NUM="$(/usr/bin/env "$XRAY_BIN" version 2>/dev/null | head -1 | awk '{print $2}')"
[[ -n "$VER_NUM" ]] || VER_NUM="26.3.27"
REL_VER="v$VER_NUM"

cleanup() {
    kill ${BPID:-} ${APID:-} ${RELPID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    pkill -f "http.server 18200" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build & 搭建本地 release 镜像（$REL_VER）"
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)
cp "$XRAY_BIN" "$TEST_XRAY"
ORIG_SHA="$(sha256sum "$TEST_XRAY" | cut -d' ' -f1)"
REL_DIR="$WORK/releases/$REL_VER"
mkdir -p "$REL_DIR"
python3 - "$TEST_XRAY" "$REL_DIR/Xray-linux-64.zip" <<'PY'
import sys, zipfile
with zipfile.ZipFile(sys.argv[2], "w") as z:
    z.write(sys.argv[1], "xray")
PY
python3 - "$REL_DIR/Xray-linux-64.zip" "$REL_DIR/Xray-linux-64.zip.dgst" <<'PY'
import hashlib, sys
h = hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest()
open(sys.argv[2], "w").write(f"SHA2-256= {h}\n")
PY
(cd "$WORK/releases" && exec python3 -m http.server 18200) >/dev/null 2>&1 &
RELPID=$!
# 镜像同样提供 GitHub release 的 latest 重定向布局：{base}/latest/download/{asset}
mkdir -p "$WORK/releases/latest/download"
cp "$REL_DIR/Xray-linux-64.zip" "$REL_DIR/Xray-linux-64.zip.dgst" "$WORK/releases/latest/download/"

db() { python3 - "$WORK/lattix.db" "$1" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
cur = con.execute(sys.argv[2])
con.commit()
for row in cur: print("|".join("" if c is None else str(c) for c in row))
PY
}

# rpc_raw <method> <path> [body]
rpc_raw() {
    local method="$1" path="$2" body="${3:-}"
    local args=(-sS -b "$JAR" -c "$JAR" -X "$method" -H "Origin: http://$ADDR")
    if [[ "$method" == "POST" ]]; then
        [[ -n "$body" ]] || body='{}'
        args+=(-H 'Content-Type: application/json' -H "Idempotency-Key: $(openssl rand -hex 16)" -d "$body")
        [[ -z "$CSRF" ]] || args+=(-H "X-CSRF-Token: $CSRF")
    fi
    curl "${args[@]}" "http://$ADDR$path"
}

# rpc_data：解包 RPC 信封输出 data。
rpc_data() {
    rpc_raw "$@" | python3 -c '
import json,sys
value=json.load(sys.stdin)
if value["code"] not in ("OK","ACCEPTED"):
    raise SystemExit(f"RPC failed: {value}")
print(json.dumps(value["data"], separators=(",",":")))
'
}

wait_upgrade_cmd() {
    for _ in $(seq 1 60); do
        s="$(db "SELECT status FROM commands WHERE type='xray.upgrade' ORDER BY id DESC LIMIT 1")"
        [[ "$s" == "$1" ]] && return 0
        [[ "$s" == "acked" || "$s" == "failed" ]] && [[ "$s" != "$1" ]] && { echo "FAIL: 升级命令意外终态 $s（期望 $1）"; return 1; }
        sleep 1
    done
    echo "FAIL: 升级命令超时未达 $1"; return 1
}

echo ">> start backend & agent（release 基址指向本地镜像）"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

# 通过 RPC 建服务器（取代 DB INSERT）
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
SRV="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"up01","address":"127.0.0.1"}')"
BOOTSTRAP="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["bootstrap_token"])' "$SRV")"

"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -settings "$WORK/agent.settings.json" \
    -xray-bin "$TEST_XRAY" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    -xray-release-base "http://$REL_ADDR" >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 1.5

# 推送设置：遥测间隔 10s（最小允许值），确保版本号能及时刷新
rpc_data POST /api/setting/update '{"agent":{"revision":1,"reconnect":{"mode":"infinite","max_retries":10},"telemetry":{"interval_seconds":10},"drift_detection":{"interval_seconds":60}}}' >/dev/null

echo ">> 建节点（使 xray 进入运行态）"
NID="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
for _ in $(seq 1 40); do
    [[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] || { echo "FAIL: 节点未 active"; exit 1; }

echo ">> 非法版本 → failed + 二进制原样"
rpc_data POST /api/server/upgrade-xray '{"server_id":1,"version":"v0.0.0-notexist"}' >/dev/null 2>&1 || true
wait_upgrade_cmd failed && echo "OK: 非法版本命令 failed" || { grep "command.*failed" "$WORK/backend.log" | tail -2; exit 1; }
[[ "$(sha256sum "$TEST_XRAY" | cut -d' ' -f1)" == "$ORIG_SHA" ]] \
    && echo "OK: 二进制未被破坏" \
    || { echo "FAIL: 二进制被意外改动"; exit 1; }

echo ">> 指定版本升级 → acked + 重启生效"
rpc_data POST /api/server/upgrade-xray "{\"server_id\":1,\"version\":\"$REL_VER\"}" >/dev/null
wait_upgrade_cmd acked && echo "OK: 升级命令 acked" || { grep "command.*failed" "$WORK/backend.log" | tail -2; tail -10 "$WORK/agent.log"; exit 1; }
"$TEST_XRAY" version | head -1 | grep -q "$VER_NUM" \
    && echo "OK: 新二进制可运行（$VER_NUM）" \
    || { echo "FAIL: 替换后二进制异常"; exit 1; }
[[ ! -f "$TEST_XRAY.bak" ]] && echo "OK: 升级成功后备份已清理" || { echo "FAIL: 备份残留"; exit 1; }

echo ">> latest 升级走镜像（不经 GitHub API）"
rpc_data POST /api/server/upgrade-xray '{"server_id":1,"version":"latest"}' >/dev/null
wait_upgrade_cmd acked && echo "OK: latest 升级命令 acked（镜像）" || { tail -10 "$WORK/agent.log"; exit 1; }
"$TEST_XRAY" version | head -1 | grep -q "$VER_NUM" \
    && echo "OK: latest 升级后二进制可运行（$VER_NUM）" \
    || { echo "FAIL: latest 替换后二进制异常"; exit 1; }

DB_VER=""
for _ in $(seq 1 30); do
    DB_VER="$(db "SELECT xray_version FROM servers WHERE id=1")"
    [[ "$DB_VER" == "$VER_NUM" ]] && break
    sleep 1
done
[[ "$DB_VER" == "$VER_NUM" ]] \
    && echo "OK: 面板版本号经 telemetry 刷新为 $DB_VER" \
    || { echo "FAIL: 面板版本号未刷新（DB=$DB_VER）"; exit 1; }

echo "E2E-UPGRADE PASS"
