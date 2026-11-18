# 链路跳位自动编排图 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用「每跳一张以原服务器为根的编排图」替换线性备选池与全链路策略开关，并提供详情页堆叠、可视化编排编辑器、每链编排 worker 与删除清理。

**Architecture:** 后端新增 `chain_hop_graphs/nodes/edges`（schema v34）替换未发布的 v33 备选/策略表；图由服务器节点与触发器节点交替组成，保存时做 DAG/回切环/唯一性校验；编排器改为按图求值，每条链一个 worker + 状态机串行化切换；删除流程先暂停 worker 再清图/失效/入队清理。前端详情页用堆叠卡片展示，点击打开全屏编排编辑器（复用链路画布的视口/拖拽/连线/边路由与服务器卡片）。

**Tech Stack:** Go 1.27 + SQLite（database/sql）、React 19 + TypeScript + Vite、Vitest、agent-browser 截图验收。

**Spec:** `docs/superpowers/specs/2026-11-18-chain-hop-orchestration-graph-design.md`

## Global Constraints

- schema 版本 v34；v33 未发布（0.1.47 = v32），删除 `chain_hop_candidates`/`chain_auto_policies`，保留 `server_pair_watch`/`chain_failover_events`。
- 不引入新依赖；请求统一走现有 `requester`；沿用现有链路状态机、Revision Planner、dispatcher 与 `cleanup_pending`。
- 触发器：疑似封禁（suspected/banned 读现有 5/10 常量）、确认封禁、上一跳超时、下一跳超时、原节点恢复；不提供 Agent 离线/配额触发器。
- priority：0=自动，1..9999 显式；排序 显式升序 → 类型序（恢复>疑似>确认>上一跳>下一跳）→ ordinal。
- 图：以根为起点的 DAG；唯一允许的环是经触发器回到根；非根可多来源；保存要求当前运行服务器在图中；有在途修订拒绝保存。
- 删除：先暂停/停止 worker；失效只看当前工作跳；历史 revision 只做清理；图清空不自动修补。
- 提交前必须通过：`go test ./...`、`go vet ./...`、`npm run format:check`、`npm run lint`、`npm test`、`npm run build`。
- UI 验收截图不提交 Git，最终报告内联展示。

## Review Focus

1. 删除服务器与在途切换并发：pause 超时后不得再生成新 revision；删除后不得留下指向已删服务器的图/命令。
2. 链路编辑失败/回滚时不得清空编排图；只有发布成功的那次拓扑替换才协调图。
3. 原节点恢复触发器在根不健康时不得切回；与替补自身触发器同时命中时按 priority 仲裁，不得双切。
4. 编排器内改地址/端口后，链路编辑向导、订阅/部署不得出现“两套值”；端口越界必须拒绝切换。
5. 同一链并发唤醒（延迟/遥测/ban/tick）只能产生一个在途 revision；deploying 中不得重复切换。

---

### Task 1: Schema v34 与编排图存储

**Files:**
- Modify: `src/backend/internal/store/store.go`（三张表 + 索引 + `chains.orchestration_error`）
- Modify: `src/backend/internal/store/migrations.go`（`schemaVersion=34`、删 v33 表）
- Create: `src/backend/internal/store/chain_graph.go`
- Modify: `src/backend/internal/store/chains.go`（DeleteChain 级联）
- Modify: `src/backend/internal/store/servers.go`（DeleteServerCascade 级联图）
- Modify: `src/backend/internal/store/revisions.go`（ReplaceWorkingChainTopology 内协调图）
- Delete: `src/backend/internal/store/chain_auto.go`
- Test: `src/backend/internal/store/chain_graph_test.go`

