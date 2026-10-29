# 服务器维护：漂移、升级、告警与备份

> 拆自[框架设计索引](../framework-design.md)，对应原 §17（配置漂移 reconcile）、§18（xray 版本升级管理）、§19（事件告警与备份）。

## 配置漂移 reconcile

检测上报 + 管理员修复：

- Agent 每次落盘记录配置哈希，按面板 Agent 设置中的间隔周期比对（默认 15s）；
  外部篡改/删除即上报 `config.drift`（仅状态变化时），回滚路径同步基线避免误报。
- 面板置 `servers.config_drift` 标志，服务器列表显示"配置漂移"徽章与"修复漂移"按钮。
- 修复 = `POST /api/server/repair` 重放该服务器全部 active 节点；
  Agent 检出漂移后的下一次落盘以"骨架 + 受管节点 inbound"净化配置为基，
  外部对非节点段（log/routing 等）的改动一并还原，漂移标志自动清除。
- 基线说明：agent 停机期间的外部改动无法区分，以启动时文件为基线。

## xray 版本升级管理

面板服务器列表"升级 xray"（`POST /api/server/upgrade-xray`，版本为 `latest` 或 `vX.Y.Z`）→
`xray.upgrade` 命令下发（离线留队列补发）→ Agent：

1. `latest` 经 GitHub API 解析（与 install.sh 同款）；设置 `-xray-release-base` 镜像基址时跳过 API，直接走 release 的 `latest/download` 重定向约定下载，实际版本由步骤 3 校验与 telemetry 上报；
2. 下载官方 release 包与 `.dgst`，校验 SHA2-256（获取不到校验文件即失败）；
3. 备份旧二进制（`.bak`）→ 原子替换 → 重启 → 校验实际版本；任一步失败回滚并重启；
4. 成功后新版本经 telemetry（见[遥测](telemetry.md)）刷新面板展示。

下载基址可用 `-xray-release-base` 指向镜像/代理（官方 GitHub 不可达的被控机场景）。

## 事件告警

单人运维的离线通知（设置页"告警"区块，Webhook URL / Telegram token+chat id 三项全空 = 关闭）。

**告警分类**（v0.1.26 起）：每个事件归属三类之一，webhook JSON 的 `category` 字段与 Telegram 文案前缀双表达；未登记事件默认归运行警告。

| 分类 | 覆盖事件 |
|---|---|
| `runtime` 运行警告 | `server_offline`、`config_drift`、`node_failed`、`chain_degraded`（未知事件默认归此类） |
| `expiry` 到期预警 | `billing_due_soon`（续费日 7 天内，每日提醒直至到期或续费）、`billing_due_today`（续费日为今天）、`billing_expired`（续费日已过且服务器离线，标记已过期） |

**触发事件**（仅状态跃迁，不周期重发）：

- `server_offline`：WS 断开导致 online→offline（hub unregister 实际移除注册连接为唯一挂点；
  被新连接顶替的旧连接注销不算，session 重连不重复报；断连后经消音窗口（默认 10s，
  `LATTIX_OFFLINE_DEBOUNCE` 可覆盖）仍离线才触发，窗口内重连则静默）；
- `config_drift`：dispatcher 收到 `config.drift` true；
- `node_failed`：RPC response 失败或命令死信导致节点置 failed；
- `chain_degraded`：链任一跳 server 离线导致 active→degraded（见[代理链与 NAT](chain-nat.md)）。

**Ban 观察状态机**（v0.1.26 起，`server.ban-watch` 任务，与三网目录测速同频——
`chain-latency.probe` 轮次，默认 5 分钟）：

- 单轮结论按窗口内逐运营商（电信/联通/移动）成败计数：三网目标齐全且全部失败 =
  `all_loss`（100% 丢包）；至少一路成功 = `online`；样本不全（目录缺失/上报迟滞）=
  `inconclusive`（本轮不计数也不重置）。
- 状态机：`normal --连续 5 轮 all_loss--> suspected（疑似被 Ban，server_ban_suspected）
  --累计 10 轮--> banned（已被 Ban，server_banned）--每 30 分钟确认一次三网恢复-->
  recovering --下一轮仍在线--> normal（发出 server_ban_recovered 取消告警并归零）`。
  状态机为纯函数（`advanceBanWatch`），快照持久化 `server_ban_watch`，面板重启后从上一次状态继续。
- 前提是 agent 在线：离线轮无法区分宕机与被 Ban，观察中（normal/suspected）直接归零重来；
  banned/recovering 保持（无数据不等于恢复），待 agent 回来后继续判定。
- **非直连豁免**：`NonDirect`（大陆不可直连）属性的服务器三网丢包属预期，跳过评估并
  清除既有观察状态（含 suspected/banned 徽标与推送）。
- **链路页联动**：入口（第一跳）服务器处于 `banned` 且未标记非直连的链路，链路页
  （详情卡与概览行）打「Banned」标（hover 详情带测速省市地域，市级目标显示市）；
  用户订阅页链路状态同步带标（随 5 分钟状态缓存刷新）；恢复后随状态自动消失
  （见[代理链与 NAT](chain-nat.md)）。

**通道**（各自独立判定，异步发送不阻塞主路径，5s 超时，失败仅记日志）：

- Webhook：POST JSON `{"event","category","server","node","detail","time"}` 到 `alert_webhook_url`；
- Telegram：Bot API `sendMessage` 纯文本（分类中文前缀），需 `alert_telegram_bot_token` +
  `alert_telegram_chat_id` 同时具备（token 与 tls key 同风格：不回显，仅给置位标记）。

- 防抖动：同一服务器同一事件 5 分钟内不重复发（内存 map 记上次发送时间，重启清零可接受；
  `LATTIX_ALERT_DEBOUNCE` 可覆盖窗口，dev/e2e 用）。
- `POST /api/setting/test-alerts`：按已保存配置向两通道各发一条测试消息，返回各通道成败。

## SQLite 备份

`GET /api/backup/download`（session 鉴权）`VACUUM INTO` 到临时文件后以
`lattix-backup-<YYYYMMDD-HHMMSS>.db` 附件返回，发送完成清理临时文件；单连接 +
busy_timeout 下与并发读写安全共存，失败 500。设置页"面板维护"区块提供下载按钮。
