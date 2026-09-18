package panel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// TestRealityStreamSettingsPinsMinClientVer 验证 REALITY inbound 模板显式声明
// minClientVer=0：xray 26.7.11+ 在字段缺省时默认要求客户端版本 ≥ 26.3.27，
// 会拒绝版本声明较旧的客户端（如 mihomo/clash），显式 0 恢复不限版本行为。
func TestRealityStreamSettingsPinsMinClientVer(t *testing.T) {
	req := createNodeRequest{
		Protocol:    shared.ProtocolVLESS,
		Network:     shared.NetworkTCP,
		Fingerprint: shared.FingerprintChrome,
		Dest:        "dl.google.com:443",
		ServerNames: []string{"dl.google.com"},
		ShortID:     "0123abcd",
	}
	ss := realityStreamSettings(req)
	rs, ok := ss["realitySettings"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 realitySettings: %v", ss)
	}
	if rs["minClientVer"] != "0" {
		t.Fatalf("minClientVer 应显式为 0（xray 26.7.11+ 缺省默认 26.3.27 会拒绝旧客户端）: %v", rs["minClientVer"])
	}
}

// TestNormalizeVMessCipher 验证 vmess cipher：显式值保留、缺省回退 auto、
// 非法值报错、非 vmess 协议强制清空。
func TestNormalizeVMessCipher(t *testing.T) {
	req := &createNodeRequest{Protocol: shared.ProtocolVMess, Cipher: "aes-128-gcm"}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Cipher != "aes-128-gcm" {
		t.Errorf("cipher 应保留 aes-128-gcm，实际 %q", req.Cipher)
	}

	req = &createNodeRequest{Protocol: shared.ProtocolVMess}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Cipher != "auto" {
		t.Errorf("cipher 默认应为 auto，实际 %q", req.Cipher)
	}

	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Cipher: "none"}
	if err := req.normalize(); err == nil {
		t.Error("非法 cipher 应报错")
	}

	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Cipher: "aes-128-gcm"}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Cipher != "" {
		t.Errorf("非 vmess 协议 cipher 应清空，实际 %q", req.Cipher)
	}
}

// TestNormalizeTransportSecurityMatrix 验证 spec §2 矩阵的 normalize 落地：
// 合法组合放行并补默认值，矩阵外组合 400（报错指明冲突字段）。
func TestNormalizeTransportSecurityMatrix(t *testing.T) {
	// 合法：vmess+ws → security 自动推导 none，path/host 保留
	req := &createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS, Path: "/p", Host: "h.example.com"}
	if err := req.normalize(); err != nil {
		t.Fatalf("vmess+ws 应合法: %v", err)
	}
	if req.Security != shared.SecurityNone || req.Path != "/p" || req.Host != "h.example.com" {
		t.Errorf("vmess+ws 归一化不符: security=%q path=%q host=%q", req.Security, req.Path, req.Host)
	}
	if req.ShortID != "" || req.Dest != "" || len(req.ServerNames) != 0 {
		t.Errorf("security=none 应清空 reality 专有字段: %+v", req)
	}
	// 合法：vless+httpupgrade+VLESS Encryption（spec §2 脚注 1）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Network: shared.NetworkHTTPUpgrade, Encryption: shared.VLessEncMLKEM768}
	if err := req.normalize(); err != nil {
		t.Fatalf("vless+httpupgrade+Encryption 应合法: %v", err)
	}
	if req.Security != shared.SecurityNone || req.Flow != "" || req.Path != "/" {
		t.Errorf("vless+httpupgrade 归一化不符: security=%q flow=%q path=%q", req.Security, req.Flow, req.Path)
	}
	// 非法：reality × ws
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS, Security: shared.SecurityReality}
	if err := req.normalize(); err == nil {
		t.Error("reality×ws 应 400")
	}
	// 非法：trojan × ws（推导 none，trojan 不允许 none）
	req = &createNodeRequest{Protocol: shared.ProtocolTrojan, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("trojan+ws 应 400")
	}
	// 非法：vless+ws 无 Encryption
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("vless+ws 无 Encryption 应 400")
	}
	// 非法：ss 显式带传输层
	req = &createNodeRequest{Protocol: shared.ProtocolShadowsocks, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("ss 带 network 应 400")
	}
	// 合法：security=tls（P3 开放；清理 #1 移除 400 引导），cert_mode 默认 selfsign
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS}
	if err := req.normalize(); err != nil {
		t.Fatalf("vmess+tls 应合法: %v", err)
	}
	if req.CertMode != shared.CertModeSelfSign {
		t.Errorf("tls 的 cert_mode 应默认 selfsign，实际 %q", req.CertMode)
	}
	// 非法：vision × none（vision 仅 vless+tcp+reality）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Security: shared.SecurityNone, Encryption: shared.VLessEncX25519, Flow: shared.FlowVision}
	if err := req.normalize(); err == nil {
		t.Error("vision+none 应 400")
	}
	// 回归：存量默认（vless 空表单）仍 = tcp+reality+vision
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if req.Security != shared.SecurityReality || req.Network != shared.NetworkTCP || req.Flow != shared.FlowVision {
		t.Errorf("存量默认不符: security=%q network=%q flow=%q", req.Security, req.Network, req.Flow)
	}
}

