#!/usr/bin/env bash
# 面板一键安装（install-panel.sh）与 latx 管理程序端到端验收（设计文档 §20）：
#   本地假 release（file://，tarball 为真实 go build 产物）→ LATX_DEV=1 无 systemd 降级安装
#   → 文件就位 → checksum 篡改应中止 → 成功输出三要素（面板地址/默认账号/latx 提示）
#   → 面板 200 → latx status/version（DEV 进程检查）→ latx reset-admin
#     （新密码可登录、旧密码失效、旧会话失效、短密码拒绝）。
# 依赖：python3、curl、tar；无需 systemd/root（全程 LATX_DEV=1）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
FAKE="$WORK/fake-release"
PANEL_ROOT="$WORK/panel"
LATX_BIN="$WORK/bin/latx"
UNIT="lattix-panel-e2e"
ADDR="127.0.0.1:18081"
VERSION="v9.9.9-e2e"
REPO="BeanYa/Lattix"

cleanup() {
    pkill -f "$PANEL_ROOT/lattix-backend" 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build backend（面板 tarball 用真实构建产物，注入版本）"
(cd "$ROOT" && go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION -X main.githubRepo=$REPO" \
    -o "$WORK/lattix-backend" ./src/backend/cmd/backend)

echo ">> 摆假 release: $FAKE"
mkdir -p "$FAKE" "$WORK/pkg/lattix-panel/frontend-dist"
cp "$WORK/lattix-backend" "$WORK/pkg/lattix-panel/lattix-backend"
echo '<html>lattix e2e</html>' > "$WORK/pkg/lattix-panel/frontend-dist/index.html"
tar -C "$WORK/pkg" -czf "$FAKE/lattix-panel-linux-amd64.tar.gz" lattix-panel
# latx / install.sh / install-panel.sh stamp（模拟 CI release.yml 的 sed）
sed -e "s|{{LATTIX_VERSION}}|$VERSION|g" -e "s|{{GITHUB_REPO}}|$REPO|g" \
    "$ROOT/scripts/latx.sh" > "$FAKE/latx"
sed -e "s|{{LATTIX_VERSION}}|$VERSION|g" -e "s|{{GITHUB_REPO}}|$REPO|g" \
    "$ROOT/scripts/latx-ag.sh" > "$FAKE/latx-ag"
sed -e "s|{{LATTIX_VERSION}}|$VERSION|g" -e "s|{{GITHUB_REPO}}|$REPO|g" \
    -e "s|{{DEFAULT_XRAY_VERSION}}|v26.3.27|g" \
    "$ROOT/scripts/install.sh" > "$FAKE/install.sh"
sed -e "s|{{LATTIX_VERSION}}|$VERSION|g" -e "s|{{GITHUB_REPO}}|$REPO|g" \
    "$ROOT/scripts/install-panel.sh" > "$FAKE/install-panel.sh"
# 假 agent 二进制（本用例不执行，仅覆盖下载与 checksum 校验路径）
echo "fake agent amd64" > "$FAKE/lattix-agent-linux-amd64"
echo "fake agent arm64" > "$FAKE/lattix-agent-linux-arm64"
(cd "$FAKE" && sha256sum \
    install.sh install-panel.sh latx latx-ag \
    lattix-agent-linux-amd64 lattix-agent-linux-arm64 \
    lattix-panel-linux-amd64.tar.gz > checksums.txt)

run_install() {
    LATX_DEV=1 LATX_ROOT="$PANEL_ROOT" LATX_UNIT="$UNIT" LATX_ADDR="$ADDR" \
    LATX_BIN="$LATX_BIN" LATX_RELEASE_BASE="file://$FAKE" \
        bash "$ROOT/scripts/install-panel.sh" "$VERSION"
}

latx() {
    LATX_DEV=1 LATX_ROOT="$PANEL_ROOT" LATX_UNIT="$UNIT" LATX_BIN="$LATX_BIN" \
        bash "$LATX_BIN" "$@"
}

echo ">> 用例 1: checksum 篡改应中止安装"
cp "$FAKE/latx" "$FAKE/latx.bak"
echo tampered >> "$FAKE/latx"
if run_install >"$WORK/tamper.log" 2>&1; then
    echo "FAIL: 篡改的 latx 未被 checksum 拦下"; exit 1
fi
grep -q "latx SHA256 校验失败" "$WORK/tamper.log" \
    && echo "OK: 篡改资产被 checksums.txt 拦下" \
    || { echo "FAIL: 报错信息不符"; cat "$WORK/tamper.log"; exit 1; }
mv "$FAKE/latx.bak" "$FAKE/latx"

echo ">> 用例 2: 正常安装（LATX_DEV=1 降级，无 systemd）"
run_install | tee "$WORK/install.log"

[[ -x "$PANEL_ROOT/lattix-backend" ]] \
    && echo "OK: lattix-backend 就位" || { echo "FAIL: lattix-backend 缺失"; exit 1; }
grep -q "lattix e2e" "$PANEL_ROOT/frontend-dist/index.html" \
    && echo "OK: frontend-dist 就位" || { echo "FAIL: frontend-dist 缺失"; exit 1; }
[[ -x "$PANEL_ROOT/dist/lattix-agent-linux-amd64" && -x "$PANEL_ROOT/dist/lattix-agent-linux-arm64" ]] \
    && echo "OK: dist/ 两架构 agent 就位" || { echo "FAIL: dist/ agent 缺失"; exit 1; }
[[ -x "$PANEL_ROOT/dist/latx-ag" ]] \
    && echo "OK: dist/ latx-ag 就位" || { echo "FAIL: dist/ latx-ag 缺失"; exit 1; }
grep -q "Lattix Agent 引导安装脚本" "$PANEL_ROOT/install.sh" \
    && grep -q "LATTIX_VERSION=\"$VERSION\"" "$PANEL_ROOT/install.sh" \
    && echo "OK: install.sh（CI stamp 版）就位" || { echo "FAIL: install.sh 缺失或未 stamp"; exit 1; }
[[ -x "$LATX_BIN" ]] \
    && echo "OK: latx 已安装" || { echo "FAIL: latx 未安装到 $LATX_BIN"; exit 1; }

grep -q "面板地址:  http://.*:18081" "$WORK/install.log" \
    && echo "OK: 成功输出含面板地址" || { echo "FAIL: 成功输出缺面板地址"; cat "$WORK/install.log"; exit 1; }
grep -q "admin / lattix-admin" "$WORK/install.log" \
    && echo "OK: 成功输出含默认账号提示" || { echo "FAIL: 成功输出缺默认账号"; exit 1; }
grep -q "latx status" "$WORK/install.log" && grep -q "latx -h" "$WORK/install.log" \
    && echo "OK: 成功输出含 latx 运维提示" || { echo "FAIL: 成功输出缺 latx 提示"; exit 1; }

[[ "$(curl -s -o /dev/null -w '%{http_code}' "http://$ADDR/")" == "200" ]] \
    && echo "OK: 面板 200" || { echo "FAIL: 面板未响应"; cat "$PANEL_ROOT/panel.log"; exit 1; }
[[ "$(curl -s -o /dev/null -w '%{http_code}' "http://$ADDR/dist/lattix-agent-linux-amd64")" == "200" ]] \
    && echo "OK: /dist/ 托管可用" || { echo "FAIL: /dist/ 不可用"; exit 1; }
curl -s "http://$ADDR/install.sh" | grep -q "Lattix Agent 引导安装脚本" \
    && echo "OK: /install.sh 托管可用" || { echo "FAIL: /install.sh 不可用"; exit 1; }

echo ">> 用例 3: latx status / version（LATX_DEV=1 进程检查）"
STATUS="$(latx status)"
echo "$STATUS" | grep -q "\[DEV\] 进程运行中" \
    && echo "OK: latx status 进程检查" || { echo "FAIL: status: $STATUS"; exit 1; }
echo "$STATUS" | grep -q "面板版本: $VERSION" \
    && echo "OK: latx status 面板版本" || { echo "FAIL: status 版本: $STATUS"; exit 1; }
echo "$STATUS" | grep -q "面板地址: http://.*:18081" \
    && echo "OK: latx status 面板地址" || { echo "FAIL: status 地址: $STATUS"; exit 1; }
VER_OUT="$(latx version)"
[[ "$VER_OUT" == *"latx 版本: $VERSION"* && "$VER_OUT" == *"面板版本: $VERSION"* ]] \
    && echo "OK: latx version（latx 与面板版本）" || { echo "FAIL: version: $VER_OUT"; exit 1; }

echo ">> 用例 4: latx reset-admin（改密即全部会话失效，§10）"
JAR="$WORK/cookies.txt"
curl -s -c "$JAR" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"lattix-admin"}' "http://$ADDR/api/login" | grep -q '"admin"' \
    && echo "OK: 默认密码可登录" || { echo "FAIL: 默认密码登录"; exit 1; }
if latx reset-admin short 2>"$WORK/short.log"; then
    echo "FAIL: 短密码未被拒绝"; exit 1
fi
grep -q "至少 8 位" "$WORK/short.log" \
    && echo "OK: 短密码被拒绝（≥8 位）" || { echo "FAIL: 短密码报错不符"; cat "$WORK/short.log"; exit 1; }
latx reset-admin newpass456 | grep -q "所有会话已失效" \
    && echo "OK: reset-admin 成功且提示会话失效" || { echo "FAIL: reset-admin"; exit 1; }
[[ "$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"newpass456"}' "http://$ADDR/api/login")" == "200" ]] \
    && echo "OK: 新密码可登录" || { echo "FAIL: 新密码登录"; exit 1; }
[[ "$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"lattix-admin"}' "http://$ADDR/api/login")" == "401" ]] \
    && echo "OK: 旧默认密码已失效" || { echo "FAIL: 旧密码仍可用"; exit 1; }
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' "http://$ADDR/api/me")" == "401" ]] \
    && echo "OK: 改密前签发的会话已失效" || { echo "FAIL: 旧会话仍有效"; exit 1; }

echo "E2E PASS"
