# 成本统计 Minor 修复实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 处理成本统计双 tab 实施后记录的 4 项 Minor：拆分 Costs.tsx（~956 行）、消除 per-tab 重复助手、补 estimated 月视图 nil 断言、为 breaking API 改名补 changelog 条目。

**Architecture:** 前端把共享助手抽到 `src/pages/costs-shared.tsx`，`EstimatedCostsTab` 抽到 `src/pages/EstimatedCostsTab.tsx`，`Costs.tsx` 只留页面外壳 + `ActualCostsTab`；排序状态收敛为共享 `useRowSort` hook（消除两 tab 的 toggleSort/sortHeader 重复）。后端补 1 个 nil 断言。新增 `docs/CHANGELOG.md` 记录 breaking 改名与新端点。

**Tech Stack:** React 19 + TS（前端）、Go（后端测试）、Markdown（changelog）。

## Global Constraints

- 纯重构，行为不变：前端拆分后 `bun run build && bun run lint && bun run test` 必须全过（lint 0 警告 0 错误）
- `Costs.tsx` 的 default export（页面外壳 + `App.tsx` 懒加载路径 `@/pages/Costs`）保持不变；`ActualCostsTab` 保留在 `Costs.tsx` 内
- 后端测试：`go test ./src/backend/...` 全过；只加断言不改逻辑
- 不动 `docs/openapi.yaml` 与 API 契约（无 API 变更）
- 验证命令：后端 `go test ./src/backend/...`（仓库根）；前端在 `src/frontend` 下 `bun run build && bun run lint && bun run test`
- 工作目录：worktree `\\wsl.localhost\Ubuntu\home\bean\workspace\Lattix-codex\.worktree\cost-stats-minor-fixes`，分支 `feat/cost-stats-minor-fixes`

---

### Task 1: 前端拆分 Costs.tsx（共享助手 + useRowSort + EstimatedCostsTab 独立文件）

**Files:**
- Create: `src/frontend/src/pages/costs-shared.tsx`
- Create: `src/frontend/src/pages/EstimatedCostsTab.tsx`
- Modify: `src/frontend/src/pages/Costs.tsx`（删除已移走的助手，改为 import）

**Interfaces:**
- Produces（costs-shared.tsx 的 export，后续 Task 消费）: `SERVER_PALETTE`、`GRANULARITY_LABEL`、`billingStatusVariant`、`billingStatusLabel`、`localDate`、`addMonths`、`addDays`、`firstOfMonth`、`daySpan`、`clampRange`、`money`、`periodLabel(period, granularity)`、`useEarliestStart()`、`CostsSeriesServer`、`buildBarOption(options)`、`buildDonutOption(data, currency, textColor, theme)`、`StatsControls` + `StatsControlsProps`、`CostSortKey`、`CostSortState`、`useRowSort()`
- Produces（EstimatedCostsTab.tsx）: `export default function EstimatedCostsTab()`（内部 `costsOfEstimated`）
- Consumes: 现有 `Costs.tsx`（worktree 中 `src/frontend/src/pages/Costs.tsx`，956 行）——先读全文，符号逐字搬移

- [ ] **Step 1: 创建 `src/pages/costs-shared.tsx`**

从现有 `Costs.tsx` 逐字搬移以下内容，并新增 `useRowSort`：

- 常量与纯函数（原样搬移，import 相应收敛）：`SERVER_PALETTE`、`GRANULARITY_LABEL`、`billingStatusVariant`、`billingStatusLabel`、`localDate`、`addMonths`、`addDays`、`firstOfMonth`、`daySpan`、`clampRange`、`money`、`periodLabel`（签名改为 `periodLabel(period: string, granularity: BillingStatsGranularity)`——原实现从组件闭包读 granularity，改为显式参数）
- `useEarliestStart`（原样搬移，含 `api.servers` 调用）
- `CostsSeriesServer` interface、`buildBarOption`、`buildDonutOption`（原样搬移）
- `StatsControlsProps` interface + `StatsControls` 组件（原样搬移）
- 新增排序 hook（替换两个 tab 各自的 `toggleSort`/`sortHeader`/`sort` state）：

