# 控制通道协议与节点落地

> 拆自[框架设计索引](../framework-design.md)，对应原 §5（控制通道协议）、§6（节点生命周期与 apply 流水线）、§7（虚拟配置与参数分工）。HTTP RPC 与 requester 约定见 [RPC API 设计](../rpc-api-design.md)。

## 控制通道协议

连接：Agent 携带服务器 token 拨出至 `Backend /api/agent/ws`。

信封：JSON，统一包含 `kind`、`type`、`request_id`、`trace_id` 和
`data`；响应另含 `code`、`message`。请求和响应使用相同 `type`，
`type` 采用 `domain.action`。完整结构见
[WS JSON Schema](../ws-protocol.schema.json)（部分参考，协议事实以 Go 序列化与契约测试为准）。

消息类型：

| type | 方向 | 说明 |
|---|---|---|
| `agent.session.open` | agent→panel | 每条已鉴权 WS 的会话初始化：agent/xray 版本、运行状态、网卡地址与上次生命周期观测；首次 bootstrap 在两阶段事务中换发长期凭据，普通重连不轮换 token |
| `agent.session.ready` | agent→panel | Agent 已应用生命周期快照且首次 liveness 成功，Panel 据此完成 connecting/reconnecting → online |
| `agent.credential.commit` | agent→panel | Agent 已原子保存首次换发的长期凭据，Panel 据此使 bootstrap 失效 |
| `panel.lifecycle.changed` | panel→agent | 带回执的 Panel 生命周期同步；更新开始前以有界 ACK 屏障确认 Agent 已暂停延迟探测 |
| `agent.settings.sync` | agent→panel | session ready 后、设置提示后及在线期间周期拉取全局 Agent 设置；请求携带已应用 revision，响应按需返回完整统一设置文档 |
| `agent.settings.changed` | panel→agent | 在线设置变更提示；仅触发 Agent 立即 pull，不承载配置本身 |
| `node.apply` | panel→agent | 下发节点：虚拟配置模板 + 分配到该节点的用户 UUID 列表（见[用户与分组](users-and-groups.md)） |
| `shared-endpoint.apply` | panel→agent | 完整替换服务器级共享入口：assignment clients + 按 chain 聚合的 routes；重发复用端口与 Reality 密钥 |
| `shared-endpoint.remove` | panel→agent | 显式删除共享入口配置件；当前常驻复用策略不自动触发 |
| `node.remove` | panel→agent | 删除节点 |
| `user.add` | panel→agent | 向载荷指定节点的 inbound 热加入一个用户（`nodes` 参数携带各节点协议参数；必填，缺省/为空回执错误） |
| `user.remove` | panel→agent | 从载荷指定节点的 inbound 热移除一个用户（`nodes` 必填，同 user.add） |
| 对应请求的 `response` | agent→panel | `code=OK` 时 data 可含 realized_config；失败由稳定 code 与安全 message 表达 |
| `agent.uninstall` | panel→agent | 卸载 agent：先回执再自毁。`purge_xray=true` 连同 xray/配置清除，`false` 仅移除 agent（xray 与节点继续运行）。识别 install.sh 安装树（`…/lattix-agent/bin/lattix-agent` 或 `…/.lattix-agent/bin/lattix-agent`）：系统路径经独立 systemd unit 跑清理（`systemd-run` → `/run` oneshot → setsid 三级回退，避免 `KillMode=control-group` 杀掉 cleaner；脚本先 `disable --now` 压制 `Restart=always`）；用户态 setsid 停 runner/crontab 后删文件。dev 非安装路径不触碰宿主机安装。清理清单与 `latx-ag uninstall` 双向对齐（含 connection/command-queue/lock/log/bak）。面板删服务器为 best-effort，不证明远端物理删除 |
| `xray.upgrade` | panel→agent | 升级 xray 到指定版本（见[服务器维护](server-maintenance.md)）：下载官方 release 校验 .dgst 后替换重启，失败回滚 |
| `agent.upgrade` | panel→agent | 下载、校验并原子替换当前 Agent，响应后退出并由 systemd 拉起 |
| `telemetry.report` | agent→panel | 周期遥测（见[遥测](telemetry.md)）：xray 版本/运行状态、主机指标、流量增量；无需回执 |
| `config.drift` | agent→panel | 配置漂移状态变化（见[服务器维护](server-maintenance.md)）：外部修改时 true，修复/恢复后 false |
| `server.settings.sync` | agent→panel | 服务器级设置（当前为 xray 版本锁定，面板默认 + 单服务器覆盖两级）按 revision 拉取同步，机制照抄 `agent.settings.sync`；面板默认或覆盖变更经 `server.settings.changed` 提示在线 Agent 立即 pull |
| `xray.cleanup` | panel→agent | 按期望 inbound tag / piece 集合清理 xray 受管配置件（支持 dry_run 预览），详见 [xray 清理设计](../xray-cleanup-design.md) |
| `xray.rebuild` | panel→agent | 重建服务器 xray 配置（期望状态全量重放，服务器页"重建 xray 配置"） |
| `server-test.run` / `server-test.progress` / `server-test.result` | panel→agent / agent→panel | 服务器测试原子任务下发、尽力进度上报与断线可恢复的权威最终报告，详见[服务器测试设计](../server-testing-design.md) |
| `latency.probe` / `latency.report` | panel→agent / agent→panel | 链路测速轮次下发与有向服务器对延迟回报（panel 调度、round_id 关联），详见[链路统计设计](../chain-link-stats-design.md)；三网目录测速目标并入同一轮次 |

