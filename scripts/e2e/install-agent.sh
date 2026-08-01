#!/usr/bin/env bash
# Agent 引导安装（install-agent.sh）与 latx-ag 节点管理程序端到端验收（设计文档 §11/§20）：
#   真实 go build 的 agent + 本地 file:// 假 release（LATX_RELEASE_BASE 覆盖下载基址）
#   → LATX_DEV=1 + LATX_PREFIX 无 systemd 降级安装（xray 由 XRAY_BIN 本机二进制复制安装）
#   → latx-ag 随装 → checksum 篡改应中止 → 成功输出（面板地址/agent 状态/latx-ag 提示块）
#   → 重装清旧 state（预置坏 state 仍能上线）→ agent 连上面板（online）
#   → PATH 中无 unzip 时 DEV 安装仍应成功（unzip 仅 xray 下载分支需要，回归用例）
#   → latx-ag version / status（进程检查）可用
#   → user 用户态模式（LATX_USER_MODE=1 无 LATX_DEV）：守护脚本常驻 + latx-ag 用户态 start/stop
#   → PATH 中无 flock 时使用 mkdir 兼容锁启动。
# 依赖：python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖；DEV/user 模式复制安装，不下载）。
# 无需 systemd/root（用例 1-4 使用 LATX_DEV=1；用例 5-7 为 user 用户态模式）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
FAKE="$WORK/fake-release"
PREFIX="$WORK/prefix"
USER_PREFIX="$WORK/user-prefix"
XRAY_BIN="${XRAY_BIN:-}"
if [[ -z "$XRAY_BIN" ]]; then
    for c in /usr/local/bin/xray "$HOME/.cache/lattix-dev/xray-core/xray"; do
        [[ -x "$c" ]] && XRAY_BIN="$c" && break
    done
fi
[[ -n "$XRAY_BIN" && -x "$XRAY_BIN" ]] || { echo "xray 不存在（XRAY_BIN 可覆盖）"; exit 1; }

ADDR="127.0.0.1:18097"
API_ADDR="127.0.0.1:14301"
USER_API_ADDR="127.0.0.1:14302"
ADMIN_PASS="testpass123"
VERSION="v9.9.9-e2e"
REPO="BeanYa/Lattix"
JAR="$WORK/cookies.txt"

cleanup() {
    kill ${BPID:-} 2>/dev/null || true
    local install_prefix
    for install_prefix in "$PREFIX" "$USER_PREFIX" "${NO_UNZIP_PREFIX:-}"; do
        [[ -n "$install_prefix" ]] || continue
        pkill -f "$install_prefix/opt/lattix-agent/bin/lattix-agent-run" 2>/dev/null || true
        pkill -f "$install_prefix/opt/lattix-agent/bin/lattix-agent" 2>/dev/null || true
        pkill -f "$install_prefix/opt/lattix-agent/bin/xray run" 2>/dev/null || true
        # 用户态安装可能向真实 crontab 注册 @reboot；仅删除指向本次临时目录的行。
        if command -v crontab >/dev/null && crontab -l 2>/dev/null | grep -qF "$install_prefix/opt/lattix-agent/bin/lattix-agent-run"; then
            crontab -l 2>/dev/null | { grep -vF "$install_prefix/opt/lattix-agent/bin/lattix-agent-run" || true; } | crontab - || true
        fi
    done
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build agent（真实构建产物注入版本）与 backend"
(cd "$ROOT" && go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION -X main.githubRepo=$REPO" \
    -o "$WORK/lattix-agent" ./src/agent/cmd/agent)
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend)

echo ">> 摆假 release: $FAKE"
mkdir -p "$FAKE"
# latx-ag / install-agent.sh stamp
sed -e "s|{{LATTIX_VERSION}}|$VERSION|g" -e "s|{{GITHUB_REPO}}|$REPO|g" \
    "$ROOT/scripts/latx-ag.sh" > "$WORK/latx-ag"
sed -e "s|{{LATTIX_VERSION}}|$VERSION|g" -e "s|{{GITHUB_REPO}}|$REPO|g" \
    "$ROOT/scripts/install-agent.sh" > "$FAKE/install-agent.sh"
# agent 包：lattix-agent/（agent 二进制 + latx-ag，模拟 CI 打包）
mkdir -p "$WORK/agent-pkg/lattix-agent"
cp "$WORK/lattix-agent" "$WORK/agent-pkg/lattix-agent/lattix-agent"
cp "$WORK/latx-ag" "$WORK/agent-pkg/lattix-agent/latx-ag"
tar -C "$WORK/agent-pkg" -czf "$FAKE/lattix-agent-linux-amd64.tar.gz" lattix-agent
(cd "$FAKE" && sha256sum lattix-agent-linux-amd64.tar.gz > checksums.txt)

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1

CSRF_TOKEN=""
api() {
    curl -s -b "$JAR" -c "$JAR" \
        -H 'Content-Type: application/json' \
        -H "Origin: http://$ADDR" \
        -H "X-CSRF-Token: $CSRF_TOKEN" \
        -H "Idempotency-Key: install-agent-e2e-${RANDOM}-${RANDOM}" \
        "$@"
}
py() { python3 -c "import json,sys; d=json.load(sys.stdin)['data']; print($1)"; }

LOGIN="$(api -X POST -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}" "http://$ADDR/api/auth/login")"
CSRF_TOKEN="$(printf '%s' "$LOGIN" | py "d['csrf_token']")"
[[ -n "$CSRF_TOKEN" ]] && echo "OK: 登录成功" || { echo "FAIL: 登录: $LOGIN"; exit 1; }
BOOTSTRAP="$(api -X POST -d '{"country_code":"US","location":"Test","alias":"e2e01"}' "http://$ADDR/api/server/create" | py "d['bootstrap_token']")"
[[ -n "$BOOTSTRAP" ]] && echo "OK: 添加服务器取得 bootstrap token" || { echo "FAIL: bootstrap token"; exit 1; }