**Interfaces:**
- `type ChainHopGraph struct { ChainID int64; Seq int; CooldownSec int; LastSwitchAt *time.Time; Nodes []ChainHopGraphNode; Edges []ChainHopGraphEdge }`
- `type ChainHopGraphNode struct { ID int64; Kind string; ServerID int64; Address string; ListenPort int; Subject string; Action string; Priority int; IsRoot bool; Ordinal int }`
- `type ChainHopGraphEdge struct { FromNode, ToNode int64 }`
- `func (s *Store) ReplaceChainHopGraph(ctx, chainID int64, seq int, nodes []ChainHopGraphNode, edges []ChainHopGraphEdge) (*ChainHopGraph, error)`
- `func (s *Store) ChainHopGraphs(ctx, chainID int64) (map[int]ChainHopGraph, error)`
- `func (s *Store) DeleteChainHopGraph(ctx, chainID int64, seq int) error`
- `func (s *Store) ClearChainHopGraphsForServer(ctx, serverID int64) ([]ChainHopGraphRef, error)`
- `func reconcileChainHopGraphsTx(tx *sql.Tx, chainID int64, olds []ChainHop, news []ChainRevisionHop) error`

- [ ] Step 1: 写失败测试：替换/读取图、根唯一/服务器唯一索引、priority/ordinal 排序、删链级联、删服务器清图、拓扑变化协调（换服务器清图、新服务器==根保留、删跳清图）。
- [ ] Step 2: 跑 `go test ./internal/store -run 'ChainHopGraph' -v`，确认 FAIL。
- [ ] Step 3: 实现表、迁移、CRUD、级联与 `ReplaceWorkingChainTopology` 协调。
- [ ] Step 4: 跑测试通过；`go test ./internal/store` 全绿。
- [ ] Step 5: 提交 `feat(chain): add hop orchestration graph storage`。

### Task 2: 保存校验（DAG/回切/唯一性/优先级）

**Files:**
- Create: `src/backend/internal/panel/orchestration_graph.go`
- Test: `src/backend/internal/panel/orchestration_graph_test.go`

**Interfaces:**
- `func validateHopOrchestration(ctx context.Context, st *store.Store, chain *store.Chain, hops []store.ChainHop, seq int, graph store.ChainHopGraph) error`
- `func buildGraphFromRequest(req hopOrchestrationSaveRequest, seq int) (store.ChainHopGraph, error)`
- 规则严格按 spec §4 的 12 条；错误消息带 `第 N 跳：`。

- [ ] Step 1: 表驱动失败测试覆盖 12 条规则（含非根多来源允许、恢复触发器无来源且目标=根、非根环拒绝、priority 越界、当前服务器不在图、部署中拒绝）。
- [ ] Step 2: 跑测试确认 FAIL。
- [ ] Step 3: 实现校验与请求→图转换。
- [ ] Step 4: 测试通过。
- [ ] Step 5: 提交 `feat(chain): validate hop orchestration graphs`。

### Task 3: DTO、保存/手动切换接口与旧字段移除

**Files:**
- Modify: `src/backend/internal/panel/chains.go`、`panel.go`
- Modify: `src/backend/internal/panel/auto_orchestrator.go`
- Modify: `docs/openapi.yaml`
- Test: `src/backend/internal/panel/orchestration_handler_test.go`

**Interfaces:**
- DTO：`chainHopDTO.Orchestration *chainHopOrchestrationDTO`，节点含 id/kind/server_id/address/listen_port/subject/action/priority/is_root/active/alias/country_code/location/online/banned；`chainDTO.OrchestrationError string`。
- `POST /api/chain/hop-orchestration/save`：`{chain_id, seq, nodes:[{kind,server_id,is_root,address,listen_port,subject,action,priority}], edges:[{from,to}]}`，返回 chainDTO。
- 保存时：校验 → 整图替换 → 若节点对应当前工作服务器，写回 `chain_hops.address/forward_port`；有在途修订返回 `INVALID_ARGUMENT`。
- `switch-hop`：有图限图成员，无图允许任意合法服务器。

