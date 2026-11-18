# 链路跳位自动编排图（设计）

> 需求：把「每跳一组线性备选 + 全链路策略开关」换成**每跳一张可编辑的编排图**：
> 详情页以堆叠卡片展示；卡片上的黄点负责展开/收起；点击堆叠打开编排编辑器；
> 触发器按「主体 × 条件」区分，并带优先级；服务器卡片带地址与端口；保存时校验
> 图的形状。新增全局「原节点恢复」触发器；每条链一个独立 worker + 状态机串行化编排；
> 编排器内修改节点地址/端口要同步到链路编辑器。订阅页不变。

## 1. 概念模型

### 1.1 每跳一张图，根是该跳的主服务器

- 编排图的粒度是**一个跳位（chain_id, seq）**，与链路的跳一一对应。
- 图的**根节点**是该跳的「原服务器」（首次保存时该跳正在使用的服务器）。
  之后即使故障切换到替补，根也不变；替补卡片上标记「编排已触发」，编辑器里
  标记「当前使用」。
- 详情页堆叠的**前卡永远是根服务器**；后卡按编排图的 BFS 顺序排列（最多 4 张）。

### 1.2 两类节点与交替的边

```
   [根服务器] --触发器 A--> [替补 B] --触发器 C--> [替补 D]
        |                        |
        +--触发器 E--> [替补 F]  +--触发器 G--> [根服务器]   ← 回切（唯一允许的环）
```

- 节点：`server`（服务器，含 address/listen_port）与 `trigger`（触发器）。
- 边只能是 `server → trigger` 或 `trigger → server`。
- 一个 `server` 可以连多个 `trigger`；一个 `trigger` 只能连一个 `server`
  （出度恰为 1）。
- 普通触发器有且只有一个来源 `server`（入度=1），该 server 即「所属服务器」。
  **「原节点恢复」是全局触发器**：没有来源服务器（入度=0），只能连到根。
- 非根 `server` 允许有多个来源触发器（例如「疑似封禁」与「确认封禁」都指向
  同一台 B），因此图是**以根为起点的 DAG**，不是严格单亲的树。
- **唯一允许的环是经触发器回到根**（回切）：删除所有「→根」的边后，剩余图必须
  无环且覆盖全部节点；根可以有多条入边（多个回切路径 + 原节点恢复）。
- 同一台服务器在一个图里只能出现一次；同一个「所属服务器 + 主体 + 条件」只能
  有一个触发器。多个条件汇入同一台替补是允许的。

### 1.3 详情页堆叠（摘要）

- 前卡：根服务器（保留现有的跳位编号、角色、状态、地址、端口）。
- 后卡：其余服务器按 BFS 顺序（根 → 根的触发器顺序 → 下一层……）。
- 当前实际运行的服务器若 != 根，在它的卡片上打「编排已触发」标记，并在堆叠头部
  显示「当前运行：B」。
- 最多显示 4 张；更多时显示前 3 张 + 「+N」。
- 卡片上的**黄点**：展开/收起堆叠（展开时露出空白占位卡，最多 4 个位置）。
- 点击堆叠（非黄点）打开该跳的编排编辑器。
- 没有编排图的跳：单张卡片 + 黄点；点黄点展开空白占位，点占位/卡片打开空图编辑器。
- 订阅页不读取编排图，行为不变。

## 2. 触发器

### 2.1 清单

| 主体 | 条件 | 判定（读取现有健康快照） | 可用位置 |
|---|---|---|---|
| 本机 | 疑似被 ban | `server_ban_watch.state ∈ {suspected, banned}`（面板连续 5 轮全丢包） | 任意托管跳；外部直链/非直连不提供 |
| 本机 | 被 ban | `state = banned`（连续 10 轮全丢包） | 同上 |
| 上一跳 | 上一跳超时 | 与 seq-1 之间的有向探测连续 ≥3 轮超时，且探测发起方 Agent 在线 | seq ≥ 1 |
| 下一跳 | 下一跳超时 | 与 seq+1 之间的有向探测连续 ≥3 轮超时，且探测发起方 Agent 在线 | seq < 跳数-1 |
| 全局 | 原节点恢复 | 当前运行 != 根，且根通过目标健康检查（在线/未封禁/配额未耗尽/与邻跳可达） | 每图最多 1 个；添加时置于根左侧并自动连根入口 |

- 5/10 沿用面板现有的 `banSuspectStreak` / `banBannedStreak` 常量；触发器只读
  ban 状态，不引入每节点阈值参数。
