# xray 全协议暴露 P1（解锁存量 + 端口冲突前置校验）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把后端已支持的 vmess/trojan/shadowsocks + reality + grpc 传输全部暴露到链路表单，新增 vmess cipher 贯通，并在 panel 侧落地端口冲突前置校验（TCP/UDP 分层）。**硬性要求：存量链路（vless 共享链、明文 socks/http 链、dokodemo 转发链、reverse/encrypted 隧道链）在本次更新后全部不失效，以完整 e2e 回归证明。**

**Architecture:** 沿用现有"panel 模板 → agent 填充 → xray run -test → 热更新"管线，不引入新 core。端口冲突治理新增 store 层 `PortOccupants` 统一查询 + panel 纯函数判定 + agent `pickPort` 分层探测三道防线。

**Tech Stack:** Go（backend/agent/shared 三个 module，go.work  workspace）、React+TS（vite/vitest/oxlint）、SQLite（modernc.org/sqlite）、OpenAPI 契约 `docs/openapi.yaml` → 前端生成类型。

**Spec:** `docs/superpowers/specs/2026-09-12-xray-full-protocol-exposure-design.md`（§2 矩阵、§3.2 端口冲突治理）

## Global Constraints

- 不新增任何第三方依赖（Go 与前端均如此）。
- 协议常量必须与 `src/shared/config.go` 保持一致，前端常量注释沿用"与后端 shared 包保持一致"。
- 后端 API 字段变更必须同步改 `docs/openapi.yaml` 并在 `src/frontend` 跑 `npm run generate:api`（`npm run build` 内含 `--check` 会拦截不一致）。
- Reality 仅允许 tcp/grpc/xhttp；vision flow 仅 vless+tcp；这些既有规则不得放松。
- 提交信息沿用仓库惯例：`type(scope): 中文摘要`（如 `feat(panel): ...`）。
- 每个 Task 完成后运行对应验证命令，全绿才提交。
- **存量回归红线**：任何 Task 不得改变既有协议（vless/socks/http/dokodemo）链路的模板结构、端口分配与订阅输出；Task 7 的全量 e2e 回归（含"编辑存量链路原样保存"用例）必须全绿才算 P1 完成。

验证命令速查：
- shared: `cd src/shared && go test ./...`
- backend: `cd src/backend && go test ./...`
- agent: `cd src/agent && go test ./...`
- frontend: `cd src/frontend && npm run generate:api && npm test && npm run lint && npm run build`
- e2e（最后统一跑）: `bash scripts/e2e/protocols.sh`

---

### Task 1: shared — 端口分层助手 + VMessCiphers + VirtualConfig.Cipher

**Files:**
- Modify: `src/shared/config.go`
- Test: `src/shared/config_test.go`（不存在则新建）

**Interfaces:**
- Produces:
  - `func PortLayers(protocol string) string` — 返回 `"tcp"` / `"udp"` / `"tcp,udp"`；本期 shadowsocks 与 dokodemo-door 为 `"tcp,udp"`，其余（含未来 hy2 之前的全部协议）为 `"tcp"`。
  - `func LayersOverlap(a, b string) bool` — 两层集合有交集即 true。
  - `const VMessCipherAuto = "auto"`、`VMessCipherAES128GCM = "aes-128-gcm"`、`VMessCipherChacha20 = "chacha20-poly1305"`；`var VMessCiphers = []string{...}`。
  - `VirtualConfig` 新增字段 `Cipher string \`json:"cipher,omitempty"\``（vmess  cipher 提示，仅 panel/订阅侧使用，不进 inbound 模板）。

- [ ] **Step 1: 写失败测试**

`src/shared/config_test.go`（若文件已存在则追加）：

```go
package shared

import "testing"

func TestPortLayers(t *testing.T) {
	cases := map[string]string{
		ProtocolVLESS:       "tcp",
		ProtocolVMess:       "tcp",
		ProtocolTrojan:      "tcp",
		ProtocolSocks:       "tcp",
		ProtocolHTTP:        "tcp",
		ProtocolShadowsocks: "tcp,udp",
		ProtocolDokodemo:    "tcp,udp",
	}
	for protocol, want := range cases {
		if got := PortLayers(protocol); got != want {
			t.Errorf("PortLayers(%s) = %q, want %q", protocol, got, want)
		}
	}
}

func TestLayersOverlap(t *testing.T) {
	if !LayersOverlap("tcp,udp", "tcp") {
		t.Error("tcp,udp 与 tcp 应重叠")
	}
	if LayersOverlap("udp", "tcp") {
		t.Error("udp 与 tcp 不应重叠")
	}
	if !LayersOverlap("tcp,udp", "udp") {
		t.Error("tcp,udp 与 udp 应重叠")
	}
}

func TestVMessCiphers(t *testing.T) {
	if !ValidValue(VMessCipherAuto, VMessCiphers) ||
		!ValidValue(VMessCipherAES128GCM, VMessCiphers) ||
		!ValidValue(VMessCipherChacha20, VMessCiphers) {
		t.Error("VMessCiphers 应包含 auto/aes-128-gcm/chacha20-poly1305")
	}
	if ValidValue("none", VMessCiphers) {
		t.Error("none 不是合法 vmess cipher")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/shared && go test ./...`
Expected: FAIL（undefined: PortLayers 等）

- [ ] **Step 3: 实现**

`src/shared/config.go`，在 SS 方法常量区块（`:65-79` 附近）之后追加：

```go
// VMess cipher（仅作客户端订阅提示，xray vmess inbound 无此字段）。
const (
	VMessCipherAuto      = "auto"
	VMessCipherAES128GCM = "aes-128-gcm"
	VMessCipherChacha20  = "chacha20-poly1305"
)

var VMessCiphers = []string{VMessCipherAuto, VMessCipherAES128GCM, VMessCipherChacha20}
```

