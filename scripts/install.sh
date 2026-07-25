#!/usr/bin/env bash
# Lattix Agent 引导安装脚本（设计文档 §11）。
#
# 由面板"添加服务器"生成一行安装命令，形如：
#   curl -fsSL <PANEL_URL>/install.sh | bash -s -- --panel <PANEL_URL> --token <BOOTSTRAP_TOKEN> --xray-version <latest|vX.Y.Z>
#
# Agent 二进制有两种获取方式：
#   1. release 钉版模式：本脚本由 CI 发版时烧入 LATTIX_VERSION / GITHUB_REPO /
#      DEFAULT_XRAY_VERSION（见 .github/workflows/release.yml），agent / latx-ag 与校验和
#      均从 GitHub release 资产下载（checksums.txt 校验，获取不到即中止）。
#   2. 面板托管模式：占位符未烧入（保留 "{{...}}"），agent / latx-ag 从面板 ${PANEL_URL}/dist
#      下载，校验面板注入的 SHA256（明文 HTTP 下的完整性锚点，§12）。
#
# 流程：解析/下载指定版本 xray-core（校验官方 .dgst SHA2-256）→ 下载/安装 Agent 二进制
# 与 latx-ag 节点管理程序 → 注册 systemd → 写入面板地址与 bootstrap token（清除旧 state）。
# Agent 首连后以 bootstrap token 换发长期凭证。
#
# 环境变量覆盖（e2e/运维用）：
#   LATX_RELEASE_BASE   release 钉版模式的下载基址覆盖（默认 GitHub release，e2e 本地模拟用，支持 file://）
#   LATX_PREFIX         路径前缀（默认空；/usr/local/bin、/etc/systemd/system、state 等全部加前缀）
#   LATX_DEV=1          无 systemd/非 root 开发模式：跳过 unit 注册，nohup 直接启动 agent；
#                       xray 不下载，由 XRAY_BIN 指定的本机二进制复制安装
#   XRAY_BIN            LATX_DEV=1 时的本机 xray 二进制（复制安装源）
#   LATX_AG_XRAY_API    DEV 模式 agent 的 -xray-api（默认 127.0.0.1:10085）
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
# latx-ag 节点管理程序与 agent 同等待遇：/dist 有 latx-ag 时注入其实际 SHA256。
LATX_AG_SHA256="{{LATX_AG_SHA256}}"

PREFIX="${LATX_PREFIX:-}"
BIN_DIR="$PREFIX/usr/local/bin"
AGENT_BIN="$BIN_DIR/lattix-agent"
LATX_AG_BIN="$BIN_DIR/latx-ag"
XRAY_BIN_DST="$BIN_DIR/xray"
XRAY_CONFIG_DIR="$PREFIX/usr/local/etc/xray"
ENV_FILE="$PREFIX/etc/lattix-agent.env"
STATE_FILE="$PREFIX/etc/lattix-agent.state.json"
SYSTEMD_DIR="$PREFIX/etc/systemd/system"
AGENT_LOG="$PREFIX/var/log/lattix-agent.log"
XRAY_API="${LATX_AG_XRAY_API:-127.0.0.1:10085}"
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
command -v curl      >/dev/null || die "curl is required"
command -v sha256sum >/dev/null || die "sha256sum is required"

# --- systemd / 权限检查：无 systemctl 或非 root 时需 LATX_DEV=1 进入开发降级模式 ---
DEV_MODE=0
if ! command -v systemctl >/dev/null || [[ "$(id -u)" -ne 0 ]]; then
    if [[ "${LATX_DEV:-0}" == "1" ]]; then
        DEV_MODE=1
        echo ">> [DEV] 无 systemd 或非 root，跳过 unit 注册，agent 将由 nohup 直接启动"
    else
        die "需要 root 且系统使用 systemd（开发/测试环境可用 LATX_DEV=1 降级运行）"
    fi
fi

PANEL_URL="${PANEL_URL%/}"
case "$(uname -m)" in
    x86_64)  XRAY_ASSET="Xray-linux-64.zip";        AGENT_ARCH="amd64" ;;
    aarch64) XRAY_ASSET="Xray-linux-arm64-v8a.zip"; AGENT_ARCH="arm64" ;;
    *)       die "unsupported arch: $(uname -m)" ;;
esac

