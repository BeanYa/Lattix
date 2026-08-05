# 跨 Profile 共享入口端口实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 同一服务器上任意端口允许不同 profile 的 VLESS 链自动加入既有受管监听（共享入口），并以前端"共享入口"徽标与端口占用提示明示语义。

**Architecture:** 后端 `store.EnsureSharedEndpoint` 将"同 server+port+protocol 即复用"的规则从 profile 相等放宽到任意 VLESS profile（保留 protocol 防御性冲突）；面板 DTO 增加 `entry_shared`；reconcile/订阅按端点聚合的现有机制无需改动。前端基于链列表本地计算端口占用提示，用 DTO 字段显示徽标。

**Tech Stack:** Go（backend/store/panel/dispatch/sub），React + TS（frontend），vitest/oxlint，SQLite。

**Spec:** `docs/superpowers/specs/2026-08-06-shared-port-join-design.md`

## Global Constraints

- 端点行永久性不变：不新增 remove 流程、不修改 `SetSharedEndpointActive` 的重复端口防御检查。
- 端口=0（自动）路径不变：仍按 profile 复用或新建。
- 订阅渲染不变：链条目始终取端点 realized 参数（`sub.go:742-772`）。
- 提交信息遵循仓库风格：`feat(store|panel|frontend|sub): ...`。
- 测试命令（workdir `src/backend`）：`go test ./internal/store/... ./internal/panel/... ./internal/dispatch/... ./internal/sub/...`；前端（workdir `src/frontend`）：`npx tsc -b && npm test && npm run lint`。

---

### Task 1: Store — EnsureSharedEndpoint 放宽为任意 VLESS profile 加入 + EndpointChainCount

**Files:**
- Modify: `src/backend/internal/store/endpoints.go:66-91`
- Modify: `src/backend/internal/store/endpoints_test.go:22-36`
- Test: `src/backend/internal/store/endpoints_test.go`

**Interfaces:**
- Consumes: 现有 `SharedEndpoint`、`scanEndpoint`、`ErrEndpointConflict`。
- Produces: `EnsureSharedEndpoint` 新语义（同 server+port+protocol 返回现有端点，`created=false`；protocol 不同返回 `ErrEndpointConflict`）；`EndpointChainCount(ctx, endpointID int64) (int, error)`（供 Task 2 的 `entry_shared`）。

- [ ] **Step 1: 更新现有断言测试（join 语义 + protocol 门槛）**

在 `src/backend/internal/store/endpoints_test.go` 的 `TestSharedEndpointReuseAndAssignmentIdentity` 中替换冲突断言（原第 30 行）：

```go
	// 不同 profile 同端口：加入既有监听（不再冲突）；protocol 不同才冲突。
	joined, created, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-b", config)
	if err != nil || created || joined.ID != endpoint.ID {
		t.Fatalf("join incompatible profile: id=%d created=%v err=%v", joined.ID, created, err)
	}
	if _, _, err := st.EnsureSharedEndpoint(ctx, serverID, "socks", 443, "profile-b", config); !errors.Is(err, ErrEndpointConflict) {
		t.Fatalf("different protocol must conflict, got %v", err)
	}
```

（此步保持 `errors` 导入持续被使用。）

- [ ] **Step 2: 运行确认失败**

Run（workdir `src/backend`）：`go test ./internal/store/ -run TestSharedEndpointReuseAndAssignmentIdentity -v`
Expected: FAIL —— join 断言仍返回 `ErrEndpointConflict`。

- [ ] **Step 3: 实现 join 语义**

修改 `src/backend/internal/store/endpoints.go`：

1. 更新注释（第 66-68 行）：

```go
// EnsureSharedEndpoint returns the shared listener for a server/port pair:
// an existing VLESS listener on the port is joined regardless of profile
// (entry params come from the first claimant); a listener of a different
// protocol is a conflict; an unmanaged OS listener is detected later by the
// Agent's bind probe.
```