run_install() {
    LATX_DEV=1 LATX_PREFIX="$PREFIX" LATX_RELEASE_BASE="file://$FAKE" \
    XRAY_BIN="$XRAY_BIN" LATX_AG_XRAY_API="$API_ADDR" \
        bash "$FAKE/install-agent.sh" --version "$VERSION" --panel "http://$ADDR" --token "$BOOTSTRAP"
}

latx_ag() {
    LATX_DEV=1 LATX_PREFIX="$PREFIX" bash "$PREFIX/opt/lattix-agent/bin/latx-ag" "$@"
}

echo ">> 用例 1: checksum 篡改应中止安装"
cp "$FAKE/lattix-agent-linux-amd64.tar.gz" "$FAKE/agent.tar.gz.bak"
echo tampered >> "$FAKE/lattix-agent-linux-amd64.tar.gz"
if run_install >"$WORK/tamper.log" 2>&1; then
    echo "FAIL: 篡改的 agent 包未被 checksum 拦下"; exit 1
fi
grep -q "agent 包 SHA256 校验失败" "$WORK/tamper.log" \
    && echo "OK: 篡改资产被 checksums.txt 拦下" \
    || { echo "FAIL: 报错信息不符"; cat "$WORK/tamper.log"; exit 1; }
mv "$FAKE/agent.tar.gz.bak" "$FAKE/lattix-agent-linux-amd64.tar.gz"

echo ">> 用例 2: 正常安装（LATX_DEV=1 降级；预置坏 state 验证新 bootstrap 优先，§11）"
mkdir -p "$PREFIX/opt/lattix-agent/data"
echo '{"token":"bogus-long-term-token","server_id":999}' > "$PREFIX/opt/lattix-agent/data/state.json"
run_install | tee "$WORK/install.log"

[[ -x "$PREFIX/opt/lattix-agent/bin/lattix-agent" ]] \
    && echo "OK: lattix-agent 就位" || { echo "FAIL: lattix-agent 缺失"; exit 1; }
[[ -x "$PREFIX/opt/lattix-agent/bin/latx-ag" ]] \
    && echo "OK: latx-ag 随装就位" || { echo "FAIL: latx-ag 未安装"; exit 1; }
[[ -x "$PREFIX/opt/lattix-agent/bin/xray" ]] \
    && echo "OK: xray 复制安装就位" || { echo "FAIL: xray 缺失"; exit 1; }

