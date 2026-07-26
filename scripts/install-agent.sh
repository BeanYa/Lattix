#!/usr/bin/env bash
# Lattix Agent 引导安装脚本（设计文档 §11）。
#
# 由根 install.sh 或面板"添加服务器"命令调用，形如：
#   install-agent.sh --version vX.Y.Z --panel <PANEL_URL> --token <BOOTSTRAP_TOKEN>
#
# Agent 二进制统一为 release 钉版获取（lattix-agent-linux-<arch>.tar.gz = agent + latx-ag，
# 与同目录 checksums.txt 校验，获取不到即中止），统一从 GitHub Release 下载。
#
# 流程：解析/下载指定版本 xray-core（校验官方 .dgst SHA2-256）→ 下载/安装 Agent 二进制
# 与 latx-ag 节点管理程序 → 注册 systemd（user 用户态模式改为守护脚本常驻，见下）→
# 写入面板地址与 bootstrap token（清除旧 state）。
# Agent 首连后以 bootstrap token 换发长期凭证。
#
# 环境变量覆盖（e2e/运维用）：
#   LATX_RELEASE_BASE   下载基址覆盖（e2e 本地模拟用，支持 file://）
#   LATX_PREFIX         路径前缀（默认空；/usr/local/bin、/etc/systemd/system、state 等全部加前缀；
#                       user 模式且非 root 时默认 $HOME/.lattix）
#   LATX_DEV=1          无 systemd/非 root 开发模式：跳过 unit 注册，nohup 直接启动 agent；
#                       xray 不下载，由 XRAY_BIN 指定的本机二进制复制安装
#   LATX_USER_MODE=1    强制 user 用户态模式（root + systemd 机器亦可用）；无 root/无 systemd 且
#                       未设 LATX_DEV 时自动落入（不再中止）：不注册 systemd，xray/agent 正常
#                       下载校验，agent 由守护脚本 lattix-agent-run 常驻，crontab @reboot
#                       best-effort 注册开机自启
#   XRAY_BIN            本机 xray 二进制复制安装源：LATX_DEV=1 时必填；其余模式为跨模式覆盖——
#                       显式设置且可执行时复制安装（e2e/运维用），未设置则正常下载（官方 .dgst 校验）
#   LATX_AG_XRAY_API    DEV/user 模式 agent 的 -xray-api（默认 127.0.0.1:10085）
#   LATX_BBR_SYSCTL     sysctl 命令覆盖（e2e 用）
#   LATX_BBR_MODPROBE   modprobe 命令覆盖（e2e 用）
#   LATX_BBR_CONFIG     BBR 持久化文件覆盖（e2e 用）
#   LATX_BBR_TEST=1     DEV/非 root 下仍执行 BBR 流程（仅限配合上述覆盖进行 e2e）
#   LATX_BBR_TEST_ONLY=1 仅执行 BBR 流程后退出（e2e 用）
set -euo pipefail

# ===== CI 发版烧入区（release.yml 在发版时 sed 替换以下三个占位符）=====
LATTIX_VERSION="${LATTIX_VERSION:-{{LATTIX_VERSION}}}"
GITHUB_REPO="${GITHUB_REPO:-{{GITHUB_REPO}}}"
# 烧入后作为 --xray-version / XRAY_VERSION 的默认值（仍可被显式覆盖）。
DEFAULT_XRAY_VERSION="${DEFAULT_XRAY_VERSION:-{{DEFAULT_XRAY_VERSION}}}"

# latest 解析失败时的回退钉住版本。
FALLBACK_XRAY_VERSION="v26.3.27"

PANEL_URL=""
BOOTSTRAP_TOKEN=""
# xray 版本默认值：CI 烧入的 DEFAULT_XRAY_VERSION 优先；占位符未替换时维持
# latest（执行时经 GitHub API 解析）。--xray-version 参数与
# XRAY_VERSION 环境变量始终可覆盖默认值。
if [[ "$DEFAULT_XRAY_VERSION" != *"{{"* ]]; then
    XRAY_VERSION="${XRAY_VERSION:-$DEFAULT_XRAY_VERSION}"
else
    XRAY_VERSION="${XRAY_VERSION:-latest}"
fi

die() { echo "install.sh: $*" >&2; exit 1; }

BBR_WARNING=""
BBR_SYSCTL="${LATX_BBR_SYSCTL:-sysctl}"
BBR_MODPROBE="${LATX_BBR_MODPROBE:-modprobe}"
BBR_CONFIG="${LATX_BBR_CONFIG:-/etc/sysctl.d/99-lattix-bbr.conf}"

one_line() {
    local value="$1"
    value="${value//$'\r'/ }"
    value="${value//$'\n'/; }"
    printf '%s' "$value"
}

