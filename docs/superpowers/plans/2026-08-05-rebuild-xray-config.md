# 重建 xray 配置（xray.rebuild）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 面板一键触发、agent 原子执行"停止 xray → 备份 → 按面板下发配置重建 xray.json → 校验 → 重启 → 自检"，失败自动恢复备份并报错。

**Architecture:** 新消息类型 `xray.rebuild`，完全复用 `xray.cleanup` 的同步下发模式（面板从 DB 收集 → dispatcher 同步等待 → agent 执行 → 回执投递 waiter）。node inbound 由面板下发模板+用户、agent 重渲染（复用备份中的私钥/decryption/端口）；链路/共享端点 pieces 由 agent 本地 `state.ChainPieces` 重放。失败路径统一走恢复备份+重启+报错。

**Tech Stack:** Go 1.26（shared/agent/backend 三个 module，go.work），React + TypeScript + vitest，openapi-typescript 生成契约。

**Spec:** `docs/superpowers/specs/2026-08-05-rebuild-xray-config-design.md`

## Global Constraints

- 服务器须在线才可触发（面板 409 拒绝离线）。
- 重建只覆盖**活跃节点**（Status == active）+ 本地 chain piece 重放；非活跃节点 inbound 不重建。
- 私钥/decryption/监听端口必须复用备份中的当前生效值，不得重新生成（客户端不断连）。
- 失败必须恢复备份并重启 xray 后报错；回滚失败时错误信息须显式标注备份路径。
- 不改动 `node.apply`/`chain-hop.apply`/`xray.cleanup`/`loadConfig` 既有行为。
- 回执数组字段必须序列化为 `[]` 而非 `null`（前端 `.length` 直接读取）。
- 所有命令从 worktree 根目录 `.worktree/rebuild-xray-config/` 执行；测试命令：`go test ./src/shared/... ./src/agent/... ./src/backend/...`、`go test -race ./src/shared/... ./src/agent/... ./src/backend/...`、`npm test`（src/frontend）。
- 提交信息沿用仓库风格（`feat:`/`fix:`/`docs:` 前缀，中文描述）。

---

### Task 1: shared 协议类型（xray.rebuild 消息）

**Files:**
- Modify: `src/shared/messages.go`
- Test: `src/shared/rebuild_xray_test.go`（新建）

**Interfaces:**
- Produces: `shared.TypeRebuildXray = "xray.rebuild"`、`shared.RebuildXrayPayload{Nodes []ApplyNodePayload; ExpectedInboundTags, ExpectedPieces []string}`、`shared.RebuiltInbound{Tag string; Port int; Kind string}`、`shared.RebuildXrayResult{RebuiltInbounds []RebuiltInbound; RebuiltPieces []string; RolledBack bool}`、`ApplyResultPayload.Rebuild *RebuildXrayResult`。后续所有任务引用这些类型。

- [ ] **Step 1: 写失败测试** `src/shared/rebuild_xray_test.go`

```go
package shared

import (
	"encoding/json"
	"testing"
)

// TestRebuildXrayResultJSONArrays 验证回执 JSON 契约：数组字段必须是 [] 而非 null
// （前端直接读 .length，null 会崩溃，与 CleanupXrayResult 同约定）。
func TestRebuildXrayResultJSONArrays(t *testing.T) {
	result := RebuildXrayResult{}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"rebuilt_inbounds":[],"rebuilt_pieces":[],"rolled_back":false}` {
		t.Fatalf("回执 JSON = %s", b)
	}
}

