#!/usr/bin/env bash
# Lattix 节点侧管理程序（latx-ag，设计文档 §20）。
#
# 由 agent install.sh 安装为 /usr/local/bin/latx-ag（CI 发版时烧入版本与仓库占位符，
# 见 .github/workflows/release.yml）。全部功能函数化，子命令：
#
#   latx-ag status                  节点状态：agent/xray 服务、版本、面板地址、配置指纹
#   latx-ag start|stop|restart      systemctl 包装（systemd 模式需 root）；用户态模式管理守护脚本与进程
#   latx-ag enable|disable          systemctl 包装（systemd 模式需 root）；用户态模式增删 crontab @reboot
#   latx-ag log [-n N]              journalctl -u lattix-agent -f（-n N 时不跟随）；用户态读日志文件
#   latx-ag log-xray [-n N]         journalctl -u xray -f（同上）
#   latx-ag update [version]        从 GitHub release 更新 agent（默认 latest；预检 -version）
#   latx-ag xray-update [version]   更新 xray（官方 .dgst 校验 SHA2-256，失败回滚 .bak）
#   latx-ag uninstall [--purge-xray] [--yes]   卸载 agent（默认保留 xray 与节点运行）
#   latx-ag version                 latx-ag 自身、agent 与 xray 版本
#   latx-ag help                    本帮助
#
# 运行模式自动判断（无需 LATX_USER_MODE，该变量仅 install.sh 使用）：unit 文件
# $LATX_PREFIX/etc/systemd/system/lattix-agent.service 存在且有 systemctl 时走 systemd；
# 否则按用户态模式（install.sh user 模式安装：无 unit 文件，守护脚本 lattix-agent-run
# 常驻；start/stop 直管进程，enable/disable 操作 crontab @reboot）。
#
# 环境变量覆盖（e2e/运维用）：
#   LATX_PREFIX         路径前缀（默认空；设置后 /usr/local/bin、/etc 等全部加前缀）
#   LATX_AG_UNIT        agent systemd unit 名（默认 lattix-agent）
#   LATX_AG_XRAY_UNIT   xray systemd unit 名（默认 xray）
#   LATX_AG_BIN         latx-ag 安装路径（默认 $LATX_PREFIX/usr/local/bin/latx-ag，uninstall 时删除）
#   LATX_AG_AGENT_BIN   agent 二进制路径（默认 $LATX_PREFIX/usr/local/bin/lattix-agent）
#   LATX_AG_XRAY_BIN    xray 二进制路径（默认 $LATX_PREFIX/usr/local/bin/xray）
#   LATX_AG_STATE       agent state 文件（默认 $LATX_PREFIX/etc/lattix-agent.state.json）
#   LATX_AG_ENV         agent env 文件（默认 $LATX_PREFIX/etc/lattix-agent.env，读面板地址）
#   LATX_AG_LOG         用户态/DEV 模式日志文件（默认 $LATX_PREFIX/var/log/lattix-agent.log）
#   XRAY_RELEASE_BASE   xray release 下载基址（默认官方 GitHub，对齐 agent -xray-release-base）
#   LATX_DEV=1          无 systemd 开发模式：status 降级为进程检查，log 读 LATX_AG_LOG
set -euo pipefail

# ===== CI 发版烧入区（release.yml 在发版时 sed 替换以下两个占位符）=====
LATX_AG_VERSION="{{LATTIX_VERSION}}"
GITHUB_REPO="{{GITHUB_REPO}}"