- [ ] Step 1: 失败测试：保存成功/校验失败/部署中拒绝/地址端口写回/DTO 形状/手动切换两种模式。
- [ ] Step 2: 跑测试确认 FAIL。
- [ ] Step 3: 实现 DTO、handler、路由、旧字段删除、OpenAPI。
- [ ] Step 4: 测试通过；`go test ./internal/panel`。
- [ ] Step 5: 提交 `feat(chain): expose hop orchestration API`。

### Task 4: 编排器按图求值（触发/优先级/仲裁/健康）

**Files:**
- Modify: `src/backend/internal/panel/auto_orchestrator.go`
- Test: `src/backend/internal/panel/auto_orchestrator_test.go`

**Interfaces:**
- `type hopTriggerHit struct { Seq int; Trigger store.ChainHopGraphNode; Target store.ChainHopGraphNode; Rank int }`
- `func (o *AutoOrchestrator) evaluateChainGraphs(ctx, chain, hops, graphs, snap) (hopTriggerHit, bool)`
- `func triggerFires(snap, graph, trigger, currentServerID, seq, totalHops) (bool, string)`
- `func (o *AutoOrchestrator) switchTarget(ctx, chain, seq, target, reasonCode, detail) (*store.ChainRevision, error)`
- 排序：显式 priority → 类型序 → ordinal；跨跳仲裁优先探测目标端；每轮最多一跳；冷却；切换前地址/端口校验。

- [ ] Step 1: 失败测试：疑似/确认、疑似先于确认、priority 覆盖、上一跳/下一跳、恢复触发器（根健康/不健康）、多来源、仲裁、冷却、端口越界。
- [ ] Step 2: 跑测试确认 FAIL。
- [ ] Step 3: 实现求值与切换。
- [ ] Step 4: 测试通过。
- [ ] Step 5: 提交 `feat(chain): evaluate hop orchestration graphs`。

### Task 5: 每链 worker 与状态机

**Files:**
- Create: `src/backend/internal/panel/orchestrator_worker.go`
- Modify: `src/backend/internal/panel/auto_orchestrator.go`、`panel.go`、`cmd/backend/main.go`
- Test: `src/backend/internal/panel/orchestrator_worker_test.go`

**Interfaces:**
- `type chainWorkerState int`（idle/evaluating/switching/deploying/cooldown/backoff/failed/paused/stopped）
- `func newChainWorkerManager(o *AutoOrchestrator) *chainWorkerManager`
- `func (m *chainWorkerManager) Wake(chainID int64)`、`Pause(ctx, chainID, reason) error`、`Resume(chainID)`、`Stop(chainID)`、`WaitSettled(ctx, chainID, timeout) error`
- **只为有图的链建 worker**（启动/保存图时 ensure，删最后一图时 Stop；无图链不建）。
- 扫描顺序：每次唤醒按 seq 升序 A→B→C；一跳切换发布完成后再评估下一跳；跳内按 priority/类型序/ordinal。
- 顺序冲突（B 的图在新拓扑下不合法）或目标不可用 → `failed`：不回滚、不自动重试、不清图，写 `chains.orchestration_error` + `chain_orchestration_failed` 告警；保存图/编辑链路清除并恢复。
- 同链唤醒合并；任何时刻最多一个在途切换；重启从 chain status/desired/last_switch_at 推导。

- [ ] Step 1: 失败测试：并发 Wake 只产生一个 revision；deploying 不重复；Pause 后不再切换；Resume 恢复；Stop 后不建 worker；无图链不建 worker；A→B 顺序；A 完成后 B 失配进入 failed 且保留现场；重启推导。
- [ ] Step 2: 跑测试确认 FAIL。
- [ ] Step 3: 实现 worker/manager 并接线事件。
- [ ] Step 4: 测试通过；`go test -race ./internal/panel`。
- [ ] Step 5: 提交 `feat(chain): serialize orchestration per chain worker`。

