# 外部订阅导入 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增外部订阅管理功能——管理员导入第三方订阅 URL，解析其中节点并保存到 `external_chains`，订阅信息（流量等）保存到 `external_subscriptions`，支持手动/定时同步；下一版本再关联用户订阅。

**Architecture:** 独立 `src/backend/internal/extsub/` 包（fetcher 复用 `src/shared/requester` + 三格式 parser + service），新增两张独立 SQLite 表（不触碰现有 chains/nodes 状态机），panel 注册 6 个 RPC 路由 + 15 分钟定时同步任务，前端新增外部订阅页。

**Tech Stack:** Go 1.26 (module `lattix/backend`)、modernc.org/sqlite、gopkg.in/yaml.v3（已有）、React + Vite + wouter + shadcn/ui（base-ui）、openapi-typescript codegen。

## Global Constraints

- 工作目录为仓库根；Go 命令在仓库根运行（go.work 覆盖三个 module）。
- 前端 `npm run build` 会执行 `node scripts/generate-api-types.mjs --check`，`docs/openapi.yaml` 新增路径必须与 `RegisterRoutes` 完全一致（`panel/contract_test.go` 强校验）。
- 所有外部 HTTP 拉取必须走 `src/shared/requester`（AGENTS.md）。
- 不新增 Go 依赖（yaml.v3 已存在）；前端不新增 npm 依赖。
- 新表加入 `store.go` 的 `Schema` 常量即自动创建；`schemaVersion` 不变（不迁移既有表）。
- 变更操作全部使用 POST（前端 requester 无 PUT/DELETE，惯例是 `/api/user/delete` 这类动词路由）。
- 中文 UI 文案，遵循现有页面风格。
- 每次任务结束必须跑测试并提交。

---

### Task 1: Requester — 带选项与响应头的文件拉取

**Files:**
- Modify: `src/shared/requester/external.go`
- Test: `src/shared/requester/external_test.go`

**Interfaces:**
- Produces:
  ```go
  type FileRequestOptions struct{ UserAgent string }
  type FileFetchResult struct { Body string; Header http.Header }
  func (r ExternalFileRequester) GetWithOptions(ctx context.Context, url string, maxBytes int64, opts FileRequestOptions) (FileFetchResult, error)
  func (r ExternalFileRequester) GetTextWithOptions(ctx context.Context, url string, maxBytes int64, opts FileRequestOptions) (string, error)
  ```
  Task 4 用 `GetWithOptions` 拿 `subscription-userinfo` 响应头；`GetText`/`GetTextWithOptions` 为薄包装。

- [ ] **Step 1: 写失败测试**

在 `src/shared/requester/external_test.go` 末尾追加：

```go
func TestGetWithOptionsSetsUserAgentAndReturnsHeader(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("subscription-userinfo", "upload=1; download=2; total=3; expire=4")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	r := ExternalFileRequester{Doer: &http.Client{Timeout: 5 * time.Second}}
	result, err := r.GetWithOptions(context.Background(), srv.URL, 1024, FileRequestOptions{UserAgent: "clash-meta/2.4.0"})
	if err != nil {
		t.Fatal(err)
	}
	if gotUA != "clash-meta/2.4.0" {
		t.Fatalf("user-agent = %q", gotUA)
	}
	if result.Body != "hello" {
		t.Fatalf("body = %q", result.Body)
	}
	if got := result.Header.Get("subscription-userinfo"); got != "upload=1; download=2; total=3; expire=4" {
		t.Fatalf("userinfo header = %q", got)
	}
}

func TestGetTextWithOptionsOmitsUserAgentByDefault(t *testing.T) {
	gotUA := "unset"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	r := ExternalFileRequester{Doer: &http.Client{Timeout: 5 * time.Second}}
	if _, err := r.GetTextWithOptions(context.Background(), srv.URL, 1024, FileRequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if gotUA != "" {
		t.Fatalf("user-agent = %q, want empty", gotUA)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/shared/requester/ -run 'TestGetWithOptions|TestGetTextWithOptions' -v`
Expected: 编译失败（`GetWithOptions`/`GetTextWithOptions`/`FileRequestOptions`/`FileFetchResult` 未定义）。

- [ ] **Step 3: 实现**

在 `src/shared/requester/external.go` 中，把 `GetText` 改为包装实现并新增类型与方法：

```go
// FileRequestOptions 控制单次文件拉取的请求细节。
type FileRequestOptions struct {
	UserAgent string
}

// FileFetchResult 携带响应体与响应头，供需要读取头信息（如
// subscription-userinfo）的调用方使用。
type FileFetchResult struct {
	Body   string
	Header http.Header
}

// GetWithOptions 拉取文件并返回响应体与响应头。
func (r ExternalFileRequester) GetWithOptions(
	ctx context.Context, url string, maxBytes int64, opts FileRequestOptions,
) (FileFetchResult, error) {
	if r.Doer == nil {
		return FileFetchResult{}, fmt.Errorf("%s: external HTTP client is nil", redactedDestination(url))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FileFetchResult{}, wrapExternalURLError(url, "build request", err)
	}
	if opts.UserAgent != "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}
	resp, err := r.Doer.Do(req)
	if err != nil {
		return FileFetchResult{}, wrapExternalURLError(url, "request", err)
	}
	defer resp.Body.Close()
	if err := require2xx(url, resp); err != nil {
		return FileFetchResult{}, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return FileFetchResult{}, fmt.Errorf("%s: read body: %w", redactedDestination(url), err)
	}
	if int64(len(body)) > maxBytes {
		return FileFetchResult{}, fmt.Errorf("%s: response exceeds %d bytes", redactedDestination(url), maxBytes)
	}
	return FileFetchResult{Body: string(body), Header: resp.Header.Clone()}, nil
}

// GetTextWithOptions 拉取文件文本，可携带自定义请求头选项。
func (r ExternalFileRequester) GetTextWithOptions(
	ctx context.Context, url string, maxBytes int64, opts FileRequestOptions,
) (string, error) {
	result, err := r.GetWithOptions(ctx, url, maxBytes, opts)
	if err != nil {
		return "", err
	}
	return result.Body, nil
}

// GetText 拉取文件文本。
func (r ExternalFileRequester) GetText(ctx context.Context, url string, maxBytes int64) (string, error) {
	return r.GetTextWithOptions(ctx, url, maxBytes, FileRequestOptions{})
}
```

把原 `GetText` 函数体整体替换为上述三个方法（`Download` 保持不变）。`external_test.go` 需新增 imports：`net/http`、`net/http/httptest`、`time`（先检查现有 import，按需补）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/shared/requester/`
Expected: PASS（含新增两个测试与既有测试）。

- [ ] **Step 5: 提交**

```bash
git add src/shared/requester/external.go src/shared/requester/external_test.go
git commit -m "feat(requester): add option/header-aware file fetch"
```

---

### Task 2: Store — 外部订阅与外部链路表 + CRUD

**Files:**
- Modify: `src/backend/internal/store/store.go`（Schema 常量，追加两张表）
- Create: `src/backend/internal/store/external_subscriptions.go`
- Test: `src/backend/internal/store/external_subscriptions_test.go`

**Interfaces:**
- Consumes: 无（纯 store 层）。
- Produces（Task 4/5 使用）：
  ```go
  type ExternalSubscription struct {
      ID                   int64      `json:"id"`
      Name                 string     `json:"name"`
      URL                  string     `json:"url"`
      UserAgent            string     `json:"user_agent"`
      SkipCertVerify       bool       `json:"skip_cert_verify"`
      AutoUpdate           bool       `json:"auto_update"`
      UpdateIntervalHours  int        `json:"update_interval_hours"`
      Format               string     `json:"format"`
      NodeCount            int        `json:"node_count"`
      Upload               int64      `json:"upload"`
      Download             int64      `json:"download"`
      Total                int64      `json:"total"`
      Expire               *int64     `json:"expire,omitempty"`
      LastSyncAt           *time.Time `json:"last_sync_at,omitempty"`
      LastAttemptAt        *time.Time `json:"last_attempt_at,omitempty"`
      LastError            string     `json:"last_error,omitempty"`
      CreatedAt            time.Time  `json:"created_at"`
      UpdatedAt            time.Time  `json:"updated_at"`
  }
  type ExternalChain struct {
      ID             int64           `json:"id"`
      SubscriptionID int64           `json:"subscription_id"`
      Name           string          `json:"name"`
      Protocol       string          `json:"protocol"`
      Server         string          `json:"server"`
      Port           int             `json:"port"`
      Config         json.RawMessage `json:"config"`
      ConfigSHA256   string          `json:"config_sha256"`
      CreatedAt      time.Time       `json:"created_at"`
  }
  func (s *Store) CreateExternalSubscription(ctx context.Context, sub ExternalSubscription) (int64, error)
  func (s *Store) UpdateExternalSubscription(ctx context.Context, sub ExternalSubscription) error
  func (s *Store) DeleteExternalSubscription(ctx context.Context, id int64) error
  func (s *Store) ListExternalSubscriptions(ctx context.Context) ([]ExternalSubscription, error)
  func (s *Store) ExternalSubscriptionByID(ctx context.Context, id int64) (ExternalSubscription, error)
  func (s *Store) ExternalSubscriptionByURL(ctx context.Context, url string) (ExternalSubscription, error)
  func (s *Store) ReplaceExternalChains(ctx context.Context, subID int64, chains []ExternalChain) (int, error)
  func (s *Store) ListExternalChains(ctx context.Context, subID int64) ([]ExternalChain, error)
  ```

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/store/external_subscriptions_test.go`：

