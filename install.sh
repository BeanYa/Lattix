#!/usr/bin/env bash
# Lattix unified installer entrypoint. It resolves a release, then loads the
# matching child installer — the release asset first, the tagged source file as
# fallback — and verifies it against the release checksums.txt before executing,
# so installer logic and artifacts stay aligned.
#
# GitHub's browser release URL (github.com/<repo>/releases/download/...) can
# return 5xx (notably 504) while the same immutable asset is healthy through the
# GitHub API asset endpoint. Every release download retries briefly and then
# falls back to that endpoint, reusing the same checksums.txt verification.
set -euo pipefail

GITHUB_REPO="${GITHUB_REPO:-BeanYa/Lattix}"
RAW_BASE="${LATX_RAW_BASE:-https://raw.githubusercontent.com/${GITHUB_REPO}}"

die() { echo "install.sh: $*" >&2; exit 1; }

# github_asset_api_url 把 release 资产直链改写为 GitHub API 资产端点：
#   https://github.com/<repo>/releases/download/<tag>/<asset>
# → https://api.github.com/repos/<repo>/releases/assets/<id>
# 仅供 download_file 在直链失败时回退；非 GitHub 直链（镜像/file://）返回非零。
github_asset_api_url() {
    local url="$1" repo tag asset json id
    [[ "$url" =~ ^https://github\.com/([^/]+/[^/]+)/releases/download/([^/]+)/([^/?#]+)$ ]] || return 1
    repo="${BASH_REMATCH[1]}"
    tag="${BASH_REMATCH[2]}"
    asset="${BASH_REMATCH[3]}"
    json="$(curl -fsSL --max-time 30 "https://api.github.com/repos/${repo}/releases/tags/${tag}")" || return 1
    # release JSON 的资产对象形如 {"url": ".../releases/assets/<id>", ..., "name": "<asset>"}；
    # 用 url 行取 id、后续 name 行匹配资产名，不依赖 jq。
    id="$(printf '%s\n' "$json" | awk -v want="$asset" '
        match($0, /releases\/assets\/[0-9]+/) { id = substr($0, RSTART + 16, RLENGTH - 16) }
        /"name": / {
            n = $0; sub(/^.*"name": "/, "", n); sub(/".*$/, "", n)
            if (n == want && id != "") { print id; exit }
        }')"
    [[ -n "$id" ]] || return 1
    printf 'https://api.github.com/repos/%s/releases/assets/%s' "$repo" "$id"
}

download_file() {
    local label="$1" url="$2" destination="$3" api_url
    command -v curl >/dev/null || die "curl is required"
    echo ">> 正在下载 $label"
    if curl --fail --location --show-error --progress-bar \
        --retry 1 --retry-delay 1 --output "$destination" "$url"; then
        echo ">> $label 下载完成"
        return 0
    fi
    if api_url="$(github_asset_api_url "$url")" && [[ -n "$api_url" ]]; then
        echo ">> 直连 release 下载失败，改走 GitHub API 资产端点重试 $label" >&2
        if curl --fail --location --show-error --progress-bar \
            --retry 1 --retry-delay 1 \
            --header 'Accept: application/octet-stream' \
            --output "$destination" "$api_url"; then
            echo ">> $label 下载完成"
            return 0
        fi
    fi
    return 1
}

resolve_latest() {
    command -v curl >/dev/null || die "curl is required"
    curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
        | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4
}

# Verify a downloaded child installer against the release checksums.txt.
# Releases older than the script assets have no matching entry: warn loudly and
# continue, so pinned old versions stay installable.
verify_child() {
    local version="$1" name="$2" file="$3" checksums="$4"
    local expected actual
    expected="$(awk -v f="$name" '$2 == f { print $1; exit }' "$checksums")"
    if [[ -z "$expected" ]]; then
        echo ">> WARNING: release ${version} 的 checksums.txt 无 ${name} 条目（旧版本 release），跳过完整性校验" >&2
        return 0
    fi
    command -v sha256sum >/dev/null || die "sha256sum is required to verify ${name}"
    actual="$(sha256sum "$file" | cut -d' ' -f1)"
    [[ "$actual" == "$expected" ]] \
        || die "${name} SHA256 verification failed (expected ${expected}, got ${actual}); refusing to execute"
    echo ">> ${name} SHA256 校验通过（checksums.txt）"
}

# stage_agent_release 在入口层预取 agent 发布包与 checksums.txt，再把子安装器
# 的 LATX_RELEASE_BASE 指向该本地目录：已发布 tag 的 install-agent.sh 仍从
# github.com 直链取包，入口层先按回退逻辑取到同一份资产后，旧 tag 也能绕开
# release CDN 的 5xx。子安装器照旧校验 checksums.txt，校验边界不变。
stage_agent_release() {
    local version="$1" stage="$2" arch asset base
    case "$(uname -m)" in
        x86_64)        arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *)             return 1 ;;
    esac
    asset="lattix-agent-linux-${arch}.tar.gz"
    base="https://github.com/${GITHUB_REPO}/releases/download/${version}"
    download_file "lattix-agent ${version}（预取）" "$base/$asset" "$stage/$asset" \
        && download_file "agent 校验文件（预取）" "$base/checksums.txt" "$stage/checksums.txt"
}