PREFIX="${LATX_PREFIX:-}"
UNIT="${LATX_AG_UNIT:-lattix-agent}"
XRAY_UNIT="${LATX_AG_XRAY_UNIT:-xray}"
UNIT_FILE="$PREFIX/etc/systemd/system/${UNIT}.service"
XRAY_UNIT_FILE="$PREFIX/etc/systemd/system/${XRAY_UNIT}.service"
LATX_AG_BIN="${LATX_AG_BIN:-$PREFIX/usr/local/bin/latx-ag}"
AGENT_BIN="${LATX_AG_AGENT_BIN:-$PREFIX/usr/local/bin/lattix-agent}"
# 用户态守护脚本路径（install.sh user 模式生成），由 agent 安装目录推导。
RUN_SCRIPT="$(dirname "$AGENT_BIN")/lattix-agent-run"
XRAY_BIN="${LATX_AG_XRAY_BIN:-$PREFIX/usr/local/bin/xray}"
XRAY_CONFIG_DIR="$PREFIX/usr/local/etc/xray"
STATE_FILE="${LATX_AG_STATE:-$PREFIX/etc/lattix-agent.state.json}"
ENV_FILE="${LATX_AG_ENV:-$PREFIX/etc/lattix-agent.env}"
LOG_FILE="${LATX_AG_LOG:-$PREFIX/var/log/lattix-agent.log}"
XRAY_RELEASE_BASE="${XRAY_RELEASE_BASE:-https://github.com/XTLS/Xray-core/releases/download}"

die() { echo "latx-ag: $*" >&2; exit 1; }

# use_systemd：非 DEV 模式、unit 文件存在且有 systemctl 时走 systemd 管理；
# 否则按用户态模式（无 unit 文件，install.sh user 模式安装）。
use_systemd() { [[ "${LATX_DEV:-0}" != "1" && -f "$UNIT_FILE" ]] && command -v systemctl >/dev/null; }

need_root() {
    [[ "$(id -u)" -eq 0 ]] || die "该操作需要 root 权限（systemctl 类命令），请用 sudo 执行"
}

agent_version() {
    if [[ -x "$AGENT_BIN" ]]; then
        "$AGENT_BIN" -version 2>/dev/null || echo "unknown"
    else
        echo "not installed"
    fi
}

xray_version() {
    if [[ -x "$XRAY_BIN" ]]; then
        "$XRAY_BIN" version 2>/dev/null | head -1 || echo "unknown"
    else
        echo "not installed"
    fi
}

# 仅匹配 agent 进程（带 -panel 参数），避免误中守护脚本 lattix-agent-run 的命令行。
agent_pid() { pgrep -f "$AGENT_BIN -panel" 2>/dev/null | head -1 || true; }

# 当前面板地址：从 install.sh 写入的 env 文件读 LATTIX_PANEL_WS。
panel_addr() {
    if [[ -r "$ENV_FILE" ]]; then
        grep -o '^LATTIX_PANEL_WS=.*' "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2- || true
    fi
}

# state 文件中的服务器 id（JSON 单行字段提取，避免依赖 jq）。
server_id() {
    if [[ -r "$STATE_FILE" ]]; then
        grep -o '"server_id": *[0-9]*' "$STATE_FILE" 2>/dev/null | head -1 | grep -o '[0-9]*' || true
    fi
}

cmd_status() {
    if use_systemd; then
        local active enabled xactive xenabled
        active="$(systemctl is-active "$UNIT" 2>/dev/null || true)"
        enabled="$(systemctl is-enabled "$UNIT" 2>/dev/null || true)"
        xactive="$(systemctl is-active "$XRAY_UNIT" 2>/dev/null || true)"
        xenabled="$(systemctl is-enabled "$XRAY_UNIT" 2>/dev/null || true)"
        echo "Agent 服务: ${active:-unknown}（${enabled:-unknown}）"
        echo "xray 服务:  ${xactive:-unknown}（${xenabled:-unknown}）"
    else
        local pid; pid="$(agent_pid)"
        if [[ -n "$pid" ]]; then
            echo "Agent 服务: [用户态] 进程运行中（pid $pid，无 systemd）"
        else
            echo "Agent 服务: [用户态] 进程未运行（无 systemd）"
        fi
        echo "xray 服务:  [用户态] 由 agent 托管（-xray-runner exec）"
    fi

    echo "Agent 版本: $(agent_version)"
    echo "xray 版本:  $(xray_version)"

    local panel sid
    panel="$(panel_addr)"
    [[ -n "$panel" ]] && echo "面板地址: $panel" || echo "面板地址: unknown（$ENV_FILE 不存在/不可读或无 LATTIX_PANEL_WS）"
    sid="$(server_id)"
    [[ -n "$sid" ]] && echo "服务器 ID: $sid"

    # §17 漂移基线由运行中的 agent 内存维护（不落盘），此处仅给出当前配置指纹。
    if [[ -r "$XRAY_CONFIG_DIR/config.json" ]] && command -v sha256sum >/dev/null; then
        local fp
        fp="$(sha256sum "$XRAY_CONFIG_DIR/config.json" 2>/dev/null | cut -c1-12 || true)"
        [[ -n "$fp" ]] && echo "配置指纹: sha256:$fp（漂移基线在 agent 内存中，§17）"
    fi
}

