#!/usr/bin/env bash
# Lattix panel installer: native systemd or isolated Docker Compose deployment.
set -euo pipefail

GITHUB_REPO="${GITHUB_REPO:-}"
[[ -n "$GITHUB_REPO" ]] || GITHUB_REPO="{{GITHUB_REPO}}"
VERSION="${LATTIX_VERSION:-}"
[[ -n "$VERSION" ]] || VERSION="{{LATTIX_VERSION}}"
MODE=""
INSTALL_DOCKER=0
BIND_ADDRESS=""
PORT=""
ADMIN_USER=""
ADMIN_PASS=""
PUBLIC_URL=""
CONFIG_DIR=""

die() { echo "install-panel.sh: $*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --mode)           MODE="${2:?--mode requires native|docker}"; shift 2 ;;
        --version)        VERSION="${2:?--version requires a value}"; shift 2 ;;
        --install-docker) INSTALL_DOCKER=1; shift ;;
        --bind)           BIND_ADDRESS="${2:?--bind requires an address}"; shift 2 ;;
        --port)           PORT="${2:?--port requires a value}"; shift 2 ;;
        --admin-user)     ADMIN_USER="${2:?--admin-user requires a value}"; shift 2 ;;
        --admin-pass)     ADMIN_PASS="${2:?--admin-pass requires a value}"; shift 2 ;;
        --public-url)     PUBLIC_URL="${2:?--public-url requires a value}"; shift 2 ;;
        --config-dir)     CONFIG_DIR="${2:?--config-dir requires a value}"; shift 2 ;;
        *)                die "unknown argument: $1" ;;
    esac
done

case "$MODE" in
    native|docker) ;;
    "") die "--mode native|docker is required" ;;
    *) die "unsupported mode: $MODE" ;;
