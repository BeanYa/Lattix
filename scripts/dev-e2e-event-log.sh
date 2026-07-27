#!/usr/bin/env bash
# 日志审查端到端验收：
#   登录（含失败）→ auth.login_failed / auth.login 留痕 → 建服务器/用户/节点
#   → server.create / user.create / node.create / command.succeeded 留痕 → 删服务器
#   → server.delete 留痕（含 alias 快照）→ 操作日志过滤/分页与请求日志尾窗正确。
# 依赖：python3、本机 xray（默认 ~/.cache/lattix-dev/xray-core/xray，XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18097"
API_ADDR="127.0.0.1:14297"
ADMIN_PASS="testpass123"
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
if [[ -n "${PANEL_BIN:-}" && -n "${AGENT_BIN:-}" && -x "${PANEL_BIN:-}" && -x "${AGENT_BIN:-}" ]]; then
    echo ">> 使用预构建二进制"
    cp "$PANEL_BIN" "$WORK/backend"
    cp "$AGENT_BIN" "$WORK/agent"
else
    (cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)
fi

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -log-dir "$WORK/logs" \
    -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1

api() { curl -s -b "$JAR" -c "$JAR" -H 'Content-Type: application/json' "$@"; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

start_agent() {
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" ${1:+-token "$1"} -state "$WORK/agent.state.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API_ADDR" -xray-runner exec \
        >"$WORK/agent.log" 2>&1 &
    APID=$!
    sleep 2
}

# --- 登录失败 → auth.login_failed 留痕 ---
curl -s -o /dev/null -X POST -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"wrong"}' "http://$ADDR/api/login"
# 登录成功 → auth.login 留痕
api -X POST -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}" "http://$ADDR/api/login" >/dev/null

AUTH_LOG="$(api "http://$ADDR/api/logs/operations?category=auth")"
[[ "$(echo "$AUTH_LOG" | py "[e['action'] for e in d['items']]")" == *"'auth.login_failed'"* ]] \
    && echo "OK: 登录失败已记 auth.login_failed" || { echo "FAIL: login_failed 未记: $AUTH_LOG"; exit 1; }
[[ "$(echo "$AUTH_LOG" | py "[e['action'] for e in d['items']]")" == *"'auth.login'"* ]] \
    && echo "OK: 登录成功已记 auth.login" || { echo "FAIL: login 未记: $AUTH_LOG"; exit 1; }
# operator/ip 字段正确
[[ "$(echo "$AUTH_LOG" | py "[e for e in d['items'] if e['action']=='auth.login'][0]['operator']")" == "admin" ]] \
    && echo "OK: login 事件 operator=admin" || { echo "FAIL: operator"; exit 1; }

# --- 建服务器 → server.create ---
SRV_RESP="$(api -X POST -d '{"country_code":"US","location":"Test","alias":"dev01","address":"198.51.100.7","xray_version":"v26.3.27"}' "http://$ADDR/api/servers")"
BOOTSTRAP="$(echo "$SRV_RESP" | py "d['bootstrap_token']")"
SRV_LOG="$(api "http://$ADDR/api/logs/operations?category=server")"
[[ "$(echo "$SRV_LOG" | py "[e for e in d['items'] if e['action']=='server.create'][0]['server']")" == "dev01" ]] \
    && echo "OK: server.create 已记（含 server alias 关联）" || { echo "FAIL: server.create: $SRV_LOG"; exit 1; }

# --- agent 上线 ---
start_agent "$BOOTSTRAP"
sleep 1
AGENT_LOG="$(api "http://$ADDR/api/logs/operations?category=agent")"
AGENT_ACTIONS="$(echo "$AGENT_LOG" | py "[e['action'] for e in d['items']]")"
[[ "$AGENT_ACTIONS" == *"'agent.online'"* ]] \
    && echo "OK: agent.online 已记" || { echo "FAIL: agent.online: $AGENT_LOG"; exit 1; }

