package panel

import (
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 域名路径模式（tls_mode=path）的文件约定（certbot 风格）：
// 证书 <tls-dir>/<域名>/fullchain.pem，私钥 <tls-dir>/<域名>/privkey.pem。
// 外部 ACME（安装脚本）申请/续期后直接写入该目录即可。
const (
	DirCertFileName = "fullchain.pem"
	DirKeyFileName  = "privkey.pem"
)

// ValidTLSDomain 校验域名路径模式的域名：合法主机名且不能逃逸证书目录。
func ValidTLSDomain(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 || strings.ContainsAny(domain, "/\\") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// DirCertPaths 返回域名对应的证书/私钥文件路径。
func DirCertPaths(dir, domain string) (certPath, keyPath string) {
	return filepath.Join(dir, domain, DirCertFileName), filepath.Join(dir, domain, DirKeyFileName)
}

// NewDirCertGetter 返回 tls.Config.GetCertificate 回调：
// 按文件 mtime 缓存证书，外部 ACME 续期替换文件后下一次握手即加载新证书（免重启）；
// 加载失败（如续期写一半）回退已缓存证书，保证握手不中断。
func NewDirCertGetter(dir, domain string) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	certPath, keyPath := DirCertPaths(dir, domain)
	type state struct {
		cert    *tls.Certificate
		modTime time.Time
	}
	var mu sync.Mutex
	var st state
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()
		var latest time.Time
		for _, p := range []string{certPath, keyPath} {
			info, err := os.Stat(p)
			if err != nil {
				if st.cert != nil {
					log.Printf("tls-dir: stat %s 失败，沿用缓存证书: %v", p, err)
					return st.cert, nil
				}
				return nil, fmt.Errorf("stat %s: %w", p, err)
			}
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
		}
		if st.cert != nil && latest.Equal(st.modTime) {
			return st.cert, nil
		}
		kp, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			if st.cert != nil {
				log.Printf("tls-dir: 加载 %s 证书失败，沿用缓存证书: %v", domain, err)
				return st.cert, nil
			}
			return nil, fmt.Errorf("load %s key pair: %w", domain, err)
		}
		if st.cert == nil {
			log.Printf("tls-dir: 已加载 %s 证书（%s）", domain, certPath)
		} else {
			log.Printf("tls-dir: 检测到证书文件变更，已热加载 %s 新证书", domain)
		}
		st = state{cert: &kp, modTime: latest}
		return st.cert, nil
	}
}
