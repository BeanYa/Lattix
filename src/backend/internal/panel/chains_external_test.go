package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// externalChainFixture 构建入口（托管 direct）与外部直链出口（未托管 socks）两行服务器。
func externalChainFixture(t *testing.T) (*store.Store, *Server, int64, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	aID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "entry-a", Address: "a.example.com",
		BootstrapToken: "token-a", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	exit := shared.ExternalExit{Protocol: shared.ProtocolSocks, Address: "socks.example.com", Port: 1080,
		Username: "u", Password: "p"}
	raw, err := json.Marshal(exit)
	if err != nil {
		t.Fatal(err)
	}
	eID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit-socks", Address: exit.Address,
		BootstrapToken: "token-e", MachineType: store.MachineTypeNAT, CountryCode: "JP",
		Kind: store.ServerKindExternal, ExternalLink: "socks5://u:p@socks.example.com:1080#exit",
		ExternalConfig: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	requester := &chainEditRequester{online: map[int64]bool{aID: true}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}), req: requester}
	return st, serverAPI, aID, eID
}

func postJSON(t *testing.T, handler http.HandlerFunc, path string, body any) (int, rpcEnvelope) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("POST", path, bytes.NewReader(raw)))
	return recorder.Code, decodeRPC(t, recorder)
}

// TestCreateChainExternalExit 验证外部直链出口链：入口端点必建、无出口业务节点、
// 修订只有 forward piece（无 service）、末位跳为外部服务器（node_id=0）。
func TestCreateChainExternalExit(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, eID := externalChainFixture(t)

	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "socks-exit", Hops: []chainHopRef{{ServerID: aID}, {ServerID: eID}},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create = %d %s %s", code, env.Code, string(env.Data))
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chain.ServiceNodeID != 0 {
		t.Fatalf("外部出口链不应有出口业务节点: %d", chain.ServiceNodeID)
	}
	if chain.EndpointID == 0 {
		t.Fatal("外部出口链应创建入口共享端点（客户端入口为 A 的 VLESS 端点）")
	}
	revision, err := st.ChainRevisionByID(ctx, chain.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if revision.Snapshot.ServiceExternal == nil || revision.Snapshot.ServiceExternal.Protocol != shared.ProtocolSocks {
		t.Fatalf("快照应冻结外部出口规格: %+v", revision.Snapshot.ServiceExternal)
	}
	for _, key := range revision.Snapshot.ApplyKeys {
		if strings.HasPrefix(key, dispatch.RevisionPieceService+"/") {
			t.Fatalf("外部出口链不应有 service apply 任务: %v", revision.Snapshot.ApplyKeys)
		}
	}
	if len(revision.Snapshot.Hops) != 2 {
		t.Fatalf("hops = %d", len(revision.Snapshot.Hops))
	}
	if revision.Snapshot.Hops[0].Transport != "direct" {
		t.Fatalf("入口跳 transport = %q, want direct", revision.Snapshot.Hops[0].Transport)
	}
	hops, err := st.ChainHops(ctx, chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 2 || hops[1].ServerID != eID || hops[1].NodeID != 0 {
		t.Fatalf("外部出口跳应无业务节点: %+v", hops)
	}
}

// TestCreateChainExternalMiddleRejected 验证未托管服务器只能作最后一跳。
func TestCreateChainExternalMiddleRejected(t *testing.T) {
	_, serverAPI, aID, eID := externalChainFixture(t)
	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "bad", Hops: []chainHopRef{{ServerID: eID}, {ServerID: aID}},
		Node:              createNodeRequest{Protocol: shared.ProtocolVLESS},
		TrafficMultiplier: "1.000",
	})
	if env.Code != shared.CodeInvalidArgument || code != http.StatusOK {
		t.Fatalf("external 入口应 INVALID_ARGUMENT: %d %s %s", code, env.Code, env.Message)
	}
	if !strings.Contains(env.Message, "最后一跳") {
		t.Fatalf("错误文案应说明只能作最后一跳: %q", env.Message)
	}
	// 单跳（仅外部节点）也应拒绝：外部节点不能作入口。
	code, env = postCreateChain(t, serverAPI, createChainRequest{
		Name: "only-external", Hops: []chainHopRef{{ServerID: eID}},
		TrafficMultiplier: "1.000",
	})
	if env.Code != shared.CodeInvalidArgument {
		t.Fatalf("单跳外部节点应 INVALID_ARGUMENT: %d %s %s", code, env.Code, env.Message)
	}
}

// TestCreateChainExternalThreeHops 验证多跳外部出口链：中间跳直连、末位托管跳接外部节点。
func TestCreateChainExternalThreeHops(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, eID := externalChainFixture(t)
	bID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "mid-b", Address: "b.example.com",
		BootstrapToken: "token-b", MachineType: store.MachineTypeDirect, CountryCode: "SG"})
	if err != nil {
		t.Fatal(err)
	}
	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "three-hops", Hops: []chainHopRef{{ServerID: aID}, {ServerID: bID}, {ServerID: eID}},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create = %d %s %s", code, env.Code, string(env.Data))
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := st.ChainRevisionByID(ctx, chain.DesiredRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	apply := map[string]bool{}
	for _, key := range revision.Snapshot.ApplyKeys {
		apply[key] = true
	}
	if !apply["forward/"+jsonInt(revision.Snapshot.Hops[0].HopID)] ||
		!apply["forward/"+jsonInt(revision.Snapshot.Hops[1].HopID)] {
		t.Fatalf("入口与中间跳应有 forward apply 任务: %v", revision.Snapshot.ApplyKeys)
	}
	for _, key := range revision.Snapshot.ApplyKeys {
		if strings.HasPrefix(key, dispatch.RevisionPieceService+"/") {
			t.Fatalf("外部出口链不应有 service 任务: %v", revision.Snapshot.ApplyKeys)
		}
	}
	if revision.Snapshot.Hops[1].Transport != "direct" {
		t.Fatalf("中间跳 → 外部节点应为 direct: %q", revision.Snapshot.Hops[1].Transport)
	}
}