// TestBuildVirtualConfigWSPlain 验证 ws/httpupgrade + security=none 模板形状
// （xray 25.x：ws 在 wsSettings、host 在 headers.Host；httpupgrade 在 httpupgradeSettings）。
func TestBuildVirtualConfigWSPlain(t *testing.T) {
	req := createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS,
		Security: shared.SecurityNone, Path: "/p", Host: "h.example.com"}
	vc := buildVirtualConfig(req)
	if vc.Security != shared.SecurityNone {
		t.Errorf("VirtualConfig.Security 应为 none，实际 %q", vc.Security)
	}
	var tmpl struct {
		StreamSettings struct {
			Network    string `json:"network"`
			Security   string `json:"security"`
			WsSettings struct {
				Path    string            `json:"path"`
				Headers map[string]string `json:"headers"`
			} `json:"wsSettings"`
			RealitySettings json.RawMessage `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(vc.Template, &tmpl); err != nil {
		t.Fatal(err)
	}
	ss := tmpl.StreamSettings
	if ss.Network != "ws" || ss.Security != "none" {
		t.Errorf("streamSettings 顶层不符: network=%q security=%q", ss.Network, ss.Security)
	}
	if ss.WsSettings.Path != "/p" || ss.WsSettings.Headers["Host"] != "h.example.com" {
		t.Errorf("wsSettings 不符: %+v", ss.WsSettings)
	}
	if len(ss.RealitySettings) != 0 {
		t.Error("security=none 模板不应带 realitySettings")
	}

	req = createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkHTTPUpgrade,
		Security: shared.SecurityNone, Path: "/hu", Host: "cdn.example.com"}
	vc = buildVirtualConfig(req)
	var tmpl2 struct {
		StreamSettings struct {
			Network             string `json:"network"`
			HTTPUpgradeSettings struct {
				Path string `json:"path"`
				Host string `json:"host"`
			} `json:"httpupgradeSettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(vc.Template, &tmpl2); err != nil {
		t.Fatal(err)
	}
	if tmpl2.StreamSettings.Network != "httpupgrade" ||
		tmpl2.StreamSettings.HTTPUpgradeSettings.Path != "/hu" ||
		tmpl2.StreamSettings.HTTPUpgradeSettings.Host != "cdn.example.com" {
		t.Errorf("httpupgradeSettings 不符: %+v", tmpl2.StreamSettings)
	}
}

