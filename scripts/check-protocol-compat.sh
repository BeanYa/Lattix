#!/usr/bin/env bash
# 协议兼容性门禁（CI: .github/workflows/protocol-check.yml）。
#
# 用法: bash scripts/check-protocol-compat.sh <base-ref>
#   例: bash scripts/check-protocol-compat.sh origin/main
#       bash scripts/check-protocol-compat.sh v0.0.1
#
# 检查 src/shared/messages.go 相对 <base-ref> "协议行只增不减"：
#   1. 消息类型常量行（形如 TypeXxx = "xxx"）
#   2. 结构体字段行（字段名 + 类型 + `json:"..."` tag）
# base 版本中出现的每一行都必须仍存在于 working tree 版本中（逐行精确比对），
# 发现缺失即打印并 exit 1；全在则打印 OK。
#
# 说明：本脚本只管"协议行只增不减"这一行级不变量；Go API 层面的兼容性
# （函数签名、行为语义）由 code review 与 e2e（scripts/dev-e2e-*.sh 及
# release workflow 的 compat job）保证。
set -euo pipefail

BASE_REF="${1:?usage: bash scripts/check-protocol-compat.sh <base-ref>}"
FILE="src/shared/messages.go"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BASE_CONTENT="$(git show "${BASE_REF}:${FILE}")" || {
    echo "无法读取 ${BASE_REF}:${FILE}（base-ref 是否正确？）" >&2
    exit 1
}
[[ -f "$FILE" ]] || { echo "working tree 中不存在 $FILE" >&2; exit 1; }

# 提取协议行：消息类型常量 + 带 json tag 的字段行。
# 常量行：`\tTypeHello       = "hello"        // 注释` → `TypeHello = "hello"`
# 字段行：`\tID      string          `json:"id"`` → `ID string `json:"id"``
# 归一化空白后比对，容忍对齐空格/注释变化，不容忍名称/类型/tag 变化。
extract_protocol_lines() {
    local input
    input="$(cat)"
    printf '%s\n' "$input" | grep -oE 'Type[A-Za-z0-9_]+[[:space:]]*=[[:space:]]*"[^"]*"' \
        | sed -E 's/[[:space:]]*=[[:space:]]*/ = /' || true
    printf '%s\n' "$input" | grep -oE '^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*[[:space:]]+[][A-Za-z0-9_.*]+[[:space:]]+`json:"[^"]*"`' \
        | sed -E 's/^[[:space:]]+//; s/[[:space:]]+/ /g' || true
}

BASE_LINES="$(printf '%s\n' "$BASE_CONTENT" | extract_protocol_lines | sort -u)"
WORK_LINES="$(extract_protocol_lines < "$FILE" | sort -u)"

[[ -n "$BASE_LINES" ]] || { echo "base 版本中未提取到协议行，检查正则是否匹配 $FILE" >&2; exit 1; }

MISSING=0
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    if ! grep -qxF "$line" <<< "$WORK_LINES"; then
        echo "MISSING: $line"
        MISSING=1
    fi
done <<< "$BASE_LINES"

if [[ "$MISSING" -ne 0 ]]; then
    echo "FAIL: $FILE 相对 $BASE_REF 存在协议行缺失/变更（协议只增不减）" >&2
    exit 1
fi
echo "OK: $FILE 相对 $BASE_REF 协议行只增不减（$(wc -l <<< "$BASE_LINES" | tr -d ' ') 行全在）"
