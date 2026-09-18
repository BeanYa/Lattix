package sub

import (
	"testing"

	"lattix/shared"
)

// TestBuildSbOutboundHysteria2 验证 sing-box hysteria2 outbound（P4 §3.4）：
// 口令派生、tls server_name、自签 insecure 回退（无 pin 表达）、obfs 与 server_ports。
func TestBuildSbOutboundHysteria2(t *testing.T) {
	rc := shared.RealizedConfig{Port: 21000, Security: shared.SecurityTLS,
		SNI: "www.example.com", CertSHA256: "deadbeef",
		ObfsPassword: "obfs-pw", UpMbps: 50, DownMbps: 100, PortHop: "20000-20031"}
	ob, err := buildSbOutbound(testNode("1.2.3.4", shared.ProtocolHysteria2), rc, "uuid-1")
	if err != nil {
		t.Fatal(err)
	}
	if ob.Type != "hysteria2" || ob.Password != shared.Hy2UserPassword("uuid-1") {
		t.Fatalf("类型/口令不符: %+v", ob)
	}
	if ob.TLS == nil || !ob.TLS.Enabled || ob.TLS.ServerName != "www.example.com" || !ob.TLS.Insecure {
		t.Fatalf("tls 不符（自签应 insecure）: %+v", ob.TLS)
	}
	if ob.Obfs == nil || ob.Obfs.Type != "salamander" || ob.Obfs.Password != "obfs-pw" {
		t.Fatalf("obfs 不符: %+v", ob.Obfs)
	}
	if len(ob.ServerPorts) != 1 || ob.ServerPorts[0] != "20000-20031" {
		t.Fatalf("server_ports 不符: %+v", ob.ServerPorts)
	}
	rcACME := rc
	rcACME.CertSHA256 = ""
	ob2, _ := buildSbOutbound(testNode("1.2.3.4", shared.ProtocolHysteria2), rcACME, "uuid-1")
	if ob2.TLS.Insecure {
		t.Fatalf("ACME 不应 insecure: %+v", ob2.TLS)
	}
}
