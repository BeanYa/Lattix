#!/usr/bin/env bash
# Lattix 面板一键安装脚本（设计文档 §20）。
#
# 与 agent install.sh 一致走 release 钉版（CI 发版时烧入 LATTIX_VERSION / GITHUB_REPO，
# 见 .github/workflows/release.yml）：
#   curl -fsSL https://github.com/<repo>/releases/download/<ver>/install-panel.sh | bash
#   curl -fsSL .../install-panel.sh | bash -s -- <version>   # 指定版本
#
# 流程：架构/OS 检查（仅 linux/amd64）→ 解析目标版本（参数 > CI 烧入 > latest 经 GitHub API）
# → 下载面板 tarball / latx / latx-ag / install.sh / agent 两架构二进制（全部校验 release checksums.txt）
# → 解压到安装根目录 → 安装 latx → 注册并启动 systemd 服务 → 打印面板地址与默认账号。
# 已安装时执行 = 同版本重装/升级（停服 → 替换 → 启服，保留 DB）。
#
# 环境变量覆盖（e2e/运维用）：
#   LATX_ROOT          安装根目录（默认 /usr/local/lattix-panel）
#   LATX_UNIT          systemd unit 名（默认 lattix-panel）
#   LATX_ADDR          面板监听地址（默认 :8080，写入 unit ExecStart -addr）
#   LATX_BIN           latx 安装路径（默认 /usr/local/bin/latx）
#   LATX_RELEASE_BASE 下载基址覆盖（默认 GitHub release，e2e 本地模拟用，支持 file://）
#   LATX_DEV=1         无 systemd/非 root 开发模式：跳过 unit 注册，nohup 直接启动
set -euo pipefail

# ===== CI 发版烧入区（release.yml 在发版时 sed 替换以下两个占位符）=====
LATTIX_VERSION="{{LATTIX_VERSION}}"
GITHUB_REPO="{{GITHUB_REPO}}"

INSTALL_ROOT="${LATX_ROOT:-/usr/local/lattix-panel}"
UNIT="${LATX_UNIT:-lattix-panel}"
ADDR="${LATX_ADDR:-:8080}"
LATX_BIN="${LATX_BIN:-/usr/local/bin/latx}"

die() { echo "install-panel.sh: $*" >&2; exit 1; }

# --- 架构/OS 检查（面板仅有 linux/amd64 构建）---
[[ "$(uname -s)" == "Linux" ]] || die "仅支持 Linux（当前 $(uname -s)）"
case "$(uname -m)" in
    x86_64) ;;
    aarch64) die "面板无 linux/arm64 构建（仅 agent 提供 arm64），请在 amd64 机器上部署面板" ;;
    *)       die "unsupported arch: $(uname -m)（面板仅有 linux/amd64 构建）" ;;
esac

command -v curl      >/dev/null || die "curl is required"
command -v sha256sum >/dev/null || die "sha256sum is required"
command -v tar       >/dev/null || die "tar is required"

# --- systemd / 权限检查：无 systemctl 或非 root 时需 LATX_DEV=1 进入开发降级模式 ---
DEV_MODE=0
if ! command -v systemctl >/dev/null || [[ "$(id -u)" -ne 0 ]]; then
    if [[ "${LATX_DEV:-0}" == "1" ]]; then
        DEV_MODE=1
        echo ">> [DEV] 无 systemd 或非 root，跳过 unit 注册，面板将由 nohup 直接启动"
    else
        die "需要 root 且系统使用 systemd（开发/测试环境可用 LATX_DEV=1 降级运行）"
    fi
fi

# --- 解析目标版本：参数 > CI 烧入 >（自定义下载基址时 dev，否则 latest 经 GitHub API）---
VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
    if [[ "$LATTIX_VERSION" != *"{{"* ]]; then
        VERSION="$LATTIX_VERSION"
    elif [[ -n "${LATX_RELEASE_BASE:-}" ]]; then
        VERSION="dev"
    else
        echo ">> resolving latest lattix version"
        VERSION="$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
            | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4)" || true
        [[ -n "$VERSION" ]] || die "latest 解析失败（GitHub API 不可达？），请显式指定版本：bash -s -- <version>"
    fi
fi
RELEASE_BASE="${LATX_RELEASE_BASE:-https://github.com/${GITHUB_REPO}/releases/download/${VERSION}}"
echo ">> installing lattix panel ${VERSION}"

# 重装/升级场景：先停掉运行中的服务，避免覆写运行中的二进制失败（ETXTBSY）；
# 全新安装时为空操作。
if [[ "$DEV_MODE" -eq 0 ]]; then
    systemctl stop "$UNIT" 2>/dev/null || true
else
    pkill -f "$INSTALL_ROOT/lattix-backend" 2>/dev/null || true
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

