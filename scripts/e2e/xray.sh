#!/usr/bin/env bash
# 阶段 2 apply 流水线端到端验收（实施计划 2.6，设计文档 §6/§7）：
#   apply_node（含热操作失败的重启兜底）→ 节点 active + realized_config →
#   add_user/remove_user 零重启热操作（xray PID 不变）→ remove_node 端口释放 →
#   非法模板 → 节点 failed + 错误详情。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：python3、curl、openssl、本机 xray 二进制（默认 ~/.cache/lattix-dev/xray-core/xray，XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18098"
API="127.0.0.1:14200"
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

# wait_cmd <id>：轮询命令进入终态（acked/failed），输出状态。
wait_cmd() {
    for _ in $(seq 1 40); do
        s="$(db "SELECT status FROM commands WHERE id=$1")"
        case "$s" in acked|failed) echo "$s"; return 0;; esac
        sleep 0.5
    done
    echo "TIMEOUT"; return 1
}

port_open() { python3 -c "import socket,sys; s=socket.socket(); sys.exit(0 if s.connect_ex(('127.0.0.1', int(sys.argv[1])))==0 else 1)" "$1"; }

xray_pid() { pgrep -f "xray run -config $XRAY_CONFIG" | head -1; }

start_agent() {
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
        -settings "$WORK/agent.settings.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
        >"$WORK/agent.log" 2>&1 &
    APID=$!
    sleep 1.5
}

# bounce_agent：重启 agent 触发补发（等价于面板侧离线队列重连补发路径）。
bounce_agent() { kill $APID 2>/dev/null || true; wait $APID 2>/dev/null || true; start_agent; }

enqueue() { # <type> <data-json>
    python3 - "$WORK/lattix.db" "$1" "$2" <<'PY'
import sqlite3, sys, secrets
con = sqlite3.connect(sys.argv[1])
cur = con.execute("INSERT INTO commands (request_id, trace_id, server_id, type, data) VALUES (?, ?, 1, ?, ?)",
    (secrets.token_hex(16), secrets.token_hex(16), sys.argv[2], sys.argv[3]))
con.commit(); print(cur.lastrowid)
PY
}

TMPL='{
  "tag": "{{TAG}}",
  "protocol": "vless",
  "port": "{{PORT}}",
  "settings": {"clients": "{{CLIENTS}}", "decryption": "none"},
  "streamSettings": {
    "network": "tcp",
    "security": "reality",
    "realitySettings": {
      "show": false,
      "dest": "dl.google.com:443",
      "xver": 0,
      "serverNames": ["dl.google.com"],
      "privateKey": "{{PRIVATE_KEY}}",
      "shortIds": ["0123456789abcdef"]
    }
  },
  "sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"]}
}'

U1="$(python3 -c 'import uuid;print(uuid.uuid4())')"
U2="$(python3 -c 'import uuid;print(uuid.uuid4())')"
U3="$(python3 -c 'import uuid;print(uuid.uuid4())')"

echo ">> start backend & seed"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

# 通过 RPC 建服务器（取代 DB INSERT）
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
SRV="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"dev01","address":"127.0.0.1"}')"
BOOTSTRAP="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["bootstrap_token"])' "$SRV")"

python3 - "$WORK/lattix.db" "$TMPL" <<'PY'
import json, sqlite3, sys
con = sqlite3.connect(sys.argv[1])
con.execute("INSERT INTO nodes (id, server_id, config_template, status) VALUES (1, 1, ?, 'applying')", (sys.argv[2],))
con.commit()
PY

echo ">> apply_node（agent 未启动时入队，走重连补发；含热操作失败的重启兜底）"
CID1="$(python3 - "$WORK/lattix.db" "$TMPL" "$U1" "$U2" <<'PY'
import json, sqlite3, sys, secrets
payload = {"node_id": 1, "config": {"protocol": "vless", "port": 0, "template": json.loads(sys.argv[2])},
           "user_uuids": [sys.argv[3], sys.argv[4]]}