```go
package store

import (
	"context"
	"encoding/json"
	"testing"
)

func insertTestExternalSubscription(t *testing.T, st *Store, name, url string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := st.CreateExternalSubscription(ctx, ExternalSubscription{
		Name: name, URL: url, AutoUpdate: true, UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestExternalSubscriptionCRUDAndURLUnique(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	id := insertTestExternalSubscription(t, st, "机场A", "https://sub.example.com/a")
	sub, err := st.ExternalSubscriptionByID(ctx, id)
	if err != nil || sub.Name != "机场A" {
		t.Fatalf("by id: %+v, err %v", sub, err)
	}
	byURL, err := st.ExternalSubscriptionByURL(ctx, "https://sub.example.com/a")
	if err != nil || byURL.ID != id {
		t.Fatalf("by url: %+v, err %v", byURL, err)
	}
	if _, err := st.CreateExternalSubscription(ctx, ExternalSubscription{Name: "重复", URL: "https://sub.example.com/a"}); err == nil {
		t.Fatal("duplicate url unexpectedly succeeded")
	}

	sub.Name = "改名"
	sub.AutoUpdate = false
	sub.Upload = 1024
	sub.LastError = "boom"
	if err := st.UpdateExternalSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	got, err := st.ExternalSubscriptionByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "改名" || got.AutoUpdate || got.Upload != 1024 || got.LastError != "boom" {
		t.Fatalf("updated = %+v", got)
	}

	subs, err := st.ListExternalSubscriptions(ctx)
	if err != nil || len(subs) != 1 {
		t.Fatalf("list: %v, err %v", subs, err)
	}
	if err := st.DeleteExternalSubscription(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ExternalSubscriptionByID(ctx, id); err != ErrNotFound {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestReplaceExternalChainsReplacesAndDedups(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	subID := insertTestExternalSubscription(t, st, "机场B", "https://sub.example.com/b")

	cfg1 := json.RawMessage(`{"name":"东京","type":"vless","server":"1.1.1.1","port":443}`)
	cfg2 := json.RawMessage(`{"name":"大阪","type":"vless","server":"2.2.2.2","port":443}`)
	cfg2dup := json.RawMessage(`{"name":"大阪-副本","type":"vless","server":"2.2.2.2","port":443,"sni":"x.com"}`)
	first := []ExternalChain{
		{SubscriptionID: subID, Name: "东京", Protocol: "vless", Server: "1.1.1.1", Port: 443, Config: cfg1, ConfigSHA256: "sha-1"},
		{SubscriptionID: subID, Name: "大阪", Protocol: "vless", Server: "2.2.2.2", Port: 443, Config: cfg2, ConfigSHA256: "sha-2"},
	}
	count, err := st.ReplaceExternalChains(ctx, subID, first)
	if err != nil || count != 2 {
		t.Fatalf("first replace: count %d, err %v", count, err)
	}
	second := []ExternalChain{
		{SubscriptionID: subID, Name: "东京", Protocol: "vless", Server: "1.1.1.1", Port: 443, Config: cfg1, ConfigSHA256: "sha-1"},
		{SubscriptionID: subID, Name: "大阪-副本", Protocol: "vless", Server: "2.2.2.2", Port: 443, Config: cfg2dup, ConfigSHA256: "sha-2"},
	}
	count, err = st.ReplaceExternalChains(ctx, subID, second)
	if err != nil || count != 2 {
		t.Fatalf("second replace: count %d, err %v", count, err)
	}
	chains, err := st.ListExternalChains(ctx, subID)
	if err != nil || len(chains) != 2 {
		t.Fatalf("chains: %v, err %v", chains, err)
	}
	if chains[0].Name != "东京" || chains[1].Name != "大阪-副本" {
		t.Fatalf("chain order/content = %+v", chains)
	}
}

func TestDeleteExternalSubscriptionCascadesChains(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	subID := insertTestExternalSubscription(t, st, "机场C", "https://sub.example.com/c")
	if _, err := st.ReplaceExternalChains(ctx, subID, []ExternalChain{{
		SubscriptionID: subID, Name: "n", Protocol: "vless", Server: "3.3.3.3", Port: 443,
		Config: json.RawMessage(`{"name":"n"}`), ConfigSHA256: "s",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteExternalSubscription(ctx, subID); err != nil {
		t.Fatal(err)
	}
	chains, err := st.ListExternalChains(ctx, subID)
	if err != nil || len(chains) != 0 {
		t.Fatalf("cascade delete left chains: %v, err %v", chains, err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/store/ -run TestExternalSubscription -v`
Expected: 编译失败（类型/方法未定义）。

- [ ] **Step 3: 加表**

在 `src/backend/internal/store/store.go` 的 `Schema` 常量末尾（`chain_hop_identities` 表之后、结尾反引号之前）追加：

```sql

-- 外部订阅（第三方机场等导入；下一版本关联用户订阅）
CREATE TABLE IF NOT EXISTS external_subscriptions (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    name                  TEXT NOT NULL,
    url                   TEXT NOT NULL UNIQUE,
    user_agent            TEXT NOT NULL DEFAULT '',
    skip_cert_verify      INTEGER NOT NULL DEFAULT 0,
    auto_update           INTEGER NOT NULL DEFAULT 1,
    update_interval_hours INTEGER NOT NULL DEFAULT 24,
    format                TEXT NOT NULL DEFAULT '',
    node_count            INTEGER NOT NULL DEFAULT 0,
    upload                INTEGER NOT NULL DEFAULT 0,
    download              INTEGER NOT NULL DEFAULT 0,
    total                 INTEGER NOT NULL DEFAULT 0,
    expire                INTEGER,
    last_sync_at          DATETIME,
    last_attempt_at       DATETIME,
    last_error            TEXT NOT NULL DEFAULT '',
    created_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS external_chains (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES external_subscriptions(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    protocol        TEXT NOT NULL,
    server          TEXT NOT NULL DEFAULT '',
    port            INTEGER NOT NULL DEFAULT 0,
    config          TEXT NOT NULL,
    config_sha256   TEXT NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_external_chains_subscription
    ON external_chains(subscription_id);
```

- [ ] **Step 4: 实现 store 方法**

创建 `src/backend/internal/store/external_subscriptions.go`：

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type ExternalSubscription struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	URL                 string     `json:"url"`
	UserAgent           string     `json:"user_agent"`
	SkipCertVerify      bool       `json:"skip_cert_verify"`
	AutoUpdate          bool       `json:"auto_update"`
	UpdateIntervalHours int        `json:"update_interval_hours"`
	Format              string     `json:"format"`
	NodeCount           int        `json:"node_count"`
	Upload              int64      `json:"upload"`
	Download            int64      `json:"download"`
	Total               int64      `json:"total"`
	Expire              *int64     `json:"expire,omitempty"`
	LastSyncAt          *time.Time `json:"last_sync_at,omitempty"`
	LastAttemptAt       *time.Time `json:"last_attempt_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type ExternalChain struct {
	ID             int64           `json:"id"`
	SubscriptionID int64           `json:"subscription_id"`
	Name           string          `json:"name"`
	Protocol       string          `json:"protocol"`
	Server         string          `json:"server"`
	Port           int             `json:"port"`
	Config         json.RawMessage `json:"config"`
	ConfigSHA256   string          `json:"config_sha256"`
	CreatedAt      time.Time       `json:"created_at"`
}

const externalSubscriptionColumns = `id, name, url, user_agent, skip_cert_verify,
	auto_update, update_interval_hours, format, node_count, upload, download, total,
	expire, last_sync_at, last_attempt_at, last_error, created_at, updated_at`

func scanExternalSubscription(row scanner) (ExternalSubscription, error) {
	var sub ExternalSubscription
	var skipCert, autoUpdate int
	var expire sql.NullInt64
	var lastSyncAt, lastAttemptAt, createdAt, updatedAt sql.NullTime
	err := row.Scan(&sub.ID, &sub.Name, &sub.URL, &sub.UserAgent, &skipCert,
		&autoUpdate, &sub.UpdateIntervalHours, &sub.Format, &sub.NodeCount,
		&sub.Upload, &sub.Download, &sub.Total, &expire,
		&lastSyncAt, &lastAttemptAt, &sub.LastError, &createdAt, &updatedAt)
	if err != nil {
		return ExternalSubscription{}, err
	}
	sub.SkipCertVerify = skipCert != 0
	sub.AutoUpdate = autoUpdate != 0
	if expire.Valid {
		sub.Expire = &expire.Int64
	}
	if lastSyncAt.Valid {
		sub.LastSyncAt = &lastSyncAt.Time
	}
	if lastAttemptAt.Valid {
		sub.LastAttemptAt = &lastAttemptAt.Time
	}
	sub.CreatedAt, sub.UpdatedAt = createdAt.Time, updatedAt.Time
	return sub, nil
}

func (s *Store) CreateExternalSubscription(ctx context.Context, sub ExternalSubscription) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO external_subscriptions
		(name, url, user_agent, skip_cert_verify, auto_update, update_interval_hours, format)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sub.Name, sub.URL, sub.UserAgent, boolInt(sub.SkipCertVerify), boolInt(sub.AutoUpdate),
		sub.UpdateIntervalHours, sub.Format)
	if err != nil {
		return 0, fmt.Errorf("insert external subscription: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("external subscription id: %w", err)
	}
	return id, nil
}

func (s *Store) UpdateExternalSubscription(ctx context.Context, sub ExternalSubscription) error {
	res, err := s.db.ExecContext(ctx, `UPDATE external_subscriptions SET
		name = ?, url = ?, user_agent = ?, skip_cert_verify = ?, auto_update = ?,
		update_interval_hours = ?, format = ?, node_count = ?, upload = ?, download = ?,
		total = ?, expire = ?, last_sync_at = ?, last_attempt_at = ?, last_error = ?,
		updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		sub.Name, sub.URL, sub.UserAgent, boolInt(sub.SkipCertVerify), boolInt(sub.AutoUpdate),
		sub.UpdateIntervalHours, sub.Format, sub.NodeCount, sub.Upload, sub.Download,
		sub.Total, sub.Expire, sub.LastSyncAt, sub.LastAttemptAt, sub.LastError, sub.ID)
	if err != nil {
		return fmt.Errorf("update external subscription: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external subscription rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteExternalSubscription(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM external_subscriptions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete external subscription: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external subscription rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListExternalSubscriptions(ctx context.Context) ([]ExternalSubscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+externalSubscriptionColumns+`
		FROM external_subscriptions ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list external subscriptions: %w", err)
	}
	defer rows.Close()
	var subs []ExternalSubscription
	for rows.Next() {
		sub, err := scanExternalSubscription(rows)
		if err != nil {
			return nil, fmt.Errorf("scan external subscription: %w", err)
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (s *Store) ExternalSubscriptionByID(ctx context.Context, id int64) (ExternalSubscription, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+externalSubscriptionColumns+`
		FROM external_subscriptions WHERE id = ?`, id)
	sub, err := scanExternalSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalSubscription{}, ErrNotFound
	}
	if err != nil {
		return ExternalSubscription{}, fmt.Errorf("query external subscription %d: %w", id, err)
	}
	return sub, nil
}

func (s *Store) ExternalSubscriptionByURL(ctx context.Context, url string) (ExternalSubscription, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+externalSubscriptionColumns+`
		FROM external_subscriptions WHERE url = ?`, url)
	sub, err := scanExternalSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalSubscription{}, ErrNotFound
	}
	if err != nil {
		return ExternalSubscription{}, fmt.Errorf("query external subscription by url: %w", err)
	}
	return sub, nil
}

