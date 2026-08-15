#!/usr/bin/env bash
# 代理链与 NAT 支持端到端验收（设计文档 §21/§21.1）：
#   同机双 agent（A=direct 当入口、C=NAT 仅出口档 machine_type=nat/allowed_ports=[] 当出口）
#   → 建链（A=入口、C=出口，vless+reality 出口节点）→ 五阶段编排 active
#   → 真实流量 → 订阅 → degraded → 失败重试 → 删链。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）。
# 外网断言默认开启，CHAINS_SKIP_EXTERNAL=1 跳过（离线环境）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

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
    kill ${BPID:-} ${APID_A:-} ${APID_C:-} ${XPID:-} ${BLOCKPID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG_A" 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG_C" 2>/dev/null || true
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
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" ${1:+-token "$1"} -state "$WORK/agent-a.state.json" \
        -settings "$WORK/agent-a.settings.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG_A" -xray-api "$API_A" -xray-runner exec \
        >>"$WORK/agent-a.log" 2>&1 &
    APID_A=$!
}
start_agent_c() {
    : > "$WORK/agent-c.log"
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" ${1:+-token "$1"} -state "$WORK/agent-c.state.json" \
        -settings "$WORK/agent-c.settings.json" \
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
    && echo "OK: 入口 forward 端口 $ENTRY_PORT、portal 端口 $PORTAL_PORT 已回执" \
    || { echo "FAIL: 端口回执: $HOPS"; exit 1; }
port_open "$ENTRY_PORT" && echo "OK: 入口端口已监听" || { echo "FAIL: 入口端口未监听"; exit 1; }
grep -q "chainportal_" "$XRAY_CONFIG_A" && grep -q "chainfwd_" "$XRAY_CONFIG_A" \
    && echo "OK: A 配置含 portal+forward 配置件" || { echo "FAIL: A 配置件"; exit 1; }
grep -q "chainbr_" "$XRAY_CONFIG_C" && echo "OK: C 配置含 bridge 配置件" || { echo "FAIL: C 配置件"; exit 1; }

echo ">> 分配用户到出口节点（UUID 扇出到 C）"
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID1,\"node_ids\":[$NID1]}" >/dev/null
for _ in $(seq 1 15); do grep -q "$UUID1" "$XRAY_CONFIG_C" && break; sleep 1; done
grep -q "$UUID1" "$XRAY_CONFIG_C" && echo "OK: 用户 UUID 已扇出到出口 xray" \
    || { echo "FAIL: UUID 未扇出"; exit 1; }
NODE_JSON="$(rpc_data GET /api/node/list | python3 -c "import json,sys; print(json.dumps(next(n for n in json.load(sys.stdin) if n['id']==$NID1)))")"
EXIT_PUB="$(py "d['realized_config']['public_key']" "$NODE_JSON")"
EXIT_SID="$(py "d['realized_config']['short_id']" "$NODE_JSON")"
EXIT_SNAME="$(py "d['realized_config']['server_name']" "$NODE_JSON")"
EXIT_FLOW="$(py "d['realized_config'].get('flow') or ''" "$NODE_JSON")"
EXIT_PORT="$(py "d['realized_config']['port']" "$NODE_JSON")"

echo ">> 订阅断言（入口地址:端口 + 出口密钥/UUID）"
# 订阅重发布已转异步（set-nodes 入队 + regenerator 去抖），轮询等待内容就绪。
SUB=""
for _ in $(seq 1 20); do
    SUB="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=clash")"
    grep -q "name: chain-a-vless-$ENTRY_PORT" <<<"$SUB" && break
    sleep 0.5
