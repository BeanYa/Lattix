#!/usr/bin/env bash
# 全协议向导端到端验收（设计文档 §15）：
#   经面板 HTTP API 创建 7 种协议节点 → 全部 active + realized_config 正确 →
#   订阅 YAML 各协议字段正确（dokodemo 不进订阅）→ 创建用户触发多协议 add_user 扇出。
# 依赖：python3、本机 xray 二进制（XRAY_BIN 可覆盖）、可访问 dest 预检域名。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18099"
API="127.0.0.1:14201"
BOOTSTRAP="bootstrap-proto-test"
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

api() { # <method> <path> [json-body]
    local args=(-s -b "$JAR" -c "$JAR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "http://$ADDR$2"
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
    res="$(api POST /api/nodes "$1")"
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

echo ">> start backend & agent"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1
db "INSERT INTO servers (alias, token) VALUES ('proto01', '$BOOTSTRAP')" >/dev/null
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 1.5

echo ">> login & create user"
api POST /api/login '{"username":"admin","password":"lattix-admin"}' >/dev/null
USER_RES="$(api POST /api/users '{"name":"u1"}')"
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
api PUT "/api/users/1/nodes" "{\"node_ids\":[$(db "SELECT group_concat(id) FROM nodes WHERE protocol != 'dokodemo-door'")]}" >/dev/null
sleep 3

echo ">> 新建用户 u2 并分配 socks/http 节点 → 多协议 add_user 扇出"
U2="$(api POST /api/users '{"name":"u2"}')"
UUID2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$U2")"
api PUT "/api/users/2/nodes" "{\"node_ids\":[$(db "SELECT group_concat(id) FROM nodes WHERE protocol IN ('socks','http')")]}" >/dev/null
sleep 3
U2_COUNT="$(grep -c "$UUID2" "$XRAY_CONFIG" || true)"
[[ "$U2_COUNT" -ge 2 ]] || { echo "FAIL: socks/http accounts 未见 u2（出现 $U2_COUNT 次）"; tail -5 "$WORK/agent.log"; exit 1; }
echo "   socks/http accounts 含新用户 OK"

echo ">> 订阅校验"
SUB="$(curl -s "http://$ADDR/sub/$SUB_TOKEN")"
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
