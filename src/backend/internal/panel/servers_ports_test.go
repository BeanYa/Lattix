package panel

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestCheckPortsShrinkRejectsOutOfRangeSharedEndpoint 验证端口段收窄校验覆盖共享端点
// （§21：收窄后存量使用越界拒绝——节点/链跳之外，端点端口同样受段内约束）。
func TestCheckPortsShrinkRejectsOutOfRangeSharedEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, "nat", "nat.example.com", "tok", store.MachineTypeNAT,
		`[{"pub_start":10000,"pub_end":10009}]`, "", "US", "")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, _, err := st.EnsureSharedEndpoint(ctx, serverID, shared.ProtocolVLESS, 10005,
		"profile-hash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st}
	req := httptest.NewRequest("POST", "/", nil)

	// 收窄到不含 10005 → 拒绝并定位到共享端点。
	err = srv.checkPortsShrink(req, serverID, []shared.PortRange{{PubStart: 10000, PubEnd: 10004}})
	if err == nil || !strings.Contains(err.Error(), "共享端点") ||
		!strings.Contains(err.Error(), "10005") {
		t.Fatalf("收窄应因共享端点越界被拒，实际 %v", err)
	}

	// 收窄后仍包含 10005 → 通过。
	if err := srv.checkPortsShrink(req, serverID, []shared.PortRange{{PubStart: 10000, PubEnd: 10005}}); err != nil {
		t.Fatalf("包含端点端口的收窄应通过: %v", err)
	}
	_ = endpoint
}
