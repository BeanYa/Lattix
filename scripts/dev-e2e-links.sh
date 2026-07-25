#!/usr/bin/env bash
# 分享链接订阅端到端验收（设计文档 §14 `vless://` 链接订阅）：
#   GET /sub/{token}/links → base64 解码 → vless 普通/加密节点链接参数齐全；
#   /sub/{token}（mihomo YAML）回归。
# 另覆盖 §9 订阅三件套：subscription-userinfo / profile-update-interval 响应头、
#   订阅落地页 Accept 分流、用户有效期（到期停权 sweeper 扇出 remove_user、延长恢复 add_user、
#   过期用户订阅/links 为空），以及 §16 显式停用/启用开关（disabled → remove_user 扇出、
#   订阅/links 为空、落地页"已停用"、启用恢复、PATCH 过去有效期 400、disabled+expired 复合不重复扇出）。
# 依赖：python3、curl、本机 xray 二进制（XRAY_BIN 可覆盖）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
XRAY_BIN="${XRAY_BIN:-$HOME/.cache/lattix-dev/xray-core/xray}"
[[ -x "$XRAY_BIN" ]] || { echo "xray 不存在: $XRAY_BIN"; exit 1; }

ADDR="127.0.0.1:18108"
API="127.0.0.1:14208"
BOOTSTRAP="bootstrap-links-test"
XRAY_CONFIG="$WORK/xray-config.json"
JAR="$WORK/cookies.txt"

cleanup() {
    kill ${BPID:-} ${APID:-} 2>/dev/null || true
    pkill -f "xray run -config $XRAY_CONFIG" 2>/dev/null || true
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

api() {
    local args=(-s -b "$JAR" -c "$JAR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "http://$ADDR$2"
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

echo ">> start backend & agent（有效期 sweeper 周期 1s，保证停权确定性）"
LATTIX_EXPIRY_SWEEP_INTERVAL=1s "$WORK/backend" -addr "$ADDR" -db "$WORK/lattix.db" >"$WORK/backend.log" 2>&1 &
BPID=$!
sleep 1
db "INSERT INTO servers (alias, token) VALUES ('lk01', '$BOOTSTRAP')" >/dev/null
"$WORK/agent" -panel "ws://$ADDR/api/agent/ws" -token "$BOOTSTRAP" -state "$WORK/agent.state.json" \
    -xray-bin "$XRAY_BIN" -xray-config "$XRAY_CONFIG" -xray-api "$API" -xray-runner exec \
    >"$WORK/agent.log" 2>&1 &
APID=$!
sleep 1.5

echo ">> 用户 + 普通 vless 节点 + VLESS Encryption 节点"
api POST /api/login '{"username":"admin","password":"lattix-admin"}' >/dev/null
USER_RES="$(api POST /api/users '{"name":"u1"}')"
TOK="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$USER_RES")"
UUID1="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$USER_RES")"
N1="$(api POST /api/nodes '{"server_id":1,"protocol":"vless"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
N2="$(api POST /api/nodes '{"server_id":1,"protocol":"vless","encryption":"mlkem768","flow":"none"}' | python3 -c 'import json,sys;print(json.loads(sys.stdin.read())["id"])')"
wait_active "$N1"; wait_active "$N2"
api PUT "/api/users/1/nodes" "{\"node_ids\":[$N1,$N2]}" >/dev/null
sleep 1

echo ">> /sub/{token}/links 校验"
LINKS="$(curl -s "http://$ADDR/sub/$TOK/links" | python3 -c 'import base64,sys;print(base64.b64decode(sys.stdin.read()).decode())')"
L1="$(grep -c '^vless://' <<<"$LINKS")"
[[ "$L1" == "2" ]] && echo "OK: 2 条 vless 链接" || { echo "FAIL: 链接数 $L1 ≠ 2"; echo "$LINKS"; exit 1; }
grep -q 'security=reality' <<<"$LINKS" && grep -q 'pbk=' <<<"$LINKS" && grep -q 'sid=' <<<"$LINKS" \
    && grep -q 'sni=' <<<"$LINKS" && grep -q 'flow=xtls-rprx-vision' <<<"$LINKS" \
    && echo "OK: reality/flow 参数齐全" || { echo "FAIL: 链接参数缺失"; echo "$LINKS"; exit 1; }
grep -q 'encryption=none' <<<"$LINKS" \
    && echo "OK: 普通节点 encryption=none" || { echo "FAIL: 缺 encryption=none"; echo "$LINKS"; exit 1; }
grep -q 'encryption=mlkem768x25519plus.' <<<"$LINKS" \
    && echo "OK: 加密节点 encryption 客户端字符串" || { echo "FAIL: 缺 encryption 字符串"; echo "$LINKS"; exit 1; }
grep -q '#lk01-vless-' <<<"$LINKS" && echo "OK: 节点命名 fragment" || { echo "FAIL: 命名异常"; echo "$LINKS"; exit 1; }

echo ">> /sub/{token}（mihomo YAML）回归"
SUB="$(curl -s "http://$ADDR/sub/$TOK")"
[[ "$(grep -c 'type: vless' <<<"$SUB")" == "2" ]] \
    && echo "OK: YAML 订阅正常" || { echo "FAIL: YAML 订阅异常"; echo "$SUB"; exit 1; }

echo ">> subscription-userinfo / profile-update-interval 响应头（§9）"
db "INSERT INTO traffic (node_id, user_uuid, up, down) VALUES (0, '$UUID1', 1234, 5678)" >/dev/null
HDRS="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK")"
grep -qi '^subscription-userinfo: upload=1234; download=5678' <<<"$HDRS" \
    && echo "OK: YAML 端点 userinfo 头（无 expire）" || { echo "FAIL: userinfo 头异常"; echo "$HDRS"; exit 1; }
