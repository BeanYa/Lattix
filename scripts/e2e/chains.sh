#!/usr/bin/env bash
# 代理链与 NAT 支持端到端验收（设计文档 §21/§21.1）：
#   同机双 agent（A=direct 当入口、C=NAT 仅出口档 machine_type=nat/allowed_ports=[] 当出口）
#   → 建链（A=入口、C=出口，vless+reality 出口节点）→ 五阶段编排 active
#   （vless 链入口落在入口机共享端点上：客户端连端点端口，链 forward 仅为本机回环转发）
#   → 分配（chain_ids → user_chain_assignments：access_uuid 扇出到共享端点；
#     业务用户 UUID 不下发出口 xray，framework-design「user_nodes 不承载新链路授权」）
#   → 真实流量 → 订阅 → degraded → 失败重试 → 删链。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）。
# 外网断言默认开启，CHAINS_SKIP_EXTERNAL=1 跳过（离线环境）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

# VLESS Encryption 需带 vlessenc 子命令的 xray（§15）；缺失时跳过链4（vless+httpupgrade）。
HAS_VLESSENC=true
"$XRAY_BIN" vlessenc >/dev/null 2>&1 || HAS_VLESSENC=false

ADDR="127.0.0.1:18116"
API_A="127.0.0.1:14216"
API_C="127.0.0.1:14226"
SOCKS_PORT=11808
BLOCK_PORT=11809
PROBE_URL="https://www.cloudflare.com/cdn-cgi/trace"
ADMIN_PASS="testpass123"
XRAY_CONFIG_A="$WORK/xray-a.json"
XRAY_CONFIG_C="$WORK/xray-c.json"
CLIENT_CONFIG="$WORK/client.json"
JAR="$WORK/cookies.txt"
CSRF=""

cleanup() {
    kill ${BPID:-} ${APID_A:-} ${APID_C:-} ${XPID:-} ${BLOCKPID:-} ${WSXPID:-} ${HUXPID:-} ${TCXPID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG_A" 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG_C" 2>/dev/null || true
    pkill -f "xray run -config $CLIENT_CONFIG" 2>/dev/null || true
    pkill -f "xray run -config $WORK/client-ws.json" 2>/dev/null || true
    pkill -f "xray run -config $WORK/client-hu.json" 2>/dev/null || true
    pkill -f "xray run -config $WORK/client-tls-chain.json" 2>/dev/null || true
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

# rpc_code：输出 RPC 信封 code（不检查成功）。
rpc_code() {
    rpc_raw "$@" | python3 -c 'import json,sys;print(json.load(sys.stdin)["code"])'
}

py() { python3 -c "import json,sys; d=json.loads(sys.argv[1]); print($1)" "$2"; }

port_open() { python3 -c "import socket,sys; s=socket.socket(); s.settimeout(1); sys.exit(0 if s.connect_ex(('127.0.0.1',$1))==0 else 1)"; }

# chain_field <id> <py-expr on c>
chain_field() { rpc_data GET /api/chain/list | python3 -c "import json,sys; cs=json.load(sys.stdin); c=next((x for x in cs if x['id']==$1), None); print($2 if c else '')"; }

# wait_chain <id> <status> [tries]
wait_chain() {
    for _ in $(seq 1 "${3:-60}"); do
        local st; st="$(chain_field "$1" "c['status']")"
        [[ "$st" == "$2" ]] && return 0
        if [[ "$st" == "failed" && "$2" != "failed" ]]; then
            echo "FAIL: 链 $1 failed: $(chain_field "$1" "c['error']")"
            tail -n 5 "$WORK/agent-a.log" "$WORK/agent-c.log"
            return 1
        fi
        sleep 1
    done
    echo "FAIL: 链 $1 未变为 $2（当前 $(chain_field "$1" "c['status']")）"
    tail -n 5 "$WORK/agent-a.log" "$WORK/agent-c.log"
    return 1
}

start_agent_a() {
    : > "$WORK/agent-a.log"
    mkdir -p "$WORK/agent-a"
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" ${1:+-token "$1"} -state "$WORK/agent-a/state.json" \
        -settings "$WORK/agent-a/settings.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG_A" -xray-api "$API_A" -xray-runner exec \
        >>"$WORK/agent-a.log" 2>&1 &
    APID_A=$!
}
start_agent_c() {
    : > "$WORK/agent-c.log"
    mkdir -p "$WORK/agent-c"
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" ${1:+-token "$1"} -state "$WORK/agent-c/state.json" \
        -settings "$WORK/agent-c/settings.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG_C" -xray-api "$API_C" -xray-runner exec \
        >>"$WORK/agent-c.log" 2>&1 &
    APID_C=$!
}

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

LOGIN="$(rpc_data POST /api/auth/login "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}")"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }

