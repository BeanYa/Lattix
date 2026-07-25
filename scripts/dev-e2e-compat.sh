#!/usr/bin/env bash
# 兼容窗口端到端验证（§18）：
#   面板 v0.0.3 × agent v0.0.1（旧源码构建）→ hello 通过但 upgrade_needed=true，
#   常规命令（apply_node）滞留、upgrade_agent 放行但旧 agent 回执 unsupported；
#   换 v0.0.2 agent（窗口内）→ 滞留命令补发、节点 active；
#   v1.0.0 agent（主版本不符）→ hello 被拒。
# 前提：仓库须存在 commit 早于 HEAD 的版本 tag（其 agent 源码须真正"旧"）。
# 若仅有的 tag 指向 HEAD（"旧版"实为当前代码，断言永不成立），输出 SKIP 原因并 exit 0。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# 挑选最早一个 commit 不同于 HEAD 的版本 tag 作为旧版源码；无则明确跳过。
OLD_TAG=""
HEAD_COMMIT="$(git -C "$ROOT" rev-parse HEAD)"
while IFS= read -r t; do
    [[ -z "$t" ]] && continue
    if [[ "$(git -C "$ROOT" rev-parse "$t^{commit}")" != "$HEAD_COMMIT" ]]; then
        OLD_TAG="$t"
        break
    fi
done < <(git -C "$ROOT" tag -l 'v*' --sort=v:refname)
if [[ -z "$OLD_TAG" ]]; then
    echo "SKIP: 无 commit 早于 HEAD 的版本 tag，无法构建真正的旧版 agent（现有 tag 均指向 HEAD）"
    exit 0
fi

WORK="$(mktemp -d)"
OLD="$WORK/oldsrc"
ADDR="127.0.0.1:18160"
API_ADDR="127.0.0.1:14203"
ADMIN_PASS="testpass123"
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

