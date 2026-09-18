#!/usr/bin/env bash
# 全协议向导端到端验收（设计文档 §15）：
#   经面板 RPC API 创建 7 种协议节点 → 全部 active + realized_config 正确 →
#   订阅 YAML 各协议字段正确（dokodemo 不进订阅）→ 创建用户触发多协议 add_user 扇出。
# 管理 API 均为 RPC 信封：写操作需 Idempotency-Key 与 X-CSRF-Token。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）、可访问 dest 预检域名。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

# VLESS Encryption 需带 vlessenc 子命令的 xray（§15）；缺失时跳过 vless+httpupgrade 用例。
HAS_VLESSENC=true
"$XRAY_BIN" vlessenc >/dev/null 2>&1 || HAS_VLESSENC=false

ADDR="127.0.0.1:18099"
API="127.0.0.1:14201"
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"
CSRF=""

cleanup() {
    kill ${BPID:-} ${APID:-} ${TLSXPID:-} ${HY2XPID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    pkill -f "xray run -config $WORK/client-tls.json" 2>/dev/null || true
    pkill -f "xray run -config $WORK/client-hy2.json" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build"
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)

db() { python3 - "$WORK/lattix.db" "$1" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
cur = con.execute(sys.argv[2])
con.commit()
for row in cur: print("|".join("" if c is None else str(c) for c in row))
PY
}

# rpc_raw <method> <path> [body]
rpc_raw() {
    local method="$1" path="$2" body="${3:-}"
    local args=(-sS -b "$JAR" -c "$JAR" -X "$method" -H "Origin: http://$ADDR")
    if [[ "$method" == "POST" ]]; then
        [[ -n "$body" ]] || body='{}'
        args+=(-H 'Content-Type: application/json' -H "Idempotency-Key: $(openssl rand -hex 16)" -d "$body")
        [[ -z "$CSRF" ]] || args+=(-H "X-CSRF-Token: $CSRF")
    fi
    curl "${args[@]}" "http://$ADDR$path"
}

# rpc_data：解包 RPC 信封输出 data。
rpc_data() {
    rpc_raw "$@" | python3 -c '
import json,sys
value=json.load(sys.stdin)
if value["code"] not in ("OK","ACCEPTED"):
    raise SystemExit(f"RPC failed: {value}")
print(json.dumps(value["data"], separators=(",",":")))
'
}

# rpc_expect_fail <method> <path> <body>：断言 RPC 业务失败（code 非 OK/ACCEPTED）。
rpc_expect_fail() {
    local out
    out="$(rpc_raw "$@")"
    python3 -c 'import json,sys; v=json.loads(sys.argv[1]); assert v["code"] not in ("OK","ACCEPTED"), v' "$out" \
        || { echo "FAIL: 预期 RPC 失败但成功: $out"; exit 1; }
}

port_open() { python3 -c "import socket,sys; s=socket.socket(); sys.exit(0 if s.connect_ex(('127.0.0.1', int(sys.argv[1])))==0 else 1)" "$1"; }

# wait_node <id>：轮询节点进入 active/failed，输出 "status|realized|error"。
wait_node() {
    for _ in $(seq 1 40); do
        s="$(db "SELECT status FROM nodes WHERE id=$1")"
        case "$s" in
            active|failed)
                db "SELECT status || '|' || COALESCE(realized_config,'') || '|' || COALESCE(error,'') FROM nodes WHERE id=$1"
                return 0 ;;
        esac
        sleep 0.5
    done
    echo "TIMEOUT||"; return 1
}

# create_node <json-body>：创建节点并等待 active，输出 realized_config。
create_node() {
    local res id out
    res="$(rpc_data POST /api/node/create "$1")"
    id="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$res" 2>/dev/null)" \
        || { echo "FAIL: 创建节点响应异常: $res"; exit 1; }
    out="$(wait_node "$id")"
    [[ "$out" == active\|* ]] || { echo "FAIL: 节点 $id 未 active: $out"; tail -5 "$WORK/agent.log"; exit 1; }
    echo "$out" | cut -d'|' -f2
}

check_port() { # <realized-json>
    local p
    p="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["port"])' "$1")"
    port_open "$p" || { echo "FAIL: 端口 $p 未监听"; exit 1; }
    echo "   端口 $p OK"
}

echo ">> start backend"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done