echo ">> 两台服务器：A（direct 入口）、C（NAT 仅出口档）"
RA="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"chain-a","address":"127.0.0.1"}')"
AID="$(py "d['server']['id']" "$RA")"
BOOT_A="$(py "d['bootstrap_token']" "$RA")"
RC="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"chain-c","address":"127.0.0.1","machine_type":"nat","allowed_ports":[]}')"
CID="$(py "d['server']['id']" "$RC")"
BOOT_C="$(py "d['bootstrap_token']" "$RC")"
[[ "$(py "d['server']['machine_type']" "$RC")" == "nat" && "$(py "d['server']['allowed_ports']" "$RC")" == "[]" ]] \
    && echo "OK: C 为 NAT 仅出口档（machine_type=nat，无端口段）" || { echo "FAIL: C 建档: $RC"; exit 1; }
start_agent_a "$BOOT_A"; start_agent_c "$BOOT_C"
sleep 2
[[ "$(rpc_data GET /api/server/list | python3 -c "import json,sys; print(sum(1 for s in json.load(sys.stdin) if s['connection_state']=='online'))")" == "2" ]] \
    && echo "OK: 双 agent 上线" || { echo "FAIL: agent 上线"; exit 1; }

echo ">> 用户 + 建链（A=入口、C=出口，vless 出口节点）"
U1="$(rpc_data POST /api/user/create '{"name":"chain-user"}')"
UUID1="$(py "d['uuid']" "$U1")"
USER_ID1="$(py "d['id']" "$U1")"
SUB_TOKEN="$(py "d['sub_token']" "$U1")"
CHAIN="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"vless\"}}")"
CH1="$(py "d['id']" "$CHAIN")"
NID1="$(py "d['hops'][-1]['node_id']" "$CHAIN")"
[[ "$CH1" != "" && "$NID1" != "0" ]] || { echo "FAIL: 建链响应: $CHAIN"; exit 1; }

wait_chain "$CH1" active 90 && echo "OK: 链 active（五阶段编排：apply_node→portal→bridge→forward）"
HOPS="$(chain_field "$CH1" "[(h['seq'],h['role'],h['status'],h['forward_port'],h['portal_port']) for h in c['hops']]")"
echo "   跳状态: $HOPS"
[[ "$(chain_field "$CH1" "all(h['status']=='active' for h in c['hops'])")" == "True" ]] \
    && echo "OK: 全部跳 active" || { echo "FAIL: 跳状态: $HOPS"; exit 1; }
ENTRY_PORT="$(chain_field "$CH1" "c['hops'][0]['forward_port']")"
PORTAL_PORT="$(chain_field "$CH1" "c['hops'][0]['portal_port']")"
[[ "$ENTRY_PORT" != "0" && "$PORTAL_PORT" != "0" ]] \
    && echo "OK: 入口 forward 端口 $ENTRY_PORT（回环）、portal 端口 $PORTAL_PORT 已回执" \
    || { echo "FAIL: 端口回执: $HOPS"; exit 1; }
port_open "$ENTRY_PORT" && echo "OK: 入口 forward 已监听（回环，供端点路由转发）" \
    || { echo "FAIL: 入口 forward 未监听"; exit 1; }
grep -q "chainportal_" "$XRAY_CONFIG_A" && grep -q "chainfwd_" "$XRAY_CONFIG_A" \
    && echo "OK: A 配置含 portal+forward 配置件" || { echo "FAIL: A 配置件"; exit 1; }
grep -q "chainbr_" "$XRAY_CONFIG_C" && echo "OK: C 配置含 bridge 配置件" || { echo "FAIL: C 配置件"; exit 1; }

