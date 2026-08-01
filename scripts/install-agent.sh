#!/usr/bin/env bash
# Lattix Agent 引导安装脚本。
#
# 由根 install.sh 或面板"添加服务器"命令调用，形如：
#   install-agent.sh --version vX.Y.Z --panel <PANEL_URL> --token <BOOTSTRAP_TOKEN>
#                    [--xray-version latest|vX.Y.Z]
#
# Agent 二进制统一为 release 钉版获取（lattix-agent-linux-<arch>.tar.gz = agent + latx-ag，
# 与同目录 checksums.txt 校验，获取不到即中止），统一从 GitHub Release 下载。
#
# 流程：解析/下载指定版本 xray-core（校验官方 .dgst SHA2-256）→ 下载/安装 Agent 二进制
# 与 latx-ag 节点管理程序 → 注册 systemd（user 用户态模式改为守护脚本常驻，见下）→
# 写入面板地址与 bootstrap token（保留旧 state，认证成功后由 Agent 判断重绑）。
# Agent 首连后以 bootstrap token 换发长期凭证。
#
# 环境变量覆盖（e2e/运维用）：
#   LATX_RELEASE_BASE   下载基址覆盖（e2e 本地模拟用，支持 file://）
#   LATX_PREFIX         路径前缀（默认空；应用根为 /opt/lattix-agent；
#                       user 模式且非 root 时使用 $HOME/.lattix-agent）
#   LATX_DEV=1          无 systemd/非 root 开发模式：跳过 unit 注册，nohup 直接启动 agent；
#                       xray 不下载，由 XRAY_BIN 指定的本机二进制复制安装
#   LATX_USER_MODE=1    强制 user 用户态模式（root + systemd 机器亦可用）；无 root/无 systemd 且
#                       未设 LATX_DEV 时自动落入（不再中止）：不注册 systemd，xray/agent 正常
#                       下载校验，agent 由守护脚本 lattix-agent-run 常驻，crontab @reboot
#                       best-effort 注册开机自启
#   XRAY_BIN            本机 xray 二进制复制安装源：LATX_DEV=1 时必填；其余模式为跨模式覆盖——
#                       显式设置且可执行时复制安装（e2e/运维用），未设置则正常下载（官方 .dgst 校验）
#   XRAY_VERSION        xray 版本覆盖（默认 latest；也可用 --xray-version 覆盖）
#   LATX_AG_XRAY_API    DEV/user 模式 agent 的 -xray-api（默认 127.0.0.1:10085）
#   LATX_BBR_SYSCTL     sysctl 命令覆盖（e2e 用）
#   LATX_BBR_MODPROBE   modprobe 命令覆盖（e2e 用）
#   LATX_BBR_CONFIG     BBR 持久化文件覆盖（e2e 用）
#   LATX_BBR_TEST=1     DEV/非 root 下仍执行 BBR 流程（仅限配合上述覆盖进行 e2e）
#   LATX_BBR_TEST_ONLY=1 仅执行 BBR 流程后退出（e2e 用）
#
# 依赖自愈：缺少 sha256sum/tar/unzip 时按发行版包管理器自动安装（root 直装；
# 非 root 有 sudo 走 sudo，都没有则报错提示手动安装）。unzip 仅 xray 下载分支
# 需要，DEV 模式或 XRAY_BIN 复制安装分支缺 unzip 不阻塞。
set -euo pipefail

# ===== CI 发版烧入区（release.yml 在发版时 sed 替换以下占位符）=====
LATTIX_VERSION="${LATTIX_VERSION:-}"
[[ -n "$LATTIX_VERSION" ]] || LATTIX_VERSION="{{LATTIX_VERSION}}"
GITHUB_REPO="${GITHUB_REPO:-}"
[[ -n "$GITHUB_REPO" ]] || GITHUB_REPO="{{GITHUB_REPO}}"

# latest 解析失败时的回退钉住版本。
FALLBACK_XRAY_VERSION="v26.3.27"