- 3 轮沿用现有 `defaultPairUnreachableStreak`；tooltip 中标注。
- **本期不提供 Agent 离线触发器**（版本升级会大面积离线），也不提供配额触发器；
  模型保留扩展位（`subject`/`action` 两个维度）。
- 「上一跳/下一跳」是链路逻辑位置；direct/reverse/encrypted 的实际探测方向由后端
  按已发布修订解析，编辑器不暴露。
- **疑似先于确认**：同一服务器同时存在两类触发器时「疑似」先触发（5 轮即切）；
  两者可以指向同一台 B（DAG 允许多来源）。只有「疑似」时，状态为 banned 也算命中。
- **优先级**：每个触发器带 `priority`；0 = 自动，1..9999 = 显式（越小越先）。
  排序：显式 priority 升序 → 自动按类型序（恢复 > 疑似 > 确认 > 上一跳 > 下一跳）
  → ordinal。这样「疑似」默认先于「确认」；显式优先级可以覆盖这个默认。
  原节点恢复添加时默认 priority=1（最高），可再改。
- 原节点恢复的默认位置在根左侧、自动连到根的入口；与其它指向根的触发器并存。

### 2.2 视觉区分（形状为主、颜色为辅）

- 触发器统一橙色家族（延续「橙色=触发器」）。
- 主体用形状 + 图标 + 文字三重冗余：
  - 本机：六边形/盾牌图标 + 「本机」；
  - 上一跳：卡片左侧向左缺口（◀）+ ← 图标 + 「上一跳」；
  - 下一跳：卡片右侧向右缺口（▶）+ → 图标 + 「下一跳」。
- 原节点恢复：菱形 + ↺（回归）图标 + 「原节点恢复」，默认置于根左侧；
- 可选的辅助色条（仅强化，不独立承载语义）：本机琥珀、上一跳紫、下一跳青、
  恢复金；两套主题都要通过现有 token 对比度检查。

## 3. 存储（schema v34）

当前 `v33`（备选池 + 策略）未在任何已发布版本中运行（0.1.47 = v32），因此 v34
直接替换它：

- 删除：`chain_hop_candidates`、`chain_auto_policies`（连同 Go 代码/API 字段）。
- 保留：`server_pair_watch`（段间探测）、`chain_failover_events`（审计）。
- 新增：

```sql
CREATE TABLE chain_hop_graphs (
    chain_id       INTEGER NOT NULL REFERENCES chains(id) ON DELETE CASCADE,
    seq            INTEGER NOT NULL,
    cooldown_sec   INTEGER NOT NULL DEFAULT 300,
    last_switch_at DATETIME,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (chain_id, seq)
);

CREATE TABLE chain_hop_graph_nodes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id        INTEGER NOT NULL REFERENCES chains(id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    kind            TEXT NOT NULL,              -- server | trigger
    server_id       INTEGER REFERENCES servers(id) ON DELETE CASCADE,
    address         TEXT NOT NULL DEFAULT '',
    listen_port     INTEGER NOT NULL DEFAULT 0,
    trigger_subject TEXT,                      -- self | prev | next | root
    trigger_action  TEXT,                      -- ban_suspected | ban_banned | segment_timeout | recovered
    priority        INTEGER NOT NULL DEFAULT 0, -- 0=自动（ordinal），1..9999 显式
    is_root         INTEGER NOT NULL DEFAULT 0,
    ordinal         INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_chain_hop_graph_nodes ON chain_hop_graph_nodes(chain_id, seq, ordinal);
CREATE UNIQUE INDEX idx_chain_hop_graph_root ON chain_hop_graph_nodes(chain_id, seq) WHERE is_root = 1;
CREATE UNIQUE INDEX idx_chain_hop_graph_server ON chain_hop_graph_nodes(chain_id, seq, server_id) WHERE kind = 'server';

CREATE TABLE chain_hop_graph_edges (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id  INTEGER NOT NULL REFERENCES chains(id) ON DELETE CASCADE,
    seq       INTEGER NOT NULL,
    from_node INTEGER NOT NULL REFERENCES chain_hop_graph_nodes(id) ON DELETE CASCADE,
    to_node   INTEGER NOT NULL REFERENCES chain_hop_graph_nodes(id) ON DELETE CASCADE,
    UNIQUE (chain_id, seq, from_node, to_node)
);
CREATE INDEX idx_chain_hop_graph_edges ON chain_hop_graph_edges(chain_id, seq);
```

