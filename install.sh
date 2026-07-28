#!/usr/bin/env bash
# Lattix unified installer entrypoint. It resolves a release, then loads the
# matching tagged child installer so installer logic and artifacts stay aligned.
set -euo pipefail

GITHUB_REPO="${GITHUB_REPO:-BeanYa/Lattix}"
RAW_BASE="${LATX_RAW_BASE:-https://raw.githubusercontent.com/${GITHUB_REPO}}"

die() { echo "install.sh: $*" >&2; exit 1; }

resolve_latest() {
    command -v curl >/dev/null || die "curl is required"
    curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
        | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4
}

run_child() {
    local component="$1" version="$2"
    shift 2
    local child="install-${component}.sh" tmp
    tmp="$(mktemp)"
    trap 'rm -f "$tmp"' RETURN
    curl -fsSL "${RAW_BASE}/${version}/scripts/${child}" -o "$tmp" \
        || die "failed to load ${child} from tag ${version}"
    chmod 0755 "$tmp"
    if GITHUB_REPO="$GITHUB_REPO" "$tmp" --version "$version" "$@"; then
        rm -f "$tmp"
        trap - RETURN
    else
        local status=$?
        rm -f "$tmp"
        trap - RETURN
        return "$status"
    fi
}

read_masked() {
    local prompt="$1" char
    REPLY=""
    printf '%s' "$prompt" >&3
    while IFS= read -r -s -n 1 -u 3 char; do
        if [[ -z "$char" ]]; then
            break
        fi
        case "$char" in
            $'\b'|$'\177')
                if [[ -n "$REPLY" ]]; then
                    REPLY="${REPLY%?}"
                    printf '\b \b' >&3
                fi
                ;;
            *)
                REPLY+="$char"
                printf '*' >&3
                ;;
        esac
    done
    printf '\n' >&3
}

prompt_panel_settings() {
    local default_bind="$1" default_dir="$2"
    local bind port admin_user admin_pass config_dir
    PANEL_OPTIONS=()
    read -r -u 3 -p "部署地址 [$default_bind]: " bind
    read -r -u 3 -p "部署端口 [8080]: " port
    read -r -u 3 -p "管理员账号 [admin]: " admin_user
    read_masked "管理员密码 [留空则随机生成]: "
    admin_pass="$REPLY"
    read -r -u 3 -p "配置目录 [$default_dir]: " config_dir
    [[ -z "$bind" ]] || PANEL_OPTIONS+=(--bind "$bind")
    [[ -z "$port" ]] || PANEL_OPTIONS+=(--port "$port")
    [[ -z "$admin_user" ]] || PANEL_OPTIONS+=(--admin-user "$admin_user")
    [[ -z "$admin_pass" ]] || PANEL_OPTIONS+=(--admin-pass "$admin_pass")
    [[ -z "$config_dir" ]] || PANEL_OPTIONS+=(--config-dir "$config_dir")
}

wizard() {
    # With `curl ... | bash`, stdin carries this script rather than terminal input.
    # Read selections from the controlling terminal so the documented wizard works.
    if ! { exec 3<>/dev/tty; } 2>/dev/null; then
        die "no arguments require an interactive terminal; use panel --mode docker|native"
    fi
    echo "Lattix 面板安装向导"
    echo "  1) Docker Compose（推荐）"
    echo "  2) 原生二进制 + systemd"
    read -r -u 3 -p "请选择面板模式 [1-2]: " mode
    case "$mode" in
        1)
            prompt_panel_settings 127.0.0.1 /opt/lattix-panel
            set -- panel --mode docker "${PANEL_OPTIONS[@]}"
            ;;
        2)
            prompt_panel_settings 0.0.0.0 /usr/local/lattix-panel
            set -- panel --mode native "${PANEL_OPTIONS[@]}"
            ;;
        *) die "invalid mode selection" ;;
    esac
    exec 3>&-
    dispatch "$@"
}

dispatch() {
    [[ $# -gt 0 ]] || { wizard; return; }
    local component="$1"
    shift
    case "$component" in
        panel|agent) ;;
        *) die "usage: install.sh panel [options]" ;;
    esac

    local version="" arg
    local -a forwarded=()
    while [[ $# -gt 0 ]]; do
        arg="$1"
        case "$arg" in
            --version)
                version="${2:?--version requires a value}"
                shift 2
                ;;
            *)
                forwarded+=("$arg")
                shift
                if [[ "$arg" == --* && "$arg" != "--install-docker" && ${#forwarded[@]} -gt 0 ]]; then
                    case "$arg" in
                        --mode|--bind|--port|--admin-user|--admin-pass|--public-url|--config-dir|--xray-version|--panel|--token)
                            [[ $# -gt 0 ]] || die "$arg requires a value"
                            forwarded+=("$1")
                            shift
                            ;;
                    esac
                fi
                ;;
        esac
    done
    if [[ -z "$version" ]]; then
        echo ">> resolving latest Lattix release"
        version="$(resolve_latest)" || true
        [[ -n "$version" ]] || die "failed to resolve latest release; pass --version vX.Y.Z"
    fi
    [[ "$version" == v* ]] || die "version must look like vX.Y.Z"
    run_child "$component" "$version" "${forwarded[@]}"
}

dispatch "$@"
