# 未托管服务器与外部直链出口链路（设计）

> 需求：服务器页新增一类**未托管服务器**（来自 `vless://` / `vmess://` / `socks://` /
> `http://` 等外部直链的节点）。它们不能作为链路入口/中间跳，只能作为接入互联网的
> 出口（最后一跳）。新增/编辑服务器处提供协议表单（vless/vmess/trojan/ss/socks/http
> 各自字段），分享链接仅用于「解析 → 自动填写」，服务器表单没有必填项。链路形态为
> `托管入口 A → [托管中间跳 0-2] → 外部出口 E → Internet`，流量统计与「转发到
> socks/vless」都发生在托管入口 A，用户订阅看到的仍是 A 的入口。链路数据建模与既有
> 链路一致（charts/chains + chain_hops + chain_revisions）。

## 1. 概念与约束

- `servers.kind`：`managed`（默认，既有被控服务器） | `external`（未托管外部节点）。
- 外部服务器没有 Agent：不安装、不升级、不遥测、不体检、不测速、不参与 Ban 巡检、
  不参与延迟测速；连接态在 DTO 中固定为 `external`（前端据此隐藏 Agent 相关操作）。
- 外部服务器**无入站能力**：存储上沿用 `machine_type='nat'` + `allowed_ports=''`
  （仅出口档），因此既有「入口/中间跳必须有入站能力」校验天然拒绝它；另加显式校验
  「未托管服务器只能作为链路的最后一跳（出口）」，让报错可读。
- 外部服务器的 `address` = 链接里的 host（链路末段拨号目标），端口取链接里的 port。
- 一条链中最多一个外部服务器，且必须是最后一跳；链最少 2 跳（A + E）。
- 外部出口协议（v1）：`vless` / `vmess` / `trojan` / `shadowsocks` / `socks` / `http`
  ——全部为 TCP 承载，可被 dokodemo 纯 TCP 管道透传。`ssr` / `hysteria2` / `tuic` /
  `wireguard` / `anytls` / `snell` 暂不支持（创建时明确报错）。

## 2. 数据面

链路 `A → …（0-2 跳）→ E`：

```
client --VLESS+REALITY(access:N)--> A 的共享入口 Endpoint
                                     │  路由（按链路）× 每用户 outbound
                                     ▼
                          E 协议客户端 outbound（vless/vmess/trojan/ss/socks/http）
                                     │ 拨 127.0.0.1:<入口 forward 端口>
                                     ▼
                     hop0 forward(dokodemo, 仅回环) --raw TCP--> hop1 forward … 
                                     ▼
                     末位托管跳 forward --raw TCP--> E 的公网 host:port
                                     ▼
                                  E（外部节点）---> Internet
```

- 入口 A **总是**挂共享端点（VLESS，按 assignment 的 `access_uuid` 认证；传输/安全层/
  证书等协议细节与普通链路一样可配置）：
  每用户流量统计（`user>>>access:N>>>traffic`）与链路总量都落在 A，满足
  「用户可见的是服务器 A 的入口」。
- Endpoint 每条链一条路由：`SharedEndpointRoute.External` 携带 E 的协议/凭据/传输
  参数，`target_address=127.0.0.1`、`target_port=<入口 forward 端口>`（agent 渲染外部
  协议 outbound 时以该地址拨号）。因此 `A` 的 xray 就是「转发到 socks/vless 的过程」。
- 链跳 forward 只做纯 TCP 透传：hop0 仅回环；中间跳对下一跳公网端口；**末位托管跳**
  的 target 为 E 的 `address:port`。因此中间跳数量不影响外部协议的端到端语义。
- 无 service 业务节点（不 `node.apply`）：`chains.service_node_id=0`、
  `chain_hops.node_id=0`、`service_uuid` 为空；修订快照
  `ChainRevisionSnapshot.ServiceExternal` 携带不可变的外部出口规格（与既有
  `ServiceConfig` 一样随修订冻结）。
