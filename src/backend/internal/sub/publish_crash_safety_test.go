package sub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
)

// seedExternalNode 为用户直接挂载一个外部订阅节点（机场下发结构，含未消费键）。
func seedExternalNode(t *testing.T, st *store.Store, userID int64, extra map[string]any) {
	t.Helper()
	ctx := context.Background()
	subID, err := st.CreateExternalSubscription(ctx, store.ExternalSubscription{
		Name: "机场X", URL: "https://sub.example.com/x", UpdateIntervalHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(extsub.Node{Name: "🇭🇰 香港01", Type: "vless", Server: "proxy.example.com", Port: 443, Extra: extra})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceExternalChains(ctx, subID, []store.ExternalChain{
		{SubscriptionID: subID, Name: "hk01", Protocol: "vless", Server: "proxy.example.com", Port: 443, Config: config, ConfigSHA256: "hk01"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalSubscriptions(ctx, userID, []store.UserExternalSubscription{
		{UserID: userID, SubscriptionID: subID, Mode: store.ExtSubModeStack},
	}); err != nil {
		t.Fatal(err)
	}
}

// 线上事故回归：机场 REALITY 节点携带与 clashProxy 类型化字段同名的 Extra 键
// （fingerprint）时，yaml.v3 inline 编码冲突会 panic 整个进程。Raw 回填必须
// 排除类型化字段键，发布正常完成且不崩溃。
func TestExternalFingerprintKeyDoesNotCrashPublish(t *testing.T) {
	ctx := context.Background()
	st, userID := setupChainFixture(t, true)
	seedExternalNode(t, st, userID, map[string]any{
		"id":  "0cf3b2e0-1111-4d5e-8f6a-000000000001",
		"udp": true, "tls": true, "flow": "xtls-rprx-vision",
		"servername": "www.amazon.com", "client-fingerprint": "chrome",
		"reality-opts": map[string]any{"public-key": "pbk", "short-id": "sid"},
		"fingerprint":  "chrome", // 机场常见别名键，与类型化字段 Fingerprint 同名
	})
	srv := New(st, nil, nil)
	if _, err := srv.PublishUser(ctx, userID, "https://panel.example"); err != nil {
		t.Fatalf("publish with fingerprint-keyed external node failed: %v", err)
	}
	file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
	if err != nil {
		t.Fatal(err)
	}
	content := string(file.Content)
	if !strings.Contains(content, "香港01") {
		t.Fatalf("delivered clash config lost the external node:\n%s", content)
	}
	if !strings.Contains(content, "reality-opts") {
		t.Fatalf("delivered clash config lost reality-opts passthrough:\n%s", content)
	}
}

// 发布渲染 panic 必须转化为错误并落库（进程存活），面板与后台队列不再被坏数据带走。
// 通过临时移除保留键还原线上冲突路径，驱动真实的 panic 分支。
func TestPublishUserConvertsRenderPanicIntoError(t *testing.T) {
	ctx := context.Background()
	st, userID := setupChainFixture(t, true)
	seedExternalNode(t, st, userID, map[string]any{
		"id":  "0cf3b2e0-1111-4d5e-8f6a-000000000001",
		"tls": true, "servername": "www.amazon.com", "fingerprint": "chrome",
	})
	delete(clashProxyYamlKeys, "fingerprint")
	t.Cleanup(func() { clashProxyYamlKeys["fingerprint"] = true })

	srv := New(st, nil, nil)
	if _, err := srv.PublishUser(ctx, userID, "https://panel.example"); err == nil {
		t.Fatal("expected render panic to be converted into a publish error")
	} else if !strings.Contains(err.Error(), "发布渲染异常") {
		t.Fatalf("unexpected publish error: %v", err)
	}
	status, err := st.SubscriptionSnapshotStatus(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != store.SubscriptionGenerationError {
		t.Fatalf("generation error not persisted: %+v", status)
	}
}
