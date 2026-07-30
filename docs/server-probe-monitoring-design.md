# 服务器探针监控设计

## 1. 目标

在现有 Agent 周期遥测基础上补全服务器探针能力，并将服务器管理页从表格改为监控卡片网格。

本次范围：

- CPU 使用率与 1/5/15 分钟负载；
- 内存、根文件系统 `/` 使用量；
- 默认路由出口网卡的实时上下行速率与开机以来累计字节；
- 系统运行时间；
- Agent 到 Panel 的 WebSocket 往返延迟；
- 最新指标、24 小时历史、最近 30 个样本趋势；
- 服务器卡片网格与右侧监控详情抽屉。

不包含流量配额、月费、丢包率、指标告警和旧 Agent 协议兼容。主机网卡流量与现有 Xray 节点/用户业务流量保持独立。

## 2. 心跳与延迟

Agent 是唯一的 Ping 发起方，但连接保活与延迟测量使用不同类型的控制帧。每条已鉴权连接先发送一次独立的 liveness Ping，再通过 `agent.session.ready` 完成会话就绪；之后 liveness 在随机错峰后每 30 秒发送。收到任何数据帧或控制帧都会续期读期限，连续 90 秒没有收到任何字节才判定连接失效并进入重连流程。

latency Ping 仅在 Panel 生命周期为 `active` 时启用：进入或恢复 `active` 后在 0–30 秒内随机错峰，随后每 30 秒采样一次，最近三次有效 RTT 的中位数作为当前延迟。单次 Pong 超过 10 秒视为超时，当前延迟置空直到下一次有效 Pong，但不会关闭 WebSocket。进入 `startup`、`updating` 或 `faulted` 时清理待完成探针但保留已有样本；恢复后继续向原窗口追加。遥测不等待延迟样本；尚无有效样本、探测超时或探针暂停时上报空值，并通过 `latency_probe_active` 区分“已接受但无结果”和“生命周期暂停”。

latency Ping 载荷使用类型字节和单调递增的 64 位序号。Agent 本地保存序号与发送时刻；Pong 只负责回显，Panel 与 Agent 不比较系统时钟。liveness 使用独立类型字节，因此不会进入延迟样本。

## 3. Agent 采集

Agent 默认每 60 秒生成一帧主机遥测：

| 指标 | Linux 数据源 | 说明 |
| --- | --- | --- |
| CPU | `/proc/stat` | 两次采样差值；首次为不可用 |
| 负载 | `/proc/loadavg` | load1/load5/load15 |
| 内存 | `/proc/meminfo` | `MemTotal - MemAvailable` |
| 磁盘 | `statfs("/")` | 根文件系统总量与已用量 |
| 网卡 | 默认路由 + `/sys/class/net/*/statistics` | TX=上传，RX=下载 |
| 速率 | 网卡计数器差值 / 单调时间差 | 首次或接口切换时不可用 |
| 累计流量 | 网卡原始计数器 | 开机以来累计；重启后自然归零 |
| 运行时间 | `/proc/uptime` | 系统 uptime |
| 延迟 | WebSocket Ping/Pong | 最近三次 RTT 中位数 |

IPv4 默认路由优先使用 `/proc/net/route`，没有可用 IPv4 默认路由时读取 `/proc/net/ipv6_route`。忽略回环、Docker、veth、TUN 等虚拟接口；默认路由切换后重新建立速率基线，并上报当前接口名。

各指标独立降级：单项采集失败不阻断其他遥测。无法计算的区间值使用空值，不能伪装为 `0`。

## 4. 协议

`telemetry.report` 保留 Xray 信息和业务流量增量，并扩展主机指标：

- `load1/load5/load15`
- `cpu_percent`（可空）
- `mem_total/mem_used`
- `disk_total/disk_used`
- `network_interface`
- `network_tx_bytes/network_rx_bytes`
- `network_tx_bps/network_rx_bps`（可空）
- `uptime_seconds`
- `latency_ms`（可空）
- `latency_probe_active`（可空布尔；缺省表示旧 Agent，Panel 按启用处理）

Panel 使用收到遥测的服务端时间作为采样时间，避免 Agent 系统时钟偏差。

## 5. 存储与保留

`server_metrics` 每台服务器保留最新值，供服务器列表快速读取。`server_metric_history` 保存时间序列，并额外记录 `latency_probe_active`、`server_id`、`sampled_at`。Panel 结合自身当前生命周期和 Agent 标记确定该字段，任一方暂停即为 false，因此旧 Agent 在 Panel 更新期间也不会误入队列。最近延迟队列只排除 `latency_probe_active=false` 的生命周期暂停包；启用状态下 `latency_ms=null` 的超时包仍保留为连通性参考。

每帧主机遥测在同一事务内更新最新值并插入历史。历史表按 `(server_id, sampled_at)` 建索引，后台每小时清理 24 小时以前的数据。删除服务器时同步删除最新指标与历史指标。

## 6. API

- `GET /api/server/list`：继续返回服务器及最新指标；
- `GET /api/server/list-metric-samples?limit=30`：批量返回所有服务器最近样本，用于卡片趋势；
- `GET /api/server/get-metric-history?server_id=<id>&hours=24`：返回单台服务器的历史，用于详情抽屉。

列表保持现有快速轮询以反映在线与命令状态；趋势接口默认每 60 秒刷新。卡片趋势使用批量接口，避免每台服务器单独请求。

## 7. 前端

服务器页改为响应式卡片网格：

- 超宽屏四列，宽屏三列，中屏两列，窄屏一列；
- 卡片顶部显示在线状态、名称、地区、异常徽标和更多操作菜单；
- 主体只显示 CPU/负载、内存、磁盘、实时上下行速率和延迟；
- 底部显示最近 30 个已接受延迟探测包的迷你趋势，超时显示空槽，生命周期暂停包不入列；
- 点击卡片打开右侧抽屉，移动端抽屉全屏；
- 抽屉展示默认网卡、累计收发量、运行时间、最近采样时间、Agent/Xray
  版本、地址等基础信息，以及 24 小时 CPU、内存、磁盘、网络与延迟趋势。

管理动作统一收纳到更多菜单；配置漂移与设置待同步保持醒目标识。离线卡片降低视觉强度，保留最后一次指标并明确标注采样时间。

固定健康阈值：

- CPU、内存、磁盘：`<80%` 正常，`80–90%` 警告，`>90%` 严重；
- 延迟：`<100ms` 正常，`100–300ms` 警告，`>300ms` 严重；
- 网络速率与累计流量不评判健康。

## 8. 验证

- Agent 单元测试覆盖 CPU、负载、默认路由、网卡差值、磁盘、uptime、RTT 中位数，以及延迟暂停后保留样本并忽略迟到 Pong；
- WebSocket 测试覆盖首次 liveness、`agent.session.ready`、周期保活和 90 秒连接失效；生命周期测试覆盖仅在 `active` 采样、恢复错峰，以及 latency 超时不关闭连接；
- Store 测试覆盖最新值 upsert、历史排序/限制、24 小时清理；
- API 测试覆盖参数校验、批量最近样本和单服务器 24 小时历史；
- 前端执行类型检查、构建、桌面与移动端浏览器验证，并检查卡片、菜单、抽屉、离线和无数据状态。