// TestApplyResultPayloadCarriesRebuild 验证回执 data 挂载点：rebuild 字段可解出。
func TestApplyResultPayloadCarriesRebuild(t *testing.T) {
	b, err := json.Marshal(ApplyResultPayload{
		Rebuild: &RebuildXrayResult{RebuiltInbounds: []RebuiltInbound{{Tag: "node_1", Port: 10001, Kind: "vless"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded ApplyResultPayload
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Rebuild == nil || len(decoded.Rebuild.RebuiltInbounds) != 1 ||
		decoded.Rebuild.RebuiltInbounds[0].Tag != "node_1" || decoded.Rebuild.RebuiltInbounds[0].Port != 10001 ||
		decoded.Rebuild.RebuiltInbounds[0].Kind != "vless" {
		t.Fatalf("rebuild 回执 = %+v", decoded.Rebuild)
	}
}

// TestRebuildXrayPayloadKeys 验证面板下发载荷的 JSON 键名（agent 侧解析依赖）。
func TestRebuildXrayPayloadKeys(t *testing.T) {
	b, err := json.Marshal(RebuildXrayPayload{
		Nodes: []ApplyNodePayload{{NodeID: 7, UserUUIDs: []string{"u1"}}},
		ExpectedInboundTags: []string{"node_7"},
		ExpectedPieces:      []string{"forward/3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"nodes"`, `"expected_inbound_tags"`, `"expected_pieces"`, `"node_id"`, `"user_uuids"`} {
		if !json.Valid(b) || !contains(b, key) {
			t.Fatalf("载荷 %s 缺少字段 %s", b, key)
		}
	}
}

func contains(b []byte, s string) bool {
	return json.Valid(b) && len(b) >= len(s) && (len(b) == len(s) || json.Valid(append(append([]byte{}, b...), 0)))
}
```

注意：上面 `contains` 是占位逻辑，请改为简单实现：`bytes.Contains(b, []byte(s))`（import `bytes`）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/shared/... -run TestRebuild -v`
Expected: FAIL（TypeRebuildXray/RebuildXrayPayload 等未定义）

- [ ] **Step 3: 实现** `src/shared/messages.go`

在消息类型常量块（`TypeCleanupXray = "xray.cleanup"` 之后）加：

```go
	TypeRebuildXray          = "xray.rebuild"
```

在 `ApplyResultPayload`（现有 `Cleanup *CleanupXrayResult` 字段之后）加：

```go
	Rebuild         *RebuildXrayResult `json:"rebuild,omitempty"` // xray.rebuild 回执
```

在 `CleanupXrayResult` 定义之后加：

```go
// RebuildXrayPayload 是 xray.rebuild 的载荷：面板下发的重建期望。
// Nodes 为该服务器全部活跃节点的完整 apply 规格（模板+用户，agent 重渲染）；
// ExpectedInboundTags/ExpectedPieces 为重建后自检用的期望集合。
type RebuildXrayPayload struct {
	Nodes               []ApplyNodePayload `json:"nodes"`
	ExpectedInboundTags []string           `json:"expected_inbound_tags"`
	ExpectedPieces      []string           `json:"expected_pieces"`
}

// RebuiltInbound 是重建后的一条 inbound 摘要（展示用）。
type RebuiltInbound struct {
	Tag  string `json:"tag"`
	Port int    `json:"port"`
	Kind string `json:"kind"` // 协议（vless/vmess/…）
}

// RebuildXrayResult 是 xray.rebuild 的回执数据：重建后的监听/piece 摘要与回滚标记。
// 失败时错误经信封 code/message 表达，RolledBack=true 表示已恢复备份配置。
type RebuildXrayResult struct {
	RebuiltInbounds []RebuiltInbound `json:"rebuilt_inbounds"`
	RebuiltPieces   []string         `json:"rebuilt_pieces"`
	RolledBack      bool             `json:"rolled_back"`
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/shared/... -run TestRebuild -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/shared/messages.go src/shared/rebuild_xray_test.go
git commit -m "feat(shared): xray.rebuild protocol types"
```

---

### Task 2: agent 渲染保留模式（rebuildInbound / extractPrevInbound）

**Files:**
- Create: `src/agent/internal/xray/rebuild.go`
- Test: `src/agent/internal/xray/rebuild_test.go`（新建，含假 xray 脚本与测试 runner）

**Interfaces:**
- Consumes: `shared.RebuildXrayPayload`、`shared.ApplyNodePayload`（Task 1）、`shared.VirtualConfig`、`shared.NodeTag`、`shared.PlaceholderRealityPrivateKey`、`shared.PlaceholderVLessDecryption`、`m.fillTemplate`、`m.pickPort`（现有）。
- Produces: `(*Manager).rebuildInbound(tag string, vc shared.VirtualConfig, userUUIDs, destCandidates []string, portCandidates []int, prev json.RawMessage) (json.RawMessage, *shared.RealizedConfig, error)`、`extractPrevInbound(raw json.RawMessage) (port int, privateKey, decryption string)`。Task 3 的 `RebuildXray` 调用它们。

实现原理：**不改 fillTemplate 签名**——重建前把备份中提取的私钥/decryption 直接替换模板占位符（占位符消失则 fillTemplate 不会重新生成），端口由调用方传入。

- [ ] **Step 1: 写失败测试** `src/agent/internal/xray/rebuild_test.go`

```go
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
	m, _ := newRebuildTestManager(t)
	inbound, _, err := m.rebuildInbound("node_1", rebuildRealityTemplate(),
		[]string{"u1"}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(inbound)
	if !jsonContains(s, "fakeprivkey1234567890") || !jsonContains(s, "decx25519") {
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
```

注意：`TestRebuildInboundWithoutPrevGenerates` 中 vlessenc 以默认 auth（空串 → ML-KEM-768）解析，取 `decmkem`；断言改为 `"decmkem"`。上面写 `decx25519` 是错的——请在实现时修正为 `decmkem`（fake 脚本两种认证都输出）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/agent/internal/xray/ -run 'TestExtractPrevInbound|TestRebuildInbound' -v`
Expected: FAIL（extractPrevInbound/rebuildInbound 未定义；若 dest 探测真实发生会 4s 超时后失败——Step 3 的测试缝解决）

- [ ] **Step 3: 实现**（两部分）

**3a. 测试缝** `src/agent/internal/xray/fill.go`：`destReachable` 改为包级变量（生产行为不变）：

```go
// destReachable 以 TCP+TLS1.3 握手探测 dest 可达性（Reality 借用证书的前提）。
// 包级变量：单元测试可桩（避免依赖外网）。
var destReachable = destReachableImpl

func destReachableImpl(dest, serverName string) bool {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: destCheckTimeout}, "tcp", dest, &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true, // 仅探测可达性，不校验证书链
		MinVersion:         tls.VersionTLS13,
	})
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
```

（把现有 `func destReachable(...) bool` 的正文原样搬进 `destReachableImpl`。）

**3b. 新建** `src/agent/internal/xray/rebuild.go`：

```go
package xray

import (
	"encoding/json"
	"strings"

	"lattix/shared"
)

// extractPrevInbound 提取备份 inbound 的当前生效值（重建时复用，保证客户端不断连）：
// 监听端口、Reality 私钥（streamSettings.realitySettings.privateKey）、
// VLESS Encryption decryption（settings.decryption）。
func extractPrevInbound(raw json.RawMessage) (port int, privateKey, decryption string) {
	var ib struct {
		Port int `json:"port"`
		Settings struct {
			Decryption string `json:"decryption"`
		} `json:"settings"`
		StreamSettings struct {
			RealitySettings struct {
				PrivateKey string `json:"privateKey"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(raw, &ib); err != nil {
		return 0, "", ""
	}
	return ib.Port, ib.StreamSettings.RealitySettings.PrivateKey, ib.Settings.Decryption
}

// rebuildInbound 以保留模式重渲染一个 node inbound（重建专用，§docs/rebuild-xray-config-design.md）：
// 端口/私钥/decryption 优先复用 prev（备份）中的当前生效值——占位符预替换后
// fillTemplate 不再重新生成；prev 缺失时回退 fillTemplate 生成路径。
// minClientVer 由 fillTemplate 内 pinRealityMinClientVer 兜底注入。
func (m *Manager) rebuildInbound(tag string, vc shared.VirtualConfig, userUUIDs, destCandidates []string, portCandidates []int, prev json.RawMessage) (json.RawMessage, *shared.RealizedConfig, error) {
	port, privateKey, decryption := extractPrevInbound(prev)
	t := string(vc.Template)
	if privateKey != "" {
		t = strings.ReplaceAll(t, shared.PlaceholderRealityPrivateKey, privateKey)
	}
	if decryption != "" {
		t = strings.ReplaceAll(t, shared.PlaceholderVLessDecryption, decryption)
	}
	if port == 0 {
		var err error
		if port, err = m.pickPort(vc.Port, portCandidates); err != nil {
			return nil, nil, err
		}
	}
	rebuilt := vc
	rebuilt.Template = json.RawMessage(t)
	return m.fillTemplate(port, tag, rebuilt, userUUIDs, destCandidates, portCandidates)
}
```

- [ ] **Step 4: 运行确认通过**（测试里先桩 dest 探测）

在测试文件加一个桩（TestRebuildInboundPreservesPrevValues / WithoutPrevGenerates 开头各插一行）：

```go
	orig := destReachable
	destReachable = func(string, string) bool { return true }
	t.Cleanup(func() { destReachable = orig })
```

Run: `go test ./src/agent/internal/xray/ -run 'TestExtractPrevInbound|TestRebuildInbound' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/agent/internal/xray/fill.go src/agent/internal/xray/rebuild.go src/agent/internal/xray/rebuild_test.go
git commit -m "feat(agent): xray rebuild inbound render with prev value preservation"
```

---

### Task 3: agent RebuildXray 主流程（停服→备份→重建→规范化→校验→落盘→重启→自检→回滚）

**Files:**
- Modify: `src/agent/internal/xray/rebuild.go`
- Modify: `src/agent/internal/xray/rebuild_test.go`
- Test: `src/agent/internal/xray/rebuild_test.go`（扩展）

**Interfaces:**
- Consumes: `rebuildInbound`（Task 2）、`m.skeleton()`、`m.mergePieces()`、`m.chainPieces`、`m.runner.Stop/Restart/IsRunning`、`hashBytes`、`inboundTag`、`inboundPort`（现有）、`shared.NodeTag`、`pinRealityMinClientVer`（fill.go 现有，就地修改 inbound map）。
- Produces: `(*Manager).RebuildXray(p shared.RebuildXrayPayload) (*shared.RebuildXrayResult, error)`、`(*Manager).writeValidated(b []byte) error`、`(*Manager).selfCheck(p) []string`、`normalizeRebuiltConfig(fc fullConfig) fullConfig`、`normalizers []func(fullConfig) fullConfig`、`normalizeRealityMinClientVer(fc fullConfig) fullConfig`。Task 4 在 main.go 调用 `RebuildXray`。

- [ ] **Step 1: 写失败测试**（追加到 `rebuild_test.go`）

```go
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
```

（说明：seed 的 node_1 inbound 没有 privateKey，`extractPrevInbound` 取不到 → 走生成路径；断言分支覆盖两种可能。此测试重点在流程与回执契约。）

```go
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
	m.SetChainPieces([]state.ChainPiece{{HopID: 7, Kind: shared.HopKindPortal, Port: 20001, Inbound: oldPortal}})
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
	if !jsonContains(s, `"minClientVer":"0"`) {
		t.Fatalf("重建后链路 piece 应补全 minClientVer: %s", s)
	}
	if len(result.RebuiltPieces) != 1 || result.RebuiltPieces[0] != "portal/7" {
		t.Fatalf("回执 pieces = %v", result.RebuiltPieces)
	}
}
```

（需要 import：`state`、`errors`；`state` 用于 `state.ChainPiece`。）

注意：`TestRebuildXrayNormalizesReplayedPieces` 中 portal piece 只有 Inbound（无 Reverse/Rules），`mergePieces` 仅合并记录中存在的部分——若 `mergePieces` 对缺字段 piece 不完整合并导致自检失败，这是正常现象吗？**不是**：链 portal piece 应有 Inbound + Reverse + Rules。请检查 `state.ChainPiece` 结构与 `mergePieces` 实现（`chain.go:77-82`），按 `chain_test.go` 中真实 portal 记录补全该测试 piece 的字段（Reverse/Rules），保证 mergePieces 完整重放。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/agent/internal/xray/ -run 'TestRebuildXray' -v`
Expected: FAIL（RebuildXray 未定义）

- [ ] **Step 3: 实现**（追加到 `src/agent/internal/xray/rebuild.go`）

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lattix/shared"
)

// rebuildBackupSuffix 是重建前备份文件的缀名（失败回滚源）。
const rebuildBackupSuffix = ".rebuild.bak"

// RebuildXray 重建 xray.json（xray.rebuild，§docs/rebuild-xray-config-design.md）：
// 停止 xray → 备份 → 按面板下发节点规格重渲染 + 本地 chain piece 重放 →
// xray run -test 校验 → 原子落盘 → 重启 → 自检（期望 tag/piece 齐全 + 进程存活）。
// 任一步失败：恢复备份并重启，回执错误且 result.RolledBack=true；
// 回滚本身失败时错误信息显式标注备份路径（需人工处理）。
func (m *Manager) RebuildXray(p shared.RebuildXrayPayload) (*shared.RebuildXrayResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := &shared.RebuildXrayResult{
		RebuiltInbounds: []shared.RebuiltInbound{},
		RebuiltPieces:   []string{},
	}
	// 1. 停止 xray。
	if err := m.runner.Stop(context.Background()); err != nil {
		return result, fmt.Errorf("停止 xray 失败: %w", err)
	}
	// 2. 备份当前配置。
	backupPath := m.configPath + rebuildBackupSuffix
	hadPrev := false
	if b, err := os.ReadFile(m.configPath); err == nil {
		hadPrev = true
		if err := os.WriteFile(backupPath, b, 0o600); err != nil {
			_ = m.runner.Restart(context.Background())
			return result, fmt.Errorf("备份 xray.json 失败: %w", err)
		}
		if err := os.Chmod(backupPath, 0o600); err != nil {
			log.Printf("xray: secure rebuild backup: %v", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = m.runner.Restart(context.Background())
		return result, fmt.Errorf("读取 xray.json 失败: %w", err)
	}
	// 统一回滚：恢复备份（或无备份时删除新配置）→ 重启 → 更新漂移基线。
	rollback := func(cause error) (*shared.RebuildXrayResult, error) {
		result.RolledBack = true
		if hadPrev {
			if rerr := os.Rename(backupPath, m.configPath); rerr != nil {
				_ = m.runner.Restart(context.Background())
				return result, fmt.Errorf("重建失败：%v；回滚失败（备份位于 %s，需人工处理）: %w", cause, backupPath, rerr)
			}
			if cerr := os.Chmod(m.configPath, 0o600); cerr != nil {
				log.Printf("xray: secure restored config: %v", cerr)
			}
		} else {
			if rerr := os.Remove(m.configPath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				_ = m.runner.Restart(context.Background())
				return result, fmt.Errorf("重建失败：%v；回滚失败（清理新配置失败）: %w", cause, rerr)
			}
		}
		if rerr := m.runner.Restart(context.Background()); rerr != nil {
			return result, fmt.Errorf("重建失败：%v；且回滚后重启 xray 失败: %w", cause, rerr)
		}
		if b, err := os.ReadFile(m.configPath); err == nil {
			m.lastHash = hashBytes(b)
		}
		m.drifted = false
		return result, fmt.Errorf("重建失败：%v（已恢复备份 xray.json 并重启）", cause)
	}
	// 3. 重建候选：骨架 + 重渲染 node inbound（复用备份生效值）+ chain piece 重放。
	prevByTag := map[string]json.RawMessage{}
	if hadPrev {
		if b, err := os.ReadFile(backupPath); err == nil {
			var prev fullConfig
			if json.Unmarshal(b, &prev) == nil {
				for _, raw := range prev.inbounds() {
					prevByTag[inboundTag(raw)] = raw
				}
			}
		}
	}
	cand := m.skeleton()
	for _, spec := range p.Nodes {
		tag := shared.NodeTag(spec.NodeID)
		inbound, _, err := m.rebuildInbound(tag, spec.Config, spec.UserUUIDs,
			spec.DestCandidates, spec.PortCandidates, prevByTag[tag])
		if err != nil {
			return rollback(fmt.Errorf("重建节点 %s 失败: %w", tag, err))
		}
		cand = cand.upsertInbound(tag, inbound)
	}
	cand = m.mergePieces(cand)
	// 3b. 规范化扫描：补全 xray 版本升级后缺省的字段（minClientVer 等，全配置生效）。
	cand = normalizeRebuiltConfig(cand)
	// 4-5. 校验 + 原子落盘。
	b, err := json.MarshalIndent(cand, "", "  ")
	if err != nil {
		return rollback(fmt.Errorf("序列化重建配置失败: %w", err))
	}
	if err := m.writeValidated(b); err != nil {
		return rollback(err)
	}
	// 6. 重启。
	if err := m.runner.Restart(context.Background()); err != nil {
		return rollback(fmt.Errorf("重启 xray 失败: %w", err))
	}
	// 7. 自检。
	if missing := m.selfCheck(p); len(missing) > 0 {
		return rollback(fmt.Errorf("重建后自检缺失: %s", strings.Join(missing, ", ")))
	}
	if !m.runner.IsRunning(context.Background()) {
		return rollback(fmt.Errorf("重建后 xray 未在运行"))
	}
	// 8. 成功：清理备份，汇总回执。
	if hadPrev {
		if err := os.Remove(backupPath); err != nil {
			log.Printf("xray: remove rebuild backup: %v", err)
		}
	}
	for _, raw := range cand.inbounds() {
		result.RebuiltInbounds = append(result.RebuiltInbounds, shared.RebuiltInbound{
			Tag: inboundTag(raw), Port: inboundPort(raw), Kind: inboundProtocol(raw),
		})
	}
	for _, rec := range m.chainPieces {
		result.RebuiltPieces = append(result.RebuiltPieces, fmt.Sprintf("%s/%d", rec.Kind, rec.HopID))
	}
	log.Printf("xray: rebuild done inbounds=%d pieces=%d", len(result.RebuiltInbounds), len(result.RebuiltPieces))
	return result, nil
}

// inboundProtocol 提取 inbound 的协议（重建结果展示用）。
func inboundProtocol(raw json.RawMessage) string {
	var p struct {
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	return p.Protocol
}

// normalizers 是按序应用的全配置规范化器（重建专用，§docs/rebuild-xray-config-design.md）：
// 用于补全 xray 版本升级后缺省/新增的字段。每个规范化器必须幂等；
// 未来升级后出现同类问题，只需在此追加一个函数。
var normalizers = []func(fullConfig) fullConfig{
	normalizeRealityMinClientVer,
}

// normalizeRebuiltConfig 依次应用全部规范化器（跳过配置为 nil 的情况）。
func normalizeRebuiltConfig(fc fullConfig) fullConfig {
	for _, n := range normalizers {
		if n == nil {
			continue
		}
		fc = n(fc)
	}
	return fc
}

// normalizeRealityMinClientVer 遍历全部 inbound（含重放的链路/共享端点 piece），
// 为缺少 minClientVer 的 realitySettings 注入 "0"（复用 pinRealityMinClientVer；
// node 重渲染经 fillTemplate 已注入，此处幂等）。显式值原样保留。
func normalizeRealityMinClientVer(fc fullConfig) fullConfig {
	list := fc.inbounds()
	out := make([]json.RawMessage, 0, len(list))
	changed := false
	for _, raw := range list {
		var ib map[string]json.RawMessage
		if err := json.Unmarshal(raw, &ib); err != nil {
			out = append(out, raw)
			continue
		}
		before, _ := json.Marshal(ib)
		pinRealityMinClientVer(ib)
		after, _ := json.Marshal(ib)
		if string(before) != string(after) {
			changed = true
			out = append(out, after)
			continue
		}
		out = append(out, raw)
	}
	if !changed {
		return fc
	}
	nc := fc.clone()
	nc.setInbounds(out)
	return nc
}

// selfCheck 核对重建后配置：期望 inbound tag 全部存在、期望 piece 全部在
// chainPieces 记录中；返回缺失项列表（空 = 通过）。
func (m *Manager) selfCheck(p shared.RebuildXrayPayload) []string {
	cur, err := m.loadConfig()
	if err != nil {
		return []string{fmt.Sprintf("重新加载配置失败: %v", err)}
	}
	present := map[string]bool{}
	for _, raw := range cur.inbounds() {
		present[inboundTag(raw)] = true
	}
	var missing []string
	for _, tag := range p.ExpectedInboundTags {
		if !present[tag] {
			missing = append(missing, tag)
		}
	}
	have := map[string]bool{}
	for _, rec := range m.chainPieces {
		have[fmt.Sprintf("%s/%d", rec.Kind, rec.HopID)] = true
	}
	for _, key := range p.ExpectedPieces {
		if !have[key] {
			missing = append(missing, key)
		}
	}
	return missing
}

// writeValidated 校验并原子落盘重建配置（复用 commitConfig 的临时文件 +
// xray run -test 校验路径；不走 .prev 备份——重建自有 .rebuild.bak 回滚语义）。
func (m *Manager) writeValidated(b []byte) error {
	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if out, err := exec.Command(m.bin, "run", "-test", "-config", tmpPath).CombinedOutput(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("xray 配置校验失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmpPath, m.configPath); err != nil {
		return err
	}
	if err := os.Chmod(m.configPath, 0o600); err != nil {
		return err
	}
	m.lastHash = hashBytes(b)
	m.drifted = false
	return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/agent/internal/xray/ -run 'TestRebuildXray|TestRebuildInbound|TestExtractPrevInbound' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/agent/internal/xray/rebuild.go src/agent/internal/xray/rebuild_test.go
git commit -m "feat(agent): xray rebuild flow with backup, self-check and rollback"
```

---

### Task 4: agent main.go 分发 xray.rebuild

**Files:**
- Modify: `src/agent/cmd/agent/main.go`

**Interfaces:**
- Consumes: `mgr.RebuildXray`（Task 3）、`shared.ApplyResultPayload{Rebuild: ...}`（Task 1）。
- Produces: `xray.rebuild` 命令处理分支（回执 `ApplyResultPayload` 挂 `rebuild` 字段）。

- [ ] **Step 1: 实现**（在 `case shared.TypeCleanupXray:` 分支之后插入）

```go
	case shared.TypeRebuildXray:
		var p shared.RebuildXrayPayload
		if !parseData(sc, env, &p) {
			return
		}
		log.Printf("xray.rebuild request_id=%s nodes=%d expected_inbounds=%d expected_pieces=%d",
			env.RequestID, len(p.Nodes), len(p.ExpectedInboundTags), len(p.ExpectedPieces))
		result, err := mgr.RebuildXray(p)
		if err != nil {
			log.Printf("xray.rebuild failed request_id=%s: %v", env.RequestID, err)
		} else {
			persistChainPieces(statePath, st, mgr)
		}
		replyResult(sc, env, shared.ApplyResultPayload{Rebuild: result}, err)
```

- [ ] **Step 2: 编译验证**

Run: `go build ./src/agent/... && go vet ./src/agent/cmd/agent/`
Expected: 无输出（成功）

- [ ] **Step 3: 提交**

```bash
git add src/agent/cmd/agent/main.go
git commit -m "feat(agent): dispatch xray.rebuild command"
```

---

### Task 5: dispatcher RebuildXraySync（同步下发 + waiter 回执投递）

**Files:**
- Modify: `src/backend/internal/dispatch/dispatcher.go`
- Test: `src/backend/internal/dispatch/rebuild_xray_test.go`（新建）

**Interfaces:**
- Consumes: `shared.TypeRebuildXray`、`shared.RebuildXrayPayload`、`shared.RebuildXrayResult`（Task 1）、`d.st.EnqueueCommand/MarkCommandSent/CommandByRequestID`、`logging.TraceID`、`uninstallRetryDelay`、`uninstallMaxAttempts`（现有）。
- Produces: `(*Dispatcher).RebuildXraySync(ctx context.Context, serverID int64, payload shared.RebuildXrayPayload) (*shared.RebuildXrayResult, error)`。Task 6 的 `handleRebuildXray` 调用。

- [ ] **Step 1: 写失败测试** `src/backend/internal/dispatch/rebuild_xray_test.go`

```go
package dispatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestRebuildXraySyncDeliversResult 验证 xray.rebuild 同步回执流转：
// agent 回执重建结果 → waiter 投递 → 调用方拿到结果，命令照常 acked 落库。
func TestRebuildXraySyncDeliversResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, "agent", "agent.test", "token", store.MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)
	go func() {
		select {
		case <-requester.wake:
		case <-ctx.Done():
			return
		}
		requester.mu.Lock()
		request := requester.sent[len(requester.sent)-1]
		requester.mu.Unlock()
		dispatcher.HandleMessage(serverID, shared.Envelope{
			Kind: shared.KindResponse, Type: request.Type,
			RequestID: request.RequestID, TraceID: request.TraceID,
			Code: shared.CodeOK,
			Data: json.RawMessage(`{"rebuild":{"rebuilt_inbounds":[{"tag":"node_1","port":12345,"kind":"vless"}],"rebuilt_pieces":["forward/3"],"rolled_back":false}}`),
		})
	}()

	result, err := dispatcher.RebuildXraySync(ctx, serverID, shared.RebuildXrayPayload{
		ExpectedInboundTags: []string{"node_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RebuiltInbounds) != 1 || result.RebuiltInbounds[0].Tag != "node_1" ||
		result.RebuiltInbounds[0].Port != 12345 || len(result.RebuiltPieces) != 1 ||
		result.RebuiltPieces[0] != "forward/3" || result.RolledBack {
		t.Fatalf("result = %+v", result)
	}
	requester.mu.Lock()
	requestID := requester.sent[0].RequestID
	requester.mu.Unlock()
	command, err := st.CommandByRequestID(ctx, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != store.CommandStatusAcked || command.Type != shared.TypeRebuildXray {
		t.Fatalf("命令 = %s/%s", command.Status, command.Type)
	}
}

// TestRebuildXraySyncFailureReturnsError 验证 agent 回执失败时同步调用返回错误且命令落 failed。
func TestRebuildXraySyncFailureReturnsError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, "agent", "agent.test", "token", store.MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	requester := &uninstallRequester{wake: make(chan struct{}, 2)}
	dispatcher := New(st, requester)
	go func() {
		select {
		case <-requester.wake:
		case <-ctx.Done():
			return
		}
		requester.mu.Lock()
		request := requester.sent[len(requester.sent)-1]
		requester.mu.Unlock()
		dispatcher.HandleMessage(serverID, shared.Envelope{
			Kind: shared.KindResponse, Type: request.Type,
			RequestID: request.RequestID, TraceID: request.TraceID,
			Code: shared.CodeInternalError, Message: "重建失败：自检缺失 node_2（已恢复备份 xray.json 并重启）",
			Data: json.RawMessage(`{"rebuild":{"rebuilt_inbounds":[],"rebuilt_pieces":[],"rolled_back":true}}`),
		})
	}()

	result, err := dispatcher.RebuildXraySync(ctx, serverID, shared.RebuildXrayPayload{})
	if err == nil {
		t.Fatal("失败回执应返回错误")
	}
	if result == nil || !result.RolledBack {
		t.Fatalf("失败回执应携带 RolledBack=true 的结果，实际 %+v", result)
	}
	requester.mu.Lock()
	requestID := requester.sent[0].RequestID
	requester.mu.Unlock()
	command, err := st.CommandByRequestID(ctx, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != store.CommandStatusFailed {
		t.Fatalf("命令状态 = %s，期望 failed", command.Status)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/dispatch/ -run TestRebuildXraySync -v`
Expected: FAIL（RebuildXraySync 未定义）

- [ ] **Step 3: 实现**（`src/backend/internal/dispatch/dispatcher.go`）

**3a.** 在 `CleanupXraySync` 定义（约 231 行）后加：

```go
// RebuildXraySync 同步下发 xray.rebuild 并等待 agent 回执（面板「重建 xray 配置」，
// §docs/rebuild-xray-config-design.md）：命令照常落库，回执数据经进程内 waiter 投递。
// 重发复用同一 request id（agent 命令队列按 request id 幂等去重）。
func (d *Dispatcher) RebuildXraySync(ctx context.Context, serverID int64, payload shared.RebuildXrayPayload) (*shared.RebuildXrayResult, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal rebuild payload: %w", err)
	}
	requestID := shared.NewMessageID()
	traceID := logging.TraceID(ctx)
	if traceID == "" {
		traceID = shared.NewMessageID()
	}
	commandID, err := d.st.EnqueueCommand(ctx, requestID, traceID, serverID, shared.TypeRebuildXray, raw)
	if err != nil {
		return nil, err
	}
	envelope := shared.Envelope{
		Kind: shared.KindRequest, Type: shared.TypeRebuildXray,
		RequestID: requestID, TraceID: traceID, Data: raw,
	}
	waiter := d.registerRebuildWaiter(requestID)
	defer d.unregisterRebuildWaiter(requestID)
	for attempt := 1; attempt <= uninstallMaxAttempts; attempt++ {
		if err := d.st.MarkCommandSent(ctx, commandID); err != nil {
			return nil, err
		}
		if err := d.req.Send(ctx, serverID, envelope); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case out := <-waiter:
			return out.result, out.err
		case <-time.After(uninstallRetryDelay(attempt)):
			// 无回执则重发（同 request id，agent 幂等）
		}
	}
	return nil, fmt.Errorf("agent 未回执重建命令（已重试 %d 次）", uninstallMaxAttempts)
}
```

**3b.** 在 `registerCleanupWaiter/unregisterCleanupWaiter` 后加（与 cleanup waiter 平行结构）：

```go
type rebuildWaiterOut struct {
	result *shared.RebuildXrayResult
	err    error
}

func (d *Dispatcher) registerRebuildWaiter(requestID string) chan rebuildWaiterOut {
	ch := make(chan rebuildWaiterOut, 1)
	d.rebuildMu.Lock()
	d.rebuildWaiters[requestID] = ch
	d.rebuildMu.Unlock()
	return ch
}

func (d *Dispatcher) unregisterRebuildWaiter(requestID string) {
	d.rebuildMu.Lock()
	delete(d.rebuildWaiters, requestID)
	d.rebuildMu.Unlock()
}

// deliverRebuildResult 把 xray.rebuild 回执投递给同步等待者（handleCommandResponse 调用）。
func (d *Dispatcher) deliverRebuildResult(requestID string, result *shared.RebuildXrayResult, errorMessage string) {
	d.rebuildMu.Lock()
	ch, ok := d.rebuildWaiters[requestID]
	delete(d.rebuildWaiters, requestID)
	d.rebuildMu.Unlock()
	if !ok {
		return
	}
	out := rebuildWaiterOut{result: result}
	if errorMessage != "" {
		out.err = fmt.Errorf("%s", errorMessage)
	}
	ch <- out
}
```

**3c.** `Dispatcher` 结构体加字段（在现有 `cleanupMu`/`cleanupWaiters` 附近）：

```go
	rebuildMu      sync.Mutex
	rebuildWaiters map[string]chan rebuildWaiterOut
```

并在 `New` 初始化处加（紧跟 cleanupWaiters 初始化）：

```go
	d.rebuildWaiters = map[string]chan rebuildWaiterOut{}
```

**3d.** `handleCommandResponse` 两处分支（成功分支在 `deliverCleanupResult` 之后；失败分支同理）：

成功分支（`if cmd.Type == shared.TypeCleanupXray {` 块之后）：

```go
		if cmd.Type == shared.TypeRebuildXray {
			d.deliverRebuildResult(cmd.RequestID, p.Rebuild, "")
			log.Printf("dispatch: server %d: rebuild xray command %d acked", serverID, cmdID)
			return
		}
```

失败分支：

```go
		if cmd.Type == shared.TypeRebuildXray {
			d.deliverRebuildResult(cmd.RequestID, p.Rebuild, errorMessage)
			log.Printf("dispatch: server %d: rebuild xray command %d failed: %s", serverID, cmdID, errorMessage)
			return
		}
```

（`sync` 已在 dispatcher.go imports 中；确认 `log`、`time` 已导入。）

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/backend/internal/dispatch/ -run TestRebuildXraySync -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/dispatch/dispatcher.go src/backend/internal/dispatch/rebuild_xray_test.go
git commit -m "feat(dispatch): rebuild xray sync command with waiter delivery"
```

---

### Task 6: 面板 API（POST /api/server/rebuild-xray）

**Files:**
- Modify: `src/backend/internal/panel/servers.go`
- Modify: `src/backend/internal/panel/panel.go`
- Test: `src/backend/internal/panel/servers_rebuild_test.go`（新建）

**Interfaces:**
- Consumes: `s.disp.RebuildXraySync`（Task 5）、`s.st.ListNodes/NodeUserUUIDs/ExpectedXrayState/ServerByID`、`store.NodeStatusActive`、`destCandidates`（nodes.go 包级变量）、`shared.ListenCandidates/ParsePortRanges`。
- Produces: `handleRebuildXray(w, r)` + 路由注册。Task 7 前端调用该接口。

- [ ] **Step 1: 写失败测试** `src/backend/internal/panel/servers_rebuild_test.go`

```go
package panel

import (
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

// rebuildRecordingRequester 记录是否在线，并捕获下发的 xray.rebuild 载荷。
type rebuildRecordingRequester struct {
	settingsRequester
	rebuildPayload shared.RebuildXrayPayload
}

func (f *rebuildRecordingRequester) Send(_ context.Context, _ int64, env shared.Envelope) error {
	if env.Type == shared.TypeRebuildXray {
		var p shared.RebuildXrayPayload
		if err := json.Unmarshal(env.Data, &p); err == nil {
			f.rebuildPayload = p
		}
	}
	return nil
}

// seedRebuildServer 构造带 1 个活跃节点 + 1 个非活跃节点的服务器。
func seedRebuildServer(t *testing.T) (*Server, *rebuildRecordingRequester, *store.Store, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	requester := &rebuildRecordingRequester{settingsRequester: settingsRequester{online: map[int64]bool{}}}
	serverAPI := &Server{st: st, disp: dispatch.New(st, requester), req: requester}
	serverID, err := st.CreateServer(ctx, "s1", "s1.test", "tok", store.MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	requester.online[serverID] = true
	tmpl := json.RawMessage(`{
		"protocol": "dokodemo-door", "listen": "0.0.0.0", "port": 10001,
		"tag": "{{TAG}}", "settings": {"address": "1.1.1.1", "port": 53, "network": "tcp,udp"}
	}`)
	if _, err := st.CreateNode(ctx, serverID, 10001, shared.ProtocolDokodemo, "", tmpl, ""); err != nil {
		t.Fatal(err)
	}
	return serverAPI, requester, st, serverID
}

func TestHandleRebuildXrayOffline(t *testing.T) {
	serverAPI, requester, _, serverID := seedRebuildServer(t)
	requester.online[serverID] = false
	rec := httptest.NewRecorder()
	serverAPI.handleRebuildXray(rec, httptest.NewRequest(http.MethodPost, "/api/server/rebuild-xray",
		strings.NewReader(`{"server_id":1}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("离线状态码 = %d，期望 409，body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRebuildXrayCollectsActiveNodes(t *testing.T) {
	serverAPI, requester, st, _ := seedRebuildServer(t)
	ctx := context.Background()
	// 第二个非活跃节点（不进入重建清单）。
	tmpl := json.RawMessage(`{"protocol":"dokodemo-door","listen":"0.0.0.0","port":10002,
		"tag":"{{TAG}}","settings":{"address":"1.1.1.1","port":53,"network":"tcp,udp"}}`)
	nodeID, err := st.CreateNode(ctx, 1, 10002, shared.ProtocolDokodemo, "", tmpl, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeFailed(ctx, nodeID, "boom"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	serverAPI.handleRebuildXray(rec, httptest.NewRequest(http.MethodPost, "/api/server/rebuild-xray",
		strings.NewReader(`{"server_id":1}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，body = %s", rec.Code, rec.Body.String())
	}
	if len(requester.rebuildPayload.Nodes) != 1 || requester.rebuildPayload.Nodes[0].NodeID != 1 {
		t.Fatalf("重建节点 = %+v，期望仅活跃节点 1", requester.rebuildPayload.Nodes)
	}
	// 期望 tag 只含活跃节点 tag（非活跃节点 tag 被剔除），期望 pieces 为空数组非 null。
	tags := map[string]bool{}
	for _, tag := range requester.rebuildPayload.ExpectedInboundTags {
		tags[tag] = true
	}
	if !tags["node_1"] || tags["node_2"] {
		t.Fatalf("期望 tag = %v", requester.rebuildPayload.ExpectedInboundTags)
	}
	if requester.rebuildPayload.ExpectedPieces == nil {
		t.Fatalf("期望 pieces 应为 [] 而非 null")
	}
}
```

（需要确认 `st.CreateNode`/`st.SetNodeFailed` 签名与现有测试用法一致——参考 `src/backend/internal/panel/` 现有测试；如有出入按实际签名调整。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/backend/internal/panel/ -run TestHandleRebuildXray -v`
Expected: FAIL（handleRebuildXray 未定义；若 CreateNode/SetNodeFailed 签名不符，按编译错误修正测试）

- [ ] **Step 3: 实现**

**3a.** `src/backend/internal/panel/panel.go` 路由注册（`/api/server/cleanup-xray` 注册处之后，约 289 行）：

```go
	s.registerRPC(mux, http.MethodPost, "/api/server/rebuild-xray",
		func(w http.ResponseWriter, r *http.Request) { s.handleRebuildXray(w, r) })
```

**3b.** `src/backend/internal/panel/servers.go`（`handleCleanupXray` 之后加；确认 `strings` 已 import，未导入则补）：

```go
// handleRebuildXray 处理 POST /api/server/rebuild-xray（重建 xray 配置，
// §docs/rebuild-xray-config-design.md）：按当前生效配置（活跃节点 + 链路/共享端点）
// 重新生成 xray.json。服务器须在线；节点规格与期望集由面板从 DB 计算，
// 同步等待 agent 重建/回滚回执（90s 超时，含停服/重启）。
func (s *Server) handleRebuildXray(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID int64 `json:"server_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.ServerID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	srv, err := s.st.ServerByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.req.IsOnline(id) {
		writeError(w, http.StatusConflict, "服务器未连接，无法重建")
		return
	}
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload := shared.RebuildXrayPayload{}
	for _, n := range nodes {
		if n.ServerID != id || n.Status != store.NodeStatusActive {
			continue
		}
		var vc shared.VirtualConfig
		if err := json.Unmarshal(n.ConfigTemplate, &vc); err != nil {
			continue // 模板损坏的节点跳过（异常留在 nodes 表）
		}
		uuids, err := s.st.NodeUserUUIDs(r.Context(), n.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		spec := shared.ApplyNodePayload{
			NodeID: n.ID, Config: vc, UserUUIDs: uuids, DestCandidates: destCandidates,
		}
		if ranges, err := shared.ParsePortRanges(srv.AllowedPorts); err == nil && len(ranges) > 0 {
			spec.PortCandidates = shared.ListenCandidates(ranges)
		}
		payload.Nodes = append(payload.Nodes, spec)
	}
	tags, pieces, err := s.st.ExpectedXrayState(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 期望 inbound 集合剔除本次未下发的节点 tag（非活跃或模板损坏），
	// 否则 agent 自检会因缺失而误判失败。
	dispatched := map[string]bool{}
	for _, spec := range payload.Nodes {
		dispatched[shared.NodeTag(spec.NodeID)] = true
	}
	for _, tag := range tags {
		if strings.HasPrefix(tag, "node_") && !dispatched[tag] {
			continue
		}
		payload.ExpectedInboundTags = append(payload.ExpectedInboundTags, tag)
	}
	payload.ExpectedPieces = pieces
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	result, err := s.disp.RebuildXraySync(ctx, id, payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sid := id
	s.audit(r, "server.rebuild_xray", &sid, nil, map[string]any{
		"nodes": len(payload.Nodes), "inbounds": len(result.RebuiltInbounds),
		"pieces": len(result.RebuiltPieces), "rolled_back": result.RolledBack,
	})
	writeJSON(w, http.StatusOK, result)
}
```

（`ServerByID` 返回的 server 类型为 `store.Server`，含 `AllowedPorts`；`context`、`time` 已在 servers.go 导入。）

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/backend/internal/panel/ -run TestHandleRebuildXray -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/backend/internal/panel/servers.go src/backend/internal/panel/panel.go src/backend/internal/panel/servers_rebuild_test.go
git commit -m "feat(panel): rebuild xray config API"
```

---

### Task 7: openapi 契约 + 前端（按钮/对话框）

**Files:**
- Modify: `docs/openapi.yaml`
- Modify: `src/frontend/src/lib/types.ts`
- Modify: `src/frontend/src/lib/api.ts`
- Modify: `src/frontend/src/pages/Servers.tsx`
- Modify: `src/frontend/src/components/ServerMonitor.tsx`
- Regenerate: `src/frontend/src/lib/api-contract.generated.ts`（`npm run generate:api`）

**Interfaces:**
- Consumes: `POST /api/server/rebuild-xray`（Task 6）、`RebuildXrayResult`/`RebuiltInbound` 类型。

- [ ] **Step 1: openapi.yaml 加路径**（`/api/server/cleanup-xray` 块之后，约 199 行）：

```yaml
  /api/server/rebuild-xray:
    post:
      operationId: serverRebuildXray
      parameters:
        - {$ref: '#/components/parameters/CSRFToken'}
        - {$ref: '#/components/parameters/IdempotencyKey'}
      requestBody: {$ref: '#/components/requestBodies/RPCBody'}
      responses: {'200': {$ref: '#/components/responses/RPCResponse'}, default: {$ref: '#/components/responses/ProtocolErrorResponse'}}
```

- [ ] **Step 2: 生成契约**

Run（src/frontend 目录）: `npm run generate:api`
Expected: `src/frontend/src/lib/api-contract.generated.ts` 更新，含 `serverRebuildXray`。若生成器校验失败按输出修正。

- [ ] **Step 3: types.ts 加类型**（`CleanupXrayResult` 之后，约 388 行）：

```ts
export interface RebuiltInbound {
  tag: string
  port: number
  kind: string
}

export interface RebuildXrayResult {
  rebuilt_inbounds: RebuiltInbound[]
  rebuilt_pieces: string[]
  rolled_back: boolean
}
```

- [ ] **Step 4: api.ts 加方法**（`cleanupXray` 之后，约 152 行；import 加 `RebuildXrayResult`）：

```ts
  rebuildXray: (serverId: number) =>
    requester.post<RebuildXrayResult>('/api/server/rebuild-xray', {
      server_id: serverId,
    }),
```

- [ ] **Step 5: Servers.tsx 加状态与处理**（`normalizeCleanup` 之后加；`onCleanupXray` 之后加 `onRebuildXray`）：

```ts
  // 重建 xray 配置（xray.rebuild）：单步确认执行，回执展示重建结果或回滚提示。
  const [rebuildTarget, setRebuildTarget] = useState<Server | null>(null)
  const [rebuildResult, setRebuildResult] = useState<RebuildXrayResult | null>(null)
  const [rebuildDone, setRebuildDone] = useState(false)
  const [rebuildBusy, setRebuildBusy] = useState(false)
  const [rebuildError, setRebuildError] = useState('')
  const normalizeRebuild = (r: RebuildXrayResult): RebuildXrayResult => ({
    rebuilt_inbounds: r.rebuilt_inbounds ?? [],
    rebuilt_pieces: r.rebuilt_pieces ?? [],
    rolled_back: r.rolled_back ?? false,
  })

  const onRebuildXray = (s: Server) => {
    setRebuildTarget(s)
    setRebuildResult(null)
    setRebuildDone(false)
    setRebuildError('')
  }

  const runRebuildXray = async () => {
    if (!rebuildTarget) {
      return
    }
    setRebuildBusy(true)
    setRebuildError('')
    try {
      setRebuildResult(normalizeRebuild(await api.rebuildXray(rebuildTarget.id)))
      setRebuildDone(true)
      load()
    } catch (err) {
      setRebuildError(errorMessage(err))
    } finally {
      setRebuildBusy(false)
    }
  }
```

（import 列表加 `RebuildXrayResult`。）

`<ServerMonitor ... />` 调用处（约 767 行，`onCleanupXray={onCleanupXray}` 之后）加：

```tsx
        onRebuildXray={onRebuildXray}
```

**5b. 对话框**（cleanup 对话框之后，约 1276 行）：

```tsx
      <Dialog open={rebuildTarget !== null} onOpenChange={(next) => !next && setRebuildTarget(null)}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>重建 Xray 配置</DialogTitle>
            <DialogDescription>
              {rebuildDone
                ? `「${rebuildTarget?.alias}」已按当前生效的链路与用户配置重建 xray.json。`
                : `将停止「${rebuildTarget?.alias}」的 xray 服务，备份并重新生成 xray.json（保留现有私钥与端口），校验后重启并自检；失败会自动恢复备份。重建期间该服务器代理不可用。`}
            </DialogDescription>
          </DialogHeader>
          {rebuildError ? <p className="text-sm text-destructive whitespace-pre-wrap">{rebuildError}</p> : null}
          {rebuildBusy ? (
            <p className="text-sm text-muted-foreground">正在向 agent 下发重建…</p>
          ) : rebuildResult ? (
            <div className="space-y-3">
              {rebuildResult.rolled_back ? (
                <p className="text-sm text-destructive">
                  重建失败，已恢复重建前的 xray.json 并重启 xray。
                </p>
              ) : (
                <>
                  <p className="text-sm text-muted-foreground">
                    已重建 {rebuildResult.rebuilt_inbounds.length} 个监听与 {rebuildResult.rebuilt_pieces.length} 个链路配置件。
                  </p>
                  {rebuildResult.rebuilt_inbounds.length > 0 ? (
                    <ul className="max-h-48 space-y-1 overflow-y-auto rounded-md border p-2 font-mono text-xs">
                      {rebuildResult.rebuilt_inbounds.map((inbound) => (
                        <li key={inbound.tag} className="flex items-center justify-between gap-3">
                          <span className="truncate">{inbound.tag}</span>
                          <span className="shrink-0 text-muted-foreground">:{inbound.port || '?'}</span>
                        </li>
                      ))}
                    </ul>
                  ) : null}
                </>
              )}
            </div>
          ) : null}
          <DialogFooter>
            {!rebuildDone ? (
              <>
                <Button variant="outline" disabled={rebuildBusy} onClick={() => setRebuildTarget(null)}>
                  取消
                </Button>
                <Button variant={rebuildTarget?.online ? 'default' : 'outline'} disabled={rebuildBusy} onClick={runRebuildXray}>
                  {rebuildBusy ? '重建中…' : '确认重建'}
                </Button>
              </>
            ) : (
              <Button onClick={() => setRebuildTarget(null)}>完成</Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
```

（`Server` 类型的在线判定字段名以现有代码为准——cleanup 菜单项用 `isServerOnline(server)`；若 Server 无 `online` 字段，把按钮 variant 简化回 `default`。）

- [ ] **Step 6: ServerMonitor.tsx 加菜单项**（`ServerMonitorProps` 约 76 行加 `onRebuildXray: (server: Server) => void`；`ServerActions` 解构处 352 行附近加；菜单项在「清理 Xray 缓存」之后约 392 行加；`ServerActions` 调用处约 1108 行透传）：

```tsx
          {isServerOnline(server) ? (
            <DropdownMenuItem onClick={() => onRebuildXray(server)}>
              <RotateCcwIcon />
              重建 Xray 配置
            </DropdownMenuItem>
          ) : null}
```

（`RotateCcwIcon` 若未导入则用 `RefreshCwIcon`——两者在文件中已有一个被导入，优先复用已导入图标。）

- [ ] **Step 7: 验证**

Run（src/frontend）: `npm run check:api && npm test && npx tsc -b`
Expected: 全部通过

- [ ] **Step 8: 提交**

```bash
git add docs/openapi.yaml src/frontend/src/lib/api-contract.generated.ts src/frontend/src/lib/types.ts src/frontend/src/lib/api.ts src/frontend/src/pages/Servers.tsx src/frontend/src/components/ServerMonitor.tsx
git commit -m "feat(frontend): rebuild xray config button and result dialog"
```

---

### Task 8: 全量验证与收尾

**Files:** 无新增（按需修复）

- [ ] **Step 1: gofmt 检查**

Run: `gofmt -l src/` 
Expected: 无输出；有输出则 `gofmt -w` 修复后提交

- [ ] **Step 2: 全量 Go 测试**

Run: `go test ./src/shared/... ./src/agent/... ./src/backend/...`
Expected: 全部 PASS

- [ ] **Step 3: race 测试**

Run: `go test -race ./src/shared/... ./src/agent/... ./src/backend/...`
Expected: 全部 PASS

- [ ] **Step 4: 前端全量**

Run（src/frontend）: `npm run check:api && npm test`
Expected: 全部通过

- [ ] **Step 5: 如有修复则提交**

```bash
git add -A
git commit -m "fix: review cleanups for xray rebuild"
```

---

## Self-Review 记录

- **Spec 覆盖**：协议（Task 1）✓、agent 流程含私钥/decryption/端口保留（Task 2-3）✓、自检+回滚（Task 3）✓、面板 API+在线检查+期望集剔除（Task 6）✓、前端按钮/对话框（Task 7）✓、openapi（Task 7）✓、错误矩阵（Task 3 rollback 标注）✓、minClientVer 注入（Task 2 断言 + 既有 TestPinRealityMinClientVer）✓、规范化扫描含重放 piece 补全（Task 3 追加）✓、测试三层（Task 1/2/3/5/6/7）✓。
- **类型一致性**：`RebuildXrayPayload.Nodes []ApplyNodePayload`（Task 1）→ Task 6 构造；`RebuildXrayResult` 字段贯穿 Task 1/3/5/7；`rebuildInbound` 签名 Task 2 定义、Task 3 调用；`RebuildXraySync` 签名 Task 5 定义、Task 6 调用。
- **占位符**：无 TBD；测试中的 `contains` 辅助函数已注明改为 `bytes.Contains`；`TestRebuildInboundWithoutPrevGenerates` 的 decmkem 修正已注明。