2. 替换冲突分支（第 86-88 行）：

```go
			if endpoint.Protocol != protocol {
				return nil, false, ErrEndpointConflict
			}
			return endpoint, false, nil
```

- [ ] **Step 4: 运行确认通过**

Run：`go test ./internal/store/ -run TestSharedEndpointReuseAndAssignmentIdentity -v`
Expected: PASS

- [ ] **Step 5: 新增"不新增行"回归测试**

在 `src/backend/internal/store/endpoints_test.go` 追加：

```go
func TestEnsureSharedEndpointJoinsWithoutDuplicateRows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", config)
	if err != nil {
		t.Fatal(err)
	}
	// 同端口多行遗留（并发竞态）：ORDER BY id 取首行加入，不新增行。
	other, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-b", config)
	if err != nil || other.ID != endpoint.ID {
		t.Fatalf("join = %+v err=%v, want endpoint %d", other, err, endpoint.ID)
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM shared_endpoints WHERE server_id=? AND port=443`, serverID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("shared_endpoints rows for port 443 = %d, want 1", count)
	}
}
```

- [ ] **Step 6: 新增 EndpointChainCount 及测试**

在 `src/backend/internal/store/endpoints.go` 追加：

```go
// EndpointChainCount 返回引用端点的未删链数量（entry_shared 聚合用；
// 不沿用 ChainIDsByEndpoint——它只统计 active/degraded 链）。
func (s *Store) EndpointChainCount(ctx context.Context, endpointID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chains WHERE endpoint_id=? AND deleted_at IS NULL`, endpointID).Scan(&count)
	return count, err
}
```

在 `src/backend/internal/store/endpoints_test.go` 追加：

```go
func TestEndpointChainCount(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", MachineTypeDirect, "", "", "US", "")
	config := json.RawMessage(`{"protocol":"vless","port":443,"template":{}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile", config)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := st.EndpointChainCount(ctx, endpoint.ID); err != nil || count != 0 {
		t.Fatalf("count before chains = %d err=%v", count, err)
	}
	deployment, err := st.CreateInitialChainDeployment(ctx, InitialChainDeployment{
		Name: "a", ServiceServerID: serverID, ServiceProtocol: shared.ProtocolVLESS,
		ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "svc-a",
		TrafficMultiplierMilli: 1000,
		Hops:                   []InitialChainHop{{ServerID: serverID, Role: HopRoleExit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := st.EndpointChainCount(ctx, endpoint.ID); err != nil || count != 1 {
		t.Fatalf("count after one chain = %d err=%v", count, err)
	}
	if err := st.DeleteChain(ctx, deployment.ChainID); err != nil {
		t.Fatal(err)
	}
	if count, err := st.EndpointChainCount(ctx, endpoint.ID); err != nil || count != 0 {
		t.Fatalf("count after delete = %d err=%v", count, err)
	}
}
```

- [ ] **Step 7: 运行 store 全量测试并提交**

Run：`go test ./internal/store/`
Expected: PASS

```bash
git add src/backend/internal/store/endpoints.go src/backend/internal/store/endpoints_test.go
git commit -m "feat(store): share entry port across profiles via endpoint join"
```

---

### Task 2: Panel — entry_shared DTO、清理死冲突分支、跨 profile 建链 join 测试

**Files:**
- Modify: `src/backend/internal/panel/chains.go:56-77,113-119,341-350,585-593`
- Create: `src/backend/internal/panel/chains_join_test.go`
- Test: `src/backend/internal/panel/chains_join_test.go`

**Interfaces:**
- Consumes: `store.EndpointChainCount`（Task 1）、`store.EnsureSharedEndpoint` join 语义、现有 `chainEditRequester`（chains_edit_test.go）。
- Produces: `chainDTO.EntryShared bool`（JSON `entry_shared,omitempty`）；create/edit 不再返回 `ErrEndpointConflict`（HTTP 500 兜底）。

- [ ] **Step 1: 写失败测试（跨 profile 建链共享端口 + entry_shared）**

创建 `src/backend/internal/panel/chains_join_test.go`：

```go
package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestCreateChainsShareOccupiedEntryPort 验证跨 profile 端口共享：链 B 使用链 A
// 已占用的入口端口时自动加入同一共享端点（不再 409），两链 entry_shared 均为
// true，且只产生一条 shared_endpoints 行。
func TestCreateChainsShareOccupiedEntryPort(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	requester := &chainEditRequester{online: map[int64]bool{serverID: true}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester), req: requester}

	create := func(name, shortID string) chainDTO {
		t.Helper()
		node := createNodeRequest{Name: name, ServerID: serverID, Protocol: shared.ProtocolVLESS,
			ShortID: shortID, Dest: "dl.google.com:443", ServerNames: []string{"dl.google.com"},
			Fingerprint: shared.FingerprintChrome, Network: shared.NetworkTCP, Flow: shared.FlowVision}
		if err := node.normalize(); err != nil {
			t.Fatal(err)
		}
		entryPort := 443
		body, _ := json.Marshal(createChainRequest{Name: name,
			Entry: chainHopRef{ServerID: serverID}, Exit: chainHopRef{ServerID: serverID},
			Node: node, EntryPort: &entryPort, TrafficMultiplier: "1.000"})
		req := httptest.NewRequest("POST", "/api/chain/create", bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		serverAPI.handleCreateChain(recorder, req)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", name, recorder.Code, recorder.Body.String())
		}
		var dto chainDTO
		if err := json.Unmarshal(recorder.Body.Bytes(), &dto); err != nil {
			t.Fatal(err)
		}
		return dto
	}

	chainA := create("a", "short-a")
	chainB := create("b", "short-b") // 不同 short_id → 不同 profile

	if chainA.EndpointID == 0 || chainB.EndpointID == 0 {
		t.Fatalf("chains lack endpoint: a=%d b=%d", chainA.EndpointID, chainB.EndpointID)
	}
	if chainA.EndpointID != chainB.EndpointID {
		t.Fatalf("chains did not share endpoint: %d vs %d", chainA.EndpointID, chainB.EndpointID)
	}
	if !chainA.EntryShared || !chainB.EntryShared {
		t.Fatalf("entry_shared = a:%v b:%v, want true/true", chainA.EntryShared, chainB.EntryShared)
	}
	endpoints, err := st.SharedEndpointsByServer(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("shared_endpoints rows = %d, want 1", len(endpoints))
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run（workdir `src/backend`）：`go test ./internal/panel/ -run TestCreateChainsShareOccupiedEntryPort -v`
Expected: FAIL —— 链 B 返回 409（`shared endpoint port conflicts...`）。

- [ ] **Step 3: 实现 DTO 字段与计数**

修改 `src/backend/internal/panel/chains.go`：

1. `chainDTO`（第 67-68 行后）新增：

```go
	EntryShared         bool                   `json:"entry_shared,omitempty"`
```

2. `toChainDTO`（第 113-119 行）的 `if out.EndpointID != 0` 块内追加：

```go
		if count, err := s.st.EndpointChainCount(r.Context(), out.EndpointID); err == nil {
			out.EntryShared = count >= 2
		}
```

- [ ] **Step 4: 清理 create/edit 的死冲突分支**

修改 `src/backend/internal/panel/chains.go`：

1. `handleCreateChain`（第 341-350 行）替换为：

```go
		endpoint, _, err := s.st.EnsureSharedEndpoint(r.Context(), entrySrv.ID, vc.Protocol,
			entryPort, profileHash, endpointJSON)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
```

2. `handleEditChain`（第 588-591 行）的 `writeError(w, http.StatusConflict, err.Error())` 改为 `writeError(w, http.StatusInternalServerError, err.Error())`。

- [ ] **Step 5: 运行测试确认通过**

Run：`go test ./internal/panel/ -run 'TestCreateChainsShareOccupiedEntryPort|TestEditChain' -v`
Expected: PASS（既有编辑测试不受影响）

- [ ] **Step 6: 提交**

```bash
git add src/backend/internal/panel/chains.go src/backend/internal/panel/chains_join_test.go
git commit -m "feat(panel): join shared entry listener across profiles with entry_shared flag"
```

---

### Task 3: Dispatch — join 后 reconcile 聚合两链路由/用户

**Files:**
- Modify: `src/backend/internal/dispatch/endpoint_test.go`
- Test: `src/backend/internal/dispatch/endpoint_test.go`

**Interfaces:**
- Consumes: `createDirectSharedChain`、`latestEndpointPayload`、`fakeRequester`（本包已有）、`store.EnsureSharedEndpoint` join 语义。
- Produces: 无新接口——验证 join 后的端点 reconcile 聚合（生产代码零改动）。

- [ ] **Step 1: 写测试**

在 `src/backend/internal/dispatch/endpoint_test.go` 追加：

```go
// TestReconcileSharedEndpointAggregatesJoinedChains 验证不同 profile 的链加入
// 同一端点后，reconcile 聚合两链的路由与用户（join 经 store 语义，端点行唯一）。
func TestReconcileSharedEndpointAggregatesJoinedChains(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, _ := st.CreateServer(ctx, "entry", "entry.test", "token", store.MachineTypeDirect, "", "", "US", "")
	configA := json.RawMessage(`{"protocol":"vless","port":443,"template":{"dest":"a.example.com"}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-a", configA)
	if err != nil {
		t.Fatal(err)
	}
	configB := json.RawMessage(`{"protocol":"vless","port":443,"template":{"dest":"b.example.com"}}`)
	joined, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 443, "profile-b", configB)
	if err != nil || joined.ID != endpoint.ID {
		t.Fatalf("join: id=%d err=%v, want endpoint %d", joined.ID, err, endpoint.ID)
	}
	chainA := createDirectSharedChain(t, st, serverID, endpoint.ID, "a")
	chainB := createDirectSharedChain(t, st, serverID, endpoint.ID, "b")
	userA, _ := st.InsertUser(ctx, "user-a", "global-a", "sub-a", nil)
	userB, _ := st.InsertUser(ctx, "user-b", "global-b", "sub-b", nil)
	if _, _, err := st.SetUserChains(ctx, userA, []int64{chainA.ChainID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userB, []int64{chainB.ChainID}); err != nil {
		t.Fatal(err)
	}

	d := New(st, &fakeRequester{online: map[int64]bool{}})
	if err := d.ReconcileSharedEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	payload := latestEndpointPayload(t, st)
	if len(payload.Routes) != 2 || len(payload.Clients) != 2 {
		t.Fatalf("payload routes/clients = %d/%d, want 2/2", len(payload.Routes), len(payload.Clients))
	}
	seen := map[int64]bool{}
	for _, route := range payload.Routes {
		seen[route.ChainID] = true
	}
	if !seen[chainA.ChainID] || !seen[chainB.ChainID] {
		t.Fatalf("routes missing chains: %+v", seen)
	}
}
```

- [ ] **Step 2: 运行确认通过**

Run：`go test ./internal/dispatch/ -run TestReconcileSharedEndpointAggregatesJoinedChains -v`
Expected: PASS（join 语义来自 Task 1，聚合为既有行为）

- [ ] **Step 3: 提交**

```bash
git add src/backend/internal/dispatch/endpoint_test.go
git commit -m "test(dispatch): joined chains aggregate on shared endpoint reconcile"
```

---

### Task 4: Sub — join 后两条链订阅均渲染端点参数

**Files:**
- Modify: `src/backend/internal/sub/publish_chain_proxies_test.go`
- Test: `src/backend/internal/sub/publish_chain_proxies_test.go`

**Interfaces:**
- Consumes: `store.EnsureSharedEndpoint` join 语义、`store.CreateInitialChainDeployment`、`sub.New`（本包）。
- Produces: 无新接口——验证 join 后订阅渲染（生产代码零改动）。

- [ ] **Step 1: 写测试**

在 `src/backend/internal/sub/publish_chain_proxies_test.go` 追加：

```go
// TestPublishUserIncludesJoinedChainsOnSharedEndpoint 验证跨 profile 加入同一
// 共享端点的两条链都出现在订阅中（条目参数取自端点 realized_config，与链自身
// 的 service config 无关）。
func TestPublishUserIncludesJoinedChainsOnSharedEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	entryID, _ := st.CreateServer(ctx, "entry", "entry.example.com", "token-entry", store.MachineTypeDirect, "", "", "US", "")
	exitID, _ := st.CreateServer(ctx, "exit", "exit.example.com", "token-exit", store.MachineTypeDirect, "", "", "JP", "")

	configA := json.RawMessage(`{"protocol":"vless","port":443,"template":{"dest":"a.example.com"}}`)
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile-a", configA)
	if err != nil {
		t.Fatal(err)
	}
	configB := json.RawMessage(`{"protocol":"vless","port":443,"template":{"dest":"b.example.com"}}`)
	joined, _, err := st.EnsureSharedEndpoint(ctx, entryID, shared.ProtocolVLESS, 443, "profile-b", configB)
	if err != nil || joined.ID != endpoint.ID {
		t.Fatalf("join: id=%d err=%v, want endpoint %d", joined.ID, err, endpoint.ID)
	}
	realized := json.RawMessage(`{"port":443,"network":"tcp","public_key":"key","short_id":"short","server_name":"example.com"}`)
	if err := st.SetSharedEndpointActive(ctx, endpoint.ID, realized); err != nil {
		t.Fatal(err)
	}

	deploy := func(name string, config json.RawMessage) int64 {
		t.Helper()
		deployment, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
			Name: name, ServiceServerID: exitID, ServiceProtocol: shared.ProtocolVLESS,
			ServiceConfig: config, EndpointID: endpoint.ID, ServiceUUID: "svc-" + name,
			TrafficMultiplierMilli: 1000,
			Hops: []store.InitialChainHop{
				{ServerID: entryID, Role: store.HopRoleEntry},
				{ServerID: exitID, Role: store.HopRoleExit},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.PublishChainRevision(ctx, deployment.RevisionID, false); err != nil {
			t.Fatal(err)
		}
		return deployment.ChainID
	}
	chainA := deploy("JP共享A", configA)
	chainB := deploy("JP共享B", configB)

	userID, _ := st.InsertUser(ctx, "Bean", "global-user-uuid", "bean-token", nil)
	if _, _, err := st.SetUserChains(ctx, userID, []int64{chainA, chainB}); err != nil {
		t.Fatal(err)
	}

	result, err := New(st, nil, nil).PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	clash := string(result.Files["clash"])
	for _, want := range []string{"JP共享A", "JP共享B"} {
		if !strings.Contains(clash, want) {
			t.Fatalf("published clash snapshot missing %q:\n%s", want, clash)
		}
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", result.Warnings)
	}
}
```

- [ ] **Step 2: 运行确认通过**

Run：`go test ./internal/sub/ -run TestPublishUserIncludesJoinedChainsOnSharedEndpoint -v`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add src/backend/internal/sub/publish_chain_proxies_test.go
git commit -m "test(sub): joined chains render endpoint params in subscription"
```

---

### Task 5: Frontend — 共享入口徽标与端口占用提示

**Files:**
- Modify: `src/frontend/src/lib/types.ts:502-503`
- Modify: `src/frontend/src/pages/Chains.tsx`（派生值区 ~470 行后；链列表 ~1119 行；入口端口表单 ~1304-1315 行）
- Test: `npx tsc -b && npm test && npm run lint`

**Interfaces:**
- Consumes: `chainDTO.EntryShared`（Task 2，JSON `entry_shared`）。
- Produces: 无新接口。

- [ ] **Step 1: types.ts 增加字段**

在 `src/frontend/src/lib/types.ts` 的 `Chain` 接口（第 502 行 `endpoint_status?: NodeStatus` 后）插入：

```ts
  entry_shared?: boolean
```

- [ ] **Step 2: 派生提示文本**

在 `src/frontend/src/pages/Chains.tsx` 的 `hopIndexes` 定义（约第 475-476 行）之后追加：

```tsx
  const entryPortHint = (() => {
    const value = Number(entryPort)
    if (!value || !entryId) return ''
    const owner = chains.find(
      (c) =>
        c.id !== editingChainId &&
        c.entry_port === value &&
        c.hops[0]?.server_id === Number(entryId) &&
        c.status !== 'deleted',
    )
    if (!owner) return ''
    return `端口已被链路「${owner.name}」的共享监听占用，将共享其入口参数（dest/short_id 以现有监听为准）`
  })()
```

- [ ] **Step 3: 表单提示渲染**

在 `src/frontend/src/pages/Chains.tsx` 的入口端口 `Input`（第 1314 行）之后追加：

```tsx
              {entryPortHint ? <p className="text-xs text-muted-foreground">{entryPortHint}</p> : null}
```

- [ ] **Step 4: 链列表共享徽标**

在 `src/frontend/src/pages/Chains.tsx` 的入口跳端口渲染（第 1119 行 `{hopPort !== 0 ? <span>端口 {hopPort}</span> : null}`）之后追加：

```tsx
                                  {h.role === 'entry' && c.entry_shared ? (
                                    <span className="rounded bg-muted px-1 py-0.5 text-[10px] font-medium text-muted-foreground">
                                      共享入口
                                    </span>
                                  ) : null}
```

- [ ] **Step 5: 类型检查、测试、lint**

Run（workdir `src/frontend`）：`npx tsc -b && npm test && npm run lint`
Expected: 全部通过

- [ ] **Step 6: 提交**

```bash
git add src/frontend/src/lib/types.ts src/frontend/src/pages/Chains.tsx
git commit -m "feat(frontend): show shared entry badge and port reuse hint"
```

---

### Task 6: 文档 — framework-design.md 端口复用语义更新

**Files:**
- Modify: `docs/framework-design.md:579-584`

- [ ] **Step 1: 更新段落**

替换 `docs/framework-design.md` 第 582-583 行的"相同 server/port 上兼容 profile 复用既有 Endpoint，不兼容受管监听报冲突。"为：

```
相同 server/port 上任意 VLESS 链可加入既有 Endpoint 共享监听（跨 profile）：
入口参数以先占用链为准（订阅渲染端点参数保持一致），不同协议仍报冲突。
```

- [ ] **Step 2: 提交**

```bash
git add docs/framework-design.md
git commit -m "docs: shared entry port joins across profiles"
```

---

## 自审记录（实现者执行前应复核）

- Spec 覆盖：store join（Task 1）、entry_shared DTO（Task 2）、死代码清理（Task 2 Step 4）、前端徽标+提示（Task 5）、文档（Task 6）、dispatch/sub 验证（Task 3/4）——全部覆盖。
- 类型一致性：`EndpointChainCount(ctx, endpointID int64) (int, error)` 在 Task 1 定义、Task 2 消费；`chainDTO.EntryShared` 在 Task 2 定义、Task 5 消费（JSON `entry_shared`）；测试辅助 `createDirectSharedChain/latestEndpointPayload/fakeRequester/chainEditRequester` 均为既有符号。
- 已知限制（不实现）：端点行永久、孤儿监听占端口、共享端点模板不可被加入链修改。