PANEL_URL=""
BOOTSTRAP_TOKEN=""
# 省略 --xray-version 时解析最新 release；环境变量和 CLI 参数均可覆盖。
XRAY_VERSION="${XRAY_VERSION:-latest}"

die() { echo "install.sh: $*" >&2; exit 1; }

download_file() {
    local label="$1" url="$2" destination="$3"
    echo ">> 正在下载 $label"
    if ! curl --fail --location --show-error --progress-bar \
        --output "$destination" "$url"; then
        return 1
    fi
    echo ">> $label 下载完成"
}

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

DEPS_PKG_MGR=""
DEPS_OS_ID=""

# 按 /etc/os-release 识别发行版包管理器；无法识别或对应命令不存在时返回非零。
detect_pkg_mgr() {
    DEPS_PKG_MGR=""
    DEPS_OS_ID=""
    [[ -r /etc/os-release ]] || return 1
    # shellcheck disable=SC1091
    . /etc/os-release
    DEPS_OS_ID="${ID:-unknown}"
    case "${ID:-}" in
        ubuntu|debian|armbian|linuxmint|pop|elementary|kali) DEPS_PKG_MGR="apt-get" ;;
        fedora)                                                DEPS_PKG_MGR="dnf" ;;
        centos|rhel|almalinux|rocky|ol|amzn)                   DEPS_PKG_MGR="yum" ;;
        alpine)                                                DEPS_PKG_MGR="apk" ;;
        arch|manjaro|endeavouros)                              DEPS_PKG_MGR="pacman" ;;
        opensuse*|sles|suse)                                   DEPS_PKG_MGR="zypper" ;;
    esac
    [[ -n "$DEPS_PKG_MGR" ]] && command -v "$DEPS_PKG_MGR" >/dev/null 2>&1
}

# 依赖自愈：命令已存在则幂等跳过；缺失时按发行版与权限自动安装，失败即中止。
ensure_deps() {
    local cmd missing=() pkgs=() pkg SUDO_PREFIX="" install_output=""
    for cmd in "$@"; do
        command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
    done
    [[ "${#missing[@]}" -eq 0 ]] && return 0

    local missing_display="${missing[*]}"
    for cmd in "${missing[@]}"; do
        case "$cmd" in
            unzip)     pkg="unzip" ;;
            tar)       pkg="tar" ;;
            sha256sum) pkg="coreutils" ;;
            *)         die "缺少 $cmd 且不支持自动安装，请手动安装后重试" ;;
        esac
        pkgs+=("$pkg")
    done

    detect_pkg_mgr \
        || die "缺少 $missing_display 且无法自动安装（系统 ${DEPS_OS_ID:-unknown} 无可用包管理器），请手动安装：${missing_display}"
    if [[ "$(id -u)" -ne 0 ]]; then
        command -v sudo >/dev/null 2>&1 \
            || die "缺少 $missing_display 且当前用户无 root/sudo 权限，无法自动安装；请以 root/sudo 重试或手动安装：${missing_display}"
        SUDO_PREFIX="sudo"
    fi

    echo ">> 缺少依赖 ${missing_display}，将通过 $DEPS_PKG_MGR 自动安装"
    case "$DEPS_PKG_MGR" in
        apt-get)
            if ! install_output="$($SUDO_PREFIX apt-get update -qq 2>&1)"; then
                die "apt-get update 失败，无法自动安装依赖（$(one_line "$install_output")）；请手动安装：${missing_display}"
            fi
            install_output="$($SUDO_PREFIX apt-get install -y --no-install-recommends "${pkgs[@]}" 2>&1)" \
                || die "依赖安装失败（apt-get）：$(one_line "$install_output")；请手动安装：${missing_display}"
            ;;
        dnf|yum)
            install_output="$($SUDO_PREFIX "$DEPS_PKG_MGR" install -y "${pkgs[@]}" 2>&1)" \
                || die "依赖安装失败（$DEPS_PKG_MGR）：$(one_line "$install_output")；请手动安装：${missing_display}"
            ;;
        apk)
            install_output="$($SUDO_PREFIX apk add --no-cache "${pkgs[@]}" 2>&1)" \
                || die "依赖安装失败（apk）：$(one_line "$install_output")；请手动安装：${missing_display}"
            ;;
        pacman)
            install_output="$($SUDO_PREFIX pacman -Sy --noconfirm "${pkgs[@]}" 2>&1)" \
                || die "依赖安装失败（pacman）：$(one_line "$install_output")；请手动安装：${missing_display}"
            ;;
        zypper)
            install_output="$($SUDO_PREFIX zypper --non-interactive install -y "${pkgs[@]}" 2>&1)" \
                || die "依赖安装失败（zypper）：$(one_line "$install_output")；请手动安装：${missing_display}"
            ;;
        *) die "不支持的包管理器：$DEPS_PKG_MGR" ;;
    esac

    for cmd in "${missing[@]}"; do
        command -v "$cmd" >/dev/null 2>&1 \
            || die "依赖 $cmd 安装后仍不可用；请手动安装后重试"
    done
    echo ">> 依赖已就绪：${missing_display}"
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
command -v curl >/dev/null || die "curl is required"

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

