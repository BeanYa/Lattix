#!/usr/bin/env bash
# Lattix 面板管理程序（latx，设计文档 §20）。
#
# 由 install-panel.sh 安装为 /usr/local/bin/latx（CI 发版时烧入版本与仓库占位符，
# 见 .github/workflows/release.yml）。全部功能函数化，子命令：
#
#   latx status                  面板状态：服务/进程、监听端口、版本、面板地址
#   latx start|stop|restart      systemctl 包装（需 root）
#   latx enable|disable          systemctl 包装（需 root）
#   latx log [-n N]              journalctl -u lattix-panel -f（-n N 时不跟随）
#   latx update [version]        从 GitHub release 更新面板（默认 latest；仅 amd64）
#   latx acme <domain>           引导式申请 ACME 证书并切 HTTPS（设置页 API，重启生效）
#   latx reset-admin <newpass>   重置管理员密码（改密即全部会话失效）
#   latx uninstall [--purge-db] [--yes]   卸载面板（默认保留 DB 并提示路径）
#   latx version                 latx 自身版本与面板版本
#   latx help                    本帮助
#
# 环境变量覆盖（e2e/运维用）：
#   LATX_ROOT       安装根目录（默认 /usr/local/lattix-panel）
#   LATX_UNIT       systemd unit 名（默认 lattix-panel）
#   LATX_BIN        latx 安装路径（默认 /usr/local/bin/latx，uninstall 时删除）
#   LATX_PANEL_URL  面板本机地址（默认 http://127.0.0.1:8080，acme 用）
#   LATX_ADMIN_USER / LATX_ADMIN_PASS   acme 登录凭据（缺省 read -s 提示）
#   LATX_DEV=1      无 systemd 开发模式：status 降级为进程检查，log 读 panel.log
set -euo pipefail

# ===== CI 发版烧入区（release.yml 在发版时 sed 替换以下两个占位符）=====
LATX_VERSION="{{LATTIX_VERSION}}"
GITHUB_REPO="{{GITHUB_REPO}}"

INSTALL_ROOT="${LATX_ROOT:-/usr/local/lattix-panel}"
UNIT="${LATX_UNIT:-lattix-panel}"
UNIT_FILE="/etc/systemd/system/${UNIT}.service"
LATX_BIN="${LATX_BIN:-/usr/local/bin/latx}"
BACKEND="$INSTALL_ROOT/lattix-backend"
DB_PATH="$INSTALL_ROOT/lattix.db"
PANEL_URL="${LATX_PANEL_URL:-http://127.0.0.1:8080}"

die() { echo "latx: $*" >&2; exit 1; }

# use_systemd：非 DEV 模式且存在 systemctl 时走 systemd 管理。
use_systemd() { [[ "${LATX_DEV:-0}" != "1" ]] && command -v systemctl >/dev/null; }

need_root() {
    [[ "$(id -u)" -eq 0 ]] || die "该操作需要 root 权限（systemctl 类命令），请用 sudo 执行"
}

panel_version() {
    if [[ -x "$BACKEND" ]]; then
        "$BACKEND" -version 2>/dev/null || echo "unknown"
    else
        echo "not installed"
    fi
}

panel_pid() { pgrep -f "$BACKEND" 2>/dev/null | head -1 || true; }

# 从 systemd unit ExecStart 或进程 cmdline 解析 -addr（默认 :8080）。
panel_addr() {
    local addr=""
    if use_systemd && [[ -f "$UNIT_FILE" ]]; then
        addr="$(grep -o -- '-addr [^ ]*' "$UNIT_FILE" | head -1 | awk '{print $2}' || true)"
    fi
    if [[ -z "$addr" ]]; then
        local pid; pid="$(panel_pid)"
        if [[ -n "$pid" && -r "/proc/$pid/cmdline" ]]; then
            addr="$(tr '\0' ' ' <"/proc/$pid/cmdline" | grep -o -- '-addr [^ ]*' | head -1 | awk '{print $2}' || true)"
        fi
    fi
    echo "${addr:-:8080}"
}

