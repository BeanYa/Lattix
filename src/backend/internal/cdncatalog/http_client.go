package cdncatalog

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DigiCert Global Root G2 is the trust anchor currently used by the fixed
// Zstatic catalog endpoint. Keeping it alongside the system pool allows native
// installs on minimal hosts to validate the endpoint without disabling TLS.
// Source: https://cacerts.digicert.com/DigiCertGlobalRootG2.crt.pem
// SHA-256: CB:3C:CB:B7:60:31:E5:E0:13:8F:8D:D3:9A:23:F9:DE:47:FF:C3:5E:43:C1:14:4C:EA:27:D4:6A:5A:B1:CB:5F
//
//go:embed certs/digicert_global_root_g2.pem
var zstaticTrustRootPEM []byte

// NewHTTPClient returns the dedicated client for fetching the Zstatic catalog.
// It preserves the default transport's proxy and connection behavior while
// supplementing, rather than replacing, the host's trusted roots.
func NewHTTPClient(timeout time.Duration) (*http.Client, error) {
	return newHTTPClient(timeout, x509.SystemCertPool, zstaticTrustRootPEM)
}

func newHTTPClient(
	timeout time.Duration,
	loadSystemRoots func() (*x509.CertPool, error),
	additionalRoots ...[]byte,
) (*http.Client, error) {
	if timeout <= 0 {
		return nil, errors.New("CDN catalog HTTP timeout must be positive")
	}
	roots, _ := loadSystemRoots()
	if roots == nil {
		roots = x509.NewCertPool()
	}
	for _, rootPEM := range additionalRoots {
		if !roots.AppendCertsFromPEM(rootPEM) {
			return nil, errors.New("parse CDN catalog trust root")
		}
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("CDN catalog default transport has type %T", http.DefaultTransport)
	}
	transport := base.Clone()
	transport.TLSClientConfig = cloneTLSConfig(transport.TLSClientConfig)
	transport.TLSClientConfig.RootCAs = roots
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func cloneTLSConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return &tls.Config{}
	}
	return config.Clone()
}
