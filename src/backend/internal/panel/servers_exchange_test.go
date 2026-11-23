package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/shared"
)

type exchangeTestForgotCall struct {
	serverID int64
	code     int
	reason   string
}

type exchangeTestSentCall struct {
	serverID int64
	msgType  string
}

type exchangeTestRequester struct {
	online map[int64]bool
	sent   []exchangeTestSentCall
	forgot []exchangeTestForgotCall
}

func (r *exchangeTestRequester) Send(_ context.Context, serverID int64, env shared.Envelope) error {
	r.sent = append(r.sent, exchangeTestSentCall{serverID: serverID, msgType: env.Type})
	return nil
}

func (r *exchangeTestRequester) IsOnline(serverID int64) bool {
	return r.online[serverID]
}

func (r *exchangeTestRequester) ForgetAgent(serverID int64, code int, reason string) {
	r.forgot = append(r.forgot, exchangeTestForgotCall{serverID: serverID, code: code, reason: reason})
}

func newExchangePanelFixture(t *testing.T) (*store.Store, *Server, *dispatch.Dispatcher, *exchangeTestRequester) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	req := &exchangeTestRequester{online: map[int64]bool{}}
	disp := dispatch.New(st, req, dispatch.Options{}, dispatch.Events{})
	srvAPI := &Server{st: st, disp: disp, req: req}
	return st, srvAPI, disp, req
}

func postUpdateServer(t *testing.T, s *Server, body map[string]any) (int, rpcEnvelope, serverUpdateResponse) {
	t.Helper()
	code, env := postJSON(t, s.handleUpdateServer, "/api/server/update", body)
	var out serverUpdateResponse
	if len(env.Data) > 0 && string(env.Data) != "null" {
		_ = json.Unmarshal(env.Data, &out)
	}
	return code, env, out
}

func buildActiveDirectTwoHopChain(
	t *testing.T,
	ctx context.Context,
	st *store.Store,
	name string,
	aID, bID int64,
	entryPort, exitPort int,
) (chainID int64, endpointID int64, entryHopID int64, exitHopID int64) {
	t.Helper()
	epReq := createNodeRequest{
		Name: name + "-ep", ServerID: aID, Protocol: shared.ProtocolVLESS,
		Port: &entryPort, ShortID: "0123456789abcdef", Dest: "dl.google.com:443",
		ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
		Network: shared.NetworkTCP, Flow: shared.FlowVision,
	}
	if err := epReq.normalize(); err != nil {
		t.Fatalf("epReq normalize: %v", err)
	}
	epCfg := buildVirtualConfig(epReq)
	epCfgJSON, _ := json.Marshal(epCfg)
	ep, _, err := st.EnsureSharedEndpoint(ctx, aID, shared.ProtocolVLESS, entryPort, "default", epCfgJSON)
	if err != nil {
		t.Fatalf("EnsureSharedEndpoint: %v", err)
	}
	epRealized, _ := json.Marshal(shared.RealizedConfig{
		Port: entryPort, PublicKey: "entry-pbk", ShortID: "0123456789abcdef",
		ServerName: "dl.google.com", Network: shared.NetworkTCP, Security: shared.SecurityReality,
	})
	_ = st.SetSharedEndpointActive(ctx, ep.ID, epRealized)

	svcReq := createNodeRequest{
		Name: name, ServerID: bID, Protocol: shared.ProtocolVLESS,
		Port: &exitPort, ShortID: "abcdef0123456789", Dest: "dl.google.com:443",
		ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
		Network: shared.NetworkTCP, Flow: shared.FlowVision,
	}
	if err := svcReq.normalize(); err != nil {
		t.Fatalf("svcReq normalize: %v", err)
	}
	svcVC := buildVirtualConfig(svcReq)
	svcCfgJSON, _ := json.Marshal(svcVC)
	svcRealizedJSON, _ := json.Marshal(shared.RealizedConfig{
		Port: exitPort, PublicKey: "exit-pbk", ShortID: svcReq.ShortID,
		ServerName: "dl.google.com", Network: shared.NetworkTCP,
		Flow: shared.FlowVision, Fingerprint: shared.FingerprintChrome,
	})

	dep, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
		Name:                   name,
		ServiceServerID:        bID,
		ServiceProtocol:        shared.ProtocolVLESS,
		ServicePort:            &exitPort,
		ServiceConfig:          svcCfgJSON,
		EndpointID:             ep.ID,
		ServiceUUID:            "11111111-1111-4111-8111-111111111111",
		TrafficMultiplierMilli: 1000,
		Hops: []store.InitialChainHop{
			{ServerID: aID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: entryPort},
			{ServerID: bID, Role: store.HopRoleExit, Transport: "direct", ForwardPort: exitPort},
		},
	})
	if err != nil {
		t.Fatalf("CreateInitialChainDeployment: %v", err)
	}
	if err := st.SetNodeActive(ctx, dep.NodeID, svcRealizedJSON); err != nil {
		t.Fatalf("SetNodeActive: %v", err)
	}
	for _, h := range dep.Hops {
		if err := st.SetChainHopStatus(ctx, h.HopID, store.HopStatusActive, ""); err != nil {
			t.Fatalf("SetChainHopStatus: %v", err)
		}
	}
	rev, err := st.ChainRevisionByID(ctx, dep.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	rev.Snapshot.ServiceRealized = svcRealizedJSON
	if err := st.UpdateChainRevision(ctx, dep.RevisionID, store.RevisionStatusApplying, "", rev.Snapshot); err != nil {
		t.Fatalf("UpdateChainRevision: %v", err)
	}
	if err := st.PublishChainRevision(ctx, dep.RevisionID, false); err != nil {
		t.Fatalf("PublishChainRevision: %v", err)
	}
	return dep.ChainID, ep.ID, dep.Hops[0].HopID, dep.Hops[1].HopID
}

