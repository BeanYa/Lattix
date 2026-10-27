# 链路延迟与负载统计设计（链路一览 · 服务器对测速架构）

## 1. 目标

在周期遥测与链路流量统计之上，提供链路维度的两类统计，供链路一览视图消费：

- **段延迟**：链内相邻两跳之间物理路径的网络往返，按 **有向服务器对** 维护
  （A→B、A→C、B→A…），来源区分 **TCP**（复用链路监听端口，含端口号）与 **ICMP**；
- **负载**：每条链当日上/下行流量（原始与折算字节），以及全部链的当日合计。

数据由各 Agent 对其关联线路的下跳服务器测速上报；panel 维护延迟数据表并调度轮次。

本次范围：测速架构（目标计算、下发、执行、上报、存储）、链路一览统计 API 与前端展示。
不包含延迟历史趋势图、丢包率、按用户拆分，以及延迟告警。

## 2. 测速目标（服务器对模型）

测速目标按 **服务器** 维度推导，与链路解耦：agent 所在服务器 S 的目标集 = S 在全部
已发布链路中作为 **物理发起方** 的出向边去重后的 m 个目标服务器。

对链的每个相邻跳对（hop[i] → hop[i+1]，取已发布 revision 快照）：

| 段类型 | 物理发起方 | TCP 目标端口 | 说明 |
| --- | --- | --- | --- |
| direct | 上游 hop[i] | 下跳 forward 监听端口（末段=出口业务 realized 端口，NAT 受限机按 allowed_ports 换算公网侧） | dokodemo 透传的真实路径 |
| reverse / encrypted | 下游 hop[i+1]（bridge） | 上游 hop[i] 的 portal 监听端口（方向反转：B→A） | 反向段物理路径由下游发起 |
| hy2（入口终结末段） | — | 无（UDP/QUIC） | 仅 ICMP |

- **TCP 复用链路端口**：握手在对端 xray 监听立即完成，测得的是业务数据真实路径的
  网络往返；链路部署后端口必然在线，无需额外监听面。端口随测量数据入库。
- **ICMP**：每个出向段同时携带 icmp 目标，作为 TCP 不可用（如 hy2 段、端口未就绪）时的回退来源。
- **目标地址**：目标跳的设置公网地址（hop 地址引用 §9 可用则用之，否则跟随服务器
  默认地址；可能是域名也可能是 IP）。
- **去重**：同一 (from, to, kind, port) 多链共享只测一次——如 Case 3 中 A 测 A→B 与
  A→D，B 测 B→C 与 B→D；B→C 同时属于链 A→B→C 与链 B→C，去重后仅一条。

## 3. 调度与队列

- **触发**：① 面板调度器每 5 分钟一轮（`LATTIX_LATENCY_PROBE_INTERVAL` 可覆盖），
  启动即先测一轮；② 链路发布成功（`publishDesiredRevision`）立即追加触发全量一轮
  （链路变更的下一次测速任务进入队列）。
- **队列与原子性**：同一服务器同时仅一轮在途（panel 侧 per-server 在途登记，命令
  Accepted 前登记、回报/失败/死信/TTL 2 分钟放行），轮次依次执行。
- **单轮原子任务**：panel 下发 `latency.probe`（round_id + 全部目标），agent 对 m 个
  目标 **并行** 测速，单目标超时 TCP 5s / ICMP 3s，整轮硬截止 60s；全部完成/失败/
  超时即视为本轮完成。离线服务器本轮跳过（无补发语义）。
- Agent 侧另有进程内单飞守卫兜底（忙时回 Conflict，panel 下轮重发）；命令 handler
  立即回 Accepted 后异步执行，不阻塞命令队列中的其余命令。

## 4. 协议

- `latency.probe`（request）：`{round_id, timeout_ms, targets: [{server_id, address, kind, port}]}`
  → Accepted；
- `latency.report`（event，无需回执）：`{round_id, results: [{server_id, kind, port,
  latency_ms?}]}`；`latency_ms` 缺省 = 失败/超时样本（连通性参考，同 server
  `latency_ms` 约定）。panel 只接受在途轮次归属的回报（round_id 校验）。
- `ws-protocol.schema.json` 同步（$defs.latencyProbeTarget / latencyProbeResult）。
- 旧遥测字段 `chain_probes` 已移除（旧 Agent 上报被忽略）。

## 5. 存储（panel）

- `server_latency_probes`：有向服务器对最新值，主键 `(from_server, to_server, kind,
  port)`；`latency_ms REAL NULL` + `measured_at`；
- `server_latency_history`：每次采样一行，保留 24 小时，与主机指标历史同任务
  （`metrics.retention`，每小时）清理；两表随服务器级联删除清理（双向）；
- schema v23 → v24：按跳的旧探测表（chain_hop_probes / chain_hop_probe_history）
  整体废弃，迁移直接 DROP（24h 保留的遥测数据，无迁移价值）；
- **新鲜度窗口 15 分钟**（> 2 轮间隔）：窗口外样本视为失效，链路延迟如实降级。

## 6. API 与前端

`GET /api/chain/get-stats`：一次返回全量链路。每链 `segments[]` 为各段（上游跳
归属）的最新样本——同段含 tcp 与 icmp 两条来源行，前端取 **首选来源**（tcp 优先，
与业务路径同端口口径）：

```json
{
  "chains": [{
    "chain_id": 1,
    "latency_ms": 64.5,
    "segments": [{"hop_id": 3, "kind": "tcp", "target_address": "…",
                  "target_port": 9101, "latency_ms": 12.5, "sampled_at": "…"}],
    "today": {"raw_up": 0, "raw_down": 0, "effective_up": 150, "effective_down": 300}
  }],
  "today_up": 300, "today_down": 600
}
```

- 链路 `latency_ms` = 各段首选来源 RTT 之和；任一段缺新鲜样本时为 `null`；
- 段延迟展示/状态分级沿用 <100/100–300/>300ms（ok/warn/fail），箭头与标注同色；
- 前端 client：`api.chainStats()`；段匹配 `segmentForLink`（上游跳索引 + tcp 优先）。

## 7. 验证

- Agent：并行整轮语义（每目标一条结果、不可达样本 nil）、整轮截止、单飞守卫
  （`serverprobe_test.go`）；TCP 真拨本地监听。
- Store：最新值 upsert（端口入键）/新鲜度/保留清理/服务器级联、直连与反向链的
  边推导与去重（`server_latency_test.go`）。
- Panel：get-stats 的 tcp 首选、icmp 回退、过期降级、失败样本、今日合计
  （`chains_stats_test.go`）。
- 契约：`ws_schema_contract_test.go` 固化 latency.probe/report wire 形状。