// TestNormalizeTLSMatrix 验证 P3 tls 矩阵（含清理 #1：tls 400 引导分支移除）：
// vless/vmess/trojan × 全部传输 × tls 合法；trojan×ws 不显式给 tls 仍 400（推导 none）；
// vision 扩展为 reality|tls；cert_mode 缺省 selfsign、tls_domain 留空随机填充。
func TestNormalizeTLSMatrix(t *testing.T) {
	// 合法：trojan+ws+tls（P2 的 400 组合，本期开放），cert_mode 缺省 selfsign、域名随机
	req := &createNodeRequest{Protocol: shared.ProtocolTrojan, Network: shared.NetworkWS, Security: shared.SecurityTLS}
	if err := req.normalize(); err != nil {
		t.Fatalf("trojan+ws+tls 应合法: %v", err)
	}
	if req.CertMode != shared.CertModeSelfSign || req.TLSDomain == "" {
		t.Errorf("tls 默认值不符: cert_mode=%q tls_domain=%q", req.CertMode, req.TLSDomain)
	}
	if req.ShortID != "" || req.Dest != "" || len(req.ServerNames) != 0 {
		t.Errorf("tls 应清空 reality 专有字段: %+v", req)
	}
	if req.Fingerprint != shared.FingerprintChrome {
		t.Errorf("tls 应保留/默认 fingerprint: %q", req.Fingerprint)
	}
	// 合法：vmess+tcp+tls 自定义伪装域名
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS, TLSDomain: "cdn.example.com"}
	if err := req.normalize(); err != nil {
		t.Fatalf("vmess+tcp+tls 应合法: %v", err)
	}
	if req.TLSDomain != "cdn.example.com" || req.Flow != "" {
		t.Errorf("自定义伪装域名不符: %+v", req)
	}
	// 合法：vless+tcp+tls+vision（§2：vision 仅 tcp+reality|tls）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Security: shared.SecurityTLS, Flow: shared.FlowVision}
	if err := req.normalize(); err != nil {
		t.Fatalf("vless+tcp+tls+vision 应合法: %v", err)
	}
	// 非法：trojan+ws 不显式给 security（推导 none，trojan 不允许 none）
	req = &createNodeRequest{Protocol: shared.ProtocolTrojan, Network: shared.NetworkWS}
	if err := req.normalize(); err == nil {
		t.Error("trojan+ws 推导 none 应 400")
	}
	// 非法：cert_mode 未知值
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS, CertMode: "bogus"}
	if err := req.normalize(); err == nil {
		t.Error("cert_mode=bogus 应 400")
	}
	// 非法：伪装域名含端口/路径
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS, TLSDomain: "evil.com:443"}
	if err := req.normalize(); err == nil {
		t.Error("伪装域名含端口应 400")
	}
	// acme：normalize 清空用户输入（域名由处理器从服务器地址检测填充）
	req = &createNodeRequest{Protocol: shared.ProtocolVMess, Security: shared.SecurityTLS,
		CertMode: shared.CertModeACME, TLSDomain: "user-input.example.com"}
	if err := req.normalize(); err != nil {
		t.Fatalf("acme 模式 normalize 应合法: %v", err)
	}
	if req.TLSDomain != "" {
		t.Errorf("acme 模式 normalize 应清空 tls_domain（由 applyACMEDomain 填充），实际 %q", req.TLSDomain)
	}
	// 回归：vision+none 仍 400；ss 显式 security=tls 仍 400（无安全层选项）
	req = &createNodeRequest{Protocol: shared.ProtocolVLESS, Security: shared.SecurityNone,
		Encryption: shared.VLessEncMLKEM768, Flow: shared.FlowVision}
	if err := req.normalize(); err == nil {
		t.Error("vision+none 应 400")
	}
	req = &createNodeRequest{Protocol: shared.ProtocolShadowsocks, Security: shared.SecurityTLS}
	if err := req.normalize(); err == nil {
		t.Error("ss 显式 security 应 400")
	}
}