# vless 链入口为服务器级共享端点：端点 active 后链才可能回到 active，订阅端口即端点端口。
EP_ID="$(chain_field "$CH1" "c['endpoint_id']")"
[[ -n "$EP_ID" && "$EP_ID" != "0" ]] || { echo "FAIL: vless 链未落到共享端点"; exit 1; }
for _ in $(seq 1 30); do
    [[ "$(chain_field "$CH1" "c.get('endpoint_status','')")" == "active" ]] && break
    sleep 1
done
EP_PORT="$(chain_field "$CH1" "c['entry_port']")"
[[ "$(chain_field "$CH1" "c.get('endpoint_status','')")" == "active" && -n "$EP_PORT" && "$EP_PORT" != "0" ]] \
    && echo "OK: 共享端点 active（endpoint $EP_ID，客户端入口端口 $EP_PORT）" \
    || { echo "FAIL: 共享端点未就绪: $(chain_field "$CH1" "c.get('endpoint_error','')")"; exit 1; }
wait_chain "$CH1" active 30
port_open "$EP_PORT" && echo "OK: 共享端点端口已监听" || { echo "FAIL: 共享端点端口未监听"; exit 1; }

echo ">> 分配用户到链（chain_ids → assignment，access_uuid 扇出到入口共享端点）"
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[],\"chain_ids\":[$CH1]}" >/dev/null
ACCESS_UUID=""
for _ in $(seq 1 15); do
    ACCESS_UUID="$(rpc_data GET /api/user/list | python3 -c "
import json,sys
u=next((x for x in json.load(sys.stdin) if x['id']==$USER_ID1), {})
ca=u.get('chain_assignments') or []
print(ca[0]['access_uuid'] if ca else '')")"
    [[ -n "$ACCESS_UUID" ]] && break
    sleep 1
done
[[ -n "$ACCESS_UUID" ]] || { echo "FAIL: 未取到链路 assignment"; exit 1; }
for _ in $(seq 1 15); do grep -q "$ACCESS_UUID" "$XRAY_CONFIG_A" && break; sleep 1; done
grep -q "$ACCESS_UUID" "$XRAY_CONFIG_A" && echo "OK: access_uuid 已扇出到入口共享端点（A）" \
    || { echo "FAIL: access_uuid 未扇出"; exit 1; }
! grep -q "$UUID1" "$XRAY_CONFIG_C" && echo "OK: 业务用户 UUID 不下发出口 xray（出口仅持隧道身份）" \
    || { echo "FAIL: 用户 UUID 泄漏到出口 xray"; exit 1; }
EP_RC="$(db "SELECT realized_config FROM shared_endpoints WHERE id=$EP_ID")"
EP_PUB="$(py "d['public_key']" "$EP_RC")"
EP_SID="$(py "d['short_id']" "$EP_RC")"
EP_SNAME="$(py "d['server_name']" "$EP_RC")"
EP_FLOW="$(py "d.get('flow') or ''" "$EP_RC")"
[[ -n "$EP_PUB" && "$(py "d['port']" "$EP_RC")" == "$EP_PORT" ]] \
    && echo "OK: 端点 realized 参数齐备（端口与链 entry_port 一致）" \
    || { echo "FAIL: 端点 realized_config: $EP_RC"; exit 1; }

echo ">> 订阅断言（共享端点地址:端口 + access_uuid + 端点密钥）"
# 订阅重发布已转异步（set-nodes 入队 + regenerator 去抖），轮询等待内容就绪。
SUB=""
for _ in $(seq 1 20); do
    SUB="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=clash")"
    grep -q "name: chain-a-vless-$EP_PORT" <<<"$SUB" && break
    sleep 0.5
done
echo "$SUB" | grep -q "name: chain-a-vless-$EP_PORT" \
    && echo "$SUB" | grep -q "server: 127.0.0.1" \
    && echo "$SUB" | grep -q "port: $EP_PORT" \
    && echo "$SUB" | grep -q "uuid: $ACCESS_UUID" \
    && echo "$SUB" | grep -q "public-key: $EP_PUB" \
    && echo "$SUB" | grep -q "short-id: $EP_SID" \
    && echo "OK: 订阅 YAML 链条目（命名/端点地址端口/access_uuid/端点密钥）" \
    || { echo "FAIL: 订阅内容"; echo "$SUB"; exit 1; }
