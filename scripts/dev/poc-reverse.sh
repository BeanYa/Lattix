#!/usr/bin/env bash
# poc-reverse.sh — §21 链式中转 Phase 0 PoC：xray reverse bridge/portal 验证。
#
# 拓扑（全部在本机回环模拟）：
#   client(socks:11080) → [B 机 portal] entry dokodemo:11002 → reverse 隧道(Reality :11001)
#     → [C 机 bridge] freedom → 业务 inbound(VLESS+Reality 127.0.0.1:21001) → Internet
#
# 验证项：
#   1. 端到端 Reality 经纯 TCP 透传链（客户端握手直达出口业务 inbound）；
#   2. portal 重启后 bridge 自动重拨、链路自愈；
#   3. 隧道口（Reality）抗主动探测：任意 SNI 均分流到 dest 真实证书。
#
# 已知要点（PoC 实测）：
#   - Reality dest 不可用（如 www.microsoft.com）会导致**合法客户端握手也失败**
#     （"REALITY: processed invalid connection"），dest 必须用稳定目标（dl.google.com）。
#   - 反向通道注册存在启动竞态：bridge 首次拨号可能失败，xray 自动重试数秒后注册成功。
#
# 依赖：xray、curl、openssl。退出码 0 = go，非 0 = no-go。
set -euo pipefail

XRAY_BIN="${XRAY_BIN:-xray}"
DEST_HOST="${POC_DEST:-dl.google.com}"
TUNNEL_PORT=11001
ENTRY_PORT=11002
BIZ_PORT=21001
SOCKS_PORT=11080
PROBE_URL="https://www.cloudflare.com/cdn-cgi/trace"

TMP="$(mktemp -d /tmp/lattix-poc-reverse.XXXXXX)"
PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null || true; done
  rm -rf "$TMP"
}
trap cleanup EXIT

fail() { echo "[FAIL] $*" >&2; exit 1; }
ok()   { echo "[ OK ] $*"; }

command -v "$XRAY_BIN" >/dev/null || fail "xray 不在 PATH（XRAY_BIN 可覆盖）"

# --- 密钥与 UUID ---
TUNNEL_PRIV="$("$XRAY_BIN" x25519 | awk '/PrivateKey/{print $NF}')"
TUNNEL_PUB="$("$XRAY_BIN" x25519 -i "$TUNNEL_PRIV" | awk '/PublicKey/{print $NF}')"
BIZ_PRIV="$("$XRAY_BIN" x25519 | awk '/PrivateKey/{print $NF}')"
BIZ_PUB="$("$XRAY_BIN" x25519 -i "$BIZ_PRIV" | awk '/PublicKey/{print $NF}')"
TUNNEL_UUID="$("$XRAY_BIN" uuid)"
BIZ_UUID="$("$XRAY_BIN" uuid)"
[ -n "$TUNNEL_PUB" ] && [ -n "$BIZ_PUB" ] || fail "x25519 密钥生成失败"

# --- portal（公网 B 机） ---
cat > "$TMP/portal.json" <<EOF
{
  "log": { "loglevel": "warning" },
  "reverse": { "portals": [{ "tag": "portal", "domain": "tunnel.lattix.test" }] },
  "inbounds": [
    {
      "tag": "interconn", "listen": "127.0.0.1", "port": $TUNNEL_PORT, "protocol": "vless",
      "settings": { "clients": [{ "id": "$TUNNEL_UUID" }], "decryption": "none" },
      "streamSettings": { "network": "tcp", "security": "reality", "realitySettings": {
        "show": false, "dest": "$DEST_HOST:443", "xver": 0,
        "serverNames": ["$DEST_HOST"], "privateKey": "$TUNNEL_PRIV", "shortIds": ["0123abcd"] } }
    },
    {
      "tag": "entry", "listen": "127.0.0.1", "port": $ENTRY_PORT, "protocol": "dokodemo-door",
      "settings": { "address": "127.0.0.1", "port": $BIZ_PORT, "network": "tcp" }
    }
  ],
  "routing": { "rules": [
    { "type": "field", "inboundTag": ["entry"], "outboundTag": "portal" },
    { "type": "field", "inboundTag": ["interconn"], "outboundTag": "portal" } ] }
}
EOF