enable_bbr_best_effort() {
    local current="" available="" detail="" config_dir="" config_tmp=""
    BBR_WARNING=""

    if ! command -v "$BBR_SYSCTL" >/dev/null 2>&1; then
        BBR_WARNING="缺少 sysctl 命令"
        return
    fi

    current="$("$BBR_SYSCTL" -n net.ipv4.tcp_congestion_control 2>/dev/null || true)"
    if [[ "$current" == "bbr" ]]; then
        return
    fi

    # DEV 默认不得修改开发宿主；测试可用隔离的 sysctl/config 替身显式放行。
    if [[ "${LATX_DEV:-0}" == "1" && "${LATX_BBR_TEST:-0}" != "1" ]]; then
        BBR_WARNING="LATX_DEV=1 开发模式未修改宿主机 sysctl"
        return
    fi
    if [[ "$(id -u)" -ne 0 && "${LATX_BBR_TEST:-0}" != "1" ]]; then
        BBR_WARNING="需要 root 权限修改 sysctl"
        return
    fi

    if ! available="$("$BBR_SYSCTL" -n net.ipv4.tcp_available_congestion_control 2>&1)"; then
        BBR_WARNING="无法读取可用拥塞算法：$(one_line "$available")"
        return
    fi
    if ! grep -qw bbr <<<"$available"; then
        if command -v "$BBR_MODPROBE" >/dev/null 2>&1; then
            if ! detail="$("$BBR_MODPROBE" tcp_bbr 2>&1)"; then
                detail="$(one_line "$detail")"
            else
                detail=""
            fi
        else
            detail="缺少 modprobe 命令"
        fi
        if ! available="$("$BBR_SYSCTL" -n net.ipv4.tcp_available_congestion_control 2>&1)"; then
            BBR_WARNING="加载 tcp_bbr 后仍无法读取可用拥塞算法：$(one_line "$available")"
            return
        fi
        if ! grep -qw bbr <<<"$available"; then
            BBR_WARNING="内核未提供 BBR${detail:+：$detail}"
            return
        fi
    fi

    # fq 对 BBR 有利，但虚拟网卡可能忽略或拒绝它；不参与最终成功判定。
    "$BBR_SYSCTL" -w net.core.default_qdisc=fq >/dev/null 2>&1 || true
    if ! detail="$("$BBR_SYSCTL" -w net.ipv4.tcp_congestion_control=bbr 2>&1)"; then
        BBR_WARNING="sysctl 设置 BBR 被拒绝：$(one_line "$detail")"
        return
    fi
    current="$("$BBR_SYSCTL" -n net.ipv4.tcp_congestion_control 2>/dev/null || true)"
    if [[ "$current" != "bbr" ]]; then
        BBR_WARNING="sysctl 返回成功但当前拥塞算法仍为 ${current:-未知}"
        return
    fi

    # 只在本次确实启用 BBR 后写 Lattix 独立配置；已启用场景保持系统原配置不变。
    config_dir="$(dirname "$BBR_CONFIG")"
    if ! detail="$(mkdir -p "$config_dir" 2>&1)"; then
        BBR_WARNING="BBR 已生效但无法创建持久化目录：$(one_line "$detail")"
        return
    fi
    if ! config_tmp="$(mktemp "${BBR_CONFIG}.tmp.XXXXXX" 2>/dev/null)"; then
        BBR_WARNING="BBR 已生效但无法创建持久化临时文件"
        return
    fi
    if ! printf '%s\n' \
        'net.core.default_qdisc=fq' \
        'net.ipv4.tcp_congestion_control=bbr' >"$config_tmp"; then
        rm -f "$config_tmp"
        BBR_WARNING="BBR 已生效但无法写入持久化配置 $BBR_CONFIG"
        return
    fi
    chmod 0644 "$config_tmp" 2>/dev/null || true
    if ! detail="$(mv "$config_tmp" "$BBR_CONFIG" 2>&1)"; then
        rm -f "$config_tmp"
        BBR_WARNING="BBR 已生效但无法持久化到 $BBR_CONFIG：$(one_line "$detail")"
    fi
}

print_bbr_warning() {
    if [[ -n "$BBR_WARNING" ]]; then
        echo "WARNING: TCP BBR 未完整启用：$BBR_WARNING" >&2
    fi
}

if [[ "${LATX_BBR_TEST_ONLY:-0}" == "1" ]]; then
    enable_bbr_best_effort
    print_bbr_warning
    exit 0
fi

while [[ $# -gt 0 ]]; do
    case "$1" in
        --panel)         PANEL_URL="${2:?--panel requires a value}"; shift 2 ;;
        --token)         BOOTSTRAP_TOKEN="${2:?--token requires a value}"; shift 2 ;;
        --version)       LATTIX_VERSION="${2:?--version requires a value}"; shift 2 ;;
        --xray-version)  XRAY_VERSION="${2:?--xray-version requires a value}"; shift 2 ;;
        *)               die "unknown argument: $1" ;;
    esac
