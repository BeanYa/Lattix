#!/usr/bin/env bash
# 面板一键安装（install-panel.sh）与 latx 管理程序端到端验收（设计文档 §20）：
#   本地假 release（file://，tarball 为真实 go build 产物）→ LATX_DEV=1 无 systemd 降级安装
#   → 文件就位 → checksum 篡改应中止 → 成功输出面板地址与随机/指定凭据
#   → 面板 200 → latx status/version（DEV 进程检查）→ latx reset-admin
#     （新密码可登录、旧密码失效、旧会话失效、短密码拒绝）。
# 管理 API 均为 RPC 信封（恒 HTTP 200），业务结果看 code 字段。
# 依赖：python3、curl、tar；无需 systemd/root（全程 LATX_DEV=1）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
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
mkdir -p "$FAKE"
# latx stamp（模拟 CI release.yml 的 sed）
sed -e "s|{{LATTIX_VERSION}}|$VERSION|g" -e "s|{{GITHUB_REPO}}|$REPO|g" \
    "$ROOT/scripts/latx.sh" > "$WORK/latx"
# 面板 tarball：嵌入前端的 backend + latx
mkdir -p "$WORK/pkg/lattix-panel" "$WORK/static"
cp "$WORK/lattix-backend" "$WORK/pkg/lattix-panel/lattix-backend"
echo '<html>lattix e2e</html>' > "$WORK/static/index.html"
cp "$WORK/latx" "$WORK/pkg/lattix-panel/"
tar -C "$WORK/pkg" -czf "$FAKE/lattix-panel-linux-amd64.tar.gz" lattix-panel
(cd "$FAKE" && sha256sum lattix-panel-linux-amd64.tar.gz > checksums.txt)

run_install_with() {
    LATX_DEV=1 LATX_ROOT="$PANEL_ROOT" LATX_UNIT="$UNIT" LATTIX_STATIC="$WORK/static" \
    LATX_BIN="$LATX_BIN" LATX_RELEASE_BASE="file://$FAKE" \
        GITHUB_REPO="$REPO" bash "$ROOT/scripts/install-panel.sh" \
        --mode native --version "$VERSION" --bind 127.0.0.1 --port 18081 "$@"
}

run_install() {
    run_install_with --admin-pass e2e-admin-pass
}

latx() {
    LATX_DEV=1 LATX_ROOT="$PANEL_ROOT" LATX_UNIT="$UNIT" LATX_BIN="$LATX_BIN" \
        bash "$LATX_BIN" "$@"
}

