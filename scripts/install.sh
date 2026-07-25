#!/usr/bin/env bash
# Lattix Agent 引导安装脚本（设计文档 §11）。
#
# 由面板"添加服务器"生成一行安装命令，形如：
#   curl -fsSL <PANEL_URL>/install.sh | bash -s -- --panel <PANEL_URL> --token <BOOTSTRAP_TOKEN> --xray-version <latest|vX.Y.Z>
#
# Agent 二进制有两种获取方式：
#   1. release 钉版模式：本脚本由 CI 发版时烧入 LATTIX_VERSION / GITHUB_REPO /
#      DEFAULT_XRAY_VERSION（见 .github/workflows/release.yml），agent 与校验和
#      均从 GitHub release 资产下载（checksums.txt 校验，获取不到即中止）。
#   2. 面板托管模式：占位符未烧入（保留 "{{...}}"），agent 从面板 ${PANEL_URL}/dist
#      下载，校验面板注入的 SHA256（明文 HTTP 下的完整性锚点，§12）。
#
# 流程：解析/下载指定版本 xray-core（校验官方 .dgst SHA2-256）→ 下载/安装 Agent 二进制
# → 注册 systemd → 写入面板地址与 bootstrap token（清除旧 state）。
# Agent 首连后以 bootstrap token 换发长期凭证。
set -euo pipefail

# ===== CI 发版烧入区（release.yml 在发版时 sed 替换以下三个占位符）=====
# 占位符未被替换（含 "{{"）即"面板托管模式"；替换后进入"release 钉版模式"。
LATTIX_VERSION="{{LATTIX_VERSION}}"
GITHUB_REPO="{{GITHUB_REPO}}"
# 烧入后作为 --xray-version / XRAY_VERSION 的默认值（仍可被显式覆盖）。
DEFAULT_XRAY_VERSION="{{DEFAULT_XRAY_VERSION}}"

# latest 解析失败时的回退钉住版本。
FALLBACK_XRAY_VERSION="v26.3.27"

# 面板托管本脚本时注入的 agent 二进制 SHA256（明文 HTTP 下的完整性锚点，§12）；
# 未注入（保留占位符或为 SKIP）时跳过校验。
AGENT_SHA256_AMD64="{{AGENT_SHA256_AMD64}}"
AGENT_SHA256_ARM64="{{AGENT_SHA256_ARM64}}"

AGENT_BIN="/usr/local/bin/lattix-agent"
XRAY_BIN="/usr/local/bin/xray"
XRAY_CONFIG_DIR="/usr/local/etc/xray"
ENV_FILE="/etc/lattix-agent.env"
PANEL_URL=""
BOOTSTRAP_TOKEN=""
# xray 版本默认值：CI 烧入的 DEFAULT_XRAY_VERSION 优先（release 钉版模式）；
# 占位符未替换时维持 latest（执行时经 GitHub API 解析）。--xray-version 参数与
# XRAY_VERSION 环境变量始终可覆盖默认值。
if [[ "$DEFAULT_XRAY_VERSION" != *"{{"* ]]; then
    XRAY_VERSION="${XRAY_VERSION:-$DEFAULT_XRAY_VERSION}"
else
    XRAY_VERSION="${XRAY_VERSION:-latest}"
fi

die() { echo "install.sh: $*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --panel)         PANEL_URL="${2:?--panel requires a value}"; shift 2 ;;
        --token)         BOOTSTRAP_TOKEN="${2:?--token requires a value}"; shift 2 ;;
        --xray-version)  XRAY_VERSION="${2:?--xray-version requires a value}"; shift 2 ;;
        *)               die "unknown argument: $1" ;;
    esac
done

[[ -n "$PANEL_URL" ]]       || die "--panel is required"
[[ -n "$BOOTSTRAP_TOKEN" ]] || die "--token is required"
[[ "$(id -u)" -eq 0 ]]      || die "must run as root"
command -v curl      >/dev/null || die "curl is required"
command -v unzip     >/dev/null || die "unzip is required"
command -v sha256sum >/dev/null || die "sha256sum is required"

PANEL_URL="${PANEL_URL%/}"
case "$(uname -m)" in
    x86_64)  XRAY_ASSET="Xray-linux-64.zip";        AGENT_ARCH="amd64" ;;
    aarch64) XRAY_ASSET="Xray-linux-arm64-v8a.zip"; AGENT_ARCH="arm64" ;;
    *)       die "unsupported arch: $(uname -m)" ;;
esac

# 重装场景：先停掉运行中的服务，避免覆写运行中的二进制失败（ETXTBSY）；
# 全新安装时该命令为空操作。
if command -v systemctl >/dev/null; then
    systemctl stop lattix-agent.service xray.service 2>/dev/null || true
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