done

[[ -n "$PANEL_URL" ]]       || die "--panel is required"
[[ -n "$BOOTSTRAP_TOKEN" ]] || die "--token is required"
[[ "$LATTIX_VERSION" != *"{{"* ]] || die "--version is required"
[[ "$GITHUB_REPO" != *"{{"* ]] || die "GITHUB_REPO 未配置"
command -v curl      >/dev/null || die "curl is required"
command -v sha256sum >/dev/null || die "sha256sum is required"

# --- 运行模式判定（三档）---
# DEV：无 systemd/非 root 且 LATX_DEV=1（现有开发降级语义不变）；
# systemd：root + systemctl 且未设 LATX_USER_MODE=1（现有行为不变）；
# user：其余情况——非 root/无 systemd 自动落入（不再中止），root 可用 LATX_USER_MODE=1 强制。
DEV_MODE=0
USER_MODE=0
if ! command -v systemctl >/dev/null || [[ "$(id -u)" -ne 0 ]]; then
    if [[ "${LATX_DEV:-0}" == "1" ]]; then
        DEV_MODE=1
        echo ">> [DEV] 无 systemd 或非 root，跳过 unit 注册，agent 将由 nohup 直接启动"
    else
        USER_MODE=1
        echo ">> [user] 无 root 或无 systemd，进入用户态模式：不注册 unit，agent 由守护脚本 lattix-agent-run 常驻"
    fi
elif [[ "${LATX_USER_MODE:-0}" == "1" ]]; then
    USER_MODE=1
    echo ">> [user] LATX_USER_MODE=1，强制用户态模式：不注册 unit，agent 由守护脚本 lattix-agent-run 常驻"
fi

# 路径变量（在模式判定之后确定）：user 模式未显式指定 LATX_PREFIX 时，
# 非 root 默认 $HOME/.lattix，root 默认系统路径（空前缀）。
if [[ "$USER_MODE" -eq 1 && -z "${LATX_PREFIX:-}" && "$(id -u)" -ne 0 ]]; then
    PREFIX="$HOME/.lattix"
    echo ">> [user] LATX_PREFIX 未设置，安装到 $PREFIX"
else
    PREFIX="${LATX_PREFIX:-}"
fi
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

PANEL_URL="${PANEL_URL%/}"
case "$(uname -m)" in
    x86_64)  XRAY_ASSET="Xray-linux-64.zip";        AGENT_ARCH="amd64" ;;
    aarch64) XRAY_ASSET="Xray-linux-arm64-v8a.zip"; AGENT_ARCH="arm64" ;;
    *)       die "unsupported arch: $(uname -m)" ;;
esac

# 重装场景：先停掉运行中的服务/进程，避免覆写运行中的二进制失败（ETXTBSY）；
# 全新安装时该命令为空操作。
if [[ "$DEV_MODE" -eq 0 && "$USER_MODE" -eq 0 ]]; then
    systemctl stop lattix-agent.service xray.service 2>/dev/null || true
else
    # DEV/user：停守护脚本与 agent（user 模式先停守护，避免其循环拉起 agent）。
    pkill -f "$BIN_DIR/lattix-agent-run" 2>/dev/null || true
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
elif [[ -n "${XRAY_BIN:-}" && -x "${XRAY_BIN:-}" ]]; then
    # XRAY_BIN 跨模式覆盖：非 DEV 模式显式指定可执行的本机二进制时同样复制安装（e2e/运维用）。
    echo ">> installing xray-core（复制本机 $XRAY_BIN，XRAY_BIN 覆盖）"
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

# agent 包下载基址：钉到目标版本的 GitHub Release。
# LATX_RELEASE_BASE 可覆盖下载基址（e2e 本地模拟，支持 file://）。
command -v tar >/dev/null || die "tar is required"
RELEASE_BASE="${LATX_RELEASE_BASE:-https://github.com/${GITHUB_REPO}/releases/download/${LATTIX_VERSION}}"
echo ">> installing lattix-agent ${LATTIX_VERSION}（source: GitHub）"
curl -fsSL -o "$TMP_DIR/lattix-agent-linux-${AGENT_ARCH}.tar.gz" \
    "${RELEASE_BASE}/lattix-agent-linux-${AGENT_ARCH}.tar.gz"
# 校验文件获取不到即中止，不降级跳过。
curl -fsSL -o "$TMP_DIR/checksums.txt" "${RELEASE_BASE}/checksums.txt" \
    || die "未获取到校验文件 checksums.txt，中止安装"
(cd "$TMP_DIR" && grep "lattix-agent-linux-${AGENT_ARCH}\.tar\.gz\$" checksums.txt | sha256sum -c - >/dev/null) \
    || die "agent 包 SHA256 校验失败（checksums.txt）"