[[ "$(echo "$SUB" | grep -cE '^[[:space:]]*- name: chain-')" == "1" && "$(echo "$SUB" | grep -c "chain-c-vless")" == "0" ]] \
    && echo "OK: 出口节点不作为单机条目出现" \
    || { echo "FAIL: 出口节点泄漏为单机条目"; echo "$SUB"; exit 1; }
LINKS="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=links" | base64 -d)"
echo "$LINKS" | grep -q "^vless://$ACCESS_UUID@127.0.0.1:$EP_PORT" \
    && echo "$LINKS" | grep -q "pbk=$EP_PUB" \
    && echo "OK: links 端点同构（vless:// access_uuid@端点地址:端口 + 端点公钥）" \
    || { echo "FAIL: links: $LINKS"; exit 1; }

echo ">> 真实流量断言（socks→A 共享端点→链 forward→reverse 隧道→C 出口→外网）"
if [[ "${CHAINS_SKIP_EXTERNAL:-0}" == "1" ]]; then
    echo ">> CHAINS_SKIP_EXTERNAL=1，跳过外网流量断言"
else
python3 - "$CLIENT_CONFIG" "$EP_PORT" "$ACCESS_UUID" "$EP_PUB" "$EP_SID" "$EP_SNAME" "$EP_FLOW" "$SOCKS_PORT" <<'PY'
import json, sys
path, port, uuid, pbk, sid, sname, flow, socks = sys.argv[1:9]
user = {"id": uuid, "encryption": "none"}
if flow: user["flow"] = flow
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": int(socks),
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": int(port), "users": [user]}]},
        "streamSettings": {"network": "tcp", "security": "reality", "realitySettings": {
            "serverName": sname, "publicKey": pbk, "shortId": sid, "fingerprint": "chrome"}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$CLIENT_CONFIG" >/dev/null || { echo "FAIL: 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$CLIENT_CONFIG" >"$WORK/client.log" 2>&1 &
XPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:$SOCKS_PORT" --max-time 8 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
[[ -n "$ok200" ]] && echo "OK: 端到端链路 200（client→入口→reverse 隧道→出口→外网）" \
    || { echo "FAIL: 链路未通"; tail -n 5 "$WORK/client.log"; exit 1; }
fi

echo ">> degraded：停 C 的 agent → 链 degraded → 重启恢复 active"
kill $APID_C; wait $APID_C 2>/dev/null || true; APID_C=""
wait_chain "$CH1" degraded 30 && echo "OK: 出口机离线 → 链 degraded"
start_agent_c
sleep 2
wait_chain "$CH1" active 30 && echo "OK: 出口机回归 → 链恢复 active"
if [[ "${CHAINS_SKIP_EXTERNAL:-0}" != "1" ]]; then
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:$SOCKS_PORT" --max-time 10 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && echo "OK: 恢复后链路仍通" || { echo "FAIL: 恢复后链路不通"; exit 1; }
fi

echo ">> 失败重试：入口端口占用 → forward 失败 → 释放后 retry（幂等）"
# 链2用 shadowsocks（无共享端点）：vless 链的入口端口由共享端点托管，占用失败落在
# 端点部署（链 degraded、端点自愈域）；无端点链的入口 forward 直接监听该端口，
# 占用即 forward piece 失败，可继续覆盖"链 piece 失败 → retry 只重放失败 piece"语义。
python3 -m http.server "$BLOCK_PORT" --bind 127.0.0.1 >/dev/null 2>&1 &
BLOCKPID=$!
sleep 0.5
CHAIN2="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"entry_port\":$BLOCK_PORT,\"node\":{\"protocol\":\"shadowsocks\",\"method\":\"aes-256-gcm\"}}")"
CH2="$(py "d['id']" "$CHAIN2")"
wait_chain "$CH2" failed 90
ERR2="$(chain_field "$CH2" "c['error']")"
echo "$ERR2" | grep -q "forward" && echo "OK: 链 failed 且错误定位到入口跳 forward piece（$ERR2）" \
    || { echo "FAIL: 失败定位: $ERR2"; exit 1; }
