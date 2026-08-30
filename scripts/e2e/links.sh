#!/usr/bin/env bash
# 分享链接订阅端到端验收（设计文档 §14 `vless://` 链接订阅）：
#   GET /sub/{token}/links → base64 解码 → vless 普通/加密节点链接参数齐全；
#   /sub/{token}（mihomo YAML）回归。
# 另覆盖 §9 订阅三件套：subscription-userinfo / profile-update-interval 响应头
#   （含扩展字段 total / reset_day / plan_name / app_url：用户级设置、全局默认回退、
#   用户级覆盖全局），订阅落地页 Accept 分流、用户有效期（到期停权 sweeper 扇出
#   remove_user、延长恢复 add_user、过期用户订阅/links 为空），以及 §16 显式停用/启用
#   开关（disabled → remove_user 扇出、订阅/links 为空、落地页"已停用"、启用恢复、
#   过去有效期被拒、disabled+expired 复合不重复扇出）。
# 管理 API 均为 RPC 信封（feat(api)! 后无 REST 端点）：写操作需 Idempotency-Key 与
#   X-CSRF-Token；业务错误 HTTP 200 + code=INVALID_ARGUMENT 等，协议错误才用 HTTP 状态码。
# 依赖：python3、curl、openssl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }
# VLESS Encryption 需带 vlessenc 子命令的 xray（§15）；缺失时退化为第二个普通节点，
# 跳过 encryption 客户端字符串断言（专用数据通路由 e2e/vlessenc.sh 覆盖）。
HAS_VLESSENC=true
"$XRAY_BIN" vlessenc >/dev/null 2>&1 || HAS_VLESSENC=false

ADDR="127.0.0.1:18108"
# release 构建拒绝默认管理员密码启动（dev 构建豁免），e2e 一律显式指定。
ADMIN_PASS="links-e2e-pass"
API="127.0.0.1:14208"
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"
CSRF=""

