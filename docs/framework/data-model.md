# 数据模型（SQLite）

> 拆自[框架设计索引](../framework-design.md)，对应原 §4。各表完整契约以所属专题设计文档为准，本表是全库清单与索引。

| 表 | 字段（要点） |
|---|---|
| `servers` | id, alias, country_code(ISO 3166-1 alpha-2), location(城市/机房位置), address(公网地址), learned_addr(拨入学习地址), nic_addresses(agent 上报网卡地址 JSON), tags(有序标签 JSON，名称模板 `TAG[n]` 来源), token(长期凭证), last_seen_at, xray_version, config_drift(见[服务器维护](server-maintenance.md)), server_settings_revision/error/reported_at(见[控制通道](control-channel.md) server settings 同步链), created_at |
| `users` | id, name, uuid, sub_token, expires_at(可空，unix 秒，NULL=长期), expired(0/1 到期停权标记，见[订阅](subscription.md)), disabled(0/1 显式停用标记，见[用户与分组](users-and-groups.md)), effective_at(可空，unix 秒，NULL=创建即生效), started_at(可空，unix 秒，激活扇出标记：sweeper 越过生效时刻后置位), is_trial(0/1 试用标记), quota_exhausted(0/1 流量配额停权标记，见[订阅](subscription.md)), created_at |
| `nodes` | id, name(解析后的管理/订阅名称), server_id, protocol, port, config_template(JSON), realized_config(JSON), status, error, created_at |
| `commands` | id, request_id, trace_id, server_id, type, data(JSON), status(queued/sent/acked/failed/abandoned), error, attempts, created_at, updated_at |
| `user_nodes` | user_id, node_id（[逐链路用户分配](users-and-groups.md)，默认全关） |
| `shared_endpoints` | server_id, protocol, port, profile_hash, config_template/realized_config, status；兼容链共享一个 VLESS+REALITY 监听 |
| `user_chain_assignments` | user_id, chain_id, access_uuid；真实用户与链的直接多对多分配 |
| `server_metrics` | server_id, load1/load5/load15, cpu_percent, mem/disk 用量、默认出口网卡速率/累计量、uptime、latency_ms、updated_at（[遥测](telemetry.md)主机遥测最新值） |
| `server_metric_history` | 与 `server_metrics` 同口径的 24 小时时序样本；按服务器与采样时间索引（[服务器探针监控](../server-probe-monitoring-design.md)） |
| `providers` | id, name（大小写不敏感唯一）, website_url, created_at, updated_at |
| `server_billing` | server_id, enabled, provider_id, amount_minor（原币种最小单位整数）, currency（ISO 4217）, service_started_on, interval_count + interval_unit, next_renewal_on, status, assumed_valid_through, last_inspected_on |
| `server_traffic_plans` | server_id, quota_bytes（NULL=无限）, accounting_mode（outbound/bidirectional/max）, reset_anchor_on, reset_count + reset_unit, tracking_started_on |
| `server_network_usage_daily` | server_id, usage_date, tx_bytes, rx_bytes（主机网卡累计计数器按日增量） |
| `exchange_rates` | base_currency, quote_currency, rate（十进制字符串）, rate_date, source, fetched_at（Frankfurter 持久化缓存） |
| `custom_exchange_rates` | source_currency（唯一）+ source_amount, target_currency（保存时的展示币种）+ target_amount, enabled；至少一侧金额为 1，每个目标币种仅一个启用锚点 |
| `traffic` | node_id, user_uuid, up, down, updated_at（[遥测](telemetry.md)流量累计：节点维度 user_uuid=''，用户维度 node_id=0） |
| `traffic_history` | 用户流量周期归档（流量重置/配额恢复留痕，见[订阅](subscription.md)） |
| `traffic_stats_bucket` / `traffic_stats_daily` | 流量统计时序桶（控制台/运行监控流量图表，`/api/stats/traffic/*`，契约见 [OpenAPI](../openapi.yaml)） |
| `chains` | 稳定 chain/service/endpoint identity、published/desired revision、倍率、发布状态与软删除时间 |
| `chain_hops` / `chain_hop_identities` | 当前期望工作拓扑与不会复用的稳定 hop identity |
| `chain_revisions` / `chain_revision_tasks` | 不可变拓扑快照及 apply/cleanup 任务状态机；任务关联离线命令队列 |
| `traffic_cursors` | Agent Xray 实例绝对计数器游标，用于幂等补差 |
| `chain_traffic_totals` / `chain_traffic_daily` | 链与逐跳 raw/effective 累计、倍率余数及按 revision/统计时区的日桶 |
| `chain_traffic_baselines` / `chain_multiplier_events` | 流量重置 checkpoint 与倍率分段审计 |
| `endpoint_traffic_totals` | 共享入口 inbound 的运维流量累计；用户/链权威流量另按 assignment identity 入账 |
| `server_latency_probes` / `server_latency_history` | 链路测速有向服务器对延迟（panel 调度 latency.probe 轮次）与历史，见[链路统计设计](../chain-link-stats-design.md) |
| `server_catalog_probes` / `server_catalog_probe_history` | 三网目录测速（省市 × 电信/联通/移动 TCP 握手 RTT），见[服务器测试设计](../server-testing-design.md) |
| `server_test_tasks` / `server_test_result_chunks` | 服务器测试原子任务与分块结果，见[服务器测试设计](../server-testing-design.md) |
| `server_ban_watch` | Ban 观察状态机快照（normal/suspected/banned/recovering），见[服务器维护](server-maintenance.md) |
| `link_groups` / `link_group_chains` / `link_group_external_subscriptions` | 链路分组及其成员（共享入口链路多条编排、外部订阅整体原子参与，见[用户与分组](users-and-groups.md)） |
| `user_groups` / `user_group_members` / `user_group_links` | 用户分组、成员及「用户组 × 链路分组」关联（组内用户订阅由分组派生） |
| `external_chains` / `external_subscriptions` | 外部订阅导入的节点与订阅记录（不进入 chains 状态机与流量统计，见[订阅](subscription.md) 外部订阅一节） |
| `user_external_subscriptions` | 用户 × 外部订阅关联与合并模式（stack/merge/nodes） |
| `subscription_templates` / `subscription_template_rules` / `subscription_files` / `subscription_rule_files` | 订阅模板、规则与制品缓存，见[订阅分流与模板](../subscription-routing-design.md) |
| `subscription_snapshots` / `published_subscription_snapshots` / `user_subscription_profiles` | 订阅快照、已发布版本与用户级模板配置，见[订阅分流与模板](../subscription-routing-design.md) |
| `rpc_idempotency` | 面板写 RPC 幂等预留；同键重试时悬空预留被接管自愈（v0.1.17） |
| `settings` | 面板设置键值（告警通道、TLS、会话密钥等，见[安全](security.md)与[服务器维护](server-maintenance.md)） |

