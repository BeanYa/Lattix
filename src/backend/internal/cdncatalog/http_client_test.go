package cdncatalog

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClientSupplementsEmptySystemRootsWithoutSkippingTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(server.Close)

	serverRoot := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.Certificate().Raw,
	})
	client, err := newHTTPClient(time.Second, unavailableSystemRoots, serverRoot)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil {
		t.Fatalf("unexpected transport: %T", client.Transport)
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("catalog client must not skip TLS verification")
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("request with supplemental root failed: %v", err)
	}
	_ = resp.Body.Close()
}

func TestHTTPClientRejectsUntrustedServer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	client, err := newHTTPClient(time.Second, unavailableSystemRoots, zstaticTrustRootPEM)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(server.URL); err == nil {
		t.Fatal("request unexpectedly trusted an unrelated certificate")
	}
}

func TestHTTPClientRejectsInvalidConfiguration(t *testing.T) {
	if _, err := newHTTPClient(0, unavailableSystemRoots, zstaticTrustRootPEM); err == nil {
		t.Fatal("zero timeout was accepted")
	}
	if _, err := newHTTPClient(time.Second, unavailableSystemRoots, []byte("not a certificate")); err == nil {
		t.Fatal("invalid trust root was accepted")
	}
}

func TestHTTPClientEmbedsBothZstaticTrustRoots(t *testing.T) {
	for name, root := range map[string][]byte{
		"digicert": zstaticTrustRootPEM,
		"aaa":      zstaticOverseasTrustRootPEM,
	} {
		root := root
		t.Run(name, func(t *testing.T) {
			if len(root) == 0 {
				t.Fatal("embedded trust root is empty")
			}
			block, _ := pem.Decode(root)
			if block == nil || block.Type != "CERTIFICATE" {
				t.Fatal("embedded trust root is not a PEM certificate")
			}
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				t.Fatalf("embedded trust root is invalid: %v", err)
			}
			if _, err := newHTTPClient(time.Second, unavailableSystemRoots, root); err != nil {
				t.Fatalf("embedded trust root could not be loaded: %v", err)
			}
		})
	}
}

func unavailableSystemRoots() (*x509.CertPool, error) {
	return nil, errors.New("system roots unavailable")
}