# --- 依赖自愈（幂等）：安装缺失的依赖 ---
# sha256sum/tar 恒需要（agent 包校验/解压）；unzip 仅 xray 下载分支需要，
# DEV 模式或 XRAY_BIN 复制安装分支缺 unzip 不阻塞。
REQUIRED_DEPS=(sha256sum tar)
if [[ "$DEV_MODE" -ne 1 && ! ( -n "${XRAY_BIN:-}" && -x "${XRAY_BIN:-}" ) ]]; then
    REQUIRED_DEPS+=(unzip)
fi
ensure_deps "${REQUIRED_DEPS[@]}"

# 路径变量（在模式判定之后确定）：user 模式未显式指定 LATX_PREFIX 时，
# 非 root 默认 $HOME/.lattix-agent，root 默认 /opt/lattix-agent。
if [[ "$USER_MODE" -eq 1 && -z "${LATX_PREFIX:-}" && "$(id -u)" -ne 0 ]]; then
    PREFIX=""
    APP_ROOT="$HOME/.lattix-agent"
    echo ">> [user] LATX_PREFIX 未设置，安装到 $APP_ROOT"
else
    PREFIX="${LATX_PREFIX:-}"
    APP_ROOT="$PREFIX/opt/lattix-agent"
fi
BIN_DIR="$APP_ROOT/bin"
AGENT_BIN="$BIN_DIR/lattix-agent"
LATX_AG_BIN="$BIN_DIR/latx-ag"
RUN_SCRIPT="$BIN_DIR/lattix-agent-run"
XRAY_BIN_DST="$BIN_DIR/xray"
CONFIG_DIR="$APP_ROOT/config"
DATA_DIR="$APP_ROOT/data"
LOG_DIR="$APP_ROOT/logs"
XRAY_CONFIG="$CONFIG_DIR/xray.json"
ENV_FILE="$CONFIG_DIR/agent.env"
STATE_FILE="$DATA_DIR/state.json"
SETTINGS_FILE="$DATA_DIR/settings.json"
SYSTEMD_DIR="$PREFIX/etc/systemd/system"
AGENT_LOG="$LOG_DIR/agent.log"
LATX_AG_LINK="$PREFIX/usr/local/bin/latx-ag"
XRAY_API="${LATX_AG_XRAY_API:-127.0.0.1:10085}"

process_pid() {
    local pattern="$1"
    command -v pgrep >/dev/null 2>&1 || return 0
    pgrep -f "$pattern" 2>/dev/null | sed -n '1p' || true
}

