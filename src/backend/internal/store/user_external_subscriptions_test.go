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
	users, err := st.UsersByExternalSubscriptionID(ctx, subA)
	if err != nil || len(users) != 0 {
		t.Fatalf("users of deleted sub = %v err %v", users, err)
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