echo ">> 登录 + 建服务器 + 拉起 agent"
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"lattix-admin"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }
SRV="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"proto01","address":"127.0.0.1"}')"
BOOTSTRAP="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["bootstrap_token"])' "$SRV")"
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -settings "$WORK/agent.settings.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2
grep -q "authenticated as server" "$WORK/agent.log" || { echo "FAIL: agent 未认证"; cat "$WORK/agent.log"; exit 1; }

echo ">> create user u1"
USER_RES="$(rpc_data POST /api/user/create '{"name":"u1"}')"
SUB_TOKEN="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$USER_RES")"

echo ">> vless tcp vision"
R="$(create_node '{"server_id":1,"protocol":"vless"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="tcp" and rc["flow"]=="xtls-rprx-vision" and rc["public_key"], rc' "$R" && check_port "$R"

echo ">> vless grpc（无 flow）"
R="$(create_node '{"server_id":1,"protocol":"vless","network":"grpc","service_name":"gsvc","flow":"none"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="grpc" and rc["service_name"]=="gsvc" and rc.get("flow","")=="", rc' "$R" && check_port "$R"

echo ">> vless xhttp"
R="$(create_node '{"server_id":1,"protocol":"vless","network":"xhttp","path":"/xp","mode":"packet-up","flow":"none"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="xhttp" and rc["path"]=="/xp" and rc["mode"]=="packet-up", rc' "$R" && check_port "$R"

echo ">> vmess tcp"
R="$(create_node '{"server_id":1,"protocol":"vmess"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="tcp" and rc["public_key"], rc' "$R" && check_port "$R"

echo ">> trojan grpc"
R="$(create_node '{"server_id":1,"protocol":"trojan","network":"grpc","service_name":"tsvc"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="grpc" and rc["service_name"]=="tsvc", rc' "$R" && check_port "$R"

echo ">> shadowsocks 2022-blake3-aes-128-gcm"
R="$(create_node '{"server_id":1,"protocol":"shadowsocks","method":"2022-blake3-aes-128-gcm"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["method"]=="2022-blake3-aes-128-gcm", rc' "$R" && check_port "$R"

echo ">> shadowsocks aes-256-gcm（旧式）"
R="$(create_node '{"server_id":1,"protocol":"shadowsocks","method":"aes-256-gcm"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["method"]=="aes-256-gcm", rc' "$R" && check_port "$R"

echo ">> socks"
R="$(create_node '{"server_id":1,"protocol":"socks"}')" && check_port "$R"

echo ">> http"
R="$(create_node '{"server_id":1,"protocol":"http"}')" && check_port "$R"

echo ">> dokodemo-door"
R="$(create_node '{"server_id":1,"protocol":"dokodemo-door","target_address":"127.0.0.1","target_port":18099}')" && check_port "$R"

echo ">> vmess cipher=chacha20-poly1305"
R="$(create_node '{"server_id":1,"protocol":"vmess","cipher":"chacha20-poly1305"}')"
check_port "$R"

echo ">> vmess ws（security=none 明文传输）"
R="$(create_node '{"server_id":1,"protocol":"vmess","network":"ws","path":"/wsp","host":"cdn.example.com"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="ws" and rc["path"]=="/wsp" and rc["host"]=="cdn.example.com" and rc.get("public_key","")=="" and rc.get("security")=="none", rc' "$R" && check_port "$R"

echo ">> vless httpupgrade + VLESS Encryption（security=none 须带 Encryption，§2 脚注 1）"
if [[ "$HAS_VLESSENC" == "true" ]]; then
    R="$(create_node '{"server_id":1,"protocol":"vless","network":"httpupgrade","path":"/hu","encryption":"mlkem768"}')"
    python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="httpupgrade" and rc["path"]=="/hu" and rc.get("security")=="none" and rc.get("encryption","").startswith("mlkem768x25519plus."), rc' "$R" && check_port "$R"
else
    echo "SKIP: xray 缺 vlessenc 子命令，跳过 vless+httpupgrade 用例"
fi

echo ">> vless tls 自签（tcp；伪装域名留空=预设池随机）"
R="$(create_node '{"server_id":1,"protocol":"vless","security":"tls","flow":"none"}')"
python3 -c 'import json,sys,re; rc=json.loads(sys.argv[1]); assert rc.get("security")=="tls" and rc.get("sni") and re.fullmatch(r"[0-9a-f]{64}", rc.get("cert_sha256","")) and rc.get("public_key","")=="" , rc' "$R" && check_port "$R"

