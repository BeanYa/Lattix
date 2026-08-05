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

// newRebuildTestManager 构造 Manager：假 xray 脚本（run -test 恒成功；
// x25519/vlessenc 输出可解析的假值）+ 可注入失败/记录调用的测试 runner。
func newRebuildTestManager(t *testing.T) (*Manager, *rebuildTestRunner) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	script := `#!/bin/sh
case "$1" in
  x25519) echo "Private key: fakeprivkey1234567890"; echo "Public key: fakepubkey1234567890";;
  vlessenc) echo 'Authentication: X25519'; echo '"decryption": "decx25519"'; echo '"encryption": "encx25519"'; echo 'Authentication: ML-KEM-768'; echo '"decryption": "decmkem"'; echo '"encryption": "encmkem"';;
  *) exit 0;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &rebuildTestRunner{running: true}
	return NewManager(bin, filepath.Join(dir, "xray.json"), "127.0.0.1:19085", runner), runner
}

// rebuildTestRunner 记录 Stop/Restart 调用并允许注入失败。
type rebuildTestRunner struct {
	stopErr    error
	restartErr error
	running    bool
	stops      int
	restarts   int
}

func (r *rebuildTestRunner) Stop(context.Context) error { r.stops++; return r.stopErr }
func (r *rebuildTestRunner) Restart(context.Context) error {
	r.restarts++
	return r.restartErr
}
func (r *rebuildTestRunner) IsRunning(context.Context) bool { return r.running }

// rebuildRealityTemplate 构造带 Reality 占位符的 vless 模板（测试用，
// dest 探测经 destReachable 桩短路——见 Task 2 Step 3 的测试缝）。
func rebuildRealityTemplate() shared.VirtualConfig {
	return shared.VirtualConfig{Template: json.RawMessage(`{
  "protocol": "vless",
  "listen": "0.0.0.0",
  "port": "{{PORT}}",
  "tag": "{{TAG}}",
  "settings": {
    "clients": "{{CLIENTS}}",
    "decryption": "{{DECRYPTION}}"
  },
  "streamSettings": {
    "network": "tcp",
    "security": "reality",
    "realitySettings": {
      "dest": "example.com:443",
      "serverNames": ["example.com"],
      "privateKey": "{{PRIVATE_KEY}}",
      "shortIds": ["6ba85179e30d4fc2"]
    }
  }
}`)}
}

func TestExtractPrevInbound(t *testing.T) {
	raw := json.RawMessage(`{
		"tag": "node_1", "port": 12345, "protocol": "vless",
		"settings": {"decryption": "dec-old"},
		"streamSettings": {"network": "tcp", "security": "reality",
			"realitySettings": {"privateKey": "priv-old", "dest": "example.com:443"}}
	}`)
	port, priv, dec := extractPrevInbound(raw)
	if port != 12345 || priv != "priv-old" || dec != "dec-old" {
		t.Fatalf("extract = %d/%q/%q", port, priv, dec)
	}
}

// TestRebuildInboundPreservesPrevValues 验证保留模式：私钥/decryption/端口复用备份值，
// 不重新生成（生成的假值 fakeprivkey1234567890 不应出现）。
func TestRebuildInboundPreservesPrevValues(t *testing.T) {
	orig := destReachable
	destReachable = func(string, string) bool { return true }
	t.Cleanup(func() { destReachable = orig })
	m, _ := newRebuildTestManager(t)
	prev := json.RawMessage(`{
		"tag": "node_1", "port": 12345, "protocol": "vless",
		"settings": {"decryption": "dec-old"},
		"streamSettings": {"network": "tcp", "security": "reality",
			"realitySettings": {"privateKey": "priv-old", "dest": "example.com:443"}}
	}`)
	inbound, _, err := m.rebuildInbound("node_1", rebuildRealityTemplate(),
		[]string{"u1"}, nil, nil, prev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(inbound)
	for _, want := range []string{`"port":12345`, "priv-old", "dec-old", `"minClientVer":"0"`} {
		if !jsonContains(s, want) {
			t.Fatalf("重建 inbound 缺少 %s: %s", want, s)
		}
	}
	if jsonContains(s, "fakeprivkey1234567890") || jsonContains(s, "decx25519") {
		t.Fatalf("不应重新生成密钥/decryption: %s", s)
	}
}

// TestRebuildInboundWithoutPrevGenerates 验证无备份时回退生成路径（x25519 假输出出现）。
func TestRebuildInboundWithoutPrevGenerates(t *testing.T) {
	orig := destReachable
	destReachable = func(string, string) bool { return true }
	t.Cleanup(func() { destReachable = orig })
	m, _ := newRebuildTestManager(t)
	inbound, _, err := m.rebuildInbound("node_1", rebuildRealityTemplate(),
		[]string{"u1"}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(inbound)
	if !jsonContains(s, "fakeprivkey1234567890") || !jsonContains(s, "decmkem") {
		t.Fatalf("应生成新密钥/decryption: %s", s)
	}
}

func jsonContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// seedRebuildConfig 直接落盘一份受管配置（重建前的"当前生效"内容）。
func seedRebuildConfig(t *testing.T, m *Manager, content string) {
	t.Helper()
	if err := os.WriteFile(m.configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readRebuildFile 读取当前 config 文件原文。
func readRebuildFile(t *testing.T, m *Manager) string {
	t.Helper()
	b, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func rebuildPayload(nodes ...shared.ApplyNodePayload) shared.RebuildXrayPayload {
	tags := []string{}
	for _, n := range nodes {
		tags = append(tags, shared.NodeTag(n.NodeID))
	}
	return shared.RebuildXrayPayload{Nodes: nodes, ExpectedInboundTags: tags}
}

// TestRebuildXraySuccess 验证成功路径：骨架+node inbound 重建、备份删除、回执齐全。
func TestRebuildXraySuccess(t *testing.T) {
	m, runner := newRebuildTestManager(t)
	orig := destReachable
	destReachable = func(string, string) bool { return true }
	t.Cleanup(func() { destReachable = orig })
	seedRebuildConfig(t, m, `{"log":{"loglevel":"warning"},"inbounds":[{"tag":"node_1","port":12345,"protocol":"vless"}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`)
	payload := rebuildPayload(shared.ApplyNodePayload{NodeID: 1, Config: rebuildRealityTemplate(), UserUUIDs: []string{"u1"}})

	result, err := m.RebuildXray(payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.RolledBack {
		t.Fatal("不应回滚")
	}
	if len(result.RebuiltInbounds) != 1 || result.RebuiltInbounds[0].Tag != "node_1" ||
		result.RebuiltInbounds[0].Port != 12345 || result.RebuiltInbounds[0].Kind != "vless" {
		t.Fatalf("重建 inbound 摘要 = %+v", result.RebuiltInbounds)
	}
	if runner.stops != 1 || runner.restarts != 1 {
		t.Fatalf("stop=%d restart=%d，期望各 1 次", runner.stops, runner.restarts)
	}
	if _, err := os.Stat(m.configPath + rebuildBackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("成功后备份应删除")
	}
	s := readRebuildFile(t, m)
	if !jsonContains(s, "priv-old") {
		// prev 中无私钥（seed 的 inbound 无 privateKey 字段）→ 重新生成
		if !jsonContains(s, "fakeprivkey1234567890") {
			t.Fatalf("重建配置缺少私钥: %s", s)
		}
	}
	// 回执 JSON 契约：数组非 null。
	b, _ := json.Marshal(result)
	if jsonContains(string(b), `"rebuilt_inbounds":null`) || jsonContains(string(b), `"rebuilt_pieces":null`) {
		t.Fatalf("回执数组为 null: %s", b)
	}
}

// TestRebuildXraySelfCheckMissingRollsBack 验证自检缺失（期望 tag 不在重建结果中）→
// 恢复备份、重启、回执 RolledBack=true、错误信息含缺失项。
func TestRebuildXraySelfCheckMissingRollsBack(t *testing.T) {
	m, runner := newRebuildTestManager(t)
	orig := destReachable
	destReachable = func(string, string) bool { return true }
	t.Cleanup(func() { destReachable = orig })
	seedRebuildConfig(t, m, `{"log":{"loglevel":"warning"},"inbounds":[{"tag":"node_1","port":12345,"protocol":"vless"}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`)
	payload := rebuildPayload(shared.ApplyNodePayload{NodeID: 1, Config: rebuildRealityTemplate(), UserUUIDs: []string{"u1"}})
	payload.ExpectedInboundTags = append(payload.ExpectedInboundTags, "node_999")

	result, err := m.RebuildXray(payload)
	if err == nil {
		t.Fatal("自检缺失应返回错误")
	}
	if !result.RolledBack {
		t.Fatal("应回滚")
	}
	if got := readRebuildFile(t, m); !jsonContains(got, `"tag":"node_1"`) || jsonContains(got, "fakeprivkey") {
		t.Fatalf("回滚后应恢复原配置: %s", got)
	}
	if runner.restarts < 2 {
		t.Fatalf("回滚应再次重启，restarts=%d", runner.restarts)
	}
}

// TestRebuildXrayValidationFailureRollsBack 验证校验失败（xray run -test 失败）→ 回滚。
func TestRebuildXrayValidationFailureRollsBack(t *testing.T) {
	m, _ := newRebuildTestManager(t)
	// 换一个 run -test 恒失败的脚本。
	dir := filepath.Dir(m.bin)
	failBin := filepath.Join(dir, "xray-fail")
	if err := os.WriteFile(failBin, []byte("#!/bin/sh\n[ \"$1\" = \"run\" ] && exit 1\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.bin = failBin
	seedRebuildConfig(t, m, `{"log":{"loglevel":"warning"},"inbounds":[{"tag":"node_1","port":12345,"protocol":"dokodemo-door"}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`)
	payload := rebuildPayload(shared.ApplyNodePayload{NodeID: 1, Config: shared.VirtualConfig{Template: json.RawMessage(`{
		"protocol": "dokodemo-door", "listen": "0.0.0.0", "port": "{{PORT}}",
		"tag": "{{TAG}}", "settings": {"address": "1.1.1.1", "port": 53, "network": "tcp,udp"}
	}`)}})

	result, err := m.RebuildXray(payload)
	if err == nil {
		t.Fatal("校验失败应返回错误")
	}
	if !result.RolledBack {
		t.Fatal("应回滚")
	}
	if got := readRebuildFile(t, m); !jsonContains(got, `"port":12345`) || jsonContains(got, `"port":100`) {
		t.Fatalf("回滚后应恢复原配置: %s", got)
	}
}

// TestRebuildXrayStopFailure 验证停止失败直接报错，不落盘不重启。
func TestRebuildXrayStopFailure(t *testing.T) {
	m, runner := newRebuildTestManager(t)
	runner.stopErr = context.Canceled
	seedRebuildConfig(t, m, `{"inbounds":[]}`)
	_, err := m.RebuildXray(rebuildPayload())
	if err == nil {
		t.Fatal("停止失败应返回错误")
	}
	if runner.restarts != 0 {
		t.Fatalf("停止失败不应重启，restarts=%d", runner.restarts)
	}
}

// TestRebuildXrayRestartFailureRollsBack 验证重启失败 → 回滚（回滚重启也失败时
// 错误信息须包含回滚失败标注）。
func TestRebuildXrayRestartFailureRollsBack(t *testing.T) {
	m, runner := newRebuildTestManager(t)
	runner.restartErr = errors.New("restart boom")
	seedRebuildConfig(t, m, `{"inbounds":[{"tag":"node_1","port":12345,"protocol":"dokodemo-door"}]}`)
	payload := rebuildPayload(shared.ApplyNodePayload{NodeID: 1, Config: shared.VirtualConfig{Template: json.RawMessage(`{
		"protocol": "dokodemo-door", "listen": "0.0.0.0", "port": "{{PORT}}",
		"tag": "{{TAG}}", "settings": {"address": "1.1.1.1", "port": 53, "network": "tcp,udp"}
	}`)}})
	result, err := m.RebuildXray(payload)
	if err == nil {
		t.Fatal("重启失败应返回错误")
	}
	if !result.RolledBack {
		t.Fatal("应回滚")
	}
	if got := readRebuildFile(t, m); !jsonContains(got, `"port":12345`) {
		t.Fatalf("回滚后应恢复原配置: %s", got)
	}
}

// TestNormalizeRealityMinClientVer 验证规范化扫描：全配置遍历注入 minClientVer。
// 重点：重放的链路 portal piece（旧渲染、缺 minClientVer）同样被补全——这是
// "升级后需补全字段"机制的核心（§docs/rebuild-xray-config-design.md）。
func TestNormalizeRealityMinClientVer(t *testing.T) {
	fc := fullConfig{}
	oldPortal := json.RawMessage(`{
		"tag": "chainportal_7", "port": 20001, "protocol": "vless",
		"settings": {"decryption": "none"},
		"streamSettings": {"network": "tcp", "security": "reality",
			"realitySettings": {"dest": "example.com:443", "serverNames": ["example.com"],
				"privateKey": "priv-old", "shortIds": ["6ba85179e30d4fc2"]}}
	}`)
	okInbound := json.RawMessage(`{
		"tag": "node_1", "port": 10001, "protocol": "vless",
		"streamSettings": {"network": "tcp", "security": "reality",
			"realitySettings": {"dest": "example.com:443", "minClientVer": "0", "privateKey": "priv"}}
	}`)
	plain := json.RawMessage(`{"tag": "chainfwd_3", "port": 20003, "protocol": "dokodemo-door", "settings": {}}`)
	fc.setInbounds([]json.RawMessage{oldPortal, okInbound, plain})

	normalized := normalizeRebuiltConfig(fc)
	byTag := map[string]string{}
	for _, raw := range normalized.inbounds() {
		byTag[inboundTag(raw)] = string(raw)
	}
	if !jsonContains(byTag["chainportal_7"], `"minClientVer":"0"`) {
		t.Fatalf("重放旧 portal piece 应被注入 minClientVer: %s", byTag["chainportal_7"])
	}
	if !jsonContains(byTag["node_1"], `"minClientVer":"0"`) {
		t.Fatalf("已有字段应原样保留: %s", byTag["node_1"])
	}
	if jsonContains(byTag["chainfwd_3"], "minClientVer") {
		t.Fatalf("非 reality inbound 不应注入: %s", byTag["chainfwd_3"])
	}
	// 幂等：二次规范化无变化。
	again := normalizeRebuiltConfig(normalized)
	for _, raw := range again.inbounds() {
		if byTag[inboundTag(raw)] != string(raw) {
			t.Fatalf("规范化应幂等: %s", raw)
		}
	}
}

// TestRebuildXrayNormalizesReplayedPieces 验证端到端：旧 chain portal piece 记录
// 重放后经规范化补全 minClientVer。
func TestRebuildXrayNormalizesReplayedPieces(t *testing.T) {
	m, _ := newRebuildTestManager(t)
	orig := destReachable
	destReachable = func(string, string) bool { return true }
	t.Cleanup(func() { destReachable = orig })
	oldPortal := json.RawMessage(`{
		"tag": "chainportal_7", "port": 20001, "protocol": "vless",
		"settings": {"decryption": "none"},
		"streamSettings": {"network": "tcp", "security": "reality",
			"realitySettings": {"dest": "example.com:443", "serverNames": ["example.com"],
				"privateKey": "priv-old", "shortIds": ["6ba85179e30d4fc2"]}}
	}`)
	// portal piece 真实结构（对照 renderPortal）：Inbound + Reverse + Rules。
	reverse, _ := json.Marshal(map[string]any{"tag": chainPortalReverseTag(7), "domain": "c1h2.lx"})
	rule, _ := json.Marshal(map[string]any{
		"type": "field", "inboundTag": []string{shared.ChainPortalTag(7)},
		"outboundTag": chainPortalReverseTag(7),
	})
	m.SetChainPieces([]state.ChainPiece{{HopID: 7, Kind: shared.HopKindPortal, Port: 20001,
		Inbound: oldPortal, Reverse: reverse, Rules: []json.RawMessage{rule}}})
	payload := shared.RebuildXrayPayload{
		ExpectedInboundTags: []string{"chainportal_7"},
		ExpectedPieces:      []string{"portal/7"},
	}
	result, err := m.RebuildXray(payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.RolledBack {
		t.Fatal("不应回滚")
	}
	s := readRebuildFile(t, m)
	if !jsonContains(s, `"minClientVer": "0"`) {
		t.Fatalf("重建后链路 piece 应补全 minClientVer: %s", s)
	}
	if len(result.RebuiltPieces) != 1 || result.RebuiltPieces[0] != "portal/7" {
		t.Fatalf("回执 pieces = %v", result.RebuiltPieces)
	}
}