done
echo "$SUB" | grep -q "name: chain-a-vless-$ENTRY_PORT" \
    && echo "$SUB" | grep -q "server: 127.0.0.1" \
    && echo "$SUB" | grep -q "port: $ENTRY_PORT" \
    && echo "$SUB" | grep -q "uuid: $UUID1" \
    && echo "$SUB" | grep -q "public-key: $EXIT_PUB" \
    && echo "$SUB" | grep -q "short-id: $EXIT_SID" \
    && echo "OK: 订阅 YAML 链条目（命名/入口地址端口/出口密钥）" \
    || { echo "FAIL: 订阅内容"; echo "$SUB"; exit 1; }
[[ "$(echo "$SUB" | grep -cE '^[[:space:]]*- name: chain-')" == "1" && "$(echo "$SUB" | grep -c "chain-c-vless")" == "0" ]] \
    && echo "OK: 出口节点不作为单机条目出现" \
    || { echo "FAIL: 出口节点泄漏为单机条目"; echo "$SUB"; exit 1; }
LINKS="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=links" | base64 -d)"
echo "$LINKS" | grep -q "^vless://$UUID1@127.0.0.1:$ENTRY_PORT" \
    && echo "$LINKS" | grep -q "pbk=$EXIT_PUB" \
    && echo "OK: links 端点同构（vless:// 入口地址:端口 + 出口公钥）" \
    || { echo "FAIL: links: $LINKS"; exit 1; }

echo ">> 真实流量断言（socks→A 入口→reverse 隧道→C 出口→外网）"
if [[ "${CHAINS_SKIP_EXTERNAL:-0}" == "1" ]]; then
    echo ">> CHAINS_SKIP_EXTERNAL=1，跳过外网流量断言"
else
python3 - "$CLIENT_CONFIG" "$ENTRY_PORT" "$UUID1" "$EXIT_PUB" "$EXIT_SID" "$EXIT_SNAME" "$EXIT_FLOW" "$SOCKS_PORT" <<'PY'
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
python3 -m http.server "$BLOCK_PORT" --bind 127.0.0.1 >/dev/null 2>&1 &
BLOCKPID=$!
sleep 0.5
CHAIN2="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"entry_port\":$BLOCK_PORT,\"node\":{\"protocol\":\"vless\"}}")"
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
[[ "$(chain_field "$CH2" "c['hops'][0]['forward_port']")" == "$BLOCK_PORT" ]] \
    && echo "OK: 重试后入口端口仍为用户指定的 $BLOCK_PORT" \
    || { echo "FAIL: 重试后端口漂移: $(chain_field "$CH2" "c['hops'][0]['forward_port']")"; exit 1; }
RETRY_CODE="$(rpc_code POST /api/chain/retry "{\"chain_id\":$CH2}")"
[[ "$RETRY_CODE" == "INVALID_ARGUMENT" ]] && echo "OK: 非 failed 状态 retry 幂等拒绝（INVALID_ARGUMENT）" \
    || { echo "FAIL: 重复 retry 返回 $RETRY_CODE"; exit 1; }

echo ">> 删链：链2 与链1"
rpc_data POST /api/chain/delete "{\"chain_id\":$CH2}" >/dev/null
rpc_data POST /api/chain/delete "{\"chain_id\":$CH1}" >/dev/null
[[ "$(rpc_data GET /api/chain/list | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')" == "0" ]] \
    && echo "OK: 链行已消失" || { echo "FAIL: 链列表非空"; exit 1; }
for _ in $(seq 1 30); do
    [[ "$(db "SELECT COUNT(*) FROM commands WHERE type IN ('chain-hop.remove','node.remove') AND status != 'acked'")" == "0" ]] && break
    sleep 1
done
[[ "$(db "SELECT COUNT(*) FROM commands WHERE type='chain-hop.remove'")" == "6" \
&& "$(db "SELECT COUNT(*) FROM commands WHERE type='chain-hop.remove' AND status='acked'")" == "6" ]] \
    && echo "OK: chain-hop.remove 全部 acked（两链 × forward/portal/bridge）" \
    || { echo "FAIL: chain-hop.remove 未全部 acked"; db "SELECT type,status FROM commands"; exit 1; }
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
