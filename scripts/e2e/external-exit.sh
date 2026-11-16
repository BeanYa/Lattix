#!/usr/bin/env bash
# 未托管外部直链出口链路端到端验收（docs/superpowers/specs/2026-11-17-external-exit-chains-design.md）：
#   A（托管入口，共享端点）+ 可选 B（托管中间跳）+ E（未托管外部 socks5 节点）
#   → 用户订阅取 A 的端点（access_uuid）→ 客户端 VLESS+Reality 接入 A
#   → A 端点按链路渲染 socks5 outbound 拨回环 forward → 链跳纯 TCP → E 出网。
# 断言：外部节点建档（无安装命令/NAT 仅出口档）、建链（末位外部、无出口业务节点）、
#       入口 forward 回环与端点 active、A 配置含 socks outbound、真实流量（可跳过）、
#       被引用时链接不可改 + Agent 动作被拒绝。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）。
# 外网断言默认开启，EXTERNAL_EXIT_SKIP_EXTERNAL=1 跳过（离线环境）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18216"
API_A="127.0.0.1:14316"
API_B="127.0.0.1:14336"
SOCKS_EXT_PORT=11811
CLIENT_SOCKS_PORT=11812
ADMIN_PASS="testpass123"
XRAY_CONFIG_A="$WORK/xray-a.json"
XRAY_CONFIG_B="$WORK/xray-b.json"
XRAY_CONFIG_EXT="$WORK/xray-ext.json"
CLIENT_CONFIG="$WORK/client.json"
JAR="$WORK/cookies.txt"
CSRF=""

cleanup() {
    kill ${BPID:-} ${APID_A:-} ${APID_B:-} ${EXTPID:-} ${CLIENTPID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG_A" 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG_B" 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG_EXT" 2>/dev/null || true
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

rpc_data() {
    rpc_raw "$@" | python3 -c '
import json,sys
value=json.load(sys.stdin)
if value["code"] not in ("OK","ACCEPTED"):
    raise SystemExit(f"RPC failed: {value}")
print(json.dumps(value["data"], separators=(",",":")))
'
}

rpc_code() {
    rpc_raw "$@" | python3 -c 'import json,sys;print(json.load(sys.stdin)["code"])'
}

py() { python3 -c "import json,sys; d=json.loads(sys.argv[1]); print($1)" "$2"; }

port_open() { python3 -c "import socket,sys; s=socket.socket(); s.settimeout(1); sys.exit(0 if s.connect_ex(('127.0.0.1',$1))==0 else 1)"; }

chain_field() { rpc_data GET /api/chain/list | python3 -c "import json,sys; cs=json.load(sys.stdin); c=next((x for x in cs if x['id']==$1), None); print($2 if c else '')"; }

wait_chain() {
    for _ in $(seq 1 "${3:-60}"); do
        local st; st="$(chain_field "$1" "c['status']")"
        [[ "$st" == "$2" ]] && return 0
        if [[ "$st" == "failed" || "$st" == "active_failed" ]]; then
            echo "FAIL: 链 $1 $st: $(chain_field "$1" "c['error']")"
            tail -n 20 "$WORK/agent-a.log" 2>/dev/null || true
            return 1
        fi
        sleep 1
    done
    echo "FAIL: 链 $1 未变为 $2（当前 $(chain_field "$1" "c['status']")）"
    tail -n 20 "$WORK/agent-a.log" 2>/dev/null || true
    return 1
}

start_agent() {
    local which="$1" token="$2" api="$3" cfg="$4"
    : > "$WORK/agent-$which.log"
    mkdir -p "$WORK/agent-$which"
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$token" -state "$WORK/agent-$which/state.json" \
        -settings "$WORK/agent-$which/settings.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$cfg" -xray-api "$api" -xray-runner exec \
        >>"$WORK/agent-$which.log" 2>&1 &
    echo $!
}

echo ">> 外部节点 E：本机 xray socks5（带用户名密码）作为「外部直链」出口"
cat >"$XRAY_CONFIG_EXT" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "tag": "ext-socks",
    "listen": "127.0.0.1",
    "port": $SOCKS_EXT_PORT,
    "protocol": "socks",
    "settings": {"auth": "password", "accounts": [{"user": "extuser", "pass": "extpass"}], "udp": false}
  }],
  "outbounds": [{"protocol": "freedom", "tag": "direct"}]
}
EOF
"$XRAY_BIN" run -config "$XRAY_CONFIG_EXT" >"$WORK/ext.log" 2>&1 &
EXTPID=$!

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 50); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

