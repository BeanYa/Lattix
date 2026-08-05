#!/usr/bin/env bash
# 分组端到端验收（2026-08-05 分组设计）：
#   链路分组（共享入口链路 + 外部订阅，外部订阅整体原子）→ 用户分组（成员订阅由分组派生，
#   遮蔽直接分配）→ 分组变更/外部订阅同步/链路重发布自动触发关联用户订阅重生成。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18203"
API_A="127.0.0.1:14206"
API_C="127.0.0.1:14207"
XRAY_CONFIG_A="$WORK/xray-a.json"
XRAY_CONFIG_C="$WORK/xray-c.json"
JAR="$WORK/cookies.txt"
CSRF=""

cleanup() {
    kill ${BPID:-} ${APID_A:-} ${APID_C:-} ${HTTPID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG_A" 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG_C" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build"
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)

# rpc_raw <method> <path> [body]：RPC 请求（POST 自动附随机 Idempotency-Key 与 CSRF 令牌）。
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

# rpc_data：解包 RPC 信封输出 data（code 非 OK/ACCEPTED 立即失败）。
rpc_data() {
    rpc_raw "$@" | python3 -c '
import json,sys
value=json.load(sys.stdin)
if value["code"] not in ("OK","ACCEPTED"):
    raise SystemExit(f"RPC failed: {value}")
print(json.dumps(value["data"], separators=(",",":")))
'
}

# py <expr> <data-json>：从 data JSON 取值，d 为 data。
py() { python3 -c "import json,sys; d=json.loads(sys.argv[1]); print($1)" "$2"; }

# wait_chain <chain-id> <status> <max-seconds>
wait_chain() {
    for _ in $(seq 1 "$3"); do
        st="$(rpc_data GET /api/chain/list | python3 -c "import json,sys; cs=json.load(sys.stdin); print(next(c['status'] for c in cs if c['id']==$1))" 2>/dev/null || echo pending)"
        [[ "$st" == "$2" ]] && return 0
        sleep 1
    done
    echo "FAIL: 链 $1 未达 $2"; return 1
}

# sub_count <token>：clash 格式订阅的节点行数。
sub_count() { curl -s "http://$ADDR/sub/$1?format=clash" | grep -c 'server: ' || true; }

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }

echo ">> 两台服务器 A（入口）/ C（出口）+ 两个 agent"
RA="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"grp-a","address":"127.0.0.1"}')"
AID="$(py "d['server']['id']" "$RA")"
BOOT_A="$(py "d['bootstrap_token']" "$RA")"
RC="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"grp-c","address":"127.0.0.1"}')"
CID="$(py "d['server']['id']" "$RC")"
BOOT_C="$(py "d['bootstrap_token']" "$RC")"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOT_A" -state "$WORK/agent-a.state.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG_A" -xray-api "$API_A" -xray-runner exec >"$WORK/agent-a.log" 2>&1 &
APID_A=$!
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOT_C" -state "$WORK/agent-c.state.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG_C" -xray-api "$API_C" -xray-runner exec >"$WORK/agent-c.log" 2>&1 &
APID_C=$!
sleep 2

echo ">> 用户 u1（入组）/ u2（不入组）+ 建链（A=入口、C=出口，vless → 自动共享入口）"
U1="$(rpc_data POST /api/user/create '{"name":"grp-member"}')"
U2="$(rpc_data POST /api/user/create '{"name":"grp-outside"}')"
UID1="$(py "d['id']" "$U1")"; UID2="$(py "d['id']" "$U2")"
TOK1="$(py "d['sub_token']" "$U1")"; TOK2="$(py "d['sub_token']" "$U2")"
CHAIN="$(rpc_data POST /api/chain/create "{\"entry\":{\"server_id\":$AID},\"exit\":{\"server_id\":$CID},\"node\":{\"protocol\":\"vless\"}}")"
CH1="$(py "d['id']" "$CHAIN")"
[[ "$CH1" != "" ]] || { echo "FAIL: 建链: $CHAIN"; exit 1; }
# 等待链 active（五阶段编排完成）
wait_chain "$CH1" active 90 || { echo "FAIL: 链未 active"; exit 1; }

