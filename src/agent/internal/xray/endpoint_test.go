package xray

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lattix/shared"
)

func TestSharedEndpointRouteOutboundUsesTunnelCredential(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 7, TargetAddress: "127.0.0.1", TargetPort: 21000,
		TunnelUUID: "route-uuid",
		Target: shared.RealizedConfig{Network: shared.NetworkTCP, PublicKey: "public",
			ShortID: "short", ServerName: "example.com", Flow: "xtls-rprx-vision"},
	}
	outbound := renderSharedEndpointOutbound(route, "route")
	vnext, _ := nested(outbound, "settings")["vnext"].([]map[string]any)
	if len(vnext) != 1 || vnext[0]["address"] != "127.0.0.1" || vnext[0]["port"] != 21000 {
		t.Fatalf("vnext = %+v", vnext)
	}
	users, _ := vnext[0]["users"].([]map[string]any)
	if len(users) != 1 || users[0]["id"] != "route-uuid" || users[0]["flow"] != "xtls-rprx-vision" {
		t.Fatalf("users = %+v", users)
	}
	if reality := nested(outbound, "streamSettings", "realitySettings"); reality["publicKey"] != "public" {
		t.Fatalf("reality settings = %+v", reality)
	}
}

func TestSharedEntryForwardIsLoopbackOnly(t *testing.T) {
	inbound := renderForwardInbound(&shared.ForwardSpec{LocalOnly: true, TargetAddress: "exit", TargetPort: 443},
		"chain", 12000)
	if inbound["listen"] != "127.0.0.1" {
		t.Fatalf("listen = %v", inbound["listen"])
	}
}

// newTestEndpointManager 构造可执行 ApplySharedEndpoint 的 Manager：
// 假 xray 脚本只做 `run -test` 校验（exit 0），模板不含 Reality/Encryption
// 占位符，避免依赖 x25519/vlessenc/dest 预检。
func newTestEndpointManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "xray")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewManager(bin, filepath.Join(dir, "xray.json"), "127.0.0.1:19085", &telemetryTestRunner{})
}

func endpointPortPayload(endpointID int64, candidates []int) shared.ApplySharedEndpointPayload {
	return shared.ApplySharedEndpointPayload{
		EndpointID: endpointID,
		Config: shared.VirtualConfig{
			Protocol: shared.ProtocolVLESS,
			Template: json.RawMessage(`{
				"tag": "{{TAG}}", "listen": "0.0.0.0", "port": "{{PORT}}",
				"protocol": "vless", "settings": {"clients": "{{CLIENTS}}"},
				"streamSettings": {"network": "tcp"}
			}`),
		},
		PortCandidates: candidates,
	}
}

// TestEndpointPortCandidatesAutoPort 验证端口候选决策（确定性，不依赖宿主机 443 状态）：
// 端口留空且面板未下发候选（普通直连机）→ 空候选，由 pickPort 挑随机空闲端口；
// NAT 受限机 → 透传面板段内候选。
func TestEndpointPortCandidatesAutoPort(t *testing.T) {
	if got := endpointPortCandidates(0, nil, nil); len(got) != 0 {
		t.Fatalf("端口留空且无候选应返回空候选（挑随机端口），实际 %v", got)
	}
	if got := endpointPortCandidates(0, []int{10001, 10002}, nil); len(got) != 2 {
		t.Fatalf("NAT 段内候选应透传，实际 %v", got)
	}
	if got := endpointPortCandidates(443, nil, nil); len(got) != 0 {
		t.Fatalf("显式端口时无候选逻辑，实际 %v", got)
	}
}

// TestApplySharedEndpointAutoPortNo443Preference 验证端口留空（直连机无面板候选）时
// agent 自行挑选随机空闲端口，不再默认 443。
func TestApplySharedEndpointAutoPortNo443Preference(t *testing.T) {
	mgr := newTestEndpointManager(t)
	payload := endpointPortPayload(8, nil)

	realized, err := mgr.ApplySharedEndpoint(payload)
	if err != nil {
		t.Fatal(err)
	}
	if realized.Port <= 0 {
		t.Fatalf("端口留空应落地实际端口，当前 %d", realized.Port)
	}
	// 443 空闲时旧实现会固定默认 443；断言自动端口不再落在 443。
	// （443 被占用时旧实现本就回退随机端口，此时断言无区分度，跳过。）
	if probe, probeErr := net.Listen("tcp", ":443"); probeErr == nil {
		probe.Close()
		if realized.Port == 443 {
			t.Fatal("端口留空且无候选时应挑随机空闲端口，实际默认了 443")
		}
	}

	// 幂等重发：复用已落地端口（自动端口不因重发漂移）。
	again, err := mgr.ApplySharedEndpoint(payload)
	if err != nil {
		t.Fatal(err)
	}
	if again.Port != realized.Port {
		t.Fatalf("重发应复用端口 %d，实际 %d", realized.Port, again.Port)
	}
}