grep -qi '^profile-update-interval: 24' <<<"$HDRS" \
    && echo "OK: profile-update-interval" || { echo "FAIL: 缺 profile-update-interval"; exit 1; }
HDRS_L="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK/links")"
grep -qi '^subscription-userinfo: upload=1234; download=5678' <<<"$HDRS_L" \
    && grep -qi '^profile-update-interval: 24' <<<"$HDRS_L" \
    && echo "OK: links 端点同样带 userinfo 头" || { echo "FAIL: links userinfo 头异常"; echo "$HDRS_L"; exit 1; }

echo ">> 订阅落地页 Accept 分流（§9）"
LAND="$(curl -s -H 'Accept: text/html,application/xhtml+xml' "http://$ADDR/sub/$TOK")"
for kw in 'Lattix 订阅' '已用流量' '长期' "/sub/$TOK" "/sub/$TOK/links" 'clash://install-config?url=' 'mihomo://install-config?url='; do
    grep -qF "$kw" <<<"$LAND" || { echo "FAIL: 落地页缺少 $kw"; exit 1; }
done
grep -q 'qrcode' <<<"$LAND" && echo "OK: 落地页内容齐全（含内嵌二维码库）" || { echo "FAIL: 落地页缺二维码"; exit 1; }
grep -q 'type: vless' <<<"$LAND" && { echo "FAIL: 落地页不应是 YAML"; exit 1; }
L2="$(curl -s -H 'Accept: text/html' "http://$ADDR/sub/$TOK/links" | python3 -c 'import base64,sys;print(base64.b64decode(sys.stdin.read()).decode())')"
[[ "$(grep -c '^vless://' <<<"$L2")" == "2" ]] \
    && echo "OK: /links 不做 Accept 分流" || { echo "FAIL: /links 被分流成落地页"; echo "$L2"; exit 1; }
CODE="$(curl -s -o /dev/null -w '%{http_code}' -H 'Accept: text/html' "http://$ADDR/sub/deadbeef")"
[[ "$CODE" == "404" ]] && echo "OK: 无效 token 落地页 404" || { echo "FAIL: 无效 token 状态码 $CODE"; exit 1; }

echo ">> 有效期：创建在过去 → 400"
CODE="$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -H 'Content-Type: application/json' \
    -X POST -d '{"name":"bad","expires_at":"2000-01-01T00:00:00Z"}' "http://$ADDR/api/users")"
[[ "$CODE" == "400" ]] && echo "OK: 过去有效期被拒" || { echo "FAIL: 状态码 $CODE ≠ 400"; exit 1; }

