#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/v9.8.7/scripts"

for component in panel agent; do
    cat >"$work/v9.8.7/scripts/install-${component}.sh" <<'CHILD'
#!/usr/bin/env bash
printf '%s\n' "$@" >"${LATX_TEST_OUTPUT:?}"
CHILD
    chmod +x "$work/v9.8.7/scripts/install-${component}.sh"
done

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

echo "dev-e2e-install-entry: PASS"