grep -q "面板地址:  http://$ADDR" "$WORK/install.log" \
    && echo "OK: 成功输出含面板地址" || { echo "FAIL: 成功输出缺面板地址"; cat "$WORK/install.log"; exit 1; }
grep -q ">> 正在下载 lattix-agent $VERSION" "$WORK/install.log" \
    && echo "OK: agent 依赖下载前显示进度提示" \
    || { echo "FAIL: agent 下载提示缺失"; cat "$WORK/install.log"; exit 1; }
grep -q "Agent 状态: \[DEV\] 进程运行中" "$WORK/install.log" \
    && echo "OK: 成功输出含 agent 状态" || { echo "FAIL: 成功输出缺 agent 状态"; cat "$WORK/install.log"; exit 1; }
grep -q "Agent 版本: $VERSION" "$WORK/install.log" \
    && echo "OK: 成功输出含 agent 版本" || { echo "FAIL: 成功输出缺 agent 版本"; cat "$WORK/install.log"; exit 1; }
grep -q "xray 状态:  \[DEV\] 进程" "$WORK/install.log" \
    && echo "OK: 成功输出含 xray 运行状态" || { echo "FAIL: 成功输出缺 xray 状态"; cat "$WORK/install.log"; exit 1; }
grep -q "xray 版本:  Xray" "$WORK/install.log" \
    && echo "OK: 成功输出含 xray 版本" || { echo "FAIL: 成功输出缺 xray 版本"; exit 1; }
! grep -qx "unknown" "$WORK/install.log" \
    || { echo "FAIL: xray 版本输出包含多余 unknown"; cat "$WORK/install.log"; exit 1; }
grep -q "latx-ag status / log / update / xray-update / uninstall" "$WORK/install.log" \
    && grep -q "latx-ag -h 查看帮助" "$WORK/install.log" \
    && echo "OK: 成功输出含 latx-ag 运维提示块" || { echo "FAIL: 成功输出缺 latx-ag 提示块"; exit 1; }
! grep -q "§11" "$WORK/install.log" \
    || { echo "FAIL: 安装完成提示不应包含设计文档章节标记"; exit 1; }

echo ">> 用例 2b: PATH 中无 unzip 时 DEV 模式安装仍应成功（unzip 仅 xray 下载分支需要）"
NO_UNZIP_BIN="$WORK/no-unzip-bin"
NO_UNZIP_PREFIX="$WORK/no-unzip-prefix"
mkdir -p "$NO_UNZIP_BIN"
for command_name in bash cat chmod curl cut dirname grep gzip head id install ln mkdir mktemp mv nohup pgrep pkill rm sed sha256sum sleep tail tar uname; do
    ln -s "$(command -v "$command_name")" "$NO_UNZIP_BIN/$command_name"
done
NO_UNZIP_OUT="$(PATH="$NO_UNZIP_BIN" LATX_DEV=1 LATX_PREFIX="$NO_UNZIP_PREFIX" LATX_RELEASE_BASE="file://$FAKE" \
    XRAY_BIN="$XRAY_BIN" LATX_AG_XRAY_API="$API_ADDR" \
    bash "$FAKE/install-agent.sh" --version "$VERSION" --panel "http://$ADDR" --token "$BOOTSTRAP" 2>&1)"
printf '%s\n' "$NO_UNZIP_OUT" | grep -qF "Agent 状态: [DEV] 进程运行中" \
    && echo "OK: PATH 无 unzip 时 DEV 安装成功" \
    || { echo "FAIL: PATH 无 unzip 时安装失败"; printf '%s\n' "$NO_UNZIP_OUT"; exit 1; }
if printf '%s\n' "$NO_UNZIP_OUT" | grep -qE "unzip is required|缺少依赖"; then
    echo "FAIL: DEV 模式不应要求或安装 unzip"; printf '%s\n' "$NO_UNZIP_OUT"; exit 1
fi
echo "OK: DEV 模式未要求/安装 unzip（依赖自愈按需判定）"
pkill -f "$NO_UNZIP_PREFIX/opt/lattix-agent/bin/lattix-agent" 2>/dev/null || true