echo ">> agent 包 SHA256 校验通过（checksums.txt）"
tar -C "$TMP_DIR" -xzf "$TMP_DIR/lattix-agent-linux-${AGENT_ARCH}.tar.gz"
[[ -f "$TMP_DIR/lattix-agent/lattix-agent" && -f "$TMP_DIR/lattix-agent/latx-ag" ]] \
    || die "agent 包内容异常（缺 lattix-agent 或 latx-ag）"
AGENT_SRC="$TMP_DIR/lattix-agent/lattix-agent"
LATX_AG_SRC="$TMP_DIR/lattix-agent/latx-ag"
install -m 0755 "$AGENT_SRC" "$AGENT_BIN"
install -m 0755 "$LATX_AG_SRC" "$LATX_AG_BIN"
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

if [[ "$DEV_MODE" -eq 0 && "$USER_MODE" -eq 0 ]]; then
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
elif [[ "$USER_MODE" -eq 1 ]]; then
    # user 用户态模式：不注册 systemd，生成守护脚本常驻（flock 防重复 + 退出后自动拉起，
    # 替代 systemd Restart=always），并 best-effort 注册 crontab @reboot 开机自启。
    echo ">> [user] 生成守护脚本 $BIN_DIR/lattix-agent-run（日志 $AGENT_LOG）"
    mkdir -p "$(dirname "$AGENT_LOG")" "$PREFIX/var/run"
    cat > "$BIN_DIR/lattix-agent-run" <<EOF
#!/usr/bin/env bash
# Lattix Agent 用户态守护脚本（install.sh 生成）：flock 防重复；agent 退出（崩溃/自升级）
# 后 sleep 5 自动拉起，替代 systemd Restart=always；每次重启重新读取 env（token 轮换后即生效）。
set -u
exec 9>"$PREFIX/var/run/lattix-agent.lock"
flock -n 9 || exit 0
while true; do
    set -a; . "$ENV_FILE"; set +a
    "$AGENT_BIN" -panel "\$LATTIX_PANEL_WS" -token "\$LATTIX_TOKEN" -state "$STATE_FILE" \\
        -xray-bin "$XRAY_BIN_DST" -xray-config "$XRAY_CONFIG_DIR/config.json" \\
        -xray-api "$XRAY_API" -xray-runner exec >>"$AGENT_LOG" 2>&1
    sleep 5
done
EOF
    chmod 0755 "$BIN_DIR/lattix-agent-run"
    nohup "$BIN_DIR/lattix-agent-run" >/dev/null 2>&1 &
    sleep 1
    if kill -0 $! 2>/dev/null; then
        AGENT_STATUS="[user] 守护脚本运行中（pid $!）"
    else
        AGENT_STATUS="[user] 守护脚本未运行（日志见 $AGENT_LOG）"
    fi
    # best-effort 开机自启：crontab @reboot（先 grep 去重）；无 crontab 时提示手动启动。
    if command -v crontab >/dev/null; then
        if crontab -l 2>/dev/null | grep -qF "$BIN_DIR/lattix-agent-run"; then
            echo ">> [user] crontab @reboot 自启已存在，跳过"
        else
            (crontab -l 2>/dev/null || true; echo "@reboot $BIN_DIR/lattix-agent-run >>$AGENT_LOG 2>&1 &") | crontab -
            echo ">> [user] 已注册 crontab @reboot 开机自启（best-effort）"
        fi
    else
        echo ">> [user] 未检测到 crontab，无法注册开机自启；重启后需手动运行 latx-ag start"
    fi
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

# BBR 是机器级优化，放在 Agent 服务成功启动之后 best-effort 执行；任何失败只缓存原因，
# 不改变安装退出码。WARNING 必须等全部成功输出结束后再打印。
enable_bbr_best_effort

# --- 成功输出 ---
MODE_LABEL="systemd"
USER_HINT=""
if [[ "$DEV_MODE" -eq 1 ]]; then
    MODE_LABEL="[DEV]"
elif [[ "$USER_MODE" -eq 1 ]]; then
    MODE_LABEL="[user] 用户态"
    USER_HINT="
  日志文件:  $AGENT_LOG
  用户态启停: latx-ag start / stop"
fi
cat <<EOF

============================================================
  Lattix Agent 安装完成

  面板地址:  $PANEL_URL
  运行模式:  $MODE_LABEL
  Agent 状态: $AGENT_STATUS
  xray 版本:  $("$XRAY_BIN_DST" version 2>/dev/null | head -1 || echo unknown)$USER_HINT

  使用 latx-ag 命令运维本节点：
    latx-ag status / log / update / xray-update / uninstall
    latx-ag -h 查看帮助
============================================================
EOF
echo ">> done. Agent 将以 bootstrap token 首连面板并换发长期凭证（§11）。"
print_bbr_warning
