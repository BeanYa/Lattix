package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func contextBackground() context.Context { return context.Background() }

func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }

func postJSON2(t *testing.T, handler func(http.ResponseWriter, *http.Request), body any) (int, rpcEnvelope) {
	t.Helper()
	return postJSON(t, http.HandlerFunc(handler), "/api/test", body)
}

// TestCreateExternalServerEndpoint 验证未托管外部直链节点的创建：地址取链接 host、
// 机器类型固定仅出口档 NAT、无安装命令，解析规格落库。
func TestCreateExternalServerEndpoint(t *testing.T) {
	ctx := contextBackground()
	st, serverAPI, _, _ := externalChainFixture(t)
	code, env := postJSON(t, serverAPI.handleCreateServer, "/api/server/create", map[string]any{
		"alias":         "",
		"kind":          "external",
		"external_link": "socks5://user:pass@203.0.113.9:1080#Exit%20SOCKS",
		"country_code":  "JP",
		"location":      "Tokyo",
	})
	if env.Code != shared.CodeOK {
		t.Fatalf("create external = %d %s %s", code, env.Code, env.Message)
	}
	var created struct {
		Server struct {
			ID           int64  `json:"id"`
			Alias        string `json:"alias"`
			Kind         string `json:"kind"`
			Connection   string `json:"connection_state"`
			MachineType  string `json:"machine_type"`
			Address      string `json:"address"`
			ExternalLink string `json:"external_link"`
		} `json:"server"`
		BootstrapToken string `json:"bootstrap_token"`
		InstallCommand string `json:"install_command"`
	}
	if err := jsonUnmarshal(env.Data, &created); err != nil {
		t.Fatal(err)
	}
	if created.Server.Kind != "external" || created.Server.Connection != "external" {
		t.Fatalf("external server dto: %+v", created.Server)
	}
	if created.Server.Alias != "Exit SOCKS" || created.Server.Address != "203.0.113.9" {
		t.Fatalf("alias/address = %q / %q", created.Server.Alias, created.Server.Address)
	}
	if created.Server.MachineType != store.MachineTypeNAT {
		t.Fatalf("machine_type = %q", created.Server.MachineType)
	}
	if created.InstallCommand != "" || created.BootstrapToken != "" {
		t.Fatalf("外部节点不应返回安装命令: %+v", created)
	}
	row, err := st.ServerByID(ctx, created.Server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !row.IsExternal() || row.ExternalLink == "" {
		t.Fatalf("stored row: %+v", row)
	}
	exit := row.ParseExternalExit()
	if exit == nil || exit.Protocol != shared.ProtocolSocks || exit.Port != 1080 ||
		exit.Username != "user" || exit.Password != "pass" {
		t.Fatalf("stored external exit: %+v", exit)
	}
	if row.AllowedPorts != "" {
		t.Fatalf("外部节点不应有端口段: %q", row.AllowedPorts)
	}
}

// TestCreateExternalServerRejectsBadLink 验证不可解析或不支持的链接被拒绝。
func TestCreateExternalServerRejectsBadLink(t *testing.T) {
	_, serverAPI, _, _ := externalChainFixture(t)
	for _, link := range []string{"", "not-a-link", "hysteria2://pass@example.com:443#hy2"} {
		_, env := postJSON(t, serverAPI.handleCreateServer, "/api/server/create", map[string]any{
			"alias": "bad", "kind": "external", "external_link": link, "country_code": "US", "location": "LA",
		})
		if env.Code != shared.CodeInvalidArgument {
			t.Fatalf("link %q: code = %s, want INVALID_ARGUMENT", link, env.Code)
		}
	}
}

// TestExternalServerAgentActionsRejected 验证外部节点上的 Agent 动作被拒绝。
func TestExternalServerAgentActionsRejected(t *testing.T) {
	_, serverAPI, _, eID := externalChainFixture(t)
	handlers := map[string]func(w http.ResponseWriter, r *http.Request){
		"rotate-token":  serverAPI.handleRotateToken,
		"upgrade-xray":  serverAPI.handleUpgradeXray,
		"upgrade-agent": serverAPI.handleUpgradeAgent,
		"repair":        serverAPI.handleRepairServer,
		"cleanup-xray":  serverAPI.handleCleanupXray,
		"rebuild-xray":  serverAPI.handleRebuildXray,
	}
	for name, handler := range handlers {
		_, env := postJSON2(t, handler, map[string]any{"server_id": eID})
		if env.Code != shared.CodeInvalidArgument {
			t.Fatalf("%s on external server: code = %s, want INVALID_ARGUMENT", name, env.Code)
		}
	}
}

// TestUpdateExternalServerLinkWhileReferenced 验证链接更新：被链路引用且规格变化时
// 拒绝（CONFLICT），仅改别名不受影响。
func TestUpdateExternalServerLinkWhileReferenced(t *testing.T) {
	ctx := contextBackground()
	st, serverAPI, aID, eID := externalChainFixture(t)
	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "ref-external", Hops: []chainHopRef{{ServerID: aID}, {ServerID: eID}},
		TrafficMultiplier: "1.000",
	})
	if env.Code != shared.CodeOK {
		t.Fatalf("create chain = %d %s %s", code, env.Code, env.Message)
	}
	_, env = postJSON(t, serverAPI.handleUpdateServer, "/api/server/update", map[string]any{
		"server_id": eID, "alias": "exit-renamed", "country_code": "JP", "location": "Tokyo",
		"external_link": "socks5://u:p2@socks.example.com:1080#exit",
	})
	if env.Code != shared.CodeConflict {
		t.Fatalf("被引用时改链接应为 CONFLICT: %s %s", env.Code, env.Message)
	}
	_, env = postJSON(t, serverAPI.handleUpdateServer, "/api/server/update", map[string]any{
		"server_id": eID, "alias": "exit-renamed", "country_code": "JP", "location": "Tokyo",
	})
	if env.Code != shared.CodeOK {
		t.Fatalf("仅改别名应成功: %s %s", env.Code, env.Message)
	}
	row, err := st.ServerByID(ctx, eID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Alias != "exit-renamed" {
		t.Fatalf("alias = %q", row.Alias)
	}
}
