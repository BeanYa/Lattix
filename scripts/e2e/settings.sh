#!/usr/bin/env bash
# 设置页端到端验收（设计文档 §10/§12）：
#   GET/POST /api/setting/get|update：对外地址（public-url）与时区保存/读取/清除后回退启动参数 →
#   管理员改密（bcrypt 落库、旧 session 失效、新密码可登录）→
#   TLS 域名路径模式（tls_mode=path：证书目录 <dir>/<域名>/fullchain.pem|privkey.pem，
#   POST /api/panel/restart 自重启后同端口 HTTPS 生效，agent 经 wss 首连）→
#   证书文件替换后热加载（下一次握手用新证书，免重启）。
# 管理 API 均为 RPC 信封（feat(api)! 后无 REST 端点）：写操作需 Idempotency-Key 与
#   X-CSRF-Token；业务错误 HTTP 200 + code=INVALID_ARGUMENT/AUTH_INVALID_CREDENTIALS 等，
#   协议错误才用 HTTP 状态码。
# 依赖：openssl、python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
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
CSRF=""
SCHEME="http"          # 自重启切 HTTPS 后置 https
CACERT_ARGS=()         # HTTPS 阶段的 --cacert 参数

cleanup() {
    kill ${SPID:-} 2>/dev/null || true   # 先停 supervisor 防止重拉
    pkill -f "$WORK/backend -addr $ADDR" 2>/dev/null || true
    kill ${APID:-} 2>/dev/null || true
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

# rpc_raw <method> <path> [body]：RPC 请求（POST 自动附随机 Idempotency-Key 与 CSRF 令牌）。
rpc_raw() {
    local method="$1" path="$2" body="${3:-}"
    local args=(-sS -b "$JAR" -c "$JAR" -X "$method" -H "Origin: $SCHEME://$ADDR")
    [[ "${#CACERT_ARGS[@]}" -gt 0 ]] && args+=("${CACERT_ARGS[@]}")
    if [[ "$method" == "POST" ]]; then
        [[ -n "$body" ]] || body='{}'
        args+=(-H 'Content-Type: application/json' -H "Idempotency-Key: $(openssl rand -hex 16)" -d "$body")
        [[ -z "$CSRF" ]] || args+=(-H "X-CSRF-Token: $CSRF")
    fi
    curl "${args[@]}" "$SCHEME://$ADDR$path"
}

# rpc_data：解包 RPC 信封输出 data（code 非 OK/ACCEPTED 立即失败）。
rpc_data() {
    rpc_raw "$@" | python3 -c '
import json,sys
value=json.load(sys.stdin)
if value["code"] not in ("OK","ACCEPTED"):
    raise SystemExit(f"RPC failed: {value}")
print(json.dumps(value["data"], separators=(",",":")))
'
}

# rpc_code：输出 RPC 信封 code（用于业务错误断言）。
rpc_code() {
    rpc_raw "$@" | python3 -c 'import json,sys;print(json.load(sys.stdin)["code"])'
}

# do_login <password>：登录并刷新 CSRF 令牌。
do_login() {
    local login
    login="$(rpc_data POST /api/auth/login "{\"username\":\"admin\",\"password\":\"$1\"}")"
    CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$login")"
    [[ -n "$CSRF" ]]
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

echo ">> start backend（supervisor 循环：自重启后自动重拉，模拟 systemd）"
cat > "$WORK/supervisor.sh" <<'SUP'
#!/usr/bin/env bash
while true; do
    "$@" >>"${BACKEND_LOG}" 2>&1
    sleep 0.2
done
SUP
chmod +x "$WORK/supervisor.sh"
BACKEND_LOG="$WORK/backend.log" "$WORK/supervisor.sh" \
    "$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" \
    -public-url "$PUBLIC_URL_FALLBACK" -tls-dir "$WORK/certs" \
    -static "$WORK/none" &
SPID=$!
for _ in $(seq 1 30); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && break; sleep 0.2; done
do_login "$ADMIN_PASS" && echo "OK: 登录成功" || { echo "FAIL: 登录失败"; exit 1; }

# --- GET/POST /api/setting/get|update：时区与对外地址 ---
echo ">> GET /api/setting/get 基线"
G="$(rpc_data GET /api/setting/get)"
[[ "$(echo "$G" | py "(d['timezone'], d['public_url'], d['tls_mode'], d['running_tls_mode'], d['restart_required'], d['password_override'])")" == "('', '', '', 'off', False, False)" ]] \
    && echo "OK: 设置基线（全空，跟随启动参数）" || { echo "FAIL: 基线: $G"; exit 1; }

S1="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"s1"}')"
printf '%s' "$(echo "$S1" | py "d['install_command']")" | grep -Fq -- "install.sh | bash -s -- agent --panel $PUBLIC_URL_FALLBACK --token " \
    && echo "OK: 未保存 public_url 时安装命令使用启动参数" || { echo "FAIL: install_command: $S1"; exit 1; }

echo ">> POST /api/setting/update 保存时区与对外地址"
[[ "$(rpc_code POST /api/setting/update '{"timezone":"Mars/Olympus"}')" == "INVALID_ARGUMENT" ]] \
    && echo "OK: 非法时区被拒" || { echo "FAIL: 非法时区未拒"; exit 1; }
[[ "$(rpc_code POST /api/setting/update '{"public_url":"ftp://x"}')" == "INVALID_ARGUMENT" ]] \
    && echo "OK: 非法对外地址被拒" || { echo "FAIL: 非法对外地址未拒"; exit 1; }
G="$(rpc_data POST /api/setting/update '{"timezone":"Asia/Shanghai","public_url":"https://panel.example.com"}')"
[[ "$(echo "$G" | py "(d['timezone'], d['public_url'])")" == "('Asia/Shanghai', 'https://panel.example.com')" ]] \
    && echo "OK: 时区与对外地址已保存" || { echo "FAIL: 保存: $G"; exit 1; }
S2="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"s2"}')"
printf '%s' "$(echo "$S2" | py "d['install_command']")" | grep -Fq -- "install.sh | bash -s -- agent --panel https://panel.example.com --token " \
    && echo "OK: 保存后安装命令使用对外地址" || { echo "FAIL: install_command: $S2"; exit 1; }
U="$(rpc_data POST /api/user/create '{"name":"u1"}')"
[[ "$(echo "$U" | py "d['sub_url']")" == "https://panel.example.com/sub/"* ]] \
    && echo "OK: 订阅链接使用对外地址" || { echo "FAIL: sub_url: $U"; exit 1; }

echo ">> 清除后回退启动参数"
G="$(rpc_data POST /api/setting/update '{"timezone":"","public_url":""}')"
[[ "$(echo "$G" | py "(d['timezone'], d['public_url'])")" == "('', '')" ]] \
    && echo "OK: 设置已清除" || { echo "FAIL: 清除: $G"; exit 1; }
S3="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"s3"}')"
printf '%s' "$(echo "$S3" | py "d['install_command']")" | grep -Fq -- "install.sh | bash -s -- agent --panel $PUBLIC_URL_FALLBACK --token " \
    && echo "OK: 清除后安装命令回退启动参数" || { echo "FAIL: 回退: $S3"; exit 1; }

# --- 管理员密码修改 ---
echo ">> 改密：校验当前密码 + bcrypt 落库 + 旧 session 失效"
[[ "$(rpc_code POST /api/setting/change-password '{"current_password":"wrong","new_password":"'$ADMIN_PASS_NEW'"}')" == "AUTH_INVALID_CREDENTIALS" ]] \
    && echo "OK: 当前密码错误被拒（AUTH_INVALID_CREDENTIALS）" || { echo "FAIL: 错误当前密码未拒"; exit 1; }
[[ "$(rpc_code POST /api/setting/change-password '{"current_password":"'$ADMIN_PASS'","new_password":"short"}')" == "INVALID_ARGUMENT" ]] \
    && echo "OK: 新密码过短被拒（INVALID_ARGUMENT）" || { echo "FAIL: 短密码未拒"; exit 1; }
[[ "$(rpc_code POST /api/setting/change-password '{"current_password":"'$ADMIN_PASS'","new_password":"'$ADMIN_PASS_NEW'"}')" == "OK" ]] \
    && echo "OK: 改密成功" || { echo "FAIL: 改密"; exit 1; }
HASH="$(sql "SELECT value FROM settings WHERE key='admin_pass_bcrypt'")"
[[ "$HASH" == \$2* ]] && echo "OK: 新密码 bcrypt 哈希落库" || { echo "FAIL: 哈希: $HASH"; exit 1; }
[[ "$(rpc_code GET /api/auth/me)" == "AUTH_REQUIRED" ]] \
    && echo "OK: 改密后旧 session 失效（AUTH_REQUIRED）" || { echo "FAIL: 旧 session 仍有效"; exit 1; }
[[ "$(rpc_code POST /api/auth/login '{"username":"admin","password":"'$ADMIN_PASS'"}')" == "AUTH_INVALID_CREDENTIALS" ]] \
    && echo "OK: 旧密码不可登录" || { echo "FAIL: 旧密码仍可登录"; exit 1; }
do_login "$ADMIN_PASS_NEW" && echo "OK: 新密码可登录" || { echo "FAIL: 新密码登录"; exit 1; }
[[ "$(rpc_data GET /api/setting/get | py "d['password_override']")" == "True" ]] \
    && echo "OK: password_override=true" || { echo "FAIL: password_override"; exit 1; }

# --- TLS 域名路径模式（tls_mode=path）+ 自重启 ---
echo ">> tls_mode=path：证书目录校验 + 保存"
gen_pair "$WORK/cert1" "lattix-test-ca1"
mkdir -p "$CERT_DIR"
cat "$WORK/cert1/server.pem" "$WORK/cert1/ca.pem" > "$CERT_DIR/fullchain.pem"
cp "$WORK/cert1/server.key" "$CERT_DIR/privkey.pem"
[[ "$(rpc_code POST /api/setting/update '{"tls_mode":"path","tls_domain":"no-such.example"}')" == "INVALID_ARGUMENT" ]] \
    && echo "OK: 目录内无证书的域名被拒（INVALID_ARGUMENT）" || { echo "FAIL: 无证书域名未拒"; exit 1; }
G="$(rpc_data POST /api/setting/update "{\"tls_mode\":\"path\",\"tls_domain\":\"$TLS_DOMAIN\"}")"
[[ "$(echo "$G" | py "(d['tls_mode'], d['tls_domain'], d['running_tls_mode'], d['restart_required'], d['tls_cert']['common_name'])")" == "('path', '$TLS_DOMAIN', 'off', True, 'localhost')" ]] \
    && echo "OK: tls_mode=path 已保存（restart_required=true，证书摘要就绪）" || { echo "FAIL: 保存 TLS: $G"; exit 1; }

echo ">> POST /api/panel/restart 自重启 → 同端口 HTTPS 生效"
BOOTSTRAP="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"tls01"}' | py "d['bootstrap_token']")"
[[ "$(rpc_code POST /api/panel/restart)" == "ACCEPTED" ]] \
    || { echo "FAIL: restart 接口非 ACCEPTED"; exit 1; }
for _ in $(seq 1 40); do
    curl -s -o /dev/null --cacert "$WORK/cert1/ca.pem" "https://$ADDR/readyz" && break
    sleep 0.5
done
curl -s -o /dev/null --cacert "$WORK/cert1/ca.pem" "https://$ADDR/readyz" \
    || { echo "FAIL: 重启后 HTTPS 未恢复"; cat "$WORK/backend.log"; exit 1; }
BPID="$(pgrep -f "$WORK/backend -addr $ADDR" | head -1)"
[[ -n "$BPID" ]] \
    && echo "OK: supervisor 重拉新进程 $BPID" || { echo "FAIL: 新进程未发现"; exit 1; }
SCHEME="https"
CACERT_ARGS=(--cacert "$WORK/cert1/ca.pem")
do_login "$ADMIN_PASS_NEW" \
    && echo "OK: 重启后同端口 HTTPS 可登录（新密码经 DB 生效）" || { echo "FAIL: 重启后登录"; exit 1; }
G="$(rpc_data GET /api/setting/get)"
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
curl -s -o /dev/null --cacert "$WORK/cert2/ca.pem" "https://$ADDR/api/auth/me" \
    && echo "OK: 新 CA 可完成握手" || { echo "FAIL: 新证书未生效"; exit 1; }
if curl -s -o /dev/null --cacert "$WORK/cert1/ca.pem" "https://$ADDR/api/auth/me"; then
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