echo ">> trojan ws + tls 自签（自定义伪装域名；P3 起合法）"
R="$(create_node '{"server_id":1,"protocol":"trojan","network":"ws","path":"/tw","security":"tls","tls_domain":"cdn.example.com"}')"
python3 -c 'import json,sys; rc=json.loads(sys.argv[1]); assert rc["network"]=="ws" and rc["path"]=="/tw" and rc.get("security")=="tls" and rc.get("sni")=="cdn.example.com" and len(rc.get("cert_sha256",""))==64, rc' "$R" && check_port "$R"
TLS_NODE_ID="$(db "SELECT id FROM nodes WHERE protocol='vless' AND json_extract(realized_config,'$.security')='tls' ORDER BY id LIMIT 1")"
TLS_SNI="$(db "SELECT json_extract(realized_config,'$.sni') FROM nodes WHERE id=$TLS_NODE_ID")"
TLS_PIN="$(db "SELECT json_extract(realized_config,'$.cert_sha256') FROM nodes WHERE id=$TLS_NODE_ID")"

echo ">> 矩阵外组合 400（reality×ws / trojan×ws 推导 none / vless+ws 无 Encryption / ss 带传输层 / acme 无域名 / cert_mode 未知）"
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vmess","network":"ws","security":"reality"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"trojan","network":"ws"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vless","network":"ws"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"shadowsocks","network":"ws"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vmess","security":"tls","cert_mode":"acme"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"vmess","security":"tls","cert_mode":"bogus"}'
echo "   矩阵外组合均被 400 拦截 OK（trojan×ws 仍需显式 tls；acme 在无域名服务器上前置 400）"

echo ">> hysteria2（port_hop=off：自签默认证书 + 显式 obfs/brutal 透传）"
# port_hop 显式 off：非 root 环境 DNAT 不可用，带段监听会按指向性错误 failed（Task 5）；
# 段语义（自动分配/DNAT/重叠 400）见下方 root 守卫块。
R="$(create_node '{"server_id":1,"protocol":"hysteria","port_hop":"off","obfs_password":"e2e-obfs","up_mbps":50,"down_mbps":100}')"
python3 -c 'import json,sys,re; rc=json.loads(sys.argv[1]); assert rc.get("security")=="tls" and rc.get("sni") and re.fullmatch(r"[0-9a-f]{64}", rc.get("cert_sha256","")) and rc.get("obfs_password")=="e2e-obfs" and rc.get("up_mbps")==50 and rc.get("down_mbps")==100 and not rc.get("port_hop"), rc' "$R"
HY2_NODE_ID="$(db "SELECT id FROM nodes WHERE protocol='hysteria' ORDER BY id LIMIT 1")"
echo "   hy2 realized 含 sni/cert_sha256/obfs/brutal 且 port_hop 为空 OK（UDP 监听，跳过 TCP 探活）"

echo ">> 端口冲突前置校验"
CLASH_PORT=23456
R="$(create_node "{\"server_id\":1,\"protocol\":\"trojan\",\"port\":$CLASH_PORT}")"
check_port "$R"
rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"vless\",\"port\":$CLASH_PORT}"
rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"trojan\",\"port\":$CLASH_PORT}"
rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"shadowsocks\",\"port\":$CLASH_PORT}"
echo "   同层同端口（异协议/同协议/跨层 ss）均被 400 拦截 OK"

echo ">> hy2 端口段治理：udp/tcp 同号共存、同层同号冲突、非法段 400"
R="$(create_node "{\"server_id\":1,\"protocol\":\"hysteria\",\"port\":$CLASH_PORT,\"port_hop\":\"off\"}")"
echo "   hy2(udp) 与 trojan(tcp) 同号 $CLASH_PORT 共存 201 OK"
HY2_CLASH_PORT=24567
R="$(create_node "{\"server_id\":1,\"protocol\":\"hysteria\",\"port\":$HY2_CLASH_PORT,\"port_hop\":\"off\"}")"
rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"hysteria\",\"port\":$HY2_CLASH_PORT,\"port_hop\":\"off\"}"
echo "   第二个 hy2 同号 $HY2_CLASH_PORT 被 400 拦截 OK"
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"hysteria","port_hop":"bad-range"}'
rpc_expect_fail POST /api/node/create '{"server_id":1,"protocol":"hysteria","port_hop":"30500-30505"}'
echo "   非法段格式 / 段长 <8 被 400 拦截 OK"

