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
	"lattix/shared"
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
		Code string `json:"code"`
		Data struct {
			SubToken    string `json:"sub_token"`
			SubURL      string `json:"sub_url"`
			SubLinksURL string `json:"sub_links_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Code != shared.CodeOK {
		t.Fatalf("code = %q, body = %s", got.Code, rec.Body.String())
	}
	if got.Data.SubToken == "" || got.Data.SubToken == "old-token" {
		t.Fatalf("sub_token = %q", got.Data.SubToken)
	}
	if !strings.Contains(got.Data.SubURL, got.Data.SubToken) || !strings.Contains(got.Data.SubLinksURL, got.Data.SubToken) {
		t.Fatalf("urls do not contain new token: %s %s", got.Data.SubURL, got.Data.SubLinksURL)
	}
	if _, err := st.UserBySubToken(ctx, got.Data.SubToken); err != nil {
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
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeInvalidArgument {
		t.Fatalf("code = %q, want %q", resp.Code, shared.CodeInvalidArgument)
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
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeNotFound {
		t.Fatalf("code = %q, want %q", resp.Code, shared.CodeNotFound)
	}
}
