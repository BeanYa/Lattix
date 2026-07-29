#!/usr/bin/env bash
# VLESS Encryption 数据通路端到端验收（设计文档 §15）：
#   创建 vless+encryption(mlkem768) 与普通 vless vision 节点 →
#   本机起第二个 xray 作客户端（reality + encryption 参数取自 realized_config）→
#   curl 经 socks 代理访问外网验证真实数据通路；订阅 YAML 校验 encryption 字段。
# 依赖：python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）、外网可达。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18100"
API="127.0.0.1:14202"
BOOTSTRAP="bootstrap-vlessenc-test"
XRAY_CONFIG="$WORK/xray-config.json"
CLIENT_CONFIG="$WORK/client-config.json"
CLIENT_SOCKS=11081
JAR="$WORK/cookies.txt"

cleanup() {
    kill ${BPID:-} ${APID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    pkill -f "xray run -config $CLIENT_CONFIG" 2>/dev/null || true
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

wait_node() {
    for _ in $(seq 1 40); do
        s="$(db "SELECT status FROM nodes WHERE id=$1")"
        case "$s" in
            active) db "SELECT realized_config FROM nodes WHERE id=$1"; return 0 ;;
            failed) echo "FAIL: 节点 $1 failed: $(db "SELECT error FROM nodes WHERE id=$1")"; return 1 ;;
        esac
        sleep 0.5
    done
    echo "FAIL: 节点 $1 超时未 active"; return 1
}

# start_client <realized-json> <uuid>：按 realized 起 xray 客户端并 curl 验证数据通路。
start_client() {
    python3 - "$1" "$2" "$CLIENT_CONFIG" "$CLIENT_SOCKS" <<'PY'
import json, sys
rc, uuid, path, socks = json.loads(sys.argv[1]), sys.argv[2], sys.argv[3], int(sys.argv[4])
user = {"id": uuid, "encryption": rc.get("encryption") or "none"}  # 新版 xray 客户端要求显式 encryption
if rc.get("flow"):
    user["flow"] = rc["flow"]
cfg = {
    "inbounds": [{"tag": "in", "protocol": "socks", "port": socks,
                  "settings": {"auth": "noauth", "udp": True}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": rc["port"], "users": [user]}]},
        "streamSettings": {"network": "tcp", "security": "reality", "realitySettings": {
            "serverName": rc["server_name"], "publicKey": rc["public_key"],
            "shortId": rc["short_id"], "fingerprint": "chrome"}},
    }],
}
json.dump(cfg, open(path, "w"))
PY
    "$XRAY_BIN" run -config "$CLIENT_CONFIG" >"$WORK/client.log" 2>&1 &
    sleep 1
    local code
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:$CLIENT_SOCKS" --max-time 20 https://example.com)" \
        || code="000"
    pkill -f "xray run -config $CLIENT_CONFIG" 2>/dev/null || true
    [[ "$code" == "200" ]] || { echo "FAIL: 数据通路不通（HTTP $code）"; tail -8 "$WORK/agent.log"; tail -3 "$WORK/client.log"; return 1; }
}

echo ">> start backend & agent"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1
db "INSERT INTO servers (alias, token) VALUES ('enc01', '$BOOTSTRAP')" >/dev/null
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 1.5

echo ">> login & create user"
api POST /api/login '{"username":"admin","password":"lattix-admin"}' >/dev/null
USER_RES="$(api POST /api/users '{"name":"u1"}')"
UUID="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$USER_RES")"
SUB_TOKEN="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$USER_RES")"

echo ">> vless + VLESS Encryption（mlkem768 后量子）"
ID1="$(api POST /api/nodes '{"server_id":1,"protocol":"vless","encryption":"mlkem768","flow":"none"}' \
    | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
R1="$(wait_node "$ID1")" || { echo "$R1"; tail -5 "$WORK/agent.log"; exit 1; }
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["encryption"].startswith("mlkem768x25519plus."), rc["encryption"]' "$R1"
echo "   节点 active，encryption 客户端字符串已上报"

echo ">> vless + VLESS Encryption + vision 组合（§15 native 拼接，1-RTT）"
ID2="$(api POST /api/nodes '{"server_id":1,"protocol":"vless","encryption":"mlkem768","flow":"xtls-rprx-vision"}' \
    | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
R2="$(wait_node "$ID2")" || { echo "$R2"; tail -5 "$WORK/agent.log"; exit 1; }
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert ".1rtt." in rc["encryption"] and rc["flow"]=="xtls-rprx-vision", rc' "$R2" \
    && echo "OK: 组合节点 active，客户端字符串为 1-RTT"

echo ">> 普通 vless vision（对照组）"
ID3="$(api POST /api/nodes '{"server_id":1,"protocol":"vless"}' \
    | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
R3="$(wait_node "$ID3")" || { tail -5 "$WORK/agent.log"; exit 1; }

echo ">> 分配全部节点给用户（§16 默认全关，需显式分配）"
api PUT "/api/users/1/nodes" "{\"node_ids\":[$ID1,$ID2,$ID3]}" >/dev/null
sleep 2 # 等 add_user 落配置

start_client "$R1" "$UUID" && echo "OK: VLESS Encryption 数据通路（curl https://example.com → 200）" || exit 1
start_client "$R2" "$UUID" && echo "OK: vision+Encryption 数据通路（curl https://example.com → 200）" || exit 1
start_client "$R3" "$UUID" && echo "OK: vless vision 数据通路（对照组）" || exit 1

echo ">> 订阅校验"
SUB="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=clash")"
grep -q "encryption: mlkem768x25519plus." <<<"$SUB" \
    && echo "OK: 订阅含 encryption 字段" \
    || { echo "FAIL: 订阅缺少 encryption 字段"; echo "$SUB"; exit 1; }

echo "E2E-VLESSENC PASS"
