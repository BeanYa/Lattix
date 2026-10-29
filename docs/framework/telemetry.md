# 遥测

> 拆自[框架设计索引](../framework-design.md)，对应原 §13。主机指标探针细节见[服务器探针监控](../server-probe-monitoring-design.md)；链路/目录测速见[链路统计](../chain-link-stats-design.md)与[服务器测试](../server-testing-design.md)。

Agent 以 `telemetry` 消息周期上报（默认 60s，由面板 Agent 设置统一调整）：

- **xray 版本与运行状态**：升级管理（见[服务器维护](server-maintenance.md)）完成后据此刷新面板展示。
- **主机指标**：采集 CPU、1/5/15 分钟负载、内存、根文件系统、系统 uptime、默认路由出口网卡实时速率与开机累计量，以及 Agent→Panel WebSocket RTT。`server_metrics` 保存最新值，`server_metric_history` 保留最近 24 小时；服务器卡片展示核心摘要，详情抽屉展示完整信息和趋势。主机网卡流量与下述 Xray 业务流量完全独立。
- **流量统计（仅统计，不做强制配额）**：骨架配置启用 xray stats（inbound 级 + 用户级 policy），
  用户条目带 `level: 0`；Agent 经 gRPC StatsService 拉取包含 `xray_instance_id` 的进程级绝对计数器，
  Backend 持久化游标并计算增量。相同实例重连可补齐丢帧和控制通道离线期间的累计量，重复快照
  增量为零；实例变化按新计数器起点处理。增量写入 `traffic`（节点与用户维度）以及链路/逐跳统计。
  Agent 启动时确保 config.json 含 stats/policy 配置（缺失则落盘重启）。
  socks/http 的 accounts 无 email，仅覆盖节点维度。出口 service inbound 是链路权威流量；入口和
  中间 hop 分别用于对账和展示。离线补差的 Xray 重启丢失窗口、日桶归属及倍率边界见
  [链路 Revision 与流量统计设计](../chain-revisions-traffic-design.md#控制通道离线时的补报)。

在线/离线由 WS 连接推导，连接存亡由 Agent 主动心跳判定（30s Ping，90s 无流量判死，见[架构总览](../framework-design.md)）。