# 重装场景：先停掉运行中的服务/进程，避免覆写运行中的二进制失败（ETXTBSY）；
# 全新安装时该命令为空操作。
if [[ "$DEV_MODE" -eq 0 ]]; then
    systemctl stop lattix-agent.service xray.service 2>/dev/null || true
else
    pkill -f "$AGENT_BIN" 2>/dev/null || true
fi

mkdir -p "$BIN_DIR" "$XRAY_CONFIG_DIR" "$(dirname "$ENV_FILE")"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

if [[ "$DEV_MODE" -eq 1 ]]; then
    # DEV：xray 不下载，复制 XRAY_BIN 指定的本机二进制（e2e 用，避免重复下载大文件）。
    [[ -n "${XRAY_BIN:-}" && -x "${XRAY_BIN:-}" ]] \
        || die "[DEV] 需要 XRAY_BIN 指定本机 xray 二进制（复制安装源）"
    echo ">> [DEV] installing xray-core（复制本机 $XRAY_BIN）"
    install -m 0755 "$XRAY_BIN" "$XRAY_BIN_DST"
else
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

    command -v unzip >/dev/null || die "unzip is required"
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
    install -m 0755 "$TMP_DIR/xray/xray" "$XRAY_BIN_DST"
fi

# Agent 独占管理该配置文件（§6）；仅在不存在时写入占位配置。
if [[ ! -f "$XRAY_CONFIG_DIR/config.json" ]]; then
    cat > "$XRAY_CONFIG_DIR/config.json" <<'EOF'
{
  "inbounds": [],
  "outbounds": [{ "protocol": "freedom", "tag": "direct" }]
}
EOF
fi

if [[ "$LATTIX_VERSION" != *"{{"* ]]; then
    # release 钉版模式：从 GitHub release 下载 agent / latx-ag 与同 release 的
    # checksums.txt 校验。LATX_RELEASE_BASE 可覆盖下载基址（e2e 本地模拟，支持 file://）。
    RELEASE_BASE="${LATX_RELEASE_BASE:-https://github.com/${GITHUB_REPO}/releases/download/${LATTIX_VERSION}}"
    echo ">> installing lattix-agent ${LATTIX_VERSION}"
    curl -fsSL -o "$TMP_DIR/lattix-agent-linux-${AGENT_ARCH}" \
        "${RELEASE_BASE}/lattix-agent-linux-${AGENT_ARCH}"
    curl -fsSL -o "$TMP_DIR/latx-ag" "${RELEASE_BASE}/latx-ag" \
        || die "下载失败：release ${LATTIX_VERSION} 无 latx-ag 资产"
    # 校验文件获取不到即中止，不降级跳过。
    curl -fsSL -o "$TMP_DIR/checksums.txt" "${RELEASE_BASE}/checksums.txt" \
        || die "未获取到 release 校验文件 checksums.txt，中止安装"
    (cd "$TMP_DIR" && grep "lattix-agent-linux-${AGENT_ARCH}\$" checksums.txt | sha256sum -c - >/dev/null) \
        || die "agent 二进制 SHA256 校验失败（release ${LATTIX_VERSION} checksums.txt）"
    echo ">> agent 二进制 SHA256 校验通过（release checksums.txt）"
    (cd "$TMP_DIR" && grep "latx-ag\$" checksums.txt | sha256sum -c - >/dev/null) \
        || die "latx-ag SHA256 校验失败（release ${LATTIX_VERSION} checksums.txt）"
    echo ">> latx-ag SHA256 校验通过（release checksums.txt）"