# 主机对外 IP（hostname -I 第一个，失败回退 127.0.0.1）。
host_ip() { hostname -I 2>/dev/null | awk '{print $1}' | grep . || echo "127.0.0.1"; }

cmd_status() {
    local pid="" active="" enabled=""
    if use_systemd; then
        active="$(systemctl is-active "$UNIT" 2>/dev/null || true)"
        enabled="$(systemctl is-enabled "$UNIT" 2>/dev/null || true)"
        pid="$(systemctl show -p MainPID --value "$UNIT" 2>/dev/null || true)"
        [[ "$pid" == "0" ]] && pid=""
        echo "服务状态: ${active:-unknown}（${enabled:-unknown}）"
    else
        pid="$(panel_pid)"
        if [[ -n "$pid" ]]; then
            echo "服务状态: [DEV] 进程运行中（pid $pid，无 systemd）"
        else
            echo "服务状态: [DEV] 进程未运行（无 systemd）"
        fi
    fi

    local addr port
    addr="$(panel_addr)"
    port="${addr##*:}"
    echo "监听端口: ${port}（-addr $addr）"
    if [[ -n "$pid" ]] && command -v ss >/dev/null; then
        ss -tlnp 2>/dev/null | grep "pid=$pid," | awk '{print "  " $4}' || true
    fi

    echo "面板版本: $(panel_version)"
    echo "面板地址: http://$(host_ip):${port}"
}

cmd_svc() {
    need_root
    use_systemd || die "未检测到 systemd（LATX_DEV=1 开发模式下请直接管理进程）"
    systemctl "$1" "$UNIT"
    echo ">> systemctl $1 $UNIT 完成"
}

cmd_log() {
    local lines="" follow=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -n) lines="${2:?-n requires a value}"; shift 2 ;;
            -f) follow=1; shift ;;
            *)  die "未知参数: $1（用法: latx log [-n N]）" ;;
        esac
    done
    # 默认无 -n 时跟随；传了 -n N 即不跟随（除非显式再传 -f）。
    if [[ -z "$follow" ]]; then
        [[ -n "$lines" ]] && follow=0 || follow=1
    fi
    if use_systemd; then
        if [[ "$follow" -eq 1 ]]; then
            journalctl -u "$UNIT" -f ${lines:+-n "$lines"}
        else
            journalctl -u "$UNIT" -n "${lines:-50}" --no-pager
        fi
    else
        local log="$INSTALL_ROOT/panel.log"
        [[ -f "$log" ]] || die "[DEV] 日志文件不存在: $log"
        if [[ "$follow" -eq 1 ]]; then
            tail -f ${lines:+-n "$lines"} "$log"
        else
            tail -n "$lines" "$log"
        fi
    fi
}

