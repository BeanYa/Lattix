#!/bin/sh
# Lattix 面板交互式管理程序（latx）。
#
# 由 install-panel.sh 安装为 /usr/local/bin/latx（CI 发版时烧入版本与仓库占位符，
# 见 .github/workflows/release.yml）。无参数运行进入交互式运维菜单，也可直接使用子命令：
#
#   latx                         打开交互式运维菜单
#   latx status                  面板状态：服务/进程、监听端口、版本、面板地址
#   latx start|stop|restart      systemctl 包装（需 root）
#   latx enable|disable          systemctl 包装（需 root）
#   latx log [-n N]              journalctl -u lattix-panel -f（-n N 时不跟随）
#   latx update [version]        从 GitHub release 更新面板（默认 latest，失败回滚 .bak）
#   latx acme <domain>           引导式申请 ACME 证书并切 HTTPS（设置页 API，重启生效）
#   latx cert <domain> [port]    用 acme.sh 申请证书到面板 data/certs 目录并切 HTTPS
#   latx bbr                     开启 BBR 拥塞控制（写入独立 sysctl 配置）
#   latx reset-admin <newpass>   重置管理员密码并轮换会话密钥（重置即全部会话失效）
#   latx uninstall [--purge-db] [--yes]   卸载面板（默认保留 DB 并提示路径）
#   latx version                 latx 自身版本与面板版本
#   latx help                    本帮助
#
# 环境变量覆盖（e2e/运维用）：
#   LATX_ROOT       安装根目录（默认 /usr/local/lattix-panel）
#   LATX_UNIT       systemd unit 名（默认 lattix-panel）
#   LATX_BIN        latx 安装路径（默认 /usr/local/bin/latx，uninstall 时删除）
#   LATX_PANEL_URL  面板本机地址（默认 http://127.0.0.1:8080，acme 用）
#   LATX_ADMIN_USER / LATX_ADMIN_PASS   acme/cert 登录凭据（缺省 read -s 提示）
#   LATX_LANG       交互菜单语言（en|zh；未设置时启动选择，默认 en）
#   LATX_DEV=1      无 systemd 开发模式：status 降级为进程检查，log 读 panel.log

# ===== CI 发版烧入区（release.yml 在发版时 sed 替换以下两个占位符）=====
LATX_VERSION="{{LATTIX_VERSION}}"
GITHUB_REPO="{{GITHUB_REPO}}"

INSTALL_ROOT="${LATX_ROOT:-/usr/local/lattix-panel}"
UNIT="${LATX_UNIT:-lattix-panel}"
UNIT_FILE="/etc/systemd/system/${UNIT}.service"
LATX_BIN="${LATX_BIN:-/usr/local/bin/latx}"
BACKEND="$INSTALL_ROOT/lattix-backend"
DB_PATH="$INSTALL_ROOT/data/lattix.db"
PANEL_URL="${LATX_PANEL_URL:-http://127.0.0.1:8080}"

# Release 包会在最小 Docker 镜像中执行 `latx version` 预检。该路径只用 POSIX sh，
# 其余运维命令仍交给 Bash，以保留数组、[[ ]] 等完整功能。
if [ "${1:-}" = "version" ] && [ -z "${BASH_VERSION:-}" ]; then
    echo "latx 版本: $LATX_VERSION"
    if [ -x "$BACKEND" ]; then
        panel_ver="$("$BACKEND" -version 2>/dev/null || printf '%s\n' unknown)"
    else
        panel_ver="not installed"
    fi
    echo "面板版本: $panel_ver"
    exit 0
fi
if [ -z "${BASH_VERSION:-}" ]; then
    if command -v bash >/dev/null 2>&1; then
        exec bash "$0" "$@"
    fi
    echo "latx: 此命令需要 bash" >&2
    exit 127
fi

set -euo pipefail

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

PANEL_SESSION_JAR=""
PANEL_CSRF=""

