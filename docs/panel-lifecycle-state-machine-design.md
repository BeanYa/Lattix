# Panel 生命周期与 Agent 连接状态机

## 1. 目标

Panel 维护自身运行状态，Agent 在此基础上维护与 Panel 的连接观测。状态机只定义
状态效果和协议动作，不限制更新失败后的人工修复方式。Panel 是期望状态的权威来源，
Agent 尽力同步；无法同步时不得把暂时故障误判为凭据永久失效。

本设计解决以下问题：

- Panel 启动、更新和故障期间的动作边界不明确；
- Panel 更新导致所有 Agent 同时进行延迟探测并产生尖峰；
- WebSocket 关闭无法可靠区分鉴权拒绝与暂时不可用；
- Panel 无法表达 Agent 正在重连以及本次在线来自重连；
- 首次凭据响应丢失会使 bootstrap 与长期凭据同时不可用。

## 2. 正交状态

Panel 全局生命周期只有四个状态：

```text
startup | active | updating | faulted
```

状态效果：

| 状态 | Agent WS | 业务命令 | liveness | latency probe |
|---|---|---|---|---|
| `startup` | 允许 | 暂停 | 继续 | 暂停 |
| `active` | 允许 | 允许 | 继续 | 正常 |
| `updating` | 允许 | 维持现状 | 继续 | 暂停 |
| `faulted` | 尽力通知后断开 | 禁止 | 不要求 | 暂停 |

`updating` 是协调信号，不表示下载、校验、替换或修复阶段。只要当前 readiness
校验通过，人工干预或更新任务均可将其恢复为 `active`。关键控制面故障可从任意状态
进入 `faulted`；恢复时重新执行初始化检查。

每个 Agent 在 Panel 侧另有连接状态：

```text
never_connected | connecting | reconnecting | online | offline | auth_rejected
```

公开 API 不再返回 `online` 布尔字段。Backend 内部保留 `IsOnline(serverID)`，用于判断
当前是否存在可投递业务消息的会话。连接历史持久化，当前连接和 session 只在 Hub 内存中维护。

Agent 自身卸载不属于 Panel 生命周期。`agent.uninstall` 成功后 Agent 先回执再自毁：
系统安装经独立 systemd unit 执行清理（避免 service cgroup 带走 cleaner）；用户态安装
停 runner/crontab 后删文件。详见 framework-design §5 `agent.uninstall` 行。卸载后
不再维护 Panel 观测状态。

## 3. 生命周期版本

每次 Panel 进程启动生成随机 `epoch`，本次进程内每次状态变化递增 `revision`：

```json
{"epoch":"random-run-id","revision":2,"state":"active"}
```

Agent 对同一 epoch 只接受更大的 revision；重复 revision 幂等确认，更小 revision 忽略。
新 epoch 只能从一条新建且已鉴权会话的 `agent.session.open` 响应中接受。随机 epoch 不依赖
数据库单调计数，因此旧备份恢复不会造成版本倒退歧义。

当前生命周期快照在进程内存中权威维护。数据库只保存尽力写入的迁移审计记录，不能从备份
中的旧状态跳过 `startup`。

## 4. HTTP Upgrade 鉴权与会话

Agent 建立 WebSocket 时使用标准头：

```http
Authorization: Bearer <bootstrap-or-long-term-token>
```

凭据明确无效时，Panel 不升级 WebSocket，返回 HTTP 403 和统一 RPC body，响应同时携带
`X-Lattix-Protocol: 1`。Agent 只有在状态、协议标记、RPC code 和 ID 均合法时才进入
`auth_rejected`。代理生成的普通 403 按暂时网络故障重试。

Panel 无法可靠读取身份数据或处于 `faulted` 时返回 HTTP 503。`startup`、`active` 和
`updating` 均允许有效凭据建立 WS。

每条 WS 的第一帧是 `agent.session.open`，替代旧的 `agent.hello`。它负责上报 Agent/Xray
能力、网卡地址和上次生命周期观测；响应返回 server ID、session ID、会话类型以及当前
Panel 生命周期快照。长期凭据重连不轮换 token。

首次 bootstrap 换发采用两阶段事务：Panel 持久化并重复返回同一 pending 长期凭据；Agent
原子落盘后发送 `agent.credential.commit`；Panel 收到 commit 后才使 bootstrap 失效。

重连会话完成条件为：`session.open` 完成、生命周期已应用、0-1000ms 随机等待后的首次
liveness Ping/Pong 成功、Agent 发送 `agent.session.ready`。延迟探测不参与上线判定。

## 5. 生命周期同步

Panel 使用 `panel.lifecycle.changed` request，Agent 应用状态效果后返回同 type response。
消息携带 panel instance ID、epoch、revision、状态和受边界约束的重试提示。

更新开始时 Panel 先进入 `updating`，向当前在线 Agent 发送请求，并等待全部 ACK 或 5 秒
上限。等待期间断线的 Agent 从屏障集合移除；超时只记录未确认 Agent，不阻止更新。更新期间
新连接从 `session.open` 响应获得 `updating`，不启动 latency probe。

Panel 进入 `active` 后，Agent 从 Panel 提示的窗口中选择随机恢复延迟。默认窗口为 0-30 秒，
之后恢复每 30 秒一次的正常测量。

## 6. Liveness 与延迟测量

WebSocket liveness 和 latency measurement 使用独立调度。两者可复用 Ping/Pong 控制帧，
但 payload 必须区分用途，只有 latency payload 计算 RTT。

- liveness 在所有已连接 Panel 状态下继续运行；
- latency 只在 `active` 下运行；
- latency 超时只移除 pending 样本，不关闭 WS；
- telemetry 不等待首次 latency，当前没有新测量时发送 `latency_ms: null`；
- 进入 `updating` 时保留最近 3 个已完成样本，只清理 pending probe；
- 恢复后向原窗口 append，新样本继续使用最近 3 次中位数；
- 数据库历史样本不修改。

普通断网沿用指数退避。已知 updating 默认在 5-15 秒内随机重连，faulted/HTTP 503 默认在
30-90 秒内随机重试。Panel 可提供提示，Agent 将重连限制在 1 秒至 5 分钟、latency 恢复
窗口限制在 5 秒至 5 分钟。

## 7. 删除与卸载

删除服务器时 Panel 尽力投递 `agent.uninstall`，不要求证明远端文件已被物理删除。总投递
次数不超过 10 次：第一次立即发送，后续从 100ms 指数退避，单次上限 10 秒。重复投递使用
同一 request ID，Agent 幂等处理。

收到 ACK 或耗尽预算后，Panel 删除该服务器的维护状态和数据，凭据随记录失效。残留 Agent
后续连接会得到明确 HTTP 403 并进入 `auth_rejected`，由管理员决定是否手工卸载。

## 8. 管理 API

`GET /api/panel/state` 返回当前生命周期快照、进入时间、安全故障摘要和重试提示。更新阶段与
百分比仍由 `/api/panel/get-update-status` 提供。Server DTO 返回 `connection_state`、session kind、
最近重连时间和重连计数，不重复嵌入 Panel 全局生命周期。