wait_user_processes_stopped() {
    local attempt
    for ((attempt = 0; attempt < 50; attempt++)); do
        if [[ -z "$(process_pid "$RUN_SCRIPT")" && -z "$(process_pid "$AGENT_BIN -panel")" ]]; then
            return 0
        fi
        sleep 0.1
    done
    return 1
}

stop_user_processes() {
    # Bash defers the runner's TERM trap while it waits for the agent child. Wait for both
    # processes so a replacement runner cannot lose the singleton-lock race during reinstall.
    pkill -f "$RUN_SCRIPT" 2>/dev/null || true
    pkill -f "$AGENT_BIN -panel" 2>/dev/null || true
    if wait_user_processes_stopped; then
        return
    fi
    echo ">> [user] 旧守护进程未及时退出，正在强制清理" >&2
    pkill -9 -f "$RUN_SCRIPT" 2>/dev/null || true
    pkill -9 -f "$AGENT_BIN -panel" 2>/dev/null || true
    wait_user_processes_stopped || die "无法停止旧的用户态 agent/守护进程，请手动检查后重试"
}

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
    stop_user_processes
fi

install -d -m 0755 "$BIN_DIR" "$LOG_DIR"
install -d -m 0700 "$CONFIG_DIR" "$DATA_DIR"
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
    # latest：执行时经 GitHub API 解析最新 release；失败回退钉住版本。
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
    download_file "xray-core ${XRAY_VERSION}" \
        "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_ASSET}" \
        "$TMP_DIR/xray.zip" || die "xray-core 下载失败"

    # 校验官方 .dgst 中的 SHA2-256；获取不到校验文件时中止，不降级跳过。
    download_file "xray-core 校验文件" \
        "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_ASSET}.dgst" \
        "$TMP_DIR/xray.dgst" \
        || die "未获取到 xray 官方 .dgst 校验文件，中止安装（必须校验官方 checksums，不降级跳过）"
    EXPECTED="$(grep 'SHA2-256=' "$TMP_DIR/xray.dgst" | head -1 | cut -d' ' -f2)"
    ACTUAL="$(sha256sum "$TMP_DIR/xray.zip" | cut -d' ' -f1)"
    [[ -n "$EXPECTED" && "$EXPECTED" == "$ACTUAL" ]] \
        || die "xray 包校验和不匹配（期望 ${EXPECTED:-<空>}，实际 $ACTUAL）"
    echo ">> xray 包 SHA2-256 校验通过"
    unzip -o -q "$TMP_DIR/xray.zip" -d "$TMP_DIR/xray"
    install -m 0755 "$TMP_DIR/xray/xray" "$XRAY_BIN_DST"
fi

# Agent 独占管理该配置文件；仅在不存在时写入完整基础配置。
if [[ ! -f "$XRAY_CONFIG" ]]; then
    cat > "$XRAY_CONFIG" <<EOF
{
  "log": {"loglevel": "warning"},
  "api": {"tag": "api", "listen": "$XRAY_API", "services": ["HandlerService", "StatsService"]},
  "stats": {},
  "policy": {
    "levels": {"0": {"statsUserUplink": true, "statsUserDownlink": true}},
    "system": {"statsInboundUplink": true, "statsInboundDownlink": true}
  },
  "inbounds": [],
  "outbounds": [{ "protocol": "freedom", "tag": "direct" }]
}
EOF
fi

# agent 包下载基址：钉到目标版本的 GitHub Release。
# LATX_RELEASE_BASE 可覆盖下载基址（e2e 本地模拟，支持 file://）。
RELEASE_BASE="${LATX_RELEASE_BASE:-https://github.com/${GITHUB_REPO}/releases/download/${LATTIX_VERSION}}"
echo ">> installing lattix-agent ${LATTIX_VERSION}（source: GitHub）"
download_file "lattix-agent ${LATTIX_VERSION}" \
    "${RELEASE_BASE}/lattix-agent-linux-${AGENT_ARCH}.tar.gz" \
    "$TMP_DIR/lattix-agent-linux-${AGENT_ARCH}.tar.gz" \
    || die "agent 包下载失败"