echo ">> 外部订阅（本地静态订阅文件，2 个节点）"
EXT_FILE="$WORK/sub-ext.txt"
VMESS_A="vmess://eyJhZGQiOiJleHQtYS5leGFtcGxlLmNvbSIsInBvcnQiOjQ0MywiaWQiOiJ4LXV1aWQtYSIsIm5ldCI6InRjcCIsInBzIjoiZXh0LWEifQ=="
VMESS_B="vmess://eyJhZGQiOiJleHQtYi5leGFtcGxlLmNvbSIsInBvcnQiOjQ0MywiaWQiOiJ4LXV1aWQtYiIsIm5ldCI6InRjcCIsInBzIjoiZXh0LWIifQ=="
printf '%s\n' "$VMESS_A" "$VMESS_B" > "$EXT_FILE"
python3 -m http.server 18080 -d "$WORK" >"$WORK/http.log" 2>&1 &
HTTPID=$!
for _ in $(seq 1 20); do curl -fsS "http://127.0.0.1:18080/sub-ext.txt" >/dev/null 2>&1 && break; sleep 0.2; done
# create 端点按设计仅允许 https 且拒绝本机/内网地址（防 SSRF），本机夹具直接落库；
# sync/list 仍走完整 API 路径。
EXTID="$(python3 - "$WORK/lattix.db" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1], timeout=10)
cur = con.execute("INSERT INTO external_subscriptions (name, url, auto_update) VALUES ('机场X', 'http://127.0.0.1:18080/sub-ext.txt', 0)")
con.commit()
print(cur.lastrowid)
PY
)"
rpc_data POST /api/external-subscription/sync "{\"id\":$EXTID}" >/dev/null
sleep 1
NODE_COUNT="$(rpc_data GET /api/external-subscription/list | python3 -c "import json,sys; subs=json.load(sys.stdin); print(next(s['node_count'] for s in subs if s['id']==$EXTID))")"
[[ "$NODE_COUNT" == "2" ]] && echo "OK: 外部订阅同步 2 节点" || { echo "FAIL: 外部订阅节点数=$NODE_COUNT"; exit 1; }

echo ">> 链路分组（链 + 外部订阅）"
LG="$(rpc_data POST /api/link-group/create "{\"name\":\"旗舰线路\",\"chain_ids\":[$CH1],\"external_subscriptions\":[{\"subscription_id\":$EXTID,\"mode\":\"stack\"}]}")"
LGID="$(py "d['id']" "$LG")"
echo ">> 用户分组（u1 入组）"
UG="$(rpc_data POST /api/user-group/create "{\"name\":\"青铜会员\",\"user_ids\":[$UID1],\"link_group_ids\":[$LGID]}")"
UGID="$(py "d['id']" "$UG")"
sleep 2

echo ">> u1 订阅 = 1 链 + 2 外部节点 = 3；u2（无分配）订阅 = 0"
[[ "$(sub_count "$TOK1")" == "3" ]] && echo "OK: 组用户订阅 3 条" || { echo "FAIL: u1 订阅数 $(sub_count "$TOK1")"; exit 1; }
[[ "$(sub_count "$TOK2")" == "0" ]] && echo "OK: 非组用户不受影响" || { echo "FAIL: u2 订阅数 $(sub_count "$TOK2")"; exit 1; }

echo ">> 直接分配被遮蔽：给 u1 直接分配链 + 外部订阅，订阅不变"
rpc_data POST /api/user/set-nodes "{\"user_id\":$UID1,\"node_ids\":[]}" >/dev/null
rpc_data POST /api/user/set-external-subscriptions "{\"user_id\":$UID1,\"items\":[{\"subscription_id\":$EXTID,\"mode\":\"stack\"}]}" >/dev/null
sleep 2
[[ "$(sub_count "$TOK1")" == "3" ]] && echo "OK: 直接分配被遮蔽" || { echo "FAIL: 遮蔽失败，订阅数 $(sub_count "$TOK1")"; exit 1; }

