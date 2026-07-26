#!/usr/bin/env bash
# Agent 引导安装（install.sh）与 latx-ag 节点管理程序端到端验收（设计文档 §11/§20）：
#   真实 go build 的 agent + 本地 file:// 假 release（LATX_RELEASE_BASE 覆盖下载基址）
#   → LATX_DEV=1 + LATX_PREFIX 无 systemd 降级安装（xray 由 XRAY_BIN 本机二进制复制安装）
#   → latx-ag 随装 → checksum 篡改应中止 → 成功输出（面板地址/agent 状态/latx-ag 提示块）
#   → 重装清旧 state（预置坏 state 仍能上线）→ agent 连上面板（online）
#   → latx-ag version / status（DEV 进程检查）可用
#   → --source panel：面板 /resource 镜像（-resource 指假 release 目录）下载安装并上线。
# 依赖：python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖；DEV 模式复制安装，不下载）。
# 无需 systemd/root（全程 LATX_DEV=1）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
FAKE="$WORK/fake-release"
PREFIX="$WORK/prefix"
XRAY_BIN="${XRAY_BIN:-}"
if [[ -z "$XRAY_BIN" ]]; then
    for c in /usr/local/bin/xray "$HOME/.cache/lattix-dev/xray-core/xray"; do
        [[ -x "$c" ]] && XRAY_BIN="$c" && break
    done
fi
[[ -n "$XRAY_BIN" && -x "$XRAY_BIN" ]] || { echo "xray 不存在（XRAY_BIN 可覆盖）"; exit 1; }

ADDR="127.0.0.1:18097"
API_ADDR="127.0.0.1:14301"
ADMIN_PASS="testpass123"
VERSION="v9.9.9-e2e"
REPO="BeanYa/Lattix"
JAR="$WORK/cookies.txt"

