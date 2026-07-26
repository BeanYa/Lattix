#!/usr/bin/env bash
# 代理链与 NAT 支持端到端验收（设计文档 §21/§21.1）：
#   同机双 agent（A=direct 当入口、C=NAT 仅出口档 machine_type=nat/allowed_ports=[] 当出口）
#   → 建链（A=入口、C=出口，vless+reality 出口节点）→ 五阶段编排（apply_node→portal→bridge→forward）active
#   → 真实流量（本机 xray 客户端 socks→入口端口→reverse 隧道→出口→外网 200）
#   → 订阅 /sub/{token} 与 /sub/{token}/links（入口地址:端口 + 出口密钥/UUID）
#   → degraded（停 C 的 agent → 链 degraded → 重启恢复 active）
#   → 失败重试（入口端口占用构造 forward 失败 → 链 failed 定位到跳 → 释放端口 retry 恢复，
#     retry 非 failed 状态幂等拒绝）
#   → 删链（remove_chain_hop 全部 acked、出口节点删除、入口端口不再监听、链行消失）。
# 依赖：python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）。
# 外网断言默认开启，CHAINS_SKIP_EXTERNAL=1 跳过（离线环境）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18116"
API_A="127.0.0.1:14216"
API_C="127.0.0.1:14226"
SOCKS_PORT=11808        # 客户端 xray socks 入口
BLOCK_PORT=11809        # 失败重试用例：先占用后释放的入口端口
PROBE_URL="https://www.cloudflare.com/cdn-cgi/trace"
ADMIN_PASS="testpass123"
XRAY_CONFIG_A="$WORK/xray-a.json"
XRAY_CONFIG_C="$WORK/xray-c.json"
CLIENT_CONFIG="$WORK/client.json"
JAR="$WORK/cookies.txt"

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

api() {
    local args=(-s -b "$JAR" -c "$JAR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "http://$ADDR$2"
}
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

# port_open <port>：端口是否有进程监听（TCP 可连）。
port_open() { python3 -c "import socket,sys; s=socket.socket(); s.settimeout(1); sys.exit(0 if s.connect_ex(('127.0.0.1',$1))==0 else 1)"; }

# chain_field <id> <py-expr on c>：从链列表取指定链的字段。
chain_field() { api GET /api/chains | python3 -c "import json,sys; cs=json.load(sys.stdin); c=next((x for x in cs if x['id']==$1), None); print($2 if c else '')"; }

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
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" ${1:+-token "$1"} -state "$WORK/agent-a.state.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG_A" -xray-api "$API_A" -xray-runner exec \
        >"$WORK/agent-a.log" 2>&1 &
    APID_A=$!
}
start_agent_c() {
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" ${1:+-token "$1"} -state "$WORK/agent-c.state.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG_C" -xray-api "$API_C" -xray-runner exec \
        >"$WORK/agent-c.log" 2>&1 &
    APID_C=$!
}

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -resource "$WORK/resource" -install-script "$ROOT/scripts/install.sh" -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1

api POST /api/login "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}" >/dev/null

echo ">> 两台服务器：A（direct 入口）、C（NAT 仅出口档）"
RA="$(api POST /api/servers '{"country_code":"US","location":"Test","alias":"chain-a","address":"127.0.0.1"}')"
AID="$(echo "$RA" | py "d['server']['id']")"
BOOT_A="$(echo "$RA" | py "d['bootstrap_token']")"
RC="$(api POST /api/servers '{"country_code":"US","location":"Test","alias":"chain-c","address":"127.0.0.1","machine_type":"nat","allowed_ports":[]}')"
CID="$(echo "$RC" | py "d['server']['id']")"
BOOT_C="$(echo "$RC" | py "d['bootstrap_token']")"
[[ "$(echo "$RC" | py "d['server']['machine_type']")" == "nat" && "$(echo "$RC" | py "d['server']['allowed_ports']")" == "[]" ]] \
    && echo "OK: C 为 NAT 仅出口档（machine_type=nat，无端口段）" || { echo "FAIL: C 建档: $RC"; exit 1; }
start_agent_a "$BOOT_A"; start_agent_c "$BOOT_C"
sleep 2
[[ "$(api GET /api/servers | py "sum(1 for s in d if s['online'])")" == "2" ]] \
    && echo "OK: 双 agent 上线" || { echo "FAIL: agent 上线: $(api GET /api/servers)"; exit 1; }