echo ">> 原子性：链路分组移除外部订阅 → u1 外部节点消失（2 条链条目→1 条）"
rpc_data POST /api/link-group/update "{\"id\":$LGID,\"name\":\"旗舰线路\",\"chain_ids\":[$CH1],\"external_subscriptions\":[]}" >/dev/null
sleep 2
[[ "$(sub_count "$TOK1")" == "1" ]] && echo "OK: 外部订阅原子移除" || { echo "FAIL: 移除后订阅数 $(sub_count "$TOK1")"; exit 1; }

echo ">> 恢复分组内外部订阅 → u1 订阅回到 3（分组变更触发重发布）"
rpc_data POST /api/link-group/update "{\"id\":$LGID,\"name\":\"旗舰线路\",\"chain_ids\":[$CH1],\"external_subscriptions\":[{\"subscription_id\":$EXTID,\"mode\":\"stack\"}]}" >/dev/null
sleep 2
[[ "$(sub_count "$TOK1")" == "3" ]] || { echo "FAIL: 恢复后订阅数 $(sub_count "$TOK1")"; exit 1; }

echo ">> 外部订阅内容变更同步 → 分组用户 u1 订阅自动重发布（3→2）"
printf '%s\n' "$VMESS_A" > "$EXT_FILE"
rpc_data POST /api/external-subscription/sync "{\"id\":$EXTID}" >/dev/null
sleep 2
[[ "$(sub_count "$TOK1")" == "2" ]] && echo "OK: 外部订阅内容变更同步后分组用户订阅自动重发布（3→2）" || { echo "FAIL: 内容变更同步后订阅数 $(sub_count "$TOK1")"; exit 1; }
printf '%s\n' "$VMESS_A" "$VMESS_B" > "$EXT_FILE"
rpc_data POST /api/external-subscription/sync "{\"id\":$EXTID}" >/dev/null
sleep 2
[[ "$(sub_count "$TOK1")" == "3" ]] && echo "OK: 外部订阅内容还原同步后分组用户订阅恢复（2→3）" || { echo "FAIL: 内容还原同步后订阅数 $(sub_count "$TOK1")"; exit 1; }

echo ">> 清空 u1 直接分配（遮蔽测试遗留的外部订阅）→ 删除用户分组 → 恢复直接分配（空 → 订阅 0）"
rpc_data POST /api/user/set-external-subscriptions "{\"user_id\":$UID1,\"items\":[]}" >/dev/null
sleep 1
rpc_data POST /api/user-group/delete "{\"id\":$UGID}" >/dev/null
sleep 2
[[ "$(sub_count "$TOK1")" == "0" ]] && echo "OK: 删除用户分组后恢复直接分配" || { echo "FAIL: 删除分组后订阅数 $(sub_count "$TOK1")"; exit 1; }

echo ">> 用户硬约束：全部操作后订阅地址不变（sub_token 原样）"
TOK1_LATER="$(rpc_data GET /api/user/list | python3 -c "import json,sys; users=json.load(sys.stdin); print(next(u['sub_token'] for u in users if u['id']==$UID1))")"
[[ "$TOK1_LATER" == "$TOK1" ]] && echo "OK: u1 订阅地址全程不变" || { echo "FAIL: u1 sub_token 变化: $TOK1 -> $TOK1_LATER"; exit 1; }
TOK2_LATER="$(rpc_data GET /api/user/list | python3 -c "import json,sys; users=json.load(sys.stdin); print(next(u['sub_token'] for u in users if u['id']==$UID2))")"
[[ "$TOK2_LATER" == "$TOK2" ]] && echo "OK: u2 订阅地址不变" || { echo "FAIL: u2 sub_token 变化: $TOK2 -> $TOK2_LATER"; exit 1; }

echo "E2E-GROUPS PASS"
