#!/usr/bin/env bash
# 面板自更新端到端验收：本地 release 镜像（模拟 GitHub release 布局）→
# GET /api/panel/version 检出更新 → POST /api/panel/update 异步更新
# （下载/校验/解压/替换进度轮询）→ 更新中其余 API 被 423 锁定 →
# 自重启后面板版本切换为新版、前端产物与 /resource 镜像已替换。无需外网。
# 依赖：python3、curl、go。覆盖：UPDATE_ADDR / MIRROR_PORT。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
OLD_VER="v0.0.1"
NEW_VER="v0.0.2"
MIRROR_PORT="${MIRROR_PORT:-18300}"
ADDR="${UPDATE_ADDR:-127.0.0.1:18301}"
JAR="$WORK/cookies.txt"

cleanup() {
    kill ${BPID:-} ${MIRRORPID:-} 2>/dev/null || true
    pkill -f "$WORK/root/lattix-backend" 2>/dev/null || true
    wait 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

api() {
    local args=(-s -b "$JAR" -c "$JAR" -X "$1")
    [[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$3")
    curl "${args[@]}" "http://$ADDR$2"
}

echo ">> 构建旧版（$OLD_VER）与新版（$NEW_VER）后端"
mkdir -p "$WORK/root"
(cd "$ROOT" && go build -ldflags "-X main.version=$OLD_VER" -o "$WORK/root/lattix-backend" ./src/backend/cmd/backend)
(cd "$ROOT" && go build -ldflags "-X main.version=$NEW_VER" -o "$WORK/lattix-backend-new" ./src/backend/cmd/backend)

echo ">> 搭建本地 release 镜像（$NEW_VER，与 GitHub release 同布局）"
REL="$WORK/releases/$NEW_VER"
mkdir -p "$REL/pkg/lattix-panel/frontend-dist"
cp "$WORK/lattix-backend-new" "$REL/pkg/lattix-panel/lattix-backend"
echo "<html>new frontend $NEW_VER</html>" > "$REL/pkg/lattix-panel/frontend-dist/index.html"
# 面板包垫到 ~64MB：本地镜像下载极快，保证轮询能观察到 download 阶段与进度百分比。
head -c 67108864 /dev/urandom > "$REL/pkg/lattix-panel/pad.bin"
tar -C "$REL/pkg" -czf "$REL/lattix-panel-linux-amd64.tar.gz" lattix-panel
cp "$ROOT/scripts/install.sh" "$REL/install.sh"
# resource 镜像资产（内容任意，校验只过 checksums.txt）
for arch in amd64 arm64; do
    mkdir -p "$REL/agent-$arch/lattix-agent"
    echo "dummy agent $arch" > "$REL/agent-$arch/lattix-agent/lattix-agent"
    tar -C "$REL/agent-$arch" -czf "$REL/lattix-agent-linux-$arch.tar.gz" lattix-agent
done
(cd "$REL" && sha256sum lattix-panel-linux-amd64.tar.gz install.sh \
    lattix-agent-linux-amd64.tar.gz lattix-agent-linux-arm64.tar.gz > checksums.txt)
echo "$NEW_VER" > "$WORK/releases/latest.txt"
(cd "$WORK/releases" && exec python3 -m http.server "$MIRROR_PORT") >/dev/null 2>&1 &
MIRRORPID=$!

echo ">> 启动旧版面板（$OLD_VER，LATTIX_RELEASE_BASE 指向本地镜像）"
mkdir -p "$WORK/root/frontend-dist" "$WORK/root/resource"
echo "<html>old frontend $OLD_VER</html>" > "$WORK/root/frontend-dist/index.html"
LATX_RELEASE_BASE="http://127.0.0.1:$MIRROR_PORT" \
    "$WORK/root/lattix-backend" -addr "$ADDR" -db "$WORK/root/lattix.db" \
    -static "$WORK/root/frontend-dist" -resource "$WORK/root/resource" \
    >"$WORK/panel.log" 2>&1 &
BPID=$!
for _ in $(seq 1 30); do
    curl -s -o /dev/null --max-time 2 "http://$ADDR/" && break
    sleep 0.5
done

echo ">> 登录"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -c "$JAR" -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"lattix-admin"}' "http://$ADDR/api/login")
[[ "$CODE" == "200" ]] || { echo "FAIL: 登录失败 ($CODE)"; exit 1; }

echo ">> GET /api/panel/version 检测更新"
VER_JSON="$(api GET /api/panel/version)"
echo "   $VER_JSON"
echo "$VER_JSON" | grep -q "\"current\":\"$OLD_VER\"" || { echo "FAIL: current 版本不符"; exit 1; }
echo "$VER_JSON" | grep -q "\"latest\":\"$NEW_VER\"" || { echo "FAIL: latest 版本不符"; exit 1; }
echo "$VER_JSON" | grep -q '"update_available":true' || { echo "FAIL: 未检出更新"; exit 1; }

echo ">> POST /api/panel/update 启动更新（后台轮询进度）"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -c "$JAR" -X POST \
    -H 'Content-Type: application/json' -d '{}' "http://$ADDR/api/panel/update")
[[ "$CODE" == "202" ]] || { echo "FAIL: 启动更新失败 ($CODE)"; exit 1; }

LOCK_VERIFIED=0
SAW_DOWNLOAD=0
SAW_PROGRESS=0
for _ in $(seq 1 200); do
    ST="$(api GET /api/panel/update/status 2>/dev/null || true)"
    [[ -z "$ST" ]] && break # 面板已退出重启
    if echo "$ST" | grep -q '"stage":"download"'; then
        SAW_DOWNLOAD=1
        echo "$ST" | grep -q '"percent":[0-9]' && SAW_PROGRESS=1
    fi
    if [[ "$LOCK_VERIFIED" -eq 0 ]] && echo "$ST" | grep -q '"running":true'; then
        CODE=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -X POST "http://$ADDR/api/settings/restart")
        [[ "$CODE" == "423" ]] || { echo "FAIL: 更新中未锁定其余 API（/api/settings/restart 返回 $CODE，期望 423）"; exit 1; }
        LOCK_VERIFIED=1
        echo ">> 更新中其余 API 已锁定（423）"
    fi
    echo "$ST" | grep -q '"running":false' && { echo "FAIL: 更新意外终止: $ST"; exit 1; }
    sleep 0.1
