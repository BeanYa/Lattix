# 服务器类型互转与链路 TryExchange（设计）

> 需求：允许已创建的服务器在 **独立 IP**（`kind=managed, machine_type=direct`）、**NAT**（`kind=managed, machine_type=nat`）与 **外部直链**（`kind=external, machine_type=nat, allowed_ports=""`）三种类型之间互转（包括 NAT 可用端口段的扩展/收窄/清空/新增），并对受影响链路自动执行 `TryExchange`；内置转换无法自动完成的链路在链路页标记为「转换失败（`exchange_failed`）」并写明原因，由管理员自行编辑修复或删除。

## 1. 服务器类型模型与互转矩阵

| 类型 | `kind` | `machine_type` | `allowed_ports` | 入站能力 |
|---|---|---|---|---|
| 独立 IP | `managed` | `direct` | `""` | 有（任意空闲端口） |
| NAT（受限直连） | `managed` | `nat` | 非空端口段列表 | 有（受限于段内） |
| NAT（仅出口档） | `managed` | `nat` | `""` | 无（仅可作多跳出口，走 reverse） |
| 外部直链 | `external` | `nat` | `""` | 无（无 Agent，仅可作多跳出口） |

### 1.1 托管类型 ↔ 托管类型（`direct ↔ nat`、NAT 端口段修改）

- 保留既有服务器记录 ID、已绑定的节点/链路/账单/流量计划，以及公共资料（`alias`、`country_code`、`location`、`tags`、`non_direct`）。
- 转为 `nat` 时必须提供非空公网地址；端口段留空表示仅出口档 NAT。
- 转为 `direct` 时自动清空 `allowed_ports`。
- 保存不再受旧有 `checkPortsShrink` 阻塞，改由保存后对受影响链路执行 `TryExchange` 自动适配或降级为 `exchange_failed`。

### 1.2 外部直链 → 托管（`external → managed`）

- 要求提供托管服务器的必填字段：`alias` 非空、`country_code` 合法；若目标为 `nat`，`address` 必填。
- 存储层清空 `external_link`、`external_config`、`panel_latency_ms`、`panel_probed_at`。
- 递增 `credential_epoch` 并生成一次性 bootstrap token（`credential_committed=0`、`last_seen_at=NULL`），重置连接/版本状态。
- `/api/server/update` 响应携带 `bootstrap_token`、`install_command`、`install_insecure`，前端自动弹出安装命令弹窗。

### 1.3 托管 → 外部直链（`managed → external`）

- 接受可选的 `external_exit` / `external_link`（均可留空占位），`country_code` 可空，`alias` 留空时按 `host:port` 或「外部直链」派生。
- 存储层写入 `kind='external'`、`machine_type='nat'`、`allowed_ports=''`、`external_link`、`external_config`、`address`；清空托管运行时字段、删除 `server_ban_watch`，并废弃该服务器上未终结命令（`queued`/`sent → abandoned`）。
- 吊销旧凭证（递增 `credential_epoch` 并写入随机不可用 token）并调用 `Hub.ForgetAgent` 断开当前 WebSocket 会话；`Dispatcher.AuthenticateToken` 拒绝 `srv.IsExternal()` 的机器再次接入。
- **不向 Host 发送 `uninstall`**：避免误删目标机器上可能正在运行的外部代理服务。

### 1.4 外部直链 → 外部直链（不变）

- 维持既有语义：若规范化后的 `external_config` 变更且仍被非 `deleted` 链路引用，继续返回 `409 CONFLICT`。

## 2. 受影响链路的 `TryExchange`

服务器类型或 `allowed_ports` 发生变更后，面板依次对引用该服务器的非 `deleted`/非 `invalid` 链路执行 `TryExchange`（执行前暂停受影响链路的自动编排 worker，完成后唤醒/清理）：

### 2.1 无法自动转换 → `exchange_failed`

出现以下任一情况时，不阻断服务器保存，而是通过 `chainFSM.MarkExchangeFailed` 将链路状态置为 `exchange_failed`（「转换失败」）并记录原因：

1. **托管 ↔ 外部直链互转**（`before.Kind != after.Kind`）：外部链路无出口业务节点且入口强制共享端点，与托管链路模型不兼容，需管理员编辑替换跳位或新建/删除链路。
2. **失去入站能力但担任入口/中转/单跳**：服务器转为仅出口 NAT（无可用端口）且当前处于单跳链、入口跳或中转跳时，无法监听入站流量。
3. **链路正处于并发部署中**：链路存在未发布的 `desired_revision_id` 且状态不是 `failed`/`active_failed`/`waiting_for_agent`/`exchange_failed`。
4. **修订规划/端点/端口段约束失败**：例如新建共享端点或替换端口时发生约束冲突。

### 2.2 可自动修复场景