cmd_update() {
    [[ "$(uname -m)" == "x86_64" ]] || die "面板仅有 linux/amd64 构建，当前架构 $(uname -m) 不支持"
    need_root
    use_systemd || die "未检测到 systemd，无法自动更新（LATX_DEV=1 开发模式请手动替换二进制）"
    [[ "$GITHUB_REPO" != *"{{"* ]] || die "脚本未经 CI stamp（{{GITHUB_REPO}} 占位符未替换），无法定位 release"
    command -v curl >/dev/null      || die "curl is required"
    command -v sha256sum >/dev/null || die "sha256sum is required"
    [[ -x "$BACKEND" ]] || die "面板未安装（$BACKEND 不存在），请先运行 install-panel.sh"

    local version="${1:-latest}"
    if [[ "$version" == "latest" ]]; then
        echo ">> resolving latest lattix version"
        version="$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
            | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4)" || true
        [[ -n "$version" ]] || die "latest 解析失败（GitHub API 不可达？），请显式指定版本：latx update <version>"
    fi

    local current; current="$(panel_version)"
    echo ">> updating panel ${current} -> ${version}"
    TMP_DIR="$(mktemp -d)"
    trap 'rm -rf "$TMP_DIR"' EXIT
    local tmp="$TMP_DIR"

    local base="https://github.com/${GITHUB_REPO}/releases/download/${version}"
    curl -fsSL -o "$tmp/lattix-panel-linux-amd64.tar.gz" "$base/lattix-panel-linux-amd64.tar.gz" \
        || die "下载失败：release ${version} 无 lattix-panel-linux-amd64.tar.gz"
    curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" \
        || die "未获取到 release 校验文件 checksums.txt，中止更新"
    (cd "$tmp" && grep 'lattix-panel-linux-amd64.tar.gz$' checksums.txt | sha256sum -c - >/dev/null) \
        || die "面板包 SHA256 校验失败（release ${version} checksums.txt）"
    echo ">> 面板包 SHA256 校验通过"
    tar -C "$tmp" -xzf "$tmp/lattix-panel-linux-amd64.tar.gz"
    [[ -f "$tmp/lattix-panel/lattix-backend" ]] || die "面板包内容异常（缺 lattix-backend）"

    systemctl stop "$UNIT"
    install -m 0755 "$tmp/lattix-panel/lattix-backend" "$BACKEND"
    rm -rf "$INSTALL_ROOT/frontend-dist"
    cp -r "$tmp/lattix-panel/frontend-dist" "$INSTALL_ROOT/frontend-dist"
    systemctl start "$UNIT"

    local new_version; new_version="$(panel_version)"
    [[ "$new_version" == "$version" ]] \
        || die "更新后版本校验失败（期望 ${version}，实际 ${new_version}）"
    echo ">> done. 面板已更新至 ${new_version}（latx status 查看状态）"
}

cmd_acme() {
    local domain="${1:?用法: latx acme <domain>}"
    command -v curl >/dev/null    || die "curl is required"
    command -v python3 >/dev/null || die "python3 is required"

    local user="${LATX_ADMIN_USER:-admin}"
    local pass="${LATX_ADMIN_PASS:-}"
    if [[ -z "$pass" ]]; then
        read -r -s -p "面板管理员密码（$user）: " pass || true; echo
        [[ -n "$pass" ]] || die "密码不能为空（或用 LATX_ADMIN_PASS 环境变量传入）"
    fi

    JAR_FILE="$(mktemp)"
    trap 'rm -f "$JAR_FILE"' EXIT
    local jar="$JAR_FILE"

    echo ">> 登录面板 $PANEL_URL"
    local body code
    body="$(python3 -c 'import json,sys; print(json.dumps({"username":sys.argv[1],"password":sys.argv[2]}))' "$user" "$pass")"
    code="$(curl -s -o /dev/null -w '%{http_code}' -c "$jar" -H 'Content-Type: application/json' \
        -d "$body" "$PANEL_URL/api/login")"
    [[ "$code" == "200" ]] || die "登录失败（HTTP $code），请检查面板地址与管理员凭据（LATX_PANEL_URL/LATX_ADMIN_USER/LATX_ADMIN_PASS）"

    echo ">> 保存 ACME 设置（tls_mode=acme, acme_domain=$domain）"
    body="$(python3 -c 'import json,sys; print(json.dumps({"tls_mode":"acme","acme_domain":sys.argv[1]}))' "$domain")"
    code="$(curl -s -o /dev/null -w '%{http_code}' -b "$jar" -H 'Content-Type: application/json' \
        -X PUT -d "$body" "$PANEL_URL/api/settings")"
    [[ "$code" == "200" ]] || die "保存设置失败（HTTP $code）"

    echo ">> 重启面板使 TLS 生效"
    code="$(curl -s -o /dev/null -w '%{http_code}' -b "$jar" -X POST "$PANEL_URL/api/settings/restart")"
    [[ "$code" == "202" ]] || die "重启请求失败（HTTP $code）"

    # ACME（TLS-ALPN-01）要求 443 公网可达；面板监听端口非 443 时按端口验证。
    local port url
    port="$(python3 -c 'import sys; from urllib.parse import urlparse; print(urlparse(sys.argv[1]).port or 80)' "$PANEL_URL")"
    if [[ "$port" == "443" ]]; then url="https://$domain"; else url="https://$domain:$port"; fi
    echo ">> 等待面板恢复并验证 $url（Let's Encrypt 首次签发可能需数十秒）"
    local i
    for i in $(seq 1 30); do
        if curl -fsS --max-time 5 -o /dev/null "$url" 2>/dev/null; then
            echo ">> done. ACME 证书已生效，面板地址：$url"
            return 0
        fi
        sleep 3
    done
    die "验证超时：$url 不可达（确认域名已解析到本机且 $port 端口公网可达；证书签发日志见 latx log）"
}

