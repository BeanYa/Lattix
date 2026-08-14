# 面板优雅停机与 Agent 设置同步设计

## 1. 目标与边界

本文定义面板生命周期、Agent 重连、全局 Agent 设置同步、凭证轮换和重新绑定。
项目为全新开发，不提供旧 token、旧 Agent state 或旧安装目录的兼容迁移。

面板地址是安装时确定的本地连接配置。同步文档中的 `panel.public_url` 和
`panel.ws_url` 仅为元数据，Agent 不据此切换连接目标；更换面板地址必须重新执行安装命令，
重新建立面板与 Agent 的认证关系。

## 2. 面板生命周期

进程入口是唯一生命周期所有者。SIGINT、SIGTERM、手动重启、自更新完成和 HTTP 服务异常
进入同一个关停流程：

1. 原子登记关停原因；重复重启请求返回 RPC `OPERATION_LOCKED`。
2. Hub 进入 draining，拒绝新 WebSocket Upgrade，并以关闭码 `1012 Service Restart`
   关闭现有 Agent WebSocket。
3. `/readyz` 返回 HTTP 503；`/healthz` 在进程退出前仍返回 HTTP 200。
4. drain 后进入管理 RPC 的请求返回 HTTP 200，业务 body 为
   `SERVICE_UNAVAILABLE`。HTTP 仅表示 RPC 是否完成传输和解析。
5. 取消后台任务，并调用 `http.Server.Shutdown`。
6. 写入面板停止事件、排空请求日志，最后关闭数据库。

应用总预算为 10 秒：HTTP 关停最多使用前 8 秒，其余 2 秒用于日志和存储清理。systemd
使用 `TimeoutStopSec=15`，`RestartSec=1`，并配置启动频率保护。第二个终止信号立即取消
HTTP 优雅等待并强制关闭连接。

计划内关停不产生 `agent.offline`、服务器离线告警或链路 degraded。Agent 重连成功记录
`agent.reconnected`。`http.Server.Shutdown` 不处理已 hijack 的 WebSocket，因此 Hub 必须
显式关闭所有 Agent 连接。

命令保持至少一次投递语义。关停不等待所有已发命令回执；Agent 重连时仍将 `sent` 重置为
`queued` 后补发，因此 Agent 命令处理必须保持幂等。

## 3. Agent 重连策略

全局设置支持两种模式：

- `infinite`：默认。持续使用指数退避重连。
- `limited`：执行 `max_retries` 次快速重试；耗尽后每 5 分钟探测一次，永不永久停机。

普通重连从 500ms 开始，倍率 2，上限 30 秒，抖动 ±20%。成功完成 `agent.session.open` 后重置失败计数。
收到 WS `1012` 时按最近一次 Panel 生命周期给出的有界提示重试。网络不可达、超时、无响应
和其他未得到面板明确认证结论的错误均可永久重试；`limited` 模式耗尽快速重试后保持每
5 分钟一次的低频探测。

面板在 HTTP Upgrade 前完成 Bearer token 鉴权，并以 HTTP 403、合法 RPC body 和
`X-Lattix-Protocol` 标记明确拒绝凭证时，Agent 记录“面板可能已重建或凭证已替换”，停止
全部连接尝试并保持进程等待关停。保持进程而非直接退出，是为了
避免 systemd `Restart=always` 或用户态守护脚本把终止性认证结果变成无限重启。管理员取得
新面板 bootstrap 凭证后重新执行安装命令或重启 Agent，才重新建立认证关系。

`max_retries` 取值 1–100，默认 10；无限模式保存该字段但忽略限制。

## 4. 统一设置文档

Panel、Agent 和前端直接使用同一数据结构：

```json
{
  "schema_version": 1,
  "panel": {
    "instance_id": "p_...",
    "version": "v1.2.0",
    "public_url": "https://panel.example.com",
    "ws_url": "wss://panel.example.com/api/agent/ws"
  },
  "agent": {
    "revision": 12,
    "reconnect": {
      "mode": "infinite",
      "max_retries": 10
    },
    "telemetry": {
      "interval_seconds": 60
    },
    "drift_detection": {
      "interval_seconds": 15
    }
  }
}
```

