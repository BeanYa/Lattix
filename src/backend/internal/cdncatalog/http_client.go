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

// DigiCert Global Root G2 is the trust anchor used by the fixed Zstatic
// catalog endpoint inside mainland China (leaf CN=zstatic.net → GeoTrust TLS
// RSA CA G1 → DigiCert Global Root G2).
// Source: https://cacerts.digicert.com/DigiCertGlobalRootG2.crt.pem
// SHA-256: CB:3C:CB:B7:60:31:E5:E0:13:8F:8D:D3:9A:23:F9:DE:47:FF:C3:5E:43:C1:14:4C:EA:27:D4:6A:5A:B1:CB:5F
//
//go:embed certs/digicert_global_root_g2.pem
var zstaticTrustRootPEM []byte

// AAA Certificate Services is the trust anchor used by the same endpoint on
// overseas Cloudflare edge nodes (leaf CN=zstaticcdn.com → Cloudflare TLS
// Issuing ECC CA 1 → SSL.com TLS Transit ECC CA R2 → AAA Certificate Services).
// Source: Windows LocalMachine\Root "AAA Certificate Services" (Comodo CA Limited)
// SHA-256: D7:A7:A0:FB:5D:7E:27:31:D7:71:E9:48:4E:BC:DE:F7:1D:5F:0C:3E:0A:29:48:78:2B:C8:3E:E0:EA:69:9E:F4
//
//go:embed certs/aaa_certificate_services.pem
var zstaticOverseasTrustRootPEM []byte

// NewHTTPClient returns the dedicated client for fetching the Zstatic catalog.
// It preserves the default transport's proxy and connection behavior while
// supplementing, rather than replacing, the host's trusted roots.
func NewHTTPClient(timeout time.Duration) (*http.Client, error) {
	return newHTTPClient(timeout, x509.SystemCertPool, zstaticTrustRootPEM, zstaticOverseasTrustRootPEM)
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