cleanup() {
    kill ${BPID:-} 2>/dev/null || true
    pkill -f "$PREFIX/usr/local/bin/lattix-agent" 2>/dev/null || true
    pkill -f "$PREFIX/usr/local/bin/xray run" 2>/dev/null || true
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
# latx-ag / install.sh stamp（模拟 CI release.yml 的 sed）
sed -e "s|{{LATTIX_VERSION}}|$VERSION|g" -e "s|{{GITHUB_REPO}}|$REPO|g" \
    "$ROOT/scripts/latx-ag.sh" > "$WORK/latx-ag"
sed -e "s|{{LATTIX_VERSION}}|$VERSION|g" -e "s|{{GITHUB_REPO}}|$REPO|g" \
    -e "s|{{DEFAULT_XRAY_VERSION}}|v26.3.27|g" \
    "$ROOT/scripts/install.sh" > "$FAKE/install.sh"
# agent 包：lattix-agent/（agent 二进制 + latx-ag，模拟 CI 打包）
mkdir -p "$WORK/agent-pkg/lattix-agent"
cp "$WORK/lattix-agent" "$WORK/agent-pkg/lattix-agent/lattix-agent"
cp "$WORK/latx-ag" "$WORK/agent-pkg/lattix-agent/latx-ag"
tar -C "$WORK/agent-pkg" -czf "$FAKE/lattix-agent-linux-amd64.tar.gz" lattix-agent
(cd "$FAKE" && sha256sum lattix-agent-linux-amd64.tar.gz > checksums.txt)

echo ">> start backend（-resource 指向假 release 目录，镜像 /resource 托管布局）"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -resource "$FAKE" -install-script "$ROOT/scripts/install.sh" -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1

api() { curl -s -b "$JAR" -c "$JAR" -H 'Content-Type: application/json' "$@"; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

api -X POST -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}" "http://$ADDR/api/login" | grep -q '"admin"' \
    && echo "OK: 登录成功" || { echo "FAIL: 登录"; exit 1; }
BOOTSTRAP="$(api -X POST -d '{"alias":"e2e01"}' "http://$ADDR/api/servers" | py "d['bootstrap_token']")"
[[ -n "$BOOTSTRAP" ]] && echo "OK: 添加服务器取得 bootstrap token" || { echo "FAIL: bootstrap token"; exit 1; }

run_install() {
    LATX_DEV=1 LATX_PREFIX="$PREFIX" LATX_RELEASE_BASE="file://$FAKE" \
    XRAY_BIN="$XRAY_BIN" LATX_AG_XRAY_API="$API_ADDR" \
        bash "$FAKE/install.sh" --panel "http://$ADDR" --token "$BOOTSTRAP" --xray-version v26.3.27
}

latx_ag() {
    LATX_DEV=1 LATX_PREFIX="$PREFIX" bash "$PREFIX/usr/local/bin/latx-ag" "$@"
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

echo ">> 用例 2: 正常安装（LATX_DEV=1 降级；预置坏 state 验证重装清理，§11）"
mkdir -p "$PREFIX/etc"
echo '{"token":"bogus-long-term-token","server_id":999}' > "$PREFIX/etc/lattix-agent.state.json"
run_install | tee "$WORK/install.log"

[[ -x "$PREFIX/usr/local/bin/lattix-agent" ]] \
    && echo "OK: lattix-agent 就位" || { echo "FAIL: lattix-agent 缺失"; exit 1; }
[[ -x "$PREFIX/usr/local/bin/latx-ag" ]] \
    && echo "OK: latx-ag 随装就位" || { echo "FAIL: latx-ag 未安装"; exit 1; }
[[ -x "$PREFIX/usr/local/bin/xray" ]] \
    && echo "OK: xray 复制安装就位" || { echo "FAIL: xray 缺失"; exit 1; }

grep -q "面板地址:  http://$ADDR" "$WORK/install.log" \
    && echo "OK: 成功输出含面板地址" || { echo "FAIL: 成功输出缺面板地址"; cat "$WORK/install.log"; exit 1; }
grep -q "Agent 状态: \[DEV\] 进程运行中" "$WORK/install.log" \
    && echo "OK: 成功输出含 agent 状态" || { echo "FAIL: 成功输出缺 agent 状态"; cat "$WORK/install.log"; exit 1; }
grep -q "xray 版本:  Xray" "$WORK/install.log" \
    && echo "OK: 成功输出含 xray 版本" || { echo "FAIL: 成功输出缺 xray 版本"; exit 1; }
grep -q "latx-ag status / log / update / xray-update / uninstall" "$WORK/install.log" \
    && grep -q "latx-ag -h 查看帮助" "$WORK/install.log" \
    && echo "OK: 成功输出含 latx-ag 运维提示块" || { echo "FAIL: 成功输出缺 latx-ag 提示块"; exit 1; }

echo ">> 用例 3: agent 连上面板（坏 state 已清除，bootstrap 换发长期凭证）"
ONLINE=""
for _ in $(seq 1 15); do
    ONLINE="$(api "http://$ADDR/api/servers" | py "d[0]['online']")"
    [[ "$ONLINE" == "True" ]] && break
    sleep 1
done
[[ "$ONLINE" == "True" ]] \
    && echo "OK: agent 已上线（旧坏 state 被清除）" \
    || { echo "FAIL: agent 未上线"; tail -10 "$PREFIX/var/log/lattix-agent.log"; exit 1; }
grep -q '"server_id": 1' "$PREFIX/etc/lattix-agent.state.json" \
    && echo "OK: state 已换发为真实服务器凭证" || { echo "FAIL: state 未换发"; cat "$PREFIX/etc/lattix-agent.state.json"; exit 1; }

echo ">> 用例 4: latx-ag version / status（LATX_DEV=1 进程检查）"
VER_OUT="$(latx_ag version)"
[[ "$VER_OUT" == *"latx-ag 版本: $VERSION"* && "$VER_OUT" == *"Agent 版本:   $VERSION"* && "$VER_OUT" == *"xray 版本:    Xray"* ]] \
    && echo "OK: latx-ag version（latx-ag/agent/xray 三版本）" || { echo "FAIL: version: $VER_OUT"; exit 1; }
STATUS="$(latx_ag status)"
echo "$STATUS" | grep -q "\[DEV\] 进程运行中" \
    && echo "OK: latx-ag status 进程检查" || { echo "FAIL: status: $STATUS"; exit 1; }
echo "$STATUS" | grep -q "面板地址: ws://$ADDR/api/agent/ws" \
    && echo "OK: latx-ag status 面板地址" || { echo "FAIL: status 面板地址: $STATUS"; exit 1; }
echo "$STATUS" | grep -q "服务器 ID: 1" \
    && echo "OK: latx-ag status 服务器 ID（state 文件）" || { echo "FAIL: status 服务器 ID: $STATUS"; exit 1; }
latx_ag log -n 5 | grep -q "authenticated as server 1" \
    && echo "OK: latx-ag log -n（DEV 读日志文件）" || { echo "FAIL: latx-ag log"; exit 1; }

echo ">> 用例 5: --source panel（面板 /resource 镜像下载，与 github 源同一下载/校验流程）"
curl -s "http://$ADDR/install.sh" | grep -q "LATTIX_VERSION=\"$VERSION\"" \
    && echo "OK: /install.sh 托管 resource 镜像（CI stamp 版）" || { echo "FAIL: /install.sh 未托管镜像脚本"; exit 1; }
[[ "$(curl -s -o /dev/null -w '%{http_code}' "http://$ADDR/resource/lattix-agent-linux-amd64.tar.gz")" == "200" ]] \
    && echo "OK: /resource/ 镜像托管可用" || { echo "FAIL: /resource/ 不可用"; exit 1; }
BOOTSTRAP2="$(api -X POST -d '{"alias":"e2e02"}' "http://$ADDR/api/servers" | py "d['bootstrap_token']")"
[[ -n "$BOOTSTRAP2" ]] || { echo "FAIL: 第二台服务器 bootstrap token"; exit 1; }
PANEL_OUT="$(LATX_DEV=1 LATX_PREFIX="$PREFIX" XRAY_BIN="$XRAY_BIN" LATX_AG_XRAY_API="$API_ADDR" \
    bash "$FAKE/install.sh" --source panel --panel "http://$ADDR" --token "$BOOTSTRAP2" --xray-version v26.3.27)"
echo "$PANEL_OUT" | grep -q "installing lattix-agent $VERSION（source: panel）" \
    && echo "OK: panel 源安装输出（钉版本 $VERSION）" || { echo "FAIL: panel 源输出: $PANEL_OUT"; exit 1; }
echo "$PANEL_OUT" | grep -q "agent 包 SHA256 校验通过（checksums.txt）" \
    && echo "OK: panel 源 checksums.txt 校验通过" || { echo "FAIL: panel 源校验: $PANEL_OUT"; exit 1; }
ONLINE2=""
for _ in $(seq 1 15); do
    ONLINE2="$(api "http://$ADDR/api/servers" | py "d[1]['online']")"
    [[ "$ONLINE2" == "True" ]] && break
    sleep 1
done
[[ "$ONLINE2" == "True" ]] \
    && echo "OK: panel 源安装的 agent 已上线（server 2）" \
    || { echo "FAIL: server 2 未上线"; tail -10 "$PREFIX/var/log/lattix-agent.log"; exit 1; }

echo "E2E PASS"
