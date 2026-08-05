# 跨 Profile 共享入口端口（自动加入受管监听）的设计

日期：2026-08-06

## 背景与问题

用户在面板中修改链路的入口端口时报错，无法保存：

```
shared endpoint port conflicts with an incompatible managed listener
```

该错误来自 `store.EnsureSharedEndpoint`（`src/backend/internal/store/endpoints.go:87`）：同一服务器上目标端口已被**任何** `shared_endpoints` 行占用（profile_hash 或 protocol 不同）即返回 `ErrEndpointConflict`（HTTP 409）。

**根因**：`profile_hash` 是整份 VLESS 虚拟配置（排除端口）的 sha256，包含 dest/serverNames/short_id/flow/network/encryption 等全部 Reality 参数。两条链配置几乎必然不同（short_id 创建时留空会随机生成），因此"端口复用"在实际使用中基本不可能触发，用户一旦想把多条链路指向同一入口端口（典型场景：共用一个优质入口服务器 + Reality 复用 443 降低封禁风险）就被 409 卡死。

**关键事实**：共享端点链的订阅条目**完全渲染端点的 realized 参数**（`sub.go:742-772` 用 endpoint 的 ConfigTemplate/RealizedConfig/credential 构造条目），与链自己的入口参数无关；端点 reconcile 按 ChainID 聚合所有链的路由与用户（`dispatch/endpoint.go:71-89`）。因此"不同 profile 的链共享同一监听"在数据面与订阅面都天然成立，被卡住的只是面板的 profile 兼容性门槛。

## 目标

- 同一服务器上，端口已被受管监听占用时，VLESS 链创建/编辑指定该端口 → **自动加入**该监听（不再 409），入口端口复用扩展到**任意端口**（不限于 443）。
- 加入链自己的入口 Reality 参数被忽略（以先占用链为准），但订阅输出与实际监听始终一致。
- 前端明示"共享入口"状态与加入语义，避免用户困惑。

## 语义

1. **加入规则**：`EnsureSharedEndpoint` 同 server + 同 port + 同 protocol → 返回现有端点（无论 profile_hash），链的 `Snapshot.EndpointID` 指向该端点；该链的路由/用户并入端点 reconcile（现有机制，无需改动）。
2. **入口参数归属**：端点的入口 Reality 参数（dest/serverNames/short_id/flow/network/encryption 等）以**首次占用该端口的链**为准；加入链自己的入口参数被忽略。订阅渲染端点参数（现状），客户端配置与实际监听一致。
3. **保留的冲突**：仅协议不同（防御性；当前共享端点只会是 VLESS，实际不会触发）。端口被 OS 非受管进程占用仍由 Agent bind 探测兜底（现状）。
4. **端口=0（自动）路径不变**：仍按 profile 复用或新建。
5. **端点行永久性不变**：加入链无法修改共享端点的模板参数；想用自己的参数需选空闲端口新建专属监听。已退出共享端点的链留下的孤儿监听继续占用端口（现状）。
6. **附带修复**：编辑链改了配置（profile 变化）但保持端口不变时，之前命中"自冲突"无法保存，现在变为加入原端点，正常保存。

## 改动清单

### 1. Backend store：放开 join 语义

`src/backend/internal/store/endpoints.go` — `EnsureSharedEndpoint`：

- 移除 profile_hash 不匹配即冲突的分支（`endpoint.ProfileHash != profileHash || endpoint.Protocol != protocol`）：
  - protocol 相同 → 返回现有端点（join）；
  - protocol 不同 → 仍返回 `ErrEndpointConflict`（防御）。
- 注释同步更新（§66-68 的语义描述）。
- `SetSharedEndpointActive` 的重复端口检查（§141-145）保留（防御重复行）。

### 2. Backend panel：DTO 字段与死代码清理

`src/backend/internal/panel/chains.go`：

- `chainDTO` 新增 `EntryShared bool json:"entry_shared,omitempty"`：该端点被 ≥2 条未删链（`chains.endpoint_id=? AND deleted_at IS NULL`）引用时为 true（store 新增等价查询，不沿用 `ChainIDsByEndpoint`——它只返回 active/degraded 链，计数不完整）。
- `toChainDTO` 在填充 EndpointID 分支里填 `EntryShared`。
- `handleCreateChain`/`handleEditChain`：`ErrEndpointConflict` 分支成为死代码，简化为统一错误处理。

### 3. Frontend：共享入口提示

`src/frontend/src/pages/Chains.tsx`：

- **链列表**：`chain.entry_shared` 为 true 时在入口端口旁显示"共享入口"徽标。
- **创建/编辑表单**：`entryPort` 输入时，若该端口已被其他链占用（从已加载链列表本地匹配 entry server + entry_port），在端口字段下方显示提示："该端口已被链路「X」的共享监听占用，将共享其入口参数（dest/short_id 以现有监听为准）"；不再作为错误拦截。

### 4. 文档

- `docs/framework-design.md` §21.1 端口复用段落："相同 server/port 上兼容 profile 复用既有 Endpoint，不兼容受管监听报冲突" → "相同 server/port 上任意 VLESS 链可加入既有 Endpoint 共享监听，入口参数以先占用链为准"。

## 错误处理与边界

- 加入的端点状态为 failed/pending 时照常加入：reconcile 会重试下发（现有自愈机制），订阅侧对 failed 端点已有"部署失败"警告文案。
- 同端口多行的历史残留（并发竞态遗留）：`ORDER BY id` 取首行加入，不新增冲突路径。
- 前端提示为尽力而为（基于链列表快照）；后端 join 是权威行为，提示与行为不一致仅影响展示。
- 已知限制（记录不处理）：端点行永久（无 remove 流程）；孤儿监听永久占用端口；共享端点的入口参数不可被加入链修改。

## 测试

- `src/backend/internal/store/endpoints_test.go`：原"不同 profile 同端口 → conflict"断言改为断言返回现有端点（join）且不新增行；protocol 不同仍冲突。
- `src/backend/internal/panel`：create/edit 指定被占用端口 → 200 且 `EndpointID` 指向现有端点；同端口改 profile 不再 409；`entry_shared` 聚合正确（2 条链共享 → true）。
- `src/backend/internal/dispatch/endpoint_test.go`：join 后 `ReconcileSharedEndpoint` 聚合两链路由/用户。
- `src/backend/internal/sub`：joined 链订阅渲染端点参数（扩展 publish_chain_proxies_test）。
- 回归：`go test ./internal/store/... ./internal/panel/... ./internal/dispatch/... ./internal/sub/...`。