// TestServerConversionAllSixUnusedDirections 覆盖 direct / nat / external 三类服务器全部 6 种互转路径，
// 并验证 Agent 凭证轮换/吊销、ForgetAgent、AuthenticateToken 拒绝与 BanWatch/Command 清理。
func TestServerConversionAllSixUnusedDirections(t *testing.T) {
	ctx := context.Background()
	st, s, disp, req := newExchangePanelFixture(t)

	panelID, err := st.PanelInstanceID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cred0, err := shared.NewCredential(panelID, 1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.CreateServer(ctx, store.ServerDraft{
		Alias: "srv-conv", Address: "1.1.1.1", BootstrapToken: cred0,
		MachineType: store.MachineTypeDirect, CountryCode: "US", Location: "LA",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 注入 BanWatch 与 queued command，用于验证 managed → external 时自动清理/废弃
	if err := st.UpsertServerBanWatch(ctx, store.ServerBanWatch{
		ServerID: id, LossStreak: 5, State: store.BanStateSuspected, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueCommand(ctx, "req-1", "trace-1", id, "node.apply", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	req.online[id] = true

	// 1. direct → nat
	code, env, out := postUpdateServer(t, s, map[string]any{
		"server_id":     id,
		"kind":          "managed",
		"machine_type":  "nat",
		"alias":         "srv-conv",
		"address":       "2.2.2.2",
		"addresses":     []string{"2.2.2.2"},
		"country_code":  "US",
		"location":      "LA",
		"allowed_ports": []shared.PortRange{{PubStart: 10000, PubEnd: 10010}},
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("1. direct→nat failed: %d %s %s", code, env.Code, env.Message)
	}
	if out.Kind != store.ServerKindManaged || out.MachineType != store.MachineTypeNAT || len(out.AllowedPorts) != 1 {
		t.Fatalf("1. direct→nat unexpected dto: %+v", out)
	}

	// 2. nat → external
	forgotBefore := len(req.forgot)
	code, env, out = postUpdateServer(t, s, map[string]any{
		"server_id":    id,
		"kind":         "external",
		"alias":        "srv-ext",
		"country_code": "JP",
		"location":     "Tokyo",
		"external_exit": shared.ExternalExit{
			Protocol: shared.ProtocolSocks, Address: "ext.example.com", Port: 1080,
		},
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("2. nat→external failed: %d %s %s", code, env.Code, env.Message)
	}
	if out.Kind != store.ServerKindExternal || len(out.AllowedPorts) != 0 {
		t.Fatalf("2. nat→external unexpected dto: kind=%s ports=%v", out.Kind, out.AllowedPorts)
	}
	if len(req.forgot) != forgotBefore+1 || req.forgot[len(req.forgot)-1].serverID != id {
		t.Fatalf("2. expected ForgetAgent to be called on managed→external, got %+v", req.forgot)
	}
	if _, authErr := disp.AuthenticateToken(ctx, cred0); authErr == nil {
		t.Fatal("2. old credential must fail AuthenticateToken after converting to external")
	}
	srvRow, _ := st.ServerByID(ctx, id)
	if _, authErr := disp.AuthenticateToken(ctx, srvRow.Token); authErr == nil {
		t.Fatal("2. external server must reject AuthenticateToken even with current token")
	}
	banMap, _ := st.ServerBanWatchMap(ctx)
	if _, exists := banMap[id]; exists {
		t.Fatal("2. server_ban_watch should be deleted after managed→external")
	}
	cmds, _ := st.RecentCommands(ctx, id, 10)
	if len(cmds) == 0 || cmds[0].Status != store.CommandStatusAbandoned {
		t.Fatalf("2. queued command should be abandoned, got %+v", cmds)
	}

	// 3. external → nat
	code, env, out = postUpdateServer(t, s, map[string]any{
		"server_id":     id,
		"kind":          "managed",
		"machine_type":  "nat",
		"alias":         "srv-nat-again",
		"address":       "3.3.3.3",
		"addresses":     []string{"3.3.3.3"},
		"country_code":  "US",
		"location":      "LA",
		"allowed_ports": []shared.PortRange{{PubStart: 20000, PubEnd: 20005}},
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("3. external→nat failed: %d %s %s", code, env.Code, env.Message)
	}
	if out.BootstrapToken == "" || out.InstallCommand == "" {
		t.Fatalf("3. external→nat should return bootstrap_token and install_command, got token=%q cmd=%q", out.BootstrapToken, out.InstallCommand)
	}
	if out.Kind != store.ServerKindManaged || out.MachineType != store.MachineTypeNAT {
		t.Fatalf("3. external→nat unexpected dto: kind=%s machine=%s", out.Kind, out.MachineType)
	}
	if _, authErr := disp.AuthenticateToken(ctx, out.BootstrapToken); authErr != nil {
		t.Fatalf("3. new bootstrap token should authenticate on managed server: %v", authErr)
	}
	srvRow, _ = st.ServerByID(ctx, id)
	if srvRow.ExternalLink != "" || srvRow.ExternalConfig != "" {
		t.Fatalf("3. external fields should be cleared, got link=%q cfg=%q", srvRow.ExternalLink, srvRow.ExternalConfig)
	}

	// 4. nat → direct
	code, env, out = postUpdateServer(t, s, map[string]any{
		"server_id":    id,
		"kind":         "managed",
		"machine_type": "direct",
		"alias":        "srv-direct-again",
		"address":      "4.4.4.4",
		"addresses":    []string{"4.4.4.4"},
		"country_code": "US",
		"location":     "LA",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("4. nat→direct failed: %d %s %s", code, env.Code, env.Message)
	}
	if out.MachineType != store.MachineTypeDirect || len(out.AllowedPorts) != 0 {
		t.Fatalf("4. nat→direct unexpected dto: machine=%s ports=%v", out.MachineType, out.AllowedPorts)
	}

	// 5. direct → external
	credDirect := srvRow.Token
	code, env, out = postUpdateServer(t, s, map[string]any{
		"server_id":    id,
		"kind":         "external",
		"alias":        "srv-ext-2",
		"country_code": "",
		"location":     "",
		"external_exit": shared.ExternalExit{
			Protocol: shared.ProtocolHTTP, Address: "http.example.com", Port: 8080,
		},
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("5. direct→external failed: %d %s %s", code, env.Code, env.Message)
	}
	if _, authErr := disp.AuthenticateToken(ctx, credDirect); authErr == nil {
		t.Fatal("5. old direct credential must be rejected after direct→external")
	}

	// 6. external → direct
	code, env, out = postUpdateServer(t, s, map[string]any{
		"server_id":    id,
		"kind":         "managed",
		"machine_type": "direct",
		"alias":        "srv-direct-final",
		"address":      "",
		"country_code": "DE",
		"location":     "Frankfurt",
	})
	if code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("6. external→direct failed: %d %s %s", code, env.Code, env.Message)
	}
	if out.BootstrapToken == "" || out.InstallCommand == "" || out.MachineType != store.MachineTypeDirect {
		t.Fatalf("6. external→direct unexpected response: %+v", out)
	}
	if _, authErr := disp.AuthenticateToken(ctx, out.BootstrapToken); authErr != nil {
		t.Fatalf("6. new bootstrap token after external→direct failed auth: %v", authErr)
	}
}

// TestTryExchangeDirectToNATReallocatesPortsAndTransport 验证托管类型转换及 NAT 端口段变化触发 TryExchange：
// 非 1:1 NAT 出口重分跳端口/重下发 service；入口收窄重分 SharedEndpoint；出口转仅出口 NAT 切换 reverse 隧道及恢复 direct；Agent 离线进入 waiting_for_agent。
func TestTryExchangeDirectToNATReallocatesPortsAndTransport(t *testing.T) {
	ctx := context.Background()

	t.Run("exit_nat_non_1to1_reallocates_hop_and_service", func(t *testing.T) {
		st, s, _, req := newExchangePanelFixture(t)
		aID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "a", Address: "a.test", BootstrapToken: "ta", MachineType: store.MachineTypeDirect, CountryCode: "US"})
		bID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "b", Address: "b.test", BootstrapToken: "tb", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
		req.online[aID], req.online[bID] = true, true

		chainID, _, _, _ := buildActiveDirectTwoHopChain(t, ctx, st, "c1", aID, bID, 8443, 3000)

		code, env, out := postUpdateServer(t, s, map[string]any{
			"server_id":     bID,
			"kind":          "managed",
			"machine_type":  "nat",
			"alias":         "b",
			"address":       "b.test",
			"addresses":     []string{"b.test"},
			"country_code":  "JP",
			"location":      "",
			"allowed_ports": []shared.PortRange{{PubStart: 20000, PubEnd: 20010, ListenStart: 15000, ListenEnd: 15010}},
		})
		if code != http.StatusOK || env.Code != shared.CodeOK {
			t.Fatalf("update b failed: %d %s %s", code, env.Code, env.Message)
		}
		if out.Exchange == nil || out.Exchange.Total != 1 || out.Exchange.Exchanged != 1 || out.Exchange.Failed != 0 {
			t.Fatalf("unexpected exchange summary: %+v", out.Exchange)
		}
		rev, err := st.DesiredChainRevision(ctx, chainID)
		if err != nil {
			t.Fatalf("DesiredChainRevision: %v", err)
		}
		var updatedVC shared.VirtualConfig
		if err := json.Unmarshal(rev.Snapshot.ServiceConfig, &updatedVC); err != nil {
			t.Fatal(err)
		}
		if updatedVC.Port != 0 {
			t.Fatalf("expected out-of-range service port cleared for reallocation, got %d", updatedVC.Port)
		}
		tasks, _ := st.RevisionTasks(ctx, rev.ID)
		hasServiceApply, hasForwardApply := false, false
		for _, task := range tasks {
			if task.Kind == dispatch.RevisionPieceService && task.Action == "apply" {
				hasServiceApply = true
			}
			if task.Kind == dispatch.RevisionPieceForward && task.Action == "apply" {
				hasForwardApply = true
			}
		}
		if !hasServiceApply || !hasForwardApply {
			t.Fatalf("expected service apply and forward apply tasks, got %+v", tasks)
		}
	})

	t.Run("middle_hop_nat_shrink_allocates_new_hop_id", func(t *testing.T) {
		st, s, _, req := newExchangePanelFixture(t)
		aID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "a", Address: "a.test", BootstrapToken: "ta", MachineType: store.MachineTypeDirect, CountryCode: "US"})
		mID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "m", Address: "m.test", BootstrapToken: "tm", MachineType: store.MachineTypeDirect, CountryCode: "SG"})
		bID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "b", Address: "b.test", BootstrapToken: "tb", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
		req.online[aID], req.online[mID], req.online[bID] = true, true, true

		exitPort := 3000
		svcReq := createNodeRequest{
			Name: "mid-chain", ServerID: bID, Protocol: shared.ProtocolVLESS,
			Port: &exitPort, ShortID: "abcdef0123456789", Dest: "dl.google.com:443",
			ServerNames: []string{"dl.google.com"}, Fingerprint: shared.FingerprintChrome,
			Network: shared.NetworkTCP, Flow: shared.FlowVision,
		}
		_ = svcReq.normalize()
		svcCfg, _ := json.Marshal(buildVirtualConfig(svcReq))
		dep, err := st.CreateInitialChainDeployment(ctx, store.InitialChainDeployment{
			Name: "mid-chain", ServiceServerID: bID, ServiceProtocol: shared.ProtocolVLESS,
			ServicePort: &exitPort, ServiceConfig: svcCfg,
			ServiceUUID: "11111111-1111-4111-8111-111111111111", TrafficMultiplierMilli: 1000,
			Hops: []store.InitialChainHop{
				{ServerID: aID, Role: store.HopRoleEntry, Transport: "direct", ForwardPort: 1000},
				{ServerID: mID, Role: store.HopRoleMiddle, Transport: "direct", ForwardPort: 2000},
				{ServerID: bID, Role: store.HopRoleExit, Transport: "direct", ForwardPort: exitPort},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = st.SetNodeActive(ctx, dep.NodeID, json.RawMessage(`{"port":3000}`))
		for _, h := range dep.Hops {
			_ = st.SetChainHopStatus(ctx, h.HopID, store.HopStatusActive, "")
		}
		_ = st.PublishChainRevision(ctx, dep.RevisionID, false)
		oldMidHopID := dep.Hops[1].HopID

		code, env, out := postUpdateServer(t, s, map[string]any{
			"server_id":     mID,
			"kind":          "managed",
			"machine_type":  "nat",
			"alias":         "m",
			"address":       "m.test",
			"addresses":     []string{"m.test"},
			"country_code":  "SG",
			"location":      "",
			"allowed_ports": []shared.PortRange{{PubStart: 12000, PubEnd: 12010}},
		})
		if code != http.StatusOK || env.Code != shared.CodeOK || out.Exchange == nil || out.Exchange.Exchanged != 1 {
			t.Fatalf("middle hop nat update failed: %d %s %s exchange=%+v", code, env.Code, env.Message, out.Exchange)
		}
		rev, err := st.DesiredChainRevision(ctx, dep.ChainID)
		if err != nil {
			t.Fatal(err)
		}
		if rev.Snapshot.Hops[1].HopID == oldMidHopID || rev.Snapshot.Hops[1].ForwardPort != 0 {
			t.Fatalf("expected new middle HopID != %d with ForwardPort=0, got %+v", oldMidHopID, rev.Snapshot.Hops[1])
		}
	})

	t.Run("entry_nat_reallocates_shared_endpoint_and_waiting_when_offline", func(t *testing.T) {
		st, s, _, req := newExchangePanelFixture(t)
		aID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "a", Address: "a.test", BootstrapToken: "ta", MachineType: store.MachineTypeDirect, CountryCode: "US"})
		bID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "b", Address: "b.test", BootstrapToken: "tb", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
		// 入口 A 离线，允许 B 在线 -> TryExchange 应将链置为 waiting_for_agent
		req.online[aID], req.online[bID] = false, true

		chainID, oldEpID, _, _ := buildActiveDirectTwoHopChain(t, ctx, st, "c2", aID, bID, 8443, 3000)

		code, env, out := postUpdateServer(t, s, map[string]any{
			"server_id":     aID,
			"kind":          "managed",
			"machine_type":  "nat",
			"alias":         "a",
			"address":       "a.test",
			"addresses":     []string{"a.test"},
			"country_code":  "US",
			"location":      "",
			"allowed_ports": []shared.PortRange{{PubStart: 10000, PubEnd: 10010}},
		})
		if code != http.StatusOK || env.Code != shared.CodeOK {
			t.Fatalf("update a failed: %d %s %s", code, env.Code, env.Message)
		}
		if out.Exchange == nil || out.Exchange.Total != 1 || out.Exchange.Waiting != 1 || out.Exchange.Failed != 0 {
			t.Fatalf("unexpected exchange summary for offline agent: %+v", out.Exchange)
		}
		ch, _ := st.ChainByID(ctx, chainID)
		if ch.Status != store.ChainStatusWaitingForAgent {
			t.Fatalf("chain status = %s, want %s", ch.Status, store.ChainStatusWaitingForAgent)
		}
		desiredRev, err := st.DesiredChainRevision(ctx, chainID)
		if err != nil {
			t.Fatalf("DesiredChainRevision: %v", err)
		}
		if desiredRev.Snapshot.EndpointID == 0 || desiredRev.Snapshot.EndpointID == oldEpID {
			t.Fatalf("expected desired revision EndpointID != %d, got %d", oldEpID, desiredRev.Snapshot.EndpointID)
		}
		newEp, err := st.SharedEndpointByID(ctx, desiredRev.Snapshot.EndpointID)
		if err != nil {
			t.Fatal(err)
		}
		if newEp.Port != 0 {
			t.Fatalf("newly allocated SharedEndpoint should start at port=0 pending agent apply, got %d", newEp.Port)
		}
	})

	t.Run("exit_direct_to_exit_only_nat_switches_to_reverse_and_back", func(t *testing.T) {
		st, s, _, req := newExchangePanelFixture(t)
		aID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "a", Address: "a.test", BootstrapToken: "ta", MachineType: store.MachineTypeDirect, CountryCode: "US"})
		bID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "b", Address: "b.test", BootstrapToken: "tb", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
		req.online[aID], req.online[bID] = true, true

		chainID, _, _, _ := buildActiveDirectTwoHopChain(t, ctx, st, "c3", aID, bID, 8443, 3000)

		code, env, out := postUpdateServer(t, s, map[string]any{
			"server_id":     bID,
			"kind":          "managed",
			"machine_type":  "nat",
			"alias":         "b",
			"address":       "b.test",
			"addresses":     []string{"b.test"},
			"country_code":  "JP",
			"location":      "",
			"allowed_ports": []shared.PortRange{},
		})
		if code != http.StatusOK || env.Code != shared.CodeOK {
			t.Fatalf("update b to exit-only nat failed: %d %s %s", code, env.Code, env.Message)
		}
		if out.Exchange == nil || out.Exchange.Exchanged != 1 || out.Exchange.Failed != 0 {
			t.Fatalf("expected reverse conversion success, got %+v", out.Exchange)
		}
		desired, err := st.DesiredChainRevision(ctx, chainID)
		if err != nil {
			t.Fatal(err)
		}
		if desired.Snapshot.Hops[0].Transport != "reverse" || desired.Snapshot.Hops[0].TunnelUUID == "" {
			t.Fatalf("expected hop 0 switched to reverse tunnel, got %+v", desired.Snapshot.Hops[0])
		}
		// 模拟该 rev 已发布，再将 B 从仅出口 NAT 转回 direct，应切回 direct 传输
		if err := st.SetChainHopStatus(ctx, desired.Snapshot.Hops[0].HopID, store.HopStatusActive, ""); err != nil {
			t.Fatal(err)
		}
		if err := st.SetChainHopStatus(ctx, desired.Snapshot.Hops[1].HopID, store.HopStatusActive, ""); err != nil {
			t.Fatal(err)
		}
		if err := st.PublishChainRevision(ctx, desired.ID, false); err != nil {
			t.Fatal(err)
		}
		code, env, out = postUpdateServer(t, s, map[string]any{
			"server_id":    bID,
			"kind":         "managed",
			"machine_type": "direct",
			"alias":        "b",
			"address":      "b.test",
			"addresses":    []string{"b.test"},
			"country_code": "JP",
			"location":     "",
		})
		if code != http.StatusOK || env.Code != shared.CodeOK {
			t.Fatalf("update b back to direct failed: %d %s %s", code, env.Code, env.Message)
		}
		if out.Exchange == nil || out.Exchange.Exchanged != 1 {
			t.Fatalf("expected direct switch back success, got %+v", out.Exchange)
		}
		desired2, err := st.DesiredChainRevision(ctx, chainID)
		if err != nil {
			t.Fatal(err)
		}
		if desired2.Snapshot.Hops[0].Transport != "direct" || desired2.Snapshot.Hops[0].TunnelUUID != "" {
			t.Fatalf("expected hop 0 switched back to direct, got %+v", desired2.Snapshot.Hops[0])
		}
	})
}

// TestTryExchangeFailureWithdrawsSubscriptionAndRecovers 验证不可自动适配的转换将链置为 exchange_failed、
// 从订阅目录摘除并保留 chain_hops，支持编辑恢复（applying）以及对含 external 服务器的链安全删除。
func TestTryExchangeFailureWithdrawsSubscriptionAndRecovers(t *testing.T) {
	ctx := context.Background()

	t.Run("entry_to_exit_only_marks_exchange_failed_withdraws_sub_and_recovers", func(t *testing.T) {
		st, s, _, req := newExchangePanelFixture(t)
		aID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry-a", Address: "a.test", BootstrapToken: "ta", MachineType: store.MachineTypeDirect, CountryCode: "US"})
		bID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit-b", Address: "b.test", BootstrapToken: "tb", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
		cID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry-c", Address: "c.test", BootstrapToken: "tc", MachineType: store.MachineTypeDirect, CountryCode: "SG"})
		req.online[aID], req.online[bID], req.online[cID] = true, true, true

		chainID, _, _, _ := buildActiveDirectTwoHopChain(t, ctx, st, "sub-chain", aID, bID, 8443, 3000)

		uid, err := st.InsertUser(ctx, "alice", "22222222-2222-4222-8222-222222222222", "sub-tok-1", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.SetUserChains(ctx, uid, []int64{chainID}); err != nil {
			t.Fatal(err)
		}
		beforeAssign, err := st.CatalogUserChainAssignments(ctx, uid)
		if err != nil || len(beforeAssign) != 1 {
			t.Fatalf("expected 1 active subscription assignment before conversion, got %+v (err=%v)", beforeAssign, err)
		}

		// 将入口服务器转为仅出口 NAT：入口无入站能力，TryExchange 应不阻塞保存，并将链标记为 exchange_failed
		code, env, out := postUpdateServer(t, s, map[string]any{
			"server_id":     aID,
			"kind":          "managed",
			"machine_type":  "nat",
			"alias":         "entry-a",
			"address":       "a.test",
			"addresses":     []string{"a.test"},
			"country_code":  "US",
			"location":      "",
			"allowed_ports": []shared.PortRange{},
		})
		if code != http.StatusOK || env.Code != shared.CodeOK {
			t.Fatalf("server update must not block on incompatible chain: %d %s %s", code, env.Code, env.Message)
		}
		if out.Exchange == nil || out.Exchange.Total != 1 || out.Exchange.Failed != 1 || len(out.Exchange.FailedChains) != 1 {
			t.Fatalf("expected 1 failed exchange chain, got %+v", out.Exchange)
		}
		ch, _ := st.ChainByID(ctx, chainID)
		if ch.Status != store.ChainStatusExchangeFailed {
			t.Fatalf("chain status = %s, want %s", ch.Status, store.ChainStatusExchangeFailed)
		}
		if ch.DesiredRevisionID != 0 || ch.Error == "" {
			t.Fatalf("expected desired_revision_id=0 and non-empty error, got rev=%d err=%q", ch.DesiredRevisionID, ch.Error)
		}
		hops, err := st.ChainHops(ctx, chainID)
		if err != nil || len(hops) != 2 {
			t.Fatalf("expected chain_hops preserved (len=2), got %+v (err=%v)", hops, err)
		}
		afterAssign, err := st.CatalogUserChainAssignments(ctx, uid)
		if err != nil || len(afterAssign) != 0 {
			t.Fatalf("expected chain withdrawn from subscriptions in exchange_failed, got %+v (err=%v)", afterAssign, err)
		}

		// 管理员在链路页更换入口为 C 恢复链路
		p := 3000
		editCode, editEnv := postJSON(t, s.handleEditChain, "/api/chain/edit", editChainRequest{
			ChainID: chainID,
			Name:    "sub-chain",
			Hops:    []chainHopRef{{ServerID: cID}, {ServerID: bID}},
			Node: createNodeRequest{
				Name: "sub-chain", ServerID: bID, Protocol: shared.ProtocolVLESS, Port: &p,
				ShortID: "abcdef0123456789", Dest: "dl.google.com:443", ServerNames: []string{"dl.google.com"},
				Fingerprint: shared.FingerprintChrome, Network: shared.NetworkTCP, Flow: shared.FlowVision,
			},
			TrafficMultiplier: "1.000",
		})
		if editCode != http.StatusOK || (editEnv.Code != shared.CodeOK && editEnv.Code != shared.CodeAccepted) {
			t.Fatalf("recover edit failed: %d %s %s", editCode, editEnv.Code, editEnv.Message)
		}
		ch, _ = st.ChainByID(ctx, chainID)
		if ch.Status != store.ChainStatusApplying {
			t.Fatalf("recovered chain status = %s, want %s", ch.Status, store.ChainStatusApplying)
		}
	})

	t.Run("managed_to_external_marks_exchange_failed_and_delete_skips_external", func(t *testing.T) {
		st, s, _, req := newExchangePanelFixture(t)
		aID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "entry-a", Address: "a.test", BootstrapToken: "ta", MachineType: store.MachineTypeDirect, CountryCode: "US"})
		bID, _ := st.CreateServer(ctx, store.ServerDraft{Alias: "exit-b", Address: "b.test", BootstrapToken: "tb", MachineType: store.MachineTypeDirect, CountryCode: "JP"})
		req.online[aID], req.online[bID] = true, true

		chainID, _, _, _ := buildActiveDirectTwoHopChain(t, ctx, st, "ext-conv-chain", aID, bID, 8443, 3000)

		code, env, out := postUpdateServer(t, s, map[string]any{
			"server_id":    bID,
			"kind":         "external",
			"alias":        "exit-b-ext",
			"country_code": "JP",
			"location":     "",
			"external_exit": shared.ExternalExit{
				Protocol: shared.ProtocolSocks, Address: "ext.test", Port: 1080,
			},
		})
		if code != http.StatusOK || env.Code != shared.CodeOK {
			t.Fatalf("b→external failed: %d %s %s", code, env.Code, env.Message)
		}
		if out.Exchange == nil || out.Exchange.Failed != 1 {
			t.Fatalf("expected 1 failed exchange chain, got %+v", out.Exchange)
		}
		ch, _ := st.ChainByID(ctx, chainID)
		if ch.Status != store.ChainStatusExchangeFailed {
			t.Fatalf("chain status = %s, want %s", ch.Status, store.ChainStatusExchangeFailed)
		}

		// 清空已发送消息记录，删除链时不得向已转成 external 的 B 下发任何命令
		req.sent = nil
		delCode, delEnv := postJSON(t, s.handleDeleteChain, "/api/chain/delete", map[string]any{
			"chain_id": chainID,
		})
		if delCode != http.StatusOK || delEnv.Code != shared.CodeOK {
			t.Fatalf("delete exchange_failed chain failed: %d %s %s", delCode, delEnv.Code, delEnv.Message)
		}
		for _, sent := range req.sent {
			if sent.serverID == bID {
				t.Fatalf("must not send cleanup command to external server %d, got %+v", bID, sent)
			}
		}
	})
}