panel_login() {
    command -v curl >/dev/null    || die "curl is required"
    command -v python3 >/dev/null || die "python3 is required"

    local user="${LATX_ADMIN_USER:-admin}"
    local pass="${LATX_ADMIN_PASS:-}"
    if [[ -z "$pass" ]]; then
        read -r -s -p "面板管理员密码（$user）: " pass || true; echo
        [[ -n "$pass" ]] || die "密码不能为空（或用 LATX_ADMIN_PASS 环境变量传入）"
    fi

    PANEL_SESSION_JAR="$(mktemp)"
    trap 'rm -f "$PANEL_SESSION_JAR"' EXIT

    local body response
    echo ">> 登录面板 $PANEL_URL"
    body="$(python3 -c 'import json,sys; print(json.dumps({"username":sys.argv[1],"password":sys.argv[2]}))' "$user" "$pass")"
    response="$(curl -fsS -c "$PANEL_SESSION_JAR" -H "Origin: $PANEL_URL" \
        -H 'Content-Type: application/json' -d "$body" "$PANEL_URL/api/auth/login")" \
        || die "登录请求失败，请检查面板地址"
    PANEL_CSRF="$(printf '%s' "$response" | python3 -c '
import json,sys
r=json.load(sys.stdin)
if r.get("code") != "OK": raise SystemExit(r.get("message") or r.get("code"))
print(r["data"]["csrf_token"])
')" || die "登录失败，请检查管理员凭据"
}

panel_save_tls() {
    local mode="$1" domain="$2" field body response
    case "$mode" in
        acme) field="acme_domain" ;;
        path) field="tls_domain" ;;
        *) die "不支持的 TLS 模式: $mode" ;;
    esac
    body="$(python3 -c 'import json,sys; print(json.dumps({"tls_mode":sys.argv[1],sys.argv[2]:sys.argv[3]}))' \
        "$mode" "$field" "$domain")"
    response="$(curl -fsS -b "$PANEL_SESSION_JAR" -H "Origin: $PANEL_URL" \
        -H "X-CSRF-Token: $PANEL_CSRF" -H "Idempotency-Key: $(openssl rand -hex 16)" \
        -H 'Content-Type: application/json' -X POST -d "$body" "$PANEL_URL/api/setting/update")" \
        || die "保存 TLS 设置请求失败"
    [[ "$(printf '%s' "$response" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("code",""))')" == "OK" ]] \
        || die "保存 TLS 设置失败"
}

panel_restart() {
    local response
    response="$(curl -fsS -b "$PANEL_SESSION_JAR" -H "Origin: $PANEL_URL" \
        -H "X-CSRF-Token: $PANEL_CSRF" -H "Idempotency-Key: $(openssl rand -hex 16)" \
        -H 'Content-Type: application/json' -X POST -d '{}' "$PANEL_URL/api/panel/restart")" \
        || die "重启请求失败"
    [[ "$(printf '%s' "$response" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("code",""))')" == "ACCEPTED" ]] \
        || die "重启请求失败"
}

