#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/v9.8.7/scripts"

if grep -Eq '\$\{[^}]+:-\{\{' \
    "$repo_root/scripts/install-agent.sh" "$repo_root/scripts/install-panel.sh"; then
    echo "installer placeholders must not be embedded in parameter defaults" >&2
    exit 1
fi
grep -Fq 'XRAY_VERSION="${XRAY_VERSION:-latest}"' "$repo_root/scripts/install-agent.sh" \
    || { echo "agent installer must default XRAY_VERSION to latest" >&2; exit 1; }

for component in panel agent; do
    cat >"$work/v9.8.7/scripts/install-${component}.sh" <<'CHILD'
#!/usr/bin/env bash
printf '%s\n' "$@" >"${LATX_TEST_OUTPUT:?}"
CHILD
    chmod +x "$work/v9.8.7/scripts/install-${component}.sh"
done

# Fake release area mirroring release.yml: script assets byte-identical to the
# tagged files, with their sha256 recorded in checksums.txt.
release="$work/release/v9.8.7"
mkdir -p "$release"
cp "$work/v9.8.7/scripts/install-panel.sh" "$work/v9.8.7/scripts/install-agent.sh" \
    "$release/"
(cd "$release" && sha256sum install-panel.sh install-agent.sh >checksums.txt)