echo ">> 用例 3: agent 连上面板（坏 state 已清除，bootstrap 换发长期凭证）"
ONLINE=""
for _ in $(seq 1 15); do
    ONLINE="$(api "http://$ADDR/api/server/list" | py "d[0]['connection_state']")"
    [[ "$ONLINE" == "online" ]] && break
    sleep 1
done
[[ "$ONLINE" == "online" ]] \
    && echo "OK: agent 已上线（旧坏 state 被清除）" \
    || { echo "FAIL: agent 未上线"; tail -10 "$PREFIX/opt/lattix-agent/logs/agent.log"; exit 1; }
grep -q '"server_id": 1' "$PREFIX/opt/lattix-agent/data/state.json" \
    && echo "OK: state 已换发为真实服务器凭证" || { echo "FAIL: state 未换发"; cat "$PREFIX/opt/lattix-agent/data/state.json"; exit 1; }

echo ">> 用例 4: latx-ag version / status（LATX_DEV=1 进程检查）"
VER_OUT="$(latx_ag version)"
[[ "$VER_OUT" == *"latx-ag 版本: $VERSION"* && "$VER_OUT" == *"Agent 版本:   $VERSION"* && "$VER_OUT" == *"xray 版本:    Xray"* ]] \
    && echo "OK: latx-ag version（latx-ag/agent/xray 三版本）" || { echo "FAIL: version: $VER_OUT"; exit 1; }
[[ "$VER_OUT" != *$'\nunknown'* ]] \
    && echo "OK: latx-ag xray 版本无多余 unknown" || { echo "FAIL: version 含多余 unknown: $VER_OUT"; exit 1; }
STATUS="$(latx_ag status)"
echo "$STATUS" | grep -q "\[用户态\] 进程运行中" \
    && echo "OK: latx-ag status 进程检查" || { echo "FAIL: status: $STATUS"; exit 1; }
echo "$STATUS" | grep -q "面板地址: ws://$ADDR/api/agent/ws" \
    && echo "OK: latx-ag status 面板地址" || { echo "FAIL: status 面板地址: $STATUS"; exit 1; }
echo "$STATUS" | grep -q "服务器 ID: 1" \
    && echo "OK: latx-ag status 服务器 ID（state 文件）" || { echo "FAIL: status 服务器 ID: $STATUS"; exit 1; }
echo "$STATUS" | grep -q "面板连接: 已连接" \
    && echo "OK: latx-ag status 显示实时面板连接" || { echo "FAIL: status 面板连接: $STATUS"; exit 1; }
! echo "$STATUS" | grep -q '§17' \
    || { echo "FAIL: status 不应显示文档章节标记: $STATUS"; exit 1; }
MENU_OUT="$(printf '0\n' | LATX_LANG=zh LATX_DEV=1 LATX_PREFIX="$PREFIX" \
    bash "$PREFIX/opt/lattix-agent/bin/latx-ag")"
echo "$MENU_OUT" | grep -q "Lattix Agent 运维菜单" \
    && echo "OK: latx-ag 无参数进入交互菜单" || { echo "FAIL: 交互菜单: $MENU_OUT"; exit 1; }
MENU_EN_OUT="$(printf '1\n0\n' | LATX_LANG= LATX_DEV=1 LATX_PREFIX="$PREFIX" \
    bash "$PREFIX/opt/lattix-agent/bin/latx-ag")"
echo "$MENU_EN_OUT" | grep -q "Lattix Agent Operations" \
    && echo "OK: latx-ag 选择 English 后进入主菜单" \
    || { echo "FAIL: English 语言选择后退出: $MENU_EN_OUT"; exit 1; }
MENU_ZH_OUT="$(printf '2\n0\n' | LATX_LANG= LATX_DEV=1 LATX_PREFIX="$PREFIX" \
    bash "$PREFIX/opt/lattix-agent/bin/latx-ag")"
echo "$MENU_ZH_OUT" | grep -q "Lattix Agent 运维菜单" \
    && echo "OK: latx-ag 选择中文后进入主菜单" \
    || { echo "FAIL: 中文语言选择后退出: $MENU_ZH_OUT"; exit 1; }