在 `HasUserList` 附近追加：

```go
// PortLayers 返回协议监听占用的传输层（端口冲突治理的分层依据）：
// shadowsocks/dokodemo-door 同时监听 tcp+udp，其余仅 tcp；hy2（P4）将引入 udp-only。
func PortLayers(protocol string) string {
	switch protocol {
	case ProtocolShadowsocks, ProtocolDokodemo:
		return "tcp,udp"
	default:
		return "tcp"
	}
}

// LayersOverlap 判定两个层集合是否有交集（"tcp,udp" 与 "udp" 重叠，"udp" 与 "tcp" 不重叠）。
func LayersOverlap(a, b string) bool {
	for _, x := range strings.Split(a, ",") {
		for _, y := range strings.Split(b, ",") {
			if x == y {
				return true
			}
		}
	}
	return false
}
```

注意：`config.go` 若未 import `strings` 需补上。

`VirtualConfig`（`:173-187`）`Encryption` 字段后追加：

```go
	Cipher        string             `json:"cipher,omitempty"`        // vmess 客户端 cipher 提示（默认 auto）
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/shared && go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/shared/config.go src/shared/config_test.go
git commit -m "feat(shared): 端口传输层助手与 vmess cipher 常量"
```

---

### Task 2: store — PortOccupants 统一端口占用查询

**Files:**
- Create: `src/backend/internal/store/ports.go`
- Test: `src/backend/internal/store/ports_test.go`

**Interfaces:**
- Consumes: `shared.PortLayers`（Task 1）；既有表 `nodes(port,protocol,name,server_id)`、`shared_endpoints(port,protocol,status)`、`chain_hops(forward_port,portal_port,server_id,chain_id)`、`chains(service_node_id,endpoint_id,deleted_at,name)`。
- Produces:
  ```go
  type PortOccupant struct {
      Port     int
      Layers   string // "tcp" / "udp" / "tcp,udp"
      Source   string // "node" | "endpoint" | "chain_forward" | "chain_portal"
      Protocol string
      ChainID  int64  // 0 = 无链归属（独立节点）
      RefName  string
  }
  func (s *Store) PortOccupants(ctx context.Context, serverID int64) ([]PortOccupant, error)
  ```
  Task 3 的 `findPortConflict` 依赖此结构与签名。

- [ ] **Step 1: 写失败测试**

`src/backend/internal/store/ports_test.go`：

```go
package store

import (
	"context"
	"encoding/json"
	"testing"

	"lattix/shared"
)

func TestPortOccupants(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// 服务器 1：一个 vless 节点（tcp）、一个 ss 节点（tcp,udp）、一个共享端点、一条链的 forward/portal
	nodePort := 10001
	if _, err := st.InsertNode(ctx, "n-vless", 1, shared.ProtocolVLESS, &nodePort, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	ssPort := 10002
	if _, err := st.InsertNode(ctx, "n-ss", 1, shared.ProtocolShadowsocks, &ssPort, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	// 服务器 2 的节点不应出现
	otherPort := 10003
	if _, err := st.InsertNode(ctx, "n-other", 2, shared.ProtocolVLESS, &otherPort, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.EnsureSharedEndpoint(ctx, 1, shared.ProtocolVLESS, 10004, "profile-x", json.RawMessage(`{"protocol":"vless"}`)); err != nil {
		t.Fatal(err)
	}

	occupants, err := st.PortOccupants(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	byPort := map[int]PortOccupant{}
	for _, o := range occupants {
		byPort[o.Port] = o
	}
	vless, ok := byPort[10001]
	if !ok || vless.Source != "node" || vless.Layers != "tcp" || vless.RefName != "n-vless" {
		t.Errorf("vless 节点占用不符: %+v", vless)
	}
	ss, ok := byPort[10002]
	if !ok || ss.Layers != "tcp,udp" {
		t.Errorf("ss 节点应为 tcp,udp 双层: %+v", ss)
	}
	ep, ok := byPort[10004]
	if !ok || ep.Source != "endpoint" {
		t.Errorf("共享端点占用缺失: %+v", ep)
	}
	if _, ok := byPort[10003]; ok {
		t.Error("其他服务器的端口不应出现")
	}
}
```

注：`EnsureSharedEndpoint` 以 port>0 直接插入（见 `store/endpoints.go:76-93`），无需先 active。若 `InsertNode`/`EnsureSharedEndpoint` 依赖 servers 行（外键），测试开头需按既有 store 测试惯例先插入服务器行——参考 `src/backend/internal/store/chain_traffic_test.go:14` 的 `Open(":memory:")` 引导方式与就近测试的 fixture 写法。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/backend && go test ./internal/store/ -run TestPortOccupants -v`
Expected: FAIL（undefined: PortOccupants）

- [ ] **Step 3: 实现**

`src/backend/internal/store/ports.go`：

```go
package store

import (
	"context"
	"fmt"

	"lattix/shared"
)

// PortOccupant 是一台服务器上一个已被占用端口的画像（端口冲突前置治理的数据源）。
type PortOccupant struct {
	Port     int
	Layers   string // "tcp" / "udp" / "tcp,udp"
	Source   string // "node" | "endpoint" | "chain_forward" | "chain_portal"
	Protocol string
	ChainID  int64 // 0 = 无链归属（独立节点）
	RefName  string
}

