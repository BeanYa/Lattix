package xray

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
