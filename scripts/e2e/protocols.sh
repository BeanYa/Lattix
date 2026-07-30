#!/usr/bin/env bash
# 全协议向导端到端验收（设计文档 §15）：
#   经面板 RPC API 创建 7 种协议节点 → 全部 active + realized_config 正确 →
#   订阅 YAML 各协议字段正确（dokodemo 不进订阅）→ 创建用户触发多协议 add_user 扇出。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）、可访问 dest 预检域名。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18099"
API="127.0.0.1:14201"
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"
CSRF=""

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

port_open() { python3 -c "import socket,sys; s=socket.socket(); sys.exit(0 if s.connect_ex(('127.0.0.1', int(sys.argv[1])))==0 else 1)" "$1"; }

# wait_node <id>：轮询节点进入 active/failed，输出 "status|realized|error"。
wait_node() {
    for _ in $(seq 1 40); do
        s="$(db "SELECT status FROM nodes WHERE id=$1")"
        case "$s" in
            active|failed)
                db "SELECT status || '|' || COALESCE(realized_config,'') || '|' || COALESCE(error,'') FROM nodes WHERE id=$1"
                return 0 ;;
        esac
        sleep 0.5
    done
    echo "TIMEOUT||"; return 1
}

# create_node <json-body>：创建节点并等待 active，输出 realized_config。
create_node() {
    local res id out
    res="$(rpc_data POST /api/node/create "$1")"
    id="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$res" 2>/dev/null)" \
        || { echo "FAIL: 创建节点响应异常: $res"; exit 1; }
    out="$(wait_node "$id")"
    [[ "$out" == active\|* ]] || { echo "FAIL: 节点 $id 未 active: $out"; tail -5 "$WORK/agent.log"; exit 1; }
    echo "$out" | cut -d'|' -f2
}

check_port() { # <realized-json>
    local p
    p="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["port"])' "$1")"
    port_open "$p" || { echo "FAIL: 端口 $p 未监听"; exit 1; }
    echo "   端口 $p OK"
}

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

echo ">> 登录 + 建服务器 + 拉起 agent"
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }
SRV="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"proto01","address":"127.0.0.1"}')"
BOOTSTRAP="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["bootstrap_token"])' "$SRV")"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -settings "$WORK/agent.settings.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2
grep -q "authenticated as server" "$WORK/agent.log" || { echo "FAIL: agent 未认证"; cat "$WORK/agent.log"; exit 1; }

echo ">> create user u1"
USER_RES="$(rpc_data POST /api/user/create '{"name":"u1"}')"
SUB_TOKEN="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$USER_RES")"

echo ">> vless tcp vision"
R="$(create_node '{"server_id":1,"protocol":"vless"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="tcp" and rc["flow"]=="xtls-rprx-vision" and rc["public_key"], rc' "$R" && check_port "$R"

echo ">> vless grpc（无 flow）"
R="$(create_node '{"server_id":1,"protocol":"vless","network":"grpc","service_name":"gsvc","flow":"none"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="grpc" and rc["service_name"]=="gsvc" and rc.get("flow","")=="", rc' "$R" && check_port "$R"

echo ">> vless xhttp"
R="$(create_node '{"server_id":1,"protocol":"vless","network":"xhttp","path":"/xp","mode":"packet-up","flow":"none"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="xhttp" and rc["path"]=="/xp" and rc["mode"]=="packet-up", rc' "$R" && check_port "$R"

echo ">> vmess tcp"
R="$(create_node '{"server_id":1,"protocol":"vmess"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="tcp" and rc["public_key"], rc' "$R" && check_port "$R"

echo ">> trojan grpc"
R="$(create_node '{"server_id":1,"protocol":"trojan","network":"grpc","service_name":"tsvc"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="grpc" and rc["service_name"]=="tsvc", rc' "$R" && check_port "$R"

echo ">> shadowsocks 2022-blake3-aes-128-gcm"
R="$(create_node '{"server_id":1,"protocol":"shadowsocks","method":"2022-blake3-aes-128-gcm"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["method"]=="2022-blake3-aes-128-gcm", rc' "$R" && check_port "$R"

echo ">> shadowsocks aes-256-gcm（旧式）"
R="$(create_node '{"server_id":1,"protocol":"shadowsocks","method":"aes-256-gcm"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["method"]=="aes-256-gcm", rc' "$R" && check_port "$R"

echo ">> socks"
R="$(create_node '{"server_id":1,"protocol":"socks"}')" && check_port "$R"

echo ">> http"
R="$(create_node '{"server_id":1,"protocol":"http"}')" && check_port "$R"

echo ">> dokodemo-door"
R="$(create_node '{"server_id":1,"protocol":"dokodemo-door","target_address":"127.0.0.1","target_port":18099}')" && check_port "$R"

echo ">> 分配全部非 dokodemo 节点给 u1（§16 默认全关，需显式分配）"
NODE_IDS="$(db "SELECT group_concat(id) FROM nodes WHERE protocol != 'dokodemo-door'")"
rpc_data POST /api/user/set-nodes "{\"user_id\":1,\"node_ids\":[$NODE_IDS]}" >/dev/null
sleep 3

echo ">> 新建用户 u2 并分配 socks/http 节点 → 多协议 add_user 扇出"
U2="$(rpc_data POST /api/user/create '{"name":"u2"}')"
UUID2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$U2")"
U2_ID="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$U2")"
NODE_IDS2="$(db "SELECT group_concat(id) FROM nodes WHERE protocol IN ('socks','http')")"
rpc_data POST /api/user/set-nodes "{\"user_id\":$U2_ID,\"node_ids\":[$NODE_IDS2]}" >/dev/null
sleep 3
U2_COUNT="$(grep -c "$UUID2" "$XRAY_CONFIG" || true)"
[[ "$U2_COUNT" -ge 2 ]] || { echo "FAIL: socks/http accounts 未见 u2（出现 $U2_COUNT 次）"; tail -5 "$WORK/agent.log"; exit 1; }
echo "   socks/http accounts 含新用户 OK"

echo ">> 订阅校验"
SUB="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=clash")"
check() { grep -q "$1" <<<"$SUB" || { echo "FAIL: 订阅缺少 $1"; echo "$SUB"; exit 1; }; }
check "type: vless"
check "type: vmess"
check "type: trojan"
check "type: ss"
check "type: socks5"
check "type: http"
check "reality-opts:"
check "grpc-opts:"
check "xhttp-opts:"
check "cipher: 2022-blake3-aes-128-gcm"
check "cipher: aes-256-gcm"
check "cipher: auto"
if grep -q "dokodemo" <<<"$SUB"; then echo "FAIL: 订阅不应包含 dokodemo 节点"; exit 1; fi
PROXY_COUNT="$(grep -c 'server: ' <<<"$SUB")"
[[ "$PROXY_COUNT" -eq 9 ]] || { echo "FAIL: 订阅应有 9 个代理（dokodemo 除外），实际 $PROXY_COUNT"; echo "$SUB"; exit 1; }
echo "   9 个代理项、各协议字段 OK，dokodemo 已排除"

echo "E2E-PROTOCOLS PASS"