- 支持的传输/安全层沿用节点矩阵：`tcp|ws|grpc|xhttp|httpupgrade` ×
  `none|tls|reality`（vmess/vless/trojan；ss/socks/http 无传输层参数）。
- UDP 不支持（外部协议均为 TCP；dokodemo 管道按 tcp 分层）。

## 3. 存储

```sql
ALTER TABLE servers ADD COLUMN kind TEXT NOT NULL DEFAULT 'managed';        -- managed|external
ALTER TABLE servers ADD COLUMN external_link TEXT NOT NULL DEFAULT '';      -- 原始分享链接
ALTER TABLE servers ADD COLUMN external_config TEXT NOT NULL DEFAULT '';    -- 规范化外部出口规格 JSON
```

- `external_config` 为 `shared.ExternalExit` 的 JSON；创建/更新时由协议表单（或解析器）产出，
  仅做「可保存」校验（协议/端口/地址字符集），地址与凭据可留空占位；链路创建/编辑时
  执行完整规格校验并复制进修订快照（不可变），运行期不读 `servers.external_config`。
- 外部服务器的 `token` 仍写入一次性随机凭证（列约束），但永不用于拨入。
- 修改外部协议参数（表单或重新解析）：若规范化结果与现值不同且被链路引用，则拒绝
  （409），提示先调整/删除链路——避免已发布修订与服务器记录静默分叉。仅改别名/
  标签/地区不受影响。

## 4. 校验（面板）

创建/编辑链路的逐跳校验新增：

1. `i < len-1` 且 `server.kind == external` → 400「未托管服务器只能作为链路的最后一跳（出口）」。
2. 最后一位为 external 时：必须 `len(hops) >= 2`；外部规格必须完整（协议/地址/端口/凭据，
   `ExternalExit.Validate`），不完整时 400「外部直链出口配置不可用：…（请编辑服务器补全
  协议参数）」——服务器表单没有必填项，补全入口在服务器编辑对话框。
3. 外部出口链**强制**创建入口共享端点（VLESS）：入口服务器 A 与普通链路一样可配置完整
   协议细节（传输/安全层/证书/flow/encryption/Reality 子参数），缺省 Reality 自动生成；
   入口协议由详情步骤的主协议表单承载（出口协议表单隐藏），编辑时从 entry_config 回填。
4. 端口校验：入口端口照旧（NAT 段/冲突）；外部出口不使用「出口节点端口」输入。
5. 编辑链路时外部出口与既有出口节点互斥；切换需重新校验。

## 5. 编排（dispatch）

- 修订计划：`RevisionTopology.External` 非空时**不生成 service piece**，下游哈希的
  基值取外部规格哈希——外部规格变化会让全部 forward piece 重发。
- `advanceChain`：外部出口跳无需 apply，直接置 active；跳过 portal/bridge（E 无下游、
  也无 ssh 隧道）；forward 阶段末位托管跳的 target 取 `E.address:E.port`。
- 发布：`ServiceRealized` 由外部规格派生（不使用 agent 回执）；强制发布只要求入口
  forward 端口已知。
- `chainFSM.Evaluate`：离线判定跳过外部跳（无 Agent）。
- 订阅：入口 Endpoint 路径不变（地址=入口跳地址引用，凭据=assignment access_uuid）。

## 6. API

- `POST /api/server/parse-link` `{link}` → `{name, protocol, address, port, exit, summary}`；
  解析失败 400 并带原因。
- `POST /api/server/create` 新增 `kind`（默认 managed）、`external_exit`（协议表单）与可选
  `external_link`；external 时忽略 machine_type/allowed_ports 并强制 `nat` + 空段；别名/国家/
  地区可空（别名按 host:port 派生）。
- `POST /api/server/update` 支持 `external_exit`（优先）与 `external_link`（规则见 §3）。
- 服务器 DTO 新增 `kind`、`external_link`、`external_exit`（完整规格，管理员 API 同信任域，
  编辑表单回填）与 `panel_latency_ms/panel_probed_at`（面板到节点的 TCP 延迟）。