echo ">> hy2 端口跳跃（root + iptables：自动段分配 + DNAT 落地 + 段重叠 400 + 删除清理）"
if [[ "$(id -u)" == "0" ]] && command -v iptables >/dev/null; then
    R="$(create_node '{"server_id":1,"protocol":"hysteria"}')"
    HOP_RANGE="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1]).get("port_hop",""))' "$R")"
    [[ "$HOP_RANGE" =~ ^[0-9]+-[0-9]+$ ]] || { echo "FAIL: 自动跳跃段未分配: $R"; exit 1; }
    HOP_NID="$(db "SELECT id FROM nodes WHERE protocol='hysteria' AND json_extract(realized_config,'$.port_hop')='$HOP_RANGE'")"
    iptables -t nat -S 2>/dev/null | grep -q "lattix:node_$HOP_NID" \
        && echo "OK: agent 已下发 udpHop DNAT（段 $HOP_RANGE，comment lattix:node_$HOP_NID）" \
        || { echo "FAIL: 未见 DNAT 规则"; iptables -t nat -S; exit 1; }
    rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"hysteria\",\"port\":39000,\"port_hop\":\"$(( ${HOP_RANGE%-*} + 5 ))-$(( ${HOP_RANGE%-*} + 15 ))\"}"
    echo "   显式段与自动段 $HOP_RANGE 重叠被 400 拦截 OK"
    rpc_data POST /api/node/delete "{\"node_id\":$HOP_NID}" >/dev/null
    for _ in $(seq 1 15); do
        iptables -t nat -S 2>/dev/null | grep -q "lattix:node_$HOP_NID" || break
        sleep 1
    done
    ! iptables -t nat -S 2>/dev/null | grep -q "lattix:node_$HOP_NID" \
        && echo "OK: 删除节点后 DNAT 规则已清除" \
        || { echo "FAIL: DNAT 规则残留"; iptables -t nat -S; exit 1; }
else
    echo "SKIP: 非 root 或无 iptables——自动段分配/DNAT/段重叠由 Task 3/5 单测覆盖"
fi

echo ">> 分配全部非 dokodemo 节点给 u1（§16 默认全关，需显式分配）"
NODE_IDS="$(db "SELECT group_concat(id) FROM nodes WHERE protocol != 'dokodemo-door'")"
rpc_data POST /api/user/set-nodes "{\"user_id\":1,\"node_ids\":[$NODE_IDS]}" >/dev/null
sleep 3

echo ">> 新建用户 u2 并分配 socks/http 节点 → 多协议 add_user 扇出"
U2="$(rpc_data POST /api/user/create '{"name":"u2"}')"
UUID2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$U2")"
U2_ID="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$U2")"
NODE_IDS2="$(db "SELECT group_concat(id) FROM nodes WHERE protocol IN ('socks','http')")"
rpc_data POST /api/user/set-nodes "{\"user_id\":$U2_ID,\"node_ids\":[$NODE_IDS2]}" >/dev/null
sleep 3
U2_COUNT="$(grep -c "$UUID2" "$XRAY_CONFIG" || true)"
[[ "$U2_COUNT" -ge 2 ]] || { echo "FAIL: socks/http accounts 未见 u2（出现 $U2_COUNT 次）"; tail -5 "$WORK/agent.log"; exit 1; }
echo "   socks/http accounts 含新用户 OK"

echo ">> 订阅校验"
SUB="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=clash")"
check() { grep -q "$1" <<<"$SUB" || { echo "FAIL: 订阅缺少 $1"; echo "$SUB"; exit 1; }; }
check "type: vless"
check "type: vmess"
check "type: trojan"
check "type: ss"
check "type: hysteria2"
check "type: socks5"
check "type: http"
check "reality-opts:"
check "grpc-opts:"
check "xhttp-opts:"
check "cipher: 2022-blake3-aes-128-gcm"
check "cipher: aes-256-gcm"
check "cipher: auto"
check "cipher: chacha20-poly1305"
check "ws-opts:"
if [[ "$HAS_VLESSENC" == "true" ]]; then
    check "v2ray-http-upgrade: true"
fi
LINKS_OUT="$(curl -s "http://$ADDR/sub/$SUB_TOKEN?format=links" | base64 -d)"
grep -q "security=tls" <<<"$LINKS_OUT" || { echo "FAIL: links 缺 security=tls"; echo "$LINKS_OUT"; exit 1; }
grep -q "allowInsecure=1" <<<"$LINKS_OUT" || { echo "FAIL: links 缺自签 allowInsecure=1"; echo "$LINKS_OUT"; exit 1; }
grep -q "^hysteria2://" <<<"$LINKS_OUT" || { echo "FAIL: links 缺 hysteria2://"; echo "$LINKS_OUT"; exit 1; }
# vmess:// 为 base64 JSON 整体编码，ws 传输需解码后断言 net=ws（vless/trojan 才是查询串 type=）。
python3 - "$LINKS_OUT" <<'PY'
import base64, json, sys
links = sys.argv[1].splitlines()
vmess = []
for l in links:
    if l.startswith("vmess://"):
        s = l[len("vmess://"):]
        vmess.append(json.loads(base64.b64decode(s + "=" * (-len(s) % 4))))