- 保存是**整图替换**（事务内 DELETE 旧图 + INSERT 新图）。
- 链删除级联；服务器删除时清空引用它的跳的整张图（链本身会因失效被标记）。
- 链路编辑的协调规则（`chain_hops` 会整体重建，seq 可能位移）：
  1. 编辑后逐 seq 比较新旧服务器：新服务器 == 该 seq 既有图的根 → 保留（回切主节点）；
     否则清空该 seq 的图。
  2. 删除 seq ≥ 新跳数的图。
  3. 若保留的图引用了某跳新的主服务器（跨跳复用），清空该图。
  4. 服务器未变（只改地址/端口）时保留图，并把当前服务器节点的 address/listen_port
     同步为编辑后的值。
  读路径再做一次防御：根服务器不存在、或当前服务器不在图中 → 视为无编排。
- 图不持久化节点坐标；打开编辑器时按 BFS 自动布局。

## 4. 保存校验

后端在同一个事务里校验并落库，任何一条失败返回 `INVALID_ARGUMENT` + 中文错误：

1. 链路存在、`seq` 合法；图规模 ≤ 8 台服务器、≤ 16 个触发器。
2. 恰好一个根服务器节点；首次保存时 = 该跳当前服务器，更新时 = 存量根。
   该跳当前运行的服务器必须是图中的一个服务器节点（根或替补），否则拒绝保存——
   否则切换后编排器在“当前服务器”上找不到任何出边，该跳会永久静默失效。
   链路有在途修订（`desired_revision_id != 0`）时拒绝保存：节点地址/端口要写回
   工作拓扑，不能与部署并发修改。
3. 每个服务器节点 `server_id` 唯一、存在，且未被同链其他跳使用（包括其他跳的
   主服务器和它们编排图里的服务器）。
4. 角色兼容：入口/中转必须有入站能力且非外部节点；末跳必须与主出口同为
   托管或同为外部直链，外部出口须有可用的直链规格。
5. 地址与端口：`address` ∈ 服务器地址集合（空 = 默认地址）；`listen_port`
   在服务器可用段内（入口/末跳），中间跳允许 0（自动分配）。
6. 边：两端节点属于本图；禁止自环；`server→trigger`、`trigger→server` 交替。
   普通触发器必须有且只有一个来源 server；「原节点恢复」必须没有来源 server。
7. 度数：所有触发器出度=1；普通触发器入度=1；「原节点恢复」入度=0、每图最多
   一个、目标必须是根；非根服务器入度 ≥ 1（允许多个来源，例如疑似与确认同时
   指向 B）；根入度 ≥ 0，且入边只能来自触发器。
8. 唯一性：同一「所属服务器 + subject + action」只能一个；`priority` ∈ [0, 9999]。
9. 连通/无环：所有节点必须从根可达；删除所有「trigger→根」的边后，剩余图必须
   无环（允许经触发器回到根的环，禁止其它环）。
10. 普通触发器不能指向自己的所属服务器。
11. 根/服务器节点的 address/listen_port 按第 5 条校验；「原节点恢复」不校验目标。
12. 空图（0 节点 0 边）表示删除该跳的编排。

错误消息示例：`第 2 跳：触发器「被 ban（本机）」未连接目标服务器`、
`第 2 跳：出现非根环（只允许经触发器回到根）`、
`第 1 跳：不允许出现回到根以外的环`。

保存不触发部署；链路部署中的修订不受影响。手动切换另见 §6。

## 5. 编排器

`EvaluateChain` 改为按图评估：

1. 只处理 `active/degraded` 且无在途 desired revision 的链路（沿用现状）。
2. 载入每一跳的图与当前 `chain_hops.server_id`；没有图的跳跳过。
3. 求值：对当前服务器 C 的出边普通触发器逐个求值；若图里有「原节点恢复」且
   C != 根，再求值该全局触发器。命中项按 §2.1 的优先级排序（显式 priority →
   类型序：恢复 > 疑似 > 确认 > 上一跳 > 下一跳 → ordinal）。
4. 目标服务器必须是健康且可用的替换：在线（外部节点除外）、未被 ban、
   配额未耗尽、与邻跳探测可达、角色/地址/端口合法、未被同链其他跳使用。
   目标不健康则尝试下一个命中的触发器；全部不可用 → `chain_failover_exhausted`
   （10 分钟去重）。
   切换前再次校验目标节点的 address/listen_port：地址须仍属于该服务器（否则回退
   默认地址），端口须落在角色可用段内（入口/末跳），否则视为目标不可用——防止
   链路编辑后图里的旧端口把隧道打到服务器 NAT 段之外。