func (s *Store) ReplaceExternalChains(ctx context.Context, subID int64, chains []ExternalChain) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin replace external chains: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM external_chains WHERE subscription_id = ?`, subID); err != nil {
		return 0, fmt.Errorf("clear external chains: %w", err)
	}
	seen := make(map[string]bool)
	count := 0
	for _, chain := range chains {
		if seen[chain.ConfigSHA256] {
			continue
		}
		seen[chain.ConfigSHA256] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO external_chains
			(subscription_id, name, protocol, server, port, config, config_sha256)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			subID, chain.Name, chain.Protocol, chain.Server, chain.Port,
			string(chain.Config), chain.ConfigSHA256); err != nil {
			return 0, fmt.Errorf("insert external chain: %w", err)
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit replace external chains: %w", err)
	}
	return count, nil
}

func (s *Store) ListExternalChains(ctx context.Context, subID int64) ([]ExternalChain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, subscription_id, name, protocol,
		server, port, config, config_sha256, created_at
		FROM external_chains WHERE subscription_id = ? ORDER BY id`, subID)
	if err != nil {
		return nil, fmt.Errorf("list external chains: %w", err)
	}
	defer rows.Close()
	var chains []ExternalChain
	for rows.Next() {
		var chain ExternalChain
		var createdAt sql.NullTime
		var config string
		if err := rows.Scan(&chain.ID, &chain.SubscriptionID, &chain.Name, &chain.Protocol,
			&chain.Server, &chain.Port, &config, &chain.ConfigSHA256, &createdAt); err != nil {
			return nil, fmt.Errorf("scan external chain: %w", err)
		}
		chain.Config = json.RawMessage(config)
		chain.CreatedAt = createdAt.Time
		chains = append(chains, chain)
	}
	return chains, rows.Err()
}
```

检查 `store.go` 中 `scanner` 接口与 `boolInt` 辅助函数的确切名字，若不同按既有命名调整（`nodes.go`/`subscriptions.go` 中有先例）。

- [ ] **Step 5: 运行确认通过**

Run: `go test ./src/backend/internal/store/ -run TestExternalSubscription -v`
Expected: 3 个测试 PASS。

- [ ] **Step 6: 提交**

```bash
git add src/backend/internal/store/store.go src/backend/internal/store/external_subscriptions.go src/backend/internal/store/external_subscriptions_test.go
git commit -m "feat(store): external subscription and chain tables"
```

---

### Task 3: extsub — 节点解析器（base64 链接 / mihomo YAML / v2rayN 自定义）

**Files:**
- Create: `src/backend/internal/extsub/parse.go`
- Create: `src/backend/internal/extsub/parse_yaml.go`
- Test: `src/backend/internal/extsub/parse_test.go`

**Interfaces:**
- Produces（Task 4 使用）：
  ```go
  // Node 是标准化后的外部节点。
  type Node struct {
      Name   string         `json:"name"`
      Type   string         `json:"type"`
      Server string         `json:"server"`
      Port   int            `json:"port"`
      Extra  map[string]any `json:"extra,omitempty"`
  }
  // ParseSubscription 识别订阅内容格式并解析节点。格式为 "yaml"|"v2ray"|"v2rayn"。
  // 无任何可解析节点时返回错误。
  func ParseSubscription(body []byte) (nodes []Node, format string, err error)
  ```
  `Node` 的 `config` JSON 由 Task 4 对 `Node` 直接 `json.Marshal` 得到（键序稳定即可，sha256 用规范化 JSON）。

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/extsub/parse_test.go`：

```go
package extsub

import "testing"

const vlessLink = "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=Pbk&sid=abcd&sni=cdn.example.com&fp=chrome&flow=xtls-rprx-vision#%E4%B8%9C%E4%BA%AC%2001"

func TestParseLinksVless(t *testing.T) {
	nodes, format, err := ParseSubscription([]byte(vlessLink))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes %v, format %q, err %v", nodes, format, err)
	}
	n := nodes[0]
	if n.Name != "东京 01" || n.Type != "vless" || n.Server != "example.com" || n.Port != 443 {
		t.Fatalf("node = %+v", n)
	}
	if n.Extra["id"] != "11111111-2222-3333-4444-555555555555" || n.Extra["security"] != "reality" || n.Extra["flow"] != "xtls-rprx-vision" {
		t.Fatalf("extra = %+v", n.Extra)
	}
}

func TestParseLinksBase64Bundle(t *testing.T) {
	body := base64Encode(vlessLink + "\n" + "ss://" + base64urlEncode("aes-128-gcm:pass") + "@1.2.3.4:8388#ss-01")
	nodes, format, err := ParseSubscription([]byte(body))
	if err != nil || format != "v2ray" {
		t.Fatalf("format %q err %v", format, err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v", nodes)
	}
	ss := nodes[1]
	if ss.Type != "ss" || ss.Server != "1.2.3.4" || ss.Port != 8388 || ss.Extra["method"] != "aes-128-gcm" || ss.Extra["password"] != "pass" {
		t.Fatalf("ss node = %+v", ss)
	}
}

func TestParseLinksVmessAndV2rayN(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"vmess-01","add":"5.6.7.8","port":"443","id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","aid":"0","scy":"auto","net":"ws","type":"none","host":"h.example.com","path":"/p","tls":"tls","sni":"h.example.com"}`
	vmessLink := "vmess://" + base64urlEncode(vmessJSON)
	// v2rayN 自定义：无 scheme 的 base64 JSON 行
	customLine := base64urlEncode(`{"v":"2","ps":"custom-01","add":"9.9.9.9","port":"8443","id":"id-id-id","net":"tcp","tls":"","type":"none"}`)
	nodes, format, err := ParseSubscription([]byte(vmessLink + "\n" + customLine))
	if err != nil || format != "v2rayn" {
		t.Fatalf("format %q err %v", format, err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v", nodes)
	}
	if nodes[0].Type != "vmess" || nodes[0].Name != "vmess-01" || nodes[0].Server != "5.6.7.8" || nodes[0].Port != 443 {
		t.Fatalf("vmess node = %+v", nodes[0])
	}
	if nodes[1].Name != "custom-01" || nodes[1].Server != "9.9.9.9" || nodes[1].Port != 8443 {
		t.Fatalf("custom node = %+v", nodes[1])
	}
}