func jsonInt(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

// TestParseExternalLinkHandler 验证解析器 API：返回预览且凭据打码。
func TestParseExternalLinkHandler(t *testing.T) {
	_, serverAPI, _, _ := externalChainFixture(t)
	code, env := postJSON(t, serverAPI.handleParseExternalLink, "/api/server/parse-link", map[string]string{
		"link": "vless://11111111-2222-3333-4444-555555555555@v.example.com:443?type=tcp&security=reality&pbk=pbk123&sid=abcd&sni=www.example.com#VLESS-Node",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("parse-link = %d %s %s", code, env.Code, string(env.Data))
	}
	var preview struct {
		Name      string              `json:"name"`
		Protocol  string              `json:"protocol"`
		Address   string              `json:"address"`
		Port      int                 `json:"port"`
		Transport string              `json:"transport"`
		Exit      shared.ExternalExit `json:"exit"`
	}
	if err := json.Unmarshal(env.Data, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Protocol != shared.ProtocolVLESS || preview.Address != "v.example.com" || preview.Port != 443 {
		t.Fatalf("preview: %+v", preview)
	}
	if preview.Name != "VLESS-Node" || preview.Transport != "tcp · reality" {
		t.Fatalf("preview name/transport: %+v", preview)
	}
	if preview.Exit.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("解析结果应回填真实凭据（服务器表单自动填写）: %q", preview.Exit.UUID)
	}
	code, env = postJSON(t, serverAPI.handleParseExternalLink, "/api/server/parse-link", map[string]string{
		"link": "hysteria2://pass@example.com:443#hy2",
	})
	if env.Code != shared.CodeInvalidArgument {
		t.Fatalf("不支持的协议应 INVALID_ARGUMENT: %d %s", code, env.Code)
	}
}

// TestCreateChainExternalExitEntryProtocolDetails 验证外部直链出口链的入口服务器 A 可像
// 普通链路一样配置完整 VLESS 协议细节（传输/安全层/证书/路径），并落到入口共享端点。
func TestCreateChainExternalExitEntryProtocolDetails(t *testing.T) {
	ctx := context.Background()
	st, serverAPI, aID, eID := externalChainFixture(t)
	code, env := postCreateChain(t, serverAPI, createChainRequest{
		Name: "external-entry-ws", Hops: []chainHopRef{{ServerID: aID}, {ServerID: eID}},
		EntryNode: &createNodeRequest{
			Protocol: shared.ProtocolVLESS, Network: shared.NetworkWS, Security: shared.SecurityTLS,
			CertMode: shared.CertModeSelfSign, Path: "/entry-ws", Host: "cdn.example.com",
			Fingerprint: shared.FingerprintChrome,
		},
		TrafficMultiplier: "1.000",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("create = %d %s %s", code, env.Code, env.Message)
	}
	var dto chainDTO
	if err := json.Unmarshal(env.Data, &dto); err != nil {
		t.Fatal(err)
	}
	chain, err := st.ChainByID(ctx, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := st.SharedEndpointByID(ctx, chain.EndpointID)
	if err != nil {
		t.Fatal(err)
	}
	var vc shared.VirtualConfig
	if err := json.Unmarshal(endpoint.ConfigTemplate, &vc); err != nil {
		t.Fatal(err)
	}
	if vc.Protocol != shared.ProtocolVLESS || vc.Network != shared.NetworkWS ||
		vc.Security != shared.SecurityTLS || vc.Path != "/entry-ws" || vc.Host != "cdn.example.com" {
		t.Fatalf("入口端点未采纳协议细节: %+v", vc)
	}
	var template struct {
		StreamSettings struct {
			Network    string `json:"network"`
			Security   string `json:"security"`
			WSSettings struct {
				Path string `json:"path"`
			} `json:"wsSettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(vc.Template, &template); err != nil {
		t.Fatal(err)
	}
	if template.StreamSettings.Network != "ws" || template.StreamSettings.Security != "tls" ||
		template.StreamSettings.WSSettings.Path != "/entry-ws" {
		t.Fatalf("入口端点模板未采纳协议细节: %s", string(vc.Template))
	}
	// Reality 与 ws 冲突（矩阵校验）：入口协议细节同样受限。
	code, env = postCreateChain(t, serverAPI, createChainRequest{
		Name: "external-entry-bad", Hops: []chainHopRef{{ServerID: aID}, {ServerID: eID}},
		EntryNode: &createNodeRequest{
			Protocol: shared.ProtocolVLESS, Network: shared.NetworkWS, Security: shared.SecurityReality,
		},
		TrafficMultiplier: "1.000",
	})
	if env.Code != shared.CodeInvalidArgument {
		t.Fatalf("reality+ws 应拒绝: %s %s", env.Code, env.Message)
	}
}

