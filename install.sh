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

wizard() {
    [[ -t 0 ]] || die "no arguments require an interactive terminal; use panel|agent"
    echo "Lattix 安装向导"
    echo "  1) 安装面板"
    echo "  2) 安装 Agent"
    read -r -p "请选择 [1-2]: " choice
    case "$choice" in
        1)
            echo "  1) Docker Compose（推荐）"
            echo "  2) 原生二进制 + systemd"
            read -r -p "请选择面板模式 [1-2]: " mode
            case "$mode" in
                1) set -- panel --mode docker ;;
                2) set -- panel --mode native ;;
                *) die "invalid mode selection" ;;
            esac
            ;;
        2)
            local panel token xray
            read -r -p "面板地址: " panel
            read -r -p "Bootstrap token: " token
            read -r -p "Xray 版本 [latest]: " xray
            set -- agent --panel "$panel" --token "$token" --xray-version "${xray:-latest}"
            ;;
        *) die "invalid component selection" ;;
    esac
    dispatch "$@"
}

dispatch() {
    [[ $# -gt 0 ]] || { wizard; return; }
    local component="$1"
    shift
    case "$component" in
        panel|agent) ;;
        *) die "usage: install.sh panel|agent [options]" ;;
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
                        --mode|--bind|--port|--admin-user|--admin-pass|--public-url|--xray-version|--panel|--token)
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
