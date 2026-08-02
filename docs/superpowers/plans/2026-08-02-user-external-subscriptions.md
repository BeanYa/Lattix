# 用户外部订阅合并（叠加 / 并入 / 附加）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在用户与外部订阅之间建立多对多关联（`user_external_subscriptions`），以三种模式（叠加 stack / 并入 merge / 附加 nodes）把外部订阅节点并入用户订阅内容，并按模式合并 `subscription-userinfo` 流量头；提供分配 RPC 与前端弹窗。

**Architecture:** 新增 store 关联表 + `extsub.MergeUserTraffic` 纯函数；`sub` 包扩展 `proxyItem.external` 分支到四个外部节点构建器（mihomo/sing-box/quanx/links），`setSubHeaders` 与 `/api/sub/{token}/info` 用合并流量；panel 新增 `POST /api/user/set-external-subscriptions`（整表替换，沿用 set-nodes 模式）；同步完成后经 `EnqueueUsers` 异步重发布关联用户。外部订阅导入功能（`external_subscriptions`/`external_chains` 表、extsub 解析/同步）已合入 main（`2cd328c54`）。

**Tech Stack:** Go 1.26 (module `lattix/backend`)、modernc.org/sqlite、gopkg.in/yaml.v3、React + Vite + wouter + shadcn/ui、openapi-typescript codegen。

## Global Constraints

- 工作目录为仓库根；Go 命令在仓库根运行（go.work 覆盖三个 module）。
- 前端 `npm run build` 会执行 `node scripts/generate-api-types.mjs --check`，`docs/openapi.yaml` 新增路径必须与 `RegisterRoutes` 完全一致（`panel/contract_test.go` 强校验）。
- 不新增 Go / npm 依赖。
- 新表加入 `store.go` 的 `Schema` 常量即自动创建；`schemaVersion` 不变（不迁移既有表）。
- 变更操作全部使用 POST（前端 requester 无 PUT/DELETE）。
- 中文 UI 文案，遵循现有页面风格。
- 外部订阅流量（`external_subscriptions` 表）为同步快照，`total=0` 视为额度未知不参与合并。
- 每次任务结束必须跑测试并提交。

---

### Task 1: Store — 用户外部订阅关联表

**Files:**
- Modify: `src/backend/internal/store/store.go`（Schema 常量末尾追加表）
- Create: `src/backend/internal/store/user_external_subscriptions.go`
- Test: `src/backend/internal/store/user_external_subscriptions_test.go`

**Interfaces:**
- Consumes: `store.Open`、`InsertUser`、`CreateExternalSubscription`、`DeleteExternalSubscription`（已存在）。
- Produces（Task 2/6/7/8 使用）：
  ```go
  const (
      ExtSubModeStack = "stack"
      ExtSubModeMerge = "merge"
      ExtSubModeNodes = "nodes"
  )
  type UserExternalSubscription struct {
      ID             int64
      UserID         int64
      SubscriptionID int64
      Mode           string
      CreatedAt      time.Time
      UpdatedAt      time.Time
  }
  type UserExternalSubscriptionJoined struct {
      UserID         int64
      SubscriptionID int64
      Mode           string
      Name           string
      Upload         int64
      Download       int64
      Total          int64
      Expire         *int64
      NodeCount      int
  }
  func (s *Store) SetUserExternalSubscriptions(ctx context.Context, userID int64, items []UserExternalSubscription) error
  func (s *Store) ListUserExternalSubscriptions(ctx context.Context, userID int64) ([]UserExternalSubscriptionJoined, error)
  func (s *Store) UsersByExternalSubscriptionID(ctx context.Context, subscriptionID int64) ([]int64, error)
  ```

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/store/user_external_subscriptions_test.go`：

```go
package store

import (
	"context"
	"testing"
)