assert any(v.get("net") == "ws" for v in vmess), links
# security=none 的 vmess 不携带空值 sni/fp/pbk/sid 键（清理 #5）
plain = [v for v in vmess if v.get("tls") in ("", None)]
assert all(not v.get("sni") and "pbk" not in v for v in plain), plain
PY
# mihomo pin 精确断言（64 位 hex fingerprint 与 realized 一致；勿用 grep "fingerprint: "——
# reality 节点的 client-fingerprint: chrome 行含该子串，grep 恒真）
python3 - "$SUB" "$TLS_PIN" <<'PY'
import sys, yaml
doc = yaml.safe_load(sys.argv[1])
pin = sys.argv[2]
pins = [p.get("fingerprint") for p in doc["proxies"]]
assert pin in pins, (pin, pins)
PY
if [[ "$HAS_VLESSENC" == "true" ]]; then
    grep -q "type=httpupgrade" <<<"$LINKS_OUT" || { echo "FAIL: links 缺 type=httpupgrade"; echo "$LINKS_OUT"; exit 1; }
fi
if grep -q "dokodemo" <<<"$SUB"; then echo "FAIL: 订阅不应包含 dokodemo 节点"; exit 1; fi
PROXY_COUNT="$(grep -c 'server: ' <<<"$SUB")"
EXPECTED=17
[[ "$HAS_VLESSENC" == "true" ]] && EXPECTED=18
[[ "$PROXY_COUNT" -eq "$EXPECTED" ]] || { echo "FAIL: 订阅应有 $EXPECTED 个代理（dokodemo 除外），实际 $PROXY_COUNT"; echo "$SUB"; exit 1; }
echo "   $EXPECTED 个代理项、ws/httpupgrade/hysteria2 字段 OK，dokodemo 已排除"