5. 扫描顺序：固定按跳序号 **A → B → C** 升序；先完成 A 的编排（切换修订发布完成），
   再评估 B，保证多跳结果不互相打架。同一跳内按触发器优先级/类型序/ordinal 选一个。
   同一轮最多一个在途切换。
6. 冷却：每跳 `cooldown_sec`（默认 300s）从 `last_switch_at` 起算，冷却内不重复切换。
7. 切换复用现有 `switchChainHop`（Make-Before-Break、新修订、审计、告警），
   目标节点的地址/端口来自图节点。原因码映射：
   ban → `server_banned`；段间超时 → `pair_unreachable`；手动 → `manual_switch`。
   事件详情追加 `trigger_subject`/`trigger_action`。
8. 回切：图里有「原节点恢复」且 C != 根、根通过健康检查时，它按优先级参与仲裁，
   命中即切回根；没有该触发器时，回切只发生在替补自身的触发器命中且指向根时。
   根不健康则视为目标不可用。
9. 没有 Agent 离线触发器：Agent 离线只作为「目标是否健康」的过滤条件。
10. 图为空/无图 → 该跳不参与自动切换；链行为与没有编排时一致。

### 5.1 每链 worker 与编排状态机

- **只为含有编排图的链创建 worker**：无图的链不建、不占 worker；删除最后一跳的图
  即停止并回收 worker；启动时只为有图的链建 worker。worker 池大小 = 有编排的链数，
  一条有图 + 一条无图时池中只有 1 个 worker。
- 每条链一个独立 worker（不是全局单循环）：ban 观察、延迟上报、遥测、Agent
  上下线、链路更新、周期 tick 都只向该链 worker 投递「唤醒」；同一链的唤醒合并，
  不并发执行。
- worker 状态机：`idle → evaluating → switching → deploying → cooldown → idle`；
  瞬时错误走 `backoff`，顺序/目标冲突进入 `failed`（暂停，保留现场，需保存图或
  编辑链路后恢复）。任何时刻同一链最多一个在途切换（同一跳/同一节点也因此最多
  一个），非 idle 期间到来的唤醒只做合并标记，回 idle 后再统一评估。
- `switching`：调用 `switchChainHop` 生成 desired revision；进入 `deploying`
  后等链路回到 `active/degraded`（复用既有链路状态机事件/周期轮询），再进
  `cooldown`；冷却结束回 `idle`。
- `backoff`：切换失败（校验/DB/网络）退避 30s 再评估，避免抖动与日志风暴。
- 进程重启：从 `chains.status`/`desired_revision_id`/`chain_hop_graphs.last_switch_at`
  推导初始状态（deploying/cooldown/idle），worker 内存状态不持久化。
- 扫描顺序：每次唤醒按 seq 升序扫描有图的跳；某跳命中并完成切换（修订发布完成）
  后，**再继续下一跳**；跳内按触发器优先级选择。
- **顺序冲突即停**：A 的切换完成后，用新拓扑重新校验 B 的图（根/当前服务器仍在图、
  跨跳不复用、无环）。若 B 不再合法，worker 进入 `failed`（暂停）：
  不回滚 A、不自动重试、不清图；写 `chains.orchestration_error` 并发出
  `chain_orchestration_failed` 告警，保留现场供管理员检查。管理员保存该跳图或
  编辑链路后清除错误并恢复 worker。
- 某跳触发但没有可用目标（目标不健康/校验失败）：同样进入 `failed` 并提示 exhausted，
  不再继续后面的跳，避免半套切换。
- 链路删除/失活时回收 worker；不同链的 worker 互不阻塞（跨链并行）；无图链永远不建。
- 测试：并发唤醒只产生一个 revision；deploying 中不重复切换；A→B 顺序；A 完成后
  B 失配→failed 且保留现场；无图链不建 worker；重启后不重复冷却；状态迁移表驱动。

## 6. API

### 6.1 DTO

`chainHopDTO` 删除 `candidates/active_priority/primary_server_id/...`，新增：