LOGIN="$(rpc_data POST /api/auth/login "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}")"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }

echo ">> 解析器 API（新增/编辑服务器对话框同款）：socks5 链接预览"
PREVIEW="$(rpc_data POST /api/server/parse-link '{"link":"socks5://extuser:extpass@127.0.0.1:11811#ExtSocks"}')"
[[ "$(py "d['protocol']" "$PREVIEW")" == "socks" && "$(py "d['port']" "$PREVIEW")" == "$SOCKS_EXT_PORT" ]] \
    && echo "OK: 解析器返回 socks5 规格（$(py "d['transport']" "$PREVIEW")）" \
    || { echo "FAIL: 解析预览: $PREVIEW"; exit 1; }
[[ "$(py "d['exit']['password']" "$PREVIEW")" != "extpass" ]] \
    && echo "OK: 预览中的凭据已打码" || { echo "FAIL: 凭据未打码"; exit 1; }

echo ">> 服务器：A（托管直接入口）、B（托管中间跳）、E（未托管外部直链）"
RA="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"ext-entry-a","address":"127.0.0.1"}')"
AID="$(py "d['server']['id']" "$RA")"; BOOT_A="$(py "d['bootstrap_token']" "$RA")"
RB="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"ext-mid-b","address":"127.0.0.1"}')"
BID="$(py "d['server']['id']" "$RB")"; BOOT_B="$(py "d['bootstrap_token']" "$RB")"
RE="$(rpc_data POST /api/server/create '{"kind":"external","alias":"ext-socks-e","external_link":"socks5://extuser:extpass@127.0.0.1:11811#ExtSocks","country_code":"JP","location":"Test"}')"
EID="$(py "d['server']['id']" "$RE")"
[[ "$(py "d['bootstrap_token']" "$RE")" == "" && "$(py "d['install_command']" "$RE")" == "" ]] \
    && echo "OK: 外部节点无安装命令" || { echo "FAIL: 外部节点建档: $RE"; exit 1; }
[[ "$(py "d['server']['kind']" "$RE")" == "external" && "$(py "d['server']['connection_state']" "$RE")" == "external" ]] \
    && echo "OK: 外部节点 kind/connection_state=external" || { echo "FAIL: 外部节点 DTO: $RE"; exit 1; }
[[ "$(py "d['server']['machine_type']" "$RE")" == "nat" && "$(py "d['server']['allowed_ports']" "$RE")" == "[]" ]] \
    && echo "OK: 外部节点无入站能力（仅出口档）" || { echo "FAIL: 外部节点机器类型: $RE"; exit 1; }