// TestApplySharedEndpointReapplyWhilePortHeld 验证重发幂等：端点已落地端口且
// xray 运行中（端口被持有）时，重发不应因占用探测误判失败（§21 重发复用端口与密钥）。
// 回归场景：面板在每次链路发布/用户分配/自愈时重发 apply_shared_endpoint，
// 旧实现 pickPort 对已落地端口做 net.Listen 探测，被自身运行中的 xray 持有 → 假失败。
func TestApplySharedEndpointReapplyWhilePortHeld(t *testing.T) {
	mgr := newTestEndpointManager(t)
	payload := endpointPortPayload(10, nil)

	realized, err := mgr.ApplySharedEndpoint(payload)
	if err != nil {
		t.Fatal(err)
	}

	// 模拟运行中的 xray 持有该监听端口。
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", realized.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	again, err := mgr.ApplySharedEndpoint(payload)
	if err != nil {
		t.Fatalf("重发应在端口被自身 xray 持有时复用端口成功: %v", err)
	}
	if again.Port != realized.Port {
		t.Fatalf("重发应复用已落地端口 %d，实际 %d", realized.Port, again.Port)
	}
}

// TestApplySharedEndpointPreservesEncryption 验证重发保留 VLESS Encryption 密钥对：
// 用户分配扇出会整端点重发 apply_shared_endpoint（ReconcileSharedEndpoint），
// 若每次重发都重新执行 xray vlessenc，realized 的客户端字符串随之轮换，
// 已发布订阅立即失效。decryption 须随 prev inbound 预替换进模板，客户端字符串
// 随 state 复用——重发后 realized.Encryption 不变且不再调用 vlessenc。
func TestApplySharedEndpointPreservesEncryption(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "vlessenc.count")
	bin := filepath.Join(dir, "xray")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  vlessenc)
    n=$(cat "%[1]s" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > "%[1]s"
    echo 'Authentication: ML-KEM-768'
    echo "\"decryption\": \"dec$n\""
    echo "\"encryption\": \"enc$n\"";;
  *) exit 0;;