只有 `agent` 对象参与 revision。面板元数据在每次同步时刷新，不触发 revision。遥测间隔
范围 10–3600 秒，漂移检测范围 5–3600 秒。路径、固定连接地址、token、Xray API、runner、
下载镜像和实际二进制版本属于 Agent 本地或运行态信息，不进入全局设置。

## 5. pull 同步状态机

1. 管理员保存 Agent 设置；面板整体替换 `agent` 对象并在同一事务中递增 revision。
2. 面板向在线 Agent 尽力发送 `agent.settings.changed` 提示。
3. Agent 在 `agent.session.open` 后立即、收到提示时立即、以及在线期间约每 60 秒发送
   `agent.settings.sync`，携带 panel instance ID、已成功应用 revision 和最近一次安全错误。
4. revision 和面板实例均一致且无错误时，面板返回 `changed=false` 并标记同步。
5. 不一致时面板返回完整文档；Agent 完整校验、原子写入 `settings.json`、整体应用
   `agent` 对象，再立即同步一次确认。
6. 校验或写入失败时保留上一份生效设置，不更新 applied revision；下次同步上报
   `last_apply_error`。

设置更新 API 只保存期望状态，成功使用 RPC `OK` 并返回新 revision；它不是异步操作，
不得返回 `ACCEPTED`。提示事件丢失不会破坏最终一致性，定时同步和重连同步会自动补偿。

服务器同步状态由面板期望 revision 与 Agent 最后上报值推导：
`synced`、`pending` 或 `failed`，不单独维护可漂移的布尔值。

## 6. 面板身份、凭证与重新绑定

面板实例 ID 在首次打开数据库时生成并持久化；数据库备份恢复保留同一逻辑面板身份。
凭证格式为：

```text
ltx1.<panel_instance_id>.<credential_epoch>.<32-byte-random-secret>
```

panel ID 和 epoch 是公开路由信息，随机 secret 提供认证强度；服务端始终校验完整 token。
bootstrap 换发长期 token 时保留 epoch、轮换 secret。

Agent 同时看到安装命令 bootstrap 与本地长期 token 时：

- panel ID 和 epoch 相同：使用本地长期 token，适用于普通服务重启；
- epoch 更高：使用安装命令 bootstrap，适用于管理员 rotate 后重装；
- panel ID 不同：使用安装命令 bootstrap，进入跨面板重新绑定。

凭证换发是一个三态小状态机（store 层 `WHERE` 守卫强制转换，见 `store/servers.go`）：

```text
bootstrap → pending（session.open 换发长期凭证，未提交；重复换发幂等返回同一 pending）
pending   → committed（agent.credential.commit，校验 exchange_id 后 bootstrap 失效）
committed → bootstrap（rotate-token 递增 epoch 并重置）
```

`rotate-token` 表示立即撤销：面板递增 epoch、替换数据库 token，并以 WS `4001` 关闭旧连接。
旧 Agent 重连时收到带 `X-Lattix-Protocol` 标记和合法 RPC body 的 HTTP 403 后进入
`auth_rejected` 并停止自动重试，直到管理员运行新安装命令并重启 Agent。同面板 rotate
保留 Xray 配置和设置。

跨面板重新绑定必须先由新面板认证成功，然后才备份并移除旧面板管理的 Xray 配置、链配置件、
旧 settings 和旧身份。认证失败不得清理旧配置。

## 7. Agent 文件布局

system 模式：

```text
/opt/lattix-agent/
  bin/lattix-agent
  bin/latx-ag
  bin/xray
  config/agent.env
  config/xray.json
  data/state.json
  data/settings.json
  logs/agent.log
/usr/local/bin/latx-ag -> /opt/lattix-agent/bin/latx-ag
```

用户模式使用 `~/.lattix-agent/{bin,config,data,logs}`。systemd unit 仍位于
`/etc/systemd/system`，BBR 配置仍位于 `/etc/sysctl.d`。`state.json` 保存 token、面板身份、
credential epoch、server ID 和链配置件；`settings.json` 保存完整统一同步文档。