[[ "$(chain_field "$CH2" "c['hops'][0]['status']")" == "failed" ]] \
    && echo "OK: 入口跳 failed，其余 piece 不回滚" || { echo "FAIL: 跳状态"; exit 1; }
kill $BLOCKPID; wait $BLOCKPID 2>/dev/null || true; BLOCKPID=""
sleep 0.5
rpc_data POST /api/chain/retry "{\"chain_id\":$CH2}" >/dev/null
wait_chain "$CH2" active 60 && echo "OK: retry 只重放失败 piece → 链 active"

echo ">> 回归：编辑存量链路（原样参数）→ 不因自身端口占用误报冲突"
# vless 共享链：编辑仅改名，入口共享监听与出口节点均为自身占用，须放行
rpc_data POST /api/chain/edit "{\"chain_id\":$CH1,\"name\":\"链A回归\",\"hops\":[{\"server_id\":$AID},{\"server_id\":$CID}],\"node\":{\"protocol\":\"vless\"},\"traffic_multiplier\":\"1.000\"}" >/dev/null
wait_chain "$CH1" active 90 && echo "OK: vless 存量链编辑通过（excludeChainID 生效）"
# ss 明文链：entry_port 为自身 forward 占用，编辑原样保存须放行
rpc_data POST /api/chain/edit "{\"chain_id\":$CH2,\"name\":\"链B回归\",\"hops\":[{\"server_id\":$AID},{\"server_id\":$CID}],\"entry_port\":$BLOCK_PORT,\"node\":{\"protocol\":\"shadowsocks\",\"method\":\"aes-256-gcm\"},\"traffic_multiplier\":\"1.000\"}" >/dev/null
wait_chain "$CH2" active 60 && echo "OK: ss 存量链编辑通过"
[[ "$(chain_field "$CH2" "c['hops'][0]['forward_port']")" == "$BLOCK_PORT" ]] \
    && echo "OK: 重试后入口端口仍为用户指定的 $BLOCK_PORT" \
    || { echo "FAIL: 重试后端口漂移: $(chain_field "$CH2" "c['hops'][0]['forward_port']")"; exit 1; }
RETRY_CODE="$(rpc_code POST /api/chain/retry "{\"chain_id\":$CH2}")"
[[ "$RETRY_CODE" == "INVALID_ARGUMENT" ]] && echo "OK: 非 failed 状态 retry 幂等拒绝（INVALID_ARGUMENT）" \
    || { echo "FAIL: 重复 retry 返回 $RETRY_CODE"; exit 1; }

echo ">> UDP 中转管道：forward inbound 按出口协议分层"
CH1_HOP="$(chain_field "$CH1" "c['hops'][0]['id']")"
CH2_HOP="$(chain_field "$CH2" "c['hops'][0]['id']")"
python3 - "$XRAY_CONFIG_A" "$CH1_HOP" "$CH2_HOP" <<'PY'
import json, sys
cfg = json.load(open(sys.argv[1]))
def fwd(tag):
    return next((i for i in cfg["inbounds"] if i.get("tag") == tag), None)
vless_fwd = fwd(f"chainfwd_{sys.argv[2]}")
ss_fwd = fwd(f"chainfwd_{sys.argv[3]}")
assert vless_fwd and vless_fwd["settings"].get("network") == "tcp", vless_fwd
assert ss_fwd and ss_fwd["settings"].get("network") == "tcp,udp", ss_fwd
PY
echo "OK: vless 链管道 tcp / ss 链管道 tcp,udp"
# ss 链入口 UDP 层确在监听：同号 UDP 端口绑定应冲突
python3 -c "import socket; socket.socket(socket.AF_INET, socket.SOCK_DGRAM).bind(('0.0.0.0', $BLOCK_PORT))" \
    2>/dev/null && { echo "FAIL: ss 链入口 UDP $BLOCK_PORT 未被监听"; exit 1; } \
    || echo "OK: ss 链入口 UDP 层已监听（绑定冲突证明）"