[[ "$(rpc_code POST /api/server/rotate-token "{\"server_id\":$EID}")" == "INVALID_ARGUMENT" ]] \
    && echo "OK: 外部节点拒绝 Agent 动作（轮换凭证）" || { echo "FAIL: 外部节点未拒绝 Agent 动作"; exit 1; }

APID_A="$(start_agent a "$BOOT_A" "$API_A" "$XRAY_CONFIG_A")"
APID_B="$(start_agent b "$BOOT_B" "$API_B" "$XRAY_CONFIG_B")"
for _ in $(seq 1 40); do
    online="$(rpc_data GET /api/server/list | python3 -c "import json,sys; print(sum(1 for s in json.load(sys.stdin) if s['connection_state']=='online'))")"
    [[ "$online" == "2" ]] && break
    sleep 0.5
done
[[ "$online" == "2" ]] && echo "OK: 两台托管 agent 上线" || { echo "FAIL: agent 上线（$online）"; tail -n 20 "$WORK/agent-a.log"; exit 1; }

echo ">> 外部节点只能作最后一跳（校验）"
BAD="$(rpc_code POST /api/chain/create "{\"hops\":[{\"server_id\":$EID},{\"server_id\":$AID}],\"traffic_multiplier\":\"1.000\"}")"
[[ "$BAD" == "INVALID_ARGUMENT" ]] && echo "OK: 外部节点作入口被拒绝" || { echo "FAIL: 外部节点入口校验: $BAD"; exit 1; }

echo ">> 建链 1：A → E（n=0 跳中间跳）"
CHAIN1="$(rpc_data POST /api/chain/create "{\"name\":\"ext-2hop\",\"hops\":[{\"server_id\":$AID},{\"server_id\":$EID}],\"traffic_multiplier\":\"1.000\"}")"
CH1="$(py "d['id']" "$CHAIN1")"
[[ "$(py "d['service_node_id']" "$CHAIN1")" == "0" ]] && echo "OK: 外部出口链无出口业务节点（service_node_id=0）" \
    || { echo "FAIL: service_node_id: $CHAIN1"; exit 1; }
[[ "$(py "d['hops'][-1]['server_id']" "$CHAIN1")" == "$EID" && "$(py "d['hops'][-1]['node_id']" "$CHAIN1")" == "0" ]] \
    && echo "OK: 末位跳为外部服务器且无业务节点" || { echo "FAIL: hops: $CHAIN1"; exit 1; }
[[ "$(py "d['endpoint_id']" "$CHAIN1")" != "0" ]] && echo "OK: 外部出口链自动创建入口共享端点" \
    || { echo "FAIL: 未创建入口端点: $CHAIN1"; exit 1; }

echo ">> 建链 2：A → B → E（n=1 中间跳）"
CHAIN2="$(rpc_data POST /api/chain/create "{\"name\":\"ext-3hop\",\"hops\":[{\"server_id\":$AID},{\"server_id\":$BID},{\"server_id\":$EID}],\"traffic_multiplier\":\"1.000\"}")"
CH2="$(py "d['id']" "$CHAIN2")"
[[ "$(py "d['hops'][1]['server_id']" "$CHAIN2")" == "$BID" ]] && echo "OK: 中间跳 B 落位" \
    || { echo "FAIL: 3 跳链拓扑: $CHAIN2"; exit 1; }

wait_chain "$CH1" active 90 && echo "OK: 链 1 active"
wait_chain "$CH2" active 90 && echo "OK: 链 2 active"
for id in "$CH1" "$CH2"; do
    [[ "$(chain_field "$id" "c.get('endpoint_status','')")" == "active" ]] \
        && echo "OK: 链 $id 入口共享端点 active" || { echo "FAIL: 链 $id 端点未生效"; exit 1; }
done

ENTRY_PORT1="$(chain_field "$CH1" "c['hops'][0]['forward_port']")"
ENTRY_PORT2="$(chain_field "$CH2" "c['hops'][0]['forward_port']")"
port_open "$ENTRY_PORT1" && echo "OK: 链 1 入口 forward 回环监听 $ENTRY_PORT1" || { echo "FAIL: 链 1 forward 未监听"; exit 1; }
port_open "$ENTRY_PORT2" && echo "OK: 链 2 入口 forward 回环监听 $ENTRY_PORT2" || { echo "FAIL: 链 2 forward 未监听"; exit 1; }
if grep -Eq "\"port\": *$SOCKS_EXT_PORT" "$XRAY_CONFIG_A"; then
    echo "OK: A 的 forward 目标为外部节点端口 $SOCKS_EXT_PORT"
else
    echo "FAIL: forward 目标不是外部节点（期望端口 ${SOCKS_EXT_PORT}）"
    python3 - "$XRAY_CONFIG_A" <<'PY'
import json, sys
config = json.load(open(sys.argv[1]))
for inbound in config.get("inbounds", []):
    print("inbound:", inbound.get("tag"), inbound.get("protocol"), inbound.get("port"), json.dumps(inbound.get("settings"))[:200])
for out in config.get("outbounds", []):
    print("outbound:", out.get("tag"), out.get("protocol"), json.dumps(out.get("settings"))[:160])
PY
    exit 1
fi
grep -Eq "\"port\": *$SOCKS_EXT_PORT" "$XRAY_CONFIG_B" && echo "OK: 中间跳 B 的 forward 目标为外部节点端口" \
    || { echo "FAIL: B 的 forward 目标不是外部节点"; exit 1; }

echo ">> 分配用户（订阅入口 = A 的 VLESS+Reality 端点）"
U1="$(rpc_data POST /api/user/create '{"name":"ext-user"}')"
USER_ID1="$(py "d['id']" "$U1")"; SUB_TOKEN="$(py "d['sub_token']" "$U1")"
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[],\"chain_ids\":[$CH1,$CH2]}" >/dev/null
ACCESS_UUID=""
for _ in $(seq 1 20); do
    ACCESS_UUID="$(rpc_data GET /api/user/list | python3 -c "
import json,sys
u=next((x for x in json.load(sys.stdin) if x['id']==$USER_ID1), {})
ca=u.get('chain_assignments') or []
print(ca[0]['access_uuid'] if ca else '')")"
    [[ -n "$ACCESS_UUID" ]] && break
    sleep 0.5
done
[[ -n "$ACCESS_UUID" ]] || { echo "FAIL: 未取到 assignment"; exit 1; }
for _ in $(seq 1 20); do grep -q "$ACCESS_UUID" "$XRAY_CONFIG_A" && break; sleep 0.5; done
grep -q "$ACCESS_UUID" "$XRAY_CONFIG_A" && echo "OK: access_uuid 已扇出到 A 的入口端点" || { echo "FAIL: access_uuid 未扇出"; exit 1; }

# 端点路由（用户分配后才渲染）：每条链一个外部 socks outbound，拨本链入口回环 forward。
HAS_EXTERNAL_OUTBOUND="$(python3 - "$XRAY_CONFIG_A" <<'PY'
import json, sys
config = json.load(open(sys.argv[1]))
print("yes" if any(out.get("protocol") == "socks" for out in config.get("outbounds", [])) else "no")
PY
)"
if [[ "$HAS_EXTERNAL_OUTBOUND" == "yes" ]]; then
    echo "OK: A 配置渲染外部 socks outbound"
else
    echo "FAIL: A 配置缺外部 socks outbound"
    python3 - "$XRAY_CONFIG_A" <<'PY'
import json, sys
config = json.load(open(sys.argv[1]))
for inbound in config.get("inbounds", []):
    print("inbound:", inbound.get("tag"), inbound.get("protocol"), inbound.get("port"))
for out in config.get("outbounds", []):
    print("outbound:", out.get("tag"), out.get("protocol"), json.dumps(out.get("settings"))[:160])
for rule in config.get("routing", {}).get("rules", []):
    print("rule:", json.dumps(rule)[:220])
PY
    tail -n 40 "$WORK/agent-a.log" || true
    exit 1
fi
python3 - "$XRAY_CONFIG_A" "$ENTRY_PORT1" "$ENTRY_PORT2" <<'PY'
import json, sys
config = json.load(open(sys.argv[1]))
socks = [out for out in config.get("outbounds", []) if out.get("protocol") == "socks"]
ports = set()
for out in socks:
    for server in out.get("settings", {}).get("servers", []):
        ports.add(server.get("port"))
    users = out.get("settings", {}).get("servers", [{}])[0].get("users") or []
    assert users and users[0].get("user") == "extuser", "外部凭据未下发: %s" % users
expected = set([int(sys.argv[2]), int(sys.argv[3])])
assert ports == expected, "外部 outbound 拨号端口 %s != 入口 forward %s" % (ports, expected)
print("OK: 外部 socks outbound 携带外部凭据并分别拨两条链的入口 forward")
PY

EP1="$(chain_field "$CH1" "c['endpoint_id']")"
EP_PORT="$(chain_field "$CH1" "c['entry_port']")"
EP_RC="$(db "SELECT realized_config FROM shared_endpoints WHERE id=$EP1")"
EP_PUB="$(py "d['public_key']" "$EP_RC")"; EP_SID="$(py "d['short_id']" "$EP_RC")"
EP_SNAME="$(py "d.get('server_name') or ''" "$EP_RC")"; EP_FLOW="$(py "d.get('flow') or ''" "$EP_RC")"
[[ -n "$EP_PUB" && -n "$EP_PORT" ]] && echo "OK: 端点参数齐备（端口 ${EP_PORT}）" || { echo "FAIL: 端点 realized: $EP_RC"; exit 1; }

echo ">> 客户端：VLESS+Reality 接入 A 端点 → 外部 socks5 出网"
python3 - "$CLIENT_CONFIG" "$EP_PORT" "$ACCESS_UUID" "$EP_PUB" "$EP_SID" "$EP_SNAME" "$EP_FLOW" "$CLIENT_SOCKS_PORT" <<'PY'
import json, sys
out, port, uuid, pub, sid, sname, flow, socks_port = sys.argv[1:]
user = {"id": uuid, "encryption": "none"}
if flow:
    user["flow"] = flow
config = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks-in", "listen": "127.0.0.1", "port": int(socks_port),
                  "protocol": "socks", "settings": {"udp": False}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": int(port), "users": [user]}]},
        "streamSettings": {"network": "tcp", "security": "reality",
                           "realitySettings": {"serverName": sname, "publicKey": pub,
                                               "shortId": sid, "fingerprint": "chrome"}},
    }],
}
json.dump(config, open(out, "w"))
PY
"$XRAY_BIN" run -test -config "$CLIENT_CONFIG" >/dev/null || { echo "FAIL: 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$CLIENT_CONFIG" >"$WORK/client.log" 2>&1 &
CLIENTPID=$!
sleep 1

if [[ "${EXTERNAL_EXIT_SKIP_EXTERNAL:-0}" != "1" ]]; then
    echo ">> 真实流量（客户端 → A 端点 → 链 → 外部 socks5 → Internet）"
    OUT=""
    for _ in $(seq 1 10); do
        OUT="$(curl -s --max-time 12 --socks5-hostname 127.0.0.1:$CLIENT_SOCKS_PORT https://www.cloudflare.com/cdn-cgi/trace || true)"
        grep -q "ip=" <<<"$OUT" && break
        sleep 1
    done
    grep -q "ip=" <<<"$OUT" && echo "OK: 经外部 socks5 出网成功（$(grep -m1 '^ip=' <<<"$OUT")）" \
        || { echo "FAIL: 链路真实流量失败"; tail -n 30 "$WORK/client.log" "$WORK/agent-a.log" "$WORK/ext.log"; exit 1; }
    echo ">> 流量统计（入口端点 access:* 计入用户与链路）"
    TRAFFIC=0
    for _ in $(seq 1 100); do
        TRAFFIC="$(chain_field "$CH1" "(c.get('traffic') or {}).get('raw_up',0)+(c.get('traffic') or {}).get('raw_down',0)")"
        [[ "${TRAFFIC:-0}" != "0" ]] && break
        sleep 1
    done
    if [[ "${TRAFFIC:-0}" != "0" ]]; then
        echo "OK: 链路流量已入账（raw 合计 $TRAFFIC 字节）"
    else
        echo "FAIL: 链路流量未入账"
        echo "--- chain_traffic_totals ---"
        db "SELECT chain_id,hop_id,raw_up,raw_down FROM chain_traffic_totals"
        echo "--- traffic_cursors ---"
        db "SELECT server_id,counter_key,up,down FROM traffic_cursors"
        echo "--- traffic ---"
        db "SELECT node_id,user_uuid,up,down FROM traffic"
        echo "--- agent-a traffic log ---"
        grep -i "telemetry\|traffic" "$WORK/agent-a.log" | tail -20 || true
        echo "--- backend log ---"
        grep -i "traffic\|telemetry" "$WORK/backend.log" | tail -20 || true
        exit 1
    fi
else
    echo "SKIP: EXTERNAL_EXIT_SKIP_EXTERNAL=1，跳过真实流量断言"
fi

echo ">> 外部链接被链路引用时不可修改；仅改别名不受影响"
EDIT_CODE="$(rpc_code POST /api/server/update "{\"server_id\":$EID,\"alias\":\"ext-renamed\",\"country_code\":\"JP\",\"location\":\"Test\",\"external_link\":\"socks5://extuser:otherpass@127.0.0.1:11811#ExtSocks\"}")"
[[ "$EDIT_CODE" == "CONFLICT" ]] && echo "OK: 被链路引用时外部链接不可改（CONFLICT）" \
    || { echo "FAIL: 外部链接被引用时可改（${EDIT_CODE}）"; exit 1; }
ALIAS_CODE="$(rpc_code POST /api/server/update "{\"server_id\":$EID,\"alias\":\"ext-renamed\",\"country_code\":\"JP\",\"location\":\"Test\"}")"
[[ "$ALIAS_CODE" == "OK" ]] && echo "OK: 仅改别名不受影响" || { echo "FAIL: 别名更新（${ALIAS_CODE}）"; exit 1; }

echo "OK: 未托管外部直链出口链路端到端验收通过"
