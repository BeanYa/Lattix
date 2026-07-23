#!/usr/bin/env bash
# 阶段 3 面板 API 端到端验收（实施计划 3.6，设计文档 §8/§10/§11）：
#   登录会话 → 添加服务器（bootstrap token + 安装命令）→ install.sh//dist 托管 →
#   agent 上线 → 建用户（无节点扇出）→ 建节点（全量用户随 apply 下发）→
#   增删用户（零重启热操作）→ 离线扇出补发 → 仪表盘计数 → 登出拦截。
# 依赖：python3、本机 xray（默认 ~/.cache/lattix-dev/xray-core/xray，XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18096"
API_ADDR="127.0.0.1:14201"
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
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)
mkdir -p "$WORK/dist"
cp "$WORK/agent" "$WORK/dist/lattix-agent-linux-amd64"

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -dist "$WORK/dist" -install-script "$ROOT/scripts/install.sh" -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1

api() { curl -s -b "$JAR" -c "$JAR" -H 'Content-Type: application/json' "$@"; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
xray_pid() { pgrep -f "xray run -config $XRAY_CONFIG" | head -1; }

start_agent() {
    "$WORK/agent" -panel "ws://$ADDR/api/agent/ws" ${1:+-token "$1"} -state "$WORK/agent.state.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API_ADDR" -xray-runner exec \
        >"$WORK/agent.log" 2>&1 &
    APID=$!
    sleep 2
}

# --- 认证 ---
[[ "$(curl -s -o /dev/null -w '%{http_code}' "http://$ADDR/api/servers")" == "401" ]] \
    && echo "OK: 未登录访问被拦截" || { echo "FAIL: 未登录未拦截"; exit 1; }
[[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{"username":"admin","password":"wrong"}' "http://$ADDR/api/login")" == "401" ]] \
    && echo "OK: 错误密码被拒" || { echo "FAIL: 错误密码未拒"; exit 1; }
api -X POST -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}" "http://$ADDR/api/login" | grep -q '"admin"' \
    && echo "OK: 登录成功" || { echo "FAIL: 登录"; exit 1; }
[[ "$(api "http://$ADDR/api/me" | py "d['username']")" == "admin" ]] \
    && echo "OK: /api/me" || { echo "FAIL: /api/me"; exit 1; }

# --- 服务器 ---
RESP="$(api -X POST -d '{"alias":"dev01"}' "http://$ADDR/api/servers")"
BOOTSTRAP="$(echo "$RESP" | py "d['bootstrap_token']")"
INSTALL_CMD="$(echo "$RESP" | py "d['install_command']")"
[[ "$INSTALL_CMD" == "curl -fsSL http://$ADDR/install.sh | bash -s -- --panel http://$ADDR --token $BOOTSTRAP" ]] \
    && echo "OK: 安装命令格式正确" || { echo "FAIL: install_command=$INSTALL_CMD"; exit 1; }
curl -s "http://$ADDR/install.sh" | grep -q "Lattix Agent 引导安装脚本" \
    && echo "OK: /install.sh 托管" || { echo "FAIL: /install.sh"; exit 1; }
[[ "$(curl -s -o /dev/null -w '%{http_code}' "http://$ADDR/dist/lattix-agent-linux-amd64")" == "200" ]] \
    && echo "OK: /dist/ 二进制托管" || { echo "FAIL: /dist/"; exit 1; }
[[ "$(api "http://$ADDR/api/servers" | py "d[0]['online']")" == "False" ]] \
    && echo "OK: 服务器初始离线" || { echo "FAIL: 初始状态"; exit 1; }

start_agent "$BOOTSTRAP"
SVR="$(api "http://$ADDR/api/servers")"
[[ "$(echo "$SVR" | py "d[0]['online']")" == "True" && -n "$(echo "$SVR" | py "d[0]['xray_version']")" ]] \
    && echo "OK: 服务器上线（xray $(echo "$SVR" | py "d[0]['xray_version']")）" || { echo "FAIL: 上线: $SVR"; exit 1; }

# --- 用户（无节点时扇出，应空操作成功）---
U1="$(api -X POST -d '{"name":"alice"}' "http://$ADDR/api/users")"
UUID1="$(echo "$U1" | py "d['uuid']")"
[[ "$(echo "$U1" | py "d['sub_url']")" == "http://$ADDR/sub/"* ]] \
    && echo "OK: 创建用户 alice（订阅链接格式正确）" || { echo "FAIL: 创建用户: $U1"; exit 1; }

# --- 节点（apply 携带全量用户）---
api -X POST -d '{"server_id":1}' "http://$ADDR/api/nodes" >/dev/null
for _ in $(seq 1 30); do
    NSTATUS="$(api "http://$ADDR/api/nodes" | py "d[0]['status']")"
    [[ "$NSTATUS" == "active" ]] && break
    [[ "$NSTATUS" == "failed" ]] && { echo "FAIL: 节点 failed: $(api "http://$ADDR/api/nodes" | py "d[0]['error']")"; cat "$WORK/agent.log" | grep -v "accepted tcp"; exit 1; }
    sleep 1
done
[[ "$NSTATUS" == "active" ]] && echo "OK: 节点 active" || { echo "FAIL: 节点未生效（30s 超时）"; exit 1; }
NPORT="$(api "http://$ADDR/api/nodes" | py "d[0]['realized_config']['port']")"
grep -q "$UUID1" "$XRAY_CONFIG" \
    && echo "OK: 全量用户已随 apply 下发（端口 $NPORT）" || { echo "FAIL: 节点配置缺 alice"; exit 1; }
XPID="$(xray_pid)"; [[ -n "$XPID" ]] || { echo "FAIL: xray 未运行"; exit 1; }

# --- 增删用户（零重启热操作）---
U2="$(api -X POST -d '{"name":"bob"}' "http://$ADDR/api/users")"
UUID2="$(echo "$U2" | py "d['uuid']")"
sleep 2
grep -q "$UUID2" "$XRAY_CONFIG" && [[ "$(xray_pid)" == "$XPID" ]] \
    && echo "OK: bob 热加入（xray 未重启）" || { echo "FAIL: bob 热加入"; exit 1; }
BOB_ID="$(echo "$U2" | py "d['id']")"
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X DELETE "http://$ADDR/api/users/$BOB_ID")" == "204" ]]
sleep 2
! grep -q "$UUID2" "$XRAY_CONFIG" && [[ "$(xray_pid)" == "$XPID" ]] \
    && echo "OK: bob 热移除（xray 未重启）" || { echo "FAIL: bob 热移除"; exit 1; }

# --- 离线扇出补发 ---
kill $APID; wait $APID 2>/dev/null || true
U3="$(api -X POST -d '{"name":"carol"}' "http://$ADDR/api/users")"
UUID3="$(echo "$U3" | py "d['uuid']")"
sleep 1
! grep -q "$UUID3" "$XRAY_CONFIG" && echo "OK: 离线期间 carol 未下发（滞留队列）" || { echo "FAIL: 离线扇出"; exit 1; }
start_agent
for i in $(seq 1 15); do grep -q "$UUID3" "$XRAY_CONFIG" && break; sleep 1; done
grep -q "$UUID3" "$XRAY_CONFIG" \
    && echo "OK: carol 重连补发热加入" || { echo "FAIL: 离线补发"; tail -5 "$WORK/agent.log"; exit 1; }

# --- 仪表盘 ---
D="$(api "http://$ADDR/api/dashboard")"
[[ "$(echo "$D" | py "(d['servers'], d['servers_online'], d['nodes'], d['nodes_active'], d['users'])")" == "(1, 1, 1, 1, 2)" ]] \
    && echo "OK: 仪表盘计数" || { echo "FAIL: dashboard: $D"; exit 1; }

# --- 订阅（§9，公开端点）---
SUB_TOKEN1="$(echo "$U1" | py "d['sub_token']")"
SUB="$(curl -s "http://$ADDR/sub/$SUB_TOKEN1")"
PUBKEY="$(api "http://$ADDR/api/nodes" | py "d[0]['realized_config']['public_key']")"
SHORTID="$(api "http://$ADDR/api/nodes" | py "d[0]['realized_config']['short_id']")"
echo "$SUB" | grep -q "name: dev01-vless-$NPORT" \
    && echo "$SUB" | grep -q "type: vless" \
    && echo "$SUB" | grep -q "uuid: $UUID1" \
    && echo "$SUB" | grep -q "flow: xtls-rprx-vision" \
    && echo "$SUB" | grep -q "public-key: $PUBKEY" \
    && echo "$SUB" | grep -q "short-id: $SHORTID" \
    && echo "$SUB" | grep -q "servername: dl.google.com" \
    && echo "$SUB" | grep -q "udp: true" \
    && echo "$SUB" | grep -q "type: select" \
    && echo "$SUB" | grep -q "MATCH,PROXY" \
    && echo "OK: 订阅 YAML 内容（命名/UUID/reality-opts/规则）" \
    || { echo "FAIL: 订阅内容"; echo "$SUB"; exit 1; }
if python3 -c 'import yaml' 2>/dev/null; then
    echo "$SUB" | python3 -c "
import sys, yaml
c = yaml.safe_load(sys.stdin)
p = c['proxies'][0]
assert p['type'] == 'vless' and p['server'] and p['port'] > 0 and p['uuid'] and p['reality-opts']['public-key']
assert c['proxy-groups'][0]['type'] == 'select' and p['name'] in c['proxy-groups'][0]['proxies']
assert c['rules'] == ['MATCH,PROXY']
" && echo "OK: YAML 结构解析校验" || { echo "FAIL: YAML 结构"; exit 1; }
fi
[[ "$(curl -s -o /dev/null -w '%{http_code}' "http://$ADDR/sub/nonexistent-token")" == "404" ]] \
    && echo "OK: 未知 sub_token 返回 404" || { echo "FAIL: 未知 token"; exit 1; }

# --- 删除节点（remove_node 下发 + 记录删除）---
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X DELETE "http://$ADDR/api/nodes/1")" == "204" ]]
for i in $(seq 1 15); do ! grep -q 'node_1' "$XRAY_CONFIG" && break; sleep 1; done
! grep -q 'node_1' "$XRAY_CONFIG" \
    && echo "OK: 节点已删除（服务器 inbound 同步移除）" \
    || { echo "FAIL: 删除节点"; tail -5 "$WORK/agent.log"; exit 1; }
[[ "$(api "http://$ADDR/api/nodes" | py "len(d)")" == "0" ]] \
    && echo "OK: 节点列表已清空" || { echo "FAIL: 节点列表"; exit 1; }

# --- 凭证刷新（未安装的服务器 → 换发 bootstrap token 并重取安装命令）---
RESP2="$(api -X POST -d '{"alias":"test2"}' "http://$ADDR/api/servers")"
OLD_BOOT="$(echo "$RESP2" | py "d['bootstrap_token']")"
NEWID="$(echo "$RESP2" | py "d['server']['id']")"
ROT="$(api -X POST "http://$ADDR/api/servers/$NEWID/rotate-token")"
NEW_BOOT="$(echo "$ROT" | py "d['bootstrap_token']")"
[[ "$NEW_BOOT" != "$OLD_BOOT" && "$(echo "$ROT" | py "d['install_command']")" == *"$NEW_BOOT" ]] \
    && echo "OK: 凭证刷新换发 bootstrap token 并重取安装命令" \
    || { echo "FAIL: 凭证刷新: $ROT"; exit 1; }

# --- 删除离线服务器（仅删记录）---
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X DELETE "http://$ADDR/api/servers/$NEWID")" == "204" ]]
[[ "$(api "http://$ADDR/api/servers" | py "len(d)")" == "1" ]] \
    && echo "OK: 离线服务器已删除" || { echo "FAIL: 删除离线服务器"; exit 1; }

# --- 删除在线服务器（下发 uninstall，agent 自毁）---
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X DELETE "http://$ADDR/api/servers/1")" == "204" ]]
for _ in $(seq 1 10); do kill -0 $APID 2>/dev/null || break; sleep 1; done
if kill -0 $APID 2>/dev/null; then
    echo "FAIL: agent 未自毁"; exit 1
else
    echo "OK: 在线服务器删除，agent 已自卸载退出"
fi
[[ "$(api "http://$ADDR/api/servers" | py "len(d)")" == "0" ]] \
    && echo "OK: 服务器级联删除（记录清空）" || { echo "FAIL: 级联删除"; exit 1; }

# --- 登出 ---
api -X POST "http://$ADDR/api/logout" >/dev/null
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' "http://$ADDR/api/servers")" == "401" ]] \
    && echo "OK: 登出后会话失效" || { echo "FAIL: 登出"; exit 1; }

echo "E2E PASS"
