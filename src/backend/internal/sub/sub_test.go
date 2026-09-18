package sub

import (
	"testing"

	"lattix/shared"
)

// TestBuildProxyHysteria2 验证 mihomo hysteria2 代理项（P4 §3.4）：
// 自签 fingerprint pin、salamander obfs、带宽、ports 段、口令派生；ACME 无 fingerprint。
func TestBuildProxyHysteria2(t *testing.T) {
	rc := shared.RealizedConfig{Port: 21000, Security: shared.SecurityTLS,
		SNI: "www.example.com", CertSHA256: "deadbeef",
		ObfsPassword: "obfs-pw", UpMbps: 50, DownMbps: 100, PortHop: "20000-20031"}
	p, err := buildProxy(testNode("1.2.3.4", shared.ProtocolHysteria2), rc, "uuid-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != "hysteria2" || p.Password != shared.Hy2UserPassword("uuid-1") {
		t.Fatalf("类型/口令不符: %+v", p)
	}
	if p.SNI != "www.example.com" || p.Fingerprint != "deadbeef" {
		t.Fatalf("SNI/pin 不符: %+v", p)
	}
	if p.Obfs != "salamander" || p.ObfsPassword != "obfs-pw" {
		t.Fatalf("obfs 不符: %+v", p)
	}
	if p.Ports != "20000-20031" || p.Up != "50 Mbps" || p.Down != "100 Mbps" {
		t.Fatalf("段/带宽不符: %+v", p)
	}
	rcACME := rc
	rcACME.CertSHA256 = ""
	p2, _ := buildProxy(testNode("1.2.3.4", shared.ProtocolHysteria2), rcACME, "uuid-1")
	if p2.Fingerprint != "" {
		t.Fatalf("ACME 不应输出 fingerprint: %+v", p2)
	}
}