# --- bridge（NAT C 机） ---
cat > "$TMP/bridge.json" <<EOF
{
  "log": { "loglevel": "warning" },
  "reverse": { "bridges": [{ "tag": "bridge", "domain": "tunnel.lattix.test" }] },
  "inbounds": [
    {
      "tag": "biz", "listen": "127.0.0.1", "port": $BIZ_PORT, "protocol": "vless",
      "settings": { "clients": [{ "id": "$BIZ_UUID", "flow": "xtls-rprx-vision" }], "decryption": "none" },
      "streamSettings": { "network": "tcp", "security": "reality", "realitySettings": {
        "show": false, "dest": "$DEST_HOST:443", "xver": 0,
        "serverNames": ["$DEST_HOST"], "privateKey": "$BIZ_PRIV", "shortIds": ["aabbccdd"] } }
    }
  ],
  "outbounds": [
    { "tag": "out", "protocol": "freedom" },
    {
      "tag": "interconn", "protocol": "vless",
      "settings": { "vnext": [{ "address": "127.0.0.1", "port": $TUNNEL_PORT,
        "users": [{ "id": "$TUNNEL_UUID", "encryption": "none" }] }] },
      "streamSettings": { "network": "tcp", "security": "reality", "realitySettings": {
        "serverName": "$DEST_HOST", "publicKey": "$TUNNEL_PUB",
        "shortId": "0123abcd", "fingerprint": "chrome" } }
    }
  ],
  "routing": { "rules": [
    { "type": "field", "domain": ["full:tunnel.lattix.test"], "outboundTag": "interconn" },
    { "type": "field", "inboundTag": ["bridge"], "outboundTag": "out" },
    { "type": "field", "inboundTag": ["biz"], "outboundTag": "out" } ] }
}
EOF

# --- client（客户端，地址端口指向 B 机 entry，密钥用 C 机业务 inbound 的） ---
cat > "$TMP/client.json" <<EOF
{
  "log": { "loglevel": "warning" },
  "inbounds": [{ "tag": "socks", "listen": "127.0.0.1", "port": $SOCKS_PORT,
    "protocol": "socks", "settings": { "auth": "noauth" } }],
  "outbounds": [{
    "tag": "proxy", "protocol": "vless",
    "settings": { "vnext": [{ "address": "127.0.0.1", "port": $ENTRY_PORT,
      "users": [{ "id": "$BIZ_UUID", "encryption": "none", "flow": "xtls-rprx-vision" }] }] },
    "streamSettings": { "network": "tcp", "security": "reality", "realitySettings": {
      "serverName": "$DEST_HOST", "publicKey": "$BIZ_PUB",
      "shortId": "aabbccdd", "fingerprint": "chrome" } } }]
}
EOF

for f in portal bridge client; do
  "$XRAY_BIN" run -test -config "$TMP/$f.json" >/dev/null || fail "$f.json 配置校验失败"
done
ok "三份配置 xray -test 通过"

start_xray() { "$XRAY_BIN" run -config "$TMP/$1.json" > "$TMP/$1.log" 2>&1 & PIDS+=($!); }

probe() { # probe <max-seconds>：轮询直到链路 200
  local deadline=$((SECONDS + $1))
  while [ $SECONDS -lt $deadline ]; do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:$SOCKS_PORT" --max-time 8 "$PROBE_URL" || true)"
    [ "$code" = "200" ] && return 0
    sleep 2
  done
  return 1
}

start_xray portal; start_xray bridge; start_xray client

# 验证 1：端到端链路（允许反向通道注册竞态，宽限 30s）
probe 30 || { tail -n 5 "$TMP/portal.log" "$TMP/bridge.log" >&2; fail "链路未通（30s 内未恢复）"; }
ok "端到端 Reality 链路（client→entry→reverse 隧道→出口）"

# 验证 2：portal 重启自愈
PORTAL_PID="${PIDS[0]}"
kill "$PORTAL_PID" 2>/dev/null || true
sleep 1
code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:$SOCKS_PORT" --max-time 6 "$PROBE_URL" || true)"
[ "$code" != "200" ] || fail "portal 已停但链路仍通，验证环境异常"
start_xray portal
probe 45 || fail "portal 重启后链路未自愈（bridge 未自动重拨）"
ok "portal 重启后链路自愈（bridge 自动重拨反向通道）"

# 验证 3：隧道口抗主动探测（任意 SNI 均分流到 dest 真实证书）
cert="$(echo | timeout 8 openssl s_client -connect "127.0.0.1:$TUNNEL_PORT" -servername evil.example.com 2>/dev/null | openssl x509 -noout -subject 2>/dev/null || true)"
echo "$cert" | grep -qi "google" || fail "隧道口未分流到 dest 证书：$cert"
ok "隧道口 Reality 分流正常（探测者只见 dest 真实证书）"

echo
echo "PoC 结论：GO —— xray reverse bridge/portal + 隧道 Reality + 端到端 Reality 透传可行。"