user_start() {
    [[ -x "$RUN_SCRIPT" ]] || die "守护脚本不存在: $RUN_SCRIPT（请先运行 install.sh）"
    nohup "$RUN_SCRIPT" >/dev/null 2>&1 &
    echo ">> [用户态] 已启动守护脚本 $RUN_SCRIPT（脚本内 flock 防重复）"
}

user_stop() {
    # 先停守护脚本（防其循环拉起），再停 agent。
    pkill -f "$RUN_SCRIPT" 2>/dev/null || true
    pkill -f "$AGENT_BIN" 2>/dev/null || true
    echo ">> [用户态] 已停止守护脚本与 agent 进程"
}

cmd_svc() {
    if use_systemd; then
        need_root
        systemctl "$1" "$UNIT"
        echo ">> systemctl $1 $UNIT 完成"
        return
    fi
    # 用户态模式（无 unit 文件）：直管守护脚本/进程与 crontab @reboot。
    case "$1" in
        start)   user_start ;;
        stop)    user_stop ;;
        restart) user_stop; sleep 1; user_start ;;
        enable)
            command -v crontab >/dev/null || die "未检测到 crontab，用户态模式不支持 enable"
            if ! crontab -l 2>/dev/null | grep -qF "$RUN_SCRIPT"; then
                (crontab -l 2>/dev/null || true; echo "@reboot $RUN_SCRIPT >>$LOG_FILE 2>&1 &") | crontab -
            fi
            echo ">> [用户态] 已注册 crontab @reboot 自启（$RUN_SCRIPT）"
            ;;
        disable)
            command -v crontab >/dev/null || die "未检测到 crontab，用户态模式不支持 disable"
            if crontab -l 2>/dev/null | grep -qF "$RUN_SCRIPT"; then
                crontab -l 2>/dev/null | { grep -vF "$RUN_SCRIPT" || true; } | crontab -
            fi
            echo ">> [用户态] 已移除 crontab @reboot 自启"
            ;;
    esac
}

cmd_log() {
    local unit="$1"; shift
    local lines="" follow=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -n) lines="${2:?-n requires a value}"; shift 2 ;;
            -f) follow=1; shift ;;
            *)  die "未知参数: $1（用法: latx-ag log [-n N]）" ;;
        esac
    done
    # 默认无 -n 时跟随；传了 -n N 即不跟随（除非显式再传 -f）。
    if [[ -z "$follow" ]]; then
        [[ -n "$lines" ]] && follow=0 || follow=1
    fi
    if use_systemd; then
        if [[ "$follow" -eq 1 ]]; then
            journalctl -u "$unit" -f ${lines:+-n "$lines"}
        else
            journalctl -u "$unit" -n "${lines:-50}" --no-pager
        fi
    else
        [[ -f "$LOG_FILE" ]] || die "[用户态] 日志文件不存在: $LOG_FILE"
        if [[ "$follow" -eq 1 ]]; then
            tail -f ${lines:+-n "$lines"} "$LOG_FILE"
        else
            tail -n "$lines" "$LOG_FILE"
        fi
    fi
}

