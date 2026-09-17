package xray

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"testing"

	"lattix/agent/internal/state"
	"lattix/shared"
)

// 以下渲染形状对照 scripts/dev/poc-reverse.sh（已验证 GO 的 PoC）逐字段断言。

func mustUnmarshal(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// nested 按路径取嵌套 map 字段。
func nested(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		v, ok := m[k].(map[string]any)
		if !ok {
			return nil
		}
		m = v
	}
	return m
}

// TestRenderPortalInbound 对照 PoC portal.json 的 interconn inbound 断言关键字段。
func TestRenderPortalInbound(t *testing.T) {
	spec := &shared.PortalSpec{
		TunnelDomain: "c1h2.lx",
		TunnelUUID:   "t-uuid",
		ShortID:      "0123abcd",
		Dest:         "dl.google.com:443",
		ServerNames:  []string{"dl.google.com"},
	}
	ib := renderPortalInbound(spec, shared.ChainPortalTag(2), 11001, "priv-key")

	if ib["tag"] != "chainportal_2" || ib["listen"] != "0.0.0.0" || ib["protocol"] != "vless" {
		t.Fatalf("inbound 顶层字段不符: %v", ib)
	}
	if ib["port"] != 11001 {
		t.Fatalf("port 不符: %v", ib["port"])
	}
	settings := nested(ib, "settings")
	if settings["decryption"] != "none" {
		t.Fatalf("decryption 不符: %v", settings)
	}
	clients, _ := settings["clients"].([]map[string]any)
	if len(clients) != 1 || clients[0]["id"] != "t-uuid" {
		t.Fatalf("clients 不符: %v", settings["clients"])
	}
	rs := nested(ib, "streamSettings", "realitySettings")
	if rs == nil {
		t.Fatalf("缺少 realitySettings: %v", ib)
	}
	if rs["dest"] != "dl.google.com:443" || rs["privateKey"] != "priv-key" || rs["show"] != false {
		t.Fatalf("realitySettings 不符: %v", rs)
	}
	names, _ := rs["serverNames"].([]string)
	if len(names) != 1 || names[0] != "dl.google.com" {
		t.Fatalf("serverNames 不符: %v", rs["serverNames"])
	}
	sids, _ := rs["shortIds"].([]string)
	if len(sids) != 1 || sids[0] != "0123abcd" {
		t.Fatalf("shortIds 不符: %v", rs["shortIds"])
	}
	if rs["minClientVer"] != "0" {
		t.Fatalf("minClientVer 应显式为 0（26.7.11+ 默认 26.3.27 会拒绝旧客户端）: %v", rs["minClientVer"])
	}
	ss := nested(ib, "streamSettings")
	if ss["network"] != "tcp" || ss["security"] != "reality" {
		t.Fatalf("streamSettings 不符: %v", ss)
	}
}

// TestRenderBridgeOutbound 对照 PoC bridge.json 的 interconn outbound 断言关键字段。
func TestRenderBridgeOutbound(t *testing.T) {
	spec := &shared.BridgeSpec{
		TunnelDomain:  "c1h2.lx",
		PortalAddress: "portal.example.com",
		PortalPort:    11001,
		TunnelUUID:    "t-uuid",
		PublicKey:     "pub-key",
		ShortID:       "0123abcd",
		ServerName:    "dl.google.com",
	}
	ob := renderBridgeOutbound(spec, shared.ChainBridgeTag(3))

	if ob["tag"] != "chainbr_3" || ob["protocol"] != "vless" {
		t.Fatalf("outbound 顶层字段不符: %v", ob)
	}
	vnext, _ := nested(ob, "settings")["vnext"].([]map[string]any)
	if len(vnext) != 1 || vnext[0]["address"] != "portal.example.com" {
		t.Fatalf("vnext 不符: %v", vnext)
	}
	if vnext[0]["port"] != 11001 {
		t.Fatalf("vnext port 不符: %v", vnext[0])
	}
	users, _ := vnext[0]["users"].([]map[string]any)
	if len(users) != 1 || users[0]["id"] != "t-uuid" || users[0]["encryption"] != "none" {
		t.Fatalf("vnext users 不符: %v", users)
	}
	rs := nested(ob, "streamSettings", "realitySettings")
	if rs == nil || rs["serverName"] != "dl.google.com" || rs["publicKey"] != "pub-key" ||
		rs["shortId"] != "0123abcd" || rs["fingerprint"] != "chrome" {
		t.Fatalf("realitySettings 不符: %v", rs)
	}
}