// PortOccupants 汇总服务器上全部受管端口占用：业务节点、共享端点、链路逐跳
// forward/portal 监听。仅返回 port>0 的确定占用（port=0 待 agent 分配的不构成冲突）。
func (s *Store) PortOccupants(ctx context.Context, serverID int64) ([]PortOccupant, error) {
	var out []PortOccupant

	appendRows := func(query, source string, fixedLayers string, args ...any) error {
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o PortOccupant
			o.Source = source
			if err := rows.Scan(&o.Port, &o.Protocol, &o.ChainID, &o.RefName); err != nil {
				return err
			}
			if fixedLayers != "" {
				o.Layers = fixedLayers
			} else {
				o.Layers = shared.PortLayers(o.Protocol)
			}
			out = append(out, o)
		}
		return rows.Err()
	}

	// 业务节点（协议决定层）；归属链 = 以该节点为出口业务节点的未删链。
	if err := appendRows(`SELECT n.port, n.protocol, COALESCE(c.id,0), n.name
		FROM nodes n LEFT JOIN chains c ON c.service_node_id=n.id AND c.deleted_at IS NULL
		WHERE n.server_id=? AND n.port IS NOT NULL AND n.port>0`, "node", "", serverID); err != nil {
		return nil, fmt.Errorf("query node occupants: %w", err)
	}
	// 共享端点（pending/applying/active 均视为将占用；port=0 未分配的不算）。
	if err := appendRows(`SELECT e.port, e.protocol,
		COALESCE((SELECT c2.id FROM chains c2 WHERE c2.endpoint_id=e.id AND c2.deleted_at IS NULL
			ORDER BY c2.id LIMIT 1),0), 'shared-endpoint #' || e.id
		FROM shared_endpoints e
		WHERE e.server_id=? AND e.port>0 AND e.status IN ('pending','applying','active')`,
		"endpoint", "", serverID); err != nil {
		return nil, fmt.Errorf("query endpoint occupants: %w", err)
	}
	// 链路逐跳 forward（dokodemo 管道，本期 tcp-only）与 portal（vless+reality，tcp）。
	if err := appendRows(`SELECT h.forward_port, 'dokodemo-door', c.id, c.name
		FROM chain_hops h JOIN chains c ON c.id=h.chain_id AND c.deleted_at IS NULL
		WHERE h.server_id=? AND h.forward_port>0`, "chain_forward", "tcp", serverID); err != nil {
		return nil, fmt.Errorf("query forward occupants: %w", err)
	}
	if err := appendRows(`SELECT h.portal_port, 'vless', c.id, c.name
		FROM chain_hops h JOIN chains c ON c.id=h.chain_id AND c.deleted_at IS NULL
		WHERE h.server_id=? AND h.portal_port>0`, "chain_portal", "tcp", serverID); err != nil {
		return nil, fmt.Errorf("query portal occupants: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/backend && go test ./internal/store/ -run TestPortOccupants -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/store/ports.go src/backend/internal/store/ports_test.go
git commit -m "feat(store): PortOccupants 统一端口占用查询"
```

---

### Task 3: panel — 端口冲突前置校验

**Files:**
- Create: `src/backend/internal/panel/ports.go`
- Modify: `src/backend/internal/panel/nodes.go`（`handleCreateNode`，在 `:218-223` 的 `checkPortInRanges` 之后插入）
- Modify: `src/backend/internal/panel/chains.go`（`handleCreateChain` 入口端口校验 `:319-331` 与出口节点端口校验 `:339-345` 之后；`handleEditChain` 同等位置镜像）
- Test: `src/backend/internal/panel/ports_test.go`

**Interfaces:**
- Consumes: `store.PortOccupants`（Task 2）、`shared.PortLayers/LayersOverlap`（Task 1）。
- Produces:
  ```go
  func findPortConflict(occupants []store.PortOccupant, protocol string, port int, excludeChainID int64) error
  func (s *Server) checkPortConflict(ctx context.Context, serverID int64, protocol string, port int, excludeChainID int64) error
  ```
  规则（与 spec §3.2 一致）：
  - 仅当 `LayersOverlap(PortLayers(protocol), o.Layers)` 且 `o.Port == port` 时构成冲突。
  - `o.ChainID == excludeChainID` 的占用跳过（编辑链路时排除自身）。
  - `o.Source == "endpoint"` 且新协议为 vless → 跳过（共享端点合并语义，由 `EnsureSharedEndpoint` 自行处理）。
  - 冲突报错文案：`端口 %d 已被%s「%s」占用（%s 层），请更换端口或留空自动分配`，其中来源描述按 Source 映射：node→节点、endpoint→共享监听、chain_forward→链路中转管道、chain_portal→链路隧道。

- [ ] **Step 1: 写失败测试**

`src/backend/internal/panel/ports_test.go`：

```go
package panel

import (
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestFindPortConflict(t *testing.T) {
	occupants := []store.PortOccupant{
		{Port: 443, Layers: "tcp", Source: "node", Protocol: shared.ProtocolVLESS, ChainID: 1, RefName: "链A"},
		{Port: 8443, Layers: "tcp,udp", Source: "node", Protocol: shared.ProtocolShadowsocks, ChainID: 2, RefName: "链B"},
		{Port: 10080, Layers: "tcp", Source: "endpoint", Protocol: shared.ProtocolVLESS, ChainID: 3, RefName: "shared-endpoint #1"},
	}

	// 同层同端口不同协议 → 冲突
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 443, 0); err == nil {
		t.Error("trojan 撞 vless(tcp) 应冲突")
	}
	// ss(tcp,udp) 撞 vless(tcp) → 冲突
	if err := findPortConflict(occupants, shared.ProtocolShadowsocks, 443, 0); err == nil {
		t.Error("ss 撞 vless(tcp) 应冲突")
	}
	// vless(tcp) 撞 ss(tcp,udp) → 冲突（方向对称）
	if err := findPortConflict(occupants, shared.ProtocolVLESS, 8443, 0); err == nil {
		t.Error("vless 撞 ss(tcp,udp) 应冲突")
	}
	// 不同端口 → 放行
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 9999, 0); err != nil {
		t.Errorf("不同端口不应冲突: %v", err)
	}
	// 编辑链路排除自身
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 443, 1); err != nil {
		t.Errorf("排除自身链后不应冲突: %v", err)
	}
	// vless 加入共享端点 → 放行（共享语义）
	if err := findPortConflict(occupants, shared.ProtocolVLESS, 10080, 0); err != nil {
		t.Errorf("vless 共享端点合并不应冲突: %v", err)
	}
	// 非 vless 撞共享端点 → 冲突
	if err := findPortConflict(occupants, shared.ProtocolTrojan, 10080, 0); err == nil {
		t.Error("trojan 撞共享端点应冲突")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/backend && go test ./internal/panel/ -run TestFindPortConflict -v`
Expected: FAIL（undefined: findPortConflict）

- [ ] **Step 3: 实现**

`src/backend/internal/panel/ports.go`：

```go
package panel

import (
	"context"
	"fmt"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// 端口冲突来源的中文描述（报错文案用）。
var portOccupantSourceNames = map[string]string{
	"node":          "节点",
	"endpoint":      "共享监听",
	"chain_forward": "链路中转管道",
	"chain_portal":  "链路隧道",
}

// findPortConflict 端口冲突前置判定（纯函数）：同端口 + 传输层重叠即冲突。
// excludeChainID 用于编辑链路时排除自身既有占用；vless 撞共享端点放行（共享合并语义，
// 由 EnsureSharedEndpoint 自行处理同协议合并与跨协议冲突）。
func findPortConflict(occupants []store.PortOccupant, protocol string, port int, excludeChainID int64) error {
	layers := shared.PortLayers(protocol)
	for _, o := range occupants {
		if o.Port != port || !shared.LayersOverlap(layers, o.Layers) {
			continue
		}
		if excludeChainID != 0 && o.ChainID == excludeChainID {
			continue
		}
		if o.Source == "endpoint" && protocol == shared.ProtocolVLESS {
			continue
		}
		source := portOccupantSourceNames[o.Source]
		if source == "" {
			source = "监听"
		}
		return fmt.Errorf("端口 %d 已被%s「%s」占用（%s 层），请更换端口或留空自动分配",
			port, source, o.RefName, o.Layers)
	}
	return nil
}

// checkPortConflict 查询服务器端口占用并判定冲突（端口 0 = 自动分配，无需校验）。
func (s *Server) checkPortConflict(ctx context.Context, serverID int64, protocol string, port int, excludeChainID int64) error {
	if port <= 0 {
		return nil
	}
	occupants, err := s.st.PortOccupants(ctx, serverID)
	if err != nil {
		return err
	}
	return findPortConflict(occupants, protocol, port, excludeChainID)
}
```

接线 `handleCreateNode`（`nodes.go:218-223` 的 `checkPortInRanges` 块之后）：

```go
		if err := s.checkPortConflict(r.Context(), req.ServerID, req.Protocol, *req.Port, 0); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
```

接线 `handleCreateChain`（`chains.go`）：
- 入口端口段校验（`:326-329` 的 `checkPortInRanges` 之后、`entryPort = *req.EntryPort` 之后）：仅非 vless 校验（vless 入口走共享端点合并）——注意入口监听是 dokodemo 管道（tcp 层），用 `shared.PortLayers(req.Node.Protocol)` 对应协议即可，因为 P1 期入口管道恒为 tcp，传协议仅用于报错文案与层推导；直接以 `"dokodemo-door"` 语义传 tcp 层太隐晦，统一传 `req.Node.Protocol`（P1 各协议层推导结果对入口管道场景等价）：

```go
		if entryPort > 0 && req.Node.Protocol != shared.ProtocolVLESS {
			if err := s.checkPortConflict(r.Context(), entrySrv.ID, req.Node.Protocol, entryPort, 0); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
```

- 出口节点端口校验（`:340-344` 的 `checkPortInRanges` 之后）：

```go
		if req.Node.Port != nil {
			if err := s.checkPortConflict(r.Context(), exitSrv.ID, req.Node.Protocol, *req.Node.Port, 0); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
```

- `handleEditChain`：找到其中对 `EntryPort`/`Node.Port` 做 `checkPortInRanges` 的对应位置（编辑链路径，镜像 create 的校验段），插入相同调用，但 `excludeChainID` 传 `req.ChainID`；vless 入口同样跳过。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/backend && go test ./internal/panel/ -run TestFindPortConflict -v && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/ports.go src/backend/internal/panel/ports_test.go src/backend/internal/panel/nodes.go src/backend/internal/panel/chains.go
git commit -m "feat(panel): 端口冲突前置校验（TCP/UDP 分层，编辑排除自身）"
```

---

### Task 4: agent — pickPort 传输层分层探测

**Files:**
- Modify: `src/agent/internal/xray/fill.go:309-355`（`pickPort`）
- Modify: `src/agent/internal/xray/manager.go:117`、`src/agent/internal/xray/rebuild.go:60`、`src/agent/internal/xray/chain.go:403-413`（`pickChainPort`）
- Test: `src/agent/internal/xray/chain_test.go`（追加；现有 TestPickPort* 在 `:335-405`）

**Interfaces:**
- 签名变更：`func (m *Manager) pickPort(preferred int, candidates []int, tag string, layers string) (int, error)`。
- 调用方：manager.go/rebuild.go 传 `shared.PortLayers(vc.Protocol)`；`pickChainPort` 内部两个调用点传 `"tcp"`（链路管道/端点本期均 tcp）。
- Produces: `func probePortFree(layers string, port int) error`（包级私有）。

- [ ] **Step 1: 写失败测试**

追加到 `src/agent/internal/xray/chain_test.go`：

```go
func TestPickPortLayeredProbe(t *testing.T) {
	// 占用一个 UDP 端口：udp 层探测应冲突，tcp 层探测应放行
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	udpPort := pc.LocalAddr().(*net.UDPAddr).Port

	m := &Manager{}
	if _, err := m.pickPort(udpPort, nil, "", "udp"); err == nil {
		t.Error("UDP 被占用时 layers=udp 应报冲突")
	}
	if got, err := m.pickPort(udpPort, nil, "", "tcp"); err != nil {
		t.Errorf("UDP 被占用不影响 tcp 层: %v", err)
	} else if got != udpPort {
		t.Errorf("tcp 层应放行并取得端口 %d，实际 %d", udpPort, got)
	}
}
```

（文件头部 import 需有 `net`；参照同文件既有 TestPickPort* 的 Manager 构造方式。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/agent && go test ./internal/xray/ -run TestPickPortLayeredProbe -v`
Expected: FAIL（编译错误：pickPort 参数数量不符）

- [ ] **Step 3: 实现**

`fill.go` 中 `pickPort` 签名加 `layers string`，三处 `net.Listen("tcp", ...)` 替换为 `probePortFree(layers, port)`；新增：

```go
// probePortFree 按传输层探测端口是否空闲（UDP/TCP 是独立端口空间，分层探测）。
func probePortFree(layers string, port int) error {
	if strings.Contains(layers, "tcp") {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			return err
		}
		l.Close()
	}
	if strings.Contains(layers, "udp") {
		c, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
		if err != nil {
			return err
		}
		c.Close()
	}
	return nil
}
```

报错文案保持 `端口 %d 被占用: %w`。更新 4 个调用点：
- `manager.go:117` → `m.pickPort(vc.Port, portCandidates, tag, shared.PortLayers(vc.Protocol))`
- `rebuild.go:60` → 同上
- `chain.go:408/413`（pickChainPort 内）→ `m.pickPort(preferred, candidates, tag, "tcp")` 与 `m.pickPort(0, candidates, tag, "tcp")`

既有测试（TestPickPort*）同步补第四个实参（`"tcp"`）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/agent && go test ./internal/xray/ -v -run 'TestPickPort|TestPickChainPort'`
Expected: PASS（含既有用例）

- [ ] **Step 5: Commit**

```bash
git add src/agent/internal/xray/
git commit -m "feat(agent): pickPort 按 TCP/UDP 分层探测"
```

---

### Task 5: backend + 契约 — vmess cipher 贯通与订阅输出

**Files:**
- Modify: `src/backend/internal/panel/nodes.go`（`createNodeRequest:80-99`、`normalize():102-194`、`buildVirtualConfig():422-491`）
- Modify: `src/backend/internal/sub/sub.go:803`（vmess `p.Cipher = "auto"`）、`src/backend/internal/sub/links.go:55-66`（vmess JSON 加 `scy`）、`src/backend/internal/sub/singbox.go`（vmess outbound 加 `security`）
- Modify: `docs/openapi.yaml`（VirtualConfig schema `:902-920` 加 `cipher`；注意契约中没有 node 创建请求 schema——前端 `CreateNodeRequest` 是手写类型）
- Modify: `src/frontend/src/lib/types.ts:411-430`（手写的 `CreateNodeRequest` 加 `cipher?: string`）
- Test: `src/backend/internal/panel/nodes_test.go`、`src/backend/internal/sub/links_test.go`（或就近的 sub 测试文件）

**Interfaces:**
- Consumes: `shared.VMessCiphers`、`VirtualConfig.Cipher`（Task 1）。
- Produces:
  - `createNodeRequest.Cipher string \`json:"cipher"\``（vmess，默认 auto；其他协议强制清空）。
  - 订阅侧包级私有 `func vmessCipher(configTemplate json.RawMessage) string`（解析 VirtualConfig.Cipher，空/解析失败回退 `"auto"`），links/sub/singbox 三处共用（放在 `sub/links.go` 或新 `sub/cipher.go`）。

- [ ] **Step 1: 写失败测试**

`src/backend/internal/panel/nodes_test.go` 追加：

```go
func TestNormalizeVMessCipher(t *testing.T) {
	req := &createNodeRequest{Protocol: shared.ProtocolVMess, Cipher: "aes-128-gcm"}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Cipher != "aes-128-gcm" {
		t.Errorf("cipher 应保留 aes-128-gcm，实际 %q", req.Cipher)
	}

	req = &createNodeRequest{Protocol: shared.ProtocolVMess}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Cipher != "auto" {
		t.Errorf("cipher 默认应为 auto，实际 %q", req.Cipher)
	}

	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Cipher: "none"}
	if err := req.normalize(); err == nil {
		t.Error("非法 cipher 应报错")
	}

	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Cipher: "aes-128-gcm"}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Cipher != "" {
		t.Errorf("非 vmess 协议 cipher 应清空，实际 %q", req.Cipher)
	}
}
```

`src/backend/internal/sub/links_test.go`（或就近文件）追加 vmess cipher 用例：构造 `store.Node{Protocol: "vmess", ConfigTemplate: json.RawMessage(`{"protocol":"vmess","cipher":"chacha20-poly1305", ...}`)}`，断言生成的 vmess 链接 base64 解码后 JSON 的 `"scy"` 字段为 `chacha20-poly1305`。参照该文件既有 vmess 用例的构造与解码方式。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd src/backend && go test ./internal/panel/ -run TestNormalizeVMessCipher -v`
Expected: FAIL（unknown field Cipher）

- [ ] **Step 3: 实现**

`nodes.go`：
- `createNodeRequest` 增加 `Cipher string \`json:"cipher"\``，注释 `// vmess 客户端 cipher，默认 auto`。
- `normalize()` 的 `case shared.ProtocolVMess, shared.ProtocolTrojan:` 拆分：

```go
	case shared.ProtocolVMess:
		req.Flow = ""
		if req.Cipher == "" {
			req.Cipher = shared.VMessCipherAuto
		}
		if !shared.ValidValue(req.Cipher, shared.VMessCiphers) {
			return fmt.Errorf("不支持的 vmess cipher: %s", req.Cipher)
		}
	case shared.ProtocolTrojan:
		req.Flow = ""
		req.Cipher = ""
```

其余协议分支末尾（vless/ss/socks/http/dokodemo）确保 `req.Cipher = ""`（可在 normalize 开头统一处理：`if req.Protocol != shared.ProtocolVMess { req.Cipher = "" }`，二选一，保持一处真相）。
- `buildVirtualConfig()` 返回的 `shared.VirtualConfig` 增加 `Cipher: req.Cipher`。

订阅侧 `sub/cipher.go`（新建）：

```go
package sub

import (
	"encoding/json"

	"lattix/shared"
)

// vmessCipher 读取面板侧 VirtualConfig.Cipher（订阅提示；空/损坏回退 auto）。
func vmessCipher(configTemplate json.RawMessage) string {
	var vc shared.VirtualConfig
	if err := json.Unmarshal(configTemplate, &vc); err == nil && vc.Cipher != "" {
		return vc.Cipher
	}
	return shared.VMessCipherAuto
}
```

- `sub/sub.go:803`：`p.Cipher = "auto"` → `p.Cipher = vmessCipher(n.ConfigTemplate)`（`n` 为该函数内节点变量，沿用既有变量名；若参数是 `store.Node` 则字段为 `ConfigTemplate`）。
- `sub/links.go:55-66` vmess 分支：组装的 JSON map 增加 `"scy": vmessCipher(...)`（同样取该函数内节点的 `ConfigTemplate`）。
- `sub/singbox.go` vmess 分支：outbound map 增加 `"security": vmessCipher(...)`。

`docs/openapi.yaml`：VirtualConfig schema（`:902-920`，搜 `encryption: {type: string}` 所在块）在 `encryption` 行后加：

```yaml
        cipher: {type: string}
```

`src/frontend/src/lib/types.ts:411-430`：`CreateNodeRequest`（手写接口，非契约生成）在 `method?: string` 后加 `cipher?: string`。

然后在 `src/frontend` 跑 `npm run generate:api` 重新生成契约类型（VirtualConfig.cipher 进入 `api-contract.generated.ts`）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd src/backend && go test ./internal/panel/ ./internal/sub/ -v -run 'Cipher|VMess'` && `cd src/frontend && npm run check:api`
Expected: PASS + 契约一致

- [ ] **Step 5: Commit**

```bash
git add src/backend/internal/panel/nodes.go src/backend/internal/panel/nodes_test.go src/backend/internal/sub/ docs/openapi.yaml src/frontend/src/lib/api-contract.generated.ts src/frontend/src/lib/types.ts
git commit -m "feat(panel,sub): vmess cipher 贯通创建/订阅/契约"
```

---

### Task 6: frontend — 链路表单全协议暴露

**Files:**
- Modify: `src/frontend/src/pages/chains/use-chain-form.ts`
- Modify: `src/frontend/src/pages/chains/ChainFormDialog.tsx`

**Interfaces:**
- Consumes: 后端 `createNodeRequest` 全部字段（Task 5 后含 `cipher`）；生成的 `CreateNodeRequest` 类型。
- Produces: 无新增导出签名变更（`ChainFormState` 增加 `serviceName/method/cipher` 三个 string 字段；`ChainFormController` 自动携带）。

- [ ] **Step 1: 常量与状态**

`use-chain-form.ts`：

```ts
// 与后端 shared 包保持一致的协议/选项常量。
export const DIRECT_PROTOCOLS = [
  'vless',
  'vmess',
  'trojan',
  'shadowsocks',
  'socks',
  'http',
  'dokodemo-door',
] as const
export const RELAY_PROTOCOLS = ['vless', 'vmess', 'trojan', 'shadowsocks', 'socks', 'http'] as const
export const NETWORKS = ['tcp', 'xhttp', 'grpc']
export const SS_METHODS = [
  { value: '2022-blake3-aes-128-gcm', label: '2022-blake3-aes-128-gcm（推荐）' },
  { value: '2022-blake3-aes-256-gcm', label: '2022-blake3-aes-256-gcm' },
  { value: '2022-blake3-chacha20-poly1305', label: '2022-blake3-chacha20-poly1305' },
  { value: 'aes-128-gcm', label: 'aes-128-gcm' },
  { value: 'aes-256-gcm', label: 'aes-256-gcm' },
  { value: 'chacha20-ietf-poly1305', label: 'chacha20-ietf-poly1305' },
]
export const VMESS_CIPHERS = [
  { value: 'auto', label: 'auto（推荐）' },
  { value: 'aes-128-gcm', label: 'aes-128-gcm' },
  { value: 'chacha20-poly1305', label: 'chacha20-poly1305' },
]
// 协议一句话定位（不懂协议的用户按此选择）。
export const PROTOCOL_LABELS: Record<string, string> = {
  vless: 'VLESS（推荐 · Reality 抗封锁最强）',
  vmess: 'VMess（兼容性广）',
  trojan: 'Trojan（兼容性广）',
  shadowsocks: 'Shadowsocks（轻量 · 特征明显）',
  socks: 'SOCKS5（明文 · 特殊用途）',
  http: 'HTTP（明文 · 特殊用途）',
  'dokodemo-door': '端口转发',
}

const REALITY_PROTOCOLS = ['vless', 'vmess', 'trojan']
```

`ChainFormState` 增加三字段（`encryption` 之后）：`serviceName: string`、`method: string`、`cipher: string`；`initialChainForm` 对应加 `serviceName: 'grpc'`、`method: '2022-blake3-aes-128-gcm'`、`cipher: 'auto'`。

新增协议切换纠偏（替换 dialog 里的直接 `patch({ protocol: v })`）：

```ts
const onProtocolChange = (value: string | null) => {
  if (!value) return
  setForm((current) => ({
    ...current,
    protocol: value,
    // 跨协议纠偏：flow/encryption 仅 vless 有意义
    flow: value === 'vless' ? current.flow : 'none',
    encryption: value === 'vless' ? current.encryption : 'none',
  }))
}
```

并从 return 对象导出 `onProtocolChange`。

- [ ] **Step 2: 提交映射（onSubmit）**

`use-chain-form.ts` 的 `onSubmit` 中 `isReality` 块内（`:345-375`）：
- `nodeBody.network = form.network` 已存在；在其后追加 grpc 分支：

```ts
      if (form.network === 'grpc') {
        nodeBody.service_name = form.serviceName.trim() || 'grpc'
      }
```

- `isReality` 块外、`dokodemo-door` 块之前追加：

```ts
    if (form.protocol === 'shadowsocks') {
      nodeBody.method = form.method
    }
    if (form.protocol === 'vmess') {
      nodeBody.cipher = form.cipher
    }
```

注意 vmess/trojan 现在也走 `isReality` 块（reality 字段随 protocol 自动出现），vless 专属的 flow/encryption 提交逻辑已限定 `form.protocol === 'vless'`，无需改动。

- [ ] **Step 3: 编辑回填（openEdit）**

`openEdit` 的 `setForm({...})` 中（`:238-264`）追加三行（`encryption` 行之后）：

```ts
      serviceName: String(virtual.service_name || 'grpc'),
      method: String(virtual.method || '2022-blake3-aes-128-gcm'),
      cipher: String(virtual.cipher || 'auto'),
```

- [ ] **Step 4: 对话框渲染（ChainFormDialog.tsx）**

a) 协议选择器（`:356-370`）：onValueChange 改用 `controller.onProtocolChange`；SelectItem 文案改用 `PROTOCOL_LABELS[p] ?? p`：

```tsx
<Select value={form.protocol} onValueChange={onProtocolChange}>
  ...
  {(form.chainType === 'direct' ? DIRECT_PROTOCOLS : RELAY_PROTOCOLS).map((p) => (
    <SelectItem key={p} value={p}>
      {PROTOCOL_LABELS[p] ?? p}
    </SelectItem>
  ))}
</Select>
```

（从 controller 解构 `onProtocolChange`；import 增加 `PROTOCOL_LABELS`、`SS_METHODS`、`VMESS_CIPHERS`。）

b) grpc serviceName 输入（`isReality` 块内、xhttp 分支之后）：

```tsx
              {form.network === 'grpc' && (
                <div className="space-y-2">
                  <Label htmlFor="grpcServiceName">gRPC serviceName</Label>
                  <Input
                    id="grpcServiceName"
                    value={form.serviceName}
                    onChange={(e) => patch({ serviceName: e.target.value })}
                    placeholder="grpc"
                  />
                </div>
              )}
```

c) ss method 与 vmess cipher（`isReality` 块之后、dokodemo 块之前）：

```tsx
          {form.protocol === 'shadowsocks' && (
            <div className="space-y-2">
              <Label>加密方式（method）</Label>
              <Select value={form.method} onValueChange={(v) => v && patch({ method: v })} items={SS_METHODS}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SS_METHODS.map((m) => (
                    <SelectItem key={m.value} value={m.value}>
                      {m.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          {form.protocol === 'vmess' && (
            <div className="space-y-2">
              <Label>加密方式（cipher）</Label>
              <Select value={form.cipher} onValueChange={(v) => v && patch({ cipher: v })} items={VMESS_CIPHERS}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {VMESS_CIPHERS.map((c) => (
                    <SelectItem key={c.value} value={c.value}>
                      {c.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
```

（Select 的 `items` 用法参照既有 VLESS_ENCS 下拉 `:443-459`；FLOWS 下拉未用 items、手写 SelectContent——两种写法均存在，保持一致即可。）

- [ ] **Step 5: 验证**

Run: `cd src/frontend && npm run lint && npm test && npm run build`
Expected: 全绿（无 vitest 用例覆盖本文件时 lint+build 为准）

- [ ] **Step 6: Commit**

```bash
git add src/frontend/src/pages/chains/
git commit -m "feat(frontend): 链路表单暴露 vmess/trojan/ss 全协议与 grpc 传输"
```

---

### Task 7: e2e — protocols.sh 扩展 + 存量链路回归

**Files:**
- Modify: `scripts/e2e/protocols.sh`
- Modify: `scripts/e2e/chains.sh`（存量链路编辑回归用例）

- [ ] **Step 1: 增加 RPC 失败断言助手**

`rpc_data` 定义（`:53-61`）之后追加：

```bash
# rpc_expect_fail <method> <path> <body>：断言 RPC 业务失败（code 非 OK/ACCEPTED）。
rpc_expect_fail() {
    local out
    out="$(rpc_raw "$@")"
    python3 -c 'import json,sys; v=json.loads(sys.argv[1]); assert v["code"] not in ("OK","ACCEPTED"), v' "$out" \
        || { echo "FAIL: 预期 RPC 失败但成功: $out"; exit 1; }
}
```

- [ ] **Step 2: 增加用例**

在 `echo ">> dokodemo-door"` 段（`:154-155`）之后追加：

```bash
echo ">> vmess cipher=chacha20-poly1305"
R="$(create_node '{"server_id":1,"protocol":"vmess","cipher":"chacha20-poly1305"}')"
check_port "$R"

echo ">> 端口冲突前置校验"
CLASH_PORT=23456
R="$(create_node "{\"server_id\":1,\"protocol\":\"trojan\",\"port\":$CLASH_PORT}")"
check_port "$R"
rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"vless\",\"port\":$CLASH_PORT}"
rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"trojan\",\"port\":$CLASH_PORT}"
rpc_expect_fail POST /api/node/create "{\"server_id\":1,\"protocol\":\"shadowsocks\",\"port\":$CLASH_PORT}"
echo "   同层同端口（异协议/同协议/跨层 ss）均被 400 拦截 OK"
```

- [ ] **Step 3: 订阅断言更新**

`:176-190` 区域：
- `check "cipher: auto"` 之后追加 `check "cipher: chacha20-poly1305"`。
- 代理总数从 9 改为 10（`PROXY_COUNT -eq 10`，注释同步）。

- [ ] **Step 4: chains.sh 存量链路编辑回归（防端口冲突误杀）**

在 `scripts/e2e/chains.sh` 中 CH2 retry 成功（`:303` 的 `wait_chain "$CH2" active 60`）之后插入：

```bash
echo ">> 回归：编辑存量链路（原样参数）→ 不因自身端口占用误报冲突"
# vless 共享链：编辑仅改名，入口共享监听与出口节点均为自身占用，须放行
rpc_data POST /api/chain/edit "{\"chain_id\":$CH1,\"name\":\"链A回归\",\"hops\":[{\"server_id\":$AID},{\"server_id\":$CID}],\"node\":{\"protocol\":\"vless\"},\"traffic_multiplier\":\"1.000\"}" >/dev/null
wait_chain "$CH1" active 90 && echo "OK: vless 存量链编辑通过（excludeChainID 生效）"
# ss 明文链：entry_port 为自身 forward 占用，编辑原样保存须放行
rpc_data POST /api/chain/edit "{\"chain_id\":$CH2,\"name\":\"链B回归\",\"hops\":[{\"server_id\":$AID},{\"server_id\":$CID}],\"entry_port\":$BLOCK_PORT,\"node\":{\"protocol\":\"shadowsocks\",\"method\":\"aes-256-gcm\"},\"traffic_multiplier\":\"1.000\"}" >/dev/null
wait_chain "$CH2" active 60 && echo "OK: ss 存量链编辑通过"
```

注意：该用例依赖 Task 3 的 `excludeChainID` 正确接线到 `handleEditChain`；若编辑接口要求 node 表单字段完整，按 `editChainRequest`（`chains.go:226-233`）对齐字段。

- [ ] **Step 5: 全量 e2e 回归（存量链路不失效的证明）**

依次运行链路相关 e2e 脚本（release CI 矩阵的子集，全部本地可跑）：

```bash
bash scripts/e2e/protocols.sh    # 全协议 + 端口冲突 + cipher
bash scripts/e2e/chains.sh       # 链生命周期 + 存量编辑回归 + 真实流量
bash scripts/e2e/links.sh        # 订阅/分享链接输出一致性
bash scripts/e2e/groups.sh       # 分组订阅与派生链
bash scripts/e2e/usernodes.sh    # 按用户节点操作
bash scripts/e2e/reconcile.sh    # 漂移自愈
bash scripts/e2e/vlessenc.sh     # vless Encryption 数据面
```

Expected: 每个脚本结尾均输出对应 `PASS`。任一失败即回退对应 Task 排查，不允许带病提交。

- [ ] **Step 6: 全仓回归 + Commit**

```bash
cd src/shared && go test ./... && cd ../backend && go test ./... && cd ../agent && go test ./...
cd ../frontend && npm run build && npm test && npm run lint
git add scripts/e2e/protocols.sh scripts/e2e/chains.sh
git commit -m "test(e2e): protocols 覆盖 vmess cipher 与端口冲突；chains 覆盖存量链路编辑回归"
```

---

## 自检记录

- **Spec 覆盖**：P1 范围 = 前端暴露 vmess/trojan/ss（Task 6）+ reality 门禁放宽（Task 6 REALITY_PROTOCOLS）+ grpc（Task 6 NETWORKS/serviceName）+ 端口冲突前置校验（Task 2/3/4）+ vmess cipher（Task 1/5）+ 存量链路回归（Task 7 Step 4/5 + Global Constraints 红线）。ws/httpupgrade/TLS/hy2 属 P2-P4，不在本计划。
- **类型一致性**：`PortLayers/LayersOverlap/VMessCiphers`（Task 1）↔ Task 2/3/4 调用一致；`PortOccupant` 字段（Task 2）↔ `findPortConflict`（Task 3）一致；`pickPort` 新签名 4 实参（Task 4）↔ 全部调用点已列出；`vmessCipher(json.RawMessage)`（Task 5）三处订阅共用。
