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
        --panel https://panel.example.com --token bootstrap
diff -u <(printf '%s\n' --version v9.8.7 --panel https://panel.example.com \
    --token bootstrap) "$work/agent.args"

# The recommended interactive command pipes the installer into bash, so its
# stdin is not a TTY. Verify the panel-only wizard reads from /dev/tty.
printf '1\n\n\n\n\n\n' | script -qec \
    "env PATH='$work/bin':\"\$PATH\" LATX_TEST_OUTPUT='$work/wizard.args' LATX_RAW_BASE='file://$work' bash -c 'cat \"$repo_root/install.sh\" | bash'" \
    /dev/null >"$work/wizard.log"
diff -u <(printf '%s\n' --version v9.8.7 --mode docker) "$work/wizard.args"
if grep -Fq 'Agent' "$work/wizard.log"; then
    echo "interactive installer must not offer Agent installation" >&2
    exit 1
fi

printf '1\n0.0.0.0\n9090\noperator\nsecret-pass\n/srv/lattix\n' | script -qec \
    "env PATH='$work/bin':\"\$PATH\" LATX_TEST_OUTPUT='$work/wizard-custom.args' LATX_RAW_BASE='file://$work' bash -c 'cat \"$repo_root/install.sh\" | bash'" \
    /dev/null >"$work/wizard-custom.log"
diff -u <(printf '%s\n' --version v9.8.7 --mode docker --bind 0.0.0.0 \
    --port 9090 --admin-user operator --admin-pass secret-pass \
    --config-dir /srv/lattix) "$work/wizard-custom.args"
grep -Fq '***********' "$work/wizard-custom.log" \
    || { echo "password prompt must display one mask per character" >&2; exit 1; }

printf '2\n192.0.2.10\n7070\noperator\nsecret-pass\n/srv/lattix-native\n' | script -qec \
    "env PATH='$work/bin':\"\$PATH\" LATX_TEST_OUTPUT='$work/wizard-native.args' LATX_RAW_BASE='file://$work' bash -c 'cat \"$repo_root/install.sh\" | bash'" \
    /dev/null >/dev/null
diff -u <(printf '%s\n' --version v9.8.7 --mode native --bind 192.0.2.10 \
    --port 7070 --admin-user operator --admin-pass secret-pass \
    --config-dir /srv/lattix-native) "$work/wizard-native.args"

echo "e2e/install-entry: PASS"