```json
"orchestration": {
  "root_server_id": 1,
  "active_server_id": 2,
  "triggered": true,
  "nodes": [
    {"id": 10, "kind": "server", "server_id": 1, "address": "", "listen_port": 443,
     "alias": "东京-入口A1", "country_code": "JP", "location": "东京",
     "kind_of_server": "managed", "online": true, "banned": false,
     "is_root": true, "active": false},
    {"id": 11, "kind": "trigger", "subject": "self", "action": "ban_banned",
     "priority": 0, "owner_server_id": 1},
    {"id": 13, "kind": "trigger", "subject": "root", "action": "recovered",
     "priority": 1}
  ],
  "edges": [{"from": 10, "to": 11}, {"from": 11, "to": 12}, {"from": 13, "to": 10}]
}
```

- `triggered = active_server_id != root_server_id`。
- `subject=root/action=recovered` 的节点没有 `owner_server_id`，出边固定指向根。
- 没有图时省略该字段。
- 读路径若根服务器不存在、或当前运行服务器不在图中，则按“无编排”返回（省略
  orchestration），避免 UI 展示一张永远不会触发的图。

### 6.2 保存

`POST /api/chain/hop-orchestration/save`（Auth + CSRF + Idempotency）：

```json
{
  "chain_id": 7, "seq": 0,
  "nodes": [
    {"kind": "server", "server_id": 1, "is_root": true, "address": "", "listen_port": 443},
    {"kind": "trigger", "subject": "self", "action": "ban_suspected", "priority": 0},
    {"kind": "trigger", "subject": "self", "action": "ban_banned", "priority": 0},
    {"kind": "trigger", "subject": "root", "action": "recovered", "priority": 1},
    {"kind": "server", "server_id": 2, "address": "", "listen_port": 443}
  ],
  "edges": [{"from": 0, "to": 1}, {"from": 1, "to": 4},
            {"from": 0, "to": 2}, {"from": 2, "to": 4},
            {"from": 3, "to": 0}]
}
```

- `from/to` 为 `nodes` 数组下标；整图替换；空数组 = 删除图。
- 返回更新后的链路 DTO。
- **地址/端口同步**：保存时若某个服务器节点就是该跳当前运行的服务器，把它的
  `address/listen_port` 写回 `chain_hops`（工作拓扑），链路编辑向导与 DTO 立即
  看到同一份值；不创建修订、不触发部署。其它节点的值保存在图里，详情堆叠立即
  按节点值渲染，切换激活时写入工作跳，向导随之显示。链路有在途修订时拒绝保存
  （§4.2）。
  > 链路编辑向导一个跳位只有一张卡（当前运行服务器），所以未激活的根/备选
  > 地址端口在详情堆叠与编排器里编辑和查看；激活后自动出现在向导里。

### 6.3 手动切换

保留 `POST /api/chain/switch-hop`（chain_id/seq/server_id/reason）：

- 该跳有编排图：目标必须是图里的服务器节点（根也可），链路有在途修订时拒绝；
- 该跳没有编排图：沿用现状，允许切换到任意合法服务器（合成临时目标），用于还没
  配置编排、或刚删除编排的跳；地址/端口按链路规则校验。

这样删除图后仍能手动切回原服务器，不会出现“回不去”的死角。

## 7. 前端：详情页堆叠

- 链路卡片/详情顶部展示 `orchestration_error`（红条）：「自动编排已暂停：…；
  修改并保存编排后恢复」，保留现场不隐藏。
- 新组件 `HopStack`（`src/frontend/src/pages/chains/HopStack.tsx`）：
  - props：`hop`、`orchestration`、`onEdit`、`onSwitch`；
  - 前卡复用现有 `.cg-hop` 内容；后卡为紧凑卡（别名、国家、角色、状态、
    地址、端口，地址/端口取该节点在编排图里的值）；
  - 卡片按 `i * 14px` 偏移 + 轻微缩放，active 卡高亮并显示「编排已触发」；
  - 黄点：`aria-expanded` 按钮，展开后显示空位卡（最多 4 格）；
  - 堆叠整体是一个按钮（`aria-label="编辑第 N 跳自动编排"`），键盘可聚焦；
  - 超过 4 台：显示前 3 张 + `+N`。
- 没有图时：单卡 + 黄点 + 空位卡；点击进入空图编辑器。
- 堆叠在 `desired_revision_id != 0` 时仍可查看/编辑（配置不影响在途部署），
  但手动切换按钮禁用。
- 减少动效偏好下取消位移过渡。

## 8. 前端：编排编辑器