// TestPinRealityMinClientVer 验证填充兜底：旧面板生成的 Reality 模板缺 minClientVer，
// 26.7.11+ xray 缺省默认 26.3.27 会拒绝旧客户端，须注入显式 0；已显式声明的不覆盖。
func TestPinRealityMinClientVer(t *testing.T) {
	asRaw := func(t *testing.T, s string) map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	check := func(t *testing.T, tmpl map[string]json.RawMessage, want string) {
		t.Helper()
		b, err := json.Marshal(tmpl)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		rs := nested(m, "streamSettings", "realitySettings")
		if rs == nil {
			t.Fatalf("缺少 realitySettings: %v", m)
		}
		if rs["minClientVer"] != want {
			t.Fatalf("minClientVer = %v, want %q: %v", rs["minClientVer"], want, m)
		}
	}

	// 旧模板缺 minClientVer → 注入 "0"。
	tmpl := asRaw(t, `{
		"tag": "node_1", "port": 443, "protocol": "vless",
		"streamSettings": {"network": "tcp", "security": "reality",
			"realitySettings": {"show": false, "dest": "dl.google.com:443"}}
	}`)
	pinRealityMinClientVer(tmpl)
	check(t, tmpl, "0")

	// 已显式声明（面板新模板）→ 原样保留。
	explicit := asRaw(t, `{
		"tag": "node_2", "port": 443, "protocol": "vless",
		"streamSettings": {"network": "tcp", "security": "reality",
			"realitySettings": {"minClientVer": "0", "dest": "dl.google.com:443"}}
	}`)
	pinRealityMinClientVer(explicit)
	check(t, explicit, "0")

	// 非 reality 模板 → 不动。
	plain := asRaw(t, `{"tag": "node_3", "port": 8388, "protocol": "shadowsocks"}`)
	pinRealityMinClientVer(plain)
	if _, ok := plain["streamSettings"]; ok {
		t.Fatalf("非 reality 模板不应被改动: %v", plain)
	}
}

// TestRenderForwardInbound 对照 PoC 的 entry dokodemo-door inbound 断言关键字段。
func TestRenderForwardInbound(t *testing.T) {
	spec := &shared.ForwardSpec{TargetAddress: "127.0.0.1", TargetPort: 21001}
	ib := renderForwardInbound(spec, shared.ChainForwardTag(1), 11002)

	if ib["tag"] != "chainfwd_1" || ib["listen"] != "0.0.0.0" || ib["protocol"] != "dokodemo-door" {
		t.Fatalf("inbound 顶层字段不符: %v", ib)
	}
	settings := nested(ib, "settings")
	if settings["address"] != "127.0.0.1" || settings["network"] != "tcp" {
		t.Fatalf("settings 不符: %v", settings)
	}
	if settings["port"] != 21001 {
		t.Fatalf("settings.port 不符: %v", settings)
	}
}

// TestRenderForwardInboundUDPLayers 验证 forward 管道按出口协议分层：
// spec.Network="tcp,udp"（ss 出口）→ dokodemo 监听双层；空 → 回退 tcp（旧载荷兼容）。
func TestRenderForwardInboundUDPLayers(t *testing.T) {
	spec := &shared.ForwardSpec{TargetAddress: "127.0.0.1", TargetPort: 21001, Network: "tcp,udp"}
	ib := renderForwardInbound(spec, shared.ChainForwardTag(9), 11009)
	if got := nested(ib, "settings")["network"]; got != "tcp,udp" {
		t.Fatalf("ss 出口管道应监听 tcp,udp，实际 %v", got)
	}
	legacy := &shared.ForwardSpec{TargetAddress: "127.0.0.1", TargetPort: 21001}
	ib = renderForwardInbound(legacy, shared.ChainForwardTag(10), 11010)
	if got := nested(ib, "settings")["network"]; got != "tcp" {
		t.Fatalf("空 Network 应回退 tcp，实际 %v", got)
	}
}

// TestPickChainPortLayered 验证 forward 端口探测分层：UDP 被外部占用时
// layers=tcp,udp 报冲突、layers=tcp 放行。
func TestPickChainPortLayered(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	udpPort := pc.LocalAddr().(*net.UDPAddr).Port

	m := &Manager{}
	if _, err := m.pickChainPort(udpPort, nil, nil, "", "tcp,udp"); err == nil {
		t.Error("UDP 占用时 layers=tcp,udp 应报冲突")
	}
	if got, err := m.pickChainPort(udpPort, nil, nil, "", "tcp"); err != nil || got != udpPort {
		t.Errorf("UDP 占用不影响 tcp 层: got=%d err=%v", got, err)
	}
}

