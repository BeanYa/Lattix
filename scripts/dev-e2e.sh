#!/usr/bin/env bash
# 阶段 1 控制通道端到端验收（实施计划 1.6，设计文档 §2/§5/§11）：
#   hello 认证换发长期凭证 → 离线命令滞留 queued → 重连补发 → apply_result 回写状态机。
# 用法：scripts/dev-e2e.sh（依赖 python3 操作 sqlite）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'kill ${BPID:-} ${APID:-} 2>/dev/null; wait 2>/dev/null; rm -rf "$WORK"' EXIT

ADDR="127.0.0.1:18099"
BOOTSTRAP="bootstrap-test-token"

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

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1

echo ">> seed server (bootstrap token)"
sql "INSERT INTO servers (alias, token) VALUES ('dev01', '$BOOTSTRAP')" >/dev/null

echo ">> agent first connect (bootstrap token)"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" >"$WORK/agent.log" 2>&1 &
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

echo ">> agent goes offline; queue apply_node command"
kill $APID; wait $APID 2>/dev/null || true
sql "INSERT INTO nodes (id, server_id, config_template, status) VALUES (1, 1, '{}', 'applying')" >/dev/null
sql "INSERT INTO commands (server_id, type, payload) VALUES (1, 'apply_node', '{\"node_id\":1,\"config\":{\"protocol\":\"vless\",\"template\":[]},\"user_uuids\":[\"u1\"]}')" >/dev/null

echo ">> agent reconnects with long-term token (from state file)"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -state "$WORK/agent.state.json" >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2

grep -q "apply_node" "$WORK/agent.log" \
    && echo "OK: 离线命令已补发" \
    || { echo "FAIL: 未收到补发命令"; cat "$WORK/agent.log" "$WORK/backend.log"; exit 1; }

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

echo "E2E PASS"