```tsx
export type CostSortKey = 'name' | 'total' | 'daily' | 'share'
export interface CostSortState {
  key: CostSortKey
  dir: 1 | -1
}

export function useRowSort() {
  const [sort, setSort] = useState<CostSortState>({ key: 'total', dir: -1 })
  const toggle = (key: CostSortKey) => {
    setSort((current) => current.key === key
      ? { key, dir: current.dir === 1 ? -1 : 1 }
      : { key, dir: key === 'name' ? 1 : -1 })
  }
  const header = (key: CostSortKey, label: string, className?: string) => (
    <button
      type="button"
      onClick={() => toggle(key)}
      className={cn('inline-flex items-center gap-1 hover:text-foreground', className)}
    >
      {label}
      <span className={cn('text-[10px] opacity-60', sort.key === key ? 'opacity-100' : 'invisible')}>
        {sort.dir === 1 ? '↑' : '↓'}
      </span>
    </button>
  )
  return { sort, toggle, header }
}
```

所需 import（按需）：`react`（`useEffect`、`useState`）、`CoinsIcon`（lucide-react）、`Chart`/`ChartOption`（`@/components/echarts`）、`Button`/`Card`/`CardContent`/`Input`/`Select` 系列/`Tabs` 系列（`@/components/ui/*`）、`api`（`@/lib/api`）、`BillingStatsGranularity`/`BillingStatsRateMode`（`@/lib/types`）、`cn`（`@/lib/utils`）。所有 export 加 `export` 关键字。

- [ ] **Step 2: 创建 `src/pages/EstimatedCostsTab.tsx`**

从 `Costs.tsx` 搬移 `EstimatedCostsTab` 组件与 `costsOfEstimated`（逐字），并按共享化调整：

- import 改为：react（`useCallback`、`useEffect`、`useMemo`、`useState`）、`CountryFlag`、`EmptyState`/`LoadingState`/`Notice`、`Badge`、`Card` 系列、`Table` 系列、`api`/`errorMessage`、`useTheme`、`BillingEstimatedServerStats`/`BillingEstimatedStats`/`BillingStatsGranularity`/`BillingStatsRateMode`（`@/lib/types`）、来自 `./costs-shared` 的共享符号
- 删除组件内的 `toggleSort`/`sortHeader`/`periodLabel`/`SERVER_PALETTE` 等（改从共享 import）；`sort` state 与 `setSort` 替换为 `const { sort, header } = useRowSort()`，`toggleSort` 调用点改 `header(key, label, className)`，`sort` 用法不变
- `periodLabel(period)` 调用改为 `periodLabel(period, granularity)`
- `const [sort, setSort] = useState<{ key: 'name' | 'total' | 'daily' | 'share'; dir: 1 | -1 }>({ key: 'total', dir: -1 })` 行删除
- 组件签名 `export default function EstimatedCostsTab()`（文件 default export）

- [ ] **Step 3: 精简 `Costs.tsx`**

- 删除已搬移的符号定义（SERVER_PALETTE、GRANULARITY_LABEL、billingStatus*、localDate、addMonths、addDays、firstOfMonth、daySpan、clampRange、money、periodLabel、useEarliestStart、CostsSeriesServer、buildBarOption、buildDonutOption、StatsControlsProps、StatsControls、costsOfEstimated、EstimatedCostsTab）
- `import { EstimatedCostsTab } from './EstimatedCostsTab'` 与 `import { ...共享符号 } from './costs-shared'`
- `ActualCostsTab` 内：`toggleSort`/`sortHeader` 替换为 `const { sort, header } = useRowSort()`；`periodLabel(period)` → `periodLabel(period, granularity)`；删除本地 sort state
- `Costs()` default export 保持不变（App.tsx 懒加载路径不变）
- import 收敛：`CoinsIcon` 若仅剩 EmptyState 使用则保留；逐字检查没有未使用 import（tsc `noUnusedLocals` 会拦截）

