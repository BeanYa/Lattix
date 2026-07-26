#!/usr/bin/env bash
# 设置页端到端验收（设计文档 §10/§12）：
#   GET/PUT /api/settings：对外地址（public-url）与时区保存/读取/清除后回退启动参数 →
#   管理员改密（bcrypt 落库、旧 session 失效、新密码可登录）→
#   TLS 域名路径模式（tls_mode=path：证书目录 <dir>/<域名>/fullchain.pem|privkey.pem，
#   POST /api/settings/restart 自重启后同端口 HTTPS 生效，agent 经 wss 首连）→
#   证书文件替换后热加载（下一次握手用新证书，免重启）。
# 依赖：openssl、python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18109"
API="127.0.0.1:14209"
ADMIN_PASS="testpass123"
ADMIN_PASS_NEW="newpass456"
PUBLIC_URL_FALLBACK="http://fallback.example:9000"
TLS_DOMAIN="settings.test"
CERT_DIR="$WORK/certs/$TLS_DOMAIN" # <tls-dir>/<域名>/fullchain.pem|privkey.pem
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"

cleanup() {
    kill ${BPID:-} ${APID:-} 2>/dev/null || true
    pkill -f "$WORK/backend -addr $ADDR" 2>/dev/null || true # 自重启派生的进程非本脚本子进程，兜底清理
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build"
(cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)

sql() { python3 - "$WORK/lattix.db" "$1" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
cur = con.execute(sys.argv[2])
con.commit()
for row in cur: print("|".join("" if c is None else str(c) for c in row))
PY
}
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
api() { # $1=METHOD $2=path [$3=body]（http + 初始 cookie jar）
    local args=(-s -b "$JAR" -c "$JAR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "http://$ADDR$2"
}
apis() { # $1=METHOD $2=path [$3=body]（https + CA1 + 初始 cookie jar）
    local args=(-s --cacert "$WORK/cert1/ca.pem" -b "$JAR" -c "$JAR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "https://$ADDR$2"
}

# gen_pair <outdir> <ca-cn>：生成一套 CA + 服务器证书（SAN 含 127.0.0.1/localhost）。
gen_pair() {
    mkdir -p "$1"
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
        -keyout "$1/ca.key" -out "$1/ca.pem" -subj "/CN=$2" >/dev/null 2>&1
    openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
        -keyout "$1/server.key" -out "$1/server.csr" -subj "/CN=localhost" >/dev/null 2>&1
    printf "subjectAltName=DNS:localhost,IP:127.0.0.1\n" > "$1/san.cnf"
    openssl x509 -req -in "$1/server.csr" -CA "$1/ca.pem" -CAkey "$1/ca.key" -CAcreateserial \
        -days 1 -extfile "$1/san.cnf" -out "$1/server.pem" >/dev/null 2>&1
}

echo ">> start backend（-public-url 启动参数回退值 + -tls-dir 证书根目录）"
"$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -public-url "$PUBLIC_URL_FALLBACK" -tls-dir "$WORK/certs" \
    -resource "$WORK/resource" -install-script "$ROOT/scripts/install.sh" -static "$WORK/none" \
    >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1
api POST /api/login "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}" >/dev/null

# --- GET/PUT /api/settings：时区与对外地址 ---
echo ">> GET /api/settings 基线"
G="$(api GET /api/settings)"
[[ "$(echo "$G" | py "(d['timezone'], d['public_url'], d['tls_mode'], d['running_tls_mode'], d['restart_required'], d['password_override'])")" == "('', '', '', 'off', False, False)" ]] \
    && echo "OK: 设置基线（全空，跟随启动参数）" || { echo "FAIL: 基线: $G"; exit 1; }

S1="$(api POST /api/servers '{"country_code":"US","location":"Test","alias":"s1"}')"
# dev 构建（version=dev）无 release 可钉：安装命令回退面板 /resource 托管源（--source panel）。
[[ "$(echo "$S1" | py "d['install_command']")" == "curl -fsSL $PUBLIC_URL_FALLBACK/resource/install.sh | bash -s -- --source panel --panel $PUBLIC_URL_FALLBACK --token "* ]] \
    && echo "OK: 未保存 public_url 时安装命令回退启动参数（dev 面板 /resource 托管源）" || { echo "FAIL: install_command: $S1"; exit 1; }

echo ">> PUT 保存时区与对外地址"
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' -d '{"timezone":"Mars/Olympus"}' "http://$ADDR/api/settings")" == "400" ]] \
    && echo "OK: 非法时区被拒" || { echo "FAIL: 非法时区未拒"; exit 1; }
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' -d '{"public_url":"ftp://x"}' "http://$ADDR/api/settings")" == "400" ]] \
    && echo "OK: 非法对外地址被拒" || { echo "FAIL: 非法对外地址未拒"; exit 1; }
G="$(api PUT /api/settings '{"timezone":"Asia/Shanghai","public_url":"https://panel.example.com"}')"
[[ "$(echo "$G" | py "(d['timezone'], d['public_url'])")" == "('Asia/Shanghai', 'https://panel.example.com')" ]] \
    && echo "OK: 时区与对外地址已保存" || { echo "FAIL: 保存: $G"; exit 1; }
S2="$(api POST /api/servers '{"country_code":"US","location":"Test","alias":"s2"}')"
[[ "$(echo "$S2" | py "d['install_command']")" == "curl -fsSL https://panel.example.com/resource/install.sh | bash -s -- --source panel --panel https://panel.example.com --token "* ]] \
    && echo "OK: 保存后安装命令使用对外地址" || { echo "FAIL: install_command: $S2"; exit 1; }
U="$(api POST /api/users '{"name":"u1"}')"
[[ "$(echo "$U" | py "d['sub_url']")" == "https://panel.example.com/sub/"* ]] \
    && echo "OK: 订阅链接使用对外地址" || { echo "FAIL: sub_url: $U"; exit 1; }

echo ">> 安装资源来源（resource_source）：校验 + 保存 + 清除"
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' -d '{"resource_source":"ftp"}' "http://$ADDR/api/settings")" == "400" ]] \
    && echo "OK: 非法资源来源被拒" || { echo "FAIL: 非法资源来源未拒"; exit 1; }
G="$(api PUT /api/settings '{"resource_source":"panel"}')"
[[ "$(echo "$G" | py "d['resource_source']")" == "panel" ]] \
    && echo "OK: resource_source=panel 已保存" || { echo "FAIL: resource_source 保存: $G"; exit 1; }
G="$(api PUT /api/settings '{"resource_source":"github"}')"
[[ -z "$(echo "$G" | py "d['resource_source']")" ]] \
    && echo "OK: resource_source=github 恢复默认（设置删除）" || { echo "FAIL: resource_source 清除: $G"; exit 1; }

echo ">> PUT 清除后回退启动参数"
G="$(api PUT /api/settings '{"timezone":"","public_url":""}')"
[[ "$(echo "$G" | py "(d['timezone'], d['public_url'])")" == "('', '')" ]] \
    && echo "OK: 设置已清除" || { echo "FAIL: 清除: $G"; exit 1; }
S3="$(api POST /api/servers '{"country_code":"US","location":"Test","alias":"s3"}')"
[[ "$(echo "$S3" | py "d['install_command']")" == "curl -fsSL $PUBLIC_URL_FALLBACK/resource/install.sh | bash -s -- --source panel --panel $PUBLIC_URL_FALLBACK --token "* ]] \
    && echo "OK: 清除后安装命令回退启动参数" || { echo "FAIL: 回退: $S3"; exit 1; }

# --- 管理员密码修改 ---
echo ">> 改密：校验当前密码 + bcrypt 落库 + 旧 session 失效"
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' -d '{"current_password":"wrong","new_password":"'$ADMIN_PASS_NEW'"}' "http://$ADDR/api/settings/password")" == "403" ]] \
    && echo "OK: 当前密码错误被拒（403）" || { echo "FAIL: 错误当前密码未拒"; exit 1; }
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' -d '{"current_password":"'$ADMIN_PASS'","new_password":"short"}' "http://$ADDR/api/settings/password")" == "400" ]] \
    && echo "OK: 新密码过短被拒（400）" || { echo "FAIL: 短密码未拒"; exit 1; }
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' -d '{"current_password":"'$ADMIN_PASS'","new_password":"'$ADMIN_PASS_NEW'"}' "http://$ADDR/api/settings/password")" == "204" ]] \
    && echo "OK: 改密成功（204）" || { echo "FAIL: 改密"; exit 1; }
HASH="$(sql "SELECT value FROM settings WHERE key='admin_pass_bcrypt'")"
[[ "$HASH" == \$2* ]] && echo "OK: 新密码 bcrypt 哈希落库" || { echo "FAIL: 哈希: $HASH"; exit 1; }
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' "http://$ADDR/api/me")" == "401" ]] \
    && echo "OK: 改密后旧 session 失效" || { echo "FAIL: 旧 session 仍有效"; exit 1; }
[[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{"username":"admin","password":"'$ADMIN_PASS'"}' "http://$ADDR/api/login")" == "401" ]] \
    && echo "OK: 旧密码不可登录" || { echo "FAIL: 旧密码仍可登录"; exit 1; }
api POST /api/login "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS_NEW\"}" | grep -q '"admin"' \
    && echo "OK: 新密码可登录" || { echo "FAIL: 新密码登录"; exit 1; }
[[ "$(api GET /api/settings | py "d['password_override']")" == "True" ]] \
    && echo "OK: password_override=true" || { echo "FAIL: password_override"; exit 1; }

# --- TLS 域名路径模式（tls_mode=path）+ 自重启 ---
echo ">> tls_mode=path：证书目录校验 + 保存"
gen_pair "$WORK/cert1" "lattix-test-ca1"
mkdir -p "$CERT_DIR"
cat "$WORK/cert1/server.pem" "$WORK/cert1/ca.pem" > "$CERT_DIR/fullchain.pem"
cp "$WORK/cert1/server.key" "$CERT_DIR/privkey.pem"
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' -d '{"tls_mode":"path","tls_domain":"no-such.example"}' "http://$ADDR/api/settings")" == "400" ]] \
    && echo "OK: 目录内无证书的域名被拒（400）" || { echo "FAIL: 无证书域名未拒"; exit 1; }
G="$(api PUT /api/settings "{\"tls_mode\":\"path\",\"tls_domain\":\"$TLS_DOMAIN\"}")"
[[ "$(echo "$G" | py "(d['tls_mode'], d['tls_domain'], d['running_tls_mode'], d['restart_required'], d['tls_cert']['common_name'])")" == "('path', '$TLS_DOMAIN', 'off', True, 'localhost')" ]] \
    && echo "OK: tls_mode=path 已保存（restart_required=true，证书摘要就绪）" || { echo "FAIL: 保存 TLS: $G"; exit 1; }

echo ">> POST /api/settings/restart 自重启 → 同端口 HTTPS 生效"
BOOTSTRAP="$(api POST /api/servers '{"country_code":"US","location":"Test","alias":"tls01"}' | py "d['bootstrap_token']")"
[[ "$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' -X POST "http://$ADDR/api/settings/restart")" == "202" ]] \
    || { echo "FAIL: restart 接口非 202"; exit 1; }
for _ in $(seq 1 40); do
    curl -s -o /dev/null --cacert "$WORK/cert1/ca.pem" "https://$ADDR/api/login" && break
    sleep 0.5
done
curl -s -o /dev/null --cacert "$WORK/cert1/ca.pem" "https://$ADDR/api/login" \
    || { echo "FAIL: 重启后 HTTPS 未恢复"; cat "$WORK/backend.log"; exit 1; }
OLD_BPID=$BPID
BPID="$(pgrep -f "$WORK/backend -addr $ADDR" | head -1)"
[[ -n "$BPID" && "$BPID" != "$OLD_BPID" ]] \
    && echo "OK: 自派生新进程 $BPID（旧进程 $OLD_BPID 已退出）" || { echo "FAIL: 新进程未发现"; exit 1; }
apis POST /api/login "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS_NEW\"}" | grep -q '"admin"' \
    && echo "OK: 重启后同端口 HTTPS 可登录（新密码经 DB 生效）" || { echo "FAIL: 重启后登录"; exit 1; }
G="$(apis GET /api/settings)"
[[ "$(echo "$G" | py "(d['running_tls_mode'], d['restart_required'])")" == "('path', False)" ]] \
    && echo "OK: 重启后 running_tls_mode=path，restart_required 已清除" || { echo "FAIL: 重启后设置: $G"; exit 1; }

echo ">> agent 经 wss 首连"
SSL_CERT_FILE="$WORK/cert1/ca.pem" "$WORK/agent" -panel "wss://$ADDR/api/agent/ws" -token "$BOOTSTRAP" \
    -state "$WORK/agent.state.json" -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" \
    -xray-api "$API" -xray-runner exec >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 2
grep -q "authenticated as server" "$WORK/agent.log" \
    && echo "OK: agent 经 wss 认证成功（tls_mode=path 生效）" \
    || { echo "FAIL: agent wss 首连失败"; cat "$WORK/agent.log"; exit 1; }

echo ">> 证书文件替换 → 下一次握手热加载新证书（免重启）"
gen_pair "$WORK/cert2" "lattix-test-ca2"
cat "$WORK/cert2/server.pem" "$WORK/cert2/ca.pem" > "$CERT_DIR/fullchain.pem"
cp "$WORK/cert2/server.key" "$CERT_DIR/privkey.pem"
sleep 0.5
curl -s -o /dev/null --cacert "$WORK/cert2/ca.pem" "https://$ADDR/api/me" \
    && echo "OK: 新 CA 可完成握手" || { echo "FAIL: 新证书未生效"; exit 1; }
if curl -s -o /dev/null --cacert "$WORK/cert1/ca.pem" "https://$ADDR/api/me"; then
    echo "FAIL: 旧证书仍在服务"; exit 1
else
    echo "OK: 旧 CA 握手被拒（确为新证书）"
fi
SERVED_FP="$(echo | openssl s_client -connect "$ADDR" -servername "$TLS_DOMAIN" 2>/dev/null | openssl x509 -fingerprint -sha256 -noout)"
NEW_FP="$(openssl x509 -in "$WORK/cert2/server.pem" -fingerprint -sha256 -noout)"
[[ "$SERVED_FP" == "$NEW_FP" ]] \
    && echo "OK: 服务证书指纹与替换后证书一致" || { echo "FAIL: 指纹不符（$SERVED_FP vs $NEW_FP）"; exit 1; }
[[ "$(pgrep -f "$WORK/backend -addr $ADDR" | head -1)" == "$BPID" ]] \
    && echo "OK: 热加载未重启进程" || { echo "FAIL: 进程发生变化"; exit 1; }

echo "E2E-SETTINGS PASS"
