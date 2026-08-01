package xray

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lattix/agent/internal/state"
	"lattix/shared"
)

// newCleanupTestManager 构造可执行 CleanupXray 的 Manager（假 xray 脚本 run -test 恒成功）。
func newCleanupTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewManager(bin, filepath.Join(dir, "xray.json"), "127.0.0.1:19085", &telemetryTestRunner{})
}

func cleanupInbound(t *testing.T, tag string, port int) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tag": tag, "listen": "0.0.0.0", "port": port, "protocol": "vless",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// seedCleanupConfig 直接落盘一份含给定 inbounds 的配置与 chainPieces 记录。
func seedCleanupConfig(t *testing.T, m *Manager, inbounds []json.RawMessage, pieces []state.ChainPiece) {
	t.Helper()
	fc := m.skeleton()
	fc.setInbounds(inbounds)
	b, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.configPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	m.SetChainPieces(pieces)
}

func readCleanupConfig(t *testing.T, m *Manager) fullConfig {
	t.Helper()
	b, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var fc fullConfig
	if err := json.Unmarshal(b, &fc); err != nil {
		t.Fatal(err)
	}
	return fc
}

// TestCleanupXrayDryRunReportsOnly 验证 dry-run 只报告差异：不改配置、不落盘、不清记录。
func TestCleanupXrayDryRunReportsOnly(t *testing.T) {
	m := newCleanupTestManager(t)
	seedCleanupConfig(t, m,
		[]json.RawMessage{
			cleanupInbound(t, "node_1", 10001),
			cleanupInbound(t, "chainfwd_7", 20001),
			cleanupInbound(t, "chainfwd_99", 20099), // 残留：期望集合外
		},
		[]state.ChainPiece{{HopID: 7, Kind: shared.HopKindForward, Port: 20001,
			Inbound: cleanupInbound(t, "chainfwd_7", 20001)}})

	result, err := m.CleanupXray(shared.CleanupXrayPayload{
		DryRun: true, ExpectedInboundTags: []string{"node_1", "chainfwd_7"},
		ExpectedPieces: []string{"forward/7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedInbounds) != 1 || result.RemovedInbounds[0].Tag != "chainfwd_99" || result.RemovedInbounds[0].Port != 20099 {
		t.Fatalf("预览差异 = %+v，期望仅 chainfwd_99@20099", result.RemovedInbounds)
	}
	if len(result.RemovedPieces) != 0 {
		t.Fatalf("预览不应清理记录，实际 %v", result.RemovedPieces)
	}
	// 配置未改动。
	fc := readCleanupConfig(t, m)
	if len(fc.inbounds()) != 3 {
		t.Fatalf("dry-run 不应改配置，inbounds = %d", len(fc.inbounds()))
	}
	// 记录未清理。
	if n := len(m.ChainPieces()); n != 1 {
		t.Fatalf("dry-run 不应清记录，实际 %d", n)
	}
}

// TestCleanupXrayExecute 验证执行：删除残留 inbound 与未期望 piece（含 outbound/reverse/routing），
// 保留期望项与对应记录。
func TestCleanupXrayExecute(t *testing.T) {
	m := newCleanupTestManager(t)
	bridgeRec := state.ChainPiece{
		HopID: 9, Kind: shared.HopKindBridge,
		Outbound: json.RawMessage(`{"tag":"chainbr_9","protocol":"vless"}`),
		Reverse:  json.RawMessage(`{"tag":"bridge_9","domain":"c1h7.lx"}`),
		Rules:    []json.RawMessage{json.RawMessage(`{"type":"field","domain":["full:c1h7.lx"],"outboundTag":"chainbr_9"}`)},
	}
	fwdRec := state.ChainPiece{
		HopID: 7, Kind: shared.HopKindForward, Port: 20001,
		Inbound: cleanupInbound(t, "chainfwd_7", 20001),
		Rules:   []json.RawMessage{json.RawMessage(`{"type":"field","inboundTag":["chainfwd_7"],"outboundTag":"direct"}`)},
	}
	seedCleanupConfig(t, m,
		[]json.RawMessage{
			cleanupInbound(t, "node_1", 10001),
			cleanupInbound(t, "chainfwd_7", 20001),
			cleanupInbound(t, "chainfwd_99", 20099), // 残留 inbound
		},
		[]state.ChainPiece{fwdRec, bridgeRec})
	// 预先把 forward/bridge 的 outbound/reverse/routing 并入配置（模拟已落盘残留）。
	fc := applyChainPiece(readCleanupConfig(t, m), fwdRec)
	fc = applyChainPiece(fc, bridgeRec)
	b, _ := json.MarshalIndent(fc, "", "  ")
	if err := os.WriteFile(m.configPath, b, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := m.CleanupXray(shared.CleanupXrayPayload{
		ExpectedInboundTags: []string{"node_1", "chainfwd_7"},
		ExpectedPieces:      []string{"forward/7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedInbounds) != 1 || result.RemovedInbounds[0].Tag != "chainfwd_99" {
		t.Fatalf("删除 inbound = %+v，期望仅 chainfwd_99", result.RemovedInbounds)
	}
	if len(result.RemovedPieces) != 1 || result.RemovedPieces[0] != "bridge/9" {
		t.Fatalf("删除 piece = %v，期望 [bridge/9]", result.RemovedPieces)
	}

	after := readCleanupConfig(t, m)
	kept := map[string]bool{}
	for _, raw := range after.inbounds() {
		kept[inboundTag(raw)] = true
	}
	if !kept["node_1"] || !kept["chainfwd_7"] || kept["chainfwd_99"] {
		t.Fatalf("清理后 inbound = %v", kept)
	}
	for _, raw := range after.outbounds() {
		var p struct{ Tag string `json:"tag"` }
		json.Unmarshal(raw, &p)
		if p.Tag == "chainbr_9" {
			t.Fatal("bridge outbound 应被清理")
		}
	}
	if _, ok := after["reverse"]; ok {
		t.Fatalf("reverse 段应被清理: %s", after["reverse"])
	}
	if n := len(after.routingRules()); n != 1 {
		t.Fatalf("routing 规则数 %d，期望仅保留 chainfwd_7 的 1 条", n)
	}
	// 记录同步清理：仅 forward/7 保留。
	records := m.ChainPieces()
	if len(records) != 1 || records[0].HopID != 7 || records[0].Kind != shared.HopKindForward {
		t.Fatalf("记录 = %+v，期望仅 forward/7", records)
	}
}

// TestCleanupXrayKeepsAPIPreservesExpected 验证 api inbound 恒保留、期望项保留、未知 inbound 删除。
func TestCleanupXrayKeepsAPIPreservesExpected(t *testing.T) {
	m := newCleanupTestManager(t)
	seedCleanupConfig(t, m,
		[]json.RawMessage{
			cleanupInbound(t, "api", 19001),
			cleanupInbound(t, "node_1", 10001),
			cleanupInbound(t, "unknown_junk", 39999),
		}, nil)

	result, err := m.CleanupXray(shared.CleanupXrayPayload{
		ExpectedInboundTags: []string{"node_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedInbounds) != 1 || result.RemovedInbounds[0].Tag != "unknown_junk" {
		t.Fatalf("删除 inbound = %+v，期望仅 unknown_junk", result.RemovedInbounds)
	}
	kept := map[string]bool{}
	for _, raw := range readCleanupConfig(t, m).inbounds() {
		kept[inboundTag(raw)] = true
	}
	if !kept["api"] || !kept["node_1"] {
		t.Fatalf("api/node_1 应保留: %v", kept)
	}
}

// TestCleanupXrayEmptyExpectedClearsAll 验证空期望集合：清空全部受管 inbound，仅剩 api。
func TestCleanupXrayEmptyExpectedClearsAll(t *testing.T) {
	m := newCleanupTestManager(t)
	seedCleanupConfig(t, m,
		[]json.RawMessage{
			cleanupInbound(t, "api", 19001),
			cleanupInbound(t, "node_1", 10001),
			cleanupInbound(t, "chainfwd_7", 20001),
			cleanupInbound(t, "shared_endpoint_5", 30005),
		}, nil)

	result, err := m.CleanupXray(shared.CleanupXrayPayload{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedInbounds) != 3 {
		t.Fatalf("删除 inbound = %+v，期望 3 个受管监听", result.RemovedInbounds)
	}
	kept := map[string]bool{}
	for _, raw := range readCleanupConfig(t, m).inbounds() {
		kept[inboundTag(raw)] = true
	}
	if !kept["api"] || len(kept) != 1 {
		t.Fatalf("仅 api 应保留: %v", kept)
	}
}

// TestCleanupXrayNoOp 验证无差异时幂等返回空结果，不落盘不重启。
func TestCleanupXrayNoOp(t *testing.T) {
	m := newCleanupTestManager(t)
	seedCleanupConfig(t, m,
		[]json.RawMessage{cleanupInbound(t, "node_1", 10001)}, nil)

	result, err := m.CleanupXray(shared.CleanupXrayPayload{
		ExpectedInboundTags: []string{"node_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedInbounds) != 0 || len(result.RemovedPieces) != 0 {
		t.Fatalf("无差异应返回空结果: %+v", result)
	}
}

// failingRestartRunner 模拟重启失败（回滚路径）。
type failingRestartRunner struct{ telemetryTestRunner }

func (*failingRestartRunner) Restart(context.Context) error { return errors.New("restart boom") }

// TestCleanupXrayRestartFailureRollsBack 验证执行失败回滚：配置与记录保持原样。
func TestCleanupXrayRestartFailureRollsBack(t *testing.T) {
	m := newCleanupTestManager(t)
	m.runner = &failingRestartRunner{}
	seedCleanupConfig(t, m,
		[]json.RawMessage{
			cleanupInbound(t, "node_1", 10001),
			cleanupInbound(t, "chainfwd_99", 20099),
		},
		[]state.ChainPiece{{HopID: 99, Kind: shared.HopKindForward, Port: 20099,
			Inbound: cleanupInbound(t, "chainfwd_99", 20099)}})

	_, err := m.CleanupXray(shared.CleanupXrayPayload{
		ExpectedInboundTags: []string{"node_1"},
	})
	if err == nil {
		t.Fatal("重启失败应返回错误")
	}
	fc := readCleanupConfig(t, m)
	if len(fc.inbounds()) != 2 {
		t.Fatalf("回滚后配置应保持原样，inbounds = %d", len(fc.inbounds()))
	}
	if n := len(m.ChainPieces()); n != 1 {
		t.Fatalf("回滚后记录应保持原样，实际 %d", n)
	}
}