# latest：执行时经 GitHub API 解析最新 release（§11）；失败回退钉住版本。
if [[ "$XRAY_VERSION" == "latest" ]]; then
    echo ">> resolving latest xray version"
    XRAY_VERSION="$(curl -fsSL https://api.github.com/repos/XTLS/Xray-core/releases/latest \
        | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4)" || true
    if [[ -z "$XRAY_VERSION" ]]; then
        echo ">> WARNING: latest 解析失败，回退钉住版本 $FALLBACK_XRAY_VERSION"
        XRAY_VERSION="$FALLBACK_XRAY_VERSION"
    fi
fi

echo ">> installing xray-core ${XRAY_VERSION}"
curl -fsSL -o "$TMP_DIR/xray.zip" \
    "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_ASSET}"

# 校验官方 .dgst 中的 SHA2-256（§11）；获取不到校验文件时中止，不降级跳过。
curl -fsSL -o "$TMP_DIR/xray.dgst" \
    "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_ASSET}.dgst" \
    || die "未获取到 xray 官方 .dgst 校验文件，中止安装（§11 要求校验官方 checksums）"
EXPECTED="$(grep 'SHA2-256=' "$TMP_DIR/xray.dgst" | head -1 | cut -d' ' -f2)"
ACTUAL="$(sha256sum "$TMP_DIR/xray.zip" | cut -d' ' -f1)"
[[ -n "$EXPECTED" && "$EXPECTED" == "$ACTUAL" ]] \
    || die "xray 包校验和不匹配（期望 ${EXPECTED:-<空>}，实际 $ACTUAL）"
echo ">> xray 包 SHA2-256 校验通过"
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
if [[ "$LATTIX_VERSION" != *"{{"* ]]; then
    # release 钉版模式：从 GitHub release 下载 agent 与同 release 的 checksums.txt 校验。
    RELEASE_URL="https://github.com/${GITHUB_REPO}/releases/download/${LATTIX_VERSION}"
    curl -fsSL -o "$TMP_DIR/lattix-agent-linux-${AGENT_ARCH}" \
        "${RELEASE_URL}/lattix-agent-linux-${AGENT_ARCH}"
    # 校验文件获取不到即中止，不降级跳过。
    curl -fsSL -o "$TMP_DIR/checksums.txt" "${RELEASE_URL}/checksums.txt" \
        || die "未获取到 release 校验文件 checksums.txt，中止安装"
    (cd "$TMP_DIR" && grep "lattix-agent-linux-${AGENT_ARCH}\$" checksums.txt | sha256sum -c - >/dev/null) \
        || die "agent 二进制 SHA256 校验失败（release ${LATTIX_VERSION} checksums.txt）"
    echo ">> agent 二进制 SHA256 校验通过（release checksums.txt）"
    install -m 0755 "$TMP_DIR/lattix-agent-linux-${AGENT_ARCH}" "$AGENT_BIN"
else
    # 面板托管模式：从面板 dist 下载，校验面板注入的 SHA256（§11/§12）。
    # TODO: Agent 二进制的托管路径由面板构建发布流程决定（§11）。
    curl -fsSL -o "$AGENT_BIN" "${PANEL_URL}/dist/lattix-agent-linux-${AGENT_ARCH}"

    case "$AGENT_ARCH" in
        amd64) EXPECTED_AGENT="$AGENT_SHA256_AMD64" ;;
        arm64) EXPECTED_AGENT="$AGENT_SHA256_ARM64" ;;
    esac
    if [[ "$EXPECTED_AGENT" == *"{{"* || "$EXPECTED_AGENT" == "SKIP" || -z "$EXPECTED_AGENT" ]]; then
        echo ">> WARNING: 面板未注入 agent 校验和，跳过校验"
    else
        echo "$EXPECTED_AGENT  $AGENT_BIN" | sha256sum -c - >/dev/null \
            || die "agent 二进制 SHA256 校验失败（期望 $EXPECTED_AGENT）"
        echo ">> agent 二进制 SHA256 校验通过"
    fi
fi
chmod 0755 "$AGENT_BIN"

# http→ws / https→wss
PANEL_WS="$(echo "$PANEL_URL" | sed -e 's|^https://|wss://|' -e 's|^http://|ws://|')/api/agent/ws"

echo ">> writing $ENV_FILE"
install -m 0600 /dev/null "$ENV_FILE"
cat > "$ENV_FILE" <<EOF
LATTIX_PANEL_WS=${PANEL_WS}
LATTIX_TOKEN=${BOOTSTRAP_TOKEN}
EOF
# 重装/换发凭证时清除旧长期凭证，确保 agent 使用新 bootstrap token（§11）。
rm -f /etc/lattix-agent.state.json

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