echo ">> 到期停权：sweeper 置 expired 并扇出 remove_user"
FUTURE_EXP="$(python3 -c 'import datetime;print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=8)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
U2="$(api POST /api/users "{\"name\":\"u2\",\"expires_at\":\"$FUTURE_EXP\"}")"
UID2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["id"])' "$U2")"
UUID2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["uuid"])' "$U2")"
TOK2="$(python3 -c 'import json,sys;print(json.loads(sys.argv[1])["sub_token"])' "$U2")"
EXPIRE2="$(python3 -c 'import json,sys,datetime;print(int(datetime.datetime.fromisoformat(json.loads(sys.argv[1])["expires_at"].replace("Z","+00:00")).timestamp()))' "$U2")"
api PUT "/api/users/$UID2/nodes" "{\"node_ids\":[$N1]}" >/dev/null
wait_clients "$N1" "$UUID2" present && echo "OK: 到期前用户已下发"
[[ "$(curl -s "http://$ADDR/sub/$TOK2" | grep -c 'type: vless')" == "1" ]] \
    && echo "OK: 到期前订阅含 1 节点" || { echo "FAIL: 到期前订阅异常"; exit 1; }
for _ in $(seq 1 30); do
    [[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "1" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "1" ]] \
    && echo "OK: sweeper 已置 expired=1" || { echo "FAIL: sweeper 未停权"; exit 1; }
wait_clients "$N1" "$UUID2" absent && echo "OK: 到期扇出 remove_user 生效"
[[ -z "$(curl -s "http://$ADDR/sub/$TOK2" | grep 'type: vless')" ]] \
    && echo "OK: 过期用户 YAML proxies 为空" || { echo "FAIL: 过期订阅应为空"; exit 1; }
[[ -z "$(curl -s "http://$ADDR/sub/$TOK2/links")" ]] \
    && echo "OK: 过期用户 links 为空" || { echo "FAIL: 过期 links 应为空"; exit 1; }
HDRS2="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK2")"
grep -qi "^subscription-userinfo: upload=0; download=0; expire=$EXPIRE2" <<<"$HDRS2" \
    && echo "OK: 过期用户 userinfo 头保留 expire" || { echo "FAIL: 过期 userinfo 头异常"; echo "$HDRS2"; exit 1; }
LAND2="$(curl -s -H 'Accept: text/html' "http://$ADDR/sub/$TOK2")"
grep -q '已到期' <<<"$LAND2" && echo "OK: 落地页显示已到期" || { echo "FAIL: 落地页缺已到期"; exit 1; }

echo ">> 延长有效期：expired 1→0 并扇出 add_user 恢复"
FUTURE1H="$(python3 -c 'import datetime;print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
api PATCH "/api/users/$UID2" "{\"expires_at\":\"$FUTURE1H\"}" >/dev/null
[[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "0" ]] \
    && echo "OK: expired 已清除" || { echo "FAIL: expired 未清除"; exit 1; }
wait_clients "$N1" "$UUID2" present && echo "OK: 恢复扇出 add_user 生效"
[[ "$(curl -s "http://$ADDR/sub/$TOK2" | grep -c 'type: vless')" == "1" ]] \
    && echo "OK: 恢复后订阅含 1 节点" || { echo "FAIL: 恢复后订阅异常"; exit 1; }

echo ">> 清除有效期（PATCH null → 长期）"
api PATCH "/api/users/$UID2" '{"expires_at":null}' >/dev/null
[[ "$(db "SELECT expires_at FROM users WHERE id=$UID2")" == "" ]] \
    && echo "OK: expires_at 已清除" || { echo "FAIL: expires_at 未清除"; exit 1; }
HDRS3="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK2" | tr -d '\r')"
grep -qi '^subscription-userinfo: upload=0; download=0$' <<<"$HDRS3" \
    && ! grep -qi 'expire=' <<<"$HDRS3" \
    && echo "OK: 长期用户 userinfo 头无 expire" || { echo "FAIL: 清除后 userinfo 头异常"; echo "$HDRS3"; exit 1; }

# cmd_cnt <type> <uuid>：该用户某类命令的累计入队数（用于断言不重复扇出）。
cmd_cnt() { db "SELECT COUNT(*) FROM commands WHERE type='$1' AND payload LIKE '%$2%'"; }

echo ">> 显式停用：PATCH disabled → 扇出 remove_user，订阅/links 为空，落地页已停用（§16）"
api PATCH /api/users/1 '{"disabled":true}' >/dev/null
[[ "$(db "SELECT disabled FROM users WHERE id=1")" == "1" ]] \
    && echo "OK: disabled=1 已置位" || { echo "FAIL: disabled 未置位"; exit 1; }
api GET /api/users | grep -q '"disabled":true' \
    && echo "OK: 用户列表 DTO 带 disabled" || { echo "FAIL: DTO 缺 disabled"; exit 1; }
wait_clients "$N1" "$UUID1" absent && wait_clients "$N2" "$UUID1" absent \
    && echo "OK: 停用扇出 remove_user 生效（两节点）"
[[ -z "$(curl -s "http://$ADDR/sub/$TOK" | grep 'type: vless')" ]] \
    && echo "OK: 停用用户 YAML proxies 为空" || { echo "FAIL: 停用订阅应为空"; exit 1; }
[[ -z "$(curl -s "http://$ADDR/sub/$TOK/links")" ]] \
    && echo "OK: 停用用户 links 为空" || { echo "FAIL: 停用 links 应为空"; exit 1; }
HDRS4="$(curl -s -D - -o /dev/null "http://$ADDR/sub/$TOK")"
grep -qi '^subscription-userinfo: upload=1234; download=5678' <<<"$HDRS4" \
    && echo "OK: 停用用户 userinfo 头照常" || { echo "FAIL: 停用 userinfo 头异常"; echo "$HDRS4"; exit 1; }
LAND3="$(curl -s -H 'Accept: text/html' "http://$ADDR/sub/$TOK")"
grep -q '已停用' <<<"$LAND3" && echo "OK: 落地页显示已停用" || { echo "FAIL: 落地页缺已停用"; exit 1; }

echo ">> PATCH 过去 expires_at → 400（借到期立即停权已由 disable 开关承担）"
CODE="$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -H 'Content-Type: application/json' \
    -X PATCH -d '{"expires_at":"2000-01-01T00:00:00Z"}' "http://$ADDR/api/users/1")"
[[ "$CODE" == "400" ]] && echo "OK: PATCH 过去有效期被拒" || { echo "FAIL: 状态码 $CODE ≠ 400"; exit 1; }

echo ">> 启用：PATCH disabled:false → 扇出 add_user 恢复"
api PATCH /api/users/1 '{"disabled":false}' >/dev/null
[[ "$(db "SELECT disabled FROM users WHERE id=1")" == "0" ]] \
    && echo "OK: disabled 已清除" || { echo "FAIL: disabled 未清除"; exit 1; }
wait_clients "$N1" "$UUID1" present && wait_clients "$N2" "$UUID1" present \
    && echo "OK: 启用扇出 add_user 生效（两节点）"
[[ "$(curl -s "http://$ADDR/sub/$TOK" | grep -c 'type: vless')" == "2" ]] \
    && echo "OK: 启用后订阅含 2 节点" || { echo "FAIL: 启用后订阅异常"; exit 1; }

echo ">> 复合场景：disabled + expired 不重复扇出（有效停权态跃迁才扇出）"
api PATCH "/api/users/$UID2" '{"disabled":true}' >/dev/null
wait_clients "$N1" "$UUID2" absent && echo "OK: 停用 u2 扇出 remove_user"
R0="$(cmd_cnt remove_user "$UUID2")"; A0="$(cmd_cnt add_user "$UUID2")"
FUTURE_EXP2="$(python3 -c 'import datetime;print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=4)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
api PATCH "/api/users/$UID2" "{\"expires_at\":\"$FUTURE_EXP2\"}" >/dev/null
for _ in $(seq 1 30); do
    [[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "1" ]] && break
    sleep 0.5
done
[[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "1" ]] \
    && echo "OK: 停用中到期，sweeper 置 expired" || { echo "FAIL: sweeper 未置 expired"; exit 1; }
sleep 2
[[ "$(cmd_cnt remove_user "$UUID2")" == "$R0" ]] \
    && echo "OK: 到期未重复扇出 remove_user" || { echo "FAIL: 重复扇出 remove_user"; exit 1; }
api PATCH "/api/users/$UID2" '{"disabled":false}' >/dev/null
[[ "$(db "SELECT disabled FROM users WHERE id=$UID2")" == "0" ]] \
    || { echo "FAIL: disabled 未清除"; exit 1; }
[[ -n "$(db "SELECT expires_at FROM users WHERE id=$UID2")" ]] \
    && echo "OK: 省略 expires_at 的 PATCH 不改动有效期" || { echo "FAIL: 有效期被误清除"; exit 1; }
sleep 2
[[ "$(cmd_cnt add_user "$UUID2")" == "$A0" ]] \
    && echo "OK: 仍 expired，解除停用不扇出 add_user" || { echo "FAIL: 误扇出 add_user"; exit 1; }
[[ -z "$(clients_of "$N1" | grep -x "$UUID2")" ]] \
    && echo "OK: 仍处于有效停权态，节点无该用户" || { echo "FAIL: 不应恢复下发"; exit 1; }
api PATCH "/api/users/$UID2" '{"expires_at":null}' >/dev/null
[[ "$(db "SELECT expired FROM users WHERE id=$UID2")" == "0" ]] \
    || { echo "FAIL: expired 未清除"; exit 1; }
wait_clients "$N1" "$UUID2" present && echo "OK: 双重解除后扇出 add_user 恢复"
[[ "$(curl -s "http://$ADDR/sub/$TOK2" | grep -c 'type: vless')" == "1" ]] \
    && echo "OK: 恢复后订阅含 1 节点" || { echo "FAIL: 恢复后订阅异常"; exit 1; }

echo "E2E-LINKS PASS"
