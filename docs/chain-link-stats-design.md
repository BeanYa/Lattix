# 链路延迟与负载统计设计（链路一览）

## 1. 目标

在现有周期遥测与链路流量统计之上，补齐链路维度的两类统计，供链路一览视图消费：

- **段延迟**：链内相邻两跳之间的 TCP 握手往返（RTT），按段采样、按链聚合；
- **负载**：每条链当日上/下行流量（原始与折算字节），以及全部链的当日合计。

本次范围：Agent 段探测与遥测上报、Panel 落库与保留、全量链路统计 API、前端 API client。
不包含链路延迟历史趋势图、丢包率、按用户/按跳的延迟拆分，以及前端页面改动
（视觉重构另行进行，仅消费本 API）。

## 2. 段探测（Agent）

每个持有链跳配置件的 Agent，在每次遥测采样前对本机的**出向段**做一轮 TCP 握手测速：

- **forward 直连段**：拨 dokodemo inbound 的透传目标（`settings.address/port`，即下一跳
  forward 监听端口）。经 reverse portal 的 forward（路由 outboundTag 为 `portal_*`）不探测——
  与同跳 bridge 段同物理路径，避免重复计量；
- **bridge 反向段**：拨 interconn outbound 的 `settings.vnext[0]`（上游 portal 监听端口）；
- **hy2 段**（forward 带 hysteria2 outbound，入口终结末段）：UDP/QUIC 无 TCP 握手可测，不探测；
- **portal piece**：为反向段被拨侧，无出向探测。

握手在对端 xray 监听处立即完成，RTT 即段网络往返；连接建立即关闭，不发送业务数据。
单段超时 5 秒，超时记为**失败样本**（`latency_ms` 缺省，保留作连通性参考，同 server
`latency_ms` 约定）。探测目标从落盘 ChainPiece 的自有渲染配置推导（§21.1），存量 piece
无需重新下发即可获得段延迟。探测随遥测帧上报（`chain_probes`，逐段一条，同跳可含
forward 与 bridge 两条），遥测间隔即可探测间隔，不引入新的周期配置。

每个物理段恰好由一侧 Agent 探测一次：直连段由上游探测，反向段由下游 bridge 探测。
单跳链无段，不上报。

## 3. 协议

`telemetry.report` 事件数据新增可选字段 `chain_probes[]`（`ws-protocol.schema.json` 已同步，
`$defs.chainProbeStat`；旧 Agent 缺省该字段，Panel 忽略）：

- `hop_id`（执行探测的本跳）、`kind`（`forward|bridge`）、`target_address`、`target_port` 必填；
- `latency_ms` 可缺省：失败/超时样本。

## 4. 存储（Panel）

- `chain_hop_probes`：每段最新值，主键 `(hop_id, kind)`（同跳最多两条出向段）；
  `latency_ms REAL NULL`，`sampled_at` 为 Panel 服务端时间；
- `chain_hop_probe_history`：每次采样一行，保留 24 小时，与主机指标历史同任务
  （`metrics.retention`，每小时）清理；
- 删除链路时在 `DeleteChain` 事务内随 `chain_hops` 删除两表的段行（历史行由保留窗口兜底）；
- schema v22 → v23（新表随 `CREATE TABLE IF NOT EXISTS` 启动建表，无数据迁移）。

新鲜度窗口 **10 分钟**：读取段统计时，`sampled_at` 超窗的最新值视为失效（Agent 离线、
拓扑变更后未再刷新的旧段自动退出聚合，链路延迟如实降级为不完整）。

## 5. API

`GET /api/chain/get-stats`（登录，链路一览轮询口径，一次取全量链路）：

```json
{
  "chains": [
    {
      "chain_id": 1,
      "latency_ms": 64.5,
      "segments": [
        {"hop_id": 3, "kind": "forward", "target_address": "203.0.113.2",
         "target_port": 9101, "latency_ms": 12.5, "sampled_at": "..."}
      ],
      "today": {"raw_up": 0, "raw_down": 0, "effective_up": 150, "effective_down": 300}
    }
  ],
  "today_up": 300,
  "today_down": 600
}
```

- `latency_ms`：各段 RTT 之和；段数不足（任一段缺新鲜样本）或单跳链为 `null`；
- `segments`：新鲜窗口内的段明细（含失败样本），按跳 seq 排列；段→跳的映射由
  `hop_id` + 目标地址/端口表达，前端可据此标注上下游；
- `today`：`chain_traffic_daily` 当日桶（流量统计时区口径，`hop_id=0` 链级），含原始
  与折算字节；无数据缺省；
- `today_up/today_down`：各链折算口径合计。

前端 client：`api.chainStats()`（类型 `ChainStatsResponse`，lib/types.ts）。

## 6. 验证

- Agent：目标推导（直连/反向/hy2/portal 各形态）、本地拨测成功与失败样本、
  遥测帧携带与缺省（`chainprobe_test.go`、`telemetry_test.go`）；
- Store：最新值 upsert 与历史保留、新鲜度过滤、保留清理、删链级联、
  日桶按链/跳合并（`chain_probes_test.go`）；
- Panel：get-stats 段聚合、不完整链 null、失败样本保留、今日合计（`chains_stats_test.go`）；
- 契约：`ws_schema_contract_test.go` 固化 `chainProbeStat` wire 形状；
  OpenAPI 路由契约测试覆盖新路由；前端 `generate:api` 产物同步提交。

## 7. 已知边界

- hy2 入口终结末段无延迟样本：该链段数不完整，`latency_ms` 为 `null`（段列表仍展示其余段）；
- 存量链路的段延迟**即刻可用**（目标从落盘配置推导），仅反向链 bridge 段依赖 bridge piece
  已就绪；hop 重编（编辑换服务器）后旧 hop 行随删链/新鲜度窗口自然淘汰；
- 探测在遥测采样线程内串行执行，单段 5 秒超时上限；段数极少（每跳 ≤2），不阻塞遥测发送节奏。