cleanup() {
    kill ${BPID:-} ${APID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

echo ">> build"
if [[ -n "${PANEL_BIN:-}" && -n "${AGENT_BIN:-}" && -x "${PANEL_BIN:-}" && -x "${AGENT_BIN:-}" ]]; then
    # CI 注入的 release 产物（内嵌前端）；本地缺省自构建（需已构建前端 dist，否则落地页断言缺 SPA）。
    cp "$PANEL_BIN" "$WORK/backend"
    cp "$AGENT_BIN" "$WORK/agent"
else
    (cd "$ROOT" && go build -o "$WORK/backend" ./src/backend/cmd/backend && go build -o "$WORK/agent" ./src/agent/cmd/agent)
fi

db() { python3 - "$WORK/lattix.db" "$1" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
cur = con.execute(sys.argv[2])
con.commit()
for row in cur: print("|".join("" if c is None else str(c) for c in row))
PY
}

# rpc_raw <method> <path> [body]：RPC 请求（POST 自动附随机 Idempotency-Key 与 CSRF 令牌）。
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

wait_active() {
    for _ in $(seq 1 40); do
        [[ "$(db "SELECT status FROM nodes WHERE id=$1")" == "active" ]] && return 0
        sleep 0.5
    done
    echo "FAIL: 节点 $1 未 active"; return 1
}

# clients_of <node-id>：输出该节点 inbound 的 clients email 列表。
clients_of() {
    python3 - "$XRAY_CONFIG" "$1" <<'PY'
import json, sys
cfg = json.load(open(sys.argv[1]))
for ib in cfg.get("inbounds", []):
    if ib.get("tag") == f"node_{sys.argv[2]}":
        for c in ib.get("settings", {}).get("clients", []):
            print(c.get("email", ""))
PY
}

# wait_clients <node-id> <uuid> <present|absent>
wait_clients() {
    for _ in $(seq 1 30); do
        found=false
        while IFS= read -r email; do [[ "$email" == "$2" ]] && found=true; done < <(clients_of "$1")
        [[ "$3" == "present" && "$found" == "true" ]] && return 0
        [[ "$3" == "absent" && "$found" == "false" ]] && return 0
        sleep 0.5
    done
    echo "FAIL: 节点 $1 用户 $2 未达 $3 状态"; return 1
}

# wait_sub_vless <token> <count>：订阅重发布已转异步（操作入队 + regenerator 去抖），
# 轮询 clash 正文的 vless 行数直到达到预期。
wait_sub_vless() {
    for _ in $(seq 1 20); do
        [[ "$(curl -s "http://$ADDR/sub/$1?format=clash" | grep -c 'type: vless')" == "$2" ]] && return 0
        sleep 0.5
    done
    echo "FAIL: 订阅 $1 vless 行数未达 $2"; return 1
}

echo ">> start backend（有效期 sweeper 周期 1s，保证停权确定性）"
LATTIX_EXPIRY_SWEEP_INTERVAL=1s "$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" -admin-pass "$ADMIN_PASS" >"$WORK/backend.log" 2>&1 &
BPID=$!
ok=0
for _ in $(seq 1 75); do curl -fsS "http://$ADDR/readyz" >/dev/null 2>&1 && { ok=1; break; }; sleep 0.2; done
# 后端 15s 未就绪即硬失败并转储日志（慢 CI 上原 6s 静默放行会让后续用例以
# 连接拒绝的形式连环报错，难以定位）。
[[ "$ok" == "1" ]] || { echo "FAIL: 后端就绪超时"; cat "$WORK/backend.log"; exit 1; }

echo ">> 登录（签发会话 + CSRF 令牌）"
LOGIN="$(rpc_data POST /api/auth/login '{"username":"admin","password":"'"$ADMIN_PASS"'"}')"
CSRF="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["csrf_token"])' "$LOGIN")"
[[ -n "$CSRF" ]] && echo "OK: 登录成功" || { echo "FAIL: 未取到 CSRF 令牌"; exit 1; }

echo ">> 创建服务器 lk01 并启动 agent（bootstrap 凭证由面板签发，ltx1 格式）"
SRV="$(rpc_data POST /api/server/create '{"country_code":"US","location":"Test","alias":"lk01","address":"127.0.0.1"}')"
BOOTSTRAP="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["bootstrap_token"])' "$SRV")"
[[ -n "$BOOTSTRAP" ]] || { echo "FAIL: 未取到 bootstrap 凭证"; exit 1; }
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 1.5

echo ">> 用户 + 普通 vless 节点 + VLESS Encryption 节点"
USER_RES="$(rpc_data POST /api/user/create '{"name":"u1"}')"
TOK="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$USER_RES")"
UUID1="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$USER_RES")"
N1="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
if [[ "$HAS_VLESSENC" == "true" ]]; then
    N2="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless","encryption":"mlkem768","flow":"none"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
else
    echo "SKIP: xray 缺 vlessenc 子命令，次节点改用普通 vless（跳过 VLESS Encryption 断言）"
    N2="$(rpc_data POST /api/node/create '{"server_id":1,"protocol":"vless"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
fi
wait_active "$N1"; wait_active "$N2"
rpc_data POST /api/user/set-nodes "{\"user_id\":1,\"node_ids\":[$N1,$N2]}" >/dev/null
wait_sub_vless "$TOK" 2 || exit 1

echo ">> /sub/{token}?format=links 校验"
LINKS="$(curl -s "http://$ADDR/sub/$TOK?format=links" | python3 -c 'import base64,sys;print(base64.b64decode(sys.stdin.read()).decode())')"
L1="$(grep -c '^vless://' <<<"$LINKS")"
[[ "$L1" == "2" ]] && echo "OK: 2 条 vless 链接" || { echo "FAIL: 链接数 $L1 ≠ 2"; echo "$LINKS"; exit 1; }
grep -q 'security=reality' <<<"$LINKS" && grep -q 'pbk=' <<<"$LINKS" && grep -q 'sid=' <<<"$LINKS" \
    && grep -q 'sni=' <<<"$LINKS" && grep -q 'flow=xtls-rprx-vision' <<<"$LINKS" \
    && echo "OK: reality/flow 参数齐全" || { echo "FAIL: 链接参数缺失"; echo "$LINKS"; exit 1; }
grep -q 'encryption=none' <<<"$LINKS" \
    && echo "OK: 普通节点 encryption=none" || { echo "FAIL: 缺 encryption=none"; echo "$LINKS"; exit 1; }
if [[ "$HAS_VLESSENC" == "true" ]]; then
    grep -q 'encryption=mlkem768x25519plus.' <<<"$LINKS" \
        && echo "OK: 加密节点 encryption 客户端字符串" || { echo "FAIL: 缺 encryption 字符串"; echo "$LINKS"; exit 1; }
fi
grep -q '#lk01-vless-' <<<"$LINKS" && echo "OK: 节点命名 fragment" || { echo "FAIL: 命名异常"; echo "$LINKS"; exit 1; }

echo ">> /sub/{token}?format=clash（mihomo YAML）回归"
SUB="$(curl -s "http://$ADDR/sub/$TOK?format=clash")"
[[ "$(grep -c 'type: vless' <<<"$SUB")" == "2" ]] \
    && echo "OK: YAML 订阅正常" || { echo "FAIL: YAML 订阅异常"; echo "$SUB"; exit 1; }

echo ">> subscription-userinfo / profile-update-interval 响应头（§9）"
# period_start 置当月月初，避免 1s 周期的流量重置 sweeper 清零（period_start 为空会被视为待初始化）。
PERIOD_START="$(python3 -c 'import datetime;print(datetime.datetime.now(datetime.timezone.utc).replace(day=1,hour=0,minute=0,second=0,microsecond=0).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
db "INSERT INTO traffic (node_id, user_uuid, up, down, period_start) VALUES (0, '$UUID1', 1234, 5678, '$PERIOD_START')" >/dev/null
HDRS="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK?format=clash")"
grep -qi '^subscription-userinfo: upload=1234; download=5678' <<<"$HDRS" \
    && echo "OK: YAML 端点 userinfo 头（无 expire）" || { echo "FAIL: userinfo 头异常"; echo "$HDRS"; exit 1; }
grep -qi '^profile-update-interval: 24' <<<"$HDRS" \
    && echo "OK: profile-update-interval" || { echo "FAIL: 缺 profile-update-interval"; exit 1; }
HDRS_L="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK?format=links")"
grep -qi '^subscription-userinfo: upload=1234; download=5678' <<<"$HDRS_L" \
    && grep -qi '^profile-update-interval: 24' <<<"$HDRS_L" \
    && echo "OK: links 端点同样带 userinfo 头" || { echo "FAIL: links userinfo 头异常"; echo "$HDRS_L"; exit 1; }

