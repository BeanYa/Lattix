#!/usr/bin/env bash
# Panel RPC + current Agent end-to-end smoke test. This intentionally exercises
# only the current protocol; fresh installs do not carry a compatibility matrix.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
ADDR="127.0.0.1:18096"
ADMIN_PASS="testpass123"
JAR="$WORK/cookies.txt"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
XRAY_CONFIG="$WORK/xray-config.json"

cleanup() {
    kill "${APID:-}" "${BPID:-}" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

if [[ -n "${PANEL_BIN:-}" && -n "${AGENT_BIN:-}" ]]; then
    cp "$PANEL_BIN" "$WORK/backend"
    cp "$AGENT_BIN" "$WORK/agent"
else
    (cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend)
    (cd "$ROOT" && go build -o "$WORK/agent" ./src/agent/cmd/agent)
fi
chmod +x "$WORK/backend" "$WORK/agent"

"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -static "$WORK/none" >"$WORK/backend.log" 2>&1 &
BPID=$!

for _ in $(seq 1 30); do
    curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break
    sleep 0.2
done
curl -fsS "http://$ADDR/healthz" | grep -qx ok
curl -fsS "http://$ADDR/readyz" | grep -qx ready
[[ "$(curl -sS -o /dev/null -w '%{http_code}' "http://$ADDR/api/not-real")" == "404" ]]
[[ "$(curl -sS -o /dev/null -w '%{http_code}' "http://$ADDR/api/auth/login")" == "405" ]]

rpc_raw() {
    local method="$1" path="$2" body="${3:-}" key="${4:-}"
    local args=(-sS -b "$JAR" -c "$JAR" -X "$method" -H "Origin: http://$ADDR")
    if [[ "$method" == "POST" ]]; then
        [[ -n "$body" ]] || body='{}'
        [[ -n "$key" ]] || key="$(openssl rand -hex 16)"
        args+=(-H 'Content-Type: application/json' -H "Idempotency-Key: $key" -d "$body")
        [[ -z "${CSRF:-}" ]] || args+=(-H "X-CSRF-Token: $CSRF")
    fi
    curl "${args[@]}" "http://$ADDR$path"
}

rpc_data() {
    rpc_raw "$@" | python3 -c '
import json,sys
value=json.load(sys.stdin)
if value["code"] not in ("OK","ACCEPTED"):
    raise SystemExit(f"RPC failed: {value}")
print(json.dumps(value["data"], separators=(",",":")))
'
}

unauth="$(rpc_raw GET /api/server/list)"
python3 - "$unauth" <<'PY'
import json,sys
value=json.loads(sys.argv[1])
assert value["code"] == "AUTH_REQUIRED"
assert len(value["request_id"]) == len(value["trace_id"]) == 32
PY

wrong="$(rpc_raw POST /api/auth/login '{"username":"admin","password":"wrong"}')"
python3 - "$wrong" <<'PY'
import json,sys
assert json.loads(sys.argv[1])["code"] == "AUTH_INVALID_CREDENTIALS"
PY

login="$(rpc_data POST /api/auth/login "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}")"
CSRF="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["csrf_token"])' "$login")"
[[ -n "$CSRF" ]]

protocol_status="$(curl -sS -o "$WORK/protocol.json" -w '%{http_code}' \
    -b "$JAR" -H "Origin: http://$ADDR" -H "X-CSRF-Token: $CSRF" \
    -H 'Content-Type: application/json' -H 'Idempotency-Key: malformed-test' \
    -X POST -d '{' "http://$ADDR/api/server/create")"
[[ "$protocol_status" == "400" ]]

key="server-create-idempotency"
body='{"country_code":"US","location":"Test","alias":"rpc-e2e","address":"127.0.0.1"}'
first="$(rpc_raw POST /api/server/create "$body" "$key")"
second="$(rpc_raw POST /api/server/create "$body" "$key")"
python3 - "$first" "$second" <<'PY'
import json,sys
a,b=map(json.loads,sys.argv[1:])
assert a["code"] == b["code"] == "OK"
assert a["data"]["server"]["id"] == b["data"]["server"]["id"] == 1
assert a["request_id"] != b["request_id"]
PY
bootstrap="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["data"]["bootstrap_token"])' "$first")"
[[ "$(rpc_data GET /api/server/list | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')" == "1" ]]

if [[ -x "$XRAY_BIN" ]]; then
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$bootstrap" \
        -state "$WORK/agent.state.json" -xray-bin "$XRAY_BIN" \
        -xray-config "$XRAY_CONFIG" -xray-api "127.0.0.1:14201" -xray-runner exec \
        >"$WORK/agent.log" 2>&1 &
    APID=$!

    for _ in $(seq 1 30); do
        online="$(rpc_data GET /api/server/list | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["online"])')"
        [[ "$online" == "True" ]] && break
        sleep 1
    done
    [[ "$online" == "True" ]]

    rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless"}' >/dev/null
    for _ in $(seq 1 45); do
        status="$(rpc_data GET /api/node/list | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["status"])')"
        [[ "$status" == "active" ]] && break
        [[ "$status" == "failed" ]] && { cat "$WORK/agent.log"; exit 1; }
        sleep 1
    done
    [[ "$status" == "active" ]]
fi

rpc_data POST /api/auth/logout '{}' >/dev/null
python3 - "$(rpc_raw GET /api/auth/me)" <<'PY'
import json,sys
assert json.loads(sys.argv[1])["code"] == "AUTH_REQUIRED"
PY

echo "OK: current RPC/Requester/Agent protocol e2e"