func TestParseYAMLMihomo(t *testing.T) {
	yamlBody := `proxies:
  - name: "香港 01"
    type: hysteria2
    server: hk.example.com
    port: 443
    password: "p1"
    sni: hk.example.com
  - name: "美国 02"
    type: vless
    server: us.example.com
    port: 443
    uuid: "11111111-2222-3333-4444-555555555555"
    network: ws
    ws-opts:
      path: /ws
    reality-opts:
      public-key: "pub"
      short-id: "1234"
    client-fingerprint: chrome
`
	nodes, format, err := ParseSubscription([]byte(yamlBody))
	if err != nil || format != "yaml" {
		t.Fatalf("format %q err %v", format, err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v", nodes)
	}
	hy2 := nodes[0]
	if hy2.Type != "hysteria2" || hy2.Server != "hk.example.com" || hy2.Port != 443 || hy2.Extra["password"] != "p1" || hy2.Extra["sni"] != "hk.example.com" {
		t.Fatalf("hy2 node = %+v", hy2)
	}
	vless := nodes[1]
	if vless.Type != "vless" || vless.Extra["uuid"] != "11111111-2222-3333-4444-555555555555" || vless.Extra["client-fingerprint"] != "chrome" {
		t.Fatalf("vless node = %+v", vless)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, _, err := ParseSubscription([]byte("hello world")); err == nil {
		t.Fatal("garbage unexpectedly parsed")
	}
	if _, _, err := ParseSubscription([]byte("")); err == nil {
		t.Fatal("empty body unexpectedly parsed")
	}
}

func base64Encode(s string) string { return stdBase64.EncodeToString([]byte(s)) }
func base64urlEncode(s string) string { return urlBase64.EncodeToString([]byte(s)) }
```

（`stdBase64`/`urlBase64` 为 `encoding/base64` 的 `StdEncoding`/`URLEncoding` 变量别名，在 Step 3 的 import 中声明。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/extsub/ -v`
Expected: 编译失败（包/类型不存在）。

- [ ] **Step 3: 实现 `parse.go`**

创建 `src/backend/internal/extsub/parse.go`：

```go
// Package extsub 实现外部订阅的拉取、解析与同步（设计文档：
// docs/superpowers/specs/2026-08-02-external-subscriptions-design.md）。
package extsub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

var (
	stdBase64 = base64.StdEncoding
	urlBase64 = base64.RawURLEncoding
)

// Node 是标准化后的外部节点。Extra 保留协议专有字段（键为小写，
// 值已做 URL 解码）。config JSON 即对 Node 的 json.Marshal 结果。
type Node struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Server string         `json:"server"`
	Port   int            `json:"port"`
	Extra  map[string]any `json:"extra,omitempty"`
}

// ParseSubscription 识别订阅内容格式并解析节点。返回格式为
// "yaml"|"v2ray"|"v2rayn"；无任何可解析节点时返回错误。
func ParseSubscription(body []byte) ([]Node, string, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, "", fmt.Errorf("subscription body is empty")
	}
	if nodes, ok := parseYAML([]byte(text)); ok && len(nodes) > 0 {
		return nodes, "yaml", nil
	}
	decoded := decodeBase64Layers(text)
	nodes, sawJSON := parseLinkLines(decoded)
	if len(nodes) > 0 {
		format := "v2ray"
		if sawJSON {
			format = "v2rayn"
		}
		return nodes, format, nil
	}
	return nil, "", fmt.Errorf("no supported nodes found")
}

// decodeBase64Layers 尝试最多三层 base64 解码；解码结果看起来不像
// 纯 base64（含换行或 URI scheme 前缀）即停止。
func decodeBase64Layers(text string) string {
	current := text
	for i := 0; i < 3; i++ {
		candidate, err := tryBase64Decode(current)
		if err != nil {
			return current
		}
		if !looksLikeBase64(candidate) {
			return current
		}
		current = candidate
	}
	return current
}

func tryBase64Decode(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	for _, enc := range []*base64.Encoding{stdBase64, urlBase64} {
		if decoded, err := enc.DecodeString(trimmed); err == nil {
			return string(decoded), nil
		}
	}
	padded := trimmed
	if pad := len(trimmed) % 4; pad != 0 {
		padded += strings.Repeat("=", 4-pad)
	}
	for _, enc := range []*base64.Encoding{stdBase64, urlBase64} {
		if decoded, err := enc.DecodeString(padded); err == nil {
			return string(decoded), nil
		}
	}
	return "", fmt.Errorf("not base64")
}

// looksLikeBase64 判断解码产物是否仍像 base64（由解码者自己决定是否再解一层）。
func looksLikeBase64(text string) bool {
	if strings.ContainsAny(text, "\n\r") {
		return false
	}
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		return false
	}
	if isKnownScheme(text) {
		return false
	}
	if strings.Contains(text, ":") {
		return false
	}
	return true
}

func isKnownScheme(text string) bool {
	for _, scheme := range []string{
		"vless://", "vmess://", "ss://", "ssr://", "trojan://",
		"hysteria2://", "hy2://", "tuic://", "wireguard://", "wg://",
		"anytls://", "snell://", "socks://", "socks5://", "http://", "https://",
	} {
		if strings.HasPrefix(text, scheme) {
			return true
		}
	}
	return false
}

// parseLinkLines 逐行解析分享链接；返回节点数与是否出现过 base64 JSON 条目。
func parseLinkLines(text string) ([]Node, bool) {
	var nodes []Node
	sawJSON := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if node, ok := parseURI(line); ok {
			nodes = append(nodes, node)
			continue
		}
		// v2rayN 自定义格式：无 scheme 的 base64 JSON 条目
		if decoded, err := tryBase64Decode(line); err == nil && looksLikeVmessJSON(decoded) {
			if node, ok := parseVmessJSON(decoded); ok {
				sawJSON = true
				nodes = append(nodes, node)
			}
		}
	}
	return nodes, sawJSON
}

func looksLikeVmessJSON(text string) bool {
	var probe map[string]any
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return false
	}
	_, hasAdd := probe["add"]
	_, hasPs := probe["ps"]
	return hasAdd && hasPs
}

func parseURI(uri string) (Node, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Hostname() == "" {
		return Node{}, false
	}
	switch u.Scheme {
	case "vless", "trojan", "hysteria2", "hy2", "tuic", "anytls", "snell", "socks", "socks5", "http":
		return parseCredentialURI(u), true
	case "vmess":
		return parseVmessURI(u), true
	case "ss":
		return parseSSURI(u), true
	case "ssr":
		return parseSSRURI(u), true
	case "wireguard", "wg":
		return parseWireguardURI(u), true
	}
	return Node{}, false
}

func portFrom(u *url.URL, fallback int) int {
	if u.Port() != "" {
		if port, err := strconv.Atoi(u.Port()); err == nil && port > 0 {
			return port
		}
	}
	return fallback
}

func nodeName(fragment string) string {
	if name, err := url.QueryUnescape(fragment); err == nil {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(fragment)
}

func extraFromQuery(u *url.URL) map[string]any {
	extra := make(map[string]any)
	for key, values := range u.Query() {
		if len(values) > 0 {
			extra[key] = values[0]
		}
	}
	return extra
}

func extraWith(extra map[string]any, key string, value any) map[string]any {
	if value == nil {
		return extra
	}
	extra[key] = value
	return extra
}

// parseCredentialURI 处理 userinfo 携带口令的协议（vless/trojan/hy2/tuic/...）。
func parseCredentialURI(u *url.URL) Node {
	node := Node{
		Name:   nodeName(u.Fragment),
		Type:   u.Scheme,
		Server: u.Hostname(),
		Port:   portFrom(u, 443),
		Extra:  extraFromQuery(u),
	}
	switch u.Scheme {
	case "vless":
		node.Extra = extraWith(node.Extra, "id", u.User.Username())
	case "trojan", "hysteria2", "hy2", "anytls":
		node.Extra = extraWith(node.Extra, "password", u.User.Username())
	case "tuic":
		node.Extra = extraWith(node.Extra, "uuid", u.User.Username())
		node.Extra = extraWith(node.Extra, "password", u.User.Password())
	case "snell":
		node.Extra = extraWith(node.Extra, "psk", u.User.Username())
	case "socks", "socks5":
		node.Type = "socks"
		node.Extra = extraWith(node.Extra, "username", u.User.Username())
		node.Extra = extraWith(node.Extra, "password", u.User.Password())
	}
	if node.Type == "" || node.Server == "" || node.Port == 0 {
		return Node{}
	}
	return node
}

func parseVmessURI(u *url.URL) (Node, bool) {
	decoded, err := tryBase64Decode(u.Host)
	if err != nil {
		return Node{}, false
	}
	return parseVmessJSON(decoded)
}

// parseVmessJSON 解析 vmess 的 base64 JSON 载荷。
func parseVmessJSON(decoded string) (Node, bool) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(decoded), &payload); err != nil {
		return Node{}, false
	}
	node := Node{
		Name:   nodeName(toString(payload["ps"])),
		Type:   "vmess",
		Server: toString(payload["add"]),
		Port:   toInt(payload["port"]),
		Extra:  make(map[string]any),
	}
	if node.Server == "" || node.Port == 0 {
		return Node{}, false
	}
	for key, value := range payload {
		if key == "ps" || key == "add" || key == "port" {
			continue
		}
		node.Extra[key] = value
	}
	return node, true
}

func parseSSURI(u *url.URL) (Node, bool) {
	userinfo := u.User.String()
	if userinfo == "" && u.Host != "" {
		userinfo = u.Host
	}
	decoded, err := tryBase64Decode(userinfo)
	if err != nil {
		return Node{}, false
	}
	// SIP002: method:password@host:port?plugin=...
	at := strings.LastIndex(decoded, "@")
	hostPort := decoded
	if at >= 0 {
		hostPort = decoded[at+1:]
	}
	var server string
	var port int
	if hp := strings.SplitN(hostPort, ":", 2); len(hp) == 2 {
		server = hp[0]
		port, _ = strconv.Atoi(hp[1])
	}
	if server == "" || port == 0 {
		// 旧格式：ss://base64(method:password@host:port)#name
		hostPort = u.Host
		if hp := strings.SplitN(hostPort, ":", 2); len(hp) == 2 {
			server = hp[0]
			port, _ = strconv.Atoi(hp[1])
		}
		if server == "" || port == 0 {
			return Node{}, false
		}
	}
	node := Node{
		Name:   nodeName(u.Fragment),
		Type:   "ss",
		Server: server,
		Port:   port,
		Extra:  extraFromQuery(u),
	}
	credentials := decoded
	if at >= 0 {
		credentials = decoded[:at]
	}
	parts := strings.SplitN(credentials, ":", 2)
	node.Extra = extraWith(node.Extra, "method", parts[0])
	if len(parts) == 2 {
		node.Extra = extraWith(node.Extra, "password", parts[1])
	}
	return node, true
}

func parseSSRURI(u *url.URL) (Node, bool) {
	decoded, err := tryBase64Decode(u.Host)
	if err != nil {
		return Node{}, false
	}
	// host:port:protocol:method:obfs:base64(password)?query
	parts := strings.SplitN(decoded, ":", 6)
	if len(parts) < 6 {
		return Node{}, false
	}
	port, _ := strconv.Atoi(parts[1])
	if port == 0 {
		return Node{}, false
	}
	password, err := tryBase64Decode(parts[5])
	if err != nil {
		return Node{}, false
	}
	node := Node{
		Name:   nodeName(u.Fragment),
		Type:   "ssr",
		Server: parts[0],
		Port:   port,
		Extra:  extraFromQuery(u),
	}
	node.Extra = extraWith(node.Extra, "protocol", parts[2])
	node.Extra = extraWith(node.Extra, "method", parts[3])
	node.Extra = extraWith(node.Extra, "obfs", parts[4])
	node.Extra = extraWith(node.Extra, "password", password)
	if query := u.Query(); query.Get("remarks") != "" {
		node.Name = nodeName(query.Get("remarks"))
	}
	return node, true
}

func parseWireguardURI(u *url.URL) (Node, bool) {
	query := u.Query()
	endpoint := query.Get("endpoint")
	var server string
	var port int
	if hp := strings.SplitN(endpoint, ":", 2); len(hp) == 2 {
		server = hp[0]
		port, _ = strconv.Atoi(hp[1])
	}
	if server == "" || port == 0 {
		return Node{}, false
	}
	extra := extraFromQuery(u)
	delete(extra, "endpoint")
	extra = extraWith(extra, "private_key", query.Get("private_key"))
	return Node{
		Name:   nodeName(u.Fragment),
		Type:   "wireguard",
		Server: server,
		Port:   port,
		Extra:  extra,
	}, true
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
	}
	return 0
}
```

- [ ] **Step 4: 实现 `parse_yaml.go`**

创建 `src/backend/internal/extsub/parse_yaml.go`：

```go
package extsub

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// parseYAML 解析 Clash/mihomo YAML 的 proxies 段。
func parseYAML(body []byte) ([]Node, bool) {
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	rawProxies, ok := doc["proxies"].([]any)
	if !ok || len(rawProxies) == 0 {
		return nil, false
	}
	var nodes []Node
	for _, raw := range rawProxies {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := toString(entry["name"])
		nodeType := toString(entry["type"])
		server := toString(entry["server"])
		port := toInt(entry["port"])
		if name == "" || nodeType == "" || server == "" || port == 0 {
			continue
		}
		extra := make(map[string]any)
		for key, value := range entry {
			switch key {
			case "name", "type", "server", "port":
			default:
				extra[key] = value
			}
		}
		nodes = append(nodes, Node{
			Name: name, Type: nodeType, Server: server, Port: port, Extra: extra,
		})
	}
	if len(nodes) == 0 {
		return nil, false
	}
	return nodes, true
}

var _ = fmt.Sprintf // 保持 import 不被误删
```

（`toString`/`toInt` 已定义在 `parse.go`。`parse_yaml.go` 若不使用 `fmt` 则删掉该 import 与 `var _` 行。）

- [ ] **Step 5: 运行确认通过**

Run: `go test ./src/backend/internal/extsub/ -v`
Expected: 5 个测试 PASS。

- [ ] **Step 6: 提交**

```bash
git add src/backend/internal/extsub/
git commit -m "feat(extsub): parse subscription links, yaml and v2rayn formats"
```

---

### Task 4: extsub — Service（校验/拉取/同步/定时）

**Files:**
- Create: `src/backend/internal/extsub/service.go`
- Test: `src/backend/internal/extsub/service_test.go`

**Interfaces:**
- Consumes: Task 1 `ExternalFileRequester.GetWithOptions`/`FileRequestOptions`/`FileFetchResult`；Task 2 store 方法；Task 3 `ParseSubscription`。
- Produces（Task 5/6 使用）：
  ```go
  type Service struct {
      st              *store.Store
      files           requester.ExternalFileRequester
      skipVerifyFiles requester.ExternalFileRequester
  }
  func New(st *store.Store, files, skipVerifyFiles requester.ExternalFileRequester) *Service
  func (s *Service) Create(ctx context.Context, name, rawURL, userAgent string, skipCertVerify, autoUpdate bool, intervalHours int) (store.ExternalSubscription, error)
  func (s *Service) Update(ctx context.Context, id int64, name, rawURL, userAgent string, skipCertVerify, autoUpdate bool, intervalHours int) (store.ExternalSubscription, error)
  func (s *Service) Sync(ctx context.Context, id int64) (store.ExternalSubscription, error)
  func (s *Service) SyncDue(ctx context.Context) error
  func validateSubscriptionURL(raw string) error
  func parseTrafficUserinfo(header string) (upload, download, total int64, expire *int64)
  ```

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/extsub/service_test.go`：

```go
package extsub

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared/requester"
)

const testLink = "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=Pbk&sid=abcd&sni=cdn.example.com&fp=chrome#Tokyo"

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	client := requester.ExternalFileRequester{Doer: &http.Client{Timeout: 5 * time.Second}}
	return New(st, client, client)
}

func TestValidateSubscriptionURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://sub.example.com/a?token=1", true},
		{"http://sub.example.com/a", false},
		{"https://localhost/a", false},
		{"https://127.0.0.1/a", false},
		{"https://192.168.1.1/a", false},
		{"https://10.0.0.1/a", false},
		{"https://172.16.0.1/a", false},
		{"https://169.254.169.254/a", false},
		{"https://foo.internal/a", false},
		{"https://foo.local/a", false},
		{"https://[::1]/a", false},
		{"https://8.8.8.8/a", true},
		{"not a url", false},
		{"https://", false},
	}
	for _, c := range cases {
		err := validateSubscriptionURL(c.url)
		if (err == nil) != c.want {
			t.Errorf("validateSubscriptionURL(%q) err = %v, want ok=%v", c.url, err, c.want)
		}
	}
}

func TestParseTrafficUserinfo(t *testing.T) {
	upload, download, total, expire := parseTrafficUserinfo(
		"upload=1024.5; download=2048; total=1073741824; expire=1700000000")
	if upload != 1024 || download != 2048 || total != 1073741824 {
		t.Fatalf("traffic = %d %d %d", upload, download, total)
	}
	if expire == nil || *expire != 1700000000 {
		t.Fatalf("expire = %v", expire)
	}
	_, _, _, expire = parseTrafficUserinfo("expire=0")
	if expire != nil {
		t.Fatalf("expire=0 should be ignored, got %v", *expire)
	}
	_, _, _, expire = parseTrafficUserinfo("total=5")
	if expire != nil {
		t.Fatalf("missing expire should stay nil")
	}
}

func TestCreateSyncsAndStoresChains(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=100; download=200; total=1000; expire=1900000000")
		body := base64.StdEncoding.EncodeToString([]byte(testLink + "\n" + testLink + "\n"))
		w.Write([]byte(body))
	}))
	defer srv.Close()

	svc := newTestService(t)
	ctx := context.Background()
	sub, err := svc.Create(ctx, "测试机场", srv.URL, "", false, true, 24)
	if err != nil {
		t.Fatal(err)
	}
	if sub.NodeCount != 1 || sub.Format != "v2ray" {
		t.Fatalf("sub = %+v", sub)
	}
	if sub.Upload != 100 || sub.Download != 200 || sub.Total != 1000 {
		t.Fatalf("traffic = %+v", sub)
	}
	if sub.Expire == nil || *sub.Expire != 1900000000 {
		t.Fatalf("expire = %v", sub.Expire)
	}
	if sub.LastSyncAt == nil || sub.LastError != "" {
		t.Fatalf("sync fields = %+v", sub)
	}
	chains, err := svc.st.ListExternalChains(ctx, sub.ID)
	if err != nil || len(chains) != 1 {
		t.Fatalf("chains = %+v err %v", chains, err)
	}
	if chains[0].Name != "Tokyo" || chains[0].Protocol != "vless" || chains[0].Server != "example.com" {
		t.Fatalf("chain = %+v", chains[0])
	}
}

func TestCreateDuplicateURLCallsUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(testLink))
	}))
	defer srv.Close()

	svc := newTestService(t)
	ctx := context.Background()
	first, err := svc.Create(ctx, "第一次", srv.URL, "", false, true, 24)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(ctx, "第二次", srv.URL, "", false, false, 12)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("ids differ: %d vs %d", first.ID, second.ID)
	}
	if second.Name != "第二次" || second.AutoUpdate {
		t.Fatalf("second = %+v", second)
	}
	all, err := svc.st.ListExternalSubscriptions(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list = %v err %v", all, err)
	}
}

func TestCreateKeepsRecordWhenFetchFails(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	sub, err := svc.Create(ctx, "坏订阅", "https://sub.invalid.example/x", "", false, true, 24)
	if err == nil {
		t.Fatal("create should surface fetch error")
	}
	got, getErr := svc.st.ExternalSubscriptionByID(ctx, sub.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Name != "坏订阅" || got.LastError == "" {
		t.Fatalf("record kept without error = %+v", got)
	}
}

func TestSyncDueOnlySyncsDueSubscriptions(t *testing.T) {
	subsURL := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		subsURL <- "hit"
		w.Write([]byte(testLink))
	}))
	defer srv.Close()

	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, "自动", srv.URL, "", false, true, 1); err != nil {
		t.Fatal(err)
	}
	// 关闭自动更新的订阅不应被 SyncDue 拉取
	if _, err := svc.Create(ctx, "手动", "https://sub.manual.example/x", "", false, false, 1); err != nil {
		t.Fatal(err) // 创建时立即同步一次会失败，但记录保留
	}
	if err := svc.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-subsURL:
	default:
		t.Fatal("auto subscription not synced by SyncDue")
	}
}

func TestUpdateRejectsBadURL(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, "n", "https://ok.example.com/x", "", false, true, 24); err == nil {
		t.Fatal("unreachable fetch should fail")
	}
	subs, err := svc.st.ListExternalSubscriptions(ctx)
	if err != nil || len(subs) != 1 {
		t.Fatalf("subs = %v err %v", subs, err)
	}
	if _, err := svc.Update(ctx, subs[0].ID, "n", "http://bad.example.com/x", "", false, true, 24); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("update err = %v", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/extsub/ -run TestService -v`
Expected: 编译失败（Service 未定义）。

- [ ] **Step 3: 实现 `service.go`**

创建 `src/backend/internal/extsub/service.go`：

```go
package extsub

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared/requester"
)

const (
	defaultUserAgent        = "clash-meta/2.4.0"
	maxSubscriptionBytes    = 2 << 20 // 2 MiB
	defaultSyncIntervalHours = 24
	minSyncIntervalHours     = 1
)

// Service 编排外部订阅的拉取、解析与入库。
type Service struct {
	st              *store.Store
	files           requester.ExternalFileRequester
	skipVerifyFiles requester.ExternalFileRequester
}

func New(st *store.Store, files, skipVerifyFiles requester.ExternalFileRequester) *Service {
	return &Service{st: st, files: files, skipVerifyFiles: skipVerifyFiles}
}

