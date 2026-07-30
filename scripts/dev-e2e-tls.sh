#!/usr/bin/env bash
# 面板 TLS 端到端验收（设计文档 §12）：
#   本地 CA 签发的服务器证书启动 HTTPS 面板 → agent 以 SSL_CERT_FILE 信任该 CA 经 wss 首连 →
#   建节点 active → HTTPS 取订阅；登录 cookie 带 Secure；HTTP 面板认 X-Forwarded-Proto 推断 https。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：openssl、python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18101"
ADDR_HTTP="127.0.0.1:18102"
API="127.0.0.1:14203"
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"
CSRF=""

cleanup() {
    kill ${BPID:-} ${BP2ID:-} ${APID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build & 签发本地 CA 证书"
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
    -keyout "$WORK/ca.key" -out "$WORK/ca.pem" -subj "/CN=lattix-test-ca" >/dev/null 2>&1
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout "$WORK/server.key" -out "$WORK/server.csr" -subj "/CN=localhost" >/dev/null 2>&1
printf "subjectAltName=DNS:localhost,IP:127.0.0.1\n" > "$WORK/san.cnf"
openssl x509 -req -in "$WORK/server.csr" -CA "$WORK/ca.pem" -CAkey "$WORK/ca.key" -CAcreateserial \
    -days 1 -extfile "$WORK/san.cnf" -out "$WORK/server.pem" >/dev/null 2>&1

db() { python3 - "$WORK/lattix.db" "$1" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
cur = con.execute(sys.argv[2])
con.commit()
for row in cur: print("|".join("" if c is None else str(c) for c in row))
PY
}

# rpc_raw <method> <path> [body] — HTTPS 主面板
rpc_raw() {
    local method="$1" path="$2" body="${3:-}"
    local args=(-sS --cacert "$WORK/ca.pem" -b "$JAR" -c "$JAR" -X "$method" -H "Origin: https://$ADDR")
    if [[ "$method" == "POST" ]]; then
        [[ -n "$body" ]] || body='{}'
        args+=(-H 'Content-Type: application/json' -H "Idempotency-Key: $(openssl rand -hex 16)" -d "$body")
        [[ -z "$CSRF" ]] || args+=(-H "X-CSRF-Token: $CSRF")
    fi
    curl "${args[@]}" "https://$ADDR$path"
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

echo ">> start HTTPS backend（自带证书）& agent（wss）"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" \
    -tls-cert "$WORK/server.pem" -tls-key "$WORK/server.key" >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS --cacert "$WORK/ca.pem" "https://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

# 通过 RPC 建服务器（取代 DB INSERT）
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }
SRV="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"tls01","address":"127.0.0.1"}')"
BOOTSTRAP="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["bootstrap_token"])' "$SRV")"

SSL_CERT_FILE="$WORK/ca.pem" "$WORK/agent" -panel "wss://$ADDR/api/agent/ws" -token "$BOOTSTRAP" \
    -state "$WORK/agent.state.json" -settings "$WORK/agent.settings.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" \
    -xray-api "$API" -xray-runner exec >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2
grep -q "authenticated as server" "$WORK/agent.log" \
    && echo "OK: agent 经 wss 首连认证成功" \
    || { echo "FAIL: agent wss 首连失败"; cat "$WORK/agent.log"; exit 1; }

echo ">> 登录 cookie Secure 标记"
HEADERS="$(curl -s -i --cacert "$WORK/ca.pem" -H "Origin: https://$ADDR" \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"lattix-admin"}' "https://$ADDR/api/auth/login")"
grep -qi "set-cookie:.*Secure" <<<"$HEADERS" \
    && echo "OK: 会话 cookie 带 Secure" \
    || { echo "FAIL: cookie 缺 Secure: $(grep -i set-cookie <<<"$HEADERS")"; exit 1; }

echo ">> 建节点（wss 通道 apply）"
NID="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless"}' \
    | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
for _ in $(seq 1 40); do
    [[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] \
    && echo "OK: 节点经 wss 下发并 active" \
    || { echo "FAIL: 节点未 active: $(db "SELECT status || '|' || COALESCE(error,'') FROM nodes WHERE id=$NID")"; exit 1; }

echo ">> HTTPS 订阅"
U1="$(rpc_data POST /api/user/create '{"name":"u1"}')"
USER_ID="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$U1")"
SUB_TOKEN="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$U1")"
rpc_data POST /api/user/set-nodes "{\"user_id\":$USER_ID,\"node_ids\":[$NID]}" >/dev/null
sleep 2
curl -s --cacert "$WORK/ca.pem" "https://$ADDR/sub/$SUB_TOKEN?format=clash" | grep -q "type: vless" \
    && echo "OK: HTTPS 订阅可取" \
    || { echo "FAIL: HTTPS 订阅异常"; exit 1; }

echo ">> X-Forwarded-Proto 推断（反代场景）"
"$WORK/backend" -addr "$ADDR_HTTP" -db "$WORK/lattix-http.db" >"$WORK/backend-http.log" 2>&1 &
BP2ID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR_HTTP/readyz" >/dev/null 2>&1 && break; sleep 0.2; done
JAR2="$WORK/cookies2.txt"
CSRF2=""
# HTTP 后端登录（Origin 用 https 因为 X-Forwarded-Proto 使 isSecure=true）
LOGIN2="$(curl -sS -b "$JAR2" -c "$JAR2" -X POST \
    -H "Origin: https://$ADDR_HTTP" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"lattix-admin"}' "http://$ADDR_HTTP/api/auth/login")"
CSRF2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["data"]["csrf_token"])' "$LOGIN2")"
RES="$(curl -sS -b "$JAR2" -c "$JAR2" -X POST \
    -H "Origin: https://$ADDR_HTTP" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $(openssl rand -hex 16)" -H "X-CSRF-Token: $CSRF2" \
    -H 'X-Forwarded-Proto: https' \
    -d '{"country_code":"US","location":"Test","alias":"px01"}' "http://$ADDR_HTTP/api/server/create")"
grep -q "https://" <<<"$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["data"]["install_command"])' "$RES")" \
    && echo "OK: 反代场景安装命令推断为 https" \
    || { echo "FAIL: X-Forwarded-Proto 未生效: $RES"; exit 1; }

echo "E2E-TLS PASS"
