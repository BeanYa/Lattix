# 重置订阅地址（更换 sub_token）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 管理员一键更换用户 `sub_token` 生成全新订阅地址，旧链接立即失效，新链接立即可用。

**Architecture:** 后端新增 store 方法 `SetUserSubToken` 与面板 RPC `POST /api/user/reset-subscription-token`（Auth+CSRF+Idempotent），更换 token 后复用 `PublishUser` 立即重新发布全部格式；前端在用户列表操作列新增重置按钮（带确认框）。UUID 不变，不触发节点扇出。

**Tech Stack:** Go（net/http + mux、sqlite）、React 19 + Vite + Tailwind、lucide-react、OpenAPI（docs/openapi.yaml → 生成 api-contract.generated.ts）。

**Spec:** `docs/superpowers/specs/2026-08-02-reset-subscription-token-design.md`

## Global Constraints

- 新 token 一律 `randomHex(8)`（16 字符十六进制），与创建用户时一致。
- 重置只改 `sub_token`，绝不动 `uuid`（节点扇出依赖 UUID）。
- 路由注册与 `docs/openapi.yaml` 必须同任务落地（`TestOpenAPIRoutesMatchRegisteredRPCs` 校验两者一致）。
- RPC 选项：`rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}}`。
- audit 事件名：`user.subscription_token.reset`（只记 `user_id`，不记 token）。
- 后端测试：`cd src/backend && go test ./internal/store/ ./internal/panel/ ./internal/sub/`（需在 WSL 内运行，UNC 路径下 Go 不可用）。
- 前端验证：`cd src/frontend && npm run generate:api && npm run build && npm run lint`（build 内含 tsc 与契约 --check）。

---

### Task 1: Store 方法 `SetUserSubToken`

**Files:**
- Test: Create `src/backend/internal/store/users_test.go`
- Modify: `src/backend/internal/store/users.go`（在 `SetUserSubSettings` 之后，约 238 行）

**Interfaces:**
- Consumes: `store.Open(":memory:")`、`InsertUser(ctx, name, uuid, subToken, expiresAt)`、`UserBySubToken(ctx, token)`、`ErrNotFound`（均已存在）
- Produces: `func (s *Store) SetUserSubToken(ctx context.Context, id int64, token string) error` — 成功返回 nil；用户不存在返回 `ErrNotFound`

- [ ] **Step 1: 写失败测试**

创建 `src/backend/internal/store/users_test.go`（完整文件）：