wait_https() {
    local domain="$1" port url i
    port="$(python3 -c 'import sys; from urllib.parse import urlparse; print(urlparse(sys.argv[1]).port or 80)' "$PANEL_URL")"
    if [[ "$port" == "443" ]]; then url="https://$domain"; else url="https://$domain:$port"; fi
    echo ">> 等待面板恢复并验证 $url"
    for i in $(seq 1 30); do
        if curl -fsS --max-time 5 -o /dev/null "$url" 2>/dev/null; then
            echo ">> done. HTTPS 已生效，面板地址：$url"
            return 0
        fi
        sleep 3
    done
    die "验证超时：$url 不可达（确认域名已解析到本机、端口公网可达；日志见 latx log）"
}

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
    local arch
    case "$(uname -m)" in
        x86_64) arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *) die "不支持的面板架构: $(uname -m)" ;;
    esac
    need_root
    use_systemd || die "未检测到 systemd，无法自动更新（LATX_DEV=1 开发模式请手动替换二进制）"
    [[ "$GITHUB_REPO" != *"{{"* ]] || die "脚本未经 CI stamp（{{GITHUB_REPO}} 占位符未替换），无法定位 release"
	command -v curl >/dev/null      || die "curl is required"
	command -v sha256sum >/dev/null || die "sha256sum is required"
	command -v tar >/dev/null       || die "tar is required"
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
    local asset="lattix-panel-linux-${arch}.tar.gz"
    curl -fsSL -o "$tmp/$asset" "$base/$asset" \
        || die "下载失败：release ${version} 无 $asset"
    curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" \
        || die "未获取到 release 校验文件 checksums.txt，中止更新"
    (cd "$tmp" && grep "lattix-panel-linux-${arch}.tar.gz$" checksums.txt | sha256sum -c - >/dev/null) \
        || die "面板包 SHA256 校验失败（release ${version} checksums.txt）"
    echo ">> 面板包 SHA256 校验通过"
    tar -C "$tmp" -xzf "$tmp/$asset"
	[[ -f "$tmp/lattix-panel/lattix-backend" && -f "$tmp/lattix-panel/latx" ]] \
		|| die "面板包内容异常（缺 lattix-backend 或 latx）"
	chmod 0755 "$tmp/lattix-panel/lattix-backend" "$tmp/lattix-panel/latx"
	local packaged_version packaged_cli_version
	packaged_version="$("$tmp/lattix-panel/lattix-backend" -version 2>/dev/null)" \
		|| die "预检失败：新面板二进制无法运行"
	[[ "$packaged_version" == "$version" ]] \
		|| die "预检失败：新面板版本不符（期望 ${version}，实际 ${packaged_version}）"
	packaged_cli_version="$(LATX_ROOT="$tmp/lattix-panel" "$tmp/lattix-panel/latx" version 2>/dev/null \
		| sed -n 's/^latx 版本: //p' | head -1)"
	[[ "$packaged_cli_version" == "$version" ]] \
		|| die "预检失败：新 latx 版本不符（期望 ${version}，实际 ${packaged_cli_version:-unknown}）"

	systemctl stop "$UNIT"
	# 备份 → 替换 → 启动 → 版本与 ready 探测；失败回滚 .bak 并重启（同 latx-ag xray-update）。
	cp -a "$BACKEND" "$BACKEND.bak"
	[[ ! -f "$INSTALL_ROOT/latx" ]] || cp -a "$INSTALL_ROOT/latx" "$INSTALL_ROOT/latx.bak"
	install -m 0755 "$tmp/lattix-panel/lattix-backend" "$BACKEND"
	install -m 0755 "$tmp/lattix-panel/latx" "$INSTALL_ROOT/latx"
	if ! systemctl start "$UNIT"; then
		mv "$BACKEND.bak" "$BACKEND"
		[[ ! -f "$INSTALL_ROOT/latx.bak" ]] || mv "$INSTALL_ROOT/latx.bak" "$INSTALL_ROOT/latx"
		systemctl restart "$UNIT" || true
		die "启动面板失败，已回滚 .bak 并重启"
	fi

	local new_version ready=0
	new_version="$(panel_version)"
	if [[ "$new_version" != "$version" ]]; then
		mv "$BACKEND.bak" "$BACKEND"
		[[ ! -f "$INSTALL_ROOT/latx.bak" ]] || mv "$INSTALL_ROOT/latx.bak" "$INSTALL_ROOT/latx"
		systemctl restart "$UNIT" || true
		die "更新后版本校验失败（期望 ${version}，实际 ${new_version}），已回滚 .bak 并重启"
	fi
	for _ in $(seq 1 30); do
		if systemctl is-active --quiet "$UNIT" && curl -fsS --max-time 2 "$PANEL_URL/readyz" >/dev/null 2>&1; then
			ready=1
			break
		fi
		sleep 1
	done
	if [[ "$ready" -ne 1 ]]; then
		mv "$BACKEND.bak" "$BACKEND"
		[[ ! -f "$INSTALL_ROOT/latx.bak" ]] || mv "$INSTALL_ROOT/latx.bak" "$INSTALL_ROOT/latx"
		systemctl restart "$UNIT" || true
		die "面板已替换为 ${new_version}，但服务未在 30 秒内恢复就绪，已回滚 .bak 并重启（详情见 latx log -n 100）"
	fi
	rm -f "$BACKEND.bak" "$INSTALL_ROOT/latx.bak"
	echo ">> done. 面板与 latx 已更新至 ${new_version}；Agent 会自动重连。"
}

cmd_acme() {
    local domain="${1:?用法: latx acme <domain>}"
    panel_login
    echo ">> 保存 ACME 设置（tls_mode=acme, acme_domain=$domain）"
    panel_save_tls acme "$domain"
    echo ">> 重启面板使 TLS 生效"
    panel_restart
    wait_https "$domain"
}

install_socat() {
    command -v socat >/dev/null && return 0
    [[ "$(id -u)" -eq 0 ]] || die "申请证书需要 socat；请先安装，或使用 sudo 执行 latx cert"
    [[ -r /etc/os-release ]] || die "无法识别系统，请手动安装 socat"
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-}" in
        ubuntu|debian|armbian) apt-get update && apt-get install -y socat ;;
        centos|rhel|almalinux|rocky|ol) yum install -y socat ;;
        fedora) dnf install -y socat ;;
        arch|manjaro) pacman -Sy --noconfirm socat ;;
        *) die "暂不支持自动安装 socat（系统 ${ID:-unknown}），请手动安装后重试" ;;
    esac
}

