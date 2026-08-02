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