cmd_reset_admin() {
    local newpass="${1:?用法: latx reset-admin <newpass>（至少 8 位）}"
    [[ -x "$BACKEND" ]] || die "面板未安装（$BACKEND 不存在）"
    "$BACKEND" -reset-admin "$newpass" -db "$DB_PATH"
}

cmd_uninstall() {
    local purge_db=0 yes=0
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --purge-db) purge_db=1; shift ;;
            --yes|-y)   yes=1; shift ;;
            *)          die "未知参数: $1（用法: latx uninstall [--purge-db] [--yes]）" ;;
        esac
    done
    if [[ "$yes" -ne 1 ]]; then
        local ans
        read -r -p "确认卸载 Lattix 面板？（$INSTALL_ROOT 将被删除） [y/N] " ans
        [[ "$ans" == "y" || "$ans" == "Y" ]] || { echo "已取消"; exit 0; }
    fi
    if use_systemd; then
        need_root
        systemctl stop "$UNIT" 2>/dev/null || true
        systemctl disable "$UNIT" 2>/dev/null || true
        rm -f "$UNIT_FILE"
        systemctl daemon-reload
        echo ">> 已停止并移除 systemd 服务 $UNIT"
    else
        pkill -f "$BACKEND" 2>/dev/null || true
        echo ">> [DEV] 已停止面板进程"
    fi
    if [[ "$purge_db" -eq 1 ]]; then
        rm -rf "$INSTALL_ROOT"
        echo ">> 已删除 $INSTALL_ROOT（含数据库）"
    else
        find "$INSTALL_ROOT" -mindepth 1 -maxdepth 1 ! -name "$(basename "$DB_PATH")" -exec rm -rf {} + 2>/dev/null || true
        echo ">> 已删除 $INSTALL_ROOT（数据库保留在 $DB_PATH，--purge-db 可一并删除）"
    fi
    rm -f "$LATX_BIN"
    echo ">> done. Lattix 面板已卸载。"
}

cmd_version() {
    echo "latx 版本: $LATX_VERSION"
    echo "面板版本: $(panel_version)"
}

cmd_help() { awk '/^set -euo pipefail/{exit} NR>1' "$0" | sed 's/^# \{0,1\}//'; }

main() {
    local cmd="${1:-help}"; shift || true
    case "$cmd" in
        status)               cmd_status "$@" ;;
        start|stop|restart|enable|disable) cmd_svc "$cmd" ;;
        log)                  cmd_log "$@" ;;
        update)               cmd_update "$@" ;;
        acme)                 cmd_acme "$@" ;;
        reset-admin)          cmd_reset_admin "$@" ;;
        uninstall)            cmd_uninstall "$@" ;;
        version)              cmd_version ;;
        help|-h|--help)       cmd_help ;;
        *)                    die "未知子命令: $cmd（latx help 查看用法）" ;;
    esac
}

main "$@"