echo ">> 链3（vmess+ws 中继，A=入口、C=出口）→ active → 真实流量"
CHAIN3="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"vmess\",\"network\":\"ws\",\"path\":\"/wsp\"}}")"
CH3="$(py "d['id']" "$CHAIN3")"
NID3="$(py "d['hops'][-1]['node_id']" "$CHAIN3")"
wait_chain "$CH3" active 90 && echo "OK: vmess+ws 中继链 active"
ENTRY3_PORT="$(chain_field "$CH3" "c['hops'][0]['forward_port']")"
[[ "$ENTRY3_PORT" != "0" && -n "$ENTRY3_PORT" ]] || { echo "FAIL: 链3 入口端口"; exit 1; }
# 非 vless 链无共享端点，不可经 chain_ids 分配（ValidateAssignableChains）；
# 订阅链条件 = 出口 service node 在用户 node_ids 内（sub.go chainSubscriptionItem），
# 条目凭据即用户自身 UUID（客户端经入口管道直连出口 vmess 入站）。
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[$NID3],\"chain_ids\":[$CH1]}" >/dev/null
# 订阅断言：vmess 链条目 network=ws + ws-opts、无 reality-opts
SUB3=""
for _ in $(seq 1 20); do
    SUB3="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=clash")"
    grep -q "type: vmess" <<<"$SUB3" && break
    sleep 0.5
done
python3 - "$SUB3" "$ENTRY3_PORT" "$UUID1" <<'PY'
import sys, yaml
doc = yaml.safe_load(sys.argv[1])
port, uuid = int(sys.argv[2]), sys.argv[3]
vm = next(p for p in doc["proxies"] if p["type"] == "vmess")
assert vm["network"] == "ws" and vm["port"] == port and vm["uuid"] == uuid, vm
assert vm.get("ws-opts", {}).get("path") == "/wsp", vm
assert "reality-opts" not in vm and not vm.get("tls"), vm
PY
echo "OK: 订阅 vmess+ws 条目（入口端口/用户 UUID/ws-opts/无 reality）"
if [[ "${CHAINS_SKIP_EXTERNAL:-0}" != "1" ]]; then
python3 - "$WORK/client-ws.json" "$ENTRY3_PORT" "$UUID1" <<'PY'
import json, sys
path, port, uuid = sys.argv[1], int(sys.argv[2]), sys.argv[3]
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11810,
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vmess",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": port,
                                "users": [{"id": uuid, "security": "auto"}]}]},
        "streamSettings": {"network": "ws", "security": "none",
                           "wsSettings": {"path": "/wsp"}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-ws.json" >/dev/null || { echo "FAIL: ws 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-ws.json" >"$WORK/client-ws.log" 2>&1 &
WSXPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11810" --max-time 8 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $WSXPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: vmess+ws 中继链路 200（client→入口管道→reverse 隧道→出口）" \
    || { echo "FAIL: vmess+ws 链路未通"; tail -n 5 "$WORK/client-ws.log"; exit 1; }
fi

if [[ "$HAS_VLESSENC" == "true" ]]; then
echo ">> 链4（vless+httpupgrade+Encryption 中继，共享端点入口）→ active → 真实流量"
CHAIN4="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"vless\",\"network\":\"httpupgrade\",\"path\":\"/hu\",\"encryption\":\"mlkem768\"}}")"
CH4="$(py "d['id']" "$CHAIN4")"
wait_chain "$CH4" active 90
for _ in $(seq 1 30); do
    [[ "$(chain_field "$CH4" "c.get('endpoint_status','')")" == "active" ]] && break
    sleep 1
done
EP4_PORT="$(chain_field "$CH4" "c['entry_port']")"
EP4_ID="$(chain_field "$CH4" "c['endpoint_id']")"
[[ -n "$EP4_PORT" && "$EP4_PORT" != "0" ]] || { echo "FAIL: 链4 端点未就绪: $(chain_field "$CH4" "c.get('endpoint_error','')")"; exit 1; }
wait_chain "$CH4" active 30
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[$NID3],\"chain_ids\":[$CH1,$CH4]}" >/dev/null
ACCESS_UUID4=""
for _ in $(seq 1 15); do
    ACCESS_UUID4="$(rpc_data GET /api/user/list | python3 -c "