cleanup() {
    kill ${BPID:-} ${APID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    git -C "$ROOT" worktree remove --force "$OLD" 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

api() { curl -s -b "$JAR" -c "$JAR" -H 'Content-Type: application/json' "$@"; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
ok() { echo "OK: $1"; }
fail() { echo "FAIL: $1"; exit 1; }

echo ">> build：面板 v0.0.3 / 当前 agent v0.0.2 + v1.0.0 / 旧源码 agent（$OLD_TAG 构建，标记 v0.0.1）"
(cd "$ROOT" && go build -ldflags "-X main.version=v0.0.3" -o "$WORK/backend" ./src/backend/cmd/backend)
(cd "$ROOT" && go build -ldflags "-X main.version=v0.0.2" -o "$WORK/agent-v002" ./src/agent/cmd/agent)
(cd "$ROOT" && go build -ldflags "-X main.version=v1.0.0" -o "$WORK/agent-v100" ./src/agent/cmd/agent)
git -C "$ROOT" worktree add "$OLD" "$OLD_TAG" >/dev/null 2>&1
(cd "$OLD" && go build -ldflags "-X main.version=v0.0.1" -o "$WORK/agent-v001" ./src/agent/cmd/agent)
mkdir -p "$WORK/dist"

echo ">> start backend (v0.0.3) + 登录 + 建服务器"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -dist "$WORK/dist" -install-script "$ROOT/scripts/install.sh" -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1
api -X POST -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}" "http://$ADDR/api/login" >/dev/null
TOKEN="$(api -X POST -d '{"alias":"compat"}' "http://$ADDR/api/servers" | py "d['bootstrap_token']")"

start_agent() { # $1=binary
    "$1" -panel "ws://$ADDR/api/agent/ws" -state "$WORK/agent.state.json" \
        -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API_ADDR" -xray-runner exec \
        ${2:+-token "$2"} >"$WORK/agent.log" 2>&1 &
    APID=$!
    sleep 2
}
stop_agent() { kill $APID 2>/dev/null; wait $APID 2>/dev/null || true; pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true; }

echo ">> 1. agent v0.0.1（落后 2 个 minor）连接"
start_agent "$WORK/agent-v001" "$TOKEN"
sleep 1
S="$(api "http://$ADDR/api/servers")"
[[ "$(echo "$S" | py "d[0]['agent_version']")" == "v0.0.1" ]] && ok "agent_version 落库" || { echo "$S"; fail "agent_version"; }
[[ "$(echo "$S" | py "d[0]['upgrade_needed']")" == "True" ]] && ok "upgrade_needed=true（超窗口）" || { echo "$S"; fail "upgrade_needed"; }

echo ">> 2. 常规命令（apply_node）滞留"
api -X POST -d '{"server_id":1}' "http://$ADDR/api/nodes" >/dev/null
sleep 3
NSTATUS="$(api "http://$ADDR/api/nodes" | py "d[0]['status']")"
[[ "$NSTATUS" == "applying" ]] && ok "节点滞留 applying（命令未下发）" || fail "节点状态: $NSTATUS"
! grep -q '"inbounds": \[' "$XRAY_CONFIG" 2>/dev/null || true
[[ "$(python3 -c "import json; print(len(json.load(open('$XRAY_CONFIG'))['inbounds']))" 2>/dev/null || echo 0)" == "0" ]] \
    && ok "xray 无 inbound（命令确未到达）" || fail "命令被下发"

echo ">> 3. upgrade_agent 放行（旧 agent 收到但只会 log，无法回执——0.0.1 无 unsupported 回执机制）"
api -X POST -d '{"version":"v0.0.3"}' "http://$ADDR/api/servers/1/upgrade-agent" >/dev/null
sleep 2
grep -q "recv unknown type=upgrade_agent" "$WORK/agent.log" && ok "窗口外仅升级命令被放行" \
    || { cat "$WORK/agent.log"; fail "旧 agent 未收到 upgrade_agent"; }
stop_agent

echo ">> 3b. 直接入库一条未来命令（模拟 panel 更新后的新命令类型）"
for _ in $(seq 1 10); do
    python3 -c "
import sqlite3
db = sqlite3.connect('$WORK/lattix.db', timeout=5)
db.execute(\"INSERT INTO commands (server_id, type, payload) VALUES (1, 'bogus_future_cmd', '{}')\")
db.commit()
" 2>/dev/null && break || sleep 1
done

echo ">> 4. 换 v0.0.2 agent（窗口内）→ 补发滞留命令 + 未来命令回执 unsupported 终态"
start_agent "$WORK/agent-v002"
for _ in $(seq 1 15); do
    NSTATUS="$(api "http://$ADDR/api/nodes" | py "d[0]['status']")"
    [[ "$NSTATUS" == "active" ]] && break
    sleep 1
done
[[ "$NSTATUS" == "active" ]] && ok "升级回窗口后节点 active（滞留命令补发）" || fail "节点未 active: $NSTATUS"
grep -q "failed: unsupported command: bogus_future_cmd" "$WORK/backend.log" \
    && ok "新 agent 回执 unsupported，面板置 failed 不重试" \
    || { grep dispatch "$WORK/backend.log" | tail -5; fail "面板未终态"; }
grep -q "recv unknown type=bogus_future_cmd" "$WORK/agent.log" && ok "agent 侧日志正确" || fail "agent 日志"
S="$(api "http://$ADDR/api/servers")"
[[ "$(echo "$S" | py "d[0]['upgrade_needed']")" == "False" ]] && ok "upgrade_needed 已清除" || fail "标志未清除"
stop_agent

echo ">> 5. v1.0.0 agent（主版本不符）→ hello 被拒"
start_agent "$WORK/agent-v100"
sleep 2
grep -q "主版本不兼容" "$WORK/backend.log" && ok "面板拒绝主版本不符的 agent" \
    || { tail -5 "$WORK/backend.log"; fail "未拒绝"; }
grep -q "4001\|authentication failed\|close" "$WORK/agent.log" && ok "agent 侧收到拒绝" || fail "agent 侧"
stop_agent

echo "ALL PASS"