# --- 下载 release 资产并逐一校验 checksums.txt（获取不到校验文件即中止，不降级跳过）---
ASSETS=(
    lattix-panel-linux-amd64.tar.gz
    latx
    latx-ag
    install.sh
    lattix-agent-linux-amd64
    lattix-agent-linux-arm64
)
for asset in "${ASSETS[@]}"; do
    curl -fsSL -o "$TMP_DIR/$asset" "$RELEASE_BASE/$asset" \
        || die "下载失败：$RELEASE_BASE/$asset"
done
curl -fsSL -o "$TMP_DIR/checksums.txt" "$RELEASE_BASE/checksums.txt" \
    || die "未获取到 release 校验文件 checksums.txt，中止安装"
for asset in "${ASSETS[@]}"; do
    (cd "$TMP_DIR" && grep "$asset\$" checksums.txt | sha256sum -c - >/dev/null) \
        || die "$asset SHA256 校验失败（checksums.txt）"
done
echo ">> 全部资产 SHA256 校验通过（checksums.txt）"

# --- 解压安装到根目录（保留既有 DB）---
tar -C "$TMP_DIR" -xzf "$TMP_DIR/lattix-panel-linux-amd64.tar.gz"
[[ -f "$TMP_DIR/lattix-panel/lattix-backend" ]] || die "面板包内容异常（缺 lattix-backend）"
mkdir -p "$INSTALL_ROOT/dist"
install -m 0755 "$TMP_DIR/lattix-panel/lattix-backend" "$INSTALL_ROOT/lattix-backend"
rm -rf "$INSTALL_ROOT/frontend-dist"
cp -r "$TMP_DIR/lattix-panel/frontend-dist" "$INSTALL_ROOT/frontend-dist"
# install.sh（agent 引导脚本，CI stamp 版）放根目录，供面板 /install.sh 端点托管（§11）。
install -m 0644 "$TMP_DIR/install.sh" "$INSTALL_ROOT/install.sh"
# agent 两架构二进制放 dist/，供面板托管模式回退下载（/dist/ 端点，§11）；
# latx-ag 节点管理程序同摆 dist/（面板据此向 install.sh 注入 LATX_AG_SHA256）。
install -m 0755 "$TMP_DIR/lattix-agent-linux-amd64" "$INSTALL_ROOT/dist/lattix-agent-linux-amd64"
install -m 0755 "$TMP_DIR/lattix-agent-linux-arm64" "$INSTALL_ROOT/dist/lattix-agent-linux-arm64"
install -m 0755 "$TMP_DIR/latx-ag" "$INSTALL_ROOT/dist/latx-ag"

# --- 安装 latx 面板管理程序 ---
mkdir -p "$(dirname "$LATX_BIN")"
install -m 0755 "$TMP_DIR/latx" "$LATX_BIN"
echo ">> latx 已安装到 $LATX_BIN"

# --- 注册并启动服务 ---
PANEL_ARGS="-addr $ADDR -db $INSTALL_ROOT/lattix.db -static $INSTALL_ROOT/frontend-dist -dist $INSTALL_ROOT/dist -install-script $INSTALL_ROOT/install.sh"
if [[ "$DEV_MODE" -eq 0 ]]; then
    echo ">> registering systemd service $UNIT"
    cat > "/etc/systemd/system/${UNIT}.service" <<EOF
[Unit]
Description=Lattix Panel
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$INSTALL_ROOT/lattix-backend $PANEL_ARGS
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable "$UNIT" >/dev/null 2>&1 || true
    systemctl restart "$UNIT"
else
    echo ">> [DEV] nohup 启动面板（日志 $INSTALL_ROOT/panel.log）"
    nohup "$INSTALL_ROOT/lattix-backend" $PANEL_ARGS >"$INSTALL_ROOT/panel.log" 2>&1 &
fi

# --- 等待端口起来 ---
PORT="${ADDR##*:}"
for _ in $(seq 1 30); do
    CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:${PORT}/" 2>/dev/null || true)"
    [[ -n "$CODE" && "$CODE" != "000" ]] && break
    sleep 1
done
[[ -n "${CODE:-}" && "$CODE" != "000" ]] \
    || die "面板未在 30s 内起来（端口 $PORT）；日志：journalctl -u $UNIT（或 $INSTALL_ROOT/panel.log）"

# --- 成功输出 ---
PUBLIC_IP="$(curl -s --max-time 5 ifconfig.me 2>/dev/null | tr -d '[:space:]' || true)"
if [[ -z "$PUBLIC_IP" ]]; then
    PUBLIC_IP="$(hostname -I 2>/dev/null | awk '{print $1}' | grep . || echo "127.0.0.1")"
fi
cat <<EOF

============================================================
  Lattix 面板安装完成（${VERSION}）

  面板地址:  http://${PUBLIC_IP}:${PORT}
  默认账号:  admin / lattix-admin
             ※ 生产环境请立即修改默认密码：
               latx reset-admin <新密码>，或登录后在设置页修改

  运维命令:  latx status / log / update / acme / reset-admin / uninstall
             latx -h 查看帮助
============================================================
EOF