echo ">> vless tls 自签数据面（pinnedPeerCertSha256 钉住证书）"
TLS_PORT="$(db "SELECT json_extract(realized_config,'$.port') FROM nodes WHERE id=$TLS_NODE_ID")"
UUID1="$(db "SELECT uuid FROM users WHERE id=1")"
python3 - "$WORK/client-tls.json" "$TLS_PORT" "$UUID1" "$TLS_SNI" "$TLS_PIN" <<'PY'
import json, sys
path, port, uuid, sni, pin = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11812,
                  "protocol": "socks", "settings": {"auth": "noauth"}}],
    "outbounds": [{
        "tag": "proxy", "protocol": "vless",
        "settings": {"vnext": [{"address": "127.0.0.1", "port": port,
                                "users": [{"id": uuid, "encryption": "none"}]}]},
        "streamSettings": {"network": "tcp", "security": "tls",
                           "tlsSettings": {"serverName": sni, "fingerprint": "chrome",
                                           "pinnedPeerCertSha256": pin}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-tls.json" >/dev/null || { echo "FAIL: tls 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-tls.json" >"$WORK/client-tls.log" 2>&1 &
TLSXPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11812" --max-time 8 https://example.com/ || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $TLSXPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: vless tls 自签链路 200（pin 校验通过）" \
    || { echo "FAIL: vless tls 链路未通"; tail -n 5 "$WORK/client-tls.log"; exit 1; }

echo ">> hysteria2 自签数据面（口令取自订阅 hysteria2:// 链接，pin 钉住自签证书）"
HY2_RC="$(db "SELECT realized_config FROM nodes WHERE id=$HY2_NODE_ID")"
HY2_PORT="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["port"])' "$HY2_RC")"
HY2_SNI="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sni"])' "$HY2_RC")"
HY2_PIN="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["cert_sha256"])' "$HY2_RC")"
HY2_LINK="$(python3 - "$LINKS_OUT" "$HY2_PORT" <<'PY'
import sys, urllib.parse
for l in sys.argv[1].splitlines():
    if l.startswith("hysteria2://") and urllib.parse.urlparse(l).port == int(sys.argv[2]):
        print(l); break
PY
)"
[[ -n "$HY2_LINK" ]] || { echo "FAIL: links 缺主用例 hy2 链接（端口 $HY2_PORT）"; echo "$LINKS_OUT"; exit 1; }
# 客户端形态以 Task 1 探针实测为准：streamSettings 必须显式 network=hysteria、
# tlsSettings 必须 alpn=[h3]（缺失分别退化为 TCP 承载/握手 no application protocol）；
# 不发 udpHop——e2e 无 DNAT 时直发主端口，跳跃数据面语义由 Task 1 探针 + root 守卫块覆盖。
python3 - "$WORK/client-hy2.json" "$HY2_LINK" "$HY2_PORT" "$HY2_SNI" "$HY2_PIN" <<'PY'
import json, sys, urllib.parse
path, link, port, sni, pin = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4], sys.argv[5]
u = urllib.parse.urlparse(link)
q = urllib.parse.parse_qs(u.query)
password = urllib.parse.unquote(u.username)
assert q.get("sni") == [sni] and q.get("insecure") == ["1"], link
assert q.get("obfs") == ["salamander"] and q.get("obfs-password") == ["e2e-obfs"], link
assert q.get("upmbps") == ["50"] and q.get("downmbps") == ["100"], link
assert "mport" not in q, link  # port_hop=off：无 mport
cfg = {
    "log": {"loglevel": "warning"},
    "inbounds": [{"tag": "socks", "listen": "127.0.0.1", "port": 11814,
                  "protocol": "socks", "settings": {"auth": "noauth", "udp": True}}],
    "outbounds": [{
        "tag": "hy2", "protocol": "hysteria",
        "settings": {"version": 2, "address": "127.0.0.1", "port": port},
        "streamSettings": {"network": "hysteria", "security": "tls",
            "tlsSettings": {"serverName": sni, "fingerprint": "chrome", "alpn": ["h3"],
                            "pinnedPeerCertSha256": pin},
            "hysteriaSettings": {"version": 2, "auth": password},
            "finalmask": {"udp": [{"type": "salamander", "settings": {"password": "e2e-obfs"}}],
                          "quicParams": {"congestion": "brutal",
                                         "brutalUp": "50 mbps", "brutalDown": "100 mbps"}}}}],
}
json.dump(cfg, open(path, "w"), indent=2)
PY
"$XRAY_BIN" run -test -config "$WORK/client-hy2.json" >/dev/null || { echo "FAIL: hy2 客户端配置校验"; exit 1; }
"$XRAY_BIN" run -config "$WORK/client-hy2.json" >"$WORK/client-hy2.log" 2>&1 &
HY2XPID=$!
ok200=""
for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -x "socks5h://127.0.0.1:11814" --max-time 8 https://example.com/ || true)"
    [[ "$code" == "200" ]] && { ok200=1; break; }
    sleep 2
done
kill $HY2XPID 2>/dev/null || true
[[ -n "$ok200" ]] && echo "OK: hysteria2 自签链路 200（派生口令 + salamander + brutal + pin 全通）" \
    || { echo "FAIL: hy2 链路未通"; tail -n 5 "$WORK/client-hy2.log"; exit 1; }

echo ">> ACME 模式：域名自检失败路径（落地服务器有域名但解析不含本机 → 节点 failed，错误指向性）"
rpc_data POST /api/server/update '{"server_id":1,"address":"127.0.0.1","addresses":["127.0.0.1","acme-e2e.invalid"]}' >/dev/null
ACME_RES="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vmess","security":"tls","cert_mode":"acme"}')"
ACME_ID="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$ACME_RES")"
ACME_OUT="$(wait_node "$ACME_ID")"
[[ "$ACME_OUT" == failed\|* ]] || { echo "FAIL: acme 节点应 failed: $ACME_OUT"; tail -5 "$WORK/agent.log"; exit 1; }
python3 -c 'import sys; err=sys.argv[1].split("|")[2]; assert ("解析" in err) or ("acme" in err.lower()), err' "$ACME_OUT" \
    && echo "OK: acme 节点 failed，错误指向域名解析/acme.sh（$(cut -d'|' -f3 <<<"$ACME_OUT" | head -c 80)…）"
# 清理：删除失败节点并还原服务器地址，避免污染后续断言
rpc_data POST /api/node/delete "{\"node_id\":$ACME_ID}" >/dev/null
rpc_data POST /api/server/update '{"server_id":1,"address":"127.0.0.1","addresses":["127.0.0.1"]}' >/dev/null

echo "E2E-PROTOCOLS PASS"