# --- 建节点 → node.create + command.succeeded ---
api -X POST -d '{"server_id":1}' "http://$ADDR/api/nodes" >/dev/null
for _ in $(seq 1 30); do
    [[ "$(api "http://$ADDR/api/nodes" | py "d[0]['status']")" == "active" ]] && break
    sleep 1
done
COMMAND_LOG="$(api "http://$ADDR/api/logs/operations?category=command")"
[[ "$(echo "$COMMAND_LOG" | py "[e['action'] for e in d['items']]")" == *"'command.succeeded'"* ]] \
    && echo "OK: command.succeeded 已记" || { echo "FAIL: command.succeeded: $COMMAND_LOG"; exit 1; }
CHAIN_LOG="$(api "http://$ADDR/api/logs/operations?category=chain")"
[[ "$(echo "$CHAIN_LOG" | py "[e for e in d['items'] if e['action']=='node.create']")" != "[]" ]] \
    && echo "OK: node.create 已记（链路类）" || { echo "FAIL: node.create"; exit 1; }

# --- 建用户 → user.create ---
api -X POST -d '{"name":"alice"}' "http://$ADDR/api/users" >/dev/null
USER_LOG="$(api "http://$ADDR/api/logs/operations?category=user")"
[[ "$(echo "$USER_LOG" | py "[e for e in d['items'] if e['action']=='user.create']")" != "[]" ]] \
    && echo "OK: user.create 已记" || { echo "FAIL: user.create"; exit 1; }

# --- 删服务器 → server.delete（alias 快照，对象已删仍可查）---
api -X DELETE "http://$ADDR/api/servers/1" >/dev/null
DEL_LOG="$(api "http://$ADDR/api/logs/operations?category=server")"
DEL_DETAIL="$(echo "$DEL_LOG" | py "[e['detail'] for e in d['items'] if e['action']=='server.delete'][0]")"
echo "$DEL_DETAIL" | grep -q '"alias": *"dev01"' \
    && echo "OK: server.delete 已记（含已删对象 alias 快照）" || { echo "FAIL: server.delete 快照: $DEL_DETAIL"; exit 1; }

# --- 关键字过滤 ---
Q_LOG="$(api "http://$ADDR/api/logs/operations?q=login")"
[[ "$(echo "$Q_LOG" | py "d['total']")" -ge 2 ]] \
    && echo "OK: 关键字过滤 q=login 命中 ≥2 条" || { echo "FAIL: 关键字过滤: $Q_LOG"; exit 1; }

# --- server_id 过滤（已删服务器应仍有其历史事件）---
# 删除后该 server_id 的事件仍可按 server_id 过滤查到（审计留痕）
SID_LOG="$(api "http://$ADDR/api/logs/operations?server_id=1")"
[[ "$(echo "$SID_LOG" | py "d['total']")" -ge 1 ]] \
    && echo "OK: server_id 过滤返回历史事件（对象已删仍可查）" || { echo "FAIL: server_id 过滤: $SID_LOG"; exit 1; }

# --- 分页 ---
PAGE_LOG="$(api "http://$ADDR/api/logs/operations?limit=1&offset=0")"
[[ "$(echo "$PAGE_LOG" | py "len(d['items'])")" == "1" && "$(echo "$PAGE_LOG" | py "d['total']")" -ge 6 ]] \
    && echo "OK: 分页 limit=1 返回 1 条且 total 正确" || { echo "FAIL: 分页: $PAGE_LOG"; exit 1; }

# --- 请求日志尾窗（读取接口自身不入日志）---
REQUEST_LOG="$(api "http://$ADDR/api/logs/requests?limit=30")"
[[ "$(echo "$REQUEST_LOG" | py "len(d['items'])")" -ge 1 ]] \
    && echo "OK: 请求日志尾窗返回记录" || { echo "FAIL: 请求日志: $REQUEST_LOG"; exit 1; }

echo "E2E PASS"