# rpc_code <method> <path> [body]：输出 RPC 信封 code。
rpc_code() {
    local args=(-s -H "Origin: http://$ADDR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "http://$ADDR$2" | python3 -c 'import json,sys;print(json.load(sys.stdin)["code"])'
}

echo ">> 用例 1: checksum 篡改应中止安装"
cp "$FAKE/lattix-panel-linux-amd64.tar.gz" "$FAKE/panel.tar.gz.bak"
echo tampered >> "$FAKE/lattix-panel-linux-amd64.tar.gz"
if run_install >"$WORK/tamper.log" 2>&1; then
    echo "FAIL: 篡改的面板包未被 checksum 拦下"; exit 1
fi
grep -q "lattix-panel-linux-amd64.tar.gz SHA256 verification failed" "$WORK/tamper.log" \
    && echo "OK: 篡改资产被 checksums.txt 拦下" \
    || { echo "FAIL: 报错信息不符"; cat "$WORK/tamper.log"; exit 1; }
mv "$FAKE/panel.tar.gz.bak" "$FAKE/lattix-panel-linux-amd64.tar.gz"

echo ">> 用例 2: 正常安装（LATX_DEV=1 降级，无 systemd）"
run_install | tee "$WORK/install.log"

[[ -x "$PANEL_ROOT/lattix-backend" ]] \
    && echo "OK: lattix-backend 就位" || { echo "FAIL: lattix-backend 缺失"; exit 1; }
[[ -x "$LATX_BIN" ]] \
    && echo "OK: latx 已安装" || { echo "FAIL: latx 未安装到 $LATX_BIN"; exit 1; }

grep -q "访问地址: http://127.0.0.1:18081" "$WORK/install.log" \
    && echo "OK: 成功输出含面板地址" || { echo "FAIL: 成功输出缺面板地址"; cat "$WORK/install.log"; exit 1; }
grep -q ">> 正在下载 Lattix Panel $VERSION" "$WORK/install.log" \
    && echo "OK: panel 依赖下载前显示进度提示" \
    || { echo "FAIL: panel 下载提示缺失"; cat "$WORK/install.log"; exit 1; }
grep -q "管理员:   admin" "$WORK/install.log" && grep -q "初始密码: e2e-admin-pass" "$WORK/install.log" \
    && echo "OK: 成功输出含默认账号提示" || { echo "FAIL: 成功输出缺默认账号"; exit 1; }

[[ "$(curl -s -o /dev/null -w '%{http_code}' "http://$ADDR/")" == "200" ]] \
    && echo "OK: 面板 200" || { echo "FAIL: 面板未响应"; cat "$PANEL_ROOT/panel.log"; exit 1; }

SETTINGS_JAR="$WORK/settings-cookies.txt"
curl -s -c "$SETTINGS_JAR" -H "Origin: http://$ADDR" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"e2e-admin-pass"}' "http://$ADDR/api/auth/login" >/dev/null
TLS_DIR="$(curl -s -b "$SETTINGS_JAR" -H "Origin: http://$ADDR" "http://$ADDR/api/setting/get" \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["tls_dir"])')"
[[ "$TLS_DIR" == "$PANEL_ROOT/data/certs" ]] \
    && echo "OK: 面板证书目录统一到 data/certs" \
    || { echo "FAIL: 证书目录应为 $PANEL_ROOT/data/certs，实际 $TLS_DIR"; exit 1; }

echo ">> 用例 3: 无凭据参数重装保留已有管理员配置"
run_install_with >"$WORK/reinstall.log"
grep -q "初始密码: e2e-admin-pass" "$WORK/reinstall.log" \
    && echo "OK: 重装保留已有管理员密码" \
    || { echo "FAIL: 重装未保留管理员密码"; cat "$WORK/reinstall.log"; exit 1; }
[[ "$(rpc_code POST /api/auth/login '{"username":"admin","password":"e2e-admin-pass"}')" == "OK" ]] \
    && echo "OK: 保留的管理员密码仍可登录" || { echo "FAIL: 重装后登录"; exit 1; }

echo ">> 用例 4: latx status / version（LATX_DEV=1 进程检查）"
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
MENU_EN_OUT="$(printf '\n13\n\n0\n' | latx)"
[[ "$MENU_EN_OUT" == *"Select language / 选择语言"* && "$MENU_EN_OUT" == *"Lattix Panel Operations"* \
    && "$MENU_EN_OUT" == *"Panel Service"* && "$MENU_EN_OUT" == *"HTTPS Certificates"* \
    && "$MENU_EN_OUT" == *"$VERSION"* ]] \
    && echo "OK: latx 语言选择默认进入英文菜单并可执行菜单项" \
    || { echo "FAIL: latx 默认英文菜单: $MENU_EN_OUT"; exit 1; }
MENU_ZH_OUT="$(printf '2\n13\n\n0\n' | latx)"
[[ "$MENU_ZH_OUT" == *"Lattix 面板运维菜单"* && "$MENU_ZH_OUT" == *"面板服务"* \
    && "$MENU_ZH_OUT" == *"HTTPS 证书"* && "$MENU_ZH_OUT" == *"$VERSION"* ]] \
    && echo "OK: latx 可选择中文菜单并执行菜单项" \
    || { echo "FAIL: latx 中文菜单: $MENU_ZH_OUT"; exit 1; }

echo ">> 用例 5: latx reset-admin（改密即全部会话失效，§10）"
JAR="$WORK/cookies.txt"
[[ "$(rpc_code POST /api/auth/login '{"username":"admin","password":"e2e-admin-pass"}')" == "OK" ]] \
    && echo "OK: 默认密码可登录" || { echo "FAIL: 默认密码登录"; exit 1; }
if latx reset-admin short 2>"$WORK/short.log"; then
    echo "FAIL: 短密码未被拒绝"; exit 1
fi
grep -q "至少 8 位" "$WORK/short.log" \
    && echo "OK: 短密码被拒绝（≥8 位）" || { echo "FAIL: 短密码报错不符"; cat "$WORK/short.log"; exit 1; }
latx reset-admin newpass456 | grep -q "所有会话失效" \
    && echo "OK: reset-admin 成功且提示会话失效" || { echo "FAIL: reset-admin"; exit 1; }
[[ "$(rpc_code POST /api/auth/login '{"username":"admin","password":"newpass456"}')" == "OK" ]] \
    && echo "OK: 新密码可登录" || { echo "FAIL: 新密码登录"; exit 1; }
[[ "$(rpc_code POST /api/auth/login '{"username":"admin","password":"e2e-admin-pass"}')" == "AUTH_INVALID_CREDENTIALS" ]] \
    && echo "OK: 旧默认密码已失效" || { echo "FAIL: 旧密码仍可用"; exit 1; }
# 旧会话失效检查（用改密前的 cookie）
curl -s -c "$JAR" -H "Origin: http://$ADDR" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"newpass456"}' "http://$ADDR/api/auth/login" >/dev/null
ME_CODE="$(curl -s -b "$JAR" -H "Origin: http://$ADDR" "http://$ADDR/api/auth/me" \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["code"])')"
# 先用新密码登录获取有效会话，再验证改密前旧会话（SETTINGS_JAR）已失效
OLD_ME_CODE="$(curl -s -b "$SETTINGS_JAR" -H "Origin: http://$ADDR" "http://$ADDR/api/auth/me" \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["code"])')"
[[ "$OLD_ME_CODE" == "AUTH_REQUIRED" ]] \
    && echo "OK: 改密前签发的会话已失效" || { echo "FAIL: 旧会话仍有效（code=$OLD_ME_CODE）"; exit 1; }

echo "E2E-INSTALL-PANEL PASS"
