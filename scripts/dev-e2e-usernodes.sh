#!/usr/bin/env bash
# 逐节点用户分配端到端验收（设计文档 §16，默认全关）：
#   新建用户/节点默认无关联（订阅为空、inbounds 无用户）→ PUT 分配后增量 add_user →
#   订阅仅含分配节点 → 取消分配后 remove_user → 存量库迁移（隐含全对全补全关联）。
# 依赖：python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18103"
API="127.0.0.1:14204"
BOOTSTRAP="bootstrap-usernodes-test"
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

api() {
    local args=(-s -b "$JAR" -c "$JAR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "http://$ADDR$2"
}

# clients_of <node-id>：输出该节点 inbound 的 clients email 列表。
clients_of() {
    python3 - "$XRAY_CONFIG" "$1" <<'PY'
import json, sys
cfg = json.load(open(sys.argv[1]))
for ib in cfg.get("inbounds", []):
    if ib.get("tag") == f"node_{sys.argv[2]}":
        for c in ib.get("settings", {}).get("clients", []):
            print(c.get("email", ""))
PY
}

# wait_clients <node-id> <uuid> <present|absent>
wait_clients() {
    for _ in $(seq 1 30); do
        found=false
        while IFS= read -r email; do [[ "$email" == "$2" ]] && found=true; done < <(clients_of "$1")
        [[ "$3" == "present" && "$found" == "true" ]] && return 0
        [[ "$3" == "absent" && "$found" == "false" ]] && return 0
        sleep 0.5
    done
    echo "FAIL: 节点 $1 用户 $2 未达 $3 状态"; return 1
}

sub_count() { curl -s "http://$ADDR/sub/$1" | grep -c 'server: ' || true; }

echo ">> start backend & agent"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1
db "INSERT INTO servers (alias, token) VALUES ('un01', '$BOOTSTRAP')" >/dev/null
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 1.5

echo ">> 2 用户 + 2 节点（默认全关）"
api POST /api/login '{"username":"admin","password":"lattix-admin"}' >/dev/null
U1="$(api POST /api/users '{"name":"u1"}')"; U2="$(api POST /api/users '{"name":"u2"}')"
UUID1="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$U1")"
UUID2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$U2")"
TOK1="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$U1")"
TOK2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$U2")"
N1="$(api POST /api/nodes '{"server_id":1,"protocol":"vless"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
N2="$(api POST /api/nodes '{"server_id":1,"protocol":"vless"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
for _ in $(seq 1 40); do
    a="$(db "SELECT count(*) FROM nodes WHERE status='active'")"
    [[ "$a" == "2" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT count(*) FROM nodes WHERE status='active'")" == "2" ]] || { echo "FAIL: 节点未全部 active"; exit 1; }
[[ "$(sub_count "$TOK1")" == "0" && "$(sub_count "$TOK2")" == "0" ]] \
    && echo "OK: 默认全关，新用户订阅为空" \
    || { echo "FAIL: 默认应为空订阅"; exit 1; }
[[ -z "$(clients_of "$N1")" && -z "$(clients_of "$N2")" ]] \
    && echo "OK: 新节点 inbounds 无用户" \
    || { echo "FAIL: 新节点不应带用户"; exit 1; }

echo ">> u1 分配节点 $N1"
api PUT "/api/users/1/nodes" "{\"node_ids\":[$N1]}" >/dev/null
wait_clients "$N1" "$UUID1" present && echo "OK: 增量 add_user 仅落到分配的节点"
[[ -z "$(clients_of "$N2")" ]] || { echo "FAIL: 未分配节点 $N2 不应有 u1"; exit 1; }
[[ "$(sub_count "$TOK1")" == "1" ]] && echo "OK: u1 订阅含 1 个节点" || { echo "FAIL: u1 订阅数异常"; exit 1; }

echo ">> u2 分配节点 $N1,$N2"
api PUT "/api/users/2/nodes" "{\"node_ids\":[$N1,$N2]}" >/dev/null
wait_clients "$N1" "$UUID2" present
wait_clients "$N2" "$UUID2" present
[[ "$(sub_count "$TOK2")" == "2" ]] && echo "OK: u2 订阅含 2 个节点" || { echo "FAIL: u2 订阅数异常"; exit 1; }

echo ">> u1 取消全部分配"
api PUT "/api/users/1/nodes" '{"node_ids":[]}' >/dev/null
wait_clients "$N1" "$UUID1" absent && echo "OK: 取消分配后 remove_user 生效"
[[ "$(sub_count "$TOK1")" == "0" ]] && echo "OK: u1 订阅回到空" || { echo "FAIL: u1 订阅应为空"; exit 1; }
[[ "$(sub_count "$TOK2")" == "2" ]] || { echo "FAIL: u2 不应受影响"; exit 1; }

echo ">> 存量库迁移（隐含全对全 → 补全 user_nodes）"
python3 - "$WORK/old.db" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
con.executescript("""
CREATE TABLE servers (id INTEGER PRIMARY KEY AUTOINCREMENT, alias TEXT NOT NULL, token TEXT NOT NULL UNIQUE,
    last_seen_at DATETIME, xray_version TEXT, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, uuid TEXT NOT NULL UNIQUE,
    sub_token TEXT NOT NULL UNIQUE, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE nodes (id INTEGER PRIMARY KEY AUTOINCREMENT, server_id INTEGER NOT NULL, protocol TEXT NOT NULL DEFAULT 'vless',
    port INTEGER, config_template TEXT NOT NULL, realized_config TEXT, status TEXT NOT NULL DEFAULT 'pending',
    error TEXT, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE commands (id INTEGER PRIMARY KEY AUTOINCREMENT, server_id INTEGER NOT NULL, type TEXT NOT NULL,
    payload TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'queued', attempts INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
INSERT INTO servers (id, alias, token) VALUES (1, 'old', 'tok');
INSERT INTO users (name, uuid, sub_token) VALUES ('a','ua','ta'), ('b','ub','tb');
INSERT INTO nodes (server_id, config_template) VALUES (1, '{}'), (1, '{}'), (1, '{}');
""")
con.commit()
PY
"$WORK/backend" -addr "127.0.0.1:18104" -db "$WORK/old.db" >"$WORK/backend-old.log" 2>&1 &
OLDPID=$!
sleep 1.5
kill $OLDPID 2>/dev/null || true
PAIRS="$(python3 -c 'import sqlite3,sys;print(sqlite3.connect(sys.argv[1]).execute("SELECT count(*) FROM user_nodes").fetchone()[0])' "$WORK/old.db")"
[[ "$PAIRS" == "6" ]] && echo "OK: 存量 2 用户 × 3 节点关联已补全" || { echo "FAIL: 迁移后关联数 $PAIRS ≠ 6"; exit 1; }

echo "E2E-USERNODES PASS"
