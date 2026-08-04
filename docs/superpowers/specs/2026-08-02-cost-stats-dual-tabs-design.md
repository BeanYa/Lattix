# 成本统计页双口径设计（已生效成本 / 计算成本）

日期：2026-08-02 · 状态：已批准（API 形态与字段命名已确认）

## 背景

成本统计页（`/costs`）现有实现按服务期与日历周期重叠摊算成本（见
`2026-08-02-cost-stats-design.md`，已实施）。现将其作为两种口径之一保留并更名，
另增一种"估算"口径：

- **已生效成本（actual）**：现有计算方式——按服务期 `[service_started_on, service_end)`
  与日历周期重叠，摊算已经花费的成本。后端字段相应改名。
- **计算成本（estimated）**：仅以"日成本 × 周期天数"估算每周期成本，忽略服务期与
  已生效部分；每台参与服务器在每个周期全额计入。

两种口径在页面上各占一个 tab。

## 参与对象

| 口径 | 参与范围 |
|---|---|
| actual（已生效） | `server_billing.enabled = true`（不限状态） |
| estimated（计算） | `server_billing.enabled = true` 且 `status != expired`（active / due_today / assumed_valid） |

estimated 估算的是持续持有成本，已过期服务器无意义，故排除。

## 计算规则

### 共享（两口径一致）

- 已生效口径周期折算天数：`day` → 1、`month` → 30、`year` → 365（复用 `intervalDays`）；
  计算成本口径年按 360 天（12 个月 × 30 天，见 estimated 节）
- 日均成本（统计币种最小单位，`big.Rat` 精确值）：`daily_rat = converted_minor / (interval_count × 折算天数)`；
  `converted_minor` 复用 `convertCosts` 对整周期金额换算（含自定义锚点桥接）
- 汇率口径：public（Frankfurter）与 custom 两套同时计算；`custom_available`
  = 存在至少一台服务器 `custom != nil` 且 `custom.AmountMinor != public.AmountMinor`；
  `rate_mode=custom` 且 `custom_available` 时返回 custom 一套

### actual（已生效成本，现有逻辑不变）

- 服务期 `[service_started_on, service_end)`（含起不含止），`service_end` 按计费状态
  决定（active/due_today → `next_renewal_on`；assumed_valid/expired →
  `max(next_renewal_on, assumed_valid_through + 1 天)`）
- 单元格：`round(daily_rat × 周期内重叠服务天数)`，每单元格独立舍入；
  视图内合计 = 单元格之和
- `days_active` = 范围内实际计费天数

### estimated（计算成本，实施评审后重构）

- **不按服务期裁剪**：每台参与服务器在每个周期全额计入（忽略已生效部分）
- 折算模型（统一以估算月成本为中间值，12 个月 × 30 天、年 = 360 天）：

  | 付费方式 | 年成本 | 月成本 | 日成本 |
  |---|---|---|---|
  | 年付 A（n 年） | A / n | 年成本 ÷ 12 | 年成本 ÷ 12 ÷ 30 = A / (n×360) |
  | 月付 M（n 月，季付 n=3） | 月成本 × 12 = M×12/n | M / n | M / (n×30) |
  | 日付 D（n 天） | 日成本 × 360 | 日成本 × 30 | D / n |

- 单元格：`round(daily_rat × 周期内计费天数)`，周期内计费天数 = 区间 `[from, to)`
  与周期重叠部分的折算天数（`estimateDays`：整月 30、整年 360、剩余按日计、
  跨月借位按 30 天）；完整月/年区间恒得 30/360
- 自定义周期总成本 = 年成本 × 整年数 + 月成本 × 整月数 + 日成本 × 剩余天数
  = `MonthlyCost × (12Y + Mo + D/30)`（Y 年 Mo 月 D 天）；12 个月视图精确还原年价
- `days_active` = 范围内总天数（恒等于 `daysBetween(from, to) + 1`，所有参与服务器相同）

## API 设计

两个独立端点（已确认），共享查询参数与校验。

### 端点 A：`GET /api/billing/stats`（已生效成本）

请求参数、跨度限制、响应结构不变，仅 DTO 字段改名：

- `billingServerStatsDTO`：`costs_public` → `actual_costs_public`，
  `costs_custom` → `actual_costs_custom`
- `billingStatsDTO`：`totals_public` → `actual_totals_public`，
  `totals_custom` → `actual_totals_custom`
- `daily_minor` / `daily_custom_minor` / `days_active` 保留（两口径共用概念）