# 校验文件获取不到即中止，不降级跳过。
download_file "agent 校验文件" "${RELEASE_BASE}/checksums.txt" "$TMP_DIR/checksums.txt" \
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
if [[ "$USER_MODE" -eq 0 || "$(id -u)" -eq 0 ]]; then
    mkdir -p "$(dirname "$LATX_AG_LINK")"
    ln -sfn "$LATX_AG_BIN" "$LATX_AG_LINK"
fi

# http→ws / https→wss
PANEL_WS="$(echo "$PANEL_URL" | sed -e 's|^https://|wss://|' -e 's|^http://|ws://|')/api/agent/ws"

echo ">> writing $ENV_FILE"
install -m 0600 /dev/null "$ENV_FILE"
cat > "$ENV_FILE" <<EOF
LATTIX_PANEL_WS=${PANEL_WS}
LATTIX_TOKEN=${BOOTSTRAP_TOKEN}
EOF
# 保留 state/settings；Agent 根据 token 内 panel instance/epoch 选择凭证，并且只在
# 新面板认证成功后清理旧面板的受管配置。

if [[ "$DEV_MODE" -eq 0 && "$USER_MODE" -eq 0 ]]; then
    echo ">> registering systemd services"
    cat > "$SYSTEMD_DIR/xray.service" <<EOF
[Unit]
Description=Xray Service
After=network-online.target
Wants=network-online.target

[Service]
UMask=0077
ExecStart=$XRAY_BIN_DST run -config $XRAY_CONFIG
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
StartLimitIntervalSec=60
StartLimitBurst=10

[Service]
UMask=0077
EnvironmentFile=$ENV_FILE
ExecStart=$AGENT_BIN -panel "\${LATTIX_PANEL_WS}" -token "\${LATTIX_TOKEN}" -state "$STATE_FILE" -settings "$SETTINGS_FILE" -xray-bin "$XRAY_BIN_DST" -xray-config "$XRAY_CONFIG"
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable --now xray.service
    systemctl enable --now lattix-agent.service
    AGENT_ACTIVE="$(systemctl is-active lattix-agent.service 2>/dev/null || true)"
    AGENT_PID="$(systemctl show -p MainPID --value lattix-agent.service 2>/dev/null || true)"
    XRAY_ACTIVE="$(systemctl is-active xray.service 2>/dev/null || true)"
    XRAY_PID="$(systemctl show -p MainPID --value xray.service 2>/dev/null || true)"
    if [[ "$AGENT_ACTIVE" == "active" && "$AGENT_PID" =~ ^[1-9][0-9]*$ ]]; then
        AGENT_STATUS="运行中（systemd active，pid $AGENT_PID）"
    else
        AGENT_STATUS="未运行（systemd ${AGENT_ACTIVE:-unknown}）"
    fi
    if [[ "$XRAY_ACTIVE" == "active" && "$XRAY_PID" =~ ^[1-9][0-9]*$ ]]; then
        XRAY_STATUS="运行中（systemd active，pid $XRAY_PID）"
    else
        XRAY_STATUS="未运行（systemd ${XRAY_ACTIVE:-unknown}）"
    fi
elif [[ "$USER_MODE" -eq 1 ]]; then
    # user 用户态模式：不注册 systemd，生成守护脚本常驻（优先用 flock 防重复，精简系统
    # 无 flock 时退到可回收的 mkdir 锁；agent 退出后自动拉起），并 best-effort 注册
    # crontab @reboot 开机自启。
    echo ">> [user] 生成守护脚本 $RUN_SCRIPT（日志 $AGENT_LOG）"
    mkdir -p "$(dirname "$AGENT_LOG")" "$DATA_DIR"
    cat > "$RUN_SCRIPT" <<EOF
#!/usr/bin/env bash
# Lattix Agent 用户态守护脚本（install.sh 生成）：防重复运行；agent 退出（崩溃/自升级）
# 后 sleep 1 自动拉起，替代 systemd Restart=always；每次重启重新读取 env（token 轮换后即生效）。
set -u
exec >>"$AGENT_LOG" 2>&1

