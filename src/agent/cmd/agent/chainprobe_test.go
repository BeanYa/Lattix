package main

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"lattix/agent/internal/state"
	"lattix/shared"
)

func forwardPiece(hopID int64, targetAddress string, targetPort int, ruleOutboundTag string, outbounds ...json.RawMessage) state.ChainPiece {
	inbound, _ := json.Marshal(map[string]any{
		"tag": shared.ChainForwardTag(hopID), "listen": "0.0.0.0", "port": 9001,
		"protocol": "dokodemo-door",
		"settings": map[string]any{"address": targetAddress, "port": targetPort, "network": "tcp"},
	})
	rule, _ := json.Marshal(map[string]any{
		"type": "field", "inboundTag": []string{shared.ChainForwardTag(hopID)},
		"outboundTag": ruleOutboundTag,
	})
	return state.ChainPiece{HopID: hopID, Kind: shared.HopKindForward,
		Inbound: inbound, Outbounds: outbounds, Rules: []json.RawMessage{rule}}
}

func bridgePiece(hopID int64, portalAddress string, portalPort int) state.ChainPiece {
	outbound, _ := json.Marshal(map[string]any{
		"tag": shared.ChainBridgeTag(hopID), "protocol": "vless",
		"settings": map[string]any{
			"vnext": []map[string]any{{
				"address": portalAddress, "port": portalPort,
				"users": []map[string]any{{"id": "u", "encryption": "none"}},
			}},
		},
	})
	return state.ChainPiece{HopID: hopID, Kind: shared.HopKindBridge, Outbound: outbound}
}

func TestChainProbeTargetsForwardDirect(t *testing.T) {
	targets := chainProbeTargets([]state.ChainPiece{
		forwardPiece(3, "203.0.113.2", 9101, "direct"),
		bridgePiece(5, "203.0.113.1", 9201),
	})
	if len(targets) != 2 {
		t.Fatalf("got %d targets: %+v", len(targets), targets)
	}
	if targets[0] != (chainProbeTarget{hopID: 3, kind: shared.ChainProbeKindForward,
		address: "203.0.113.2", port: 9101}) {
		t.Fatalf("forward target mismatch: %+v", targets[0])
	}
	if targets[1] != (chainProbeTarget{hopID: 5, kind: shared.ChainProbeKindBridge,
		address: "203.0.113.1", port: 9201}) {
		t.Fatalf("bridge target mismatch: %+v", targets[1])
	}
}

func TestChainProbeSkipsReverseAndHy2Segments(t *testing.T) {
	hy2Outbound, _ := json.Marshal(map[string]any{"protocol": shared.ProtocolHysteria2})
	pieces := []state.ChainPiece{
		// 经 reverse portal 的 forward：与同跳 bridge 段同物理路径，不重复探测。
		forwardPiece(3, "10.0.0.9", 9000, "portal_7"),
		// hy2 outbound 段（UDP/QUIC）无 TCP 握手可测。
		forwardPiece(4, "203.0.113.3", 9102, "chainfwd_4", hy2Outbound),
		// portal 是反向段被拨侧，无出向探测。
		{HopID: 7, Kind: shared.HopKindPortal},
	}
	if targets := chainProbeTargets(pieces); len(targets) != 0 {
		t.Fatalf("expected no targets, got %+v", targets)
	}
}

func TestChainProbeSampleDialsAndReports(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	stats := chainProbeSample([]state.ChainPiece{forwardPiece(3, "127.0.0.1", port, "direct")},
		time.Second)
	if len(stats) != 1 {
		t.Fatalf("got %d stats: %+v", len(stats), stats)
	}
	if stats[0].LatencyMS == nil || *stats[0].LatencyMS < 0 {
		t.Fatalf("expected reachable latency, got %+v", stats[0])
	}

	stats = chainProbeSample([]state.ChainPiece{forwardPiece(4, "127.0.0.1", 1 /*拒连*/, "direct")},
		300*time.Millisecond)
	if len(stats) != 1 || stats[0].LatencyMS != nil {
		t.Fatalf("expected failed probe kept as connectivity record, got %+v", stats)
	}
}

func TestTelemetryCarriesChainProbes(t *testing.T) {
	mgr := &fakeTelemetryManager{}
	called := false
	tm := &telemetry{mgr: mgr, chainProbes: func() []shared.ChainProbeStat {
		called = true
		return []shared.ChainProbeStat{{HopID: 3, Kind: shared.ChainProbeKindForward,
			TargetAddress: "203.0.113.2", TargetPort: 9101}}
	}}
	payload := tm.collect()
	if !called || len(payload.ChainProbes) != 1 || payload.ChainProbes[0].HopID != 3 {
		t.Fatalf("chain probes not carried: called=%v payload=%+v", called, payload.ChainProbes)
	}
	// 探测器缺省（nil）时字段缺省，兼容旧装配路径。
	if (&telemetry{mgr: mgr}).collect().ChainProbes != nil {
		t.Fatal("nil sampler must leave chain_probes absent")
	}
}