import json,sys
u=next((x for x in json.load(sys.stdin) if x['id']==$USER_ID1), {})
ca=[a for a in (u.get('chain_assignments') or []) if a.get('chain_id')==$CH4]
print(ca[0]['access_uuid'] if ca else '')")"
    [[ -n "$ACCESS_UUID4" ]] && break
    sleep 1
done
[[ -n "$ACCESS_UUID4" ]] || { echo "FAIL: 未取到链4 assignment"; exit 1; }
EP4_RC="$(db "SELECT realized_config FROM shared_endpoints WHERE id=$EP4_ID")"
EP4_ENC="$(py "d.get('encryption') or ''" "$EP4_RC")"
[[ "$EP4_ENC" == mlkem768x25519plus.* ]] || { echo "FAIL: 链4 端点 encryption 缺失: $EP4_RC"; exit 1; }
if [[ "${CHAINS_SKIP_EXTERNAL:-0}" != "1" ]]; then
python3 - "$WORK/client-hu.json" "$EP4_PORT" "$ACCESS_UUID4" "$EP4_ENC" <<'PY'
import json, sys
path, port, uuid, enc = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11811,
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": port,
                                "users": [{"id": uuid, "encryption": enc}]}]},
        "streamSettings": {"network": "httpupgrade", "security": "none",
                           "httpupgradeSettings": {"path": "/hu"}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-hu.json" >/dev/null || { echo "FAIL: httpupgrade 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-hu.json" >"$WORK/client-hu.log" 2>&1 &
HUXPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11811" --max-time 8 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $HUXPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: vless+httpupgrade 中继链路 200（client→端点→加密隧道→出口）" \
    || { echo "FAIL: vless+httpupgrade 链路未通"; tail -n 5 "$WORK/client-hu.log"; exit 1; }
fi
else
    echo "SKIP: xray 缺 vlessenc，跳过链4（vless+httpupgrade）"
fi

echo ">> 链5（vless+tcp+tls 中继，共享端点 tls 自签 + 出口 tls pin 隧道）→ active → 真实流量"
CHAIN5="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"vless\",\"security\":\"tls\",\"flow\":\"none\"}}")"
CH5="$(py "d['id']" "$CHAIN5")"
wait_chain "$CH5" active 90
for _ in $(seq 1 30); do
    [[ "$(chain_field "$CH5" "c.get('endpoint_status','')")" == "active" ]] && break
    sleep 1
done
EP5_PORT="$(chain_field "$CH5" "c['entry_port']")"
EP5_ID="$(chain_field "$CH5" "c['endpoint_id']")"
[[ -n "$EP5_PORT" && "$EP5_PORT" != "0" ]] || { echo "FAIL: 链5 端点未就绪: $(chain_field "$CH5" "c.get('endpoint_error','')")"; exit 1; }
wait_chain "$CH5" active 30
# CH3（vmess）无共享端点，不可经 chain_ids 分配（ValidateAssignableChains）；其订阅条件由 node_ids 维持
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[$NID3],\"chain_ids\":[$CH1,$CH5]}" >/dev/null
ACCESS_UUID5=""
for _ in $(seq 1 15); do
    ACCESS_UUID5="$(rpc_data GET /api/user/list | python3 -c "
import json,sys
u=next((x for x in json.load(sys.stdin) if x['id']==$USER_ID1), {})
ca=[a for a in (u.get('chain_assignments') or []) if a.get('chain_id')==$CH5]
print(ca[0]['access_uuid'] if ca else '')")"
    [[ -n "$ACCESS_UUID5" ]] && break
    sleep 1
