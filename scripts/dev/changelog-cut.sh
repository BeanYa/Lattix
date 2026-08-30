#!/usr/bin/env bash
# changelog-cut.sh：把 docs/CHANGELOG.md 的 [Unreleased] 段固化为指定版本段。
# 用法：scripts/dev/changelog-cut.sh vX.Y.Z（发布流程见 README「CI/CD 与版本发布（§18）」）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHANGELOG="$ROOT/docs/CHANGELOG.md"

if [[ $# -ne 1 ]]; then
    echo "用法: $0 vX.Y.Z" >&2
    exit 2
fi
VERSION="${1#v}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "FAIL: 版本号须形如 vX.Y.Z：$1" >&2
    exit 2
fi

# [Unreleased] 段须存在且非空（提取逻辑与 release.yml 发布步骤一致）。
if ! grep -q '^## \[Unreleased\]' "$CHANGELOG"; then
    echo "FAIL: 未找到 [Unreleased] 段" >&2
    exit 1
fi
SECTION="$(awk '
    /^## \[Unreleased\]/ { found = 1; next }
    found && /^## / { exit }
    found { print }
' "$CHANGELOG")"
if [[ -z "$(printf '%s' "$SECTION" | tr -d '[:space:]')" ]]; then
    echo "FAIL: [Unreleased] 段为空，没有可固化的内容" >&2
    exit 1
fi

# 保留原文件行尾风格（本仓库 docs/CHANGELOG.md 为 CRLF）。
NL=""
if grep -q $'\r' "$CHANGELOG"; then
    NL=$'\r'
fi

TMP="$(mktemp "$CHANGELOG.XXXXXX")"
trap 'rm -f "$TMP"' EXIT

awk -v version="$VERSION" -v date="$(date +%F)" -v nl="$NL" '
    index($0, "## [" version "]") == 1 {
        printf "FAIL: 已存在版本段 [%s]，请勿重复固化\n", version > "/dev/stderr"
        failed = 1
        exit 3
    }
    !cut && /^## \[Unreleased\]/ {
        print
        print nl
        print "## [" version "] - " date nl
        cut = 1
        next
    }
    { print }
    END {
        if (failed) exit 3
        if (!cut) {
            print "FAIL: 未找到 [Unreleased] 段" > "/dev/stderr"
            exit 3
        }
    }
' "$CHANGELOG" >"$TMP"

cat "$TMP" >"$CHANGELOG"
echo ">> [Unreleased] 已固化为 [$VERSION] - $(date +%F)"
