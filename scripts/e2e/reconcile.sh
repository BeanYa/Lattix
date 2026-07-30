#!/usr/bin/env bash
# 配置漂移 reconcile 端到端验收（设计文档 §17）：
#   外部篡改 config.json → agent 检测并上报 → 面板 config_drift 置位 →
#   修复按钮（重放 active 节点）→ agent 重建配置 → 漂移自动清除。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18106"
API="127.0.0.1:14206"
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

# wait_drift <0|1>
wait_drift() {
    for _ in $(seq 1 30); do
        [[ "$(db "SELECT config_drift FROM servers WHERE id=1")" == "$1" ]] && return 0
        sleep 0.5
    done
    echo "FAIL: config_drift 未变为 $1"; return 1
}

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

echo ">> 登录 + 推送漂移检测间隔 5s（最小允许值）"
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }
rpc_data POST /api/setting/update '{"agent":{"revision":1,"reconnect":{"mode":"infinite","max_retries":10},"telemetry":{"interval_seconds":60},"drift_detection":{"interval_seconds":5}}}' >/dev/null

echo ">> 建服务器并拉起 agent"
SRV="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"rc01","address":"127.0.0.1"}')"
BOOTSTRAP="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["bootstrap_token"])' "$SRV")"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -settings "$WORK/agent.settings.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2
grep -q "authenticated as server" "$WORK/agent.log" || { echo "FAIL: agent 未认证"; cat "$WORK/agent.log"; exit 1; }

echo ">> 建节点 active"
NID="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
for _ in $(seq 1 40); do
    [[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] || { echo "FAIL: 节点未 active"; exit 1; }
wait_drift 0 && echo "OK: 初始无漂移"

echo ">> 外部篡改 config.json"
python3 - "$XRAY_CONFIG" <<'PY'
import json, sys
p = sys.argv[1]
cfg = json.load(open(p))
cfg["log"] = {"loglevel": "debug"}  # 模拟管理员手动改配置
json.dump(cfg, open(p, "w"), indent=2)
PY
wait_drift 1 && echo "OK: 漂移被检测并上报"
python3 - "$(rpc_data GET /api/server/list)" <<'PY'
import json, sys
assert json.loads(sys.argv[1])[0]["config_drift"] is True
PY
echo "OK: API DTO config_drift=true"

echo ">> 修复（重放 active 节点）"
RES="$(rpc_data POST /api/server/repair '{"server_id":1}')"
REAPPLIED="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["reapplied"])' "$RES")"
[[ "$REAPPLIED" -ge 1 ]] \
    && echo "OK: 已重放 $REAPPLIED 个节点" \
    || { echo "FAIL: repair 响应异常: $RES"; exit 1; }
wait_drift 0 && echo "OK: 修复后漂移自动清除"
python3 - "$XRAY_CONFIG" <<'PY'
import json, sys
cfg = json.load(open(sys.argv[1]))
assert cfg.get("log", {}).get("loglevel") == "warning", cfg.get("log")
PY
echo "OK: 篡改内容已被重建覆盖"

echo "E2E-RECONCILE PASS"