1. **监听端口落入 NAT 可用端口段（`direct → restricted nat` 或 `allowed_ports` 变更）**：
   - Agent 侧 `pickChainPort` / `ApplySharedEndpoint` 对同一 `hop_id` / `endpoint_id` 会幂等复用 `prev.Port`。因此当旧端口不再落在新的 `allowed_ports` 内时，必须分配新的 `SharedEndpoint`（`EnsureSharedEndpointInRanges` / `EnsureProtocolSharedEndpointInRanges`）或新的 `HopID`（`NextChainHopID`），并置监听端口为 `0`（由 Agent 在 `port_candidates` 内重新挑空闲端口）；普通出口业务节点则清零 `vc.Port` 并强制重发 `service` 与上游 `forward`。
2. **可入站 ↔ 仅出口 NAT（出口跳）**：
   - 重新按既有规则推导全部跳间 `transport`（`direct`/`hy2`/`encrypted` ↔ `reverse`），按需生成/清理 `tunnel_uuid` 与 `portal`/`bridge` 配置件。
   - 若出口为 Hysteria2 且失去入站能力或原 `port_hop` 不再落在新 `allowed_ports` 内，自动在可用段内重新分配或关闭 `port_hop`；端到端 hy2 同步更新各跳 `forward_port`（并按需分配新 `HopID`）。
3. **非 1:1 NAT 映射公网端口变化**：
   - 即使内部监听端口不变，只要 `publicPort(before, listen) != publicPort(after, listen)`，强制重发引用该公网端口的上游 `forward` 或下游 `bridge` piece，并重建订阅。
4. **编排下发与离线等待**：
   - 若 `PlanRevision` 产出新 revision 且所需 Agent 全部在线，立即启动 `applying` 编排；若任一必需 Agent 离线，则置入 `waiting_for_agent` 并在 Agent 上线后自动恢复；若没有任何需要重编排的变化，跳过该链。
   - 发布后若旧共享端点因端口越界不再被任何链引用，则清理 DB 记录并向托管 Agent 下发 `shared-endpoint.remove`；否则正常 reconcile 移除该链路由。

## 3. `exchange_failed` 状态语义

- 所属状态机：`dispatch/chain_fsm.go`：
  - 任意非终态（`pending`/`applying`/`active`/`degraded`/`failed`/`active_unconfirmed`/`active_failed`/`waiting_for_agent`/`cleanup_pending`/`exchange_failed`）均可经 `MarkExchangeFailed` 转入 `exchange_failed`。
  - 允许从 `exchange_failed` 转换到：`applying`（管理员通过「编辑链路」修复拓扑/服务器后重编排）、`invalid`（关联服务器被删除）、`deleted`（管理员删除链路）。
- **数据完整性**：`MarkChainExchangeFailed` 会废弃在途 `commands`、`chain_revision_tasks` 与 `desired_revision_id`，但**保留 `chain_hops`** 与历史 `published_revision_id`，保证链路页详情与编辑向导仍能展示并编辑原有跳点拓扑。
- **隔离效应**：
  - 订阅目录（`catalogChain`）与服务器间延迟探测（`PublishedLatencyEdges`）均排除 `exchange_failed`，自动撤出用户订阅。
  - 进入 `exchange_failed` 时立即触发订阅重生成，并 reconcile 相关联的托管共享端点以撤销该链路由。
  - 链状态机 `Evaluate` 跳过 `exchange_failed`，不被心跳/端点事件覆盖。

## 4. 自动编排图（`chain_hop_graphs`）协调

若服务器存在于其他链路的备选自动编排图中：
- 对引用该服务器的节点，若公网地址已不在新地址集合内则重置为默认地址，若监听端口不在新 `allowed_ports` 内或转为外部直链则重置为 `0`。
- 重新执行 `validateHopOrchestration`：校验通过且节点有调整时持久化更新；不再兼容（如非出口备选失去入站能力、托管/外部备选属性冲突、启用 Ban 触发器但转为外部直链）时删除该跳位编排图并写入 `chain_orchestration_states` 错误提示。

## 5. API 与前端交互

- `POST /api/server/update`（`ServerUpdateRequest`）新增可选字段 `kind`、`machine_type`、`allowed_ports`，返回值新增可选字段 `bootstrap_token`、`install_command`、`install_insecure` 与 `exchange`（`{total, exchanged, waiting, failed, failed_chains}`）。
- 前端「编辑服务器」弹窗顶部复用与「添加服务器」一致的三选一类型切换器，切型时保留公共资料并动态切换特有表单与上下文变更提示；转换为托管类型且返回安装命令时自动弹出安装命令弹窗；存在 `exchange_failed` 链路时弹出汇总提示并引导管理员前往链路页处理。
- 链路页、链路概览与控制台展示红色「转换失败」状态，计入异常筛选，隐藏无意义的「重试链路」，保持「编辑链路」与「删除链路」可用。