mkdir -p "$work/bin"
cat >"$work/bin/curl" <<'CURL'
#!/usr/bin/env bash
case "$*" in
    *api.github.com/repos/*/releases/latest*)
        printf '%s\n' '{"tag_name":"v9.8.7"}'
        ;;
    *)
        exec /usr/bin/curl "$@"
        ;;
esac
CURL
chmod +x "$work/bin/curl"

LATX_TEST_OUTPUT="$work/panel.args" \
LATX_RAW_BASE="file://$work" \
LATX_RELEASE_BASE="file://$release" \
    bash "$repo_root/install.sh" panel --version v9.8.7 --mode docker --port 9090 \
    >"$work/panel.log" 2>&1
diff -u <(printf '%s\n' --version v9.8.7 --mode docker --port 9090) "$work/panel.args"
grep -Fq '>> 正在下载 install-panel.sh' "$work/panel.log" \
    || { echo "panel child installer download progress is missing" >&2; exit 1; }
grep -Fq '>> install-panel.sh SHA256 校验通过' "$work/panel.log" \
    || { echo "panel child installer checksum verification is missing" >&2; exit 1; }

LATX_TEST_OUTPUT="$work/agent.args" \
LATX_RAW_BASE="file://$work" \
LATX_RELEASE_BASE="file://$release" \
    bash "$repo_root/install.sh" agent --version v9.8.7 \
        --panel https://panel.example.com --token bootstrap \
    >"$work/agent.log" 2>&1
diff -u <(printf '%s\n' --version v9.8.7 --panel https://panel.example.com \
    --token bootstrap) "$work/agent.args"
grep -Fq '>> 正在下载 install-agent.sh' "$work/agent.log" \
    || { echo "agent child installer download progress is missing" >&2; exit 1; }
grep -Fq '>> install-agent.sh SHA256 校验通过' "$work/agent.log" \
    || { echo "agent child installer checksum verification is missing" >&2; exit 1; }

# The panel pins its effective xray version in the install command; the entrypoint
# must forward it to the child installer instead of dropping it.
LATX_TEST_OUTPUT="$work/agent-xray.args" \
LATX_RAW_BASE="file://$work" \
LATX_RELEASE_BASE="file://$release" \
    bash "$repo_root/install.sh" agent --version v9.8.7 \
        --panel https://panel.example.com --token bootstrap \
        --xray-version v26.3.27 \
    >"$work/agent-xray.log" 2>&1
diff -u <(printf '%s\n' --version v9.8.7 --panel https://panel.example.com \
    --token bootstrap --xray-version v26.3.27) "$work/agent-xray.args"

# Release assets unavailable: warn about the missing checksums.txt, fall back to
# the raw tagged file, and continue without verification (old releases).
LATX_TEST_OUTPUT="$work/fallback.args" \
LATX_RAW_BASE="file://$work" \
LATX_RELEASE_BASE="file://$work/no-such-release" \
    bash "$repo_root/install.sh" panel --version v9.8.7 --mode docker \
    >"$work/fallback.log" 2>&1
diff -u <(printf '%s\n' --version v9.8.7 --mode docker) "$work/fallback.args"
grep -Fq 'WARNING' "$work/fallback.log" \
    || { echo "fallback without checksums.txt must print a warning" >&2; exit 1; }
grep -Fq '回退 Git tag 原始文件' "$work/fallback.log" \
    || { echo "fallback must fall back to the raw tagged file" >&2; exit 1; }

# Tampered child installer: hash mismatch must be refused before execution.
tampered="$work/release-tampered/v9.8.7"
mkdir -p "$tampered"
cp "$release/checksums.txt" "$tampered/checksums.txt"
cp "$release/install-panel.sh" "$tampered/install-panel.sh"
echo '# tampered' >>"$tampered/install-panel.sh"
if LATX_TEST_OUTPUT="$work/tampered.args" \
LATX_RELEASE_BASE="file://$tampered" \
    bash "$repo_root/install.sh" panel --version v9.8.7 --mode docker \
    >"$work/tampered.log" 2>&1; then
    echo "tampered child installer must be rejected" >&2
    exit 1
fi
grep -Fq 'SHA256 verification failed' "$work/tampered.log" \
    || { echo "tampered child installer must fail SHA256 verification" >&2; exit 1; }
[[ ! -e "$work/tampered.args" ]] \
    || { echo "tampered child installer must not be executed" >&2; exit 1; }

# Old release: checksums.txt exists but has no script entry -> warn and continue.
old="$work/release-old/v9.8.7"
mkdir -p "$old"
cp "$work/v9.8.7/scripts/install-agent.sh" "$old/install-agent.sh"
echo "0000000000000000000000000000000000000000000000000000000000000000  lattix-agent-linux-amd64.tar.gz" \
    >"$old/checksums.txt"
LATX_TEST_OUTPUT="$work/old.args" \
LATX_RELEASE_BASE="file://$old" \
    bash "$repo_root/install.sh" agent --version v9.8.7 \
        --panel https://panel.example.com --token bootstrap \
    >"$work/old.log" 2>&1
diff -u <(printf '%s\n' --version v9.8.7 --panel https://panel.example.com \
    --token bootstrap) "$work/old.args"
grep -Fq '无 install-agent.sh 条目' "$work/old.log" \
    || { echo "missing checksum entry must print a warning" >&2; exit 1; }

# GitHub release downloads can return 504 at the browser endpoint while the same
# immutable asset is served by the API asset endpoint. The entrypoint must retry
# through the API, then stage the agent bundle for the (possibly old) tagged child
# installer through LATX_RELEASE_BASE=file://, with checksums verified as usual.
fallback="$work/api-fallback"
mkdir -p "$fallback/bin" "$fallback/release"
case "$(uname -m)" in
    x86_64)  arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) echo "unsupported test arch: $(uname -m)" >&2; exit 1 ;;
esac
printf 'fake agent bundle\n' >"$fallback/release/lattix-agent-linux-${arch}.tar.gz"
cat >"$fallback/release/install-agent.sh" <<'CHILD'
#!/usr/bin/env bash
{
    printf 'RELEASE_BASE=%s\n' "${LATX_RELEASE_BASE:-}"
    printf 'ARG=%s\n' "$@"
    if [[ "${LATX_RELEASE_BASE:-}" == file://* ]]; then
        ls "${LATX_RELEASE_BASE#file://}"
    fi
} >"${LATX_TEST_OUTPUT:?}"
CHILD
chmod +x "$fallback/release/install-agent.sh"
(cd "$fallback/release" && sha256sum "lattix-agent-linux-${arch}.tar.gz" install-agent.sh >checksums.txt)
cat >"$fallback/api.json" <<JSON
{
  "url": "https://api.github.com/repos/BeanYa/Lattix/releases/1",
  "assets": [
    { "url": "https://api.github.com/repos/BeanYa/Lattix/releases/assets/1", "id": 1, "name": "install-agent.sh" },
    { "url": "https://api.github.com/repos/BeanYa/Lattix/releases/assets/2", "id": 2, "name": "checksums.txt" },
    { "url": "https://api.github.com/repos/BeanYa/Lattix/releases/assets/3", "id": 3, "name": "lattix-agent-linux-${arch}.tar.gz" }
  ]
}
JSON
cat >"$fallback/bin/curl" <<'CURL'
#!/usr/bin/env bash
out="" url="" prev=""
for arg in "$@"; do
    [[ "$prev" == "--output" || "$prev" == "-o" ]] && out="$arg"
    [[ "$arg" == http* ]] && url="$arg"
    prev="$arg"
done
case "$url" in
    *github.com/*/releases/download/*) echo "simulated 504" >&2; exit 22 ;;
    *api.github.com/repos/BeanYa/Lattix/releases/tags/v9.8.7) cat "$FAKE_API_JSON" ;;
    *api.github.com/repos/BeanYa/Lattix/releases/assets/1) cp "$FAKE_RELEASE/install-agent.sh" "$out" ;;
    *api.github.com/repos/BeanYa/Lattix/releases/assets/2) cp "$FAKE_RELEASE/checksums.txt" "$out" ;;
    *api.github.com/repos/BeanYa/Lattix/releases/assets/3) cp "$FAKE_RELEASE/lattix-agent-linux-ARCH.tar.gz" "$out" ;;
    *) exec /usr/bin/curl "$@" ;;
esac
CURL
sed -i.bak "s|ARCH|${arch}|" "$fallback/bin/curl"
rm -f "$fallback/bin/curl.bak"
chmod +x "$fallback/bin/curl"
mkdir -p "$work/raw/v9.8.7/scripts"
cp "$fallback/release/install-agent.sh" "$work/raw/v9.8.7/scripts/install-agent.sh"
PATH="$fallback/bin:$PATH" \
FAKE_API_JSON="$fallback/api.json" \
FAKE_RELEASE="$fallback/release" \
LATX_TEST_OUTPUT="$fallback/child.out" \
LATX_RAW_BASE="file://$work/raw" \
LATX_RELEASE_BASE="" \
    bash "$repo_root/install.sh" agent --version v9.8.7 \
        --panel https://panel.example.com --token bootstrap \
    >"$fallback/install.log" 2>&1
grep -Fq '直连 release 下载失败' "$fallback/install.log" \
    || { echo "release 504 fallback to the GitHub API endpoint is missing" >&2; exit 1; }
grep -Fq '（预取）' "$fallback/install.log" \
    || { echo "agent bundle staging is missing" >&2; exit 1; }
staged_base="$(sed -n 's/^RELEASE_BASE=file:\/\///p' "$fallback/child.out")"
[[ -n "$staged_base" ]] \
    || { echo "child installer did not receive a staged release base" >&2; exit 1; }
grep -Fq "lattix-agent-linux-${arch}.tar.gz" "$fallback/child.out" \
    && grep -Fq "checksums.txt" "$fallback/child.out" \
    || { echo "staged release base is missing release assets" >&2; exit 1; }
[[ ! -e "$staged_base" ]] \
    || { echo "staged release directory must be removed after install" >&2; exit 1; }
diff -u <(printf '%s\n' --version v9.8.7 --panel https://panel.example.com \
    --token bootstrap) <(grep '^ARG=' "$fallback/child.out" | sed 's/^ARG=//') \
    || { echo "staged child installer lost forwarded arguments" >&2; exit 1; }

# The recommended interactive command pipes the installer into bash, so its
# stdin is not a TTY. Verify the panel-only wizard reads from /dev/tty.
printf '1\n\n\n\n\n\n' | script -qec \
    "env PATH='$work/bin':\"\$PATH\" LATX_TEST_OUTPUT='$work/wizard.args' LATX_RAW_BASE='file://$work' LATX_RELEASE_BASE='file://$release' bash -c 'cat \"$repo_root/install.sh\" | bash'" \
    /dev/null >"$work/wizard.log"
diff -u <(printf '%s\n' --version v9.8.7 --mode docker) "$work/wizard.args"
if grep -Fq 'Agent' "$work/wizard.log"; then
    echo "interactive installer must not offer Agent installation" >&2
    exit 1
fi

printf '1\n0.0.0.0\n9090\noperator\nsecret-pass\n/srv/lattix\n' | script -qec \
    "env PATH='$work/bin':\"\$PATH\" LATX_TEST_OUTPUT='$work/wizard-custom.args' LATX_RAW_BASE='file://$work' LATX_RELEASE_BASE='file://$release' bash -c 'cat \"$repo_root/install.sh\" | bash'" \
    /dev/null >"$work/wizard-custom.log"
diff -u <(printf '%s\n' --version v9.8.7 --mode docker --bind 0.0.0.0 \
    --port 9090 --admin-user operator --admin-pass secret-pass \
    --config-dir /srv/lattix) "$work/wizard-custom.args"
grep -Fq '***********' "$work/wizard-custom.log" \
    || { echo "password prompt must display one mask per character" >&2; exit 1; }

printf '2\n192.0.2.10\n7070\noperator\nsecret-pass\n/srv/lattix-native\n' | script -qec \
    "env PATH='$work/bin':\"\$PATH\" LATX_TEST_OUTPUT='$work/wizard-native.args' LATX_RAW_BASE='file://$work' LATX_RELEASE_BASE='file://$release' bash -c 'cat \"$repo_root/install.sh\" | bash'" \
    /dev/null >/dev/null
diff -u <(printf '%s\n' --version v9.8.7 --mode native --bind 192.0.2.10 \
    --port 7070 --admin-user operator --admin-pass secret-pass \
    --config-dir /srv/lattix-native) "$work/wizard-native.args"

echo "e2e/install-entry: PASS"