### 端点 B：`GET /api/billing/stats/estimated`（计算成本，新增）

请求参数与校验同端点 A（`AllowedQuery: from, to, granularity, rate_mode`；
日粒度 ≤ 372 天；月/年 ≤ 3660 天）。

响应（周期标签、服务器排序同端点 A）：

```jsonc
{
  "reporting_currency": "CNY",
  "granularity": "month",
  "from": "2026-01-01",
  "to": "2026-12-31",
  "rate_mode": "custom",
  "rate_date": "2026-07-29",
  "custom_available": false,
  "periods": ["2026-01", "2026-02"],
  "servers": [{
    "server_id": 1,
    "alias": "US-LA-Direct",
    "country_code": "US",
    "location": "Los Angeles",
    "currency": "USD",
    "amount_minor": 1200,
    "interval_count": 1,
    "interval_unit": "month",
    "service_started_on": "2025-01-15",
    "status": "active",
    "days_active": 365,
    "daily_minor": 400,
    "estimated_costs_public": [12000, 12000]
  }],
  "estimated_totals_public": [24000, 24000]
}
```

- 服务器按 `server_id` 升序稳定排列
- `custom_available` 判定同端点 A；`rate_mode=custom` 且 `custom_available` 时追加
  `estimated_costs_custom`（每服务器）与 `estimated_totals_custom`，以及每服务器的
  `daily_custom_minor`
- `estimated_costs_public` 单元格 = `round(daily_rat × 周期天数)`；视图内合计 =
  单元格之和

### 实现位置

- `src/backend/internal/panel/cost_stats.go`：抽取公共核心（参与服务器装配 + 元数据 +
  `convertCosts` 两套日成本），按口径分支的单元格计算；新增 `estimatedBillingStatsDTO` /
  `estimatedBillingServerStatsDTO` 与 `handleEstimatedBillingStats`
- `panel.go` 注册 `GET /api/billing/stats/estimated`
- `docs/openapi.yaml`：`BillingStats` 系列字段改名，增补 `BillingStatsEstimated` 系列
  与 path（契约测试强制同步）
- 不新增 store 查询

## 前端页面

`src/pages/Costs.tsx` 重构为双 tab：

- 页面标题仍为「成本统计」；顶层 `Tabs`：「已生效成本」｜「计算成本」
  （「已生效成本」为默认 tab）
- 现有页面内容抽为 `ActualCostsTab` 组件：逻辑不变，字段随端点 A 改名
  （`costs_public` → `actual_costs_public` 等），标题/描述改为已生效口径
- 新增 `EstimatedCostsTab` 组件：
  1. 控制栏：粒度（日/月/年）、from/to + 快捷预设（本月/近 12 个月/近 3 年/全部）、
     换算方式切换（仅 `custom_available` 时显示）
  2. 汇总卡片：估算日成本（Σ 日成本，日成本按换算方式选择取
     `daily_custom_minor ?? daily_minor` 或 `daily_minor`，与现有表格一致）、
     估算月成本（Σ 日成本 × 30）、估算年成本（Σ 日成本 × 365）、启用计费服务器数
  3. 堆叠柱状图：X = 周期，每服务器固定色段；tooltip 逐服务器明细；日视图 dataZoom
  4. 成本占比环形图：范围内各服务器估算成本占比
  5. 服务器汇总表：国旗+别名、原价/周期（原币种）、估算日均成本、估算总成本、
     占比、状态徽标，可排序
  6. 周期 × 服务器明细矩阵：行 = 周期、列 = 服务器，行尾合计列
- 控件、图表、表格与 `ActualCostsTab` 共用同一套子组件/工具函数；两个 tab 各自独立
  请求与状态
- `types.ts`：现有 `BillingStats` / `BillingServerStats` 改名 `BillingActualStats` /
  `BillingActualServerStats`（字段 `actual_*`）；新增 `BillingEstimatedStats` /
  `BillingEstimatedServerStats`（字段 `estimated_*`）
- `api.ts`：`billingStats()` 保留（端点 A），新增 `billingStatsEstimated()`
- `bun run generate:api` 重新生成 `api-contract.generated.ts`

## 测试与验证

- `cost_stats_test.go`：现有测试更新字段名；新增 estimated 测试：
  - 参与范围：expired 排除、enabled 排除
  - 单元格 = `round(daily × 1/30/365)`；月/年周期天数恒定（不随日历月实际天数变化）
  - 服务期中途开通/已结束仍全额计入（不按服务期裁剪）
  - `custom_available` 判定与 custom 一套的返回
  - 视图合计 = 单元格之和；`days_active` = 范围天数
  - 参数校验复用（跨度超限等）