cmd_update() {
    # root 仅 systemd 模式需要；user 模式由守护循环拉起新版，DEV 模式维持手动替换。
    if use_systemd; then need_root; fi
    [[ "${LATX_DEV:-0}" != "1" ]] || die "未检测到 systemd，无法自动更新（LATX_DEV=1 开发模式请手动替换二进制）"
    [[ "$GITHUB_REPO" != *"{{"* ]] || die "脚本未经 CI stamp（{{GITHUB_REPO}} 占位符未替换），无法定位 release"
    command -v curl >/dev/null      || die "curl is required"
    command -v sha256sum >/dev/null || die "sha256sum is required"
    command -v tar >/dev/null       || die "tar is required"
    [[ -x "$AGENT_BIN" ]] || die "agent 未安装（$AGENT_BIN 不存在），请先运行 install.sh"

    local arch
    case "$(uname -m)" in
        x86_64)  arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *)       die "unsupported arch: $(uname -m)" ;;
    esac

    local version="${1:-latest}"
    if [[ "$version" == "latest" ]]; then
        echo ">> resolving latest lattix version"
        version="$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
            | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4)" || true
        [[ -n "$version" ]] || die "latest 解析失败（GitHub API 不可达？），请显式指定版本：latx-ag update <version>"
    fi

    local current; current="$(agent_version)"
    echo ">> updating agent ${current} -> ${version}"
    TMP_DIR="$(mktemp -d)"
    trap 'rm -rf "$TMP_DIR"' EXIT
    local tmp="$TMP_DIR"

    local base="https://github.com/${GITHUB_REPO}/releases/download/${version}"
    curl -fsSL -o "$tmp/lattix-agent-linux-${arch}.tar.gz" "$base/lattix-agent-linux-${arch}.tar.gz" \
        || die "下载失败：release ${version} 无 lattix-agent-linux-${arch}.tar.gz"
    curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" \
        || die "未获取到 release 校验文件 checksums.txt，中止更新"
    (cd "$tmp" && grep "lattix-agent-linux-${arch}\.tar\.gz\$" checksums.txt | sha256sum -c - >/dev/null) \
        || die "agent 包 SHA256 校验失败（release ${version} checksums.txt）"
    echo ">> agent 包 SHA256 校验通过"
    tar -C "$tmp" -xzf "$tmp/lattix-agent-linux-${arch}.tar.gz"
    [[ -f "$tmp/lattix-agent/lattix-agent" ]] || die "agent 包内容异常（缺 lattix-agent）"

    # 预检：新二进制可执行且 -version 正常输出，失败即放弃（不触碰在运安装）。
    chmod 0755 "$tmp/lattix-agent/lattix-agent"
    local new_version
    new_version="$("$tmp/lattix-agent/lattix-agent" -version 2>/dev/null)" \
        || die "预检失败：新二进制 -version 执行异常，放弃更新"
    [[ "$new_version" == "$version" ]] \
        || die "预检失败：新二进制版本不符（期望 ${version}，实际 ${new_version}），放弃更新"

    if use_systemd; then
        systemctl stop "$UNIT"
        install -m 0755 "$tmp/lattix-agent/lattix-agent" "$AGENT_BIN"
        systemctl start "$UNIT"
    else
        # 用户态：先停 agent 释放二进制占用（守护循环 5s 内拉起新版），再替换。
        pkill -f "$AGENT_BIN -panel" 2>/dev/null || true
        sleep 1
        install -m 0755 "$tmp/lattix-agent/lattix-agent" "$AGENT_BIN"
    fi

    local running_version; running_version="$(agent_version)"
    [[ "$running_version" == "$version" ]] \
        || die "更新后版本校验失败（期望 ${version}，实际 ${running_version}）"
    echo ">> done. agent 已更新至 ${running_version}（latx-ag status 查看状态）"
}