- 外部服务器上的 Agent 动作（install/rotate/repair/cleanup/rebuild/upgrade-xray/
  upgrade-agent/run-test/命令行/设置同步）一律 400「未托管服务器无 Agent」。

## 7. 前端

- 服务器页：「添加服务器」类型选择新增「外部直链（未托管）」；选择后展示可选分享链接
  +「解析」按钮，以及按协议切换的完整协议表单（协议/地址/端口/UUID/密码/传输/安全层/
  Reality/证书等）。服务器表单没有必填项（别名/国家/地区/地址/端口均可空，未补全的
  节点由链路页与后端提示）。外部服务器不进入「服务器读数」主列表，单独成板展示在
  上方：仅协议/目标/面板测得延迟，取消三网测试与性能监控。
- 编辑服务器：外部服务器展示同一份协议表单（保存的是协议参数；被链路引用时修改
  参数返回 409）。
- 链路画布：外部服务器卡片可拖入，但只能是最后一跳；校验新增
  `external_not_last`（「未托管服务器只能作为最后一跳」）；外部节点不显示端口输入
  （端口来自链接，只读展示）。
- 链路细节：出口为外部节点时隐藏出口协议表单，改为展示外部节点只读摘要；
  入口协议区块强制开启（可填子参数）。

## 8. 链路编辑器：多节点连接与结果预览

链路编辑器画布从「单链」升级为**有向图**：允许分叉（一个节点多条出边）、汇合（多条入边）、多个入口节点（流量入口连出多条边）；仍禁止回环、同一服务器重复出现、单路径超过 4 跳、外部直链服务器出现在非末位。

点击「下一步」时执行一次图分析并预览结果：`validPaths` = 所有「流量入口 → … → Internet」的简单路径（路径内不重复节点）；每条合法路径即一条将创建的链路（末位是出口）。

- **空悬链路**（不生成，预览面板以警告列出）：
  - 入口侧空悬：节点有出边但不可从「流量入口」到达（其链路不会生成，如 `E → B → Internet`）。
  - 出口侧空悬：节点可从入口到达但没有通向 Internet 的路径（如 `F → G`）。
  - 孤立节点（无任何连线）同样提示。
- **硬错误**（阻止下一步）：图中有环；没有任何合法路径；某条路径超过 4 跳；外部直链服务器非末位；有指向其他服务器的出边的节点无入站能力；入口/出口端口非法或不在 NAT 段内。
- 有环检测在图分析入口完成（DFS 染色），有环时直接返回错误，不做路径枚举，避免死循环；路径枚举用「路径内访问集」剪枝，保证同一路径不重复节点、同一简单路径只产出一次。
- 端口输入：有入口边（来自流量入口）的节点显示入口端口；指向 Internet 的节点显示出口端口；同一节点既是入口又是出口（单节点链路）时复用同一输入；其余节点不显示端口。
- 结果预览：放在「协议与细节」步骤（下一步），完整沿用链路详情页的链路样式（`cg-chain-entry` 卡片：线路徽标、名称、直连/中转/外部出口标签、状态、逐跳卡片 + 跳间连线（`cg-hop-link`）、右侧累计流量读数板（`cg-chain-traffic`））展示本次编辑将产生/影响的链路。端口等配置显示编辑器中所见值（自动端口显示「自动分配」，部署后才生成真实端口），外部出口显示外部节点端口与协议；未部署时累计流量与今日负载显示 `--`、保留流量倍率。空悬链路（不会生成）在预览底部列出；拓扑步骤只显示校验摘要与空悬警告。「下一步」在无硬错误且 N≥1 时可用。编辑模式只展示单条链、状态为「待更新」。
- 详情步骤：协议/传输/安全参数对该次生成的**全部链路**生效（同一套出口参数），名称以基准名派生（`基准-1`、`基准-2`……）；入口/出口端口取画布上各节点的输入。提交时按顺序逐条调用既有 `POST /api/chain/create`（一次一条链路），复用现有编排与取消语义。
- 编辑现有链路仍按单链编辑（画布预填该链路径）。
