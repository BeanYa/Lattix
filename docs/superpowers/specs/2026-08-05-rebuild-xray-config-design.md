# 重建 xray 配置（xray.rebuild）设计

日期：2026-08-05

## 背景

xray 升级 26.3.27 → 26.7.28 后，realitySettings 缺省 `minClientVer` 时默认 26.3.27，会拒绝旧客户端（mihomo/clash）。面板与 agent 已补齐兼容性处理（`pinRealityMinClientVer` 注入、模板迁移 `minClientVer: "0"`），但**既有部署的 xray.json 是升级前生成的文件**，不会自动获得该字段——即使升级 xray 二进制，已有端口配置也不会改变。

**目的不限于 miniclientver**：xray 版本升级后可能陆续出现其他"缺省值变化/新字段缺失"类兼容问题（如本次 minClientVer 一样），旧文件一律不会自动补全。本功能提供"重建 xray 配置"：按当前生效的链路与用户配置重新生成 xray.json，并内建**可扩展的全配置规范化扫描**——凡"升级后需要补全的字段"类问题，只需追加一个规范化器，重建即生效。

## 目标

- 面板一键触发（服务器须在线），agent 原子执行完整动作序列：
  停止 xray → 备份 xray.json → 按面板下发配置重建 → 校验 → 重启 → 自检 → 成功回执。
- 任一步失败：恢复备份 xray.json、重启 xray、向面板报错。
- 重建不破坏现有客户端：Reality 私钥、decryption、监听端口均复用当前生效值。

## 非目标

- 不改动 `node.apply` / `chain-hop.apply` / `shared-endpoint.apply` 常规下发流程。
- 不改动 `xray.cleanup`、`loadConfig` 自愈（缺失/损坏/drift）逻辑。
- 不支持离线排队执行（服务器须在线）。
- 不新增"面板渲染完整 xray.json"能力（私钥不出服务器，§7）。

## 方案

新消息类型 `xray.rebuild`，完全复用 `xray.cleanup` 的同步下发模式：
面板从 DB 收集数据 → dispatcher 同步下发并等待 → agent 原子执行 → 回执。
链路与共享端点 pieces 不重新下发，由 agent 本地 `state.ChainPieces` 重放（已持久化渲染结果与私钥，公钥不变、bridge 凭证不失效）。

## 协议（src/shared/messages.go）

```go
TypeRebuildXray = "xray.rebuild"

// 面板下发：该服务器所有活跃节点的完整 apply 规格 + 期望状态快照
type RebuildXrayPayload struct {
    Nodes               []ApplyNodePayload // 复用：node_id + VirtualConfig + user_uuids + dest/port_candidates
    ExpectedInboundTags []string           // 自检用（store.ExpectedXrayState 现有计算）
    ExpectedPieces      []string           // 自检用（"forward/7"、"portal/9"…）
}

// agent 回执；错误原因由信封 code/message 表达
type RebuildXrayResult struct {
    RebuiltInbounds []RebuiltInbound // {tag, port, kind} 重建后的监听摘要
    RebuiltPieces   []string         // 重建后的链路 piece 集合
    RolledBack      bool             // 是否失败回滚
}
```

`ApplyResultPayload` 增加 `Rebuild *RebuildXrayResult` 字段（回执 data 挂载点）。

## Agent 执行流程

新方法 `Manager.RebuildXray(p shared.RebuildXrayPayload)`（新文件 `src/agent/internal/xray/rebuild.go`），全程持 `m.mu`：

1. **停止 xray**：`runner.Stop()`。`SystemdRunner` 新增 `Stop`（`systemctl stop xray`），`ExecRunner` 新增 `Stop`（kill 子进程）。
2. **备份**：xray.json 原子拷贝为 `xray.json.rebuild.bak`（同目录，保留权限）。文件不存在则跳过（重建纯生成）。
3. **重建候选配置**：
   - 骨架 `skeleton()`（api/stats/policy/log + direct outbound）。
   - 逐个 `Payload.Nodes` 重渲染：`fillTemplate` 增加"保留模式"——按 tag 从备份文件提取现有 `node_` inbound 的 Reality `privateKey`、`decryption`、监听端口，替换占位符时复用而非重新生成/重选；备份缺失该 inbound 时回退现有逻辑（`x25519()`/`vlessEnc()`/`pickPort()`）。`minClientVer` 由 `pinRealityMinClientVer` 兜底注入（模板本身已含迁移后的 `minClientVer: "0"`）。
   - `mergePieces(m.chainPieces)` 重放链路/共享端点 pieces（字节级原样——旧 piece 不含升级后新增字段）。
   - **规范化扫描（全配置）**：`normalizeRebuiltConfig(cand)` 依次应用可扩展的规范化器列表（`normalizers`），对最终配置全部 inbound 生效——重放的链路/共享端点 piece 同样补全缺失字段（首项：`normalizeRealityMinClientVer`，复用 `pinRealityMinClientVer` 遍历注入；幂等，node 重渲染已注入时无操作）。未来升级后新增字段：追加一个规范化器函数即可，无需改协议。
