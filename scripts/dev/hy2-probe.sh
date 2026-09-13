#!/usr/bin/env bash
# hy2 数据面一次性验证（P4 Task 1，2026-09-14 实测定稿）：
#   Step 1) xray hy2 服务端（自签证书 + salamander + brutal，服务端 finalmask 声明 udpHop 段）
#           监听 127.0.0.1:14439 —— 复核服务端 udpHop 行为：ss 应只见 UDP 14439 单端口
#           （段内其余端口不监听 → 端口跳跃走 iptables DNAT 收敛，DNAT 路径成立）
#   Step 2) xray 客户端（socks → hysteria outbound + pinnedPeerCertSha256 pin，无 udpHop）
#           curl 经 socks 数据面断言 200（真 QUIC/UDP 承载）
#   Step 3) iptables DNAT 冒烟：客户端带 udpHop 段重测（需 root/CAP_NET_ADMIN；
#           非 root 明确输出 SKIP，不假绿）
#
# 模板定稿要点（与计划 Task 1 原始脚本的实测出入，已回填计划「关键已验证事实」）：
#   - streamSettings 必须显式 "network":"hysteria"（缺省 tcp → QUIC 跑在 TCP 承载上，
#     表现为 TCP LISTEN 单端口、UDP 跳跃/salamander 全部失效）
#   - 服务端用户列表键是 settings.clients（写 users 被静默忽略 → 空 validator → auth failed）
#   - tlsSettings 必须 alpn:["h3"]（否则服务端 CRYPTO_ERROR: no application protocol）
#   - 客户端 udpHop 首包即落段内随机端口（握手前）→ 无 DNAT 时客户端不得声明 udpHop
# 依赖：xray（XRAY_BIN 可覆盖）、openssl、curl、ss。Step 1/2 仅本机回环，不需要特权。
# 幂等：临时目录 mktemp、进程与 iptables 规则均由 EXIT trap 清理，可重复执行。
set -euo pipefail

XRAY_BIN="${XRAY_BIN:-/usr/local/bin/xray}"
LISTEN_PORT=14439
SOCKS_PORT=11080
HOP_START=20000
HOP_END=20031
HOP_PORTS="${HOP_START}-${HOP_END}"
HOP_PORTS_IPT="${HOP_START}:${HOP_END}"
HOP_CHAIN="LATTIX_UDPHOP"

WORK="$(mktemp -d)"
SRVPID=""; CLIPID=""; DNAT_INSTALLED=0

cleanup() {
  kill ${SRVPID:-} ${CLIPID:-} 2>/dev/null || true
  wait ${SRVPID:-} ${CLIPID:-} 2>/dev/null || true
  rm -rf "$WORK"
  if [[ "$DNAT_INSTALLED" == "1" ]] && command -v iptables >/dev/null 2>&1; then
    iptables -t nat -D OUTPUT -p udp --dport "$HOP_PORTS_IPT" -j "$HOP_CHAIN" 2>/dev/null || true
    iptables -t nat -D PREROUTING -p udp --dport "$HOP_PORTS_IPT" -j "$HOP_CHAIN" 2>/dev/null || true
    iptables -t nat -F "$HOP_CHAIN" 2>/dev/null || true
    iptables -t nat -X "$HOP_CHAIN" 2>/dev/null || true
  fi
}
trap cleanup EXIT

"$XRAY_BIN" tls cert -domain=www.example.com -name=www.example.com -file="$WORK/cert" >/dev/null
PIN="$(openssl x509 -in "$WORK/cert.crt" -outform DER | openssl dgst -sha256 -hex | awk '{print $2}')"

# 服务端：finalmask 声明 udpHop 段用于复核「服务端不绑定跳跃段」；
# 生产模板（Task 3）服务端不含 udpHop——段治理由 panel/agent 的 DNAT 负责。
cat >"$WORK/server.json" <<EOF
{"log":{"loglevel":"warning"},"inbounds":[{"tag":"hy2","listen":"127.0.0.1","port":${LISTEN_PORT},
 "protocol":"hysteria",
 "settings":{"version":2,"clients":[{"auth":"test-auth","level":0,"email":"u1"}]},
 "streamSettings":{"network":"hysteria","security":"tls",
  "tlsSettings":{"serverName":"www.example.com","alpn":["h3"],
   "certificates":[{"certificateFile":"$WORK/cert.crt","keyFile":"$WORK/cert.key"}]},
  "hysteriaSettings":{"version":2},
  "finalmask":{"udp":[{"type":"salamander","settings":{"password":"obfs-pw"}}],
   "quicParams":{"congestion":"brutal","brutalUp":"50 mbps","brutalDown":"100 mbps",
    "udpHop":{"ports":"${HOP_PORTS}","interval":"10-30"}}}}}],
"outbounds":[{"protocol":"freedom","tag":"direct"}]}
EOF

# $1 = with_hop（true=客户端声明 udpHop 段，须配合 DNAT；false=直发主端口）
write_client_config() {
  local with_hop="$1" hop_json=""
  if [[ "$with_hop" == "true" ]]; then
    hop_json=',"udpHop":{"ports":"'"${HOP_PORTS}"'","interval":"10-30"}'
  fi
  cat >"$WORK/client.json" <<EOF
{"log":{"loglevel":"warning"},
"inbounds":[{"tag":"socks","listen":"127.0.0.1","port":${SOCKS_PORT},"protocol":"socks","settings":{"udp":true}}],
"outbounds":[{"tag":"hy2","protocol":"hysteria",
 "settings":{"version":2,"address":"127.0.0.1","port":${LISTEN_PORT}},
 "streamSettings":{"network":"hysteria","security":"tls",
  "tlsSettings":{"serverName":"www.example.com","alpn":["h3"],"pinnedPeerCertSha256":"$PIN"},
  "hysteriaSettings":{"version":2,"auth":"test-auth"},
  "finalmask":{"udp":[{"type":"salamander","settings":{"password":"obfs-pw"}}],
   "quicParams":{"congestion":"brutal","brutalUp":"50 mbps","brutalDown":"100 mbps"${hop_json}}}}}]}
EOF
}