### Task 6: 删除与清理协调

**Files:**
- Modify: `src/backend/internal/panel/servers.go`、`panel/chains.go`
- Modify: `src/backend/internal/store/revisions.go`、`store/servers.go`、`store/chain_graph.go`
- Test: `src/backend/internal/panel/orchestration_delete_test.go`

**Interfaces:**
- `func (s *Store) ChainsWithWorkingHopOnServer(ctx, id) ([]Chain, error)`
- `func (s *Store) ChainsWithGraphNodeOnServer(ctx, id) ([]ChainHopGraphRef, error)`
- `func (s *Store) ChainDeploymentPieces(ctx, chainID) ([]store.ChainHopPieceRef, error)`（跨全部部署 revision 去重）
- 删除服务器：Pause → 当前工作跳 invalidate+stop / 图节点清图+resume → 清理入队 → uninstall → cascade → 摘要 DTO。
- 删除链路：Stop → 全部 revision pieces 入队 → 软删除 → 操作日志。
- 操作事件 `chain.orchestration_cleared`、`chain.cleanup_queued`。

- [ ] Step 1: 失败测试：仅图节点不失效、当前跳失效、历史 revision 不失效、删链覆盖历史 pieces、pause 与切换竞态、重复删除幂等。
- [ ] Step 2: 跑测试确认 FAIL。
- [ ] Step 3: 实现查询、协调、事件。
- [ ] Step 4: 测试通过。
- [ ] Step 5: 提交 `feat(chain): coordinate orchestration on deletions`。

### Task 7: 前端数据层与旧配置移除

**Files:**
- Modify: `src/frontend/src/lib/types.ts`、`lib/api.ts`
- Modify: `src/frontend/src/pages/chains/use-chain-form.ts`
- Modify: `src/frontend/src/pages/chains/chain-wizard/ChainDetailsStep.tsx`
- Modify: `src/frontend/src/pages/chains/chain-wizard/ChainNodeCard.tsx`、`ChainCanvas.tsx`
- Test: 对应 `*.test.ts(x)`

**Interfaces:**
- `ChainHop.orchestration?: ChainOrchestration`；`ChainOrchestration { root_server_id; active_server_id; triggered; nodes: OrchestrationNode[]; edges: {from,to}[] }`
- `api.saveHopOrchestration(body)` → `/api/chain/hop-orchestration/save`
- 删除 `ChainAutoPolicy/ChainHopCandidate*` 与提交字段。

- [ ] Step 1: 更新/新增失败测试：建链/编辑载荷不再含 candidates/auto_policy；新类型编译失败。
- [ ] Step 2: 跑 vitest 确认 FAIL。
- [ ] Step 3: 改类型、api、表单、向导移除旧 UI。
- [ ] Step 4: `npx vitest run` 通过；`npm run build`。
- [ ] Step 5: 提交 `feat(chains): switch frontend to orchestration graphs`。

### Task 8: 详情页堆叠（HopStack）

**Files:**
- Create: `src/frontend/src/pages/chains/HopStack.tsx`、`src/frontend/src/pages/chains/hop-stack.ts`
- Modify: `src/frontend/src/pages/Chains.tsx`、`pages/chains.css`
- Test: `hop-stack.test.ts`、`HopStack.test.tsx`

**Interfaces:**
- `func stackServers(orchestration): { server: OrchestrationNode; active: boolean }[]`（BFS、去重、最多 4）
- 组件 props：`{ hop, orchestration, onEdit, onSwitch }`；黄点 `aria-expanded`；前卡根、active 标记「编排已触发」；`+N`。

- [ ] Step 1: 失败测试：BFS 顺序（多来源去重）、最多 4、+N、active 标记、无图单卡、黄点展开。
- [ ] Step 2: 跑测试确认 FAIL。
- [ ] Step 3: 实现纯函数、组件与样式。
- [ ] Step 4: 测试通过。
- [ ] Step 5: 提交 `feat(chains): render hop orchestration stack`。