run_child() {
    local component="$1" version="$2"
    shift 2
    local child="install-${component}.sh" tmp checksums staged=""
    local release_base="${LATX_RELEASE_BASE:-https://github.com/${GITHUB_REPO}/releases/download/${version}}"
    tmp="$(mktemp)"
    checksums="$(mktemp)"
    trap 'rm -f "$tmp" "$checksums"; [[ -z "$staged" ]] || rm -rf "$staged"' RETURN
    # checksums.txt lives in the same trust domain as the release assets. If it
    # cannot be fetched (old release without assets), warn and skip verification
    # rather than breaking installs of pinned old versions.
    local have_checksums=0
    if download_file "checksums.txt" "${release_base}/checksums.txt" "$checksums"; then
        have_checksums=1
    else
        echo ">> WARNING: 未能获取 release ${version} 的 checksums.txt，${child} 跳过完整性校验" >&2
    fi
    # Prefer the release asset (same domain as checksums.txt); fall back to the
    # raw tagged file. Both serve identical content, verified the same way.
    if ! download_file "$child" "${release_base}/${child}" "$tmp"; then
        echo ">> release ${version} 无 ${child} 资产，回退 Git tag 原始文件" >&2
        download_file "$child" "${RAW_BASE}/${version}/scripts/${child}" "$tmp" \
            || die "failed to load ${child} from tag ${version}"
    fi
    if [[ "$have_checksums" -eq 1 ]]; then
        verify_child "$version" "$child" "$tmp" "$checksums"
    fi
    chmod 0755 "$tmp"
    local -a child_env=(env "GITHUB_REPO=$GITHUB_REPO")
    # 未显式指定下载基址（镜像）时，入口层预取 agent 包，让已发布 tag 的旧子
    # 安装器也走回退下载；镜像基址已由使用者保证可达，保持原样交给子安装器。
    if [[ "$component" == "agent" && -z "${LATX_RELEASE_BASE:-}" ]]; then
        staged="$(mktemp -d)"
        if stage_agent_release "$version" "$staged"; then
            child_env+=("LATX_RELEASE_BASE=file://$staged")
        else
            echo ">> WARNING: 入口预取 agent 包失败，交给子安装器重试" >&2
            rm -rf "$staged"
            staged=""
        fi
    fi
    child_env+=("$tmp" --version "$version" "$@")
    if "${child_env[@]}"; then
        rm -f "$tmp" "$checksums"
        [[ -z "$staged" ]] || rm -rf "$staged"
        trap - RETURN
    else
        local status=$?
        rm -f "$tmp" "$checksums"
        [[ -z "$staged" ]] || rm -rf "$staged"
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