// TestBuildVirtualConfigTLS 验证 tlsStreamSettings 模板形状：tlsSettings.serverName
// 为 TLS 域名，certificates 引用占位符路径（agent 落地后替换，§3.2/§3.3）。
func TestBuildVirtualConfigTLS(t *testing.T) {
	req := createNodeRequest{Protocol: shared.ProtocolVMess, Network: shared.NetworkWS,
		Security: shared.SecurityTLS, CertMode: shared.CertModeSelfSign,
		TLSDomain: "cdn.example.com", Path: "/p", Host: "h.example.com"}
	vc := buildVirtualConfig(req)
	if vc.Security != shared.SecurityTLS || vc.CertMode != shared.CertModeSelfSign || vc.TLSDomain != "cdn.example.com" {
		t.Errorf("VirtualConfig TLS 字段不符: %+v", vc)
	}
	var tmpl struct {
		StreamSettings struct {
			Network     string `json:"network"`
			Security    string `json:"security"`
			TLSSettings struct {
				ServerName   string `json:"serverName"`
				Certificates []struct {
					CertificateFile string `json:"certificateFile"`
					KeyFile         string `json:"keyFile"`
				} `json:"certificates"`
			} `json:"tlsSettings"`
			WsSettings struct {
				Path string `json:"path"`
			} `json:"wsSettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(vc.Template, &tmpl); err != nil {
		t.Fatal(err)
	}
	ss := tmpl.StreamSettings
	if ss.Network != "ws" || ss.Security != "tls" || ss.TLSSettings.ServerName != "cdn.example.com" {
		t.Errorf("tls streamSettings 不符: %+v", ss)
	}
	if len(ss.TLSSettings.Certificates) != 1 ||
		ss.TLSSettings.Certificates[0].CertificateFile != shared.PlaceholderTLSCertFile ||
		ss.TLSSettings.Certificates[0].KeyFile != shared.PlaceholderTLSKeyFile {
		t.Errorf("证书占位符不符: %+v", ss.TLSSettings.Certificates)
	}
	if ss.WsSettings.Path != "/p" {
		t.Errorf("ws 传输子段不符: %+v", ss.WsSettings)
	}
}

// TestApplyACMEDomain 验证 ACME 域名检测（§3.3 模式 B + §5 错误处理）：
// 落地服务器公网地址无域名条目 → 指向性错误；有 → 沿用该域名填充 TLSDomain。
func TestApplyACMEDomain(t *testing.T) {
	noDomain := &store.Server{Alias: "nat01", Addresses: `["1.2.3.4","2400:cb00::1"]`}
	req := &createNodeRequest{Security: shared.SecurityTLS, CertMode: shared.CertModeACME}
	if err := applyACMEDomain(req, noDomain); err == nil {
		t.Error("无域名服务器的 acme 模式应报错")
	} else if !strings.Contains(err.Error(), "未设置域名") {
		t.Errorf("错误信息应指向域名配置: %v", err)
	}
	withDomain := &store.Server{Alias: "hk01", Addresses: `["1.2.3.4","exit.example.com"]`}
	if err := applyACMEDomain(req, withDomain); err != nil {
		t.Fatal(err)
	}
	if req.TLSDomain != "exit.example.com" {
		t.Errorf("应沿用落地服务器域名，实际 %q", req.TLSDomain)
	}
	// 非 acme 直通（selfsign 的 TLSDomain 不被触碰）
	req = &createNodeRequest{Security: shared.SecurityTLS, CertMode: shared.CertModeSelfSign, TLSDomain: "cdn.example.com"}
	if err := applyACMEDomain(req, noDomain); err != nil || req.TLSDomain != "cdn.example.com" {
		t.Errorf("selfsign 应直通: err=%v domain=%q", err, req.TLSDomain)
	}
}

// TestNormalizeHysteria2 验证 hy2 矩阵（spec §2）：无 network/security 选择，
// 恒 tls + 证书模式复用；带宽/混淆/跳跃默认值；矩阵外组合 400。
func TestNormalizeHysteria2(t *testing.T) {
	req := &createNodeRequest{Protocol: "hysteria"}
	if err := req.normalize(); err != nil {
		t.Fatalf("默认 hy2 应合法: %v", err)
	}
	if req.Security != shared.SecurityTLS || req.CertMode != shared.CertModeSelfSign {
		t.Fatalf("hy2 应强制 tls/selfsign: %+v", req)
	}
	if req.TLSDomain == "" || req.Fingerprint != shared.FingerprintChrome {
		t.Fatalf("hy2 伪装域/指纹默认值缺失: %+v", req)
	}
	if req.ObfsPassword == "" || req.UpMbps != 50 || req.DownMbps != 100 || req.PortHop != "" {
		t.Fatalf("hy2 默认值不符（obfs 自动生成、50/100、port_hop 留待分配）: %+v", req)
	}
	// 矩阵外：network/reality/none 一律 400 并指明冲突字段。
	for _, bad := range []createNodeRequest{
		{Protocol: "hysteria", Network: "tcp"},
		{Protocol: "hysteria", Security: "reality"},
		{Protocol: "hysteria", Security: "none"},
		{Protocol: "hysteria", PortHop: "abc"},
		{Protocol: "hysteria", PortHop: "20000-20001"}, // 段长 < 8
		{Protocol: "hysteria", UpMbps: -1},
	} {
		if err := bad.normalize(); err == nil {
			t.Fatalf("非法组合应 400: %+v", bad)
		}
	}
	// off 保留哨兵（由 resolveHy2PortHop 归一为空，避免与"默认空=自动分配"混淆）；显式段保留。
	off := &createNodeRequest{Protocol: "hysteria", PortHop: "off"}
	if err := off.normalize(); err != nil || off.PortHop != "off" {
		t.Fatalf("port_hop=off 应保留哨兵: %v %+v", err, off)
	}
	explicit := &createNodeRequest{Protocol: "hysteria", PortHop: "31000-31031"}
	if err := explicit.normalize(); err != nil || explicit.PortHop != "31000-31031" {
		t.Fatalf("显式段应保留: %v %+v", err, explicit)
	}
}

// TestBuildVirtualConfigHysteria2 验证 hy2 模板形态（Task 1 定稿）：settings.clients +
// tls 证书占位符 + hysteriaSettings + finalmask（salamander/quicParams）+
// network:"hysteria" + alpn h3（后两者缺了会退化为 TCP 承载/握手失败，见事实区）。
func TestBuildVirtualConfigHysteria2(t *testing.T) {
	req := createNodeRequest{Protocol: "hysteria", Security: "tls", CertMode: "selfsign",
		TLSDomain: "www.example.com", ObfsPassword: "obfs-pw", UpMbps: 50, DownMbps: 100,
		PortHop: "20000-20031"}
	vc := buildVirtualConfig(req)
	var inbound map[string]any
	if err := json.Unmarshal(vc.Template, &inbound); err != nil {
		t.Fatal(err)
	}
	if inbound["protocol"] != "hysteria" {
		t.Fatalf("协议名须为 hysteria: %v", inbound["protocol"])
	}
	settings := inbound["settings"].(map[string]any)
	if settings["version"].(float64) != 2 || settings["clients"] != shared.PlaceholderClients {
		t.Fatalf("settings 不符: %v", settings)
	}
	ss := inbound["streamSettings"].(map[string]any)
	if ss["network"] != "hysteria" {
		t.Fatal("hy2 模板必须显式 network=hysteria（缺省 tcp 会退化为 TCP 承载，Task 1 实测）")
	}
	if ss["security"] != "tls" || ss["hysteriaSettings"].(map[string]any)["version"].(float64) != 2 {
		t.Fatalf("streamSettings 不符: %v", ss)
	}
	fm := ss["finalmask"].(map[string]any)
	udp := fm["udp"].([]any)[0].(map[string]any)
	if udp["type"] != "salamander" || udp["settings"].(map[string]any)["password"] != "obfs-pw" {
		t.Fatalf("salamander 段不符: %v", udp)
	}
	quic := fm["quicParams"].(map[string]any)
	if quic["congestion"] != "brutal" || quic["brutalUp"] != "50 mbps" || quic["brutalDown"] != "100 mbps" {
		t.Fatalf("quicParams 不符: %v", quic)
	}
	if _, hasHop := quic["udpHop"]; hasHop {
		t.Fatal("服务端模板不含 udpHop（DNAT 路径：段由 iptables 收敛，客户端侧才声明 udpHop）")
	}
	tlsS := ss["tlsSettings"].(map[string]any)
	if alpn, ok := tlsS["alpn"].([]any); !ok || len(alpn) != 1 || alpn[0] != "h3" {
		t.Fatalf("tlsSettings 必须 alpn=[h3]（否则握手 no application protocol）: %v", tlsS)
	}
	certs := tlsS["certificates"].([]any)[0].(map[string]any)
	if certs["certificateFile"] != shared.PlaceholderTLSCertFile || certs["keyFile"] != shared.PlaceholderTLSKeyFile {
		t.Fatalf("证书占位符缺失: %v", certs)
	}
	if vc.ObfsPassword != "obfs-pw" || vc.UpMbps != 50 || vc.DownMbps != 100 || vc.PortHop != "20000-20031" {
		t.Fatalf("VirtualConfig 字段透传不符: %+v", vc)
	}
}

// TestResolveHy2PortHopZeroSpanNAT 验证零公共端口 NAT（machine_type=nat 且无可用段）
// 自动关闭端口跳跃（spec §3.2 回退语义）：空段不能与 direct 机（空段=不限制，候选
// 40000-61000）混用同一候选池，否则零段 NAT 出口会被分配到公网不可达的跳跃段。
func TestResolveHy2PortHopZeroSpanNAT(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{st: st}

	nat := &store.Server{Alias: "nat-zero", MachineType: store.MachineTypeNAT, AllowedPorts: ""}
	req := createNodeRequest{Protocol: shared.ProtocolHysteria2}
	if err := s.resolveHy2PortHop(ctx, &req, nat, 0, 0); err != nil {
		t.Fatal(err)
	}
	if req.PortHop != "" {
		t.Fatalf("零段 NAT 应自动关跳跃（PortHop 保持空）: %q", req.PortHop)
	}

	// direct 机（空段=不限制）照常自动分配。
	direct := &store.Server{Alias: "direct", MachineType: store.MachineTypeDirect, AllowedPorts: ""}
	req = createNodeRequest{Protocol: shared.ProtocolHysteria2}
	if err := s.resolveHy2PortHop(ctx, &req, direct, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := shared.ParsePortHop(req.PortHop); err != nil {
		t.Fatalf("direct 机应自动分配合法段: %q", req.PortHop)
	}

	// 受限 NAT（有段）仍在段内分配。
	restricted := &store.Server{Alias: "nat-r", MachineType: store.MachineTypeNAT,
		AllowedPorts: `[{"pub_start":50000,"pub_end":50099}]`}
	req = createNodeRequest{Protocol: shared.ProtocolHysteria2}
	if err := s.resolveHy2PortHop(ctx, &req, restricted, 0, 0); err != nil {
		t.Fatal(err)
	}
	if start, _, err := shared.ParsePortHop(req.PortHop); err != nil || start != 50000 {
		t.Fatalf("受限 NAT 应在段内分配: %q", req.PortHop)
	}
}

// TestResolveHy2PortHopOwnEndpointSpanCrossServer 验证 ownEndpointID 段豁免仅限同机
// （终审修复 N-1）：编辑链把出口迁到服务器 Y 时，前端回填的 port_hop 与旧端点
//（X 上）段一致，但「共有占用」语义不跨机——段与 Y 上既有占用冲突必须照常报错，
// 否则 EnsureProtocolSharedEndpoint 会在 Y 上以冲突段新建端点 → 双 DNAT 抢同段。
func TestResolveHy2PortHopOwnEndpointSpanCrossServer(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{st: st}
	server := func(alias string) int64 {
		id, err := st.CreateServer(ctx, store.ServerDraft{Alias: alias, Address: alias + ".example.com",
			BootstrapToken: "token-" + alias, MachineType: store.MachineTypeDirect, CountryCode: "US"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	xID, yID := server("exit-x"), server("exit-y")
	// 旧端点在 X，段 40000-40031；Y 上既有 hy2 节点占同段。
	epCfg, _ := json.Marshal(buildVirtualConfig(createNodeRequest{Protocol: shared.ProtocolHysteria2,
		PortHop: "40000-40031"}))
	endpoint, _, err := st.EnsureProtocolSharedEndpoint(ctx, xID, shared.ProtocolHysteria2, 0, epCfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertNode(ctx, "occ", yID, shared.ProtocolHysteria2, nil, epCfg); err != nil {
		t.Fatal(err)
	}

	// 跨机：段与 Y 上既有占用冲突 → 报错（旧端点在 X，豁免不跨机）。
	srvY, err := st.ServerByID(ctx, yID)
	if err != nil {
		t.Fatal(err)
	}
	req := createNodeRequest{Protocol: shared.ProtocolHysteria2, PortHop: "40000-40031"}
	if err := s.resolveHy2PortHop(ctx, &req, srvY, 0, endpoint.ID); err == nil {
		t.Fatal("段与目标机 Y 既有占用冲突应报错（ownEndpointID 豁免不应跨机生效）")
	}

	// 同机：回填本链端点段原样提交 → 豁免生效，不误判冲突。
	srvX, err := st.ServerByID(ctx, xID)
	if err != nil {
		t.Fatal(err)
	}
	req = createNodeRequest{Protocol: shared.ProtocolHysteria2, PortHop: "40000-40031"}
	if err := s.resolveHy2PortHop(ctx, &req, srvX, 0, endpoint.ID); err != nil {
		t.Fatalf("同机端点段豁免应放行: %v", err)
	}
}