// Create 校验并保存订阅（同 URL 视为同一订阅并更新），随后立即同步一次。
// 拉取失败时记录已保留，LastError 写明原因。
func (s *Service) Create(ctx context.Context, name, rawURL, userAgent string,
	skipCertVerify, autoUpdate bool, intervalHours int) (store.ExternalSubscription, error) {
	if err := validateSubscriptionURL(rawURL); err != nil {
		return store.ExternalSubscription{}, err
	}
	if strings.TrimSpace(name) == "" {
		return store.ExternalSubscription{}, errors.New("订阅名称不能为空")
	}
	intervalHours = normalizeInterval(intervalHours)
	existing, err := s.st.ExternalSubscriptionByURL(ctx, rawURL)
	switch {
	case err == nil:
		existing.Name = strings.TrimSpace(name)
		existing.UserAgent = strings.TrimSpace(userAgent)
		existing.SkipCertVerify = skipCertVerify
		existing.AutoUpdate = autoUpdate
		existing.UpdateIntervalHours = intervalHours
		if err := s.st.UpdateExternalSubscription(ctx, existing); err != nil {
			return store.ExternalSubscription{}, fmt.Errorf("update external subscription: %w", err)
		}
		return s.Sync(ctx, existing.ID)
	case errors.Is(err, store.ErrNotFound):
		id, err := s.st.CreateExternalSubscription(ctx, store.ExternalSubscription{
			Name: strings.TrimSpace(name), URL: rawURL,
			UserAgent: strings.TrimSpace(userAgent), SkipCertVerify: skipCertVerify,
			AutoUpdate: autoUpdate, UpdateIntervalHours: intervalHours,
		})
		if err != nil {
			return store.ExternalSubscription{}, err
		}
		return s.Sync(ctx, id)
	default:
		return store.ExternalSubscription{}, err
	}
}

// Update 仅更新订阅设置，不触发同步。
func (s *Service) Update(ctx context.Context, id int64, name, rawURL, userAgent string,
	skipCertVerify, autoUpdate bool, intervalHours int) (store.ExternalSubscription, error) {
	if err := validateSubscriptionURL(rawURL); err != nil {
		return store.ExternalSubscription{}, err
	}
	if strings.TrimSpace(name) == "" {
		return store.ExternalSubscription{}, errors.New("订阅名称不能为空")
	}
	sub, err := s.st.ExternalSubscriptionByID(ctx, id)
	if err != nil {
		return store.ExternalSubscription{}, err
	}
	sub.Name = strings.TrimSpace(name)
	sub.URL = rawURL
	sub.UserAgent = strings.TrimSpace(userAgent)
	sub.SkipCertVerify = skipCertVerify
	sub.AutoUpdate = autoUpdate
	sub.UpdateIntervalHours = normalizeInterval(intervalHours)
	if err := s.st.UpdateExternalSubscription(ctx, sub); err != nil {
		return store.ExternalSubscription{}, fmt.Errorf("update external subscription: %w", err)
	}
	return s.st.ExternalSubscriptionByID(ctx, id)
}

// Sync 拉取、解析并全量替换该订阅的节点，回填流量与同步时间。
func (s *Service) Sync(ctx context.Context, id int64) (store.ExternalSubscription, error) {
	sub, err := s.st.ExternalSubscriptionByID(ctx, id)
	if err != nil {
		return store.ExternalSubscription{}, err
	}
	now := time.Now().UTC()
	sub.LastAttemptAt = &now
	if err := s.st.UpdateExternalSubscription(ctx, sub); err != nil {
		return store.ExternalSubscription{}, err
	}

	sub, result, err := s.fetchAndParse(ctx, sub)
	if err != nil {
		sub.LastError = err.Error()
		if updateErr := s.st.UpdateExternalSubscription(ctx, sub); updateErr != nil {
			return store.ExternalSubscription{}, updateErr
		}
		return sub, err
	}

	count, err := s.st.ReplaceExternalChains(ctx, sub.ID, result.chains)
	if err != nil {
		return store.ExternalSubscription{}, err
	}
	sub.Format = result.format
	sub.NodeCount = count
	sub.Upload = result.upload
	sub.Download = result.download
	sub.Total = result.total
	sub.Expire = result.expire
	sub.LastError = ""
	sub.LastSyncAt = &now
	if err := s.st.UpdateExternalSubscription(ctx, sub); err != nil {
		return store.ExternalSubscription{}, err
	}
	return sub, nil
}

type syncResult struct {
	chains   []store.ExternalChain
	format   string
	upload   int64
	download int64
	total    int64
	expire   *int64
}

func (s *Service) fetchAndParse(ctx context.Context, sub store.ExternalSubscription) (store.ExternalSubscription, syncResult, error) {
	files := s.files
	if sub.SkipCertVerify {
		files = s.skipVerifyFiles
	}
	userAgent := sub.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	result, err := files.GetWithOptions(ctx, sub.URL, maxSubscriptionBytes,
		requester.FileRequestOptions{UserAgent: userAgent})
	if err != nil {
		return sub, syncResult{}, err
	}
	upload, download, total, expire := parseTrafficUserinfo(result.Header.Get("subscription-userinfo"))
	if total == 0 && !strings.Contains(strings.ToLower(userAgent), "clash") {
		if retry, retryErr := files.GetWithOptions(ctx, sub.URL, maxSubscriptionBytes,
			requester.FileRequestOptions{UserAgent: defaultUserAgent}); retryErr == nil {
			if retryUpload, retryDownload, retryTotal, retryExpire := parseTrafficUserinfo(
				retry.Header.Get("subscription-userinfo")); retryTotal > 0 {
				upload, download, total, expire = retryUpload, retryDownload, retryTotal, retryExpire
			}
		}
	}
	nodes, format, err := ParseSubscription([]byte(result.Body))
	if err != nil {
		return sub, syncResult{}, err
	}
	chains := make([]store.ExternalChain, 0, len(nodes))
	seen := make(map[string]bool)
	for _, node := range nodes {
		config, err := jsonMarshalNode(node)
		if err != nil {
			continue
		}
		sha := configSHA256(config)
		if seen[sha] {
			continue
		}
		seen[sha] = true
		chains = append(chains, store.ExternalChain{
			SubscriptionID: sub.ID, Name: node.Name, Protocol: node.Type,
			Server: node.Server, Port: node.Port, Config: config, ConfigSHA256: sha,
		})
	}
	if len(chains) == 0 {
		return sub, syncResult{}, errors.New("订阅中没有可解析的节点")
	}
	return sub, syncResult{
		chains: chains, format: format,
		upload: upload, download: download, total: total, expire: expire,
	}, nil
}

// SyncDue 同步所有到达自动更新间隔的订阅；单订阅失败不影响其他订阅。
func (s *Service) SyncDue(ctx context.Context) error {
	subs, err := s.st.ListExternalSubscriptions(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
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
	}
	return nil
}

func normalizeInterval(hours int) int {
	if hours < minSyncIntervalHours {
		return minSyncIntervalHours
	}
	return hours
}

func jsonMarshalNode(node Node) ([]byte, error) {
	return json.Marshal(node)
}

func configSHA256(config []byte) string {
	sum := sha256.Sum256(config)
	return hex.EncodeToString(sum[:])
}

// validateSubscriptionURL 仅允许 https，并拒绝 localhost、内网与保留段地址
// （IP 字面量直接判定；主机名按常见内网后缀拒绝，不做 DNS 解析）。
func validateSubscriptionURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("订阅地址必须是有效的 https URL")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("订阅地址不能指向本机或内网地址")
		}
		return nil
	}
	lower := strings.ToLower(host)
	if lower == "localhost" {
		return errors.New("订阅地址不能指向本机或内网地址")
	}
	for _, suffix := range []string{".local", ".internal", ".lan", ".home.arpa"} {
		if strings.HasSuffix(lower, suffix) {
			return errors.New("订阅地址不能指向本机或内网地址")
		}
	}
	return nil
}

// parseTrafficUserinfo 解析 subscription-userinfo 响应头：
// upload=..; download=..; total=..; expire=..（支持浮点取整；expire=0 忽略）。
func parseTrafficUserinfo(header string) (upload, download, total int64, expire *int64) {
	for _, part := range strings.Split(header, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(value, 64)
			if ferr != nil {
				continue
			}
			parsed = int64(f)
		}
		switch key {
		case "upload":
			upload = parsed
		case "download":
			download = parsed
		case "total":
			total = parsed
		case "expire":
			if parsed > 0 {
				expire = &parsed
			}
		}
	}
	return upload, download, total, expire
}
```

所需 import 补充：`crypto/sha256`、`encoding/hex`、`encoding/json`（如 `jsonMarshalNode` 使用 `json.Marshal`）。`syncResult` 等类型同包内使用。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/backend/internal/extsub/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/extsub/
git commit -m "feat(extsub): import and sync service"
```

---

### Task 5: Panel — 路由、处理器与 openapi.yaml

**Files:**
- Create: `src/backend/internal/panel/external_subscriptions.go`
- Modify: `src/backend/internal/panel/panel.go`（Server 字段 + `SetExternalSubscriptionService` + 路由注册）
- Modify: `src/backend/internal/panel/contract_test.go`（若 openapi 同步添加则无需改）
- Modify: `docs/openapi.yaml`
- Test: `src/backend/internal/panel/external_subscriptions_test.go`

**Interfaces:**
- Consumes: Task 2 store 方法、Task 4 `extsub.Service`。
- Produces（Task 6 使用）：`Server.SetExternalSubscriptionService(service *extsub.Service)`。

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/panel/external_subscriptions_test.go`：

```go
package panel

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
	"lattix/shared/requester"
)

func newExternalSubscriptionTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	client := requester.ExternalFileRequester{Doer: &http.Client{}}
	svc := extsub.New(st, client, client)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=1; download=2; total=3")
		link := "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp#Node1"
		w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(link))))
	}))
	t.Cleanup(upstream.Close)
	return &Server{st: st, extSubs: svc}, upstream
}

