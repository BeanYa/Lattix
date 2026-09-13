package xray

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lattix/shared"
)

// writeTestCert 用 Go 标准库生成测试自签证书（execTLSCert 测试桩的落盘实现，
// §6：fill 测试自签证书生成 mock——桩掉 xray 外部命令，证书本身真实可解析）。
func writeTestCert(t *testing.T, domain, prefix string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certOut, err := os.Create(prefix + ".crt")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	certOut.Close()
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyOut, err := os.Create(prefix + ".key")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatal(err)
	}
	keyOut.Close()
}

// stubTLSCert 把 execTLSCert 替换为本地证书生成，返回调用计数（幂等复用断言用）。
func stubTLSCert(t *testing.T) *int {
	t.Helper()
	calls := new(int)
	orig := execTLSCert
	execTLSCert = func(bin, domain, prefix string) error {
		*calls++
		writeTestCert(t, domain, prefix)
		return nil
	}
	t.Cleanup(func() { execTLSCert = orig })
	return calls
}

// TestEnsureTLSCertificateSelfSign 验证自签模式：调用 xray tls cert 落地证书、
// 返回绝对路径与 hex pin；二次调用幂等复用（不再调用外部命令，不轮换 pin）。
func TestEnsureTLSCertificateSelfSign(t *testing.T) {
	calls := stubTLSCert(t)
	m, _ := newRebuildTestManager(t)
	vc := shared.VirtualConfig{Security: shared.SecurityTLS,
		CertMode: shared.CertModeSelfSign, TLSDomain: "www.example.com"}
	certFile, keyFile, pin, err := m.ensureTLSCertificate("node_1", vc)
	if err != nil {
		t.Fatal(err)
	}
	if len(pin) != 64 {
		t.Errorf("pin 应为 64 位 hex，实际 %q", pin)
	}
	if !filepath.IsAbs(certFile) || !strings.Contains(certFile, "certs/node_1/") {
		t.Errorf("证书路径布局不符: %s", certFile)
	}
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("私钥未落地: %v", err)
	}
	certFile2, _, pin2, err := m.ensureTLSCertificate("node_1", vc)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || certFile2 != certFile || pin2 != pin {
		t.Errorf("二次调用应幂等复用: calls=%d pin=%q→%q", *calls, pin, pin2)
	}
}

// TestACMEDomainSelfCheck 验证 ACME 域名自检（§5：域名解析不含本机 → 指向性错误）。
func TestACMEDomainSelfCheck(t *testing.T) {
	origLookup, origAddrs := lookupHostIPs, localIfaceAddrs
	t.Cleanup(func() { lookupHostIPs, localIfaceAddrs = origLookup, origAddrs })

	// /32 使 ParseCIDR 返回的 ipNet.IP 即主机地址本身（/24 会得到网络地址 .0，与本机 .7 不匹配）。
	_, ipNet, _ := net.ParseCIDR("203.0.113.7/32")
	localIfaceAddrs = func() ([]net.Addr, error) { return []net.Addr{ipNet}, nil }

	lookupHostIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("198.51.100.9")}, nil }
	if err := acmeDomainSelfCheck("exit.example.com"); err == nil ||
		!strings.Contains(err.Error(), "不含本机地址") {
		t.Errorf("解析不含本机应报指向性错误: %v", err)
	}
	lookupHostIPs = func(string) ([]net.IP, error) { return nil, &net.DNSError{IsNotFound: true} }
	if err := acmeDomainSelfCheck("exit.example.com"); err == nil ||
		!strings.Contains(err.Error(), "解析失败") {
		t.Errorf("解析失败应报指向性错误: %v", err)
	}
	lookupHostIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.7")}, nil }
	if err := acmeDomainSelfCheck("exit.example.com"); err != nil {
		t.Errorf("解析含本机应通过: %v", err)
	}
}

// TestIssueACMECertificateGuards 验证签发前置自检顺序：80 端口被占 → 指向性错误，
// 且不进入签发（execACMESh 计数为 0）。（真实 LE 签发不做，spec §6。）
func TestIssueACMECertificateGuards(t *testing.T) {
	origLookup, origAddrs, origPort, origExec := lookupHostIPs, localIfaceAddrs, acmePort80Free, execACMESh
	t.Cleanup(func() {
		lookupHostIPs, localIfaceAddrs, acmePort80Free, execACMESh = origLookup, origAddrs, origPort, origExec
	})
	// /32 同上：ParseCIDR 的 ipNet.IP 须为主机地址本身。
	_, ipNet, _ := net.ParseCIDR("203.0.113.7/32")
	localIfaceAddrs = func() ([]net.Addr, error) { return []net.Addr{ipNet}, nil }
	lookupHostIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.7")}, nil }
	issued := 0
	execACMESh = func(home string, args ...string) error { issued++; return nil }
	acmePort80Free = func() error {
		return os.ErrExist // 模拟占用（实现包装为指向性错误文案）
	}
	m, _ := newRebuildTestManager(t)
	dir := t.TempDir()
	// acme.sh 已安装（跳过下载）：放一个假 acme.sh。
	home := m.acmeHome()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "acme.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := m.issueACMECertificate("exit.example.com", filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err == nil {
		t.Fatal("80 端口被占应报错")
	}
	if issued != 0 {
		t.Errorf("端口自检失败不应进入签发，execACMESh 调用 %d 次", issued)
	}
	acmePort80Free = func() error { return nil }
	if err := m.issueACMECertificate("exit.example.com",
		filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")); err != nil {
		t.Fatal(err)
	}
	if issued != 2 {
		t.Errorf("应依次调用 --issue 与 --install-cert，实际 %d 次", issued)
	}
}