cmd_xray_update() {
    # root 仅 systemd 模式需要；user 模式停 agent（守护循环连带重启 xray），DEV 模式维持手动替换。
    if use_systemd; then need_root; fi
    [[ "${LATX_DEV:-0}" != "1" ]] || die "未检测到 systemd，无法自动更新（LATX_DEV=1 开发模式请手动替换二进制）"
    command -v curl >/dev/null      || die "curl is required"
    command -v unzip >/dev/null     || die "unzip is required"
    command -v sha256sum >/dev/null || die "sha256sum is required"
    [[ -x "$XRAY_BIN" ]] || die "xray 未安装（$XRAY_BIN 不存在）"

    local asset
    case "$(uname -m)" in
        x86_64)  asset="Xray-linux-64.zip" ;;
        aarch64) asset="Xray-linux-arm64-v8a.zip" ;;
        *)       die "unsupported arch: $(uname -m)" ;;
    esac

    # 与 agent upgrade.go 同语义（§18）：显式版本走 {base}/{version}；
    # latest 在自定义基址（镜像）下跳过 GitHub API 走 {base}/latest/download 约定，
    # 官方基址下经 GitHub API 解析实际版本号。
    local version="${1:-latest}" dl_ref
    if [[ "$version" == "latest" && "$XRAY_RELEASE_BASE" != *"github.com/XTLS/Xray-core"* ]]; then
        dl_ref="latest/download"
        echo ">> latest 走镜像基址 $XRAY_RELEASE_BASE（跳过 GitHub API）"
    else
        if [[ "$version" == "latest" ]]; then
            echo ">> resolving latest xray version"
            version="$(curl -fsSL https://api.github.com/repos/XTLS/Xray-core/releases/latest \
                | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4)" || true
            [[ -n "$version" ]] || die "latest 解析失败（GitHub API 不可达？），请显式指定版本：latx-ag xray-update <version>"
        fi
        [[ "$version" == v* ]] || die "版本号须形如 vX.Y.Z 或 latest: $version"
        dl_ref="$version"
    fi

    echo ">> updating xray -> ${version}"
    TMP_DIR="$(mktemp -d)"
    trap 'rm -rf "$TMP_DIR"' EXIT
    local tmp="$TMP_DIR"

    curl -fsSL -o "$tmp/xray.zip" "$XRAY_RELEASE_BASE/${dl_ref}/${asset}" \
        || die "下载失败：$XRAY_RELEASE_BASE/${dl_ref}/${asset}"
    # 校验官方 .dgst 中的 SHA2-256（同 agent upgrade.go）；获取不到校验文件即失败，不降级跳过。
    curl -fsSL -o "$tmp/xray.dgst" "$XRAY_RELEASE_BASE/${dl_ref}/${asset}.dgst" \
        || die "未获取到 xray 官方 .dgst 校验文件，中止更新"
    local expected actual
    expected="$(grep 'SHA2-256=' "$tmp/xray.dgst" | head -1 | cut -d' ' -f2)"
    actual="$(sha256sum "$tmp/xray.zip" | cut -d' ' -f1)"
    [[ -n "$expected" && "$expected" == "$actual" ]] \
        || die "xray 包校验和不匹配（期望 ${expected:-<空>}，实际 $actual）"
    echo ">> xray 包 SHA2-256 校验通过"
    unzip -o -q "$tmp/xray.zip" -d "$tmp/xray"
    [[ -f "$tmp/xray/xray" ]] || die "xray 包内容异常（缺 xray 二进制）"

    # 备份 → 替换 → 重启 → 版本校验；失败回滚 .bak 并重启。
    cp -a "$XRAY_BIN" "$XRAY_BIN.bak"
    if use_systemd; then
        install -m 0755 "$tmp/xray/xray" "$XRAY_BIN"
        if ! systemctl restart "$XRAY_UNIT"; then
            mv "$XRAY_BIN.bak" "$XRAY_BIN"
            systemctl restart "$XRAY_UNIT" || true
            die "重启 xray 失败，已回滚 .bak"
        fi
    else
        # 用户态：xray 由 agent 以 exec runner 托管；先停 agent（连带停 xray）释放二进制
        # 占用，替换后守护循环拉起 agent 并连带重启 xray（替代 systemctl restart）。
        pkill -f "$AGENT_BIN -panel" 2>/dev/null || true
        pkill -f "$XRAY_BIN run" 2>/dev/null || true
        sleep 1
        install -m 0755 "$tmp/xray/xray" "$XRAY_BIN"
    fi
    local ver
    ver="$("$XRAY_BIN" version 2>/dev/null | head -1 || true)"
    if [[ -z "$ver" ]] || { [[ "$version" != "latest" ]] && [[ "$ver" != *"${version#v}"* ]]; }; then
        mv "$XRAY_BIN.bak" "$XRAY_BIN"
        if use_systemd; then systemctl restart "$XRAY_UNIT" || true; fi
        die "升级后版本校验失败（期望 ${version}，实际 ${ver:-<无输出>}），已回滚 .bak"
    fi
    rm -f "$XRAY_BIN.bak"
    echo ">> done. xray 已更新：$ver"
}