echo ">> userinfo 扩展字段：配额/reset_day/套餐名/跳转链接（§9 用户级订阅设置）"
rpc_data POST /api/user/sub-settings \
    '{"user_id":1,"traffic_limit":1073741824,"traffic_reset_day":1,"plan_name":"VIP1","app_url":"https://example.com"}' >/dev/null
HDRS_X="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK?format=clash" | tr -d '\r')"
grep -Eqi '^subscription-userinfo: upload=1234; download=5678; total=1073741824; reset_day=[0-9]+; plan_name=VIP1; app_url=https://example\.com' <<<"$HDRS_X" \
    && echo "OK: 扩展 userinfo 头（total/reset_day/plan_name/app_url）" \
    || { echo "FAIL: 扩展 userinfo 头异常"; echo "$HDRS_X"; exit 1; }

echo ">> 订阅落地页 SPA 分流（§9）"
LAND="$(curl -s -H 'Accept: text/html,application/xhtml+xml' -H 'User-Agent: Mozilla/5.0' "http://$ADDR/sub/$TOK")"
grep -q '<div id="root"' <<<"$LAND" \
    && echo "OK: 浏览器 UA 返回 SPA index.html" || { echo "FAIL: 落地页未返回 SPA"; echo "$LAND" | head -5; exit 1; }
grep -q 'type: vless' <<<"$LAND" && { echo "FAIL: 落地页不应是 YAML"; exit 1; }
CODE="$(curl -s -o /dev/null -w '%{http_code}' -H 'Accept: text/html' -H 'User-Agent: Mozilla/5.0' "http://$ADDR/sub/deadbeef")"
[[ "$CODE" == "404" ]] && echo "OK: 无效 token 404" || { echo "FAIL: 无效 token 状态码 $CODE"; exit 1; }