con = sqlite3.connect(sys.argv[1])
cur = con.execute("INSERT INTO commands (request_id, trace_id, server_id, type, data) VALUES (?, ?, 1, 'node.apply', ?)",
    (secrets.token_hex(16), secrets.token_hex(16), json.dumps(payload)))
con.commit(); print(cur.lastrowid)
PY
)"
start_agent
[[ "$(wait_cmd "$CID1")" == "acked" ]] || { echo "FAIL: apply_node 未成功"; cat "$WORK/agent.log"; exit 1; }

REALIZED="$(db "SELECT status || '|' || COALESCE(realized_config, '') FROM nodes WHERE id=1")"
[[ "$REALIZED" == active\|* ]] || { echo "FAIL: 节点未 active: $REALIZED"; exit 1; }
PORT="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1].split("|",1)[1])["port"])' "$REALIZED")"
PUBKEY="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1].split("|",1)[1])["public_key"])' "$REALIZED")"
[[ "$PORT" -gt 0 && -n "$PUBKEY" ]] || { echo "FAIL: realized_config 不完整: $REALIZED"; exit 1; }
port_open "$PORT" && echo "OK: 节点 active，端口 $PORT 监听中，public_key 已上报" || { echo "FAIL: 端口 $PORT 未监听"; exit 1; }
XPID="$(xray_pid)"; [[ -n "$XPID" ]] || { echo "FAIL: xray 未运行"; exit 1; }

echo ">> add_user（零重启热操作，nodes 显式携带目标节点）"
CID2="$(enqueue user.add "{\"uuid\":\"$U3\",\"nodes\":{\"node_1\":{\"protocol\":\"vless\"}}}")"
bounce_agent
[[ "$(wait_cmd "$CID2")" == "acked" ]] || { echo "FAIL: add_user 未成功"; cat "$WORK/agent.log"; exit 1; }
grep -q "$U3" "$XRAY_CONFIG" && [[ "$(xray_pid)" == "$XPID" ]] \
    && echo "OK: 用户热加入，xray 未重启（PID $XPID）" \
    || { echo "FAIL: add_user 校验（config 含用户=$(grep -c "$U3" "$XRAY_CONFIG" || true)，PID =$(xray_pid)）"; exit 1; }

echo ">> remove_user（零重启热操作）"
CID3="$(enqueue user.remove "{\"uuid\":\"$U3\",\"nodes\":{\"node_1\":{\"protocol\":\"vless\"}}}")"
bounce_agent
[[ "$(wait_cmd "$CID3")" == "acked" ]] || { echo "FAIL: remove_user 未成功"; cat "$WORK/agent.log"; exit 1; }
! grep -q "$U3" "$XRAY_CONFIG" && [[ "$(xray_pid)" == "$XPID" ]] \
    && echo "OK: 用户热移除，xray 未重启" \
    || { echo "FAIL: remove_user 校验"; exit 1; }

echo ">> remove_node（热删除，端口释放）"
CID4="$(enqueue node.remove '{"node_id":1}')"
bounce_agent
[[ "$(wait_cmd "$CID4")" == "acked" ]] || { echo "FAIL: remove_node 未成功"; cat "$WORK/agent.log"; exit 1; }
if grep -q 'node_1' "$XRAY_CONFIG"; then echo "FAIL: config 中仍有 node_1"; exit 1; fi
if port_open "$PORT"; then echo "FAIL: 端口 $PORT 仍监听"; echo "--- agent.log"; cat "$WORK/agent.log"; echo "--- xray 进程"; pgrep -af xray | head -5; exit 1; fi
echo "OK: 节点热删除，端口 $PORT 已释放"