- 前端：`bun run build`（tsc + API 契约 --check）+ `oxlint` + `bun run test`

## 不做的事

- 不改变已生效成本的计算逻辑（仅更名与字段改名）
- estimated 不在前端计算（前端无货币换算逻辑，全部由后端返回）
- 不新增历史价格/成本快照表
- 不新增 e2e 脚本

## 实现状态（2026-08-02 已实施）

### 变更文件

- `src/backend/internal/panel/cost_stats.go`：共享装配核心 `loadStatsRows` / `statsRow` / `periodDays`；
  已生效端点 DTO 字段改名 `actual_*`；新增 `handleEstimatedBillingStats` 与 `estimatedBillingStatsDTO` 系列
- `src/backend/internal/panel/cost_stats_test.go`：改名同步 + estimated 端点测试
- `src/backend/internal/panel/panel.go`：注册 `GET /api/billing/stats/estimated`
- `docs/openapi.yaml`：`BillingActualStats` / `BillingActualServerStats`（改名）与
  `BillingEstimatedStats` / `BillingEstimatedServerStats`（新增）schemas + path
- `src/frontend/src/lib/types.ts` / `api.ts`：类型改名 + `billingStatsEstimated()` 客户端
- `src/frontend/src/lib/api-contract.generated.ts`：`bun run generate:api` 重新生成
- `src/frontend/src/pages/Costs.tsx`：双 tab（已生效成本 / 计算成本），共享
  `StatsControls` / `buildBarOption` / `buildDonutOption` / `useEarliestStart`

### 与设计一致/偏差记录

- 计算成本参与范围 = 启用计费且未过期（expired 排除）、单元格 = round(daily × 周期天数
  1/30/365)、days_active = 范围天数：按设计实现
- custom 模式合计（`*_totals_custom`）包含无锚点服务器的 public 回退单元格，合计列 =
  可见单元格之和（实施期评审发现并修复，两个端点同步）
- `convertCosts` 失败映射 502（Bad Gateway），其余装配错误 500：恢复原行为
- 图表配置经共享构建函数 `buildBarOption` / `buildDonutOption` 复用（评审要求，避免逐字重复）
- EstimatedCostsTab 汇总卡片为估算日/月/年成本（×1/×30/×360）与启用服务器数
- 前端两个 tab 条件渲染、各自独立请求；tab 切换时重新挂载并重新请求（设计如此）
- 计算成本折算模型实施后重构为「12 个月 × 30 天」口径（2026-08-04 评审修正）：
  - 年付：日成本 = 年付价 ÷ n ÷ 12 ÷ 30（原 365 天年）；月付/季付按周期数折算，
    月成本 = 实付 ÷ n、年成本 = 月成本 × 12；与用户侧公式
    「总成本 = MonthlyCost × (12Y + Mo + D/30)」一致
  - 单元格改按区间 [from, to) 与周期的重叠折算天数（整月 30、整年 360、剩余按日、
    跨月借位 30，`estimateDays`）：自定义范围总成本 = 年成本 × 整年 + 月成本 × 整月 +
    日成本 × 剩余天，2 年 4 个月 5 天 × 年付 300 = 704.17（用户示例精确成立）；
    12 个月视图精确还原年价（99 CNY/年 → 99.00），年粒度单元格 360 天
  - 已生效口径维持 365 天年与日历重叠摊算（保证服务期整段还原实付），两口径
    日成本并列返回；estimated DTO 新增 `monthly_minor` / `annual_minor` 及 custom
    变体供前端标注公式
  - 日粒度仍按真实日历天逐日计（整年视图 = 365/366 天），与月/年粒度（360 天年）
    存在 ≤1.4% 的展示差异，属该口径固有边界
- 估算汇总卡片按"Σ 日成本 × 周期天数（1/30/360）"计算；与矩阵单元格
  `round(daily × 周期天数)` 的舍入顺序不同，日成本带小数的服务器卡片与矩阵存在
  展示差异（已确认保持现状）

### 验证

- `go test ./src/backend/...`：全部通过（含 estimated 端点与 OpenAPI 契约测试）
- `bun run lint`：0 警告 0 错误
- `bun run test`：17 个前端测试通过
- `bun run build`：tsc + API 契约 --check + vite 全部通过