esac
if [[ -n "$CONFIG_DIR" ]]; then
    [[ "$CONFIG_DIR" == /* && "$CONFIG_DIR" != "/" && "$CONFIG_DIR" != *[[:space:]]* ]] \
        || die "--config-dir must be an absolute directory without whitespace and other than /"
fi
[[ "$VERSION" != *"{{"* && "$VERSION" == v* ]] || die "--version vX.Y.Z is required"
[[ "$GITHUB_REPO" != *"{{"* ]] || die "GITHUB_REPO is not configured"
[[ "$(uname -s)" == "Linux" ]] || die "only Linux is supported"

case "$(uname -m)" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    *)       die "unsupported architecture: $(uname -m)" ;;
esac

random_password() {
    local value=""
    while [[ ${#value} -lt 8 ]]; do
        value+="$(LC_ALL=C tr -dc 'A-Za-z' </dev/urandom | head -c 8 || true)"
    done
    printf '%s' "${value:0:8}"
}

need_root() {
    if [[ "$(id -u)" -eq 0 ]]; then
        ROOT=()
    elif command -v sudo >/dev/null; then
        ROOT=(sudo)
    else
        die "root or sudo is required"
    fi
}

docker_cmd() {
    if docker info >/dev/null 2>&1; then
        docker "$@"
    elif command -v sudo >/dev/null && sudo docker info >/dev/null 2>&1; then
        sudo docker "$@"
    else
        die "cannot access Docker daemon"
    fi
}

install_docker_engine() {
    need_root
    command -v curl >/dev/null || die "curl is required to install Docker"
    local installer
    installer="$(mktemp)"
    trap 'rm -f "$installer"' RETURN
    curl -fsSL https://get.docker.com -o "$installer"
    "${ROOT[@]}" sh "$installer"
    "${ROOT[@]}" systemctl enable --now docker
}

ensure_docker() {
    if command -v docker >/dev/null && docker_cmd compose version >/dev/null 2>&1; then
        return
    fi
    if [[ "$INSTALL_DOCKER" -eq 1 ]]; then
        install_docker_engine
    elif [[ -t 0 ]]; then
        read -r -p "未检测到 Docker Engine + Compose，是否由安装器安装？[y/N] " answer
        [[ "$answer" =~ ^[Yy]$ ]] || die "Docker is required"
        install_docker_engine
    else
        die "Docker Engine + Compose is required; pass --install-docker to install it"
    fi
    docker_cmd compose version >/dev/null 2>&1 || die "Docker Compose plugin installation failed"
}

env_value() {
    local file="$1" key="$2"
    [[ -f "$file" ]] || return 0
    local value
    value="$(sed -n "s/^${key}=//p" "$file" | tail -1)"
    printf '%s' "${value//\$\$/\$}"
}

env_escape() {
    local value="$1"
    printf '%s' "${value//\$/\$\$}"
}

write_env() {
    local file="$1"
    local old_user old_pass old_bind old_port old_public
    old_user="$(env_value "$file" LATTIX_ADMIN_USER)"
    old_pass="$(env_value "$file" LATTIX_ADMIN_PASS)"
    old_bind="$(env_value "$file" LATTIX_BIND)"
    old_port="$(env_value "$file" LATTIX_PORT)"
    old_public="$(env_value "$file" LATTIX_PUBLIC_URL)"

    ADMIN_USER="${ADMIN_USER:-${old_user:-admin}}"
    ADMIN_PASS="${ADMIN_PASS:-${old_pass:-$(random_password)}}"
    BIND_ADDRESS="${BIND_ADDRESS:-${old_bind:-127.0.0.1}}"
    PORT="${PORT:-${old_port:-8080}}"
    PUBLIC_URL="${PUBLIC_URL:-$old_public}"

    [[ "$ADMIN_USER" != *$'\n'* && "$ADMIN_PASS" != *$'\n'* ]] || die "credentials cannot contain newlines"
    [[ "$PORT" =~ ^[0-9]+$ && "$PORT" -ge 1 && "$PORT" -le 65535 ]] || die "invalid port: $PORT"

    local tmp="${file}.new"
    cat >"$tmp" <<EOF
LATTIX_VERSION=$VERSION
LATTIX_BIND=$(env_escape "$BIND_ADDRESS")
LATTIX_PORT=$PORT
LATTIX_ADMIN_USER=$(env_escape "$ADMIN_USER")
LATTIX_ADMIN_PASS=$(env_escape "$ADMIN_PASS")
LATTIX_PUBLIC_URL=$(env_escape "$PUBLIC_URL")
LATTIX_TIMEZONE=Asia/Shanghai
LATTIX_DEPLOY_MODE=docker
LATTIX_DB=/data/lattix.db
LATTIX_TLS_DIR=/data/certs
LATTIX_ACME_CACHE=/data/acme-cache
EOF
    chmod 0600 "$tmp"
    mv "$tmp" "$file"
}

install_docker_mode() {
    local root="${CONFIG_DIR:-${LATX_DOCKER_ROOT:-/opt/lattix-panel}}"
    ensure_docker
    need_root
    "${ROOT[@]}" install -d -m 0755 "$root" "$root/config"
    "${ROOT[@]}" install -d -m 0750 -o 10001 -g 10001 \
        "$root/data" "$root/data/certs" "$root/data/acme-cache"

    local work
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' RETURN
    if [[ -f "$root/config/.env" ]]; then
        "${ROOT[@]}" cp "$root/config/.env" "$work/.env"
        "${ROOT[@]}" chown "$(id -u):$(id -g)" "$work/.env"
    fi
    write_env "$work/.env"
    cat >"$work/compose.yaml" <<'YAML'
services:
  lattix:
    image: ghcr.io/beanya/lattix:${LATTIX_VERSION}
    container_name: lattix-panel
    restart: unless-stopped
    env_file:
      - config/.env
    ports:
      - "${LATTIX_BIND:-127.0.0.1}:${LATTIX_PORT:-8080}:8080"
    volumes:
      - ./data:/data
YAML
    "${ROOT[@]}" install -m 0600 "$work/.env" "$root/config/.env"
    "${ROOT[@]}" install -m 0644 "$work/compose.yaml" "$root/compose.yaml"

    docker_cmd compose --project-directory "$root" --env-file "$root/config/.env" pull
    docker_cmd compose --project-directory "$root" --env-file "$root/config/.env" up -d
    echo
    echo "Lattix Docker 面板安装完成：$VERSION"
    echo "访问地址: ${PUBLIC_URL:-http://${BIND_ADDRESS}:${PORT}}"
    echo "管理员:   $ADMIN_USER"
    echo "初始密码: $ADMIN_PASS"
    echo "配置目录: $root"
}

install_native_mode() {
    if [[ "${LATX_DEV:-0}" == "1" ]]; then
        ROOT=()
    else
        need_root
    fi
    command -v curl >/dev/null || die "curl is required"
    command -v sha256sum >/dev/null || die "sha256sum is required"
    command -v tar >/dev/null || die "tar is required"

    local root="${CONFIG_DIR:-${LATX_ROOT:-/usr/local/lattix-panel}}"
    local unit="${LATX_UNIT:-lattix-panel}"
    local config="$root/config.env"
    local release="${LATX_RELEASE_BASE:-https://github.com/${GITHUB_REPO}/releases/download/${VERSION}}"
    local asset="lattix-panel-linux-${ARCH}.tar.gz"
    local work
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' RETURN
    curl -fsSL "$release/$asset" -o "$work/$asset"
    curl -fsSL "$release/checksums.txt" -o "$work/checksums.txt"
    (cd "$work" && grep " ${asset}$" checksums.txt | sha256sum -c - >/dev/null) \
        || die "$asset SHA256 verification failed"
    tar -C "$work" -xzf "$work/$asset"

    ADMIN_USER="${ADMIN_USER:-$(env_value "$config" LATTIX_ADMIN_USER)}"
    ADMIN_PASS="${ADMIN_PASS:-$(env_value "$config" LATTIX_ADMIN_PASS)}"
    PUBLIC_URL="${PUBLIC_URL:-$(env_value "$config" LATTIX_PUBLIC_URL)}"
    ADMIN_USER="${ADMIN_USER:-admin}"
    ADMIN_PASS="${ADMIN_PASS:-$(random_password)}"
    PORT="${PORT:-8080}"
    BIND_ADDRESS="${BIND_ADDRESS:-0.0.0.0}"
    [[ "$ADMIN_USER" != *$'\n'* && "$ADMIN_PASS" != *$'\n'* ]] || die "credentials cannot contain newlines"
    [[ "$PORT" =~ ^[0-9]+$ && "$PORT" -ge 1 && "$PORT" -le 65535 ]] || die "invalid port: $PORT"
    if [[ "${LATX_DEV:-0}" == "1" ]]; then
        pkill -f "^${root}/lattix-backend([[:space:]]|$)" >/dev/null 2>&1 || true
    else
        "${ROOT[@]}" systemctl stop "$unit" >/dev/null 2>&1 || true
    fi
    "${ROOT[@]}" install -d -m 0755 "$root"
    "${ROOT[@]}" install -d -m 0700 "$root/data" "$root/data/certs" "$root/data/acme-cache"
    "${ROOT[@]}" install -m 0755 "$work/lattix-panel/lattix-backend" "$root/lattix-backend"
    local latx_bin="${LATX_BIN:-/usr/local/bin/latx}"
    "${ROOT[@]}" install -d -m 0755 "$(dirname "$latx_bin")"
    "${ROOT[@]}" install -m 0755 "$work/lattix-panel/latx" "$root/latx"
    local quoted_root quoted_bin quoted_latx
    printf -v quoted_root '%q' "$root"
    printf -v quoted_bin '%q' "$latx_bin"
    printf -v quoted_latx '%q' "$root/latx"
    "${ROOT[@]}" tee "$latx_bin" >/dev/null <<EOF
#!/usr/bin/env bash
LATX_ROOT=$quoted_root LATX_BIN=$quoted_bin exec $quoted_latx "\$@"
EOF
    "${ROOT[@]}" chmod 0755 "$latx_bin"
    "${ROOT[@]}" install -m 0600 /dev/null "$config"
    "${ROOT[@]}" tee "$config" >/dev/null <<EOF
LATTIX_ADMIN_USER=$ADMIN_USER
LATTIX_ADMIN_PASS=$ADMIN_PASS
LATTIX_PUBLIC_URL=$PUBLIC_URL
LATTIX_DEPLOY_MODE=native
LATTIX_DB=$root/data/lattix.db
LATTIX_TLS_DIR=$root/data/certs
LATTIX_ACME_CACHE=$root/data/acme-cache
EOF
    if [[ "${LATX_DEV:-0}" == "1" ]]; then
        set -a
        # shellcheck disable=SC1090
        source "$config"
        set +a
        nohup "$root/lattix-backend" -addr "$BIND_ADDRESS:$PORT" >"$root/panel.log" 2>&1 &
    else
        "${ROOT[@]}" tee "/etc/systemd/system/${unit}.service" >/dev/null <<EOF
[Unit]
Description=Lattix Panel
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
UMask=0077
EnvironmentFile=$config
ExecStart=$root/lattix-backend -addr $BIND_ADDRESS:$PORT
Restart=always
RestartSec=1
TimeoutStopSec=15
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
EOF
        "${ROOT[@]}" systemctl daemon-reload
        "${ROOT[@]}" systemctl enable --now "$unit"
    fi
    local probe_host="$BIND_ADDRESS"
    [[ "$probe_host" == "0.0.0.0" || "$probe_host" == "::" ]] && probe_host="127.0.0.1"
    local ready=0
    for _ in $(seq 1 30); do
        if curl -fsS --max-time 2 "http://${probe_host}:${PORT}/" >/dev/null 2>&1; then
            ready=1
            break
        fi
        sleep 1
    done
    [[ "$ready" -eq 1 ]] || die "panel did not become ready on ${probe_host}:${PORT}"
    echo
    echo "Lattix 原生面板安装完成：$VERSION"
    echo "访问地址: ${PUBLIC_URL:-http://${BIND_ADDRESS}:${PORT}}"
    echo "管理员:   $ADMIN_USER"
    echo "初始密码: $ADMIN_PASS"
    echo "配置目录: $root"
}

if [[ "$MODE" == "docker" ]]; then
    install_docker_mode
else
    install_native_mode
fi