echo ">> 有效期：创建在过去 → INVALID_ARGUMENT"
BAD_CODE="$(rpc_code POST /api/user/create '{"name":"bad","expires_at":"2000-01-01T00:00:00Z"}')"
[[ "$BAD_CODE" == "INVALID_ARGUMENT" ]] \
    && echo "OK: 过去有效期被拒" || { echo "FAIL: code=$BAD_CODE"; exit 1; }

echo ">> 到期停权：sweeper 置 expired 并扇出 remove_user"
FUTURE_EXP="$(python3 -c 'import datetime;print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=8)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
U2="$(rpc_data POST /api/user/create "{\"name\":\"u2\",\"expires_at\":\"$FUTURE_EXP\"}")"
UID2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$U2")"
UUID2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$U2")"
TOK2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$U2")"
EXPIRE2="$(python3 -c 'import json,sys,datetime;print(int(datetime.datetime.fromisoformat(json.loads(sys.argv[1])["expires_at"].replace("Z","+00:00")).timestamp()))' "$U2")"
rpc_data POST /api/user/set-nodes "{\"user_id\":$UID2,\"node_ids\":[$N1]}" >/dev/null
wait_clients "$N1" "$UUID2" present && echo "OK: 到期前用户已下发"
wait_sub_vless "$TOK2" 1 \
    && echo "OK: 到期前订阅含 1 节点" || exit 1
for _ in $(seq 1 30); do
    [[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "1" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "1" ]] \
    && echo "OK: sweeper 已置 expired=1" || { echo "FAIL: sweeper 未停权"; exit 1; }
wait_clients "$N1" "$UUID2" absent && echo "OK: 到期扇出 remove_user 生效"
# 到期重发布经订阅队列异步完成（sweeper → EnqueueUsers → regenerator），轮询等待。
YAML_EMPTY=0
LINKS_EMPTY=0
for _ in $(seq 1 20); do
    [[ -z "$(curl -s "http://$ADDR/sub/$TOK2?format=clash" | grep 'type: vless')" ]] && YAML_EMPTY=1
    [[ -z "$(curl -s "http://$ADDR/sub/$TOK2?format=links")" ]] && LINKS_EMPTY=1
    [[ "$YAML_EMPTY" == "1" && "$LINKS_EMPTY" == "1" ]] && break
    sleep 0.5
done
[[ "$YAML_EMPTY" == "1" ]] \
    && echo "OK: 过期用户 YAML proxies 为空" || { echo "FAIL: 过期订阅应为空"; exit 1; }
[[ "$LINKS_EMPTY" == "1" ]] \
    && echo "OK: 过期用户 links 为空" || { echo "FAIL: 过期 links 应为空"; exit 1; }
HDRS2="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK2?format=clash")"
grep -qi "^subscription-userinfo: upload=0; download=0; expire=$EXPIRE2" <<<"$HDRS2" \
    && echo "OK: 过期用户 userinfo 头保留 expire" || { echo "FAIL: 过期 userinfo 头异常"; echo "$HDRS2"; exit 1; }
LAND2="$(curl -s -H 'Accept: text/html' -H 'User-Agent: Mozilla/5.0' "http://$ADDR/sub/$TOK2")"
grep -q '<div id="root"' <<<"$LAND2" && echo "OK: 过期用户落地页返回 SPA" || { echo "FAIL: 过期用户落地页异常"; exit 1; }

echo ">> 延长有效期：expired 1→0 并扇出 add_user 恢复"
FUTURE1H="$(python3 -c 'import datetime;print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
rpc_data POST /api/user/update "{\"user_id\":$UID2,\"expires_at\":\"$FUTURE1H\"}" >/dev/null
[[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "0" ]] \
    && echo "OK: expired 已清除" || { echo "FAIL: expired 未清除"; exit 1; }
wait_clients "$N1" "$UUID2" present && echo "OK: 恢复扇出 add_user 生效"
wait_sub_vless "$TOK2" 1 \
    && echo "OK: 恢复后订阅含 1 节点" || exit 1

echo ">> 清除有效期（expires_at:null → 长期）"
rpc_data POST /api/user/update "{\"user_id\":$UID2,\"expires_at\":null}" >/dev/null
[[ "$(db "SELECT expires_at FROM users WHERE id=$UID2")" == "" ]] \
    && echo "OK: expires_at 已清除" || { echo "FAIL: expires_at 未清除"; exit 1; }
HDRS3="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK2?format=clash" | tr -d '\r')"
grep -qi '^subscription-userinfo: upload=0; download=0$' <<<"$HDRS3" \
    && ! grep -qi 'expire=' <<<"$HDRS3" \
    && echo "OK: 长期用户 userinfo 头无 expire" || { echo "FAIL: 清除后 userinfo 头异常"; echo "$HDRS3"; exit 1; }

echo ">> 全局默认套餐名/跳转链接回退（用户级为空 → 取全局，§9）"
rpc_data POST /api/setting/sub '{"plan_name":"GLOBAL-PLAN","app_url":"https://global.example.com"}' >/dev/null
HDRS_G="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK2?format=clash" | tr -d '\r')"
grep -Eqi '^subscription-userinfo: upload=0; download=0; plan_name=GLOBAL-PLAN; app_url=https://global\.example\.com$' <<<"$HDRS_G" \
    && echo "OK: 全局默认套餐名/跳转链接回退" || { echo "FAIL: 全局回退头异常"; echo "$HDRS_G"; exit 1; }
HDRS_O="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK?format=clash" | tr -d '\r')"
grep -qi 'plan_name=VIP1' <<<"$HDRS_O" && ! grep -qi 'GLOBAL-PLAN' <<<"$HDRS_O" \
    && echo "OK: 用户级套餐名覆盖全局" || { echo "FAIL: 用户级覆盖全局异常"; echo "$HDRS_O"; exit 1; }