// pieceForTest 构造一个 portal piece 记录（合并/移除测试用）。
func pieceForTest(t *testing.T, hopID int64) state.ChainPiece {
	t.Helper()
	spec := &shared.PortalSpec{
		TunnelDomain: "c1h2.lx", TunnelUUID: "t-uuid", ShortID: "sid",
		Dest: "dl.google.com:443", ServerNames: []string{"dl.google.com"},
	}
	inbound, err := json.Marshal(renderPortalInbound(spec, shared.ChainPortalTag(hopID), 11001, "priv"))
	if err != nil {
		t.Fatal(err)
	}
	reverse, _ := json.Marshal(map[string]any{"tag": chainPortalReverseTag(hopID), "domain": "c1h2.lx"})
	rule, _ := json.Marshal(map[string]any{
		"type": "field", "inboundTag": []string{shared.ChainPortalTag(hopID)},
		"outboundTag": chainPortalReverseTag(hopID),
	})
	return state.ChainPiece{
		HopID: hopID, Kind: shared.HopKindPortal, Port: 11001,
		Inbound: inbound, Reverse: reverse, Rules: []json.RawMessage{rule},
	}
}

// TestApplyChainPieceIdempotent 验证同 piece 重复合并不产生重复 inbound/reverse/routing 项。
func TestApplyChainPieceIdempotent(t *testing.T) {
	m := NewManager("xray", "/nonexistent/config.json", "127.0.0.1:10085", nil)
	fc := m.skeleton()
	rec := pieceForTest(t, 2)

	fc = applyChainPiece(fc, rec)
	fc = applyChainPiece(fc, rec) // 重发语义：第二次合并

	if n := len(fc.inbounds()); n != 1 {
		t.Fatalf("inbounds 数 %d，期望 1（无重复）", n)
	}
	if n := len(fc.reverseEntries("portals")); n != 1 {
		t.Fatalf("reverse.portals 数 %d，期望 1", n)
	}
	if n := len(fc.routingRules()); n != 1 {
		t.Fatalf("routing.rules 数 %d，期望 1", n)
	}
	// 骨架 freedom outbound 不受影响。
	if n := len(fc.outbounds()); n != 1 {
		t.Fatalf("outbounds 数 %d，期望 1（骨架 freedom）", n)
	}
}

// TestRemoveChainPieceItems 验证移除后 inbound/reverse/routing 干净且骨架段保留。
func TestRemoveChainPieceItems(t *testing.T) {
	m := NewManager("xray", "/nonexistent/config.json", "127.0.0.1:10085", nil)
	fc := applyChainPiece(m.skeleton(), pieceForTest(t, 2))

	nc, changed := removeChainPieceItems(fc, 2, shared.HopKindPortal)
	if !changed {
		t.Fatal("应有配置件被移除")
	}
	if n := len(nc.inbounds()); n != 0 {
		t.Fatalf("移除后 inbounds 数 %d，期望 0", n)
	}
	if _, ok := nc["reverse"]; ok {
		t.Fatalf("移除后 reverse 段应消失: %s", nc["reverse"])
	}
	if _, ok := nc["routing"]; ok {
		t.Fatalf("移除后 routing 段应消失: %s", nc["routing"])
	}
	outbounds := nc.outbounds()
	if len(outbounds) != 1 {
		t.Fatalf("骨架 freedom outbound 应保留，实际 %d 个", len(outbounds))
	}

	// 幂等：再次移除报"无变化"。
	if _, changed := removeChainPieceItems(nc, 2, shared.HopKindPortal); changed {
		t.Fatal("重复移除应无变化")
	}
}

// TestRemoveChainPieceKeepsNodeInbound 验证移除链 piece 不影响节点 inbound 与其他规则。
func TestRemoveChainPieceKeepsNodeInbound(t *testing.T) {
	m := NewManager("xray", "/nonexistent/config.json", "127.0.0.1:10085", nil)
	nodeInbound := json.RawMessage(`{"tag":"node_1","protocol":"vless"}`)
	fc := m.skeleton().upsertInbound("node_1", nodeInbound)
	fc = applyChainPiece(fc, pieceForTest(t, 2))

	nc, _ := removeChainPieceItems(fc, 2, shared.HopKindPortal)
	inbounds := nc.inbounds()
	if len(inbounds) != 1 || inboundTag(inbounds[0]) != "node_1" {
		t.Fatalf("节点 inbound 应保留: %v", inbounds)
	}
}