- 全屏弹窗（`role="dialog" aria-modal`），Esc/取消在未保存时二次确认。
- 顶部：链路名、跳序号、根服务器、当前使用服务器、保存/取消、校验状态。
- 左侧 2 个 tab：
  - **触发动作**：5 个内置触发器（含「原节点恢复」），按 §2.1 的可用性禁用并
    说明原因；拖拽或点击添加。「原节点恢复」在根左侧创建并自动连到根入口，
    其它触发器放在画布中心等待连线。
  - **服务器**：可作该跳替补的服务器列表（排除已用、其他跳主节点、不兼容者），
    展示别名/地区/在线状态；卡片与链路编辑器一致（地址下拉 + 端口输入）。
- 画布：
  - 复用现有的视口/拖拽/连线/边路由基础设施（`canvas-viewport`、
    `canvas-edge-routing`、`use-world-drag`），新建 `OrchestrationCanvas`；
  - 固定根卡片（不可删除/拖动）；服务器卡片 = `ChainNodeCard` 的变体（地址+端口）；
  - 触发器节点带优先级输入框：0 = 自动（占位文案「自动」），1..9999 显式；
    校验范围并即时重排；
  - 触发器节点：橙色，按主体呈现六边形/左缺口/右缺口 + 图标 + 标签；
  - 连线：server 出口 → trigger 入口，trigger 出口 → server 入口；方向箭头；
  - 删除节点/边；无法连接时给出即时提示（如 trigger 已有目标）；
  - 当前正在使用的服务器卡片不可删除（提示先切走或保留），保存时后端同样校验。
- 校验：
  - 前端镜像 §4 的度数/DAG/环/唯一性/优先级规则，实时高亮；
  - 当前使用节点不可删除；
  - 保存前必须通过；后端错误映射到对应节点提示。
- 保存成功后关闭并刷新链路；失败保留编辑态。

## 9. 兼容与迁移

- v34 迁移：删除 v33 的两张表；创建图的三张表；`schemaVersion = 34`。
  新装/升级到 v32 的库都会得到新表。v33 从未发布，无需数据迁移。
- `chains.orchestration_error`（v34 新列）：worker 失败时的错误文案；DTO 返回，
  保存图/编辑链路后清空。
- 移除：`chain_hop_candidates`/`chain_auto_policies` 相关 store、DTO、
  `auto_policy`/`candidates` 请求字段、向导里的自动编排开关与策略表单、
  P1 备选按钮、`ChainAutoPolicy`/备选类型。
- 保留：`switch-hop`、`chain_failover_events`、`server_pair_watch`、
  `chain_auto_failover`/`chain_failover_exhausted` 告警。
- 订阅：不改；仍以 `chain_hops` 当前服务器 + 已发布修订为准。

## 10. 测试与验收

后端：

- store：图替换（增删改）、级联、根唯一、服务器唯一、priority 排序。
- 校验：表驱动覆盖 §4 的 12 条（含“回到根以外的环”“触发器多目标”“触发器无来源”
  “非根多来源（允许）”“原节点恢复无来源且目标=根”“priority 越界”
  “地址/端口非法”“跨跳复用”“在途修订拒绝保存”）。
- 编排器：疑似/确认封禁、疑似先于确认、显式 priority 覆盖、上一跳/下一跳超时、
  原节点恢复（根健康切回 / 根不健康不切）、目标不健康→exhausted、探测目标端
  优先、冷却、多分支回切、切换前端口越界拒绝。
- 每链 worker：并发唤醒只生成一个 revision、deploying 中不重复、状态迁移、
  重启后从 DB 推导状态、跨链并行不互相阻塞。
- 链路编辑协调：换服务器清图、回切到根保图、删跳清图、跨跳复用清图、同服务器
  改地址/端口同步；读路径对不一致图降级为无编排。
- 手动切换：有图限图成员、无图任意合法服务器。
- handler：保存成功/校验失败/DTO 形状/手动切换/保存时的地址端口写回。

前端：

- 图 reducer/校验镜像（连接规则、DAG/环、唯一性、priority）；
- 原节点恢复的添加行为（根左侧、自动连根）与删除保护；
- BFS 堆叠顺序（多来源只出现一次）、最多 4 张、+N、active 标记、黄点展开；
- 编辑器组件测试（palette 可用性、priority 输入、保存载荷、错误展示）；
- 详情页组件测试（无图/有图/触发中/根与备份端口渲染）。

端到端与验收：

- 本地构建面板 → 建链 → 用 UI 保存一张含回切边的图 → 注入 ban 状态触发切换
  → 详情页出现「编排已触发」与堆叠 → 编辑器显示当前使用；