# cmd_cnt <type> <uuid>：该用户某类命令的累计入队数（用于断言不重复扇出）。
cmd_cnt() { db "SELECT COUNT(*) FROM commands WHERE type='$1' AND data LIKE '%$2%'"; }

echo ">> 显式停用：disabled:true → 扇出 remove_user，订阅/links 为空，落地页已停用（§16）"
rpc_data POST /api/user/update '{"user_id":1,"disabled":true}' >/dev/null
[[ "$(db "SELECT disabled FROM users WHERE id=1")" == "1" ]] \
    && echo "OK: disabled=1 已置位" || { echo "FAIL: disabled 未置位"; exit 1; }
rpc_data GET /api/user/list | grep -q '"disabled":true' \
    && echo "OK: 用户列表 DTO 带 disabled" || { echo "FAIL: DTO 缺 disabled"; exit 1; }
wait_clients "$N1" "$UUID1" absent && wait_clients "$N2" "$UUID1" absent \
    && echo "OK: 停用扇出 remove_user 生效（两节点）"
# 停用重发布经订阅队列异步完成，轮询等待（同到期停权段）。
DIS_YAML_EMPTY=0
DIS_LINKS_EMPTY=0
for _ in $(seq 1 20); do
    [[ -z "$(curl -s "http://$ADDR/sub/$TOK?format=clash" | grep 'type: vless')" ]] && DIS_YAML_EMPTY=1
    [[ -z "$(curl -s "http://$ADDR/sub/$TOK?format=links")" ]] && DIS_LINKS_EMPTY=1
    [[ "$DIS_YAML_EMPTY" == "1" && "$DIS_LINKS_EMPTY" == "1" ]] && break
    sleep 0.5
done
[[ "$DIS_YAML_EMPTY" == "1" ]] \
    && echo "OK: 停用用户 YAML proxies 为空" || { echo "FAIL: 停用订阅应为空"; exit 1; }
[[ "$DIS_LINKS_EMPTY" == "1" ]] \
    && echo "OK: 停用用户 links 为空" || { echo "FAIL: 停用 links 应为空"; exit 1; }
HDRS4="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK?format=clash")"
grep -qi '^subscription-userinfo: upload=1234; download=5678' <<<"$HDRS4" \
    && echo "OK: 停用用户 userinfo 头照常" || { echo "FAIL: 停用 userinfo 头异常"; echo "$HDRS4"; exit 1; }
LAND3="$(curl -s -H 'Accept: text/html' -H 'User-Agent: Mozilla/5.0' "http://$ADDR/sub/$TOK")"
grep -q '<div id="root"' <<<"$LAND3" && echo "OK: 停用用户落地页返回 SPA" || { echo "FAIL: 停用用户落地页异常"; exit 1; }

echo ">> 更新过去 expires_at → INVALID_ARGUMENT（借到期立即停权已由 disable 开关承担）"
BAD2_CODE="$(rpc_code POST /api/user/update '{"user_id":1,"expires_at":"2000-01-01T00:00:00Z"}')"
[[ "$BAD2_CODE" == "INVALID_ARGUMENT" ]] \
    && echo "OK: 过去有效期被拒" || { echo "FAIL: code=$BAD2_CODE"; exit 1; }

echo ">> 启用：disabled:false → 扇出 add_user 恢复"
rpc_data POST /api/user/update '{"user_id":1,"disabled":false}' >/dev/null
[[ "$(db "SELECT disabled FROM users WHERE id=1")" == "0" ]] \
    && echo "OK: disabled 已清除" || { echo "FAIL: disabled 未清除"; exit 1; }
wait_clients "$N1" "$UUID1" present && wait_clients "$N2" "$UUID1" present \
    && echo "OK: 启用扇出 add_user 生效（两节点）"
wait_sub_vless "$TOK" 2 \
    && echo "OK: 启用后订阅含 2 节点" || exit 1

echo ">> 复合场景：disabled + expired 不重复扇出（有效停权态跃迁才扇出）"
rpc_data POST /api/user/update "{\"user_id\":$UID2,\"disabled\":true}" >/dev/null
wait_clients "$N1" "$UUID2" absent && echo "OK: 停用 u2 扇出 remove_user"
R0="$(cmd_cnt remove_user "$UUID2")"; A0="$(cmd_cnt add_user "$UUID2")"
FUTURE_EXP2="$(python3 -c 'import datetime;print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=4)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
rpc_data POST /api/user/update "{\"user_id\":$UID2,\"expires_at\":\"$FUTURE_EXP2\"}" >/dev/null
for _ in $(seq 1 30); do
    [[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "1" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "1" ]] \
    && echo "OK: 停用中到期，sweeper 置 expired" || { echo "FAIL: sweeper 未置 expired"; exit 1; }
sleep 2
[[ "$(cmd_cnt remove_user "$UUID2")" == "$R0" ]] \
    && echo "OK: 到期未重复扇出 remove_user" || { echo "FAIL: 重复扇出 remove_user"; exit 1; }
rpc_data POST /api/user/update "{\"user_id\":$UID2,\"disabled\":false}" >/dev/null
[[ "$(db "SELECT disabled FROM users WHERE id=$UID2")" == "0" ]] \
    || { echo "FAIL: disabled 未清除"; exit 1; }
[[ -n "$(db "SELECT expires_at FROM users WHERE id=$UID2")" ]] \
    && echo "OK: 省略 expires_at 的更新不改动有效期" || { echo "FAIL: 有效期被误清除"; exit 1; }
sleep 2
[[ "$(cmd_cnt add_user "$UUID2")" == "$A0" ]] \
    && echo "OK: 仍 expired，解除停用不扇出 add_user" || { echo "FAIL: 误扇出 add_user"; exit 1; }
[[ -z "$(clients_of "$N1" | grep -x "$UUID2")" ]] \
    && echo "OK: 仍处于有效停权态，节点无该用户" || { echo "FAIL: 不应恢复下发"; exit 1; }
rpc_data POST /api/user/update "{\"user_id\":$UID2,\"expires_at\":null}" >/dev/null
[[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "0" ]] \
    || { echo "FAIL: expired 未清除"; exit 1; }
wait_clients "$N1" "$UUID2" present && echo "OK: 双重解除后扇出 add_user 恢复"
# 订阅重发布经订阅队列异步完成（同上文到期/停用段），用 wait_sub_vless 轮询等待。
wait_sub_vless "$TOK2" 1 \
    && echo "OK: 恢复后订阅含 1 节点" || exit 1

echo "E2E-LINKS PASS"