Panel 生命周期为 `startup|active|updating|faulted`；每个 Agent 的连接状态为 `never_connected|connecting|reconnecting|online|offline|auth_rejected`。HTTP Upgrade 使用 Bearer token，明确鉴权失败返回 HTTP 403 与结构化 RPC body，暂时不可用返回 503。liveness 与 latency probe 分离：liveness 维持连接，latency 只在 active 测量且失败不再断开 WS。完整语义见 [Panel 生命周期与 Agent 连接状态机](../panel-lifecycle-state-machine-design.md)。

读模型：每连接读循环只负责读取、续期与投递，业务信封由独立的 handlerPump goroutine 顺序消费（连接内保序，连接之间并发），回执落库等慢处理不阻塞读取与续期；上行处理队列 256 缓冲，写满按慢连接断开，未处理消息由 Agent 重连后补发（下行发送队列同策略）。业务命令门控在 dispatch 层统一下发入口执行（`src/backend/internal/dispatch` 的 `isBusinessCommand`）：面板处于 startup/faulted 时拒投 14 类业务命令，命令滞留 queued，待面板就绪后补发；握手、回执、上报等非业务信封不受影响。

## 节点生命周期与 apply 流水线

节点状态机：`pending → applying → active | failed`。`failed` 携带错误详情，面板提供重试按钮。（链场景的跨机编排、链级状态机与逐跳重试见[代理链与 NAT](chain-nat.md)；本节流水线对链中每一跳仍然适用。）

Agent 收到 `node.apply` 后的落地流水线（顺序固定）：

1. 填充模板占位符（见下节）；
2. **dest 预检**：对模板 dest 做 TCP+TLS 可达性检查；不可达则按 `node.apply` 携带的白名单候选（`dest_candidates`，面板内置并随版本更新，尽量丰富）逐个尝试，全部失败则上报 error；
3. 写入临时配置文件；
4. `xray run -test -config <file>` 校验，失败则丢弃并上报 error；
5. 落盘正式配置（Agent **独占管理** `/opt/lattix-agent/config/xray.json`；
   用户模式对应 `~/.lattix-agent/config/xray.json`）；
6. 调 xray gRPC API 热操作（`AddInbound` / `AlterInbound` / `RemoveInbound`）；
7. 热操作失败才 `systemctl restart xray`；
8. 重启失败则恢复上一份可用配置、再次重启，并上报 failed。

参考依据：3x-ui/x-ui 系通过 xray gRPC API 的 `AlterInbound`（AddUser/RemoveUserOperation）实现增删用户零重启；XrayR/V2bX 类商业化节点侧通过进程内重载 xray-core 达成同等效果。MVP 采用前者为主路径、重启为兜底。

## 虚拟配置与参数分工

虚拟配置 = **xray inbound JSON 模板 + 占位符**，Agent 填值后**原样写入**，不存在任何"翻译层"。

| 参数 | 生成方 | 说明 |
|---|---|---|
| 独立节点用户 UUID | 面板 | 历史/独立节点继续使用 `users.uuid` |
| 链路访问 UUID | 面板 | 每个 user-chain assignment 独立 `access_uuid`；Xray email 为 `access:<assignment_id>` |
| 内部隧道 UUID | 面板 | `service_uuid` 只认证入口到出口的内部连接；Xray email 为 `tunnel:<service_uuid>` |
| Reality 密钥对 | **Agent** | 执行 `xray x25519` 生成，私钥不出服务器，public_key 随 `RPC response` 上报 |
| short_id | 面板 | 随模板下发 |
| 端口 | 两者皆可 | 向导中可指定（Agent 检查占用，冲突报错）或留空（Agent 挑空闲端口上报） |
| dest / serverNames | 面板 | 向导表单（带默认值）；留空时由 Agent 按白名单预检自动选择（见上节），选定值随 `RPC response` 上报 |