- 截图：堆叠常态、堆叠展开、编辑器（含四类触发器）、校验错误、触发后的详情；
- CI 门禁：`go test ./...`、`go vet`、`npm run format:check`、`lint`、
  `test`、`build`（含 OpenAPI 契约）。

## 11. 我的决定（可否决）

1. 每跳冷却 300s（存 `chain_hop_graphs.cooldown_sec`）。
2. 保留手动切换，放在卡片菜单；不校验目标健康，但校验图成员。
3. 堆叠 BFS 顺序 = 触发器 ordinal 顺序；最多 4 张 + 「+N」。
4. 图规模上限 8 服务器/16 触发器。
5. 「疑似」在 suspected 或 banned 时命中；默认先于「确认」（类型序），
   显式 priority 可覆盖。
6. 同机同条件唯一；链路编辑的图清理按 §3 的协调规则执行。
7. 节点坐标不持久化，打开时自动布局。
8. v34 迁移删除 v33 表；v33 未发布，不做数据迁移。
9. 根允许多个回切入边；非根允许多个来源（DAG），只禁止非根之间的环。
10. 链路编辑按 §3 协调：只有新服务器 == 图根时保留，否则清空；同服务器改
    地址/端口时保留并同步当前服务器节点。
11. 保存要求当前运行服务器在图中；编辑器禁止删除当前使用的服务器卡片。
12. 无图的跳仍可手动切换（沿用现状）；有图时目标限制为图成员。
13. 原节点恢复是全局单例：入度 0、目标固定为根，默认 priority=1，添加时位于
    根左侧并自动连根。
14. 每链一个 worker + 编排状态机（§5.1）；只为有图的链创建，同一链/同一跳最多
    一个在途切换；跳序 A→B→C，B 失配进入 failed 并保留现场。
15. 保存图时把当前服务器的 address/listen_port 写回 `chain_hops`；链路有在途
    修订时拒绝保存（不同步、不部署）。
16. priority：0 = 自动（类型序 + ordinal），1..9999 = 显式升序。

## 12. 删除与清理

### 12.1 原则

- **删除优先于编排**：任何删除流程先暂停/停止该链 worker，之后不再产生新的切换；
  在途切换由链路状态 CAS 收敛，删除不会与编排并发改拓扑。
- **图是配置，不是运行态**：拓扑删除后不做自动修补，只清理引用；需要时管理员重配。
- **复用既有清理机制**：Revision Planner 的 cleanup pieces + dispatcher 命令队列 +
  `cleanup_pending` / 链路 FSM；不引入第二套清理队列。
- **幂等 + CAS**：remove 命令按 HopID/Kind 幂等；链路删除/失效用状态 CAS；重复
  删除安全。

### 12.2 场景矩阵

| 操作 | 拓扑 | 编排图 | worker | 清理命令 | 用户可见 |
|---|---|---|---|---|---|
| 链路编辑删除/替换跳 | 新 revision，旧 pieces 进 cleanup | 删除/替换的 seq 清图；新服务器==图根则保留 | paused（applying/cleanup_pending） | Revision Planner 生成，cleanup_pending 等待回执 | 编辑结果提示清空的跳 |
| 链路编辑重排 | seq 位移 | 位移的 seq 清图 | 同上 | 同上 | 同上 |
| 删除服务器（当前工作跳） | 链路 invalid | 清空引用该服务器的图 | stopped | 对所有部署 revision 的非目标 pieces 去重入队 | 删除摘要：失效链路数/清空跳数 |
| 删除服务器（仅历史 revision/图） | 不变 | 根被删则清图，否则删节点 | resume | 不需要（服务器卸载即消失） | 操作日志 |
| 删除链路 | 软删除 | 级联删除图与审计 | stopped | 对全部部署 revision 的 pieces 去重入队 | 链路消失；日志记录入队数 |
| 编排编辑器删除节点 | 不变 | 图内删除（当前/根/跨跳使用被拒） | 不变 | 无 | 堆叠变化 |

### 12.3 链路编辑：删跳/换跳/重排

- 清理点选在 **`ReplaceWorkingChainTopology` 发布成功的那一次事务**：先比较旧/新
  `chain_hops`，再按 §3 协调规则删/留图，最后删除旧跳。编辑失败/回滚时图不动，
  避免“部署失败但配置先丢”。
- 删除/替换的跳由 Revision Planner 生成 cleanup pieces；链进入
  `applying`→`cleanup_pending`，worker 在整段时间内不评估。