done
[[ "$SAW_DOWNLOAD" -eq 1 && "$SAW_PROGRESS" -eq 1 ]] \
    || { echo "FAIL: 未观察到 download 阶段进度（download=$SAW_DOWNLOAD progress=$SAW_PROGRESS）"; exit 1; }

echo ">> 等待面板重启恢复"
BACK=0
for _ in $(seq 1 60); do
    VER_JSON="$(api GET /api/panel/version 2>/dev/null || true)"
    if [[ -n "$VER_JSON" ]]; then BACK=1; break; fi
    sleep 1
done
[[ "$BACK" -eq 1 ]] || { echo "FAIL: 面板未在 60s 内恢复；日志："; cat "$WORK/panel.log"; exit 1; }

echo ">> 校验新版本与文件替换"
echo "   $VER_JSON"
echo "$VER_JSON" | grep -q "\"current\":\"$NEW_VER\"" || { echo "FAIL: 重启后版本未切换到 $NEW_VER"; exit 1; }
echo "$VER_JSON" | grep -q '"update_available":false' || { echo "FAIL: 重启后仍提示有更新"; exit 1; }
grep -q "$NEW_VER" "$WORK/root/frontend-dist/index.html" || { echo "FAIL: 前端产物未替换"; exit 1; }
for f in install.sh checksums.txt lattix-agent-linux-amd64.tar.gz lattix-agent-linux-arm64.tar.gz; do
    [[ -f "$WORK/root/resource/$f" ]] || { echo "FAIL: /resource 镜像缺 $f"; exit 1; }
done
[[ -f "$WORK/root/lattix-backend.bak" ]] || { echo "FAIL: 旧二进制未备份"; exit 1; }

echo ">> 重复触发更新应幂等（已是最新，done 不重启）"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -c "$JAR" -X POST \
    -H 'Content-Type: application/json' -d '{}' "http://$ADDR/api/panel/update")
[[ "$CODE" == "202" ]] || { echo "FAIL: 重复更新启动失败 ($CODE)"; exit 1; }
sleep 2
ST="$(api GET /api/panel/update/status)"
echo "$ST" | grep -q '"stage":"done"' || { echo "FAIL: 幂等更新未进入 done: $ST"; exit 1; }

echo "PASS: 面板自更新 e2e（$OLD_VER → $NEW_VER，锁定/进度/替换/重启/幂等全过）"