start_server() {
  "$XRAY_BIN" run -config "$WORK/server.json" & SRVPID=$!
  sleep 1
  kill -0 "$SRVPID" 2>/dev/null || { echo "FAIL: 服务端进程未存活"; exit 1; }
}

start_client() {
  "$XRAY_BIN" run -config "$WORK/client.json" & CLIPID=$!
  sleep 1
  kill -0 "$CLIPID" 2>/dev/null || { echo "FAIL: 客户端进程未存活"; exit 1; }
}

stop_client() {
  kill ${CLIPID:-} 2>/dev/null || true
  wait ${CLIPID:-} 2>/dev/null || true
  CLIPID=""
}

# 服务端实际监听的、落在跳跃段内的 UDP 端口列表（空 = 仅主端口，符合预期）
hop_bound_ports() {
  ss -ulnH 2>/dev/null | awk '{print $5}' | sed -n 's/.*:\([0-9][0-9]*\)$/\1/p' \
    | awk -v lo="$HOP_START" -v hi="$HOP_END" '$1>=lo && $1<=hi'
}

assert_data_plane() {
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:${SOCKS_PORT}" --max-time 10 https://example.com/)"
  echo "data plane http_code=$code"
  [[ "$code" == "200" ]]
}

echo "== Step 1: 服务端 udpHop 监听复核（QUIC/UDP 承载）=="
start_server
echo "-- 服务端全部 socket（期望：仅 UDP ${LISTEN_PORT}，无 TCP 监听、无段内端口）--"
ss -tuanp 2>/dev/null | grep "pid=${SRVPID}" || true

if ss -tlnH 2>/dev/null | awk '{print $4}' | grep -q ":${LISTEN_PORT}\$"; then
  echo "FAIL: 服务端出现 TCP 监听 ${LISTEN_PORT}——streamSettings.network 未生效（缺省 tcp 承载），检查模板"
  exit 1
fi
HOP_BOUND="$(hop_bound_ports || true)"
if [[ -n "$HOP_BOUND" ]]; then
  echo "FAIL: 服务端绑定了跳跃段内端口（${HOP_BOUND//$'\n'/ }）——与「udpHop 仅客户端实现」结论相反，"
  echo "      停止后续任务，把计划 Task 3/5/6 的 DNAT 方案替换为「xray 自绑段」方案。"
  exit 1
fi
echo "OK: 服务端仅监听 UDP ${LISTEN_PORT} 单端口（udpHop 仅客户端实现 → DNAT 路径成立）"

echo "== Step 2: xray↔xray hy2 数据面（客户端无 udpHop，直发主端口）=="
write_client_config false
start_client
assert_data_plane
echo "OK: xray↔xray hy2 数据面可用（QUIC/UDP + salamander + brutal + pin）"
stop_client

echo "== Step 3: iptables DNAT 冒烟（客户端 udpHop 段 ${HOP_PORTS} → REDIRECT ${LISTEN_PORT}）=="
if [[ "$(id -u)" != "0" ]] || ! command -v iptables >/dev/null 2>&1; then
  echo "SKIP: DNAT 冒烟需要 root + iptables（当前 uid=$(id -u)，$(command -v iptables >/dev/null 2>&1 && echo iptables 可用 || echo 无 iptables)；e2e 同款守卫，Task 9 复用）"
else
  # PREROUTING 为生产形态（外部客户端流量入向收敛）；OUTPUT 为本机回环探针流量所需
  # （本机发往 127.0.0.1 的包不过 PREROUTING）。两链均挂同一自定义链，清理时一并移除。
  iptables -t nat -N "$HOP_CHAIN" 2>/dev/null || true
  iptables -t nat -F "$HOP_CHAIN"
  iptables -t nat -A "$HOP_CHAIN" -p udp --dport "$HOP_PORTS_IPT" -j REDIRECT --to-ports "$LISTEN_PORT" \
    -m comment --comment "lattix:probe"
  iptables -t nat -C PREROUTING -p udp --dport "$HOP_PORTS_IPT" -j "$HOP_CHAIN" 2>/dev/null \
    || iptables -t nat -A PREROUTING -p udp --dport "$HOP_PORTS_IPT" -j "$HOP_CHAIN"
  iptables -t nat -C OUTPUT -p udp --dport "$HOP_PORTS_IPT" -j "$HOP_CHAIN" 2>/dev/null \
    || iptables -t nat -A OUTPUT -p udp --dport "$HOP_PORTS_IPT" -j "$HOP_CHAIN"
  DNAT_INSTALLED=1
  echo "-- DNAT 规则已下发，客户端带 udpHop 段重测（首包即落段内随机端口，靠 DNAT 收敛到 ${LISTEN_PORT}）--"
  write_client_config true
  start_client
  assert_data_plane
  stop_client
  echo "OK: DNAT 冒烟通过（段内流量 REDIRECT 到 ${LISTEN_PORT} 后数据面 200）"
fi

echo "DONE: hy2-probe 完成（Step 1/2 PASS；Step 3 见上方 PASS/SKIP）"
