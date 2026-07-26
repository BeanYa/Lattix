#!/usr/bin/env bash
# install-agent.sh 的 BBR 逻辑隔离测试：只使用 sysctl/modprobe/config 替身，
# 不读取或修改宿主机网络参数。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

STATE="$WORK/congestion-control"
AVAILABLE="$WORK/available"
SYSCTL="$WORK/sysctl"
MODPROBE="$WORK/modprobe"
CONFIG="$WORK/99-lattix-bbr.conf"

cat >"$SYSCTL" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
    "-n net.ipv4.tcp_congestion_control")
        cat "$LATX_TEST_BBR_STATE"
        ;;
    "-n net.ipv4.tcp_available_congestion_control")
        cat "$LATX_TEST_BBR_AVAILABLE"
        ;;
    "-w net.core.default_qdisc=fq")
        # 模拟容器虚拟网卡拒绝 fq；BBR 仍应视为成功。
        echo "permission denied" >&2
        exit 1
        ;;
    "-w net.ipv4.tcp_congestion_control=bbr")
        if [[ "${LATX_TEST_BBR_DENY:-0}" == "1" ]]; then
            printf 'permission denied\ninside container\n' >&2
            exit 1
        fi
        printf 'bbr\n' >"$LATX_TEST_BBR_STATE"
        echo "net.ipv4.tcp_congestion_control = bbr"
        ;;
    *)
        echo "unexpected sysctl arguments: $*" >&2
        exit 2
        ;;
esac
EOF

cat >"$MODPROBE" <<'EOF'
#!/usr/bin/env bash
echo "Operation not permitted" >&2
exit 1
EOF
chmod 0755 "$SYSCTL" "$MODPROBE"

run_bbr() {
    LATX_BBR_TEST=1 LATX_BBR_TEST_ONLY=1 \
    LATX_BBR_SYSCTL="$SYSCTL" LATX_BBR_MODPROBE="$MODPROBE" \
    LATX_BBR_CONFIG="$CONFIG" LATX_TEST_BBR_STATE="$STATE" \
    LATX_TEST_BBR_AVAILABLE="$AVAILABLE" \
        bash "$ROOT/scripts/install-agent.sh" 2>&1
}

echo ">> 用例 1: 已启用 BBR 时不改现有配置"
printf 'bbr\n' >"$STATE"
printf 'reno cubic bbr\n' >"$AVAILABLE"
printf 'keep-existing\n' >"$CONFIG"
OUT="$(run_bbr)"
[[ -z "$OUT" ]] || { echo "FAIL: 已启用时产生输出: $OUT"; exit 1; }
[[ "$(cat "$CONFIG")" == "keep-existing" ]] \
    || { echo "FAIL: 已启用时覆盖了现有配置"; exit 1; }

echo ">> 用例 2: fq 失败不妨碍启用并持久化 BBR"
printf 'cubic\n' >"$STATE"
rm -f "$CONFIG"
OUT="$(run_bbr)"
[[ -z "$OUT" ]] || { echo "FAIL: BBR 成功时产生输出: $OUT"; exit 1; }
[[ "$(cat "$STATE")" == "bbr" ]] || { echo "FAIL: BBR 未即时生效"; exit 1; }
diff -u <(printf '%s\n' \
    'net.core.default_qdisc=fq' \
    'net.ipv4.tcp_congestion_control=bbr') "$CONFIG"

echo ">> 用例 3: 内核/容器不提供 BBR 时仅输出一行原因 WARNING"
printf 'cubic\n' >"$STATE"
printf 'reno cubic\n' >"$AVAILABLE"
OUT="$(run_bbr)"
[[ "$(printf '%s\n' "$OUT" | wc -l)" -eq 1 ]] \
    || { echo "FAIL: WARNING 不是一行: $OUT"; exit 1; }
[[ "$OUT" == "WARNING: TCP BBR 未完整启用：内核未提供 BBR：Operation not permitted" ]] \
    || { echo "FAIL: WARNING 原因不符: $OUT"; exit 1; }

echo ">> 用例 4: sysctl 拒绝时压缩多行错误且退出成功"
printf 'cubic\n' >"$STATE"
printf 'reno cubic bbr\n' >"$AVAILABLE"
OUT="$(LATX_TEST_BBR_DENY=1 run_bbr)"
[[ "$(printf '%s\n' "$OUT" | wc -l)" -eq 1 ]] \
    || { echo "FAIL: sysctl WARNING 不是一行: $OUT"; exit 1; }
[[ "$OUT" == "WARNING: TCP BBR 未完整启用：sysctl 设置 BBR 被拒绝：permission denied; inside container" ]] \
    || { echo "FAIL: sysctl WARNING 原因不符: $OUT"; exit 1; }

echo "dev-test-install-agent-bbr: PASS"