```go
package store

import (
	"context"
	"errors"
	"testing"
)

func TestSetUserSubToken(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	userID, err := st.InsertUser(ctx, "user", "user-uuid", "old-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSubToken(ctx, userID, "new-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserBySubToken(ctx, "old-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token lookup err = %v, want ErrNotFound", err)
	}
	u, err := st.UserBySubToken(ctx, "new-token")
	if err != nil {
		t.Fatal(err)
	}
	if u.SubToken != "new-token" {
		t.Fatalf("sub token = %q, want new-token", u.SubToken)
	}
	if err := st.SetUserSubToken(ctx, 9999, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run（WSL 内）: `cd src/backend && go test ./internal/store/ -run TestSetUserSubToken -v`
Expected: FAIL，`undefined: st.SetUserSubToken`

- [ ] **Step 3: 实现方法**

在 `src/backend/internal/store/users.go` 的 `SetUserSubSettings` 之后追加：

```go
// SetUserSubToken 更换用户的订阅 token（§8）。
func (s *Store) SetUserSubToken(ctx context.Context, id int64, token string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET sub_token = ? WHERE id = ?`, token, id)
	if err != nil {
		return fmt.Errorf("set user sub token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run（WSL 内）: `cd src/backend && go test ./internal/store/ -run TestSetUserSubToken -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/store/users.go src/backend/internal/store/users_test.go
git commit -m "feat(store): add SetUserSubToken"
```

### Task 2: Panel 处理器 + 路由 + OpenAPI

**Files:**
- Test: Modify `src/backend/internal/panel/users_test.go`（现仅 16 行，整体替换）
- Modify: `src/backend/internal/panel/users.go`（在 `handleRegenerateUserSubscription` 之后，约 811 行）
- Modify: `src/backend/internal/panel/panel.go`（321 行 `handleRegenerateUserSubscription` 注册之后）
- Modify: `docs/openapi.yaml`（`/api/user/regenerate-subscription` 条目之后，约 442 行）
- Generate: `src/frontend/src/lib/api-contract.generated.ts`（`npm run generate:api`）

**Interfaces:**
- Consumes: `randomHex(8)`（panel 包已有）、`store.UserByID`、`store.UserBySubToken`、`store.SetUserSubToken`（Task 1）、`sub.Server.PublishUser`、`s.panelBase(r)`、`writeError`/`writeJSON`/`writeProtocolError`/`s.audit`（均已存在）
- Produces: 路由 `POST /api/user/reset-subscription-token`；响应 `{"sub_token", "sub_url", "sub_links_url"}`；audit `user.subscription_token.reset`

- [ ] **Step 1: 写失败测试**

整体替换 `src/backend/internal/panel/users_test.go` 为：

```go
package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

func TestValidateTrafficResetDay(t *testing.T) {
	for _, day := range []int{0, 1, 28, 29, 30, 31} {
		if err := validateTrafficResetDay(day); err != nil {
			t.Errorf("day %d rejected: %v", day, err)
		}
	}
	for _, day := range []int{-1, 32} {
		if err := validateTrafficResetDay(day); err == nil {
			t.Errorf("day %d accepted", day)
		}
	}
}

func TestResetUserSubscriptionToken(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "user", "user-uuid", "old-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	rec := httptest.NewRecorder()
	server.handleResetUserSubscriptionToken(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/reset-subscription-token", strings.NewReader(fmt.Sprintf(`{"user_id": %d}`, userID))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		SubToken    string `json:"sub_token"`
		SubURL      string `json:"sub_url"`
		SubLinksURL string `json:"sub_links_url"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.SubToken == "" || got.SubToken == "old-token" {
		t.Fatalf("sub_token = %q", got.SubToken)
	}
	if !strings.Contains(got.SubURL, got.SubToken) || !strings.Contains(got.SubLinksURL, got.SubToken) {
		t.Fatalf("urls do not contain new token: %s %s", got.SubURL, got.SubLinksURL)
	}
	if _, err := st.UserBySubToken(ctx, got.SubToken); err != nil {
		t.Fatalf("new token not persisted: %v", err)
	}
	if _, err := st.UserBySubToken(ctx, "old-token"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old token still resolves: %v", err)
	}
}

func TestResetUserSubscriptionTokenRejectsInvalidUserID(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st}
	rec := httptest.NewRecorder()
	server.handleResetUserSubscriptionToken(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/reset-subscription-token", strings.NewReader(`{"user_id": 0}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestResetUserSubscriptionTokenMissingUser(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	rec := httptest.NewRecorder()
	server.handleResetUserSubscriptionToken(rec, httptest.NewRequest(http.MethodPost,
		"/api/user/reset-subscription-token", strings.NewReader(`{"user_id": 9999}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run（WSL 内）: `cd src/backend && go test ./internal/panel/ -run TestResetUserSubscriptionToken -v`
Expected: FAIL，`undefined: s.handleResetUserSubscriptionToken`

- [ ] **Step 3: 实现处理器**

在 `src/backend/internal/panel/users.go` 的 `handleRegenerateUserSubscription` 之后追加：

```go
// handleResetUserSubscriptionToken 处理 POST /api/user/reset-subscription-token：
// 更换 sub_token 生成全新订阅地址（旧链接立即失效），并重新发布全部格式。
// UUID 不变，不触发节点扇出（§7/§8）。
func (s *Server) handleResetUserSubscriptionToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID <= 0 || s.subscriptions == nil {
		writeError(w, http.StatusBadRequest, "invalid user id or subscription service unavailable")
		return
	}
	if _, err := s.st.UserByID(r.Context(), req.UserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var token string
	for i := 0; i < 5; i++ {
		candidate := randomHex(8)
		if _, err := s.st.UserBySubToken(r.Context(), candidate); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			token = candidate
			break
		}
	}
	if token == "" {
		writeError(w, http.StatusInternalServerError, "failed to generate unique subscription token")
		return
	}
	if err := s.st.SetUserSubToken(r.Context(), req.UserID, token); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	base := s.panelBase(r)
	if _, err := s.subscriptions.PublishUser(r.Context(), req.UserID, base); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "user.subscription_token.reset", nil, nil, map[string]any{
		"user_id": req.UserID,
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"sub_token":     token,
		"sub_url":       fmt.Sprintf("%s/sub/%s", base, token),
		"sub_links_url": fmt.Sprintf("%s/sub/%s?format=links", base, token),
	})
}
```

（users.go 已 import `errors`、`fmt`、`store`，无需新增。）

- [ ] **Step 4: 注册路由**

在 `src/backend/internal/panel/panel.go` 的 `handleRegenerateUserSubscription` 注册之后追加：

```go
	s.registerRPC(mux, http.MethodPost, "/api/user/reset-subscription-token",
		rpcRouteOptions{Auth: true, CSRF: true, Idempotent: true, SafeBodyFields: []string{"user_id"}},
		s.handleResetUserSubscriptionToken)
```

- [ ] **Step 5: 更新 OpenAPI 并重新生成契约**

在 `docs/openapi.yaml` 的 `/api/user/regenerate-subscription` 条目之后追加：

```yaml
  /api/user/reset-subscription-token:
    post:
      operationId: userResetSubscriptionToken
      parameters:
        - {$ref: '#/components/parameters/CSRFToken'}
        - {$ref: '#/components/parameters/IdempotencyKey'}
      requestBody: {$ref: '#/components/requestBodies/RPCBody'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
```

Run（在仓库根目录）: `cd src/frontend && npm run generate:api`
Expected: `src/frontend/src/lib/api-contract.generated.ts` 出现 `userResetSubscriptionToken` 条目

- [ ] **Step 6: 运行全部后端测试确认通过**

Run（WSL 内）: `cd src/backend && go test ./internal/store/ ./internal/panel/ ./internal/sub/`
Expected: 全部 ok（含 `TestOpenAPIRoutesMatchRegisteredRPCs` 契约校验）

- [ ] **Step 7: 提交**

```bash
git add src/backend/internal/panel/users.go src/backend/internal/panel/users_test.go src/backend/internal/panel/panel.go docs/openapi.yaml src/frontend/src/lib/api-contract.generated.ts
git commit -m "feat(panel): add reset subscription token RPC"
```

### Task 3: 前端重置按钮

**Files:**
- Modify: `src/frontend/src/lib/api.ts`（`regenerateUserSubscription` 之后，约 275 行）
- Modify: `src/frontend/src/pages/Users.tsx`

**Interfaces:**
- Consumes: Task 2 的 `POST /api/user/reset-subscription-token`、`useAppDialog().confirm`、`errorMessage`、`api.users`（均已存在）
- Produces: `api.resetUserSubscriptionToken(userId)`；操作列「重置订阅地址」按钮（KeyRound 图标）

- [ ] **Step 1: 添加 API 方法**

在 `src/frontend/src/lib/api.ts` 的 `regenerateUserSubscription` 之后追加：

```ts
  resetUserSubscriptionToken: (userId: number) =>
    requester.post<{ sub_token: string; sub_url: string; sub_links_url: string }>(
      '/api/user/reset-subscription-token', { user_id: userId }),
```

- [ ] **Step 2: 添加状态与处理函数**

在 `src/frontend/src/pages/Users.tsx`：

1. 在 `regenerating` state（178 行）之后追加：

```ts
  const [resettingToken, setResettingToken] = useState<number | null>(null)
```

2. 在 `onRegenerate`（417 行）之后追加：

```ts
  const onResetToken = async (user: SubUser) => {
    if (!(await confirm({
      title: '重置订阅地址',
      description: `确认重置「${user.name}」的订阅地址？新地址将立即生效，旧链接立即失效，客户端需要重新导入新链接。`,
      confirmLabel: '重置订阅地址',
      destructive: true,
    }))) {
      return
    }
    setResettingToken(user.id)
    setError('')
    try {
      await api.resetUserSubscriptionToken(user.id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setResettingToken(null)
    }
  }
```

3. 在 lucide-react import 列表（2-15 行）中追加 `KeyRoundIcon`：

```ts
  KeyRoundIcon,
```

4. 在「重新生成全部订阅格式」按钮（556-564 行）之后追加：

```tsx
                    <Button
                      variant="outline"
                      size="sm"
                      title="重置订阅地址（更换链接，旧链接立即失效）"
                      disabled={resettingToken === u.id}
                      onClick={() => onResetToken(u)}
                    >
                      <KeyRoundIcon className={resettingToken === u.id ? 'animate-spin' : undefined} />
                    </Button>
```

- [ ] **Step 3: 验证构建与 lint**

Run: `cd src/frontend && npm run build && npm run lint`
Expected: 构建成功、无 lint 错误

- [ ] **Step 4: 提交**

```bash
git add src/frontend/src/lib/api.ts src/frontend/src/pages/Users.tsx
git commit -m "feat(frontend): add reset subscription token button"
```

### Task 4: 全量回归验证

- [ ] **Step 1: 后端全量测试**

Run（WSL 内）: `cd src/backend && go build ./... && go test ./...`
Expected: 全部 ok

- [ ] **Step 2: 前端全量验证**

Run: `cd src/frontend && npm run build && npm run lint && npm run test`
Expected: 全部通过（build 含契约 --check）

- [ ] **Step 3: 手工核对**

- `GET /sub/{旧token}` → 404（重置后旧链接失效）。
- `GET /sub/{新token}` → 正常返回订阅内容。
- 前端「重置订阅地址」确认框文案、成功刷新后新链接显示、二维码/复制按钮使用新链接。