latx_ag log -n 50 | grep -q "authenticated as server 1" \
    && echo "OK: latx-ag log -n（DEV 读日志文件）" || { echo "FAIL: latx-ag log"; exit 1; }

echo ">> 用例 5: user 用户态模式（LATX_USER_MODE=1 无 LATX_DEV；守护脚本常驻 + latx-ag 用户态管理）"
BOOTSTRAP3="$(api -X POST -d '{"country_code":"US","location":"Test","alias":"e2e02"}' "http://$ADDR/api/server/create" | py "d['bootstrap_token']")"
[[ -n "$BOOTSTRAP3" ]] || { echo "FAIL: 第二台服务器 bootstrap token"; exit 1; }
USER_OUT="$(LATX_USER_MODE=1 LATX_PREFIX="$USER_PREFIX" LATX_RELEASE_BASE="file://$FAKE" \
    XRAY_BIN="$XRAY_BIN" LATX_AG_XRAY_API="$USER_API_ADDR" \
    bash "$FAKE/install-agent.sh" --version "$VERSION" --panel "http://$ADDR" --token "$BOOTSTRAP3")"
echo "$USER_OUT" | grep -q "\[user\]" \
    && echo "OK: 安装输出含用户态模式提示" || { echo "FAIL: 缺用户态提示: $USER_OUT"; exit 1; }
echo "$USER_OUT" | grep -q "Agent 状态: \[user\] 进程运行中" \
    && echo "OK: user 安装摘要含 agent 实际运行状态" || { echo "FAIL: 缺 agent 实际状态: $USER_OUT"; exit 1; }
echo "$USER_OUT" | grep -q "xray 状态:  \[user\] 进程" \
    && echo "OK: user 安装摘要含 xray 实际运行状态" || { echo "FAIL: 缺 xray 实际状态: $USER_OUT"; exit 1; }
[[ -x "$USER_PREFIX/opt/lattix-agent/bin/lattix-agent-run" ]] \
    && echo "OK: 守护脚本 lattix-agent-run 就位且可执行" || { echo "FAIL: 守护脚本缺失"; exit 1; }
pgrep -f "$USER_PREFIX/opt/lattix-agent/bin/lattix-agent -panel" >/dev/null \
    && echo "OK: user 模式 agent 进程运行中" \
    || { echo "FAIL: agent 未运行"; tail -10 "$USER_PREFIX/opt/lattix-agent/logs/agent.log"; exit 1; }
ONLINE3=""
for _ in $(seq 1 15); do
    ONLINE3="$(api "http://$ADDR/api/server/list" | py "d[1]['connection_state']")"
    [[ "$ONLINE3" == "online" ]] && break
    sleep 1
done
[[ "$ONLINE3" == "online" ]] \
    && echo "OK: user 模式安装的 agent 已上线（server 2）" \
    || { echo "FAIL: server 2 未上线"; tail -10 "$USER_PREFIX/opt/lattix-agent/logs/agent.log"; exit 1; }

# latx-ag 用户态分支：不带 LATX_DEV（无 unit 文件自动判定），仅 LATX_PREFIX。
latx_ag_user() { LATX_PREFIX="$USER_PREFIX" bash "$USER_PREFIX/opt/lattix-agent/bin/latx-ag" "$@"; }
USTATUS="$(latx_ag_user status)"
echo "$USTATUS" | grep -q "\[用户态\] 进程运行中" \
    && echo "OK: latx-ag status 用户态进程检查" || { echo "FAIL: status: $USTATUS"; exit 1; }
echo "$USTATUS" | grep -q "面板地址: ws://$ADDR/api/agent/ws" \
    && echo "OK: latx-ag status 面板地址" || { echo "FAIL: status 面板地址: $USTATUS"; exit 1; }
echo "$USTATUS" | grep -q "面板连接: 已连接" \
    && echo "OK: 用户态 status 显示实时面板连接" || { echo "FAIL: 用户态面板连接: $USTATUS"; exit 1; }