echo ">> 用户 + 建链（A=入口、C=出口，vless 出口节点）"
U1="$(api POST /api/users '{"name":"chain-user"}')"
UUID1="$(echo "$U1" | py "d['uuid']")"
UID1="$(echo "$U1" | py "d['id']")"
SUB_TOKEN="$(echo "$U1" | py "d['sub_token']")"
CHAIN="$(api POST /api/chains "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"vless\"}}")"
CH1="$(echo "$CHAIN" | py "d['id']")"
NID1="$(echo "$CHAIN" | py "d['hops'][-1]['node_id']")"
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
api PUT "/api/users/$UID1/nodes" "{\"node_ids\":[$NID1]}" >/dev/null
for _ in $(seq 1 15); do grep -q "$UUID1" "$XRAY_CONFIG_C" && break; sleep 1; done
grep -q "$UUID1" "$XRAY_CONFIG_C" && echo "OK: 用户 UUID 已扇出到出口 xray" \
    || { echo "FAIL: UUID 未扇出"; exit 1; }
# 出口节点凭证（订阅/客户端共用 realized_config）
NODE_JSON="$(api GET /api/nodes | python3 -c "import json,sys; print(json.dumps(next(n for n in json.load(sys.stdin) if n['id']==$NID1)))")"
EXIT_PUB="$(echo "$NODE_JSON" | py "d['realized_config']['public_key']")"
EXIT_SID="$(echo "$NODE_JSON" | py "d['realized_config']['short_id']")"
EXIT_SNAME="$(echo "$NODE_JSON" | py "d['realized_config']['server_name']")"
EXIT_FLOW="$(echo "$NODE_JSON" | py "d['realized_config'].get('flow') or ''")"
EXIT_PORT="$(echo "$NODE_JSON" | py "d['realized_config']['port']")"

echo ">> 订阅断言（入口地址:端口 + 出口密钥/UUID）"
SUB="$(curl -s "http://$ADDR/sub/$SUB_TOKEN")"
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
LINKS="$(curl -s "http://$ADDR/sub/$SUB_TOKEN/links" | base64 -d)"
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
    || { echo "FAIL: 链路未通"; tail -n 5 "$WORK/client.log" "$XRAY_CONFIG_A" >/dev/null; tail -n 5 "$WORK/client.log"; exit 1; }
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
CHAIN2="$(api POST /api/chains "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"entry_port\":$BLOCK_PORT,\"node\":{\"protocol\":\"vless\"}}")"
CH2="$(echo "$CHAIN2" | py "d['id']")"
wait_chain "$CH2" failed 90
ERR2="$(chain_field "$CH2" "c['error']")"
echo "$ERR2" | grep -q "forward" && echo "OK: 链 failed 且错误定位到入口跳 forward piece（$ERR2）" \
    || { echo "FAIL: 失败定位: $ERR2"; exit 1; }
[[ "$(chain_field "$CH2" "c['hops'][0]['status']")" == "failed" ]] \
    && echo "OK: 入口跳 failed，其余 piece 不回滚" || { echo "FAIL: 跳状态"; exit 1; }
kill $BLOCKPID; wait $BLOCKPID 2>/dev/null || true; BLOCKPID=""
sleep 0.5
api POST "/api/chains/$CH2/retry" >/dev/null
wait_chain "$CH2" active 60 && echo "OK: retry 只重放失败 piece → 链 active"
[[ "$(chain_field "$CH2" "c['hops'][0]['forward_port']")" == "$BLOCK_PORT" ]] \
    && echo "OK: 重试后入口端口仍为用户指定的 $BLOCK_PORT" \
    || { echo "FAIL: 重试后端口漂移: $(chain_field "$CH2" "c['hops'][0]['forward_port']")"; exit 1; }
CODE="$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X POST "http://$ADDR/api/chains/$CH2/retry")"
[[ "$CODE" == "400" ]] && echo "OK: 非 failed 状态 retry 幂等拒绝（400）" \
    || { echo "FAIL: 重复 retry 返回 $CODE"; exit 1; }

echo ">> 删链：链2 与链1"
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X DELETE "http://$ADDR/api/chains/$CH2")" == "204" ]]
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X DELETE "http://$ADDR/api/chains/$CH1")" == "204" ]]
[[ "$(api GET /api/chains | py "len(d)")" == "0" ]] \
    && echo "OK: 链行已消失" || { echo "FAIL: 链列表非空"; exit 1; }
for _ in $(seq 1 20); do
    [[ "$(db "SELECT COUNT(*) FROM commands WHERE type IN ('remove_chain_hop','remove_node') AND status != 'acked'")" == "0" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT COUNT(*) FROM commands WHERE type='remove_chain_hop'")" == "6" \
&& "$(db "SELECT COUNT(*) FROM commands WHERE type='remove_chain_hop' AND status='acked'")" == "6" ]] \
    && echo "OK: remove_chain_hop 全部 acked（两链 × forward/portal/bridge）" \
    || { echo "FAIL: remove_chain_hop 未全部 acked"; db "SELECT type,status FROM commands"; exit 1; }
[[ "$(api GET /api/nodes | py "len(d)")" == "0" ]] \
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
