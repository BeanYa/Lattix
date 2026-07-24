#!/usr/bin/env bash
# 流量统计 + 主机遥测端到端验收（设计文档 §13，仅统计）：
#   agent 周期上报 telemetry → 真实流量经节点转发 → 面板 traffic/server_metrics 落库 →
#   API DTO 携带 traffic/metrics 字段。
# 依赖：python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）、外网可达。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18105"
API="127.0.0.1:14205"
BOOTSTRAP="bootstrap-telemetry-test"
XRAY_CONFIG="$WORK/xray-config.json"
CLIENT_CONFIG="$WORK/client-config.json"
CLIENT_SOCKS=11082
JAR="$WORK/cookies.txt"

cleanup() {
    kill ${BPID:-} ${APID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    pkill -f "xray run -config $CLIENT_CONFIG" 2>/dev/null || true
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

echo ">> start backend & agent（telemetry 间隔 2s）"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1
db "INSERT INTO servers (alias, token) VALUES ('tm01', '$BOOTSTRAP')" >/dev/null
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    -telemetry-interval 2s >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 1.5

echo ">> 用户 + 节点 + 分配"
api POST /api/login '{"username":"admin","password":"lattix-admin"}' >/dev/null
USER_RES="$(api POST /api/users '{"name":"u1"}')"
UUID="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$USER_RES")"
NID="$(api POST /api/nodes '{"server_id":1,"protocol":"vless"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
api PUT "/api/users/1/nodes" "{\"node_ids\":[$NID]}" >/dev/null
for _ in $(seq 1 40); do
    [[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT status FROM nodes WHERE id=$NID")" == "active" ]] || { echo "FAIL: 节点未 active"; exit 1; }
sleep 3 # 等 add_user 落配置
R="$(db "SELECT realized_config FROM nodes WHERE id=$NID")"

echo ">> 经节点转发真实流量"
python3 - "$R" "$UUID" "$CLIENT_CONFIG" "$CLIENT_SOCKS" <<'PY'
import json, sys
rc, uuid, path, socks = json.loads(sys.argv[1]), sys.argv[2], sys.argv[3], int(sys.argv[4])
cfg = {
    "inbounds": [{"tag": "in", "protocol": "socks", "port": socks,
                  "settings": {"auth": "noauth", "udp": True}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": rc["port"],
                                "users": [{"id": uuid, "encryption": "none", "flow": rc.get("flow", "")}]}]},
        "streamSettings": {"network": "tcp", "security": "reality", "realitySettings": {
            "serverName": rc["server_name"], "publicKey": rc["public_key"],
            "shortId": rc["short_id"], "fingerprint": "chrome"}},
    }],
}
json.dump(cfg, open(path, "w"))
PY
"$XRAY_BIN" run -config "$CLIENT_CONFIG" >"$WORK/client.log" 2>&1 &
sleep 1
CODE="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:$CLIENT_SOCKS" --max-time 20 https://example.com)" || CODE="000"
[[ "$CODE" == "200" ]] || { echo "FAIL: 数据通路不通（HTTP $CODE）"; tail -5 "$WORK/client.log"; exit 1; }

echo ">> 等待遥测上报"
NODE_TRAFFIC=""
for _ in $(seq 1 20); do
    NODE_TRAFFIC="$(db "SELECT up + down FROM traffic WHERE node_id=$NID AND user_uuid=''")"
    [[ -n "$NODE_TRAFFIC" && "$NODE_TRAFFIC" -gt 0 ]] && break
    sleep 1
done
[[ -n "$NODE_TRAFFIC" && "$NODE_TRAFFIC" -gt 0 ]] \
    && echo "OK: 节点流量已落库（$NODE_TRAFFIC 字节）" \
    || { echo "FAIL: 节点流量无记录"; tail -5 "$WORK/agent.log"; exit 1; }

USER_TRAFFIC="$(db "SELECT up + down FROM traffic WHERE node_id=0 AND user_uuid='$UUID'")"
[[ -n "$USER_TRAFFIC" && "$USER_TRAFFIC" -gt 0 ]] \
    && echo "OK: 用户流量已落库（$USER_TRAFFIC 字节）" \
    || { echo "FAIL: 用户流量无记录"; exit 1; }

METRICS="$(db "SELECT mem_total FROM server_metrics WHERE server_id=1")"
[[ -n "$METRICS" && "$METRICS" -gt 0 ]] \
    && echo "OK: 主机指标已落库（内存总量 $METRICS 字节）" \
    || { echo "FAIL: 主机指标无记录"; exit 1; }

echo ">> API DTO 校验"
python3 - "$(api GET /api/servers)" <<'PY'
import json, sys
m = json.loads(sys.argv[1])[0]["metrics"]
assert m and m["mem_total"] > 0, m
PY
echo "OK: /api/servers 携带 metrics"
python3 - "$(api GET /api/nodes)" <<'PY'
import json, sys
t = json.loads(sys.argv[1])[0]["traffic"]
assert t and t["up"] + t["down"] > 0, t
PY
echo "OK: /api/nodes 携带 traffic"
python3 - "$(api GET /api/users)" <<'PY'
import json, sys
t = json.loads(sys.argv[1])[0]["traffic"]
assert t and t["up"] + t["down"] > 0, t
PY
echo "OK: /api/users 携带 traffic"

echo "E2E-TELEMETRY PASS"
