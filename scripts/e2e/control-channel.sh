#!/usr/bin/env bash
# 阶段 1 控制通道端到端验收（实施计划 1.6，设计文档 §2/§5/§11）：
#   hello 认证换发长期凭证 → 离线命令滞留 queued → 重连补发 → apply_result 回写状态机。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 用法：scripts/e2e/control-channel.sh（依赖 python3 操作 sqlite）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'kill ${BPID:-} ${APID:-} 2>/dev/null; wait 2>/dev/null; rm -rf "$WORK"' EXIT

ADDR="127.0.0.1:18099"
JAR="$WORK/cookies.txt"
CSRF=""

echo ">> build"
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)

sql() { python3 - "$WORK/lattix.db" "$1" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
cur = db.execute(sys.argv[2])
db.commit()
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

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

echo ">> seed server (via RPC)"
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
SRV="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"dev01","address":"127.0.0.1"}')"
BOOTSTRAP="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["bootstrap_token"])' "$SRV")"

echo ">> agent first connect (bootstrap token)"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -settings "$WORK/agent.settings.json" >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2

grep -q "authenticated as server 1" "$WORK/agent.log" || { echo "FAIL: agent not authenticated"; cat "$WORK/agent.log" "$WORK/backend.log"; exit 1; }
LONG_TOKEN="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["token"])' "$WORK/agent.state.json")"
DB_TOKEN="$(sql "SELECT token FROM servers WHERE id = 1")"
if [[ -n "$LONG_TOKEN" && "$LONG_TOKEN" == "$DB_TOKEN" && "$DB_TOKEN" != "$BOOTSTRAP" ]]; then
    echo "OK: bootstrap token 已换发长期凭证"
else
    echo "FAIL: token 未换发 (agent=$LONG_TOKEN db=$DB_TOKEN)"; exit 1
fi

echo ">> agent goes offline; queue node.apply command"
kill $APID; wait $APID 2>/dev/null || true
sql "INSERT INTO nodes (id, server_id, config_template, status) VALUES (1, 1, '{}', 'applying')" >/dev/null
python3 - "$WORK/lattix.db" <<'PY'
import json, sqlite3, secrets, sys
con = sqlite3.connect(sys.argv[1])
payload = {"node_id":1,"config":{"protocol":"vless","template":[]},"user_uuids":["u1"]}
con.execute("INSERT INTO commands (request_id, trace_id, server_id, type, data) VALUES (?, ?, 1, 'node.apply', ?)",
    (secrets.token_hex(16), secrets.token_hex(16), json.dumps(payload)))
con.commit()
PY

echo ">> agent reconnects with long-term token (from state file)"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -state "$WORK/agent.state.json" \
    -settings "$WORK/agent.settings.json" >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2

grep -q "node.apply" "$WORK/agent.log" \
    && echo "OK: 离线命令已补发" \
    || { echo "FAIL: 未收到补发命令"; cat "$WORK/agent.log" "$WORK/backend.log"; exit 1; }

DB_TOKEN2="$(sql "SELECT token FROM servers WHERE id = 1")"
[[ "$DB_TOKEN2" == "$LONG_TOKEN" ]] \
    && echo "OK: 长期 token 未再轮换（仅 bootstrap 换发，§5）" \
    || { echo "FAIL: token 被重复换发"; exit 1; }

sleep 1
CMD_STATUS="$(sql "SELECT status FROM commands WHERE id = 1")"
[[ "$CMD_STATUS" == "failed" ]] \
    && echo "OK: 命令状态已回写 failed" \
    || { echo "FAIL: command status=$CMD_STATUS"; exit 1; }

NODE_ROW="$(sql "SELECT status || '|' || COALESCE(error, '') FROM nodes WHERE id = 1")"
# 阶段 2 起 apply 走真实流水线（dev 环境无 /usr/local/bin/xray，必然失败），
# 本脚本聚焦控制通道：只要求状态机推进到 failed 且带错误详情。
[[ "$NODE_ROW" == failed\|?* ]] \
    && echo "OK: 节点状态机已回写 failed + error" \
    || { echo "FAIL: node=$NODE_ROW"; exit 1; }

echo ">> sent 未终态命令重连重置重发（§2）"
python3 - "$WORK/lattix.db" <<'PY'
import json, sqlite3, secrets, sys
con = sqlite3.connect(sys.argv[1])
payload = {"uuid":"u9","nodes":{"node_1":{"protocol":"vless"}}}
con.execute("INSERT INTO commands (request_id, trace_id, server_id, type, data, status) VALUES (?, ?, 1, 'user.add', ?, 'sent')",
    (secrets.token_hex(16), secrets.token_hex(16), json.dumps(payload)))
con.commit()
PY
kill $APID; wait $APID 2>/dev/null || true
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -state "$WORK/agent.state.json" \
    -settings "$WORK/agent.settings.json" >>"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2
grep -q "user.add" "$WORK/agent.log" \
    && echo "OK: sent 命令已重置重发" \
    || { echo "FAIL: sent 命令未重发"; cat "$WORK/agent.log"; exit 1; }

echo "E2E PASS"
