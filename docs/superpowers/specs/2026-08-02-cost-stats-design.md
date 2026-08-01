# 成本统计页面设计

日期：2026-08-02 · 状态：已批准（分区确认：计算规则 / API / 前端 / 测试）

## 背景

服务器计费基础设施已存在（`server_billing` 表、统计计费开关、`convertCosts` 汇率换算、
统计币种设置），缺的是成本统计页面：对启用统计计费的服务器按日/月/年周期出具图表，
展示每周期每台服务器的成本，并按统计币种统一换算。

## 计算规则（后端）

### 参与对象

仅 `server_billing.enabled = true` 的服务器参与统计；关闭"统计计费"的服务器完全排除。

### 服务期（计费起止）

计费日期集合为 `[service_started_on, service_end)`（含起、不含止，统一排除边界避免
跨周期重复计数）。`service_end` 按计费状态决定：

| 状态 | service_end |
|---|---|
| `active` / `due_today` | `next_renewal_on`（已付费到续费日；续费日属于下一周期） |
| `assumed_valid`（已逾期但在线） | `max(next_renewal_on, assumed_valid_through + 1 天)` |
| `expired`（已逾期且离线） | `max(next_renewal_on, assumed_valid_through + 1 天)` |

`assumed_valid_through` 语义为"推定有效至当天"（含当天），故截止日 +1 天转为排他边界；
为空时回退 `next_renewal_on`。统计范围不受"今天"限制：范围终点即权威边界
（"从开通日计算到所选范围结束"）；已过期服务器在截止日后自然无成本。

### 折算模型（按日成本摊算）

- 周期折算天数：`day` → 1、`month` → 30、`year` → 365
- 日均成本（统计币种最小单位，`big.Rat` 精确值）：
  `daily_rat = converted_minor / (interval_count × 折算天数)`
  `converted_minor` 由现有 `convertCosts` 对整周期金额换算得出（复用 big.Rat 舍入、零小数
  币种、自定义锚点桥接全部现有逻辑）
- **每单元格独立舍入**：`cell_minor = round(daily_rat × 周期内服务天数)`（大整数四舍五入）
- 视图内合计 = 该视图单元格之和（精确）

**视图间不承诺数值一致**：日/月/年三视图各自按上述公式独立计算。月视图合计与年视图的
差异来自 30 天月模型（12 × 30 = 360 ≠ 365）与逐单元格舍入噪声（每单元格 ≤ 0.5 最小单位）。
对账锚点是汇总表中的"原价/周期"（原币种），页面文案说明该口径。

### 汇率口径

后端对每台服务器同时计算 public（Frankfurter）与 custom（自定义锚点）两套换算
（复用 `convertCosts` 的两个返回值）。响应级 `custom_available` 判定：
**存在至少一台服务器 `custom != nil` 且 `custom.AmountMinor != public.AmountMinor`**。

- `custom_available = true`：响应携带两套单元格；前端展示"公共汇率 / 自定义锚点"切换
  （默认自定义）
- `custom_available = false`：只携带 public 一套；前端仅显示公共汇率

## API 设计

**新增**：`GET /api/billing/stats`（`panel.go` 注册；只读、需登录；
`AllowedQuery: from, to, granularity, rate_mode`）

### 请求参数

| 参数 | 必填 | 校验 |
|---|---|---|
| `from` / `to` | 是 | `YYYY-MM-DD` 合法；`from ≤ to` |
| `granularity` | 是 | `day \| month \| year` |
| `rate_mode` | 否 | `public \| custom`，默认 `custom` |

跨度限制：日粒度 ≤ 372 天；月/年粒度 ≤ 10 年（3660 天）。

### 响应

周期标签：日 `YYYY-MM-DD`、月 `YYYY-MM`、年 `YYYY`；`periods` 覆盖范围内所有日历周期
（含无服务天数的零成本周期）。

```jsonc
{
  "reporting_currency": "CNY",
  "granularity": "month",
  "from": "2025-08-01",
  "to": "2026-07-31",
  "rate_mode": "custom",
  "rate_date": "2026-07-29",
  "custom_available": false,
  "periods": ["2025-08", "2025-09"],
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
    "days_active": 200,
    "daily_minor": 400,
    "costs_public": [12345, 12345]
  }],
  "totals_public": [24690, 24690]
}
```

- 服务器按 `server_id` 升序稳定排列（前端按此顺序分配稳定色板）
- `custom_available` 恒照常判定（数据属性，与请求模式无关）；返回内容由 `rate_mode`
  决定：`public` 只返回 public 一套；`custom` 且 `custom_available` 时追加 `costs_custom`
  （每服务器）与 `totals_custom`，以及每服务器的 `daily_custom_minor`；`custom` 且
  `custom_available = false` 时与 public 相同，只返回一套
- `days_active` = 范围内实际计费天数；`daily_minor` = `round(daily_rat)`

### 实现位置

- 新增 `src/backend/internal/panel/cost_stats.go`：处理器 + 周期生成 + 服务期计算
- `panel.go` 注册路由；`docs/openapi.yaml` 增补 path/schemas
- 不新增 store 查询：复用 `ServerBillingMap`、服务器列表与 `convertCosts`

## 前端页面

- 路由 `/costs`，懒加载页面 `src/pages/Costs.tsx`；导航项"成本统计"（`CoinsIcon`）置于
  "服务器"之后（`Layout.tsx`、`App.tsx`）