// TestReversePortalTagByDomain 验证 forward 路由目标按 tunnel_domain 找到 reverse portal tag。
func TestReversePortalTagByDomain(t *testing.T) {
	m := NewManager("xray", "/nonexistent/config.json", "127.0.0.1:10085", nil)
	fc := applyChainPiece(m.skeleton(), pieceForTest(t, 2))
	if tag := reversePortalTagByDomain(fc, "c1h2.lx"); tag != "portal_2" {
		t.Fatalf("tag %q，期望 portal_2", tag)
	}
	if tag := reversePortalTagByDomain(fc, "c9h9.lx"); tag != "" {
		t.Fatalf("未知 domain 应返回空串，实际 %q", tag)
	}
}

// TestPickChainPort 验证链 piece 端口挑选：指定校验占用、复用已记录端口、候选全占用报错。
func TestPickChainPort(t *testing.T) {
	m := NewManager("xray", "/nonexistent/config.json", "127.0.0.1:10085", nil)
	// 复用已记录端口（重发幂等）。
	prev := &state.ChainPiece{Port: 0}
	p, err := m.pickChainPort(0, nil, prev, "", "tcp")
	if err != nil || p == 0 {
		t.Fatalf("无候选无记录应挑随机空闲端口: %d %v", p, err)
	}
	// 候选全占用报错：占住一个端口后单候选挑选。
	hold, err := m.pickPort(0, nil, "", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	l := listenOn(t, hold)
	defer l.Close()
	if _, err := m.pickChainPort(0, []int{hold}, nil, "", "tcp"); err == nil {
		t.Fatal("候选全占用应报错")
	}
}

func TestPickChainPortReusesPortOwnedByExistingPiece(t *testing.T) {
	m := NewManager("xray", "/nonexistent/config.json", "127.0.0.1:10085", nil)
	port, err := m.pickPort(0, nil, "", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	listener := listenOn(t, port)
	defer listener.Close()
	previous := &state.ChainPiece{HopID: 7, Kind: shared.HopKindForward, Port: port}

	if got, err := m.pickChainPort(port, nil, previous, "", "tcp"); err != nil || got != port {
		t.Fatalf("显式复用已有配置件端口 = %d, %v；期望 %d", got, err, port)
	}
	if got, err := m.pickChainPort(0, nil, previous, "", "tcp"); err != nil || got != port {
		t.Fatalf("自动复用已有配置件端口 = %d, %v；期望 %d", got, err, port)
	}
}

// TestPickPortReusesManagedPort 验证占用探测区分端口归属（§21 端口复用）：
// 端口已被 agent 自身受管 config 记录（运行中的 xray 持有）→ 复用不报冲突。
func TestPickPortReusesManagedPort(t *testing.T) {
	mgr := newTestEndpointManager(t)
	port, err := mgr.pickPort(0, nil, "", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	// 模拟受管 config 已含该端口的 inbound（如既有节点/链 piece）。
	fc := mgr.skeleton().upsertInbound("node_9", json.RawMessage(
		fmt.Sprintf(`{"tag":"node_9","protocol":"vless","port":%d}`, port)))
	b, err := json.Marshal(fc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mgr.configPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	// 模拟运行中的 xray 持有该端口（bind 探测会失败）。
	ln := listenOn(t, port)
	defer ln.Close()

	if got, err := mgr.pickPort(port, nil, "", "tcp"); err != nil || got != port {
		t.Fatalf("受管端口应可复用: got=%d err=%v", got, err)
	}
}

// TestPickPortForeignPortConflict 验证其他服务占用的端口仍报冲突（e2e 语义：
// 外部进程占用 → node_failed/forward failed 告警路径不变）。
func TestPickPortForeignPortConflict(t *testing.T) {
	mgr := newTestEndpointManager(t)
	port, err := mgr.pickPort(0, nil, "", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	ln := listenOn(t, port)
	defer ln.Close()

	if _, err := mgr.pickPort(port, nil, "", "tcp"); err == nil {
		t.Fatal("其他服务占用的端口应报冲突")
	}
}

// TestPickPortManualPortValidatedAgainstCandidates 验证受限直连 NAT 机（载荷带
// 段内候选）手动指定端口必须落在候选段内（§21）：段内通过，段外拒绝（先于占用探测）。
func TestPickPortManualPortValidatedAgainstCandidates(t *testing.T) {
	mgr := newTestEndpointManager(t)
	free, err := mgr.pickPort(0, nil, "", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	// 段内手动端口 → 采用。
	if got, err := mgr.pickPort(free, []int{free}, "", "tcp"); err != nil || got != free {
		t.Fatalf("候选段内手动端口应通过: got=%d err=%v", got, err)
	}
	// 占住段内端口后再取一个不同端口作段外输入（确定性，避免 OS 复用临时端口）。
	ln := listenOn(t, free)
	defer ln.Close()
	other, err := mgr.pickPort(0, nil, "", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.pickPort(other, []int{free}, "", "tcp"); err == nil {
		t.Fatal("候选段外手动端口应报错")
	}
	// pickChainPort 透传候选做同样校验（链 piece 入口）。
	if _, err := mgr.pickChainPort(other, []int{free}, nil, "", "tcp"); err == nil {
		t.Fatal("pickChainPort 候选段外手动端口应报错")
	}
}

// TestApplySharedEndpointFixedPortWhileManagedHeld 验证新端点固定端口已被自身受管
// xray 持有（config 已含同端口 inbound，前测试动作遗留）时仍可复用落地，
// 不误报"端口被占用"（§21 端口复用：受管端口可复用，其他服务占用才冲突）。
func TestApplySharedEndpointFixedPortWhileManagedHeld(t *testing.T) {
	mgr := newTestEndpointManager(t)
	port, err := mgr.pickPort(0, nil, "", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	fc := mgr.skeleton().upsertInbound("node_10", json.RawMessage(
		fmt.Sprintf(`{"tag":"node_10","protocol":"vless","port":%d}`, port)))
	b, err := json.Marshal(fc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mgr.configPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	ln := listenOn(t, port)
	defer ln.Close()

	payload := endpointPortPayload(11, nil)
	payload.Config.Port = port
	realized, err := mgr.ApplySharedEndpoint(payload)
	if err != nil {
		t.Fatalf("受管端口应可复用落地: %v", err)
	}
	if realized.Port != port {
		t.Fatalf("应复用端口 %d，实际 %d", port, realized.Port)
	}
}

func TestPickPortLayeredProbe(t *testing.T) {
	// 占用一个 UDP 端口：udp 层探测应冲突，tcp 层探测应放行
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	udpPort := pc.LocalAddr().(*net.UDPAddr).Port

	m := &Manager{}
	if _, err := m.pickPort(udpPort, nil, "", "udp"); err == nil {
		t.Error("UDP 被占用时 layers=udp 应报冲突")
	}
	if got, err := m.pickPort(udpPort, nil, "", "tcp"); err != nil {
		t.Errorf("UDP 被占用不影响 tcp 层: %v", err)
	} else if got != udpPort {
		t.Errorf("tcp 层应放行并取得端口 %d，实际 %d", udpPort, got)
	}
}

func listenOn(t *testing.T, port int) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestRenderForwardHy2Target 验证入口终结末段 piece：dokodemo-udp inbound + hy2 outbound
// （routing 指向 hy2 outbound 而非 freedom）。
func TestRenderForwardHy2Target(t *testing.T) {
	p := shared.ApplyChainHopPayload{
		ChainID: 1, HopID: 7, Kind: shared.HopKindForward,
		Forward: &shared.ForwardSpec{
			Tag: shared.ChainForwardTag(7), Port: 22000, Network: "udp",
			TargetAddress: "203.0.113.9", TargetPort: 21000,
			Hy2Target: &shared.Hy2DialSpec{
				Address: "203.0.113.9", Port: 21000, Auth: "auth-x",
				SNI: "www.example.com", CertSHA256: "deadbeef",
			},
		},
	}
	m, _ := newRebuildTestManager(t)
	cur, _ := m.loadConfig()
	_, rec, err := m.renderForward(p, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Outbounds) != 1 {
		t.Fatalf("应含 1 条 hy2 outbound: %+v", rec)
	}
	var ob map[string]any
	if err := json.Unmarshal(rec.Outbounds[0], &ob); err != nil {
		t.Fatal(err)
	}
	if ob["protocol"] != "hysteria" {
		t.Fatalf("outbound 应为 hysteria: %v", ob["protocol"])
	}
	// 路由规则指向 hy2 outbound tag（= forward tag）。
	var rule struct {
		OutboundTag string `json:"outboundTag"`
	}
	if err := json.Unmarshal(rec.Rules[0], &rule); err != nil || rule.OutboundTag != shared.ChainForwardTag(7) {
		t.Fatalf("路由应指向 hy2 outbound: %+v", rec.Rules[0])
	}
}

// TestRenderForwardHopPorts 验证端到端跳跃段：主 inbound 外逐端口附加 dokodemo inbound
// （[Port+1, HopPortEnd]，目标下一跳同号端口，udp-only）。
func TestRenderForwardHopPorts(t *testing.T) {
	p := shared.ApplyChainHopPayload{
		ChainID: 1, HopID: 8, Kind: shared.HopKindForward,
		Forward: &shared.ForwardSpec{
			Tag: shared.ChainForwardTag(8), Port: 30000, HopPortEnd: 30002,
			Network: "udp", TargetAddress: "198.51.100.2", TargetPort: 30000,
		},
	}
	m, _ := newRebuildTestManager(t)
	cur, _ := m.loadConfig()
	_, rec, err := m.renderForward(p, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Inbounds) != 2 { // 30001, 30002（主 inbound = 30000）
		t.Fatalf("附加 inbound 数不符: %+v", rec.Inbounds)
	}
	for i, raw := range rec.Inbounds {
		var ib struct {
			Tag      string `json:"tag"`
			Port     int    `json:"port"`
			Settings struct {
				Port    int    `json:"port"`
				Network string `json:"network"`
			} `json:"settings"`
		}
		if err := json.Unmarshal(raw, &ib); err != nil {
			t.Fatal(err)
		}
		want := 30001 + i
		if ib.Port != want || ib.Settings.Port != want || ib.Settings.Network != "udp" {
			t.Fatalf("附加 inbound 应 1:1 同号转发: %+v", ib)
		}
	}
}

// TestRemoveForwardHopPiece 验证 forward piece 移除覆盖 Hy2Target 的 hy2 outbound
// 与跳跃段附加 inbound（<tag>_hop_<port>），且骨架 freedom 不受影响。
func TestRemoveForwardHopPiece(t *testing.T) {
	m := NewManager("xray", "/nonexistent/config.json", "127.0.0.1:10085", nil)
	fc := m.skeleton()
	rec := state.ChainPiece{HopID: 8, Kind: shared.HopKindForward, Port: 30000}
	mainInbound, _ := json.Marshal(renderForwardInbound(&shared.ForwardSpec{
		TargetAddress: "198.51.100.2", TargetPort: 30000, Network: "udp",
	}, shared.ChainForwardTag(8), 30000))
	rec.Inbound = mainInbound
	for hp := 30001; hp <= 30002; hp++ {
		extra, _ := json.Marshal(renderForwardInbound(&shared.ForwardSpec{
			TargetAddress: "198.51.100.2", TargetPort: hp, Network: "udp",
		}, fmt.Sprintf("%s_hop_%d", shared.ChainForwardTag(8), hp), hp))
		rec.Inbounds = append(rec.Inbounds, extra)
	}
	hy2Outbound, _ := json.Marshal(renderHy2Outbound(shared.ChainForwardTag(8), shared.Hy2DialSpec{
		Address: "203.0.113.9", Port: 21000, Auth: "auth-x", SNI: "www.example.com",
	}))
	rec.Outbounds = []json.RawMessage{hy2Outbound}
	rule, _ := json.Marshal(map[string]any{
		"type": "field", "inboundTag": []string{shared.ChainForwardTag(8)},
		"outboundTag": shared.ChainForwardTag(8),
	})
	rec.Rules = []json.RawMessage{rule}

	fc = applyChainPiece(fc, rec)
	if n := len(fc.inbounds()); n != 3 {
		t.Fatalf("合并后应有 3 个 inbound（主+2 附加），实际 %d", n)
	}
	nc, changed := removeChainPieceItems(fc, 8, shared.HopKindForward)
	if !changed {
		t.Fatal("应有配置件被移除")
	}
	if n := len(nc.inbounds()); n != 0 {
		t.Fatalf("附加 inbound 应一并清除，剩余 %d 个", n)
	}
	for _, raw := range nc.outbounds() {
		var ob struct {
			Tag string `json:"tag"`
		}
		json.Unmarshal(raw, &ob)
		if ob.Tag == shared.ChainForwardTag(8) {
			t.Fatal("hy2 outbound 应随 piece 一并清除")
		}
	}
	if _, ok := nc["routing"]; ok {
		t.Fatalf("routing 段应清空: %s", nc["routing"])
	}
}
