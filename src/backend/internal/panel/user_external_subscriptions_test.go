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