done
[[ -n "$ACCESS_UUID5" ]] || { echo "FAIL: 未取到链5 assignment"; exit 1; }
EP5_RC="$(db "SELECT realized_config FROM shared_endpoints WHERE id=$EP5_ID")"
EP5_SNI="$(py "d.get('sni') or ''" "$EP5_RC")"
EP5_PIN="$(py "d.get('cert_sha256') or ''" "$EP5_RC")"
[[ -n "$EP5_SNI" && "${#EP5_PIN}" == "64" ]] || { echo "FAIL: 链5 端点 tls realized 缺失: $EP5_RC"; exit 1; }
# 出口 realized 也是 tls（隧道段 outbound 的 pin 来源）
CH5_EXIT_SNI="$(chain_field "$CH5" "json.loads(c['hops'][-1].get('service_realized') or '{}').get('sni','')" 2>/dev/null || true)"
if [[ "${CHAINS_SKIP_EXTERNAL:-0}" != "1" ]]; then
python3 - "$WORK/client-tls-chain.json" "$EP5_PORT" "$ACCESS_UUID5" "$EP5_SNI" "$EP5_PIN" <<'PY'
import json, sys
path, port, uuid, sni, pin = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11813,
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": port,
                                "users": [{"id": uuid, "encryption": "none"}]}]},
        "streamSettings": {"network": "tcp", "security": "tls",
                           "tlsSettings": {"serverName": sni, "fingerprint": "chrome",
                                           "pinnedPeerCertSha256": pin}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-tls-chain.json" >/dev/null || { echo "FAIL: 链5 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-tls-chain.json" >"$WORK/client-tls-chain.log" 2>&1 &
TCXPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11813" --max-time 8 "$PROBE_URL" || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $TCXPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: vless tls 中继链路 200（client→tls 端点→pin 隧道段→tls 出口）" \
    || { echo "FAIL: vless tls 中继链路未通"; tail -n 5 "$WORK/client-tls-chain.log"; exit 1; }
fi

echo ">> 删链：链3（与链4，若创建）、链5、链2 与链1"
rpc_data POST /api/chain/delete "{\"chain_id\":$CH3}" >/dev/null
[[ "$HAS_VLESSENC" == "true" ]] && rpc_data POST /api/chain/delete "{\"chain_id\":$CH4}" >/dev/null
rpc_data POST /api/chain/delete "{\"chain_id\":$CH5}" >/dev/null
rpc_data POST /api/chain/delete "{\"chain_id\":$CH2}" >/dev/null
rpc_data POST /api/chain/delete "{\"chain_id\":$CH1}" >/dev/null
EXPECT_REMOVE=12
[[ "$HAS_VLESSENC" == "true" ]] && EXPECT_REMOVE=15
[[ "$(rpc_data GET /api/chain/list | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')" == "0" ]] \
    && echo "OK: 链行已消失" || { echo "FAIL: 链列表非空"; exit 1; }
for _ in $(seq 1 30); do
    [[ "$(db "SELECT COUNT(*) FROM commands WHERE type IN ('chain-hop.remove','node.remove') AND status != 'acked'")" == "0" ]] && break
    sleep 1
done
[[ "$(db "SELECT COUNT(*) FROM commands WHERE type='chain-hop.remove'")" == "$EXPECT_REMOVE" \
&& "$(db "SELECT COUNT(*) FROM commands WHERE type='chain-hop.remove' AND status='acked'")" == "$EXPECT_REMOVE" ]] \
    && echo "OK: chain-hop.remove 全部 acked（$EXPECT_REMOVE 件）" \
    || { echo "FAIL: chain-hop.remove 未全部 acked"; db "SELECT id,type,status,error,data FROM commands WHERE status='failed'"; exit 1; }
[[ "$(rpc_data GET /api/node/list | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')" == "0" ]] \
    && echo "OK: 出口业务节点已删除" || { echo "FAIL: 节点残留"; exit 1; }
sleep 1
! port_open "$ENTRY_PORT" && echo "OK: 链1 入口端口 $ENTRY_PORT 不再监听" \
    || { echo "FAIL: 入口端口仍监听"; exit 1; }
! port_open "$BLOCK_PORT" && echo "OK: 链2 入口端口 $BLOCK_PORT 不再监听" \
    || { echo "FAIL: 链2 入口端口仍监听"; exit 1; }
! grep -q "chainfwd_\|chainportal_" "$XRAY_CONFIG_A" && echo "OK: A 配置件已清空" \
    || { echo "FAIL: A 配置件残留"; exit 1; }
! grep -q "chainbr_" "$XRAY_CONFIG_C" && echo "OK: C bridge 配置件已清空" \
    || { echo "FAIL: C 配置件残留"; exit 1; }

echo "E2E-CHAINS PASS"