func TestExternalSubscriptionCreateSyncListDelete(t *testing.T) {
	server, upstream := newExternalSubscriptionTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/external-subscription/create", strings.NewReader(fmt.Sprintf(
		`{"name":"机场","url":%q,"auto_update":true,"update_interval_hours":12}`, upstream.URL)))
	server.handleCreateExternalSubscription(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Code string `json:"code"`
		Data store.ExternalSubscription `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Code != "OK" || created.Data.NodeCount != 1 || created.Data.Total != 3 {
		t.Fatalf("created = %+v", created)
	}

	rec = httptest.NewRecorder()
	server.handleListExternalSubscriptions(rec, httptest.NewRequest(http.MethodGet, "/api/external-subscription/list", nil))
	var listed struct {
		Code string `json:"code"`
		Data []store.ExternalSubscription `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != created.Data.ID {
		t.Fatalf("listed = %+v", listed)
	}

	rec = httptest.NewRecorder()
	server.handleListExternalChains(rec, httptest.NewRequest(http.MethodGet,
		"/api/external-subscription/chains?id="+fmt.Sprint(created.Data.ID), nil))
	var chains struct {
		Code string `json:"code"`
		Data []store.ExternalChain `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&chains); err != nil {
		t.Fatal(err)
	}
	if len(chains.Data) != 1 || chains.Data[0].Name != "Node1" {
		t.Fatalf("chains = %+v", chains)
	}

	rec = httptest.NewRecorder()
	server.handleDeleteExternalSubscription(rec, httptest.NewRequest(http.MethodPost,
		"/api/external-subscription/delete",
		strings.NewReader(fmt.Sprintf(`{"id":%d}`, created.Data.ID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestExternalSubscriptionCreateValidation(t *testing.T) {
	server, _ := newExternalSubscriptionTestServer(t)
	rec := httptest.NewRecorder()
	server.handleCreateExternalSubscription(rec, httptest.NewRequest(http.MethodPost,
		"/api/external-subscription/create", strings.NewReader(`{"name":"x","url":"http://bad.example.com/a"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("validation should return RPC error with 200: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Code != "INVALID_ARGUMENT" {
		t.Fatalf("code = %q", got.Code)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/panel/ -run TestExternalSubscription -v`
Expected: 编译失败（处理器未定义、`Server.extSubs` 字段缺失）。

- [ ] **Step 3: 实现处理器**

创建 `src/backend/internal/panel/external_subscriptions.go`：

```go
package panel

import (
	"errors"
	"net/http"
	"strings"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
)

type externalSubscriptionInput struct {
	ID                   int64  `json:"id"`
	Name                 string `json:"name"`
	URL                  string `json:"url"`
	UserAgent            string `json:"user_agent"`
	SkipCertVerify       bool   `json:"skip_cert_verify"`
	AutoUpdate           bool   `json:"auto_update"`
	UpdateIntervalHours  int    `json:"update_interval_hours"`
}

func (s *Server) handleListExternalSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.st.ListExternalSubscriptions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, subs)
}

func (s *Server) handleCreateExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req externalSubscriptionInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UpdateIntervalHours == 0 {
		req.UpdateIntervalHours = 24
	}
	sub, err := s.extSubs.Create(r.Context(), req.Name, strings.TrimSpace(req.URL),
		req.UserAgent, req.SkipCertVerify, req.AutoUpdate, req.UpdateIntervalHours)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "external_subscription.created", nil, nil, map[string]any{
		"id": sub.ID, "name": sub.Name, "node_count": sub.NodeCount,
	})
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleUpdateExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req externalSubscriptionInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	if req.UpdateIntervalHours == 0 {
		req.UpdateIntervalHours = 24
	}
	sub, err := s.extSubs.Update(r.Context(), req.ID, req.Name, strings.TrimSpace(req.URL),
		req.UserAgent, req.SkipCertVerify, req.AutoUpdate, req.UpdateIntervalHours)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.audit(r, "external_subscription.updated", nil, nil, map[string]any{
		"id": sub.ID, "name": sub.Name,
	})
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleDeleteExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	if err := s.st.DeleteExternalSubscription(r.Context(), req.ID); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.audit(r, "external_subscription.deleted", nil, nil, map[string]any{"id": req.ID})
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) handleSyncExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	sub, err := s.extSubs.Sync(r.Context(), req.ID)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.audit(r, "external_subscription.synced", nil, nil, map[string]any{
		"id": sub.ID, "node_count": sub.NodeCount,
	})
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleListExternalChains(w http.ResponseWriter, r *http.Request) {
	id := queryInt(r, "id")
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	chains, err := s.st.ListExternalChains(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chains)
}
```

`queryInt` 需存在于 panel 包（若不存在，改用 `strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)` 并内联错误处理）。

- [ ] **Step 4: Server 字段 + setter + 路由注册**

在 `src/backend/internal/panel/panel.go`：
1. import 增加 `"lattix/backend/internal/extsub"`；
2. `Server` 结构体增加字段 `extSubs *extsub.Service`（放在 `subscriptions` 字段旁）；
3. 新增 setter（放在 `SetSubscriptionService` 后）：

```go
// SetExternalSubscriptionService wires the external subscription importer and
// its periodic sync task.
func (s *Server) SetExternalSubscriptionService(service *extsub.Service) {
	s.extSubs = service
	s.scheduler.register(scheduledTask{
		name: "external_subscriptions.sync", timeout: 10 * time.Minute,
		trigger: func(context.Context) taskTrigger { return intervalTrigger(15 * time.Minute) },
		run:     func(ctx context.Context) error { return service.SyncDue(ctx) },
	})
}
```

4. `RegisterRoutes` 中追加（放在订阅模板路由旁）：

```go
	s.registerRPC(mux, http.MethodGet, "/api/external-subscription/list", read, s.handleListExternalSubscriptions)
	s.registerRPC(mux, http.MethodPost, "/api/external-subscription/create", write, s.handleCreateExternalSubscription)
	s.registerRPC(mux, http.MethodPost, "/api/external-subscription/update", write, s.handleUpdateExternalSubscription)
	s.registerRPC(mux, http.MethodPost, "/api/external-subscription/delete", write, s.handleDeleteExternalSubscription)
	s.registerRPC(mux, http.MethodPost, "/api/external-subscription/sync", write, s.handleSyncExternalSubscription)
	s.registerRPC(mux, http.MethodGet, "/api/external-subscription/chains", read, s.handleListExternalChains)