cmd_uninstall() {
    local purge_xray=0 yes=0
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --purge-xray) purge_xray=1; shift ;;
            --yes|-y)     yes=1; shift ;;
            *)            die "未知参数: $1（用法: latx-ag uninstall [--purge-xray] [--yes]）" ;;
        esac
    done
    local target="agent（xray 与节点继续运行）"
    [[ "$purge_xray" -eq 1 ]] && target="agent + xray（节点流量将中断）"
    if [[ "$yes" -ne 1 ]]; then
        local ans
        read -r -p "确认卸载 Lattix Agent？（范围：$target） [y/N] " ans
        [[ "$ans" == "y" || "$ans" == "Y" ]] || { echo "已取消"; exit 0; }
    fi

    # 清理清单与 src/agent/cmd/agent/uninstall.go 对齐（含 .bak 文件）。
    if use_systemd; then
        need_root
        systemctl stop "$UNIT" 2>/dev/null || true
        systemctl disable "$UNIT" 2>/dev/null || true
        rm -f "$UNIT_FILE"
        if [[ "$purge_xray" -eq 1 ]]; then
            systemctl stop "$XRAY_UNIT" 2>/dev/null || true
            systemctl disable "$XRAY_UNIT" 2>/dev/null || true
            rm -f "$XRAY_UNIT_FILE"
        fi
        systemctl daemon-reload
        echo ">> 已停止并移除 systemd 服务 $UNIT$([[ "$purge_xray" -eq 1 ]] && echo " 与 $XRAY_UNIT")"
    else
        # 用户态：停守护脚本（防循环拉起）与 agent，移除 crontab @reboot 行。
        pkill -f "$RUN_SCRIPT" 2>/dev/null || true
        pkill -f "$AGENT_BIN" 2>/dev/null || true
        echo ">> [用户态] 已停止守护脚本与 agent 进程"
        if command -v crontab >/dev/null && crontab -l 2>/dev/null | grep -qF "$RUN_SCRIPT"; then
            crontab -l 2>/dev/null | { grep -vF "$RUN_SCRIPT" || true; } | crontab -
            echo ">> [用户态] 已移除 crontab @reboot 自启"
        fi
        if [[ "$purge_xray" -eq 1 ]]; then
            pkill -f "$XRAY_BIN run" 2>/dev/null || true
            echo ">> [用户态] 已停止 xray 进程"
        fi
    fi
    rm -f "$AGENT_BIN" "$AGENT_BIN.bak" "$RUN_SCRIPT" "$ENV_FILE" "$STATE_FILE"
    echo ">> 已删除 agent 二进制/env/state"
    if [[ "$purge_xray" -eq 1 ]]; then
        rm -f "$XRAY_BIN" "$XRAY_BIN.bak"
        rm -rf "$XRAY_CONFIG_DIR"
        echo ">> 已删除 xray 二进制与配置 $XRAY_CONFIG_DIR"
    else
        echo ">> 保留 xray（$XRAY_BIN）与其配置，节点继续运行"
    fi
    rm -f "$LATX_AG_BIN"
    echo ">> done. Lattix Agent 已卸载。"
}

cmd_version() {
    echo "latx-ag 版本: $LATX_AG_VERSION"
    echo "Agent 版本:   $(agent_version)"
    echo "xray 版本:    $(xray_version)"
}

cmd_help() { awk '/^set -euo pipefail/{exit} NR>1' "$0" | sed 's/^# \{0,1\}//'; }

main() {
    local cmd="${1:-help}"; shift || true
    case "$cmd" in
        status)               cmd_status "$@" ;;
        start|stop|restart|enable|disable) cmd_svc "$cmd" ;;
        log)                  cmd_log "$UNIT" "$@" ;;
        log-xray)             cmd_log "$XRAY_UNIT" "$@" ;;
        update)               cmd_update "$@" ;;
        xray-update)          cmd_xray_update "$@" ;;
        uninstall)            cmd_uninstall "$@" ;;
        version)              cmd_version ;;
        help|-h|--help)       cmd_help ;;
        *)                    die "未知子命令: $cmd（latx-ag help 查看用法）" ;;
    esac
}

main "$@"