- 页面结构：
  1. 控制栏：粒度切换（日/月/年）、时间范围（from/to + 快捷预设：本月 / 近 12 个月 /
     近 3 年 / 全部，其中"全部"= 最早开通日（无启用服务器时为今天）→ 今天）、
     换算方式切换（仅 `custom_available` 时显示，默认自定义）、
     统计币种徽标（只读）、汇率日期
  2. 汇总卡片：范围内总成本、启用计费服务器数、平均月成本、成本最高服务器
  3. 堆叠柱状图（ECharts）：X = 周期，每服务器固定色段（按响应顺序分配稳定色板）；
     tooltip 逐服务器明细；日视图 dataZoom 滑块；图例可开关单台服务器
  4. 成本占比环形图（ECharts）：范围内各服务器占比
  5. 服务器汇总表：国旗+别名、原价/周期（原币种）、服务天数、日均成本、总成本、占比、
     计费状态徽标（复用现有状态标签），可排序
  6. 周期 × 服务器明细矩阵：行 = 周期、列 = 服务器，横向滚动，行尾合计列
- ECharts 按需引入（`echarts/core` + BarChart/PieChart + Grid/Tooltip/Legend/DataZoom +
  CanvasRenderer），随页面懒加载进独立 chunk；新建 `src/components/echarts.tsx` 小封装
  （init / resize / 主题销毁），轴与文本颜色跟随浅色/深色主题
- 金额显示复用现有 divisor 规则（JPY/KRW/ISK → 1，其余 → 100）；前端只渲染后端返回的
  数值，不含任何货币换算逻辑
- `types.ts` 增补 `BillingStats` / `BillingServerStats`；`api.ts` 增补 `billingStats()`
- `bun run generate:api` 重新生成 `api-contract.generated.ts`（构建 `--check` 强制同步）

## 测试与验证

- `cost_stats_test.go`（沿用现有 panel 测试 store 构造，如 `exchange_test.go`）：
  - 周期生成：日/月/年边界、闰年（2024-02）、跨年
  - 服务期规则：四种状态各自的截止日、空 `assumed_valid_through` 回退、排他边界
  - 单元格计算：`round(daily_rat × 天数)`、零金额、周期内部分天数（开通日/截止日截断）
  - 视图合计 = 单元格之和；`custom_available` 判定（含 custom == public 时不生效）
  - 参数校验：非法日期、from > to、粒度非法、跨度超限
  - Handler 层：计费禁用服务器被排除、0 成本周期保留
- 前端：`bun run build`（tsc + API 契约 --check + chunk 检查）+ `oxlint`
- e2e：计费功能目前无 e2e 脚本，成本统计沿用该先例不新增脚本（后续可补）

## 不做的事

- 不新增历史价格/成本快照表：历史周期按当前价格摊算（已确认）
- 不做"已支付 vs 已发生"口径切换：范围终点即权威边界
- 不重写汇率换算逻辑：全部复用 `convertCosts`
- 不新增 e2e 脚本

## 实现状态（2026-08-02 已实施）

### 变更文件

- `src/backend/internal/panel/cost_stats.go`（新增）：周期生成、服务期计算、单元格摊算、
  `handleBillingStats` 处理器、`reportingCurrency` 助手
- `src/backend/internal/panel/cost_stats_test.go`（新增）：周期/服务期/折算/校验/Handler 测试
- `src/backend/internal/panel/panel.go`：注册 `GET /api/billing/stats`
- `docs/openapi.yaml`：增补 path 与 `BillingStats` / `BillingServerStats` /
  `BillingStatsGranularity` / `BillingStatsRateMode` schemas（契约测试强制同步）
- `src/frontend/package.json` / `bun.lock`：新增 `echarts@6.1.0`
- `src/frontend/src/lib/api-contract.generated.ts`：`bun run generate:api` 重新生成
- `src/frontend/src/lib/types.ts` / `api.ts`：`BillingStats` 系列类型与 `billingStats()` 客户端
- `src/frontend/src/components/echarts.tsx`（新增）：按需引入的 Chart 封装
  （init / ResizeObserver / dispose / notMerge 更新）
- `src/frontend/src/pages/Costs.tsx`（新增）：控制栏（粒度/日期范围/快捷预设/换算方式）、
  汇总卡片、堆叠柱状图、占比环形图、服务器汇总表（可排序）、周期 × 服务器明细矩阵
- `src/frontend/src/App.tsx` / `components/Layout.tsx`：路由 `/costs` 与导航项（服务器之后）

### 与设计一致/偏差记录

- 单元格级舍入、视图内合计精确、视图间独立：按设计实现
- 年付服务器的年视图单元格 = `round(amount/365 × 365)` 精确还原年价（非逐日舍入累加）
- 跨 372 天日视图禁用；月/年 ≤ 3660 天；超出时前端自动钳制起始日期
- "全部"预设的前端需要 `api.servers()` 计算最早开通日（后端无此接口）
- ECharts 懒加载进独立 chunk（约 194KB gzip），与既有 GlobeTopology（525KB gzip）同量级
- 换算方式切换控件仅在 `custom_available` 时展示（含存在差异的判定），默认自定义锚点

### 验证

- `go test ./src/backend/...`：全部通过（含新 Handler/单元测试与 OpenAPI 契约测试）
- `bun run lint`：0 警告 0 错误
- `bun run test`：17 个前端测试通过
- `bun run build`：tsc + API 契约 --check + vite + chunk 循环检查全部通过