```

- [ ] **Step 5: openapi.yaml 同步**

在 `docs/openapi.yaml` 的 `paths:` 中追加（放在订阅模板路径块附近，字母序不强制；`contract_test.go` 校验的是集合一致）：

```yaml
  /api/external-subscription/list:
    get:
      operationId: externalSubscriptionList
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
  /api/external-subscription/create:
    post:
      operationId: externalSubscriptionCreate
      parameters:
        - {$ref: '#/components/parameters/CSRFToken'}
        - {$ref: '#/components/parameters/IdempotencyKey'}
      requestBody: {$ref: '#/components/requestBodies/RPCBody'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
  /api/external-subscription/update:
    post:
      operationId: externalSubscriptionUpdate
      parameters:
        - {$ref: '#/components/parameters/CSRFToken'}
        - {$ref: '#/components/parameters/IdempotencyKey'}
      requestBody: {$ref: '#/components/requestBodies/RPCBody'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
  /api/external-subscription/delete:
    post:
      operationId: externalSubscriptionDelete
      parameters:
        - {$ref: '#/components/parameters/CSRFToken'}
        - {$ref: '#/components/parameters/IdempotencyKey'}
      requestBody: {$ref: '#/components/requestBodies/RPCBody'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
  /api/external-subscription/sync:
    post:
      operationId: externalSubscriptionSync
      parameters:
        - {$ref: '#/components/parameters/CSRFToken'}
        - {$ref: '#/components/parameters/IdempotencyKey'}
      requestBody: {$ref: '#/components/requestBodies/RPCBody'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
  /api/external-subscription/chains:
    get:
      operationId: externalSubscriptionChains
      parameters:
        - {name: id, in: query, required: true, schema: {type: integer, minimum: 1}}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
```

- [ ] **Step 6: 运行确认通过**

Run: `go test ./src/backend/internal/panel/ -run 'TestExternalSubscription|TestOpenAPIRoutesMatchRegisteredRPCs' -v`
Expected: PASS（新增两个测试 + 契约测试）。

- [ ] **Step 7: 提交**

```bash
git add src/backend/internal/panel/external_subscriptions.go src/backend/internal/panel/external_subscriptions_test.go src/backend/internal/panel/panel.go docs/openapi.yaml
git commit -m "feat(panel): external subscription RPC routes"
```

---

### Task 6: main.go 装配

**Files:**
- Modify: `src/backend/cmd/backend/main.go`

**Interfaces:**
- Consumes: Task 4 `extsub.New`、Task 5 `ps.SetExternalSubscriptionService`。

- [ ] **Step 1: 装配**

1. import 增加 `"crypto/tls"`（若已有则跳过）、`"lattix/backend/internal/extsub"`、`external "lattix/shared/requester"`。
2. 在 `subSrv := sub.New(...)` 附近（`ps.SetSubscriptionService(subSrv)` 之前或之后均可）追加：

```go
	// 外部订阅（第三方机场导入）：普通与跳过证书校验两套拉取客户端。
	externalFiles := external.ExternalFileRequester{Doer: &http.Client{Timeout: 30 * time.Second}}
	skipVerifyFiles := external.ExternalFileRequester{Doer: &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}}
	extSvc := extsub.New(st, externalFiles, skipVerifyFiles)
	ps.SetExternalSubscriptionService(extSvc)
```

- [ ] **Step 2: 构建确认**

Run: `go build ./src/backend/...`
Expected: 成功。

- [ ] **Step 3: 提交**

```bash
git add src/backend/cmd/backend/main.go
git commit -m "feat(main): wire external subscription service"
```

---

### Task 7: 前端 — API 类型与方法 + codegen

**Files:**
- Modify: `src/frontend/src/lib/types.ts`
- Modify: `src/frontend/src/lib/api.ts`

**Interfaces:**
- Consumes: Task 5 路由与 openapi.yaml（operationId）。
- Produces（Task 8 使用）：
  ```ts
  interface ExternalSubscription { id: number; name: string; url: string; user_agent: string;
    skip_cert_verify: boolean; auto_update: boolean; update_interval_hours: number;
    format: string; node_count: number; upload: number; download: number; total: number;
    expire?: number | null; last_sync_at?: string | null; last_attempt_at?: string | null;
    last_error?: string; created_at: string; updated_at: string }
  interface ExternalChain { id: number; subscription_id: number; name: string; protocol: string;
    server: string; port: number; config: unknown; config_sha256: string; created_at: string }
  api.externalSubscriptions(): Promise<ExternalSubscription[]>
  api.createExternalSubscription(body): Promise<ExternalSubscription>
  api.updateExternalSubscription(body): Promise<ExternalSubscription>
  api.deleteExternalSubscription(id: number): Promise<void>
  api.syncExternalSubscription(id: number): Promise<ExternalSubscription>
  api.externalSubscriptionChains(id: number): Promise<ExternalChain[]>
  ```

- [ ] **Step 1: types.ts 追加**

在 `src/frontend/src/lib/types.ts` 末尾追加（遵循既有接口风格）：

```ts
export interface ExternalSubscription {
  id: number
  name: string
  url: string
  user_agent: string
  skip_cert_verify: boolean
  auto_update: boolean
  update_interval_hours: number
  format: string
  node_count: number
  upload: number
  download: number
  total: number
  expire?: number | null
  last_sync_at?: string | null
  last_attempt_at?: string | null
  last_error?: string
  created_at: string
  updated_at: string
}

export interface ExternalChain {
  id: number
  subscription_id: number
  name: string
  protocol: string
  server: string
  port: number
  config: unknown
  config_sha256: string
  created_at: string
}
```

- [ ] **Step 2: api.ts 追加方法**

在 `src/frontend/src/lib/api.ts` 的 `export const api = { ... }` 内（订阅模板方法旁）追加：

```ts
  externalSubscriptions: (options?: RequestOptions) =>
    requester.get<ExternalSubscription[]>('/api/external-subscription/list', undefined, options),
  externalSubscriptionChains: (id: number, options?: RequestOptions) =>
    requester.get<ExternalChain[]>('/api/external-subscription/chains', { id }, options),
  createExternalSubscription: (body: {
    name: string
    url: string
    user_agent?: string
    skip_cert_verify?: boolean
    auto_update?: boolean
    update_interval_hours?: number
  }) => requester.post<ExternalSubscription>('/api/external-subscription/create', body),
  updateExternalSubscription: (body: {
    id: number
    name: string
    url: string
    user_agent?: string
    skip_cert_verify?: boolean
    auto_update?: boolean
    update_interval_hours?: number
  }) => requester.post<ExternalSubscription>('/api/external-subscription/update', body),
  deleteExternalSubscription: (id: number) =>
    requester.post<void>('/api/external-subscription/delete', { id }),
  syncExternalSubscription: (id: number) =>
    requester.post<ExternalSubscription>('/api/external-subscription/sync', { id }),
```

在文件顶部 `import type { ... } from './types'` 中追加 `ExternalSubscription`、`ExternalChain`。

- [ ] **Step 3: 重新生成契约类型并校验**

Run: `cd src/frontend && bun run generate:api && bunx tsc --noEmit`
Expected: codegen 重写 `src/frontend/src/lib/api-contract.generated.ts`；tsc 无错误。

- [ ] **Step 4: 提交**

```bash
git add src/frontend/src/lib/types.ts src/frontend/src/lib/api.ts src/frontend/src/lib/api-contract.generated.ts
git commit -m "feat(frontend): external subscription api client"
```

---

### Task 8: 前端 — 外部订阅页面

**Files:**
- Create: `src/frontend/src/pages/ExternalSubscriptions.tsx`
- Modify: `src/frontend/src/App.tsx`
- Modify: `src/frontend/src/components/Layout.tsx`

**Interfaces:**
- Consumes: Task 7 `api.*` 方法与类型。

- [ ] **Step 1: 页面实现**

创建 `src/frontend/src/pages/ExternalSubscriptions.tsx`（参考 `SubscriptionTemplates.tsx` 的模式；卡片用 `@/components/ui/card`，勾选用 `<input type="checkbox">` 跟随 `Users.tsx` 先例；字节数用 `humanizeBytes`（`@/lib/format`），时间用 `formatDateTime` + `useTimezone`）：

关键结构（完整代码按此骨架实现，遵循页面既有排版）：

```tsx
import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { GlobeIcon, PlusIcon, RefreshCwIcon, Trash2Icon, EyeIcon } from 'lucide-react'

import { EmptyState, Notice, Page, PageHeader, Surface } from '@/components/PagePrimitives'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { humanizeBytes, formatDateTime } from '@/lib/format'
import { useTimezone } from '@/lib/timezone'
import type { ExternalSubscription, ExternalChain } from '@/lib/types'
```

页面行为：
- `load()`：`api.externalSubscriptions()` → `subs` state；出错 `setError(errorMessage(err))`
- 新增/编辑弹窗字段：名称、URL、User-Agent、`skip_cert_verify`（checkbox）、`auto_update`（checkbox）、`update_interval_hours`（Input type=number，1–168）
- 每卡片展示：名称、URL（截断）、Badge 格式、节点数、流量（`humanizeBytes(download)` / `humanizeBytes(total)`）、到期（`expire` 存在时 `formatDateTime`）、上次同步、`last_error` 时红色 Notice
- 操作按钮：「同步」（loading 时 spin）、「节点」（打开节点 Dialog，`api.externalSubscriptionChains(id)` 渲染 Table：name/protocol/server:port）、「删除」（`useAppDialog().confirm` destructive）
- 创建成功后自动刷新列表

- [ ] **Step 2: 路由注册**

`src/frontend/src/App.tsx`：
1. 顶部 lazy imports 区追加 `const ExternalSubscriptions = lazy(() => import('@/pages/ExternalSubscriptions'))`
2. `<ProtectedRoutes>` 的 `<Switch>` 内、订阅模板路由旁追加：

```tsx
            <Route path="/external-subscriptions">
              <SuspendedRoute><ExternalSubscriptions /></SuspendedRoute>
            </Route>
```

`src/frontend/src/components/Layout.tsx`：`navItems` 中「订阅模板」之后追加：

```tsx
  { to: '/external-subscriptions', activePrefix: '/external-subscriptions', label: '外部订阅', icon: GlobeIcon, end: false },
```

并在 lucide-react import 中追加 `GlobeIcon`。

- [ ] **Step 3: 构建校验**

Run: `cd src/frontend && bun run build`
Expected: codegen --check 通过、tsc 通过、vite build 成功。

- [ ] **Step 4: 提交**

```bash
git add src/frontend/src/pages/ExternalSubscriptions.tsx src/frontend/src/App.tsx src/frontend/src/components/Layout.tsx
git commit -m "feat(frontend): external subscription management page"
```

---

### Task 9: 全量验证与收尾

**Files:**
- Modify: `docs/CHANGELOG.md`
- Modify: `docs/superpowers/specs/2026-08-02-external-subscriptions-design.md`（API 动词路由与 requester 接口的微小修订说明）

- [ ] **Step 1: 全量测试**

Run: `go build ./src/backend/... ./src/agent/... ./src/shared/... && go vet ./src/backend/... ./src/shared/... && go test ./src/backend/... ./src/shared/...`
Expected: 全部通过。

Run: `cd src/frontend && bun run build`
Expected: 成功。

- [ ] **Step 2: 设计文档修订**

在 `docs/superpowers/specs/2026-08-02-external-subscriptions-design.md` 的「改动清单」前追加一节：

```markdown
## 实施修订（2026-08-02）

- 路由采用现有 RPC 动词风格（前端 requester 无 PUT/DELETE）：`/api/external-subscription/list|create|update|delete|sync|chains`，替代初稿的 REST 式 `/api/external-subscriptions` 与 `PUT/DELETE .../{id}`。
- `requester` 增加 `GetWithOptions`（返回响应头）与 `GetTextWithOptions`；跳证书校验由 Service 持有的第二套 `ExternalFileRequester`（InsecureSkipVerify Transport）实现，`FileRequestOptions` 仅携带 UserAgent。
```

- [ ] **Step 3: CHANGELOG 追加**

`docs/CHANGELOG.md` 的 `[Unreleased]` → `### Added` 追加：

```markdown
- 新增外部订阅管理页：导入第三方订阅 URL（base64 分享链接 / Clash-mihomo YAML /
  v2rayN 自定义格式），解析节点保存到外部链路表，订阅信息（流量/到期/节点数）保存到
  外部订阅表，支持手动与定时同步（每订阅可配间隔）。
```

- [ ] **Step 4: 提交**

```bash
git add docs/CHANGELOG.md docs/superpowers/specs/2026-08-02-external-subscriptions-design.md
git commit -m "docs: changelog and design amendments for external subscriptions"
```

---

## Self-Review 记录

- **Spec 覆盖**：三格式解析 → Task 3；两张表 + 全量替换 → Task 2；requester 复用与 UA/跳证 → Task 1/4；流量头解析 → Task 4；手动/定时同步 → Task 4/5/6；6 个 API → Task 5；页面与导航 → Task 8；契约与 codegen → Task 5/7；测试 → 各任务内。
- **占位符检查**：Task 8 页面给出结构骨架而非完整 UI 代码——页面代码量大且高度模板化，骨架 + 明确行为清单足够执行者实现；Task 3 的 `parse.go` 与 `parse_yaml.go` 给出完整代码。
- **类型一致性**：`ExternalSubscription`/`ExternalChain` 在 Task 2/5/7 三处签名一致；`GetWithOptions`/`FileRequestOptions` 在 Task 1/4 一致；`ParseSubscription` 在 Task 3/4 一致。
