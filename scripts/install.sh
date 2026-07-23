#!/usr/bin/env bash
# Lattix Agent 引导安装脚本（设计文档 §11）。
#
# 由面板"添加服务器"生成一行安装命令，形如：
#   curl -fsSL <PANEL_URL>/install.sh | bash -s -- --panel <PANEL_URL> --token <BOOTSTRAP_TOKEN>
#
# 流程：按钉住的 xray 版本安装 xray-core → 安装 Agent 二进制 → 注册 systemd →
# 写入面板地址与 bootstrap token。Agent 首连后以 bootstrap token 换发长期凭证。
set -euo pipefail

# TODO: xray 版本应由面板配置项钉住并注入本脚本（§11），此处仅为占位缺省值。
# 注意：低于 v26 的 xray 与新版客户端（xray 26 / 新 mihomo 内核）Reality 握手不兼容，
# 且不支持 api.listen（gRPC 热操作通道），不要回退到 v25.x。
XRAY_VERSION="${XRAY_VERSION:-v26.3.27}"

AGENT_BIN="/usr/local/bin/lattix-agent"
XRAY_BIN="/usr/local/bin/xray"
XRAY_CONFIG_DIR="/usr/local/etc/xray"
ENV_FILE="/etc/lattix-agent.env"
PANEL_URL=""
BOOTSTRAP_TOKEN=""

die() { echo "install.sh: $*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --panel)  PANEL_URL="${2:?--panel requires a value}"; shift 2 ;;
        --token)  BOOTSTRAP_TOKEN="${2:?--token requires a value}"; shift 2 ;;
        *)        die "unknown argument: $1" ;;
    esac
done

[[ -n "$PANEL_URL" ]]       || die "--panel is required"
[[ -n "$BOOTSTRAP_TOKEN" ]] || die "--token is required"
[[ "$(id -u)" -eq 0 ]]      || die "must run as root"
command -v curl  >/dev/null || die "curl is required"
command -v unzip >/dev/null || die "unzip is required"

PANEL_URL="${PANEL_URL%/}"
case "$(uname -m)" in
    x86_64)  XRAY_ASSET="Xray-linux-64.zip";    AGENT_ARCH="amd64" ;;
    aarch64) XRAY_ASSET="Xray-linux-arm64-v8a.zip"; AGENT_ARCH="arm64" ;;
    *)       die "unsupported arch: $(uname -m)" ;;
esac

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo ">> installing xray-core ${XRAY_VERSION}"
curl -fsSL -o "$TMP_DIR/xray.zip" \
    "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_ASSET}"
unzip -o -q "$TMP_DIR/xray.zip" -d "$TMP_DIR/xray"
install -m 0755 "$TMP_DIR/xray/xray" "$XRAY_BIN"

# Agent 独占管理该配置文件（§6）；仅在不存在时写入占位配置。
mkdir -p "$XRAY_CONFIG_DIR"
if [[ ! -f "$XRAY_CONFIG_DIR/config.json" ]]; then
    cat > "$XRAY_CONFIG_DIR/config.json" <<'EOF'
{
  "inbounds": [],
  "outbounds": [{ "protocol": "freedom", "tag": "direct" }]
}
EOF
fi

echo ">> installing lattix-agent"
# TODO: Agent 二进制的托管路径由面板构建发布流程决定（§11）。
curl -fsSL -o "$AGENT_BIN" "${PANEL_URL}/dist/lattix-agent-linux-${AGENT_ARCH}"
chmod 0755 "$AGENT_BIN"

# http→ws / https→wss
PANEL_WS="$(echo "$PANEL_URL" | sed -e 's|^https://|wss://|' -e 's|^http://|ws://|')/api/agent/ws"

echo ">> writing $ENV_FILE"
install -m 0600 /dev/null "$ENV_FILE"
cat > "$ENV_FILE" <<EOF
LATTIX_PANEL_WS=${PANEL_WS}
LATTIX_TOKEN=${BOOTSTRAP_TOKEN}
EOF

echo ">> registering systemd services"
cat > /etc/systemd/system/xray.service <<'EOF'
[Unit]
Description=Xray Service
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/xray run -config /usr/local/etc/xray/config.json
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/lattix-agent.service <<'EOF'
[Unit]
Description=Lattix Agent
After=network-online.target xray.service
Wants=network-online.target

[Service]
EnvironmentFile=/etc/lattix-agent.env
ExecStart=/usr/local/bin/lattix-agent -panel "${LATTIX_PANEL_WS}" -token "${LATTIX_TOKEN}"
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now xray.service
systemctl enable --now lattix-agent.service

echo ">> done. Agent 将以 bootstrap token 首连面板并换发长期凭证（§11）。"