- [ ] **Step 4: 验证前端**

Run（workdir `src/frontend`，经 WSL）: `wsl -d Ubuntu -- bash -lc 'cd ~/workspace/Lattix-codex/.worktree/cost-stats-minor-fixes/src/frontend && bun run build && bun run lint && bun run test'`
Expected: build 过（tsc + 契约 --check + vite）、lint 0 警告 0 错误、17/17 测试过

- [ ] **Step 5: Commit**

```bash
git add src/frontend/src/pages/Costs.tsx src/frontend/src/pages/costs-shared.tsx src/frontend/src/pages/EstimatedCostsTab.tsx
git commit -m "refactor(frontend): split cost stats pages and share tab helpers"
```

---

### Task 2: 后端 — estimated 月视图补 nil 断言

**Files:**
- Modify: `src/backend/internal/panel/cost_stats_test.go`

**Interfaces:**
- Consumes: 现有 `TestEstimatedBillingStatsHandlerMonthView`（无锚点、默认 custom 模式路径）

- [ ] **Step 1: 加断言（先跑确认当前通过，再改后跑）**

在 `TestEstimatedBillingStatsHandlerMonthView` 中：

1. 服务器遍历循环（expired 排除检查处）追加：

```go
	for _, srv := range dto.Servers {
		if srv.ServerID == expiredID {
			t.Fatal("expired billing server leaked into estimated stats")
		}
		if srv.EstimatedCostsCustom != nil {
			t.Fatal("estimated_costs_custom should be nil without anchors")
		}
	}
```

2. 在 `if dto.CustomAvailable { t.Fatal("custom_available should be false without anchors") }` 之后追加：

```go
	if dto.EstimatedTotalsCustom != nil {
		t.Fatal("estimated_totals_custom should be nil without anchors")
	}
```

- [ ] **Step 2: 运行后端测试**

Run: `wsl -d Ubuntu -- bash -lc 'cd ~/workspace/Lattix-codex/.worktree/cost-stats-minor-fixes && go test ./src/backend/...'`
Expected: 全部通过（含 `TestEstimatedBillingStatsHandlerMonthView`）

- [ ] **Step 3: Commit**

```bash
git add src/backend/internal/panel/cost_stats_test.go
git commit -m "test(panel): assert custom payloads absent without anchors in estimated view"
```

---

### Task 3: 新增 docs/CHANGELOG.md（breaking 改名 + 新端点）

**Files:**
- Create: `docs/CHANGELOG.md`

**Interfaces:**
- Produces: 仓库首个 changelog（keep-a-changelog 风格，Unreleased 节）

- [ ] **Step 1: 创建文件**

`docs/CHANGELOG.md`：

```markdown
# Changelog

本项目所有显著变更记录于此。格式基于
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循
[Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Breaking changes

- `GET /api/billing/stats`（成本统计）响应字段改名：`costs_public` →
  `actual_costs_public`、`costs_custom` → `actual_costs_custom`、
  `totals_public` → `actual_totals_public`、`totals_custom` →
  `actual_totals_custom`。外部 API 消费者需同步更新。

### Added

- 新增 `GET /api/billing/stats/estimated`：对启用统计计费且未过期的服务器，按
  日成本 × 周期天数（日 1 / 月 30 / 年 365）估算每周期成本。
- 成本统计页新增「计算成本」tab：估算日/月/年成本汇总卡片、周期分布图与明细矩阵；
  原统计口径更名为「已生效成本」tab。

### Fixed

- custom 换算模式下，无自定义锚点服务器的周期成本以 public 回退值计入 custom 合计，
  合计列 = 可见单元格之和。
```

- [ ] **Step 2: 检查渲染**

Run: `Get-Content docs/CHANGELOG.md`（确认无占位符、无乱码）
Expected: 与上一致

- [ ] **Step 3: Commit**

```bash
git add docs/CHANGELOG.md
git commit -m "docs: add changelog with breaking cost stats field rename"
```
