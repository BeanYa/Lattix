#!/usr/bin/env bash
# 事件告警与 SQLite 备份端到端验收（设计文档 §19）：
#   设置页保存告警三键（bot token 不回显仅置位）→ setting/test-alerts 两通道结果（本地 webhook 成功 /
#   假 telegram token 失败）→ 杀 agent 收到 server_offline（仅跃迁）→ 5 分钟内第二次离线防抖不重发 →
#   外部篡改配置收到 config_drift → 占用端口建节点失败收到 node_failed →
#   GET /api/backup/download 200 + Content-Disposition + 合法 SQLite（头字节 + integrity_check）。
# 管理 API 均为 RPC 信封（feat(api)! 后无 REST 端点）：写操作需 Idempotency-Key 与
#   X-CSRF-Token；业务错误 HTTP 200 + code=INVALID_ARGUMENT 等，协议错误才用 HTTP 状态码。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18113"
API="127.0.0.1:14213"
HOOK="127.0.0.1:18114"
BUSY_PORT=19313
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"
HOOKS="$WORK/hooks.log"
STATE="$WORK/agent.state.json"
CSRF=""

cleanup() {
    kill ${BPID:-} ${APID:-} ${HPID:-} ${BUSYPID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    pkill -f "hook_receiver.py $HOOKS" 2>/dev/null || true
    pkill -f "busy_listener.py $BUSY_PORT" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build"
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)

# 本地 webhook 接收端：把每次 POST body 追加一行到 hooks.log。
cat > "$WORK/hook_receiver.py" <<'PY'
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(n).decode()
        with open(sys.argv[1], 'a') as f:
            f.write(body + '\n')
        self.send_response(200)
        self.end_headers()
    def log_message(self, *a):
        pass
HTTPServer(('127.0.0.1', int(sys.argv[2])), H).serve_forever()
PY
# 端口占位监听：用于制造节点 apply 失败（端口已被占用）。
cat > "$WORK/busy_listener.py" <<'PY'
import socket, sys, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('0.0.0.0', int(sys.argv[1])))
s.listen(1)
while True:
    time.sleep(3600)
PY

db() { python3 - "$WORK/lattix.db" "$1" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
cur = con.execute(sys.argv[2])
con.commit()
for row in cur: print("|".join("" if c is None else str(c) for c in row))
PY
}
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

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

# rpc_code：输出 RPC 信封 code（用于业务错误断言）。
rpc_code() {
    rpc_raw "$@" | python3 -c 'import json,sys;print(json.load(sys.stdin)["code"])'
}

# wait_hook <pattern> <loops>：等待 hooks.log 出现匹配行。
wait_hook() {
    for _ in $(seq 1 "${2:-40}"); do
        [[ -f "$HOOKS" ]] && grep -q "$1" "$HOOKS" && return 0
        sleep 0.5
    done
    echo "FAIL: hooks.log 未出现: $1"; cat "${HOOKS:-/dev/null}" 2>/dev/null; return 1
}
hook_count() { [[ -f "$HOOKS" ]] && grep -c "$1" "$HOOKS" || echo 0; }
start_agent() {
    : > "$WORK/agent.log" # truncate old log so grep waits for this auth
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "" -state "$STATE" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
        >>"$WORK/agent.log" 2>&1 &
    APID=$!
    for _ in $(seq 1 30); do
        grep -q "authenticated as server" "$WORK/agent.log" && return 0
        sleep 0.5
    done
    echo "FAIL: agent 未认证"; cat "$WORK/agent.log"; return 1
}

echo ">> start webhook 接收端 + backend"
python3 "$WORK/hook_receiver.py" "$HOOKS" "${HOOK##*:}" & HPID=$!
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

echo ">> 登录（签发会话 + CSRF 令牌）"
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }

# --- 告警设置保存 / 回显 ---
echo ">> POST /api/setting/update 告警三键并回显"
G="$(rpc_data POST /api/setting/update '{"alert_webhook_url":"http://'$HOOK'/hook","alert_telegram_bot_token":"123456:fake-token","alert_telegram_chat_id":"-1001"}')"
[[ "$(echo "$G" | py "(d['alert_webhook_url'], d['alert_telegram_bot_token_set'], d['alert_telegram_chat_id'])")" == "('http://$HOOK/hook', True, '-1001')" ]] \
    && echo "OK: 告警设置已保存（bot token 置位不回显）" || { echo "FAIL: 保存: $G"; exit 1; }