esac
`, counter)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(bin, filepath.Join(dir, "xray.json"), "127.0.0.1:19085", &telemetryTestRunner{})
	payload := shared.ApplySharedEndpointPayload{
		EndpointID: 11,
		Config: shared.VirtualConfig{
			Protocol:   shared.ProtocolVLESS,
			Encryption: shared.VLessEncMLKEM768,
			Template: json.RawMessage(`{
				"tag": "{{TAG}}", "listen": "0.0.0.0", "port": "{{PORT}}",
				"protocol": "vless",
				"settings": {"clients": "{{CLIENTS}}", "decryption": "{{DECRYPTION}}"},
				"streamSettings": {"network": "httpupgrade", "httpupgradeSettings": {"path": "/hu"}}
			}`),
		},
	}

	first, err := mgr.ApplySharedEndpoint(payload)
	if err != nil {
		t.Fatal(err)
	}
	if first.Encryption != "enc1" {
		t.Fatalf("首发应使用 vlessenc 生成的客户端字符串，实际 %q", first.Encryption)
	}

	// 模拟用户分配扇出触发的重发（仅 Clients 变化）。
	payload.Clients = []shared.ClientCredential{{ID: "access-1", Email: "access:1"}}
	again, err := mgr.ApplySharedEndpoint(payload)
	if err != nil {
		t.Fatal(err)
	}
	if again.Encryption != first.Encryption {
		t.Fatalf("重发不得轮换 Encryption 客户端字符串：%q → %q", first.Encryption, again.Encryption)
	}
	if n, _ := os.ReadFile(counter); strings.TrimSpace(string(n)) != "1" {
		t.Fatalf("重发不应再执行 xray vlessenc，计数 = %q", strings.TrimSpace(string(n)))
	}
	// 落地 inbound 的 decryption 同样稳定（服务端私钥侧未轮换）。
	var ib struct {
		Settings struct {
			Decryption string `json:"decryption"`
		} `json:"settings"`
	}
	piece := mgr.findChainPiece(11, sharedEndpointPieceKind)
	if piece == nil || json.Unmarshal(piece.Inbound, &ib) != nil || ib.Settings.Decryption != "dec1" {
		t.Fatalf("重发后 inbound decryption 应仍为 dec1: %+v", piece)
	}
}

// TestApplySharedEndpointHonorsNatCandidates 验证 NAT 受限机：面板下发段内候选时按序挑选。
func TestApplySharedEndpointHonorsNatCandidates(t *testing.T) {
	mgr := newTestEndpointManager(t)
	free, err := mgr.pickPort(0, nil, "", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	realized, err := mgr.ApplySharedEndpoint(endpointPortPayload(9, []int{free}))
	if err != nil {
		t.Fatal(err)
	}
	if realized.Port != free {
		t.Fatalf("NAT 段内候选应被采用，期望 %d 实际 %d", free, realized.Port)
	}
}

// TestApplyNodeAutoPortReusesOwnManagedPort 验证单候选端口 NAT 机上同节点重发
// （配置变更/重试/重建）时，候选端口虽被自身受管 inbound 持有仍可复用：
// pickPort 自动路径须按 tag 区分——同 tag（本次将替换的 inbound）可复用，
// 而不是一律跳过导致"候选端口全部被占用（1 个）"。
func TestApplyNodeAutoPortReusesOwnManagedPort(t *testing.T) {
	mgr := newTestEndpointManager(t)
	vc := shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Port: 0,
		Template: json.RawMessage(`{
			"tag": "{{TAG}}", "listen": "0.0.0.0", "port": "{{PORT}}",
			"protocol": "vless", "settings": {"clients": "{{CLIENTS}}"},
			"streamSettings": {"network": "tcp"}
		}`),
	}
	realized, err := mgr.ApplyNode(42, vc, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟运行中的 xray 持有该端口（同节点旧 inbound 未清理）。
	ln := listenOn(t, realized.Port)
	defer ln.Close()

	// 同节点重发：候选仅剩自身持有的端口 → 应复用而非报"候选端口全部被占用"。
	again, err := mgr.ApplyNode(42, vc, nil, nil, []int{realized.Port})
	if err != nil {
		t.Fatalf("同节点重发应复用自身受管端口: %v", err)
	}
	if again.Port != realized.Port {
		t.Fatalf("重发应复用端口 %d，实际 %d", realized.Port, again.Port)
	}
}

// TestApplyNodeAutoPortBlockedByOtherManagedInbound 验证其他受管配置持有候选端口时
// 仍拒绝自动挑选（不同 inbound 不得共用一个监听端口），错误信息区分"其他受管配置"，
// 便于用户在单端口 NAT 机上定位是别的部署占用了端口。
func TestApplyNodeAutoPortBlockedByOtherManagedInbound(t *testing.T) {
	mgr := newTestEndpointManager(t)
	vc := shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Port: 0,
		Template: json.RawMessage(`{
			"tag": "{{TAG}}", "listen": "0.0.0.0", "port": "{{PORT}}",
			"protocol": "vless", "settings": {"clients": "{{CLIENTS}}"},
			"streamSettings": {"network": "tcp"}
		}`),
	}
	realized, err := mgr.ApplyNode(7, vc, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ApplyNode(8, vc, nil, nil, []int{realized.Port}); err == nil {
		t.Fatal("其他节点持有的候选端口应拒绝自动挑选")
	} else if !strings.Contains(err.Error(), "其他受管配置") {
		t.Fatalf("错误应区分其他受管配置占用，实际: %v", err)
	}
}

// TestApplySharedEndpointRestartFailureReportsDetail 验证重启失败时错误消息携带
// runner 的真实原因（stderr/journal 详情），且配置回滚到上一份。
func TestApplySharedEndpointRestartFailureReportsDetail(t *testing.T) {
	mgr := newTestEndpointManager(t)
	payload := endpointPortPayload(11, nil)
	if _, err := mgr.ApplySharedEndpoint(payload); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(mgr.configPath)
	if err != nil {
		t.Fatal(err)
	}
	mgr.runner = &failingRestartRunner{}
	_, err = mgr.ApplySharedEndpoint(payload)
	if err == nil {
		t.Fatal("重启失败应返回错误")
	}
	if !strings.Contains(err.Error(), "restart boom") {
		t.Fatalf("错误应包含 runner 详情: %v", err)
	}
	restored, err := os.ReadFile(mgr.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(first) {
		t.Fatal("失败后配置应回滚到上一份")
	}
}

// TestRenderSharedEndpointOutboundPlain 验证端点路由 outbound 按出口 realized 分派：
// 出口无 reality 公钥（ws/httpupgrade + none）时不输出 realitySettings，
// 传输子段走 wsSettings/httpupgradeSettings；VLESS Encryption 随 user.encryption 携带。
func TestRenderSharedEndpointOutboundPlain(t *testing.T) {
	route := shared.SharedEndpointRoute{
		ChainID: 1, TargetAddress: "127.0.0.1", TargetPort: 1443, TunnelUUID: "t-uuid",
		Target: shared.RealizedConfig{Network: shared.NetworkWS, Path: "/p", Host: "h.example.com",
			Security: shared.SecurityNone, Encryption: "mlkem768x25519plus.0rtt.XXX"},
	}
	ob := renderSharedEndpointOutbound(route, "shared_endpoint_route_1_1")
	stream := nested(ob, "streamSettings")
	if stream["security"] != "none" {
		t.Errorf("无公钥出口应为 security=none: %v", stream)
	}
	if _, ok := stream["realitySettings"]; ok {
		t.Error("security=none 不应输出 realitySettings")
	}
	ws, ok := stream["wsSettings"].(map[string]any)
	if !ok || ws["path"] != "/p" {
		t.Fatalf("wsSettings 不符: %v", stream)
	}
	headers, _ := ws["headers"].(map[string]any)
	if headers["Host"] != "h.example.com" {
		t.Errorf("ws headers.Host 不符: %v", ws)
	}
	settings := nested(ob, "settings")
	vnext := settings["vnext"].([]map[string]any)[0]
	user := vnext["users"].([]map[string]any)[0]
	if user["encryption"] != "mlkem768x25519plus.0rtt.XXX" {
		t.Errorf("隧道身份应携带 Encryption 客户端字符串: %v", user)
	}
}
