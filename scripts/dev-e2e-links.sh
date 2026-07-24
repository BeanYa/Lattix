#!/usr/bin/env bash
# 分享链接订阅端到端验收（设计文档 §14 `vless://` 链接订阅）：
#   GET /sub/{token}/links → base64 解码 → vless 普通/加密节点链接参数齐全；
#   /sub/{token}（mihomo YAML）回归。
# 依赖：python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18108"
API="127.0.0.1:14208"
BOOTSTRAP="bootstrap-links-test"
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"

cleanup() {
    kill ${BPID:-} ${APID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build"
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)

db() { python3 - "$WORK/lattix.db" "$1" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
cur = con.execute(sys.argv[2])
con.commit()
for row in cur: print("|".join("" if c is None else str(c) for c in row))
PY
}

api() {
    local args=(-s -b "$JAR" -c "$JAR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "http://$ADDR$2"
}

wait_active() {
    for _ in $(seq 1 40); do
        [[ "$(db "SELECT status FROM nodes WHERE id=$1")" == "active" ]] && return 0
        sleep 0.5
    done
    echo "FAIL: 节点 $1 未 active"; return 1
}

echo ">> start backend & agent"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1
db "INSERT INTO servers (alias, token) VALUES ('lk01', '$BOOTSTRAP')" >/dev/null
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 1.5

echo ">> 用户 + 普通 vless 节点 + VLESS Encryption 节点"
api POST /api/login '{"username":"admin","password":"lattix-admin"}' >/dev/null
USER_RES="$(api POST /api/users '{"name":"u1"}')"
TOK="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$USER_RES")"
N1="$(api POST /api/nodes '{"server_id":1,"protocol":"vless"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
N2="$(api POST /api/nodes '{"server_id":1,"protocol":"vless","encryption":"mlkem768","flow":"none"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
wait_active "$N1"; wait_active "$N2"
api PUT "/api/users/1/nodes" "{\"node_ids\":[$N1,$N2]}" >/dev/null
sleep 1

echo ">> /sub/{token}/links 校验"
LINKS="$(curl -s "http://$ADDR/sub/$TOK/links" | python3 -c 'import base64,sys;print(base64.b64decode(sys.stdin.read()).decode())')"
L1="$(grep -c '^vless://' <<<"$LINKS")"
[[ "$L1" == "2" ]] && echo "OK: 2 条 vless 链接" || { echo "FAIL: 链接数 $L1 ≠ 2"; echo "$LINKS"; exit 1; }
grep -q 'security=reality' <<<"$LINKS" && grep -q 'pbk=' <<<"$LINKS" && grep -q 'sid=' <<<"$LINKS" \
    && grep -q 'sni=' <<<"$LINKS" && grep -q 'flow=xtls-rprx-vision' <<<"$LINKS" \
    && echo "OK: reality/flow 参数齐全" || { echo "FAIL: 链接参数缺失"; echo "$LINKS"; exit 1; }
grep -q 'encryption=none' <<<"$LINKS" \
    && echo "OK: 普通节点 encryption=none" || { echo "FAIL: 缺 encryption=none"; echo "$LINKS"; exit 1; }
grep -q 'encryption=mlkem768x25519plus.' <<<"$LINKS" \
    && echo "OK: 加密节点 encryption 客户端字符串" || { echo "FAIL: 缺 encryption 字符串"; echo "$LINKS"; exit 1; }
grep -q '#lk01-vless-' <<<"$LINKS" && echo "OK: 节点命名 fragment" || { echo "FAIL: 命名异常"; echo "$LINKS"; exit 1; }

echo ">> /sub/{token}（mihomo YAML）回归"
SUB="$(curl -s "http://$ADDR/sub/$TOK")"
[[ "$(grep -c 'type: vless' <<<"$SUB")" == "2" ]] \
    && echo "OK: YAML 订阅正常" || { echo "FAIL: YAML 订阅异常"; echo "$SUB"; exit 1; }

echo "E2E-LINKS PASS"
