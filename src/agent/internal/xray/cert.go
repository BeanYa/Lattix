package xray

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lattix/shared"
)

// 证书布局（§3.3）：证书文件在 <config 目录>/certs/<inbound tag>/ 下
//（node_*/shared_endpoint_* 各自隔离）；acme.sh 安装在 <安装根>/acme/（与 config/ 平级）。
// PurgeXray/ResetForPanelRebind 不触碰这两个目录——证书随重装/换绑保留，避免订阅 pin 失效。

func (m *Manager) certsDir(tag string) string {
	return filepath.Join(filepath.Dir(m.configPath), "certs", tag)
}

func (m *Manager) acmeHome() string {
	return filepath.Join(filepath.Dir(filepath.Dir(m.configPath)), "acme")
}

// ensureTLSCertificate 确保 TLS 证书就位，返回证书/私钥绝对路径与自签 pin
//（证书 DER 的 sha256 hex；ACME 模式为空串——公共 CA 走系统根验证，无需 pin）。
// 幂等：证书文件已存在则直接复用（重新生成会轮换 pin、失效已下发订阅）。
// 例外：ACME 模式下服务器域名变更后，旧证书与 realized.SNI 不符（客户端系统根
// +SNI 校验必败），复用前校验证书 DNSNames/CN 覆盖 vc.TLSDomain，不符则重新签发
//（acme.sh 同域名重复 issue 有自身缓存/限频）。自签分支不校验主机名（pin 语义）。
func (m *Manager) ensureTLSCertificate(tag string, vc shared.VirtualConfig) (certFile, keyFile, pin string, err error) {
	dir := m.certsDir(tag)
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if fileExists(certFile) && fileExists(keyFile) {
		if vc.CertMode == shared.CertModeACME {
			if certCoversDomain(certFile, vc.TLSDomain) {
				return certFile, keyFile, "", nil
			}
			// 域名已变更：落入下方重新签发（install-cert 覆盖旧文件）。
		} else {
			pin, err = certPinHex(certFile)
			return certFile, keyFile, pin, err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", "", err
	}
	if vc.CertMode == shared.CertModeACME {
		if err := m.issueACMECertificate(vc.TLSDomain, certFile, keyFile); err != nil {
			return "", "", "", err
		}
		return certFile, keyFile, "", nil
	}
	// selfsign：xray tls cert -file=<prefix> 输出 <prefix>.crt/<prefix>.key，归一为 cert.pem/key.pem。
	if vc.TLSDomain == "" {
		return "", "", "", fmt.Errorf("自签证书缺少伪装域名（tls_domain）")
	}
	if err := execTLSCert(m.bin, vc.TLSDomain, filepath.Join(dir, "server")); err != nil {
		return "", "", "", err
	}
	if err := os.Rename(filepath.Join(dir, "server.crt"), certFile); err != nil {
		return "", "", "", fmt.Errorf("整理自签证书失败: %w", err)
	}
	if err := os.Rename(filepath.Join(dir, "server.key"), keyFile); err != nil {
		return "", "", "", fmt.Errorf("整理自签私钥失败: %w", err)
	}
	if err := os.Chmod(keyFile, 0o600); err != nil {
		return "", "", "", err
	}
	pin, err = certPinHex(certFile)
	if err != nil {
		return "", "", "", err
	}
	return certFile, keyFile, pin, nil
}

// execTLSCert 执行 `xray tls cert` 生成自签证书（-name 使 CN=伪装域名，SAN 由 -domain 提供；
// xray CLI 无 CA 签发下级证书能力，单张自签证书兼作服务器证书，pin 该证书）。
// 包级变量 = 测试缝（§6：fill 测试自签证书生成 mock）。
var execTLSCert = func(bin, domain, prefix string) error {
	out, err := exec.Command(bin, "tls", "cert", "-domain="+domain, "-name="+domain, "-file="+prefix).CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray tls cert 执行失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// certPinHex 计算 PEM 证书 DER 的 sha256（hex）——订阅 pin（mihomo fingerprint）与
// xray 客户端 pinnedPeerCertSha256（26.x 起为 hex 字符串，allowInsecure 已移除）共用。
func certPinHex(certFile string) (string, error) {
	b, err := os.ReadFile(certFile)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return "", fmt.Errorf("证书 %s 不是合法 PEM", certFile)
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// certCoversDomain 判断现有证书是否覆盖 domain（SAN 优先，无 SAN 回退 CN，
// 与 TLS 主机名校验语义一致）；文件缺失/不可解析一律视为不覆盖 → 重新签发。
func certCoversDomain(certFile, domain string) bool {
	b, err := os.ReadFile(certFile)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	return cert.VerifyHostname(domain) == nil
}

// issueACMECertificate 走 acme.sh standalone 全流程（§3.3 模式 B）：
// 域名解析自检（须含本机地址）→ 80 端口自检 → 安装 acme.sh（首次）→ 签发 → 安装到目标路径。
// 廉价的指向性自检先于 acme.sh 网络安装：域名/端口不满足时不做下载，失败快且确定。
// acme.sh 自注册续期 cron；xray 每小时热重载证书文件，续期后自动生效，无需 reloadcmd。
func (m *Manager) issueACMECertificate(domain, certFile, keyFile string) error {
	home := m.acmeHome()
	if err := acmeDomainSelfCheck(domain); err != nil {
		return err
	}
	if err := acmePort80Free(); err != nil {
		return err
	}
	if err := ensureACMESh(home); err != nil {
		return err
	}
	if err := execACMESh(home, "--issue", "--standalone", "-d", domain); err != nil {
		return fmt.Errorf("acme.sh 签发证书失败（域名 %s）: %w", domain, err)
	}
	if err := execACMESh(home, "--install-cert", "-d", domain,
		"--key-file", keyFile, "--fullchain-file", certFile); err != nil {
		return fmt.Errorf("acme.sh 安装证书失败（域名 %s）: %w", domain, err)
	}
	// IsNotExist 容忍：测试桩不落盘；真实流程 install-cert 已写入 keyFile。
	if err := os.Chmod(keyFile, 0o600); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ACME 测试缝（§6：仅覆盖到"域名自检失败/80 端口被占"等路径，不做真实 LE 签发）。
var (
	lookupHostIPs   = net.LookupIP
	localIfaceAddrs = net.InterfaceAddrs
	acmePort80Free  = func() error {
		if err := probePortFree("tcp", 80); err != nil {
			return fmt.Errorf("80 端口被占用（acme.sh standalone 签发需要），请释放后重试或改用自签模式: %w", err)
		}
		return nil
	}
	execACMESh = func(home string, args ...string) error {
		full := append([]string{"--home", home}, args...)
		out, err := exec.Command(filepath.Join(home, "acme.sh"), full...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
)

// acmeDomainSelfCheck 校验域名解析结果包含本机网卡地址（standalone 签发要求 CA 回调本机）。
func acmeDomainSelfCheck(domain string) error {
	ips, err := lookupHostIPs(domain)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("域名 %s 解析失败，请确认域名已指向本服务器或改用自签模式", domain)
	}
	addrs, err := localIfaceAddrs()
	if err != nil {
		return fmt.Errorf("读取本机网卡地址失败: %w", err)
	}
	local := map[string]bool{}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip != nil {
			local[ip.String()] = true
		}
	}
	resolved := make([]string, 0, len(ips))
	for _, ip := range ips {
		if local[ip.String()] {
			return nil
		}
		resolved = append(resolved, ip.String())
	}
	return fmt.Errorf("域名 %s 解析结果（%s）不含本机地址，请确认域名指向本服务器或改用自签模式",
		domain, strings.Join(resolved, ","))
}

// ensureACMESh 首次使用时安装 acme.sh 至 home（官方安装脚本；离线给出指向性错误）。
// 下载复用 upgrade.go 的 downloadFile（requester 外部下载防护）。
func ensureACMESh(home string) error {
	if fileExists(filepath.Join(home, "acme.sh")) {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	script := filepath.Join(home, "get.acme.sh")
	if err := downloadFile("https://get.acme.sh", script); err != nil {
		return fmt.Errorf("下载 acme.sh 安装脚本失败（服务器需可访问外网，或改用自签模式）: %w", err)
	}
	out, err := exec.Command("sh", script, "--install", "--home", home).CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装 acme.sh 失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