else
    # 面板托管模式：从面板 dist 下载，校验面板注入的 SHA256（§11/§12）。
    # 与 release 模式一致：先落临时文件、校验通过后再安装到最终路径，
    # 避免校验失败时坏二进制已写入 $AGENT_BIN。
    echo ">> installing lattix-agent（面板托管模式）"
    curl -fsSL -o "$TMP_DIR/lattix-agent-linux-${AGENT_ARCH}" \
        "${PANEL_URL}/dist/lattix-agent-linux-${AGENT_ARCH}"
    curl -fsSL -o "$TMP_DIR/latx-ag" "${PANEL_URL}/dist/latx-ag" \
        || die "下载失败：面板 ${PANEL_URL}/dist 无 latx-ag"

    case "$AGENT_ARCH" in
        amd64) EXPECTED_AGENT="$AGENT_SHA256_AMD64" ;;
        arm64) EXPECTED_AGENT="$AGENT_SHA256_ARM64" ;;
    esac
    if [[ "$EXPECTED_AGENT" == *"{{"* || "$EXPECTED_AGENT" == "SKIP" || -z "$EXPECTED_AGENT" ]]; then
        echo ">> WARNING: 面板未注入 agent 校验和，跳过校验"
    else
        echo "$EXPECTED_AGENT  $TMP_DIR/lattix-agent-linux-${AGENT_ARCH}" | sha256sum -c - >/dev/null \
            || die "agent 二进制 SHA256 校验失败（期望 $EXPECTED_AGENT）"
        echo ">> agent 二进制 SHA256 校验通过"
    fi
    # latx-ag 与 agent 同等待遇：SKIP 语义一致。
    if [[ "$LATX_AG_SHA256" == *"{{"* || "$LATX_AG_SHA256" == "SKIP" || -z "$LATX_AG_SHA256" ]]; then
        echo ">> WARNING: 面板未注入 latx-ag 校验和，跳过校验"
    else
        echo "$LATX_AG_SHA256  $TMP_DIR/latx-ag" | sha256sum -c - >/dev/null \
            || die "latx-ag SHA256 校验失败（期望 $LATX_AG_SHA256）"
        echo ">> latx-ag SHA256 校验通过"
    fi
fi
install -m 0755 "$TMP_DIR/lattix-agent-linux-${AGENT_ARCH}" "$AGENT_BIN"
install -m 0755 "$TMP_DIR/latx-ag" "$LATX_AG_BIN"
echo ">> latx-ag 已安装到 $LATX_AG_BIN"

# http→ws / https→wss
PANEL_WS="$(echo "$PANEL_URL" | sed -e 's|^https://|wss://|' -e 's|^http://|ws://|')/api/agent/ws"

echo ">> writing $ENV_FILE"
install -m 0600 /dev/null "$ENV_FILE"
cat > "$ENV_FILE" <<EOF
LATTIX_PANEL_WS=${PANEL_WS}
LATTIX_TOKEN=${BOOTSTRAP_TOKEN}
EOF
# 重装/换发凭证时清除旧长期凭证，确保 agent 使用新 bootstrap token（§11）。
rm -f "$STATE_FILE"

if [[ "$DEV_MODE" -eq 0 ]]; then
    echo ">> registering systemd services"
    cat > "$SYSTEMD_DIR/xray.service" <<EOF
[Unit]
Description=Xray Service
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$XRAY_BIN_DST run -config $XRAY_CONFIG_DIR/config.json
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    cat > "$SYSTEMD_DIR/lattix-agent.service" <<EOF
[Unit]
Description=Lattix Agent
After=network-online.target xray.service
Wants=network-online.target

[Service]
EnvironmentFile=$ENV_FILE
ExecStart=$AGENT_BIN -panel "\${LATTIX_PANEL_WS}" -token "\${LATTIX_TOKEN}"
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable --now xray.service
    systemctl enable --now lattix-agent.service
    AGENT_STATUS="$(systemctl is-active lattix-agent.service 2>/dev/null || echo unknown)"
else
    echo ">> [DEV] nohup 启动 agent（日志 $AGENT_LOG）"
    mkdir -p "$(dirname "$AGENT_LOG")"
    nohup "$AGENT_BIN" -panel "$PANEL_WS" -token "$BOOTSTRAP_TOKEN" -state "$STATE_FILE" \
        -xray-bin "$XRAY_BIN_DST" -xray-config "$XRAY_CONFIG_DIR/config.json" \
        -xray-api "$XRAY_API" -xray-runner exec \
        >"$AGENT_LOG" 2>&1 &
    sleep 1
    if kill -0 $! 2>/dev/null; then
        AGENT_STATUS="[DEV] 进程运行中（pid $!）"
    else
        AGENT_STATUS="[DEV] 进程未运行（日志见 $AGENT_LOG）"
    fi
fi

# --- 成功输出 ---
cat <<EOF

============================================================
  Lattix Agent 安装完成

  面板地址:  $PANEL_URL
  Agent 状态: $AGENT_STATUS
  xray 版本:  $("$XRAY_BIN_DST" version 2>/dev/null | head -1 || echo unknown)

  使用 latx-ag 命令运维本节点：
    latx-ag status / log / update / xray-update / uninstall
    latx-ag -h 查看帮助
============================================================
EOF
echo ">> done. Agent 将以 bootstrap token 首连面板并换发长期凭证（§11）。"
