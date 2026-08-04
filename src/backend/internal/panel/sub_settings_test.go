package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
	"lattix/shared"
)

func mustCreateUser(t *testing.T, st *store.Store, name string, expiresAt *time.Time) int64 {
	t.Helper()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	var body string
	if expiresAt != nil {
		body = fmt.Sprintf(`{"name":%q,"expires_at":%q}`, name, expiresAt.UTC().Format(time.RFC3339))
	} else {
		body = fmt.Sprintf(`{"name":%q}`, name)
	}
	rec := httptest.NewRecorder()
	server.handleCreateUser(rec, httptest.NewRequest(http.MethodPost, "/api/user/create", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("create user status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Data.ID
}

func subSettingsBody(userID int64, extra string) string {
	return fmt.Sprintf(`{"user_id":%d,"traffic_limit":0,"traffic_reset_day":0,"sub_title":"","sub_announcement":"","plan_name":"","app_url":""%s}`,
		userID, extra)
}

func TestUpdateUserSubSettingsSetsAndClearsExpiry(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	userID := mustCreateUser(t, st, "alice", nil)
	if u, _ := st.UserByID(ctx, userID); u.ExpiresAt != nil {
		t.Fatalf("user should start without expiry")
	}

	// 设置有效期：sub-settings 必须持久化 expires_at。
	future := time.Now().Add(90 * 24 * time.Hour).UTC()
	rec := httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost, "/api/user/sub-settings",
		strings.NewReader(subSettingsBody(userID, fmt.Sprintf(`,"expires_at":%q`, future.Format(time.RFC3339))))))
	if rec.Code != http.StatusOK {
		t.Fatalf("set expiry status = %d, body = %s", rec.Code, rec.Body.String())
	}
	u, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.ExpiresAt == nil || u.ExpiresAt.Unix() != future.Unix() {
		t.Fatalf("expires_at = %v, want %v", u.ExpiresAt, future)
	}

	// 清除有效期：null = 长期。
	rec = httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost, "/api/user/sub-settings",
		strings.NewReader(subSettingsBody(userID, `,"expires_at":null`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear expiry status = %d, body = %s", rec.Code, rec.Body.String())
	}
	u, err = st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.ExpiresAt != nil {
		t.Fatalf("expires_at = %v, want nil", u.ExpiresAt)
	}
}

func TestUpdateUserSubSettingsRejectsPastExpiry(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	userID := mustCreateUser(t, st, "alice", nil)
	past := time.Now().Add(-time.Hour).UTC()
	rec := httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost, "/api/user/sub-settings",
		strings.NewReader(subSettingsBody(userID, fmt.Sprintf(`,"expires_at":%q`, past.Format(time.RFC3339))))))
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeInvalidArgument {
		t.Fatalf("code = %q, want %q, body = %s", resp.Code, shared.CodeInvalidArgument, rec.Body.String())
	}
	if u, _ := st.UserByID(ctx, userID); u.ExpiresAt != nil {
		t.Fatalf("past expiry must not be persisted, got %v", u.ExpiresAt)
	}
}

func TestUpdateUserSubSettingsRestoresExpiredUser(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st, subscriptions: sub.New(st, nil, nil)}

	userID := mustCreateUser(t, st, "alice", nil)
	if err := st.SetUserExpired(ctx, userID, true); err != nil {
		t.Fatal(err)
	}

	future := time.Now().Add(30 * 24 * time.Hour).UTC()
	rec := httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost, "/api/user/sub-settings",
		strings.NewReader(subSettingsBody(userID, fmt.Sprintf(`,"expires_at":%q`, future.Format(time.RFC3339))))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	u, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Expired {
		t.Fatalf("user should be restored after expiry extension")
	}
	if u.ExpiresAt == nil || u.ExpiresAt.Unix() != future.Unix() {
		t.Fatalf("expires_at = %v, want %v", u.ExpiresAt, future)
	}
}

func TestUpdateUserSubSettingsMissingUser(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st}
	rec := httptest.NewRecorder()
	server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost, "/api/user/sub-settings",
		strings.NewReader(`{"user_id":9999,"expires_at":null}`)))
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeNotFound {
		t.Fatalf("code = %q, want %q", resp.Code, shared.CodeNotFound)
	}
}