cmd_cert() {
    local domain="${1:?用法: latx cert <domain> [http-port]}"
    local http_port="${2:-80}"
    [[ $# -le 2 ]] || die "用法: latx cert <domain> [http-port]"
    [[ "$domain" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$ ]] \
        || die "无效域名: $domain"
    [[ "$http_port" =~ ^[0-9]+$ ]] && (( http_port >= 1 && http_port <= 65535 )) \
        || die "无效 HTTP 验证端口: $http_port"

    panel_login
    local settings tls_dir cert_dir acme
    settings="$(curl -fsS -b "$PANEL_SESSION_JAR" "$PANEL_URL/api/setting/get")" \
        || die "读取面板设置失败"
    tls_dir="$(printf '%s' "$settings" | python3 -c \
        'import json,sys; print(json.load(sys.stdin).get("data", {}).get("tls_dir", ""))')" \
        || die "解析面板证书目录失败"
    [[ -n "$tls_dir" && "$tls_dir" == /* ]] || die "面板返回的证书目录无效: $tls_dir"
    cert_dir="$tls_dir/$domain"
    mkdir -p "$cert_dir" 2>/dev/null \
        || die "无法创建证书目录 $cert_dir；请以拥有该目录权限的用户执行"
    [[ -w "$cert_dir" ]] || die "证书目录不可写: $cert_dir"

    install_socat
    acme="${HOME:?HOME 未设置}/.acme.sh/acme.sh"
    if [[ ! -x "$acme" ]]; then
        echo ">> 安装 acme.sh 到 $HOME/.acme.sh"
        (cd "$HOME" && curl -fsSL https://get.acme.sh | sh) \
            || die "acme.sh 安装失败"
    fi
    [[ -x "$acme" ]] || die "未找到可执行的 acme.sh: $acme"

    echo ">> 申请 $domain 证书（HTTP standalone，端口 $http_port）"
    "$acme" --set-default-ca --server letsencrypt
    "$acme" --issue -d "$domain" --standalone --httpport "$http_port"
    "$acme" --install-cert -d "$domain" \
        --key-file "$cert_dir/privkey.pem" \
        --fullchain-file "$cert_dir/fullchain.pem"
    chmod 600 "$cert_dir/privkey.pem"
    chmod 644 "$cert_dir/fullchain.pem"
    "$acme" --upgrade --auto-upgrade

    echo ">> 证书已写入 $cert_dir，保存域名路径 TLS 设置"
    panel_save_tls path "$domain"
    echo ">> 重启面板使 HTTPS 生效；后续续期证书将自动热加载"
    panel_restart
    wait_https "$domain"
}

cmd_bbr() {
    need_root
    command -v sysctl >/dev/null || die "sysctl is required"
    if ! sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null | grep -qw bbr; then
        command -v modprobe >/dev/null && modprobe tcp_bbr 2>/dev/null || true
    fi
    sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null | grep -qw bbr \
        || die "当前内核不支持 BBR，请先升级到支持 BBR 的 Linux 内核"

    local config="/etc/sysctl.d/99-lattix-bbr.conf"
    printf '%s\n' \
        'net.core.default_qdisc=fq' \
        'net.ipv4.tcp_congestion_control=bbr' >"$config"
    sysctl --system >/dev/null
    [[ "$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)" == "bbr" ]] \
        || die "BBR 配置已写入 $config，但未能立即生效"
    echo ">> BBR 已开启（qdisc=fq，配置：$config）"
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

cmd_help() { awk 'NR == 1 {next} /^#/ {sub(/^# ?/, ""); print; next} {exit}' "$0"; }

MENU_LANG="en"

menu_phrase() {
    if [[ "$MENU_LANG" == "zh" ]]; then
        printf '%s' "$2"
    else
        printf '%s' "$1"
    fi
}

select_menu_language() {
    local choice="${LATX_LANG:-}"
    case "$choice" in
        zh|ZH|2) MENU_LANG="zh"; return ;;
        en|EN|1) MENU_LANG="en"; return ;;
        "") ;;
        *) echo "Unsupported LATX_LANG: $choice; using English." >&2; MENU_LANG="en"; return ;;
    esac

    cat <<'EOF'
Select language / 选择语言
  1. English (default)
  2. 中文
EOF
    read -r -p "Language [1]: " choice || true
    case "$choice" in
        2|zh|ZH) MENU_LANG="zh" ;;
        *) MENU_LANG="en" ;;
    esac
}

pause_menu() {
    local unused
    echo
    read -r -p "$(menu_phrase "Press Enter to return to the main menu..." "按 Enter 返回主菜单...")" unused || true
}

show_menu() {
    local choice domain port version newpass confirm_pass lines
    select_menu_language
    while true; do
        [[ -t 1 ]] && clear || true
        if [[ "$MENU_LANG" == "zh" ]]; then
            cat <<'EOF'
============================================================
  Lattix 面板运维菜单
============================================================
  面板服务
    1. 查看面板状态          2. 启动面板
    3. 停止面板              4. 重启面板
    5. 开启开机自启          6. 关闭开机自启
    7. 查看最近日志

  更新与网络
    8. 更新面板              9. 开启 BBR

  HTTPS 证书
   10. 申请证书（acme.sh）  11. 使用面板内置 ACME

  账户与维护
   12. 重置管理员密码       13. 查看版本
   14. 卸载面板

    0. 退出
============================================================
EOF
        else
            cat <<'EOF'
============================================================
  Lattix Panel Operations
============================================================
  Panel Service
    1. Show panel status       2. Start panel
    3. Stop panel              4. Restart panel
    5. Enable autostart        6. Disable autostart
    7. Show recent logs

  Updates and Network
    8. Update panel            9. Enable BBR

  HTTPS Certificates
   10. Issue certificate (acme.sh)
   11. Use built-in ACME

  Account and Maintenance
   12. Reset admin password   13. Show version
   14. Uninstall panel

    0. Exit
============================================================
EOF
        fi
        read -r -p "$(menu_phrase "Select [0-14]: " "请选择 [0-14]: ")" choice || { echo; return 0; }
        case "$choice" in
            0) return 0 ;;
            1) cmd_status; pause_menu ;;
            2) cmd_svc start; pause_menu ;;
            3) cmd_svc stop; pause_menu ;;
            4) cmd_svc restart; pause_menu ;;
            5) cmd_svc enable; pause_menu ;;
            6) cmd_svc disable; pause_menu ;;
            7)
                read -r -p "$(menu_phrase "Number of log lines [50]: " "显示最近多少行日志 [50]: ")" lines
                cmd_log -n "${lines:-50}"
                pause_menu
                ;;
            8)
                read -r -p "$(menu_phrase "Target version [latest]: " "目标版本 [latest]: ")" version
                cmd_update "${version:-latest}"
                pause_menu
                ;;
            9)
                read -r -p "$(menu_phrase \
                    "Enable BBR? This writes a system sysctl configuration [y/N]: " \
                    "确认开启 BBR？此操作会写入系统 sysctl 配置 [y/N]: ")" choice
                if [[ "$choice" == "y" || "$choice" == "Y" ]]; then
                    cmd_bbr
                else
                    menu_phrase "Cancelled" "已取消"; echo
                fi
                pause_menu
                ;;
            10)
                read -r -p "$(menu_phrase "Certificate domain: " "证书域名: ")" domain
                read -r -p "$(menu_phrase "HTTP validation port [80]: " "HTTP 验证端口 [80]: ")" port
                cmd_cert "$domain" "${port:-80}"
                pause_menu
                ;;
            11)
                read -r -p "$(menu_phrase "Certificate domain: " "证书域名: ")" domain
                cmd_acme "$domain"
                pause_menu
                ;;
            12)
                read -r -s -p "$(menu_phrase \
                    "New admin password (at least 8 characters): " \
                    "新的管理员密码（至少 8 位）: ")" newpass; echo
                read -r -s -p "$(menu_phrase "Confirm new password: " "再次输入新密码: ")" confirm_pass; echo
                [[ "$newpass" == "$confirm_pass" ]] \
                    || die "$(menu_phrase "Passwords do not match" "两次输入的密码不一致")"
                cmd_reset_admin "$newpass"
                pause_menu
                ;;
            13) cmd_version; pause_menu ;;
            14) cmd_uninstall; return 0 ;;
            *) menu_phrase "Invalid option: $choice" "无效选项: $choice"; echo; pause_menu ;;
        esac
    done
}

main() {
    if [[ $# -eq 0 ]]; then
        show_menu
        return
    fi
    local cmd="$1"; shift
    case "$cmd" in
        status)               cmd_status "$@" ;;
        start|stop|restart|enable|disable) cmd_svc "$cmd" ;;
        log)                  cmd_log "$@" ;;
        update)               cmd_update "$@" ;;
        acme)                 cmd_acme "$@" ;;
        cert)                 cmd_cert "$@" ;;
        bbr)                  cmd_bbr "$@" ;;
        reset-admin)          cmd_reset_admin "$@" ;;
        uninstall)            cmd_uninstall "$@" ;;
        version)              cmd_version ;;
        help|-h|--help)       cmd_help ;;
        *)                    die "未知子命令: $cmd（latx help 查看用法）" ;;
    esac
}

main "$@"