LOCK_KIND=""
LOCK_DIR="$DATA_DIR/lattix-agent.lock.d"

lock_owner_alive() {
    local owner="\$1"
    [[ "\$owner" =~ ^[1-9][0-9]*\$ ]] || return 1
    kill -0 "\$owner" 2>/dev/null || return 1
    [[ ! -r "/proc/\$owner/cmdline" ]] || grep -aqF "$BIN_DIR/lattix-agent-run" "/proc/\$owner/cmdline"
}

cleanup_lock() {
    if [[ "\$LOCK_KIND" == "mkdir" ]]; then
        rm -f "\$LOCK_DIR/pid"
        rmdir "\$LOCK_DIR" 2>/dev/null || true
    fi
}
trap cleanup_lock EXIT
trap 'exit 0' INT TERM

if command -v flock >/dev/null 2>&1; then
    exec 9>"$DATA_DIR/lattix-agent.lock"
    flock -n 9 || exit 0
    LOCK_KIND="flock"
else
    # mkdir 在本地文件系统上原子创建；PID 校验与原子 rename 负责回收崩溃/断电遗留锁。
    while ! mkdir "\$LOCK_DIR" 2>/dev/null; do
        owner="\$(cat "\$LOCK_DIR/pid" 2>/dev/null || true)"
        lock_owner_alive "\$owner" && exit 0
        # 给刚创建目录、尚未来得及写 PID 的并发启动者一次完成初始化的机会。
        sleep 1
        owner="\$(cat "\$LOCK_DIR/pid" 2>/dev/null || true)"
        lock_owner_alive "\$owner" && exit 0
        stale="\${LOCK_DIR}.stale.\$\$"
        if mv "\$LOCK_DIR" "\$stale" 2>/dev/null; then
            rm -f "\$stale/pid"
            rmdir "\$stale" 2>/dev/null || true
        fi
    done
    printf '%s\n' "\$\$" >"\$LOCK_DIR/pid"
    LOCK_KIND="mkdir"
    echo "lattix-agent-run: flock 不可用，已使用 mkdir 兼容锁"
fi
while true; do
    set -a; . "$ENV_FILE"; set +a
    "$AGENT_BIN" -panel "\$LATTIX_PANEL_WS" -token "\$LATTIX_TOKEN" -state "$STATE_FILE" \\
        -settings "$SETTINGS_FILE" -xray-bin "$XRAY_BIN_DST" -xray-config "$XRAY_CONFIG" \\
        -xray-api "$XRAY_API" -xray-runner exec 9>&-
    sleep 1
