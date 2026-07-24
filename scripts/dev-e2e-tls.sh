#!/usr/bin/env bash
# 面板 TLS 端到端验收（设计文档 §12）：
#   本地 CA 签发的服务器证书启动 HTTPS 面板 → agent 以 SSL_CERT_FILE 信任该 CA 经 wss 首连 →
#   建节点 active → HTTPS 取订阅；登录 cookie 带 Secure；HTTP 面板认 X-Forwarded-Proto 推断 https。
# 依赖：openssl、python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18101"
ADDR_HTTP="127.0.0.1:18102"
API="127.0.0.1:14203"
BOOTSTRAP="bootstrap-tls-test"
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"

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

api() {
    local args=(-s --cacert "$WORK/ca.pem" -b "$JAR" -c "$JAR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "https://$ADDR$2"
}

echo ">> start HTTPS backend（自带证书）& agent（wss）"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" \
    -tls-cert "$WORK/server.pem" -tls-key "$WORK/server.key" >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1
db "INSERT INTO servers (alias, token) VALUES ('tls01', '$BOOTSTRAP')" >/dev/null
SSL_CERT_FILE="$WORK/ca.pem" "$WORK/agent" -panel "wss://$ADDR/api/agent/ws" -token "$BOOTSTRAP" \
    -state "$WORK/agent.state.json" -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" \
    -xray-api "$API" -xray-runner exec >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2
grep -q "authenticated as server" "$WORK/agent.log" \
    && echo "OK: agent 经 wss 首连认证成功" \
    || { echo "FAIL: agent wss 首连失败"; cat "$WORK/agent.log"; exit 1; }

echo ">> 登录 cookie Secure 标记"
HEADERS="$(curl -s -i --cacert "$WORK/ca.pem" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"lattix-admin"}' "https://$ADDR/api/login")"
grep -qi "set-cookie:.*Secure" <<<"$HEADERS" \
    && echo "OK: 会话 cookie 带 Secure" \
    || { echo "FAIL: cookie 缺 Secure: $(grep -i set-cookie <<<"$HEADERS")"; exit 1; }

echo ">> 建节点（wss 通道 apply）"
api POST /api/login '{"username":"admin","password":"lattix-admin"}' >/dev/null
ID="$(api POST /api/nodes '{"server_id":1,"protocol":"vless"}' \
    | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
for _ in $(seq 1 40); do
    [[ "$(db "SELECT status FROM nodes WHERE id=$ID")" == "active" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT status FROM nodes WHERE id=$ID")" == "active" ]] \
    && echo "OK: 节点经 wss 下发并 active" \
    || { echo "FAIL: 节点未 active: $(db "SELECT status || '|' || COALESCE(error,'') FROM nodes WHERE id=$ID")"; exit 1; }

echo ">> HTTPS 订阅"
SUB_TOKEN="$(db "SELECT sub_token FROM users LIMIT 1")"
[[ -z "$SUB_TOKEN" ]] && { api POST /api/users '{"name":"u1"}' >/dev/null; SUB_TOKEN="$(db "SELECT sub_token FROM users LIMIT 1")"; }
# §16 默认全关：分配节点后订阅才有内容
UID_ROW="$(db "SELECT id FROM users LIMIT 1")"
api PUT "/api/users/$UID_ROW/nodes" "{\"node_ids\":[$ID]}" >/dev/null
sleep 2
curl -s --cacert "$WORK/ca.pem" "https://$ADDR/sub/$SUB_TOKEN" | grep -q "type: vless" \
    && echo "OK: HTTPS 订阅可取" \
    || { echo "FAIL: HTTPS 订阅异常"; exit 1; }

echo ">> X-Forwarded-Proto 推断（反代场景）"
"$WORK/backend" -addr "$ADDR_HTTP" -db "$WORK/lattix-http.db" >"$WORK/backend-http.log" 2>&1 &
BP2ID=$!
sleep 1
JAR2="$WORK/cookies2.txt"
curl -s -c "$JAR2" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"lattix-admin"}' "http://$ADDR_HTTP/api/login" >/dev/null
RES="$(curl -s -b "$JAR2" -H 'Content-Type: application/json' -H 'X-Forwarded-Proto: https' \
    -d '{"alias":"px01"}' "http://$ADDR_HTTP/api/servers")"
grep -q "https://" <<<"$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["install_command"])' "$RES")" \
    && echo "OK: 反代场景安装命令推断为 https" \
    || { echo "FAIL: X-Forwarded-Proto 未生效: $RES"; exit 1; }

echo "E2E-TLS PASS"