说明：

- `commands` 表充当**离线命令队列**并保留命令生命周期；面板操作日志与 API 请求日志
  使用独立存储、容量和查询接口，详见 [日志系统设计](../logging-design.md)。
- `nodes.config_template` 是面板侧虚拟配置（含占位符）；`nodes.realized_config` 是 Agent 上报的实际生效值（端口、public_key、short_id 等）。
- `servers.country_code` 与 `servers.location` 在创建/编辑服务器时必填；国家通过选择器写入标准两位代码，`COUNTRY` 与 `COUNTRY_FLAG` 由代码派生。Location 提供按国家过滤的本地城市建议，同时允许管理员填写机房区域等自由文本。
- `servers.address` 是订阅中节点地址的唯一来源（见[订阅](subscription.md)）：**创建服务器时由管理员填写公网地址，agent 不校验**；留空则按 agent WS 拨入的对端 IP 自动学习（可信反代——回环、内建内网/容器网段或 `trusted_proxies` 追加网段——时从 `X-Forwarded-For` 右向左取第一个非可信 IP，公网对端直连不信任该头以防伪造），一经写入不再被覆盖（地址变更由管理员通过 `POST /api/server/update` 修改）。每次 `agent.session.open` 另将拨入学习地址写入 `learned_addr`、将 agent 上报的网卡非回环地址写入 `nic_addresses`，二者仅作面板"编辑地址"的内置候选（可选内置地址或自定义），不参与自动学习决策。`servers` 同时保存机器类型与 NAT 可用端口段元数据（含非 1:1 映射的 public_port），NAT 类型 address 强制必填、禁用自动学习；引入链后订阅地址改取链**入口**的 address。
- 用户-节点关联：全新数据库中的新建用户和节点默认不关联，由管理员显式分配；结构迁移不会隐式补全或改变现有关联。
- 服务器费用、生命周期状态机、流量额度、汇率换算及后端公共定时任务器的完整契约见[后端调度与服务器计费设计](../billing-scheduler-design.md)。费用原价和币种分列保存；关闭“统计计费”仅排除费用逻辑和状态展示，不删除资料。流量计划始终独立生效，默认无限额度。