done
EOF
    chmod 0755 "$RUN_SCRIPT"
    nohup "$RUN_SCRIPT" >/dev/null 2>&1 &
    RUNNER_PID=$!
    AGENT_PID=""
    for ((attempt = 0; attempt < 50; attempt++)); do
        AGENT_PID="$(process_pid "$AGENT_BIN -panel")"
        [[ -n "$AGENT_PID" ]] && break
        if ! kill -0 "$RUNNER_PID" 2>/dev/null; then
            # A concurrent start can make this process exit after another healthy runner won the lock.
            RUNNER_PID="$(process_pid "$RUN_SCRIPT")"
            [[ -n "$RUNNER_PID" ]] || break
        fi
        sleep 0.1
    done
    XRAY_PID="$(process_pid "$XRAY_BIN_DST run -config $XRAY_CONFIG")"
    if [[ -n "$AGENT_PID" ]]; then
        AGENT_STATUS="[user] 进程运行中（pid $AGENT_PID，守护 pid $RUNNER_PID）"
    elif kill -0 "$RUNNER_PID" 2>/dev/null; then
        AGENT_STATUS="[user] 进程未运行（守护脚本 pid $RUNNER_PID 正在重试；日志见 $AGENT_LOG）"
    else
        AGENT_STATUS="[user] 守护脚本未运行（日志见 $AGENT_LOG）"
        echo ">> [user] 守护脚本启动失败，最近日志：" >&2
        tail -n 20 "$AGENT_LOG" >&2 || true
    fi
    if [[ -n "$XRAY_PID" ]]; then
        XRAY_STATUS="[user] 进程运行中（pid $XRAY_PID，由 Agent 托管）"
    else
        XRAY_STATUS="[user] 进程未运行（由 Agent 托管，等待可运行配置）"
    fi
    # best-effort 开机自启：crontab @reboot（先 grep 去重）；无 crontab 时提示手动启动。
    if command -v crontab >/dev/null; then
        if crontab -l 2>/dev/null | grep -qF "$RUN_SCRIPT"; then
            echo ">> [user] crontab @reboot 自启已存在，跳过"
        else
            (crontab -l 2>/dev/null || true; echo "@reboot $RUN_SCRIPT >>$AGENT_LOG 2>&1 &") | crontab -
            echo ">> [user] 已注册 crontab @reboot 开机自启（best-effort）"
        fi
    else
        echo ">> [user] 未检测到 crontab，无法注册开机自启；重启后需手动运行 latx-ag start"
    fi
else
    echo ">> [DEV] nohup 启动 agent（日志 $AGENT_LOG）"
    mkdir -p "$(dirname "$AGENT_LOG")"
    nohup "$AGENT_BIN" -panel "$PANEL_WS" -token "$BOOTSTRAP_TOKEN" -state "$STATE_FILE" \
        -settings "$SETTINGS_FILE" -xray-bin "$XRAY_BIN_DST" -xray-config "$XRAY_CONFIG" \
        -xray-api "$XRAY_API" -xray-runner exec \
        >"$AGENT_LOG" 2>&1 &
    AGENT_PID=$!
    sleep 1
    XRAY_PID="$(process_pid "$XRAY_BIN_DST run -config $XRAY_CONFIG")"
    if kill -0 "$AGENT_PID" 2>/dev/null; then
        AGENT_STATUS="[DEV] 进程运行中（pid $AGENT_PID）"
    else
        AGENT_STATUS="[DEV] 进程未运行（日志见 $AGENT_LOG）"
    fi
    if [[ -n "$XRAY_PID" ]]; then
        XRAY_STATUS="[DEV] 进程运行中（pid $XRAY_PID，由 Agent 托管）"
    else
        XRAY_STATUS="[DEV] 进程未运行（由 Agent 托管，等待可运行配置）"
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
AGENT_VERSION_DISPLAY="$("$AGENT_BIN" -version 2>/dev/null || true)"
AGENT_VERSION_DISPLAY="${AGENT_VERSION_DISPLAY%%$'\n'*}"
[[ -n "$AGENT_VERSION_DISPLAY" ]] || AGENT_VERSION_DISPLAY="unknown"
XRAY_VERSION_DISPLAY="$("$XRAY_BIN_DST" version 2>/dev/null || true)"
XRAY_VERSION_DISPLAY="${XRAY_VERSION_DISPLAY%%$'\n'*}"
[[ -n "$XRAY_VERSION_DISPLAY" ]] || XRAY_VERSION_DISPLAY="unknown"
cat <<EOF

============================================================
  Lattix Agent 安装完成

  面板地址:  $PANEL_URL
  运行模式:  $MODE_LABEL
  Agent 状态: $AGENT_STATUS
  Agent 版本: $AGENT_VERSION_DISPLAY
  xray 状态:  $XRAY_STATUS
  xray 版本:  $XRAY_VERSION_DISPLAY$USER_HINT

  使用 latx-ag 命令运维本节点：
    latx-ag status / log / update / xray-update / uninstall
    latx-ag -h 查看帮助
============================================================
EOF
echo ">> done. Agent 将以 bootstrap token 首连面板并换发长期凭证。"
print_bbr_warning