- 被删除跳的当前服务器 pieces 在 cleanup 完成后消失；图已删除。
- 失败重试沿用链路 retry；cleanup 失败不回滚已发布的新拓扑，`cleanup_pending`
  由既有 reconcile 收敛。
- 响应：编辑请求返回 accepted；发布完成后的 DTO 不再返回被清空的 orchestration；
  操作日志 `chain.orchestration_cleared`（reason=hop_deleted/hop_replaced）。
- **链路编辑器交互**：对启用了编排的跳点击删除时，确认框必须明确提示「该跳的自动
  编排配置将一并删除」；确认后按本节规则清图。

### 12.4 删除服务器

引用集合分三层，**失效判定只看当前工作拓扑**，历史 revision 不应误伤已经
切换走的链：

- `chain_hops.server_id = id`（当前工作跳）→ 失效；
- 历史部署 revision 里出现、但当前工作拓扑已不含它 → 只作为历史 pieces 来源，
  不失效（服务器卸载后这些 pieces 自然消失）；
- `chain_hop_graph_nodes.server_id = id`（编排图）→ 清图，不失效。

顺序：

1. 对每个受影响链调用 `worker.Pause(chainID, "server_delete")`，等待状态稳定
   （超时则继续，后续 CAS 会拒绝新 revision）。
2. 当前工作跳命中 → `ChainFSM().InvalidateForServerDeletion`（链路 invalid、
   desired=0、任务收敛），worker → stopped；清空引用该服务器的图。
3. 只命中历史 revision 或编排图 → 清空引用该服务器的图；链保持 active，
   worker resume。
4. 对所有部署 revision 的非目标跳 pieces 去重入队 remove；uninstall agent；
   `DeleteServerCascade` 删除图节点/边、hops、命令等。
5. 返回摘要 `{invalidated_chains, cleared_orchestration_hops, cleanup_commands}`；
   删除确认框在提交前展示同样的计数。

竞态：pause 成功后 worker 不再开始新切换；若 pause 超时且切换已在途，invalidation
的 CAS/任务 abandon 会收敛；删除是幂等的。

### 12.5 删除链路

- worker → stopped；级联删除图/审计（`chain_hop_graphs/nodes/edges` 随 FK 或显式）；
- 对**全部部署 revision** 的 pieces 去重入队 remove（修复当前只遍历工作 hop 的
  缺口：故障切换过的链可能在其他服务器留有历史 pieces），然后软删除链路；
- 不等待 ack：命令队列重试，agent 侧 `xray.cleanup` 兜底；操作日志记录入队数量。
- 若后续要求“可见的清理完成态”，再把链路临时置为 `cleanup_pending` 并在完成后
  软删除；本期沿用即时删除。

### 12.6 worker 协作

- worker manager 增加 `Pause/Resume/Stop/WaitSettled`；状态机增加
  `paused`/`stopped`。
- worker 在 evaluating/switching 前检查 `chain.status` 与自身状态；paused 直接返回。
- 删除协调器在 panel 的删除 handler 内同步执行“暂停→改图/失效→入队→删除→恢复”；
  不新增常驻清理 goroutine。
- 重启：invalid/deleted 的链不建 worker；active 且无在途修订的链按需建 worker。

### 12.7 可见性与审计

- 新增操作事件：`chain.orchestration_cleared`（reason）、`chain.cleanup_queued`
  （命令数），沿用 `chain.deleted` / 服务器删除事件。
- 详情页：图被清空 → 回到单卡；删除操作完成后以 toast/操作日志说明原因。
- 服务器删除确认框显示影响计数，避免“删完才发现链路失效”。

### 12.8 测试

- 编辑删跳/换跳/重排：图按 §3 清理；部署失败不清理；保留的图不受影响。
- 服务器删除：当前跳→invalid+stop；仅图节点→清图+resume；清理命令去重；
  pause 与在途切换的竞态。
- 链路删除：覆盖历史 revision 的 pieces；命令幂等。
- 编排编辑器删除节点：当前/根/跨跳拒绝；清空图后 worker 不下发切换。

## 13. 非目标

- Agent 离线触发器、配额触发器（模型可扩展，本期不做）。
- 每个触发器的独立阈值参数。
- 图的历史版本/回滚/审计回放。
- 订阅页展示备选或编排信息。
- 非根服务器之间的环（只允许经触发器回到根）。
- 编排图的跨链共享（每链一张图，worker 按链隔离）。
