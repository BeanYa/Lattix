#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/v9.8.7/scripts"

if grep -Eq '\$\{[^}]+:-\{\{' \
    "$repo_root/scripts/install-agent.sh" "$repo_root/scripts/install-panel.sh"; then
    echo "installer placeholders must not be embedded in parameter defaults" >&2
    exit 1
fi

for component in panel agent; do
    cat >"$work/v9.8.7/scripts/install-${component}.sh" <<'CHILD'
#!/usr/bin/env bash
printf '%s\n' "$@" >"${LATX_TEST_OUTPUT:?}"
CHILD
    chmod +x "$work/v9.8.7/scripts/install-${component}.sh"
done

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
    bash "$repo_root/install.sh" panel --version v9.8.7 --mode docker --port 9090
diff -u <(printf '%s\n' --version v9.8.7 --mode docker --port 9090) "$work/panel.args"

LATX_TEST_OUTPUT="$work/agent.args" \
LATX_RAW_BASE="file://$work" \
    bash "$repo_root/install.sh" agent --version v9.8.7 \
        --panel https://panel.example.com --token bootstrap --xray-version latest
diff -u <(printf '%s\n' --version v9.8.7 --panel https://panel.example.com \
    --token bootstrap --xray-version latest) "$work/agent.args"

# The recommended interactive command pipes the installer into bash, so its
# stdin is not a TTY. Verify the wizard reads its choices from /dev/tty.
printf '1\n1\n' | script -qec \
    "env PATH='$work/bin':\"\$PATH\" LATX_TEST_OUTPUT='$work/wizard.args' LATX_RAW_BASE='file://$work' bash -c 'cat \"$repo_root/install.sh\" | bash'" \
    /dev/null >/dev/null
diff -u <(printf '%s\n' --version v9.8.7 --mode docker) "$work/wizard.args"

echo "dev-e2e-install-entry: PASS"
