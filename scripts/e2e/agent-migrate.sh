#!/usr/bin/env bash
# 兼容性迁移端到端验收（latx-ag migrate 注册表框架）：
#   版本窗口（--from 低于阈值触发 / 达到阈值跳过）→ phase 过滤 → 完成标记幂等 →
#   自守卫（新格式 journal 不动）→ 修复模式（裸 migrate 清掉崩溃循环节点的旧 journal）。
# 首个条目即 0.1.23 事故：≤0.1.22 写下的 server test journal（international 类别）
# 在 ≥0.1.23 的 Payload.Validate() 下不合法，agent 启动即崩溃循环。
# 依赖：bash。无需外网。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

migrate() {
    # DEV 模式调用仓库 latx-ag.sh 的 migrate 子命令（绕开 systemd root 要求）。
    LATX_DEV=1 bash "$ROOT/scripts/latx-ag.sh" migrate "$@"
}

fail() { echo "::error::e2e agent-migrate: $*"; exit 1; }
MARKER_ID="server_test_journal_international"

# --- 1) 版本窗口内 + 旧格式 journal：删除 journal 与配套结果文件，写完成标记 ---
DATA="$WORK/data1"; mkdir -p "$DATA"
printf '{"version":1,"state":"result_pending","payload":{"categories":["latency","international","speed"]}}\n' \
    >"$DATA/server-test-task.json"
echo pending >"$DATA/server-test-result.json.gz"
migrate --from v0.1.21 --phase post --data-dir "$DATA" || fail "迁移 1 应成功"
[[ -f "$DATA/server-test-task.json" ]] && fail "旧 journal 应被删除"
[[ -f "$DATA/server-test-result.json.gz" ]] && fail "配套结果文件应被删除"
[[ -f "$DATA/migrations/$MARKER_ID.done" ]] || fail "完成标记未写入"

# --- 2) 完成标记幂等：重跑不改动数据目录（即使守卫条件重新成立）---
printf 'x' >"$DATA/server-test-task.json"
migrate --from v0.1.21 --phase post --data-dir "$DATA" || fail "标记后重跑应成功"
[[ -f "$DATA/server-test-task.json" ]] || fail "完成标记后重跑不应再改动数据目录"

# --- 3) 版本窗口外：--from 达到阈值即跳过（journal 保留、不写标记）---
DATA="$WORK/data3"; mkdir -p "$DATA"
printf '{"categories":["international"]}\n' >"$DATA/server-test-task.json"
migrate --from v0.1.24 --phase post --data-dir "$DATA" || fail "窗口外迁移应成功跳过"
[[ -f "$DATA/server-test-task.json" ]] || fail "版本窗口外不应删除 journal"
[[ -f "$DATA/migrations/$MARKER_ID.done" ]] && fail "版本窗口外不应写完成标记"

# --- 4) phase 过滤：--phase pre 不执行 post 条目 ---
DATA="$WORK/data4"; mkdir -p "$DATA"
printf '{"categories":["international"]}\n' >"$DATA/server-test-task.json"
migrate --from v0.1.21 --phase pre --data-dir "$DATA" || fail "phase 过滤应成功"
[[ -f "$DATA/server-test-task.json" ]] || fail "phase=pre 不应执行 post 条目"
[[ -f "$DATA/migrations/$MARKER_ID.done" ]] && fail "被 phase 过滤的条目不应写标记"

# --- 5) 自守卫：新格式（connectivity）journal 原样保留 ---
DATA="$WORK/data5"; mkdir -p "$DATA"
printf '{"categories":["latency","connectivity"]}\n' >"$DATA/server-test-task.json"
migrate --from v0.1.21 --phase post --data-dir "$DATA" || fail "守卫路径应成功"
[[ -f "$DATA/server-test-task.json" ]] || fail "新格式 journal 不应被删"
[[ -f "$DATA/migrations/$MARKER_ID.done" ]] || fail "守卫通过（无需改动）也应写完成标记"

# --- 6) 修复模式：裸 migrate（无 --from）清掉崩溃循环节点的旧 journal ---
DATA="$WORK/data6"; mkdir -p "$DATA"
printf '{"categories":["international"]}\n' >"$DATA/server-test-task.json"
migrate --data-dir "$DATA" || fail "修复模式应成功"
[[ -f "$DATA/server-test-task.json" ]] && fail "修复模式应删除旧 journal"
[[ -f "$DATA/migrations/$MARKER_ID.done" ]] || fail "修复模式应写完成标记"

echo ">> e2e agent-migrate 通过（6 组断言全绿）"