echo ">> 非法模板 → failed + 错误详情"
BADTMPL='{"tag":"{{TAG}}","protocol":"bogus3","port":"{{PORT}}","settings":{"clients":"{{CLIENTS}}"}}'
python3 - "$WORK/lattix.db" "$BADTMPL" <<'PY'
import json, sqlite3, sys, secrets
con = sqlite3.connect(sys.argv[1])
con.execute("INSERT INTO nodes (id, server_id, config_template, status) VALUES (2, 1, ?, 'applying')", (sys.argv[2],))
payload = {"node_id": 2, "config": {"protocol": "vless", "port": 0, "template": json.loads(sys.argv[2])}, "user_uuids": []}
con.execute("INSERT INTO commands (request_id, trace_id, server_id, type, data) VALUES (?, ?, 1, 'node.apply', ?)",
    (secrets.token_hex(16), secrets.token_hex(16), json.dumps(payload)))
con.commit()
PY
bounce_agent
sleep 2
NODE2="$(db "SELECT status || '|' || COALESCE(error, '') FROM nodes WHERE id=2")"
[[ "$NODE2" == failed\|*校验* ]] && echo "OK: 非法模板被拒，错误详情已上报" || { echo "FAIL: node2=$NODE2"; cat "$WORK/agent.log"; exit 1; }

echo ">> 修复条件后 node/retry → 重新下发 active（§6 重试按钮）"
python3 - "$WORK/lattix.db" "$TMPL" <<'PY'
import json, sqlite3, sys
vc = {"protocol": "vless", "port": 0, "template": json.loads(sys.argv[2])}
con = sqlite3.connect(sys.argv[1])
con.execute("UPDATE nodes SET config_template=? WHERE id=2", (json.dumps(vc),))
con.commit()
PY
rpc_data POST /api/node/retry '{"node_id":2}' >/dev/null
NODE2=""
for _ in $(seq 1 30); do
    NODE2="$(db "SELECT status FROM nodes WHERE id=2")"
    [[ "$NODE2" == "active" || "$NODE2" == "failed" ]] && break
    sleep 1
done
[[ "$NODE2" == "active" ]] && echo "OK: failed 节点修复条件后重试 active" \
    || { echo "FAIL: 重试后 node2=$NODE2 $(db "SELECT COALESCE(error,'') FROM nodes WHERE id=2")"; tail -5 "$WORK/agent.log" | grep -v "accepted tcp"; exit 1; }

echo ">> dest 不可达 → 白名单 fallback（§6 预检）"
python3 - "$WORK/lattix.db" <<'PY'
import json, sqlite3, sys, secrets
tmpl = {"tag":"{{TAG}}","protocol":"vless","port":"{{PORT}}","settings":{"clients":"{{CLIENTS}}","decryption":"none"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"show":False,"dest":"192.0.2.1:443","xver":0,"serverNames":["192.0.2.1"],"privateKey":"{{PRIVATE_KEY}}","shortIds":["0123456789abcdef"]}},"sniffing":{"enabled":True,"destOverride":["http","tls","quic"]}}
con = sqlite3.connect(sys.argv[1])
con.execute("INSERT INTO nodes (id, server_id, config_template, status) VALUES (3, 1, ?, 'applying')", (json.dumps(tmpl),))
payload = {"node_id":3,"config":{"protocol":"vless","port":0,"template":tmpl},"user_uuids":[],"dest_candidates":["dl.google.com:443"]}
cur = con.execute("INSERT INTO commands (request_id, trace_id, server_id, type, data) VALUES (?, ?, 1, 'node.apply', ?)",
    (secrets.token_hex(16), secrets.token_hex(16), json.dumps(payload)))
con.commit(); print(cur.lastrowid)
PY
bounce_agent
NODE3="$(db "SELECT status || '|' || COALESCE(realized_config,'') FROM nodes WHERE id=3")"
for _ in $(seq 1 20); do [[ "$NODE3" == active\|* ]] && break; sleep 1; NODE3="$(db "SELECT status || '|' || COALESCE(realized_config,'') FROM nodes WHERE id=3")"; done
[[ "$NODE3" == active\|*dl.google.com* ]] \
    && echo "OK: dest 不可达时 fallback 到白名单候选" \
    || { echo "FAIL: node3=$NODE3"; tail -5 "$WORK/agent.log" | grep -v "accepted tcp"; exit 1; }

echo "E2E-XRAY PASS"