func insertTestExtSub(t *testing.T, st *Store, name, url string, upload, download, total int64) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := st.CreateExternalSubscription(ctx, ExternalSubscription{
		Name: name, URL: url, Upload: upload, Download: download, Total: total,
		AutoUpdate: true, UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUserExternalSubscriptionsSetAndList(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "token-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	subA := insertTestExtSub(t, st, "机场A", "https://sub.example.com/a", 100, 200, 1000)
	subB := insertTestExtSub(t, st, "机场B", "https://sub.example.com/b", 10, 20, 100)

	if err := st.SetUserExternalSubscriptions(ctx, userID, []UserExternalSubscription{
		{UserID: userID, SubscriptionID: subA, Mode: ExtSubModeStack},
		{UserID: userID, SubscriptionID: subB, Mode: ExtSubModeMerge},
	}); err != nil {
		t.Fatal(err)
	}
	joined, err := st.ListUserExternalSubscriptions(ctx, userID)
	if err != nil || len(joined) != 2 {
		t.Fatalf("joined = %+v err %v", joined, err)
	}
	if joined[0].Name != "机场A" || joined[0].Mode != ExtSubModeStack ||
		joined[0].Upload != 100 || joined[0].Total != 1000 {
		t.Fatalf("joined[0] = %+v", joined[0])
	}
	if joined[1].Name != "机场B" || joined[1].Mode != ExtSubModeMerge {
		t.Fatalf("joined[1] = %+v", joined[1])
	}

	// 整表替换：去掉 B，A 改为附加。
	if err := st.SetUserExternalSubscriptions(ctx, userID, []UserExternalSubscription{
		{UserID: userID, SubscriptionID: subA, Mode: ExtSubModeNodes},
	}); err != nil {
		t.Fatal(err)
	}
	joined, err = st.ListUserExternalSubscriptions(ctx, userID)
	if err != nil || len(joined) != 1 || joined[0].Mode != ExtSubModeNodes {
		t.Fatalf("after replace = %+v err %v", joined, err)
	}

	otherID, err := st.InsertUser(ctx, "bob", "00000000-0000-0000-0000-0000000000bb", "token-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	otherJoined, err := st.ListUserExternalSubscriptions(ctx, otherID)
	if err != nil || len(otherJoined) != 0 {
		t.Fatalf("other user joined = %+v err %v", otherJoined, err)
	}
}

func TestUsersByExternalSubscriptionID(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	aliceID, _ := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "token-a", nil)
	bobID, _ := st.InsertUser(ctx, "bob", "00000000-0000-0000-0000-0000000000bb", "token-b", nil)
	subA := insertTestExtSub(t, st, "机场A", "https://sub.example.com/a", 0, 0, 100)
	subB := insertTestExtSub(t, st, "机场B", "https://sub.example.com/b", 0, 0, 100)
	if err := st.SetUserExternalSubscriptions(ctx, aliceID, []UserExternalSubscription{
		{UserID: aliceID, SubscriptionID: subA, Mode: ExtSubModeStack},
		{UserID: aliceID, SubscriptionID: subB, Mode: ExtSubModeStack},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalSubscriptions(ctx, bobID, []UserExternalSubscription{
		{UserID: bobID, SubscriptionID: subA, Mode: ExtSubModeMerge},
	}); err != nil {
		t.Fatal(err)
	}
	users, err := st.UsersByExternalSubscriptionID(ctx, subA)
	if err != nil || len(users) != 2 {
		t.Fatalf("users of A = %v err %v", users, err)
	}
	users, err = st.UsersByExternalSubscriptionID(ctx, subB)
	if err != nil || len(users) != 1 || users[0] != aliceID {
		t.Fatalf("users of B = %v err %v", users, err)
	}
}

func TestUserExternalSubscriptionsCascade(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, _ := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "token-a", nil)
	subA := insertTestExtSub(t, st, "机场A", "https://sub.example.com/a", 0, 0, 100)
	if err := st.SetUserExternalSubscriptions(ctx, userID, []UserExternalSubscription{
		{UserID: userID, SubscriptionID: subA, Mode: ExtSubModeStack},
	}); err != nil {
		t.Fatal(err)
	}
	// 删除外部订阅 → 关联级联清理。
	if err := st.DeleteExternalSubscription(ctx, subA); err != nil {
		t.Fatal(err)
	}
	joined, err := st.ListUserExternalSubscriptions(ctx, userID)
	if err != nil || len(joined) != 0 {
		t.Fatalf("after sub delete = %+v err %v", joined, err)
	}
	// 删除用户 → 关联级联清理。
	subB := insertTestExtSub(t, st, "机场B", "https://sub.example.com/b", 0, 0, 100)
	if err := st.SetUserExternalSubscriptions(ctx, userID, []UserExternalSubscription{
		{UserID: userID, SubscriptionID: subB, Mode: ExtSubModeMerge},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	joined, err = st.ListUserExternalSubscriptions(ctx, userID)
	if err != nil || len(joined) != 0 {
		t.Fatalf("after user delete = %+v err %v", joined, err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/store/ -run TestUserExternalSubscriptions -v`
Expected: 编译失败（`user_external_subscriptions.go` 未创建，类型/方法未定义）。

- [ ] **Step 3: 加表**

在 `src/backend/internal/store/store.go` 的 `Schema` 常量末尾（`idx_external_chains_subscription` 索引之后、结尾反引号之前）追加：

```sql

-- 用户引入外部订阅（叠加 stack / 并入 merge / 附加 nodes）；删除订阅或用户时级联清理。
CREATE TABLE IF NOT EXISTS user_external_subscriptions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subscription_id INTEGER NOT NULL REFERENCES external_subscriptions(id) ON DELETE CASCADE,
    mode            TEXT NOT NULL DEFAULT 'stack',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, subscription_id)
);
```

- [ ] **Step 4: 实现 store 方法**

创建 `src/backend/internal/store/user_external_subscriptions.go`：

```go
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// 用户引入外部订阅的模式。
const (
	ExtSubModeStack = "stack" // 叠加：独立配额池，额度与已用相加
	ExtSubModeMerge = "merge" // 并入：已用并入面板配额池，总额度不变
	ExtSubModeNodes = "nodes" // 附加：仅引入节点，不参与流量合并
)

// UserExternalSubscription 是用户与外部订阅的一对关联行。
type UserExternalSubscription struct {
	ID             int64
	UserID         int64
	SubscriptionID int64
	Mode           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UserExternalSubscriptionJoined 是关联行 + 外部订阅统计字段的 join 结果。
type UserExternalSubscriptionJoined struct {
	UserID         int64
	SubscriptionID int64
	Mode           string
	Name           string
	Upload         int64
	Download       int64
	Total          int64
	Expire         *int64
	NodeCount      int
}

// SetUserExternalSubscriptions 整表替换用户的关联外部订阅（含模式）。
func (s *Store) SetUserExternalSubscriptions(ctx context.Context, userID int64, items []UserExternalSubscription) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set user external subscriptions: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM user_external_subscriptions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("clear user external subscriptions: %w", err)
	}
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_external_subscriptions
			(user_id, subscription_id, mode) VALUES (?, ?, ?)
			ON CONFLICT(user_id, subscription_id)
			DO UPDATE SET mode = excluded.mode, updated_at = CURRENT_TIMESTAMP`,
			userID, item.SubscriptionID, item.Mode); err != nil {
			return fmt.Errorf("insert user external subscription: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit user external subscriptions: %w", err)
	}
	return nil
}

// ListUserExternalSubscriptions 返回用户关联的外部订阅（含流量统计 join）。
func (s *Store) ListUserExternalSubscriptions(ctx context.Context, userID int64) ([]UserExternalSubscriptionJoined, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ues.user_id, ues.subscription_id, ues.mode,
		es.name, es.upload, es.download, es.total, es.expire, es.node_count
		FROM user_external_subscriptions ues
		JOIN external_subscriptions es ON es.id = ues.subscription_id
		WHERE ues.user_id = ?
		ORDER BY ues.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user external subscriptions: %w", err)
	}
	defer rows.Close()
	var out []UserExternalSubscriptionJoined
	for rows.Next() {
		var joined UserExternalSubscriptionJoined
		var expire sql.NullInt64
		if err := rows.Scan(&joined.UserID, &joined.SubscriptionID, &joined.Mode,
			&joined.Name, &joined.Upload, &joined.Download, &joined.Total,
			&expire, &joined.NodeCount); err != nil {
			return nil, fmt.Errorf("scan user external subscription: %w", err)
		}
		if expire.Valid {
			joined.Expire = &expire.Int64
		}
		out = append(out, joined)
	}
	return out, rows.Err()
}

// UsersByExternalSubscriptionID 返回关联了指定外部订阅的用户 ID 列表。
func (s *Store) UsersByExternalSubscriptionID(ctx context.Context, subscriptionID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id FROM user_external_subscriptions
		WHERE subscription_id = ? ORDER BY user_id`, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("users by external subscription: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("scan user id: %w", err)
		}
		out = append(out, userID)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./src/backend/internal/store/ -run TestUserExternalSubscriptions -v`
Expected: 3 个测试 PASS。

- [ ] **Step 6: 提交**

```bash
git add src/backend/internal/store/store.go src/backend/internal/store/user_external_subscriptions.go src/backend/internal/store/user_external_subscriptions_test.go
git commit -m "feat(store): user external subscription link table"
```

---

### Task 2: extsub — 合并流量纯函数

**Files:**
- Create: `src/backend/internal/extsub/merge.go`
- Test: `src/backend/internal/extsub/merge_test.go`

**Interfaces:**
- Consumes: Task 1 `store.ExtSubModeStack/ExtSubModeMerge/ExtSubModeNodes`、`store.UserExternalSubscriptionJoined`。
- Produces（Task 6/7 使用）：
  ```go
  type Traffic struct {
      Upload   int64
      Download int64
      Total    int64
      Expire   *int64
  }
  func MergeUserTraffic(panel Traffic, attached []store.UserExternalSubscriptionJoined) Traffic
  ```

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/extsub/merge_test.go`：

```go
package extsub

import (
	"testing"

	"lattix/backend/internal/store"
)

func expiry(v int64) *int64 { return &v }

func TestMergeUserTraffic(t *testing.T) {
	cases := []struct {
		name     string
		panel    Traffic
		attached []store.UserExternalSubscriptionJoined
		want     Traffic
	}{
		{"无外部订阅", Traffic{Upload: 300, Download: 0, Total: 500, Expire: expiry(1)}, nil,
			Traffic{Upload: 300, Download: 0, Total: 500, Expire: expiry(1)}},
		{"叠加", Traffic{Upload: 300, Download: 0, Total: 500},
			[]store.UserExternalSubscriptionJoined{{Mode: store.ExtSubModeStack, Upload: 100, Download: 0, Total: 200, Expire: expiry(2)}},
			Traffic{Upload: 400, Download: 0, Total: 700, Expire: expiry(2)}},
		{"并入", Traffic{Upload: 300, Download: 0, Total: 500, Expire: expiry(1)},
			[]store.UserExternalSubscriptionJoined{{Mode: store.ExtSubModeMerge, Upload: 100, Download: 0, Total: 200, Expire: expiry(2)}},
			Traffic{Upload: 400, Download: 0, Total: 500, Expire: expiry(1)}},
		{"附加", Traffic{Upload: 300, Download: 0, Total: 500},
			[]store.UserExternalSubscriptionJoined{{Mode: store.ExtSubModeNodes, Upload: 100, Download: 0, Total: 200}},
			Traffic{Upload: 300, Download: 0, Total: 500}},
		{"未知额度不参与", Traffic{Upload: 300, Download: 0, Total: 500},
			[]store.UserExternalSubscriptionJoined{
				{Mode: store.ExtSubModeStack, Upload: 100, Download: 0, Total: 0},
				{Mode: store.ExtSubModeMerge, Upload: 50, Download: 0, Total: 0},
			},
			Traffic{Upload: 300, Download: 0, Total: 500}},
		{"混合", Traffic{Upload: 300, Download: 100, Total: 500, Expire: expiry(1)},
			[]store.UserExternalSubscriptionJoined{
				{Mode: store.ExtSubModeMerge, Upload: 100, Download: 0, Total: 200, Expire: expiry(2)},
				{Mode: store.ExtSubModeStack, Upload: 100, Download: 50, Total: 200, Expire: expiry(3)},
				{Mode: store.ExtSubModeNodes, Upload: 9, Download: 9, Total: 9},
			},
			Traffic{Upload: 500, Download: 150, Total: 700, Expire: expiry(1)}},
		{"叠加取最早到期", Traffic{Upload: 0, Download: 0, Total: 0, Expire: expiry(5)},
			[]store.UserExternalSubscriptionJoined{{Mode: store.ExtSubModeStack, Upload: 1, Download: 0, Total: 200, Expire: expiry(3)}},
			Traffic{Upload: 1, Download: 0, Total: 200, Expire: expiry(3)}},
	}
	for _, c := range cases {
		got := MergeUserTraffic(c.panel, c.attached)
		if got.Upload != c.want.Upload || got.Download != c.want.Download || got.Total != c.want.Total {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
		if (got.Expire == nil) != (c.want.Expire == nil) {
			t.Errorf("%s: expire nil mismatch, got %v want %v", c.name, got.Expire, c.want.Expire)
		}
		if got.Expire != nil && c.want.Expire != nil && *got.Expire != *c.want.Expire {
			t.Errorf("%s: expire = %d, want %d", c.name, *got.Expire, *c.want.Expire)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/extsub/ -run TestMergeUserTraffic -v`
Expected: 编译失败（`Traffic`/`MergeUserTraffic` 未定义）。

- [ ] **Step 3: 实现**

创建 `src/backend/internal/extsub/merge.go`：

```go
package extsub

import "lattix/backend/internal/store"

// Traffic 是合并后的流量统计（字节）。
type Traffic struct {
	Upload   int64
	Download int64
	Total    int64
	Expire   *int64 // Unix 秒
}

// MergeUserTraffic 按引入模式合并用户面板流量与外部订阅流量：
//   - stack：总额度与已用全部相加（独立配额池），到期取最早；
//   - merge：已用并入面板配额池，总额度不变（外部订阅 total 忽略）；
//   - nodes：不参与合并。
//
// total=0（未知额度）的订阅不参与任何合并计算（仅引入节点）。
func MergeUserTraffic(panel Traffic, attached []store.UserExternalSubscriptionJoined) Traffic {
	out := panel
	for _, sub := range attached {
		if sub.Total <= 0 {
			continue
		}
		switch sub.Mode {
		case store.ExtSubModeStack:
			out.Total += sub.Total
			out.Upload += sub.Upload
			out.Download += sub.Download
			if out.Expire == nil || (sub.Expire != nil && *sub.Expire < *out.Expire) {
				out.Expire = sub.Expire
			}
		case store.ExtSubModeMerge:
			out.Upload += sub.Upload
			out.Download += sub.Download
		}
	}
	return out
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/backend/internal/extsub/ -run TestMergeUserTraffic -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/extsub/merge.go src/backend/internal/extsub/merge_test.go
git commit -m "feat(extsub): merge user traffic with external subscriptions"
```

---

### Task 3: sub — 外部节点 mihomo 构建器

**Files:**
- Modify: `src/backend/internal/sub/sub.go:204-228`（`clashProxy` 增加可选字段与 `clashWsOpts`/`clashHTTPOpts` 结构）
- Create: `src/backend/internal/sub/external.go`（Extra 辅助 + `buildExternalClash`）
- Test: `src/backend/internal/sub/external_clash_test.go`

**Interfaces:**
- Consumes: `extsub.Node`（`Name/Type/Server/Port/Extra map[string]any`，见 `src/backend/internal/extsub/parse.go:22`）。
- Produces（Task 6 使用）：
  ```go
  func buildExternalClash(n extsub.Node) (clashProxy, error)
  func extStr(extra map[string]any, keys ...string) string
  func extBool(extra map[string]any, keys ...string) bool
  func extInt(extra map[string]any, keys ...string) int
  func externalNetwork(extra map[string]any, keys ...string) string
  ```

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/sub/external_clash_test.go`：

```go
package sub

import (
	"strings"
	"testing"

	"lattix/backend/internal/extsub"
)

func extNode(name, typ, server string, port int, extra map[string]any) extsub.Node {
	return extsub.Node{Name: name, Type: typ, Server: server, Port: port, Extra: extra}
}

func TestBuildExternalClash(t *testing.T) {
	vless := extNode("东京", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "tcp",
		"security": "reality", "pbk": "pub", "sid": "abcd", "fp": "chrome", "sni": "cdn.example.com",
	})
	p, err := buildExternalClash(vless)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "vless" || p.UUID != "11111111-2222-3333-4444-555555555555" ||
		!p.TLS || p.RealityOpts == nil || p.RealityOpts.PublicKey != "pub" ||
		p.RealityOpts.ShortID != "abcd" || p.ClientFingerprint != "chrome" ||
		p.Servername != "cdn.example.com" {
		t.Fatalf("vless = %+v", p)
	}

	hy2 := extNode("香港", "hysteria2", "hk.example.com", 443, map[string]any{
		"password": "p1", "obfs": "salamander", "obfs-password": "op", "sni": "hk.example.com",
	})
	p, err = buildExternalClash(hy2)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "hysteria2" || p.Password != "p1" || p.Obfs != "salamander" || p.ObfsPassword != "op" {
		t.Fatalf("hy2 = %+v", p)
	}

	wg := extNode("WG", "wireguard", "wg.example.com", 51820, map[string]any{
		"private_key": "pk", "ip": "10.0.0.2", "public_key": "peer", "mtu": "1420",
	})
	p, err = buildExternalClash(wg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "wireguard" || p.PrivateKey != "pk" || p.IP != "10.0.0.2" || p.PublicKey != "peer" || p.MTU != 1420 {
		t.Fatalf("wg = %+v", p)
	}

	if _, err := buildExternalClash(extNode("未知", "hysteria", "x", 1, nil)); err == nil {
		t.Fatal("unknown protocol unexpectedly accepted")
	}
	if _, err := buildExternalClash(extNode("缺凭据", "vless", "x", 1, nil)); err == nil {
		t.Fatal("missing credential unexpectedly accepted")
	}
}

func TestBuildExternalClashWS(t *testing.T) {
	p, err := buildExternalClash(extNode("ws", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "ws", "path": "/ws", "host": "h.example.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Network != "ws" || p.WsOpts == nil || p.WsOpts.Path != "/ws" || p.WsOpts.Headers["Host"] != "h.example.com" {
		t.Fatalf("ws = %+v", p)
	}
	if strings.Contains(p.Name, "h.example.com") {
		t.Fatalf("name must stay from config: %q", p.Name)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/sub/ -run TestBuildExternalClash -v`
Expected: 编译失败（`buildExternalClash` 未定义）。

- [ ] **Step 3: 扩展 clashProxy 结构**

在 `src/backend/internal/sub/sub.go` 的 `clashProxy` 结构（`XhttpOpts` 字段之后）追加字段：

```go
	Ports                string         `yaml:"ports,omitempty"`                  // hysteria2 多端口
	SkipCertVerify       bool           `yaml:"skip-cert-verify,omitempty"`
	Obfs                 string         `yaml:"obfs,omitempty"`                   // hysteria2 / snell
	ObfsPassword         string         `yaml:"obfs-password,omitempty"`
	Up                   string         `yaml:"up,omitempty"`                     // hysteria2
	Down                 string         `yaml:"down,omitempty"`                   // hysteria2
	Protocol             string         `yaml:"protocol,omitempty"`               // ssr
	ProtocolParam        string         `yaml:"protocol-param,omitempty"`
	ObfsParam            string         `yaml:"obfs-param,omitempty"`
	PSK                  string         `yaml:"psk,omitempty"`                    // snell
	Version              int            `yaml:"version,omitempty"`                // snell
	IP                   string         `yaml:"ip,omitempty"`                     // wireguard
	PrivateKey           string         `yaml:"private-key,omitempty"`
	PublicKey            string         `yaml:"public-key,omitempty"`
	PresharedKey         string         `yaml:"preshared-key,omitempty"`
	MTU                  int            `yaml:"mtu,omitempty"`
	CongestionController string         `yaml:"congestion-controller,omitempty"`  // tuic
	UDPRelayMode         string         `yaml:"udp-relay-mode,omitempty"`         // tuic
	ReduceRTT            bool           `yaml:"reduce-rtt,omitempty"`             // tuic
	WsOpts               *clashWsOpts   `yaml:"ws-opts,omitempty"`
	HTTPOpts             *clashHTTPOpts `yaml:"http-opts,omitempty"`
```

并在 `clashProxy` 结构定义之后追加两个结构：

```go
type clashWsOpts struct {
	Path    string            `yaml:"path,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

type clashHTTPOpts struct {
	Path    string            `yaml:"path,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}
```

- [ ] **Step 4: 实现构建器**

创建 `src/backend/internal/sub/external.go`：

```go
package sub

import (
	"fmt"
	"strconv"
	"strings"

	"lattix/backend/internal/extsub"
	"lattix/shared"
)

// extStr 按序取 Extra 中第一个存在的字符串值。
func extStr(extra map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := extra[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// extBool 判断 Extra 布尔值（1/true/yes/on）。
func extBool(extra map[string]any, keys ...string) bool {
	switch strings.ToLower(extStr(extra, keys...)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// extInt 取 Extra 整数值。
func extInt(extra map[string]any, keys ...string) int {
	for _, key := range keys {
		if v, ok := extra[key]; ok {
			switch t := v.(type) {
			case string:
				if n, err := strconv.Atoi(t); err == nil {
					return n
				}
			case float64:
				return int(t)
			}
		}
	}
	return 0
}

// externalNetwork 归一化外部节点传输层字段（tcp/ws/grpc/xhttp/http/h2）。
func externalNetwork(extra map[string]any, keys ...string) string {
	return strings.ToLower(extStr(extra, keys...))
}

// buildExternalClash 把外部订阅节点编译为 mihomo 代理项。
// 凭据取自 config（外部节点没有面板派发的用户 UUID）。
func buildExternalClash(n extsub.Node) (clashProxy, error) {
	if n.Name == "" || n.Server == "" || n.Port == 0 {
		return clashProxy{}, fmt.Errorf("外部节点「%s」缺少名称/地址/端口", n.Name)
	}
	e := n.Extra
	p := clashProxy{
		Name: n.Name, Server: n.Server, Port: n.Port, UDP: true,
		SkipCertVerify: extBool(e, "insecure", "allowInsecure", "allow_insecure"),
	}
	switch n.Type {
	case "vless":
		p.Type = "vless"
		p.UUID = extStr(e, "id")
		p.Network = externalNetwork(e, "type", "network")
		if p.Network == "" {
			p.Network = shared.NetworkTCP
		}
		p.Flow = extStr(e, "flow")
		p.Encryption = extStr(e, "encryption")
		switch extStr(e, "security") {
		case "reality":
			p.TLS = true
			p.RealityOpts = &clashRealityOpts{PublicKey: extStr(e, "pbk"), ShortID: extStr(e, "sid")}
			p.ClientFingerprint = extStr(e, "fp")
		case "tls":
			p.TLS = true
			p.ClientFingerprint = extStr(e, "fp")
		}
		p.Servername = extStr(e, "sni")
		applyExternalTransport(&p, e)
	case "vmess":
		zero := 0
		p.Type = "vmess"
		p.UUID = extStr(e, "id")
		p.AlterID = &zero
		p.Cipher = "auto"
		p.Network = externalNetwork(e, "net")
		if p.Network == "" {
			p.Network = shared.NetworkTCP
		}
		if extStr(e, "tls") == "tls" {
			p.TLS = true
			p.Servername = extStr(e, "sni")
		}
		applyExternalTransport(&p, e)
	case "trojan":
		p.Type = "trojan"
		p.Password = extStr(e, "password")
		p.TLS = true
		p.SNI = extStr(e, "sni")
		p.Network = externalNetwork(e, "type")
		if p.Network == "" {
			p.Network = shared.NetworkTCP
		}
		applyExternalTransport(&p, e)
	case "ss":
		p.Type = "ss"
		p.Cipher = extStr(e, "method")
		p.Password = extStr(e, "password")
	case "ssr":
		p.Type = "ssr"
		p.Cipher = extStr(e, "method")
		p.Password = extStr(e, "password")
		p.Protocol = extStr(e, "protocol")
		p.ProtocolParam = extStr(e, "protocol_param", "protocol-param")
		p.Obfs = extStr(e, "obfs")
		p.ObfsParam = extStr(e, "obfs_param", "obfs-param")
	case "hysteria2":
		p.Type = "hysteria2"
		p.Password = extStr(e, "password")
		p.Ports = extStr(e, "mport", "ports")
		p.Obfs = extStr(e, "obfs")
		p.ObfsPassword = extStr(e, "obfs-password", "obfs_password")
		p.Up = extStr(e, "up")
		p.Down = extStr(e, "down")
		p.Servername = extStr(e, "sni", "peername")
	case "tuic":
		p.Type = "tuic"
		p.UUID = extStr(e, "uuid")
		p.Password = extStr(e, "password")
		p.Servername = extStr(e, "sni")
		p.CongestionController = extStr(e, "congestion_controller", "congestion-controller")
		p.UDPRelayMode = extStr(e, "udp_relay_mode", "udp-relay-mode")
		p.ReduceRTT = extBool(e, "reduce_rtt", "reduce-rtt")
	case "wireguard":
		p.Type = "wireguard"
		p.IP = extStr(e, "ip", "address")
		p.PrivateKey = extStr(e, "private_key", "private-key")
		p.PublicKey = extStr(e, "public_key", "pk")
		p.PresharedKey = extStr(e, "preshared_key", "preshared-key", "psk")
		p.MTU = extInt(e, "mtu")
	case "anytls":
		p.Type = "anytls"
		p.Password = extStr(e, "password")
		p.Servername = extStr(e, "sni")
	case "snell":
		p.Type = "snell"
		p.PSK = extStr(e, "psk")
		p.Obfs = extStr(e, "obfs")
		p.Version = extInt(e, "version")
	case "socks":
		p.Type = "socks5"
		p.Username = extStr(e, "username")
		p.Password = extStr(e, "password")
	case "http":
		p.Type = "http"
		p.Username = extStr(e, "username")
		p.Password = extStr(e, "password")
		p.UDP = false
	default:
		return clashProxy{}, fmt.Errorf("外部节点「%s」未知协议 %s", n.Name, n.Type)
	}
	switch p.Type {
	case "vless", "vmess", "tuic":
		if p.UUID == "" {
			return clashProxy{}, fmt.Errorf("外部节点「%s」缺少凭据", n.Name)
		}
	case "trojan", "ss", "ssr", "hysteria2", "anytls", "snell":
		if p.Password == "" && p.PSK == "" {
			return clashProxy{}, fmt.Errorf("外部节点「%s」缺少凭据", n.Name)
		}
	case "wireguard":
		if p.PrivateKey == "" {
			return clashProxy{}, fmt.Errorf("外部节点「%s」缺少 private_key", n.Name)
		}
	}
	return p, nil
}

// applyExternalTransport 填充外部节点 ws/grpc/xhttp/http 传输层选项。
func applyExternalTransport(p *clashProxy, e map[string]any) {
	switch p.Network {
	case "ws":
		p.WsOpts = &clashWsOpts{Path: extStr(e, "path")}
		if host := extStr(e, "host"); host != "" {
			p.WsOpts.Headers = map[string]string{"Host": host}
		}
	case shared.NetworkGRPC:
		p.GrpcOpts = &clashGrpcOpts{ServiceName: extStr(e, "serviceName", "service_name")}
	case shared.NetworkXHTTP:
		p.XhttpOpts = &clashXHTTPOpts{
			Path: extStr(e, "path"), Mode: extStr(e, "mode"), Host: extStr(e, "host"),
		}
	case "http", "h2":
		p.HTTPOpts = &clashHTTPOpts{Path: extStr(e, "path")}
		if host := extStr(e, "host"); host != "" {
			p.HTTPOpts.Headers = map[string]string{"Host": host}
		}
	}
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./src/backend/internal/sub/ -run TestBuildExternalClash -v`
Expected: PASS（`shared`/`extsub` import 均无循环依赖：sub → extsub → store）。

- [ ] **Step 6: 提交**

```bash
git add src/backend/internal/sub/sub.go src/backend/internal/sub/external.go src/backend/internal/sub/external_clash_test.go
git commit -m "feat(sub): build mihomo proxies for external nodes"
```

---

### Task 4: sub — 外部节点 sing-box 构建器

**Files:**
- Create: `src/backend/internal/sub/external_singbox.go`
- Test: `src/backend/internal/sub/external_singbox_test.go`

**Interfaces:**
- Consumes: Task 3 的 `extStr/extBool/extInt/extBool/externalNetwork`。
- Produces（Task 6 使用）：
  ```go
  func buildExternalSingbox(n extsub.Node) (any, error)
  ```

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/sub/external_singbox_test.go`：

```go
package sub

import (
	"testing"

	"lattix/backend/internal/extsub"
)

func TestBuildExternalSingbox(t *testing.T) {
	vless := extNode("东京", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "ws", "path": "/ws",
		"security": "reality", "pbk": "pub", "sid": "abcd", "fp": "chrome", "sni": "cdn.example.com",
	})
	ob, err := buildExternalSingbox(vless)
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	if m["type"] != "vless" || m["uuid"] != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("vless = %+v", m)
	}
	tls := m["tls"].(map[string]any)
	reality := tls["reality"].(map[string]any)
	if reality["public_key"] != "pub" || reality["short_id"] != "abcd" {
		t.Fatalf("reality = %+v", reality)
	}
	tr := m["transport"].(map[string]any)
	if tr["type"] != "ws" || tr["path"] != "/ws" {
		t.Fatalf("transport = %+v", tr)
	}

	hy2 := extNode("香港", "hysteria2", "hk.example.com", 443, map[string]any{
		"password": "p1", "obfs": "salamander", "obfs-password": "op", "sni": "hk.example.com",
	})
	ob, err = buildExternalSingbox(hy2)
	if err != nil {
		t.Fatal(err)
	}
	m = ob.(map[string]any)
	if m["password"] != "p1" {
		t.Fatalf("hy2 = %+v", m)
	}
	if obfs := m["obfs"].(map[string]any); obfs["password"] != "op" {
		t.Fatalf("hy2 obfs = %+v", obfs)
	}

	ssr := extNode("SSR", "ssr", "1.2.3.4", 8388, map[string]any{"protocol": "auth_sha1_v4"})
	if _, err := buildExternalSingbox(ssr); err == nil {
		t.Fatal("ssr should be unsupported in sing-box")
	}
}

func TestBuildExternalSingboxTrojanTLS(t *testing.T) {
	ob, err := buildExternalSingbox(extNode("tr", "trojan", "1.2.3.4", 443, map[string]any{
		"password": "pw", "sni": "t.example.com", "allowInsecure": "1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := ob.(map[string]any)
	tls := m["tls"].(map[string]any)
	if tls["server_name"] != "t.example.com" || tls["insecure"] != true {
		t.Fatalf("tls = %+v", tls)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/sub/ -run TestBuildExternalSingbox -v`
Expected: 编译失败（`buildExternalSingbox` 未定义）。

- [ ] **Step 3: 实现**

创建 `src/backend/internal/sub/external_singbox.go`：

```go
package sub

import (
	"fmt"
	"strconv"
	"strings"

	"lattix/backend/internal/extsub"
)

// buildExternalSingbox 把外部订阅节点编译为 sing-box 出站（map JSON）。
// sing-box 不原生支持 ssr/snell，返回错误由调用方记 warning 跳过。
func buildExternalSingbox(n extsub.Node) (any, error) {
	if n.Name == "" || n.Server == "" || n.Port == 0 {
		return nil, fmt.Errorf("外部节点「%s」缺少名称/地址/端口", n.Name)
	}
	e := n.Extra
	base := map[string]any{"type": n.Type, "tag": n.Name, "server": n.Server, "server_port": n.Port}
	switch n.Type {
	case "vless":
		base["uuid"] = extStr(e, "id")
		base["packet_encoding"] = "xudp"
		if flow := extStr(e, "flow"); flow != "" {
			base["flow"] = flow
		}
		if tls := externalSingboxTLS(e, extStr(e, "security") == "reality"); tls != nil {
			base["tls"] = tls
		}
		if tr := externalSingboxTransport(e, externalNetwork(e, "type")); tr != nil {
			base["transport"] = tr
		}
	case "vmess":
		base["uuid"] = extStr(e, "id")
		base["alter_id"] = extInt(e, "aid")
		base["security"] = extStr(e, "scy", "auto")
		if extStr(e, "tls") == "tls" {
			base["tls"] = externalSingboxTLSSimple(e)
		}
		if tr := externalSingboxTransport(e, externalNetwork(e, "net")); tr != nil {
			base["transport"] = tr
		}
	case "trojan":
		base["password"] = extStr(e, "password")
		base["tls"] = externalSingboxTLSSimple(e)
		if tr := externalSingboxTransport(e, externalNetwork(e, "type")); tr != nil {
			base["transport"] = tr
		}
	case "ss":
		base["method"] = extStr(e, "method")
		base["password"] = extStr(e, "password")
	case "hysteria2":
		base["password"] = extStr(e, "password")
		if extStr(e, "obfs") != "" {
			base["obfs"] = map[string]any{
				"type": "salamander", "password": extStr(e, "obfs-password", "obfs_password"),
			}
		}
		base["tls"] = map[string]any{
			"enabled": true, "server_name": extStr(e, "sni", "peername"),
			"insecure": extBool(e, "insecure"),
		}
	case "tuic":
		base["uuid"] = extStr(e, "uuid")
		if pwd := extStr(e, "password"); pwd != "" {
			base["password"] = pwd
		}
		if cc := extStr(e, "congestion_controller", "congestion-controller"); cc != "" {
			base["congestion_control"] = cc
		}
		if udp := extStr(e, "udp_relay_mode", "udp-relay-mode"); udp != "" {
			base["udp_relay_mode"] = udp
		}
		base["zero_rtt_handshake"] = extBool(e, "reduce_rtt", "reduce-rtt")
		base["tls"] = map[string]any{
			"enabled": true, "server_name": extStr(e, "sni"),
			"insecure": extBool(e, "allow_insecure"),
		}
	case "wireguard":
		base["local_address"] = []string{extStr(e, "ip", "address")}
		base["private_key"] = extStr(e, "private_key")
		if pk := extStr(e, "public_key", "pk"); pk != "" {
			base["peer_public_key"] = pk
		}
		if psk := extStr(e, "preshared_key", "preshared-key", "psk"); psk != "" {
			base["preshared_key"] = psk
		}
		if mtu := extInt(e, "mtu"); mtu > 0 {
			base["mtu"] = mtu
		}
		if reserved := extStr(e, "reserved"); reserved != "" {
			var values []int
			for _, part := range strings.Split(reserved, ",") {
				if v, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
					values = append(values, v)
				}
			}
			if len(values) > 0 {
				base["reserved"] = values
			}
		}
	case "socks", "http":
		base["username"] = extStr(e, "username")
		if pwd := extStr(e, "password"); pwd != "" {
			base["password"] = pwd
		}
	case "anytls":
		base["password"] = extStr(e, "password")
		base["tls"] = externalSingboxTLSSimple(e)
	default:
		return nil, fmt.Errorf("外部节点「%s」sing-box 不支持协议 %s", n.Name, n.Type)
	}
	return base, nil
}

// externalSingboxTLS 构造 vless 的 TLS 对象（reality 时带 reality/utls）。
func externalSingboxTLS(e map[string]any, reality bool) map[string]any {
	tls := externalSingboxTLSSimple(e)
	if reality {
		tls["reality"] = map[string]any{
			"enabled": true, "public_key": extStr(e, "pbk"), "short_id": extStr(e, "sid"),
		}
		if fp := extStr(e, "fp"); fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
	}
	return tls
}

// externalSingboxTLSSimple 构造普通 TLS 对象。
func externalSingboxTLSSimple(e map[string]any) map[string]any {
	return map[string]any{
		"enabled": true, "server_name": extStr(e, "sni"),
		"insecure": extBool(e, "insecure", "allowInsecure", "allow_insecure"),
	}
}

// externalSingboxTransport 构造传输层对象；不支持/缺省返回 nil。
func externalSingboxTransport(e map[string]any, network string) map[string]any {
	switch network {
	case "ws":
		tr := map[string]any{"type": "ws", "path": extStr(e, "path")}
		if host := extStr(e, "host"); host != "" {
			tr["headers"] = map[string]any{"Host": host}
		}
		return tr
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": extStr(e, "serviceName", "service_name")}
	case "xhttp":
		tr := map[string]any{"type": "xhttp", "path": extStr(e, "path")}
		if mode := extStr(e, "mode"); mode != "" {
			tr["mode"] = mode
		}
		return tr
	case "http", "h2":
		tr := map[string]any{"type": "http", "path": []string{extStr(e, "path")}}
		if host := extStr(e, "host"); host != "" {
			tr["host"] = []string{host}
		}
		return tr
	}
	return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/backend/internal/sub/ -run TestBuildExternalSingbox -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/sub/external_singbox.go src/backend/internal/sub/external_singbox_test.go
git commit -m "feat(sub): build sing-box outbounds for external nodes"
```

---

### Task 5: sub — 外部节点 quanx 行与分享链接构建器

**Files:**
- Create: `src/backend/internal/sub/external_quanx.go`
- Create: `src/backend/internal/sub/external_links.go`
- Test: `src/backend/internal/sub/external_links_test.go`

**Interfaces:**
- Consumes: Task 3 的 Extra 辅助函数。
- Produces（Task 6 使用）：
  ```go
  func buildExternalQuanX(n extsub.Node) string
  func buildExternalLink(n extsub.Node) (string, bool)
  ```

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/sub/external_links_test.go`：

```go
package sub

import (
	"strings"
	"testing"

	"lattix/backend/internal/extsub"
)

func TestBuildExternalQuanX(t *testing.T) {
	if got := buildExternalQuanX(extNode("reality", "vless", "1.2.3.4", 443, map[string]any{
		"id": "x", "security": "reality", "pbk": "p", "sid": "s",
	})); got != "" {
		t.Fatalf("reality must be skipped: %q", got)
	}
	got := buildExternalQuanX(extNode("ss", "ss", "1.2.3.4", 8388, map[string]any{
		"method": "aes-128-gcm", "password": "pw",
	}))
	if !strings.Contains(got, "shadowsocks=1.2.3.4:8388") || !strings.Contains(got, "method=aes-128-gcm") || !strings.Contains(got, "tag=ss") {
		t.Fatalf("ss quanx = %q", got)
	}
	if got := buildExternalQuanX(extNode("wg", "wireguard", "1.2.3.4", 51820, map[string]any{"private_key": "k"})); got != "" {
		t.Fatalf("wireguard should be skipped: %q", got)
	}
}

func TestBuildExternalLink(t *testing.T) {
	link, ok := buildExternalLink(extNode("东京 01", "vless", "1.2.3.4", 443, map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "type": "tcp",
		"security": "reality", "pbk": "pub", "sid": "abcd", "fp": "chrome",
	}))
	if !ok {
		t.Fatal("vless link failed")
	}
	if !strings.HasPrefix(link, "vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?") ||
		!strings.Contains(link, "pbk=pub") || !strings.Contains(link, "type=tcp") ||
		!strings.Contains(link, "#%E4%B8%9C%E4%BA%AC") {
		t.Fatalf("vless link = %q", link)
	}
	// 回环：序列化 → 重新解析 → 关键字段一致。
	nodes, _, err := extsub.ParseSubscription([]byte(link))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("reparse = %+v err %v", nodes, err)
	}
	back := nodes[0]
	if back.Type != "vless" || back.Server != "1.2.3.4" || back.Port != 443 ||
		back.Extra["id"] != "11111111-2222-3333-4444-555555555555" || back.Extra["pbk"] != "pub" {
		t.Fatalf("round trip = %+v", back)
	}

	ssLink, ok := buildExternalLink(extNode("ss-01", "ss", "5.6.7.8", 8388, map[string]any{
		"method": "aes-128-gcm", "password": "pass",
	}))
	if !ok {
		t.Fatal("ss link failed")
	}
	if !strings.HasPrefix(ssLink, "ss://") {
		t.Fatalf("ss link = %q", ssLink)
	}
	if _, _, err := extsub.ParseSubscription([]byte(ssLink)); err != nil {
		t.Fatalf("ss reparse err = %v", err)
	}

	if _, ok := buildExternalLink(extNode("x", "unknown", "1.2.3.4", 1, nil)); ok {
		t.Fatal("unknown protocol unexpectedly serialized")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/sub/ -run 'TestBuildExternalQuanX|TestBuildExternalLink' -v`
Expected: 编译失败（函数未定义）。

- [ ] **Step 3: 实现 quanx 构建器**

创建 `src/backend/internal/sub/external_quanx.go`：

```go
package sub

import (
	"fmt"
	"strings"

	"lattix/backend/internal/extsub"
)

// buildExternalQuanX 把外部订阅节点编译为 Quantumult X [server_local] 行；
// 客户端无法表达的协议返回空串（调用方跳过）。
func buildExternalQuanX(n extsub.Node) string {
	e := n.Extra
	server := fmt.Sprintf("%s:%d", n.Server, n.Port)
	add := func(fields []string, key, value string) []string {
		if value != "" {
			return append(fields, key+"="+value)
		}
		return fields
	}
	var fields []string
	switch n.Type {
	case "vless":
		if extStr(e, "security") == "reality" {
			return "" // quanx 不支持 reality
		}
		fields = append(fields, "vless="+server)
		fields = add(fields, "method", "chacha20-poly1305")
		fields = add(fields, "password", extStr(e, "id"))
		if externalNetwork(e, "type") == "ws" {
			fields = add(fields, "obfs", "wss")
			fields = add(fields, "obfs-host", extStr(e, "host", "sni"))
			fields = add(fields, "obfs-uri", extStr(e, "path"))
		} else if extStr(e, "security") == "tls" {
			fields = add(fields, "obfs", "over-tls")
			fields = add(fields, "obfs-host", extStr(e, "sni"))
		}
	case "vmess":
		fields = append(fields, "vmess="+server)
		fields = add(fields, "method", "chacha20-ietf-poly1305")
		fields = add(fields, "password", extStr(e, "id"))
		net := externalNetwork(e, "net")
		switch {
		case net == "ws" && extStr(e, "tls") == "tls":
			fields = add(fields, "obfs", "wss")
		case net == "ws":
			fields = add(fields, "obfs", "ws")
		case extStr(e, "tls") == "tls":
			fields = add(fields, "obfs", "over-tls")
		}
		fields = add(fields, "obfs-host", extStr(e, "host", "sni"))
		fields = add(fields, "obfs-uri", extStr(e, "path"))
	case "trojan":
		fields = append(fields, "trojan="+server)
		fields = add(fields, "password", extStr(e, "password"))
		if externalNetwork(e, "type") == "ws" {
			fields = add(fields, "obfs", "wss")
			fields = add(fields, "obfs-host", extStr(e, "host", "sni"))
			fields = add(fields, "obfs-uri", extStr(e, "path"))
		}
	case "ss":
		fields = append(fields, "shadowsocks="+server)
		fields = add(fields, "method", extStr(e, "method"))
		fields = add(fields, "password", extStr(e, "password"))
	case "hysteria2":
		fields = append(fields, "hysteria2="+server)
		fields = add(fields, "password", extStr(e, "password"))
		fields = add(fields, "obfs", extStr(e, "obfs"))
		fields = add(fields, "obfs-password", extStr(e, "obfs-password", "obfs_password"))
		fields = add(fields, "sni", extStr(e, "sni"))
	case "tuic":
		fields = append(fields, "tuic="+server)
		fields = add(fields, "uuid", extStr(e, "uuid"))
		fields = add(fields, "password", extStr(e, "password"))
		fields = add(fields, "congestion-controller", extStr(e, "congestion_controller", "congestion-controller"))
		fields = add(fields, "udp-relay-mode", extStr(e, "udp_relay_mode", "udp-relay-mode"))
		fields = add(fields, "sni", extStr(e, "sni"))
	case "socks":
		fields = append(fields, "socks5="+server)
		fields = add(fields, "username", extStr(e, "username"))
		fields = add(fields, "password", extStr(e, "password"))
	case "http":
		fields = append(fields, "http="+server)
		fields = add(fields, "username", extStr(e, "username"))
		fields = add(fields, "password", extStr(e, "password"))
	default:
		return ""
	}
	fields = add(fields, "tag", n.Name)
	return strings.Join(fields, ", ")
}
```

- [ ] **Step 4: 实现分享链接构建器**

创建 `src/backend/internal/sub/external_links.go`：

```go
package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"lattix/backend/internal/extsub"
)

// buildExternalLink 把外部订阅节点序列化为分享链接（与 extsub 解析器互逆）；
// 不支持的协议返回 ok=false。
func buildExternalLink(n extsub.Node) (string, bool) {
	e := n.Extra
	name := url.QueryEscape(n.Name)
	hostPort := fmt.Sprintf("%s:%d", n.Server, n.Port)
	switch n.Type {
	case "vless":
		return "vless://" + extStr(e, "id") + "@" + hostPort + "?" + externalQuery(e, "id") + "#" + name, true
	case "trojan":
		return "trojan://" + extStr(e, "password") + "@" + hostPort + "?" + externalQuery(e, "password") + "#" + name, true
	case "hysteria2":
		return "hysteria2://" + extStr(e, "password") + "@" + hostPort + "?" + externalQuery(e, "password") + "#" + name, true
	case "tuic":
		return "tuic://" + extStr(e, "uuid") + ":" + extStr(e, "password") + "@" + hostPort + "?" + externalQuery(e, "uuid", "password") + "#" + name, true
	case "anytls":
		return "anytls://" + extStr(e, "password") + "@" + hostPort + "?" + externalQuery(e, "password") + "#" + name, true
	case "snell":
		return "snell://" + extStr(e, "psk") + "@" + hostPort + "?" + externalQuery(e, "psk") + "#" + name, true
	case "socks":
		cred := extStr(e, "username")
		if pwd := extStr(e, "password"); pwd != "" {
			cred += ":" + pwd
		}
		return "socks://" + cred + "@" + hostPort + "#" + name, true
	case "http":
		cred := extStr(e, "username")
		if pwd := extStr(e, "password"); pwd != "" {
			cred += ":" + pwd
		}
		return "http://" + cred + "@" + hostPort + "#" + name, true
	case "vmess":
		payload := map[string]any{
			"v": "2", "ps": n.Name, "add": n.Server, "port": strconv.Itoa(n.Port),
		}
		for key, value := range e {
			payload[key] = value
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", false
		}
		return "vmess://" + base64.RawURLEncoding.EncodeToString(raw), true
	case "ss":
		cred := base64.StdEncoding.EncodeToString([]byte(extStr(e, "method") + ":" + extStr(e, "password")))
		query := externalQuery(e, "method", "password")
		if query != "" {
			return "ss://" + cred + "@" + hostPort + "?" + query + "#" + name, true
		}
		return "ss://" + cred + "@" + hostPort + "#" + name, true
	case "ssr":
		payload := fmt.Sprintf("%s:%d:%s:%s:%s:%s", n.Server, n.Port,
			extStr(e, "protocol"), extStr(e, "method"), extStr(e, "obfs"),
			base64.StdEncoding.EncodeToString([]byte(extStr(e, "password"))))
		query := externalQuery(e, "protocol", "method", "obfs", "password", "remarks")
		remarks := "remarks=" + url.QueryEscape(base64.StdEncoding.EncodeToString([]byte(n.Name)))
		if query != "" {
			query += "&" + remarks
		} else {
			query = remarks
		}
		return "ssr://" + base64.StdEncoding.EncodeToString([]byte(payload)) + "?" + query, true
	case "wireguard":
		query := externalQuery(e, "private_key", "endpoint", "pk", "public_key")
		params := []string{
			"endpoint=" + url.QueryEscape(hostPort),
			"private_key=" + url.QueryEscape(extStr(e, "private_key")),
		}
		if query != "" {
			params = append(params, query)
		}
		return "wireguard://?" + strings.Join(params, "&") + "#" + name, true
	}
	return "", false
}

// externalQuery 把 Extra 剩余键值重建为 URL query（排序保证确定性）。
func externalQuery(extra map[string]any, skip ...string) string {
	skipped := map[string]bool{}
	for _, key := range skip {
		skipped[key] = true
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		if !skipped[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(extStr(extra, key)))
	}
	return strings.Join(parts, "&")
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./src/backend/internal/sub/ -run 'TestBuildExternalQuanX|TestBuildExternalLink' -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add src/backend/internal/sub/external_quanx.go src/backend/internal/sub/external_links.go src/backend/internal/sub/external_links_test.go
git commit -m "feat(sub): build quanx lines and share links for external nodes"
```

---

### Task 6: sub — 外部订阅并入用户订阅管线

**Files:**
- Modify: `src/backend/internal/sub/sub.go:67-99`（`setSubHeaders` 合并流量）、`sub.go:505-511`（`proxyItem` 加 `external` 字段）、`sub.go:519-583`（`subscriptionItems` 追加外部条目）
- Modify: `src/backend/internal/sub/publisher.go:212-218`（`compiledNode.Singbox` 改为 `any`）、`publisher.go:220-248`（`compileNodes` 分支）、`publisher.go:456-469`（`renderLinks` 分支）
- Modify: `src/backend/internal/sub/api.go:37-90`（`HandleSubInfo` 合并）
- Test: `src/backend/internal/sub/external_test.go`

**Interfaces:**
- Consumes: Task 1 store 方法、Task 2 `extsub.Traffic`/`MergeUserTraffic`、Task 3/4/5 构建器。
- Produces（Task 7 前端 DTO 独立实现，不依赖本任务产物；本任务产物供前端页面行为验证）。

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/sub/external_test.go`：

```go
package sub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
)

// newExternalFixture 建一个带外部订阅关联的用户：
// 面板配额 500/已用 300（traffic 表 node_id=0），外部订阅 叠加 total=200 up=100 down=50。
func newExternalFixture(t *testing.T) (*store.Store, *Server, *store.User) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	userID, err := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "alice-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSubSettings(ctx, userID, 500, 0, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	subID, err := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x",
		Upload: 100, Download: 50, Total: 200, AutoUpdate: true, UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := json.Marshal(extsub.Node{
		Name: "东京 01", Type: "vless", Server: "1.2.3.4", Port: 443,
		Extra: map[string]any{
			"id": "11111111-2222-3333-4444-555555555555", "type": "tcp",
			"security": "reality", "pbk": "pub", "sid": "abcd",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceExternalChains(ctx, subID, []store.ExternalChain{{
		SubscriptionID: subID, Name: "东京 01", Protocol: "vless",
		Server: "1.2.3.4", Port: 443, Config: cfg, ConfigSHA256: "sha-1",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalSubscriptions(ctx, userID, []store.UserExternalSubscription{
		{UserID: userID, SubscriptionID: subID, Mode: store.ExtSubModeStack},
	}); err != nil {
		t.Fatal(err)
	}
	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)
	return st, server, user
}

func TestSubscriptionItemsIncludesExternalChains(t *testing.T) {
	st, server, user := newExternalFixture(t)
	items, warnings := server.subscriptionItems(httptest.NewRequest("GET", "/sub/x", nil), user, nil)
	if len(items) != 1 || items[0].external == nil {
		t.Fatalf("items = %+v warnings = %v", items, warnings)
	}
	if items[0].external.Name != "东京 01" || items[0].external.Type != "vless" {
		t.Fatalf("external = %+v", items[0].external)
	}

	// 过期/禁用用户不出外部节点。
	user.Expired = true
	items, _ = server.subscriptionItems(httptest.NewRequest("GET", "/sub/x", nil), user, nil)
	if len(items) != 0 {
		t.Fatalf("expired user items = %+v", items)
	}
	_ = st
}

func TestSetSubHeadersMergesTraffic(t *testing.T) {
	_, server, user := newExternalFixture(t)
	rec := httptest.NewRecorder()
	server.setSubHeaders(rec, httptest.NewRequest("GET", "/sub/alice-token", nil), user)
	got := rec.Header().Get("Subscription-Userinfo")
	if !strings.Contains(got, "upload=100; download=50; total=700") {
		t.Fatalf("userinfo = %q", got)
	}
	if !strings.Contains(got, "reset_day=") {
		t.Fatalf("userinfo missing reset_day: %q", got)
	}
}

func TestCompileNodesAndRenderIncludeExternal(t *testing.T) {
	ctx := context.Background()
	_, server, user := newExternalFixture(t)
	items, _ := server.subscriptionItems(httptest.NewRequest("GET", "/sub/x", nil), user, nil)
	compiled, warnings, err := server.compileNodes(ctx, items, user.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 1 {
		t.Fatalf("compiled = %+v warnings = %v", compiled, warnings)
	}
	if compiled[0].Clash.Type != "vless" || compiled[0].Clash.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("clash = %+v", compiled[0].Clash)
	}
	sb, ok := compiled[0].Singbox.(map[string]any)
	if !ok || sb["type"] != "vless" {
		t.Fatalf("singbox = %+v", compiled[0].Singbox)
	}

	links, err := renderLinks(items, user.UUID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(links)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), "vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443") {
		t.Fatalf("links = %s", decoded)
	}
}

func TestHandleSubInfoMergesTraffic(t *testing.T) {
	_, server, user := newExternalFixture(t)
	rec := httptest.NewRecorder()
	server.HandleSubInfo(rec, httptest.NewRequest("GET", "/api/sub/alice-token/info", nil).WithPathValue("token", "alice-token"))
	var resp SubInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.UsedUp != 100 || resp.UsedDown != 50 || resp.TrafficLimit != 700 {
		t.Fatalf("resp = %+v", resp)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/sub/ -run 'TestSubscriptionItemsIncludesExternalChains|TestSetSubHeadersMergesTraffic|TestCompileNodesAndRenderIncludeExternal|TestHandleSubInfoMergesTraffic' -v`
Expected: 编译失败（`proxyItem.external`、`extsub` import 等未就绪）。

- [ ] **Step 3: sub.go — proxyItem 与订阅条目**

在 `src/backend/internal/sub/sub.go` 的 import 中追加 `"lattix/backend/internal/extsub"`。

`proxyItem`（sub.go:507-511）改为：

```go
// proxyItem 是一个订阅条目的来源：节点行 + 生效值
// （链条目已把别名/地址/端口替换为入口侧，§21；其余字段取出口 realized_config）。
// external 非空时表示该条目来自外部订阅节点（凭据取自 config，不派生用户 UUID）。
type proxyItem struct {
	node       store.Node
	rc         shared.RealizedConfig
	credential string
	external   *extsub.Node
}
```

在 `subscriptionItems`（sub.go:519-583）的 chain 循环之后、`return items, warnings` 之前追加：

```go
	attached, err := s.st.ListUserExternalSubscriptions(r.Context(), user.ID)
	if err != nil {
		return items, warnings
	}
	for _, sub := range attached {
		chains, err := s.st.ListExternalChains(r.Context(), sub.SubscriptionID)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("外部订阅「%s」节点读取失败：%v", sub.Name, err))
			continue
		}
		for _, chain := range chains {
			var ext extsub.Node
			if err := json.Unmarshal(chain.Config, &ext); err != nil {
				continue
			}
			items = append(items, proxyItem{external: &ext})
		}
	}
```

- [ ] **Step 4: sub.go — setSubHeaders 合并流量**

把 `setSubHeaders`（sub.go:67-99）开头的流量取值段替换为：

```go
	t, err := s.st.UserTraffic(r.Context(), user.UUID)
	if err != nil {
		t = store.TrafficTotals{} // 统计查询失败不阻断订阅
	}
	merged := s.mergedUserTraffic(r.Context(), user, t)
	v := fmt.Sprintf("upload=%d; download=%d", merged.Upload, merged.Download)
	if merged.Total > 0 {
		v += fmt.Sprintf("; total=%d", merged.Total)
		v += fmt.Sprintf("; reset_day=%d", daysUntilReset(user, time.Now()))
	}
	if merged.Expire != nil {
		v += fmt.Sprintf("; expire=%d", *merged.Expire)
	}
```

并在 `setSubHeaders` 之后追加：

```go
// mergedUserTraffic 合并面板实时流量与用户引入的外部订阅流量（叠加/并入）。
func (s *Server) mergedUserTraffic(ctx context.Context, user *store.User, t store.TrafficTotals) extsub.Traffic {
	var panelExpire *int64
	if user.ExpiresAt != nil {
		v := user.ExpiresAt.Unix()
		panelExpire = &v
	}
	attached, err := s.st.ListUserExternalSubscriptions(ctx, user.ID)
	if err != nil {
		attached = nil
	}
	return extsub.MergeUserTraffic(extsub.Traffic{
		Upload: t.Up, Download: t.Down, Total: user.TrafficLimit, Expire: panelExpire,
	}, attached)
}
```

- [ ] **Step 5: publisher.go — compileNodes 与 renderLinks 分支**

`compiledNode`（publisher.go:212-218）的 `Singbox` 字段类型改为：

```go
type compiledNode struct {
	Name        string
	CountryCode string
	Clash       clashProxy
	Singbox     any // 面板节点为 sbOutbound；外部节点为 map[string]any
	QuanX       string
}
```

`compileNodes`（publisher.go:220-248）的循环开头追加分支：

```go
	for _, item := range items {
		if item.external != nil {
			clash, err := buildExternalClash(*item.external)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			singbox, err := buildExternalSingbox(*item.external)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			out = append(out, compiledNode{
				Name: clash.Name, Clash: clash, Singbox: singbox,
				QuanX: buildExternalQuanX(*item.external),
			})
			continue
		}
		credential := item.credential
```

`renderLinks`（publisher.go:456-469）的循环开头追加分支：

```go
	for _, item := range items {
		if item.external != nil {
			if link, ok := buildExternalLink(*item.external); ok {
				links = append(links, link)
			}
			continue
		}
		credential := item.credential
```

- [ ] **Step 6: api.go — HandleSubInfo 合并**

`HandleSubInfo`（api.go:49-89）中替换流量取值与到期：

```go
	t, _ := s.st.UserTraffic(r.Context(), user.UUID)
	assigned, _ := s.st.UserNodeIDs(r.Context(), user.ID)
	var panelExpire *int64
	if user.ExpiresAt != nil {
		v := user.ExpiresAt.Unix()
		panelExpire = &v
	}
	attached, _ := s.st.ListUserExternalSubscriptions(r.Context(), user.ID)
	merged := extsub.MergeUserTraffic(extsub.Traffic{
		Upload: t.Up, Download: t.Down, Total: user.TrafficLimit, Expire: panelExpire,
	}, attached)
```

并把响应构造改为：

```go
	resp := SubInfoResponse{
		Name:           user.Name,
		Expired:        user.Expired,
		Disabled:       user.Disabled,
		UsedUp:         merged.Upload,
		UsedDown:       merged.Download,
		TrafficLimit:   merged.Total,
		NodesCount:     len(assigned),
		Title:          title,
		Announcement:   announcement,
		UpdateInterval: interval,
	}
	if merged.Expire != nil {
		v := *merged.Expire
		resp.ExpiresAt = &v
	}
```

并在 `api.go` 的 import 中追加 `"lattix/backend/internal/extsub"`。

- [ ] **Step 7: 运行确认通过**

Run: `go test ./src/backend/internal/sub/ -v`
Expected: PASS（含新增 4 个测试与既有测试；`renderSingbox` 因 `Singbox any` 变化仍可编译）。

- [ ] **Step 8: 提交**

```bash
git add src/backend/internal/sub/
git commit -m "feat(sub): include external subscriptions in user subscription"
```

---

### Task 7: panel — 分配 RPC、DTO 与 openapi

**Files:**
- Modify: `src/backend/internal/panel/users.go`（userDTO/`toUserDTO`、新 handler、`handleCreateUser` 支持）
- Modify: `src/backend/internal/panel/panel.go:315-338`（注册路由）
- Modify: `docs/openapi.yaml`（新增路径）
- Test: `src/backend/internal/panel/user_external_subscriptions_test.go`

**Interfaces:**
- Consumes: Task 1 store 方法、Task 2 `extsub.Traffic`/`MergeUserTraffic`。
- Produces（Task 9 前端使用）：
  - `POST /api/user/set-external-subscriptions` 请求 `{user_id, items: [{subscription_id, mode}]}`，响应 `{code:"OK", data:{items}}`
  - `userDTO` 新增 `external_subscriptions: [{subscription_id, name, mode, upload, download, total, expire?, remaining, node_count}]` 与 `merged_traffic: {upload, download, total, expire?}`
  - `POST /api/user/create` 请求支持可选 `external_subscriptions`

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/panel/user_external_subscriptions_test.go`：

```go
package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
)

func TestSetUserExternalSubscriptionsRPC(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "alice-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	subID, err := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x",
		Upload: 100, Download: 50, Total: 200, AutoUpdate: true, UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st}

	rec := httptest.NewRecorder()
	server.handleSetUserExternalSubscriptions(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/set-external-subscriptions",
		strings.NewReader(`{"user_id":`+itoa(userID)+`,"items":[{"subscription_id":`+itoa(subID)+`,"mode":"stack"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Code string `json:"code"`
		Data struct {
			Items []struct {
				SubscriptionID int64  `json:"subscription_id"`
				Mode           string `json:"mode"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Code != "OK" || len(got.Data.Items) != 1 || got.Data.Items[0].Mode != "stack" {
		t.Fatalf("got = %+v", got)
	}
	joined, err := st.ListUserExternalSubscriptions(ctx, userID)
	if err != nil || len(joined) != 1 || joined[0].Mode != store.ExtSubModeStack {
		t.Fatalf("joined = %+v err %v", joined, err)
	}

	// 非法 mode 与不存在的订阅被拒绝。
	rec = httptest.NewRecorder()
	server.handleSetUserExternalSubscriptions(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/set-external-subscriptions",
		strings.NewReader(`{"user_id":`+itoa(userID)+`,"items":[{"subscription_id":`+itoa(subID)+`,"mode":"bogus"}]}`)))
	var bad struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&bad); err != nil {
		t.Fatal(err)
	}
	if bad.Code != "INVALID_ARGUMENT" {
		t.Fatalf("bogus mode code = %q", bad.Code)
	}

	rec = httptest.NewRecorder()
	server.handleSetUserExternalSubscriptions(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/set-external-subscriptions",
		strings.NewReader(`{"user_id":`+itoa(userID)+`,"items":[{"subscription_id":999,"mode":"nodes"}]}`)))
	bad = struct {
		Code string `json:"code"`
	}{}
	if err := json.NewDecoder(rec.Body).Decode(&bad); err != nil {
		t.Fatal(err)
	}
	if bad.Code != "NOT_FOUND" {
		t.Fatalf("missing sub code = %q", bad.Code)
	}
}

func TestUserDTOIncludesExternalSubscriptions(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "alice", "00000000-0000-0000-0000-0000000000aa", "alice-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSubSettings(ctx, userID, 500, 0, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	subID, err := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x",
		Upload: 100, Download: 50, Total: 200, AutoUpdate: true, UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalSubscriptions(ctx, userID, []store.UserExternalSubscription{
		{UserID: userID, SubscriptionID: subID, Mode: store.ExtSubModeStack},
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st}
	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	dto := server.toUserDTO(httptest.NewRequest(http.MethodGet, "/api/user/list", nil), *user, nil)
	if len(dto.ExternalSubscriptions) != 1 {
		t.Fatalf("dto = %+v", dto)
	}
	sub := dto.ExternalSubscriptions[0]
	if sub.Name != "机场X" || sub.Mode != store.ExtSubModeStack || sub.Total != 200 ||
		sub.Remaining == nil || *sub.Remaining != 50 {
		t.Fatalf("sub dto = %+v", sub)
	}
	if dto.MergedTraffic == nil || dto.MergedTraffic.Total != 700 || dto.MergedTraffic.Upload != 100 {
		t.Fatalf("merged = %+v", dto.MergedTraffic)
	}
}

func itoa(v int64) string { return fmt.Sprintf("%d", v) }
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/panel/ -run TestSetUserExternalSubscriptionsRPC -v`
Expected: 编译失败（DTO/方法未定义）。

- [ ] **Step 3: 实现 handler 与校验辅助**

在 `src/backend/internal/panel/users.go` 的 import 中追加 `"lattix/backend/internal/extsub"`。

在 `handleSetUserNodes`（users.go:519）之后追加：

```go
type userExternalSubscriptionInput struct {
	SubscriptionID int64  `json:"subscription_id"`
	Mode           string `json:"mode"`
}

// validExtSubMode 判断外部订阅引入模式。
func validExtSubMode(mode string) bool {
	return mode == store.ExtSubModeStack || mode == store.ExtSubModeMerge || mode == store.ExtSubModeNodes
}

// validateExternalSubscriptions 校验外部订阅列表（去重、模式、存在性）。
func (s *Server) validateExternalSubscriptions(ctx context.Context, items []userExternalSubscriptionInput) ([]store.UserExternalSubscription, error) {
	seen := map[int64]bool{}
	out := make([]store.UserExternalSubscription, 0, len(items))
	for _, item := range items {
		if !validExtSubMode(item.Mode) {
			return nil, errors.New("mode 必须是 stack/merge/nodes")
		}
		if item.SubscriptionID <= 0 || seen[item.SubscriptionID] {
			return nil, errors.New("外部订阅重复或 id 非法")
		}
		seen[item.SubscriptionID] = true
		if _, err := s.st.ExternalSubscriptionByID(ctx, item.SubscriptionID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("外部订阅 %d 不存在: %w", item.SubscriptionID, store.ErrNotFound)
			}
			return nil, err
		}
		out = append(out, store.UserExternalSubscription{SubscriptionID: item.SubscriptionID, Mode: item.Mode})
	}
	return out, nil
}

func (s *Server) handleSetUserExternalSubscriptions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64                           `json:"user_id"`
		Items  []userExternalSubscriptionInput `json:"items"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.UserID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if _, err := s.st.UserByID(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := s.validateExternalSubscriptions(r.Context(), req.Items)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	for i := range items {
		items[i].UserID = id
	}
	if err := s.st.SetUserExternalSubscriptions(r.Context(), id, items); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.subscriptions != nil {
		if _, err := s.subscriptions.PublishUser(r.Context(), id, s.panelBase(r)); err != nil {
			writeError(w, http.StatusInternalServerError, "重新生成订阅失败: "+err.Error())
			return
		}
	}
	s.audit(r, "user.external_subscriptions_updated", nil, nil, map[string]any{
		"user_id": id, "items": req.Items,
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": req.Items})
}
```

（错误码映射：`writeError` 按 legacy HTTP 状态映射 RPC 码（panel.go:504），404 → `NOT_FOUND`、400 → `INVALID_ARGUMENT`，与 `handleSetUserNodes` 分支风格一致。）

- [ ] **Step 4: 实现 DTO 扩展**

`userDTO`（users.go:22-45）追加字段：

```go
	ExternalSubscriptions []userExternalSubscriptionDTO `json:"external_subscriptions"`
	MergedTraffic         *mergedTrafficDTO             `json:"merged_traffic,omitempty"`
```

在 `userChainAssignmentDTO` 之后追加：

```go
type userExternalSubscriptionDTO struct {
	SubscriptionID int64  `json:"subscription_id"`
	Name           string `json:"name"`
	Mode           string `json:"mode"`
	Upload         int64  `json:"upload"`
	Download       int64  `json:"download"`
	Total          int64  `json:"total"`
	Expire         *int64 `json:"expire,omitempty"`
	Remaining      *int64 `json:"remaining"` // total=0（未知额度）时为 null
	NodeCount      int    `json:"node_count"`
}

type mergedTrafficDTO struct {
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	Total    int64  `json:"total"`
	Expire   *int64 `json:"expire,omitempty"`
}
```

`toUserDTO`（users.go:54）末尾（`dto.ChainAssignments` 循环之后）追加：

```go
	attached, err := s.st.ListUserExternalSubscriptions(r.Context(), u.ID)
	if err == nil && len(attached) > 0 {
		dto.ExternalSubscriptions = make([]userExternalSubscriptionDTO, 0, len(attached))
		var panelTraffic store.TrafficTotals
		if t, err := s.st.UserTraffic(r.Context(), u.UUID); err == nil {
			panelTraffic = t
		}
		var panelExpire *int64
		if u.ExpiresAt != nil {
			v := u.ExpiresAt.Unix()
			panelExpire = &v
		}
		for _, sub := range attached {
			var remaining *int64
			if sub.Total > 0 {
				v := sub.Total - sub.Upload - sub.Download
				if v < 0 {
					v = 0
				}
				remaining = &v
			}
			dto.ExternalSubscriptions = append(dto.ExternalSubscriptions, userExternalSubscriptionDTO{
				SubscriptionID: sub.SubscriptionID, Name: sub.Name, Mode: sub.Mode,
				Upload: sub.Upload, Download: sub.Download, Total: sub.Total,
				Expire: sub.Expire, Remaining: remaining, NodeCount: sub.NodeCount,
			})
		}
		merged := extsub.MergeUserTraffic(extsub.Traffic{
			Upload: panelTraffic.Up, Download: panelTraffic.Down,
			Total: u.TrafficLimit, Expire: panelExpire,
		}, attached)
		dto.MergedTraffic = &mergedTrafficDTO{
			Upload: merged.Upload, Download: merged.Download,
			Total: merged.Total, Expire: merged.Expire,
		}
	}
```

- [ ] **Step 5: handleCreateUser 支持外部订阅**

`handleCreateUser`（users.go:237-248）的请求结构追加字段：

```go
		ExternalSubscriptions []userExternalSubscriptionInput `json:"external_subscriptions"`
```

在链路分配块（`if len(req.ChainIDs) > 0 {...}`）之后、`if s.subscriptions != nil` 之前追加：

```go
	if len(req.ExternalSubscriptions) > 0 {
		items, err := s.validateExternalSubscriptions(r.Context(), req.ExternalSubscriptions)
		if err != nil {
			_ = s.st.DeleteUser(r.Context(), id)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		for i := range items {
			items[i].UserID = id
		}
		if err := s.st.SetUserExternalSubscriptions(r.Context(), id, items); err != nil {
			_ = s.st.DeleteUser(r.Context(), id)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
```

- [ ] **Step 6: 注册路由**

在 `src/backend/internal/panel/panel.go` 的 `set-nodes` 路由（panel.go:320-322）之后追加：

```go
	s.registerRPC(mux, http.MethodPost, "/api/user/set-external-subscriptions",
		write, s.handleSetUserExternalSubscriptions)
```

- [ ] **Step 7: openapi.yaml**

在 `docs/openapi.yaml` 的 `/api/user/set-nodes:`（第 404 行起）之后追加：

```yaml
  /api/user/set-external-subscriptions:
    post:
      operationId: userSetExternalSubscriptions
      description: |
        整表替换用户引入的外部订阅及其模式（叠加 stack / 并入 merge / 附加 nodes）。
        保存后重发布该用户的订阅快照。
      parameters:
        - {$ref: '#/components/parameters/CSRFToken'}
        - {$ref: '#/components/parameters/IdempotencyKey'}
      requestBody: {$ref: '#/components/requestBodies/RPCBody'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
```

- [ ] **Step 8: 运行确认通过**

Run: `go test ./src/backend/internal/panel/ -run 'TestSetUserExternalSubscriptionsRPC|TestUserDTOIncludesExternalSubscriptions' -v`
Expected: PASS。

Run: `go test ./src/backend/internal/panel/ -run TestContract`
Expected: PASS（openapi 与路由一致）。

- [ ] **Step 9: 提交**

```bash
git add src/backend/internal/panel/users.go src/backend/internal/panel/panel.go docs/openapi.yaml src/backend/internal/panel/user_external_subscriptions_test.go
git commit -m "feat(panel): assign external subscriptions to users"
```

---

### Task 8: 同步联动 — 重发布关联用户

**Files:**
- Modify: `src/backend/internal/extsub/service.go:227-247`（`SyncDue` 返回本次同步成功的订阅 ID）
- Modify: `src/backend/internal/extsub/service_test.go`（SyncDue 测试适配新签名）
- Modify: `src/backend/internal/panel/panel.go:93-100`（定时任务编排重发布）
- Modify: `src/backend/internal/panel/external_subscriptions.go`（`handleSyncExternalSubscription` 调重发布 + 新增 helper）

**Interfaces:**
- Consumes: Task 1 `UsersByExternalSubscriptionID`、`sub.Server.EnqueueUsers(userIDs []int64, baseURL string)`（`src/backend/internal/sub/regenerate.go:37`）。

- [ ] **Step 1: 改 SyncDue 签名并适配测试**

`src/backend/internal/extsub/service.go` 的 `SyncDue`（service.go:227-247）替换为：

```go
// SyncDue 同步所有到达自动更新间隔的订阅；单订阅失败不影响其他订阅。
// 返回本次实际同步成功的订阅 ID 列表（供调用方触发关联用户订阅重发布）。
func (s *Service) SyncDue(ctx context.Context) ([]int64, error) {
	subs, err := s.st.ListExternalSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var synced []int64
	for _, sub := range subs {
		if !sub.AutoUpdate {
			continue
		}
		if sub.LastAttemptAt != nil &&
			sub.LastAttemptAt.Add(time.Duration(sub.UpdateIntervalHours)*time.Hour).After(now) {
			continue
		}
		if _, err := s.Sync(ctx, sub.ID); err != nil {
			// 记录保留 last_error；继续其他订阅
			continue
		}
		synced = append(synced, sub.ID)
	}
	return synced, nil
}
```

`src/backend/internal/extsub/service_test.go` 的 `TestSyncDueOnlySyncsDueSubscriptions` 中调用改为：

```go
	hitsBefore := hits
	synced, err := svc.SyncDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hits != hitsBefore+1 {
		t.Fatalf("SyncDue hits = %d (before %d), want exactly one more", hits, hitsBefore)
	}
	if len(synced) != 1 || synced[0] != auto.ID {
		t.Fatalf("synced = %v, want [%d]", synced, auto.ID)
	}
```

- [ ] **Step 2: 运行确认**

Run: `go test ./src/backend/internal/extsub/ -v`
Expected: PASS。

- [ ] **Step 3: panel — republish helper 与手动同步挂钩**

`src/backend/internal/panel/external_subscriptions.go` 追加：

```go
// republishExternalSubUsers 将关联了给定外部订阅的用户加入订阅重生成队列。
func (s *Server) republishExternalSubUsers(ctx context.Context, subscriptionIDs []int64) {
	if s.subscriptions == nil || len(subscriptionIDs) == 0 {
		return
	}
	seen := map[int64]bool{}
	var userIDs []int64
	for _, subID := range subscriptionIDs {
		users, err := s.st.UsersByExternalSubscriptionID(ctx, subID)
		if err != nil {
			continue
		}
		for _, userID := range users {
			if !seen[userID] {
				seen[userID] = true
				userIDs = append(userIDs, userID)
			}
		}
	}
	s.subscriptions.EnqueueUsers(userIDs, "")
}
```

`handleSyncExternalSubscription`（external_subscriptions.go:117-130）中 `Sync` 成功返回后追加：

```go
	s.republishExternalSubUsers(r.Context(), []int64{sub.ID})
```

- [ ] **Step 4: panel — 定时任务编排**

`src/backend/internal/panel/panel.go` 的 `SetExternalSubscriptionService`（panel.go:93-100）中定时任务 `run` 改为：

```go
		run: func(ctx context.Context) error {
			synced, err := service.SyncDue(ctx)
			if err != nil {
				return err
			}
			s.republishExternalSubUsers(ctx, synced)
			return nil
		},
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./src/backend/internal/panel/ ./src/backend/internal/extsub/ -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add src/backend/internal/extsub/service.go src/backend/internal/extsub/service_test.go src/backend/internal/panel/panel.go src/backend/internal/panel/external_subscriptions.go
git commit -m "feat(panel): republish linked users after external sync"
```

---

### Task 9: 前端 — 用户弹窗外部订阅分配与统计

**Files:**
- Modify: `src/frontend/src/lib/types.ts`（`SubUser` 扩展 + `ExternalSubscriptionMode`）
- Modify: `src/frontend/src/lib/api.ts`（`setUserExternalSubscriptions`、`createUser` 支持）
- Modify: `src/frontend/src/pages/Users.tsx`（创建/分配弹窗 + 统计表）
- Check: `src/frontend/src/lib/api-contract.generated.ts`（`npm run build` 自动再生成/校验）

**Interfaces:**
- Consumes: Task 7 的 `userDTO.external_subscriptions` / `merged_traffic`、`POST /api/user/set-external-subscriptions`；既有 `api.externalSubscriptions()`（返回 `ExternalSubscription[]`，`src/frontend/src/lib/api.ts:302`）。

- [ ] **Step 1: types.ts**

`src/frontend/src/lib/types.ts` 在 `ExternalSubscription` 接口（第 948 行）之前追加：

```ts
export type ExternalSubscriptionMode = 'stack' | 'merge' | 'nodes'

export interface UserExternalSubscription {
  subscription_id: number
  name: string
  mode: ExternalSubscriptionMode
  upload: number
  download: number
  total: number
  expire?: number | null
  remaining: number | null
  node_count: number
}

export interface MergedTraffic {
  upload: number
  download: number
  total: number
  expire?: number | null
}
```

`SubUser`（第 518 行起）追加字段：

```ts
  external_subscriptions: UserExternalSubscription[]
  merged_traffic?: MergedTraffic
```

- [ ] **Step 2: api.ts**

`src/frontend/src/lib/api.ts` 的 import 中追加 `UserExternalSubscription`（若用于类型标注）与 `ExternalSubscriptionMode`；在 `setUserAssignments`（第 258 行）之后追加：

```ts
  setUserExternalSubscriptions: (
    userId: number,
    items: Array<{ subscription_id: number; mode: ExternalSubscriptionMode }>,
  ) =>
    requester.post<{ items: Array<{ subscription_id: number; mode: ExternalSubscriptionMode }> }>(
      '/api/user/set-external-subscriptions',
      { user_id: userId, items },
    ),
```

`createUser`（第 239-250 行）的 `sub` 参数类型追加：

```ts
    sub?: { traffic_limit?: number; traffic_reset_day?: number; plan_name?: string; app_url?: string; routing?: SubscriptionRoutingProfile; external_subscriptions?: Array<{ subscription_id: number; mode: ExternalSubscriptionMode }> },
```

- [ ] **Step 3: Users.tsx — 状态与加载**

`src/frontend/src/pages/Users.tsx`：

import 中追加 `ExternalSubscription, ExternalSubscriptionMode, UserExternalSubscription` 类型，并追加：

```ts
const EXTERNAL_MODE_LABELS: Record<ExternalSubscriptionMode, string> = {
  stack: '叠加',
  merge: '并入',
  nodes: '附加',
}

function ExternalModeSelect({
  value,
  onChange,
  disabled,
}: {
  value: ExternalSubscriptionMode
  onChange: (mode: ExternalSubscriptionMode) => void
  disabled?: boolean
}) {
  return (
    <Select value={value} onValueChange={(next) => next && onChange(next as ExternalSubscriptionMode)} disabled={disabled}>
      <SelectTrigger className="w-24" aria-label="引入模式">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {(Object.keys(EXTERNAL_MODE_LABELS) as ExternalSubscriptionMode[]).map((mode) => (
          <SelectItem key={mode} value={mode}>{EXTERNAL_MODE_LABELS[mode]}</SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
```

组件状态追加：

```ts
  const [extSubs, setExtSubs] = useState<ExternalSubscription[]>([])
  const [assignExt, setAssignExt] = useState<Record<number, ExternalSubscriptionMode>>({})
  const [createExt, setCreateExt] = useState<Record<number, ExternalSubscriptionMode>>({})
```

`load`（第 191 行）中 `Promise.all` 之后追加独立加载（失败不阻断页面）：

```ts
      try {
        setExtSubs(await api.externalSubscriptions({ display: 'silent' }))
      } catch {
        // 外部订阅列表不可用不阻断用户页
      }
```

- [ ] **Step 4: Users.tsx — 分配弹窗**

`onOpenAssign`（第 341 行）改为初始化外部订阅选择：

```ts
  const onOpenAssign = (u: SubUser) => {
    setAssignTarget(u)
    setAssignSelection(u.chain_ids)
    setAssignExt(
      Object.fromEntries(u.external_subscriptions.map((s) => [s.subscription_id, s.mode])),
    )
    setAssignError('')
  }
```

`onSaveAssign`（第 351 行）在 `setUserAssignments` 之后追加：

```ts
      await api.setUserExternalSubscriptions(
        assignTarget.id,
        Object.entries(assignExt).map(([id, mode]) => ({ subscription_id: Number(id), mode })),
      )
```

分配弹窗（`<Dialog open={assignTarget !== null} ...>`，第 784 行起）的链路区之后追加「外部订阅」区与统计表：

```tsx
          <div className="space-y-2 border-t pt-3">
            <Label>外部订阅（叠加 = 额度相加，并入 = 已用计入面板配额，附加 = 仅节点）</Label>
            {extSubs.length === 0 ? (
              <p className="text-sm text-muted-foreground">暂无外部订阅，请先在「外部订阅」页添加。</p>
            ) : (
              extSubs.map((sub) => {
                const checked = assignExt[sub.id] !== undefined
                return (
                  <label
                    key={sub.id}
                    className="flex cursor-pointer items-center gap-2 rounded-md border p-2 text-sm"
                  >
                    <input
                      type="checkbox"
                      className="size-4 accent-primary"
                      checked={checked}
                      onChange={(e) => {
                        setAssignExt((cur) => {
                          const next = { ...cur }
                          if (e.target.checked) {
                            next[sub.id] = 'stack'
                          } else {
                            delete next[sub.id]
                          }
                          return next
                        })
                      }}
                    />
                    <span>{sub.name}</span>
                    <span className="text-xs text-muted-foreground">
                      {sub.total > 0
                        ? `${humanizeBytes(sub.total)} / 已用 ${humanizeBytes(sub.upload + sub.download)}`
                        : '额度未知'}
                    </span>
                    <span className="ml-auto">
                      <ExternalModeSelect
                        value={checked ? assignExt[sub.id] : 'stack'}
                        disabled={!checked}
                        onChange={(mode) => setAssignExt((cur) => ({ ...cur, [sub.id]: mode }))}
                      />
                    </span>
                  </label>
                )
              })
            )}
          </div>
          {assignTarget && assignTarget.external_subscriptions.length > 0 ? (
            <div className="space-y-2 border-t pt-3">
              <Label>外部订阅统计</Label>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>名称</TableHead>
                    <TableHead>模式</TableHead>
                    <TableHead>额度</TableHead>
                    <TableHead>已用</TableHead>
                    <TableHead>剩余</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {assignTarget.external_subscriptions.map((s) => (
                    <TableRow key={s.subscription_id}>
                      <TableCell className="text-xs">{s.name}</TableCell>
                      <TableCell className="text-xs">{EXTERNAL_MODE_LABELS[s.mode]}</TableCell>
                      <TableCell className="text-xs">
                        {s.total > 0 ? humanizeBytes(s.total) : '未知'}
                      </TableCell>
                      <TableCell className="text-xs">{humanizeBytes(s.upload + s.download)}</TableCell>
                      <TableCell className="text-xs">
                        {s.remaining === null ? '未知' : humanizeBytes(s.remaining)}
                      </TableCell>
                    </TableRow>
                  ))}
                  {assignTarget.merged_traffic ? (
                    <TableRow>
                      <TableCell className="text-xs font-medium" colSpan={2}>合并后（含面板）</TableCell>
                      <TableCell className="text-xs">
                        {assignTarget.merged_traffic.total > 0 ? humanizeBytes(assignTarget.merged_traffic.total) : '不限'}
                      </TableCell>
                      <TableCell className="text-xs">
                        {humanizeBytes(assignTarget.merged_traffic.upload + assignTarget.merged_traffic.download)}
                      </TableCell>
                      <TableCell className="text-xs">
                        {assignTarget.merged_traffic.total > 0
                          ? humanizeBytes(Math.max(0, assignTarget.merged_traffic.total - assignTarget.merged_traffic.upload - assignTarget.merged_traffic.download))
                          : '—'}
                      </TableCell>
                    </TableRow>
                  ) : null}
                </TableBody>
              </Table>
            </div>
          ) : null}
```

- [ ] **Step 5: Users.tsx — 创建弹窗**

`onOpenChange`（第 233 行）重置时追加 `setCreateExt({})`。

`onCreate`（第 256 行）的 `api.createUser` 调用中 `sub` 对象追加：

```ts
          external_subscriptions: Object.entries(createExt).map(([id, mode]) => ({ subscription_id: Number(id), mode })),
```

创建弹窗（第 655 行起 `form` 内）链路区之后追加与分配弹窗同构的「外部订阅」区（复用 `ExternalModeSelect`，状态用 `createExt`/`setCreateExt`）。

- [ ] **Step 6: 运行确认通过**

Run（`src/frontend` 目录）：`npm run build`
Expected: 构建成功（含 `generate-api-types.mjs --check` 通过）。

Run: `go test ./src/backend/internal/panel/ -run TestContract`
Expected: PASS（若前端 codegen 校验失败，检查 `api-contract.generated.ts` 是否需随 openapi 更新提交）。

- [ ] **Step 7: 提交**

```bash
git add src/frontend/src/lib/types.ts src/frontend/src/lib/api.ts src/frontend/src/pages/Users.tsx
git commit -m "feat(frontend): external subscription assignment in user dialogs"
```

---

## 非目标（后续版本）

- 外部节点测速、按协议/地区筛选。
- 外部订阅流量历史统计/归档。
- 配额强制（现状仅展示，维持）。
- 外部节点名称与面板节点重名的去重处理（客户端按名称引用分组，重名风险接受）。
