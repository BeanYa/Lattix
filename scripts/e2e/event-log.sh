#!/usr/bin/env bash
# 日志审查端到端验收：
#   登录（含失败）→ auth.login_failed / auth.login 留痕 → 建服务器/用户/节点
#   → server.create / user.create / node.create / command.succeeded 留痕 → 删服务器
#   → server.delete 留痕（含 alias 快照）→ 操作日志过滤/分页与请求日志尾窗正确。
# 管理 API 均为 RPC 信封（feat(api)! 后无 REST 端点）：写操作需 Idempotency-Key 与
#   X-CSRF-Token；业务错误 HTTP 200 + code=INVALID_ARGUMENT 等，协议错误才用 HTTP 状态码。
# 依赖：python3、curl、openssl、本机 xray（默认 ~/.cache/lattix-dev/xray-core/xray，XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18097"
API_ADDR="127.0.0.1:14297"
ADMIN_PASS="testpass123"
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
if [[ -n "${PANEL_BIN:-}" && -n "${AGENT_BIN:-}" && -x "${PANEL_BIN:-}" && -x "${AGENT_BIN:-}" ]]; then
    echo ">> 使用预构建二进制"
    cp "$PANEL_BIN" "$WORK/backend"
    cp "$AGENT_BIN" "$WORK/agent"
else
    (cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)
fi

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

start_agent() {
    : > "$WORK/agent.log"
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" ${1:+-token "$1"} -state "$WORK/agent.state.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API_ADDR" -xray-runner exec \
        >"$WORK/agent.log" 2>&1 &
    APID=$!
    sleep 2
}

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -log-dir "$WORK/logs" \
    -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

# --- 登录失败 → auth.login_failed 留痕 ---
rpc_raw POST /api/auth/login '{"username":"admin","password":"wrong"}' >/dev/null
# 登录成功 → auth.login 留痕（签发会话 + CSRF 令牌）
LOGIN="$(rpc_data POST /api/auth/login "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}")"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }

AUTH_LOG="$(rpc_data GET '/api/log/list-operations?category=auth')"
[[ "$(echo "$AUTH_LOG" | py "[e['action'] for e in d['items']]")" == *"'auth.login_failed'"* ]] \
    && echo "OK: 登录失败已记 auth.login_failed" || { echo "FAIL: login_failed 未记: $AUTH_LOG"; exit 1; }
[[ "$(echo "$AUTH_LOG" | py "[e['action'] for e in d['items']]")" == *"'auth.login'"* ]] \
    && echo "OK: 登录成功已记 auth.login" || { echo "FAIL: login 未记: $AUTH_LOG"; exit 1; }
# operator 字段正确
[[ "$(echo "$AUTH_LOG" | py "[e for e in d['items'] if e['action']=='auth.login'][0]['operator']")" == "admin" ]] \
    && echo "OK: login 事件 operator=admin" || { echo "FAIL: operator"; exit 1; }

# --- 建服务器 → server.create ---
SRV_RESP="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"dev01","address":"198.51.100.7"}')"
BOOTSTRAP="$(echo "$SRV_RESP" | py "d['bootstrap_token']")"
SRV_LOG="$(rpc_data GET '/api/log/list-operations?category=server')"
[[ "$(echo "$SRV_LOG" | py "[e for e in d['items'] if e['action']=='server.create'][0]['server']")" == "dev01" ]] \
    && echo "OK: server.create 已记（含 server alias 关联）" || { echo "FAIL: server.create: $SRV_LOG"; exit 1; }

# --- agent 上线 ---
start_agent "$BOOTSTRAP"
sleep 1
AGENT_LOG="$(rpc_data GET '/api/log/list-operations?category=agent')"
AGENT_ACTIONS="$(echo "$AGENT_LOG" | py "[e['action'] for e in d['items']]")"
[[ "$AGENT_ACTIONS" == *"'agent.online'"* ]] \
    && echo "OK: agent.online 已记" || { echo "FAIL: agent.online: $AGENT_LOG"; exit 1; }