latx_ag_user log -n 50 | grep -q "authenticated as server 2" \
    && echo "OK: latx-ag log -n（用户态读日志文件）" \
    || { echo "FAIL: latx-ag log"; tail -10 "$USER_PREFIX/opt/lattix-agent/logs/agent.log"; exit 1; }

echo ">> 用例 5b: user 模式重装会等待旧守护进程释放单实例锁"
OLD_AGENT_PID="$(pgrep -f "$USER_PREFIX/opt/lattix-agent/bin/lattix-agent -panel" | head -1)"
kill -STOP "$OLD_AGENT_PID"
USER_REINSTALL_OUT="$(LATX_USER_MODE=1 LATX_PREFIX="$USER_PREFIX" LATX_RELEASE_BASE="file://$FAKE" \
    XRAY_BIN="$XRAY_BIN" LATX_AG_XRAY_API="$USER_API_ADDR" \
    bash "$FAKE/install-agent.sh" --version "$VERSION" --panel "http://$ADDR" --token "$BOOTSTRAP3" 2>&1)"
echo "$USER_REINSTALL_OUT" | grep -q "旧守护进程未及时退出，正在强制清理" \
    && echo "OK: 重装超时后清理未响应的旧进程" \
    || { echo "FAIL: 未触发旧进程超时清理: $USER_REINSTALL_OUT"; exit 1; }
echo "$USER_REINSTALL_OUT" | grep -q "Agent 状态: \[user\] 进程运行中" \
    && echo "OK: user 重装后新守护进程与 agent 正常运行" \
    || { echo "FAIL: user 重装后 agent 未运行: $USER_REINSTALL_OUT"; exit 1; }

latx_ag_user stop
sleep 1
if pgrep -f "$USER_PREFIX/opt/lattix-agent/bin/lattix-agent" >/dev/null; then
    echo "FAIL: stop 后守护脚本/agent 进程仍在"; exit 1
fi
echo "OK: latx-ag stop 后守护脚本与 agent 进程消失"

echo ">> 用例 6: user 用户态守护脚本在无 flock 的精简系统上仍可启动"
NO_FLOCK_BIN="$WORK/no-flock-bin"
mkdir -p "$NO_FLOCK_BIN"
for command_name in bash cat grep mkdir mv pgrep pkill rm rmdir sleep; do
    ln -s "$(command -v "$command_name")" "$NO_FLOCK_BIN/$command_name"
done
PATH="$NO_FLOCK_BIN" "$USER_PREFIX/opt/lattix-agent/bin/lattix-agent-run" &
NO_FLOCK_RUNNER_PID=$!
STARTED=""
for _ in $(seq 1 10); do
    if pgrep -f "$USER_PREFIX/opt/lattix-agent/bin/lattix-agent -panel" >/dev/null; then
        STARTED=1
        break
    fi
    sleep 1
done
[[ -n "$STARTED" ]] \
    && echo "OK: 无 flock 时 mkdir 兼容锁成功启动 agent" || {
        echo "FAIL: 无 flock 时 agent 未启动"
        tail -20 "$USER_PREFIX/opt/lattix-agent/logs/agent.log" || true
        exit 1
    }
grep -q "flock 不可用，已使用 mkdir 兼容锁" "$USER_PREFIX/opt/lattix-agent/logs/agent.log" \
    && echo "OK: 守护器记录 mkdir 兼容锁诊断" \
    || { echo "FAIL: 缺少无 flock 兼容锁诊断"; exit 1; }
latx_ag_user stop
wait "$NO_FLOCK_RUNNER_PID" 2>/dev/null || true

echo ">> 用例 7: latx-ag start 恢复用户态进程"
latx_ag_user start
STARTED=""
for _ in $(seq 1 10); do
    if pgrep -f "$USER_PREFIX/opt/lattix-agent/bin/lattix-agent -panel" >/dev/null; then
        STARTED=1
        break
    fi
    sleep 1
done
[[ -n "$STARTED" ]] \
    && echo "OK: latx-ag start 后进程恢复" || {
        echo "FAIL: start 后进程未恢复"
        pgrep -af "$USER_PREFIX/opt/lattix-agent" || true
        tail -20 "$USER_PREFIX/opt/lattix-agent/logs/agent.log" || true
        exit 1
    }

echo "E2E PASS"
