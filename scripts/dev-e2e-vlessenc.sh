#!/usr/bin/env bash
# VLESS Encryption 数据通路端到端验收（设计文档 §15）：
#   创建 vless+encryption(mlkem768) 与普通 vless vision 节点 →
#   本机起第二个 xray 作客户端（reality + encryption 参数取自 realized_config）→
#   curl 经 socks 代理访问外网验证真实数据通路；订阅 YAML 校验 encryption 字段。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）、外网可达。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18100"
API="127.0.0.1:14202"
XRAY_CONFIG="$WORK/xray-config.json"
CLIENT_CONFIG="$WORK/client-config.json"
CLIENT_SOCKS=11081
JAR="$WORK/cookies.txt"
CSRF=""

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

# start_client <realized-json> <uuid>
start_client() {
    python3 - "$1" "$2" "$CLIENT_CONFIG" "$CLIENT_SOCKS" <<'PY'
import json, sys
rc, uuid, path, socks = json.loads(sys.argv[1]), sys.argv[2], sys.argv[3], int(sys.argv[4])
user = {"id": uuid, "encryption": rc.get("encryption") or "none"}
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

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

echo ">> 登录 + 建服务器 + 拉起 agent"
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }
SRV="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"enc01","address":"127.0.0.1"}')"
BOOTSTRAP="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["bootstrap_token"])' "$SRV")"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -settings "$WORK/agent.settings.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2
grep -q "authenticated as server" "$WORK/agent.log" || { echo "FAIL: agent 未认证"; cat "$WORK/agent.log"; exit 1; }

echo ">> create user"
USER_RES="$(rpc_data POST /api/user/create '{"name":"u1"}')"
UUID="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$USER_RES")"
USER_ID="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$USER_RES")"
SUB_TOKEN="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$USER_RES")"

echo ">> vless + VLESS Encryption（mlkem768 后量子）"
ID1="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless","encryption":"mlkem768","flow":"none"}' \
    | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
R1="$(wait_node "$ID1")" || { echo "$R1"; tail -5 "$WORK/agent.log"; exit 1; }
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["encryption"].startswith("mlkem768x25519plus."), rc["encryption"]' "$R1"
echo "   节点 active，encryption 客户端字符串已上报"

echo ">> vless + VLESS Encryption + vision 组合（§15 native 拼接，1-RTT）"
ID2="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless","encryption":"mlkem768","flow":"xtls-rprx-vision"}' \
    | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
R2="$(wait_node "$ID2")" || { echo "$R2"; tail -5 "$WORK/agent.log"; exit 1; }
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert ".1rtt." in rc["encryption"] and rc["flow"]=="xtls-rprx-vision", rc' "$R2" \
    && echo "OK: 组合节点 active，客户端字符串为 1-RTT"

echo ">> 普通 vless vision（对照组）"
ID3="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless"}' \
    | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
R3="$(wait_node "$ID3")" || { tail -5 "$WORK/agent.log"; exit 1; }

echo ">> 分配全部节点给用户"
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID,\"node_ids\":[$ID1,$ID2,$ID3]}" >/dev/null
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