# --- 建节点 → node.create + command.succeeded ---
rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless"}' >/dev/null
for _ in $(seq 1 30); do
    [[ "$(rpc_data GET /api/node/list | py "d[0]['status']")" == "active" ]] && break
    sleep 1
done
COMMAND_LOG="$(rpc_data GET '/api/log/list-operations?category=command')"
[[ "$(echo "$COMMAND_LOG" | py "[e['action'] for e in d['items']]")" == *"'command.succeeded'"* ]] \
    && echo "OK: command.succeeded 已记" || { echo "FAIL: command.succeeded: $COMMAND_LOG"; exit 1; }
CHAIN_LOG="$(rpc_data GET '/api/log/list-operations?category=chain')"
[[ "$(echo "$CHAIN_LOG" | py "[e for e in d['items'] if e['action']=='node.create']")" != "[]" ]] \
    && echo "OK: node.create 已记（链路类）" || { echo "FAIL: node.create"; exit 1; }

# --- 建用户 → user.create ---
rpc_data POST /api/user/create '{"name":"alice"}' >/dev/null
USER_LOG="$(rpc_data GET '/api/log/list-operations?category=user')"
[[ "$(echo "$USER_LOG" | py "[e for e in d['items'] if e['action']=='user.create']")" != "[]" ]] \
    && echo "OK: user.create 已记" || { echo "FAIL: user.create"; exit 1; }

# --- 删服务器 → server.delete（alias 快照，对象已删仍可查）---
rpc_data POST /api/server/delete '{"server_id":1}' >/dev/null
DEL_LOG="$(rpc_data GET '/api/log/list-operations?category=server')"
DEL_DETAIL="$(echo "$DEL_LOG" | py "[e['detail'] for e in d['items'] if e['action']=='server.delete'][0]")"
echo "$DEL_DETAIL" | grep -q '"alias": *"dev01"' \
    && echo "OK: server.delete 已记（含已删对象 alias 快照）" || { echo "FAIL: server.delete 快照: $DEL_DETAIL"; exit 1; }

# --- 关键字过滤 ---
Q_LOG="$(rpc_data GET '/api/log/list-operations?q=login')"
[[ "$(echo "$Q_LOG" | py "d['total']")" -ge 2 ]] \
    && echo "OK: 关键字过滤 q=login 命中 ≥2 条" || { echo "FAIL: 关键字过滤: $Q_LOG"; exit 1; }

# --- server_id 过滤（已删服务器应仍有其历史事件）---
SID_LOG="$(rpc_data GET '/api/log/list-operations?server_id=1')"
[[ "$(echo "$SID_LOG" | py "d['total']")" -ge 1 ]] \
    && echo "OK: server_id 过滤返回历史事件（对象已删仍可查）" || { echo "FAIL: server_id 过滤: $SID_LOG"; exit 1; }

# --- 分页 ---
PAGE_LOG="$(rpc_data GET '/api/log/list-operations?limit=1&offset=0')"
[[ "$(echo "$PAGE_LOG" | py "len(d['items'])")" == "1" && "$(echo "$PAGE_LOG" | py "d['total']")" -ge 6 ]] \
    && echo "OK: 分页 limit=1 返回 1 条且 total 正确" || { echo "FAIL: 分页: $PAGE_LOG"; exit 1; }

# --- 请求日志尾窗（读取接口自身不入日志）---
REQUEST_LOG="$(rpc_data GET '/api/log/list-requests?limit=30')"
[[ "$(echo "$REQUEST_LOG" | py "len(d['items'])")" -ge 1 ]] \
    && echo "OK: 请求日志尾窗返回记录" || { echo "FAIL: 请求日志: $REQUEST_LOG"; exit 1; }

echo "E2E-EVENT-LOG PASS"