4. **校验**：候选写临时文件 + `xray run -test`（复用 `commitConfig` 的校验逻辑，但不经 commitConfig 的 .prev 路径）。
5. **原子落盘**：rename 至 xray.json、chmod 0600、更新 `lastHash`、清 `drifted`。
6. **重启**：`runner.Restart()`。
7. **自检**：加载新配置，确认全部 `ExpectedInboundTags` 为 inbound、全部 `ExpectedPieces` 在 `m.chainPieces`、xray 进程存活。通过 → 删除 `.bak`，回执 `RebuiltInbounds`/`RebuiltPieces`。
8. **任一步失败**：恢复 `xray.json.rebuild.bak`（原子 rename 回）、重启 xray、更新 `lastHash`，回执错误（信封 code/message）且 `RolledBack=true`。回滚本身失败时，错误信息显式标注"回滚失败，备份位于 xray.json.rebuild.bak，需人工处理"。

崩溃安全：中途崩溃时 xray.json 要么是旧文件、要么是已通过校验的新文件；`.bak` 残留不影响下次运行（重跑覆盖）。

## 面板 API 与前端

```
POST /api/server/rebuild-xray   body: { "server_id": N }
```

- 仿 `handleCleanupXray`（servers.go:805-850）：
  - 服务器存在性检查；`s.req.IsOnline(id)` 离线返回 409。
  - 收集数据：`ListNodes` 过滤 `ServerID == id && Status == active`，解 `ConfigTemplate` 为 `VirtualConfig`，查用户 UUID 列表（与 dispatch 拼 `apply_node` 载荷同一数据源）→ `Nodes`；`store.ExpectedXrayState` → 期望 tag/pieces。
- 新增 dispatcher `RebuildXraySync`（仿 `CleanupXraySync`），超时 90s（含停服/重启）。
- 审计：`server.rebuild_xray`，记录重建 inbound/piece 数与是否回滚。
- 前端：Servers 页"重建 xray 配置"按钮 + 确认对话框（仿 cleanup 对话框）；`RolledBack=true` 时红色提示"重建失败，已恢复原配置"并展示信封 message 中的原因；成功展示重建后的监听/piece 清单。
- 契约：`docs/openapi.yaml`、`api-contract.generated.ts`、`api.ts`、`types.ts` 同步更新。

## 错误处理矩阵

| 失败点 | 处理 |
|---|---|
| 备份失败 | 恢复重启 xray，报错（服务不停留） |
| 模板渲染失败（dest 不可达等） | 回滚 + 重启 + 报错（含节点信息） |
| `xray run -test` 校验不过 | 回滚 + 重启 + 报错（含校验输出） |
| 落盘 / 重启失败 | 回滚 + 重启 + 报错 |
| 自检缺 inbound / piece | 回滚 + 重启 + 报错（列出缺失 tag/piece） |
| 回滚本身失败 | 报错并标注"需人工处理备份文件" |

## 测试

- **agent 单元测试**（`src/agent/internal/xray/rebuild_test.go`，用 ExecRunner + 假 xray 二进制，沿用现有测试模式）：
  - 成功重建：骨架 + node inbounds + pieces 齐全，期望 tag/piece 全命中；
  - 渲染失败、校验失败、自检缺失 → 回滚断言（xray.json 恢复为备份内容、服务重启、RolledBack=true）；
  - 备份缺失（xray.json 不存在）场景；
  - 私钥/decryption/端口复用断言：备份中的值出现在新配置，未重新生成；
  - minClientVer 注入断言：旧模板重渲染后新配置含 `minClientVer: "0"`；
  - **规范化扫描断言**：重放的旧链路 portal piece（无 minClientVer）经重建后同样被注入 `minClientVer: "0"`；规范化幂等（二次执行无变化）。
- **面板测试**：`handleRebuildXray` 载荷组装（活跃 node + 用户 + 期望集）、离线 409、`RebuildXraySync` 超时/失败路径。
- **前端**：类型生成 + 按钮/对话框渲染冒烟（现有测试框架内）。

## 涉及文件

- `src/shared/messages.go`（消息类型、载荷、回执、ApplyResultPayload）
- `src/agent/internal/xray/rebuild.go`（新：RebuildXray）
- `src/agent/internal/xray/fill.go`（fillTemplate 保留模式）
- `src/agent/internal/xray/manager.go`（备份/恢复辅助，供 rebuild 复用）
- `src/agent/internal/xray/runner.go`（SystemdRunner/ExecRunner 增加 Stop）
- `src/agent/cmd/agent/main.go`（handle() 分发 xray.rebuild）
- `src/agent/cmd/agent/command_queue.go`（新消息类型入队优先级：与 cleanup 同级 default）
- `src/backend/internal/panel/servers.go`（handleRebuildXray）、`panel.go`（路由）
- `src/backend/internal/dispatch/dispatcher.go`（RebuildXraySync）
- `src/backend/internal/store/xray_cleanup.go`（复用 ExpectedXrayState，不改）
- `src/frontend/...`（按钮、对话框、类型、api.ts）
- `docs/openapi.yaml`、`src/shared/server_settings_test.go` 模式外的新增测试文件

## 迁移与兼容

- 无 DB schema 变更。
- 旧 agent 收到 `xray.rebuild` 会按未知类型忽略，面板侧因 agent 版本未知属可接受边界（与既有消息一致）；服务器在线检查已保证命令可达。