[[ "$(rpc_code POST /api/setting/update '{"alert_webhook_url":"ftp://x"}')" == "INVALID_ARGUMENT" ]] \
    && echo "OK: 非法 webhook 地址被拒（INVALID_ARGUMENT）" || { echo "FAIL: 非法 webhook 未拒"; exit 1; }

# --- setting/test-alerts：两通道结果 ---
echo ">> POST /api/setting/test-alerts"
R="$(rpc_data POST /api/setting/test-alerts)"
[[ "$(echo "$R" | py "(d['webhook']['configured'], d['webhook']['ok'])")" == "(True, True)" ]] \
    && echo "OK: webhook 测试发送成功" || { echo "FAIL: webhook 测试: $R"; exit 1; }
[[ "$(echo "$R" | py "(d['telegram']['configured'], d['telegram']['ok'])")" == "(True, False)" ]] \
    && echo "OK: telegram 假 token 发送失败（符合预期）" || { echo "FAIL: telegram 测试: $R"; exit 1; }
wait_hook '"event": *"test"' 10 && echo "OK: 接收端收到测试消息"

# --- server_offline（仅跃迁）+ 防抖动 ---
echo ">> 建服务器并拉起 agent"
BOOTSTRAP="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"alert01"}' | py "d['bootstrap_token']")"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$STATE" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2
grep -q "authenticated as server" "$WORK/agent.log" || { echo "FAIL: agent 首连"; cat "$WORK/agent.log"; exit 1; }

echo ">> 杀 agent → server_offline 告警"
kill -9 $APID; APID=""
wait_hook '"event": *"server_offline".*"server": *"alert01"' && echo "OK: 收到 server_offline（含服务器别名）"
sleep 1
[[ "$(hook_count server_offline)" == "1" ]] || { echo "FAIL: offline 计数异常: $(hook_count server_offline)"; exit 1; }

echo ">> 重连再杀 → 5 分钟内防抖不重发"
start_agent
kill -9 $APID; APID=""
sleep 4
[[ "$(hook_count server_offline)" == "1" ]] \
    && echo "OK: 第二次离线被防抖抑制（仍 1 条）" || { echo "FAIL: 防抖失效: $(hook_count server_offline) 条"; cat "$HOOKS"; exit 1; }

# --- config_drift ---
echo ">> 重连 → 外部篡改配置 → config_drift 告警"
start_agent
NID="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless"}' | py "d['id']")"
for _ in $(seq 1 40); do
    [[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] || { echo "FAIL: 节点未 active"; exit 1; }
python3 - "$XRAY_CONFIG" <<'PY'
import json, sys
p = sys.argv[1]
cfg = json.load(open(p))
cfg["log"] = {"loglevel": "debug"}
json.dump(cfg, open(p, "w"), indent=2)
PY
wait_hook '"event": *"config_drift"' && echo "OK: 收到 config_drift"

# --- node_failed（端口被占用 → apply 失败） ---
echo ">> 占用端口建节点 → node_failed 告警"
python3 "$WORK/busy_listener.py" "$BUSY_PORT" & BUSYPID=$!
sleep 0.5
NID2="$(rpc_data POST /api/node/create "{\"server_id\":1,\"protocol\":\"vless\",\"port\":$BUSY_PORT}" | py "d['id']")"
for _ in $(seq 1 40); do
    [[ "$(db "SELECT status FROM nodes WHERE id=$NID2")" == "failed" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT status FROM nodes WHERE id=$NID2")" == "failed" ]] \
    || { echo "FAIL: 节点未 failed: $(db "SELECT status FROM nodes WHERE id=$NID2")"; exit 1; }
wait_hook "\"event\": *\"node_failed\".*\"node\": *\"node_$NID2\"" && echo "OK: 收到 node_failed（含节点标识）"

# --- GET /api/backup/download ---
echo ">> 备份下载"
[[ "$(curl -s "http://$ADDR/api/backup/download" | python3 -c 'import json,sys;print(json.load(sys.stdin)["code"])')" == "AUTH_REQUIRED" ]] \
    && echo "OK: 未登录访问备份被拒（AUTH_REQUIRED）" || { echo "FAIL: 备份未鉴权"; exit 1; }
CODE="$(curl -s -b "$JAR" -D "$WORK/headers.txt" -o "$WORK/backup.db" -w '%{http_code}' "http://$ADDR/api/backup/download")"
[[ "$CODE" == "200" ]] || { echo "FAIL: 备份下载 $CODE"; exit 1; }
grep -i '^content-disposition: attachment; filename="lattix-backup-[0-9]\{8\}-[0-9]\{6\}\.db"' "$WORK/headers.txt" >/dev/null \
    && echo "OK: Content-Disposition 附件名正确" || { echo "FAIL: 附件名:"; cat "$WORK/headers.txt"; exit 1; }
python3 - "$WORK/backup.db" <<'PY'
import sqlite3, sys
p = sys.argv[1]
head = open(p, 'rb').read(16)
assert head == b'SQLite format 3\x00', head
con = sqlite3.connect(p)
assert con.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
assert con.execute('SELECT COUNT(*) FROM servers').fetchone()[0] == 1
assert con.execute("SELECT value FROM settings WHERE key='alert_webhook_url'").fetchone()[0].endswith('/hook')
PY
echo "OK: 备份文件是合法 SQLite（integrity_check ok，数据完整）"
sleep 0.5
if ls "${TMPDIR:-/tmp}"/lattix-backup-*.db >/dev/null 2>&1; then
    echo "FAIL: 临时备份文件未清理"; exit 1
fi
echo "OK: 临时备份文件已清理"

echo "E2E-ALERTS PASS"