### Task 9: 编排编辑器

**Files:**
- Create: `src/frontend/src/pages/chains/orchestration/OrchestrationEditor.tsx`、`OrchestrationCanvas.tsx`、`TriggerNode.tsx`、`orchestration-graph.ts`、`orchestration.css`
- Modify: `src/frontend/src/pages/Chains.tsx`
- Test: `orchestration-graph.test.ts`、`OrchestrationEditor.test.tsx`

**Interfaces:**
- 纯函数：`createEditorState(orchestration)`、`reduce(state, action)`、`validateGraph(state)`、`autoLayout(state)`、`toSavePayload(state)`
- actions：add-server/remove-server/add-trigger/remove-trigger/connect/disconnect/move/set-priority/set-address/set-port
- 触发器 5 类（恢复添加即连根、默认 priority=1）；服务器卡片复用 `ChainNodeCard` 变体；保存调用 `api.saveHopOrchestration`。
- 链路详情展示 `orchestration_error` 红条（「自动编排已暂停：…；修改并保存编排后恢复」），保存图后清除。

- [ ] Step 1: 失败测试：连接规则、DAG/环、多来源、priority 排序、布局、保存载荷、当前节点删除拒绝。
- [ ] Step 2: 跑测试确认 FAIL。
- [ ] Step 3: 实现纯函数、画布、palette、弹窗与保存。
- [ ] Step 4: 测试通过；`npm run build`。
- [ ] Step 5: 提交 `feat(chains): add orchestration editor`。

### Task 10: 链路编辑器联动（删除提醒与地址端口同步）

**Files:**
- Modify: `src/frontend/src/pages/chains/chain-wizard/ChainCanvas.tsx`、`use-chain-form.ts`、`Chains.tsx`
- Test: 对应组件测试

**Interfaces:**
- 删除启用编排的节点：确认框文案「该跳的自动编排配置将一并删除」；删除最后一跳的图后 worker 停止。
- 编排保存后：链路 DTO 的 `chain_hops` 地址/端口已更新，向导回填即看到。

- [ ] Step 1: 失败测试：删除确认文案、删除后提交载荷不含该跳、编排保存后回填当前节点地址/端口。
- [ ] Step 2: 跑测试确认 FAIL。
- [ ] Step 3: 实现确认弹窗与回填。
- [ ] Step 4: 测试通过。
- [ ] Step 5: 提交 `feat(chains): link orchestration edits with chain editor`。

### Task 11: 文档、OpenAPI、端到端与验收

**Files:**
- Modify: `docs/CHANGELOG.md`、`docs/openapi.yaml`、`src/frontend/src/lib/api-contract.generated.ts`

- [ ] Step 1: 重新生成契约、补 OpenAPI schema。
- [ ] Step 2: 全量门禁（go test/vet、format/lint/test/build）。
- [ ] Step 3: 本地构建面板：建链 → 保存含恢复/回切边的图 → 注入 ban → 验证切换/堆叠/编辑器 → 删除节点/服务器 → 验证清理。
- [ ] Step 4: 截图并内联报告。
- [ ] Step 5: 提交 `feat(chains): complete hop orchestration graph`。

## Self-Review

- Spec coverage：§1 概念（T1/T2/T4）、§2 触发器（T2/T4/T9）、§3 存储（T1）、§4 校验（T2）、§5 编排器+worker（T4/T5）、§6 API（T3）、§7 堆叠（T8）、§8 编辑器（T9）、§9 迁移（T1/T11）、§10 测试（各任务+T11）、§12 删除（T6）、链路编辑器提醒（T10）。
- 类型一致性：store 类型与 DTO、前端类型在 T1/T3/T7 定义，后续任务只引用。
- Review Focus 五类风险分别由 T5、T1、T4、T3+T10、T5 的测试覆盖。
