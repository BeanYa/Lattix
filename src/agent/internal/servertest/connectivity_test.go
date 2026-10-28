package servertest

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"lattix/shared"
)

func insecureConnectivityClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			ForceAttemptHTTP2: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       5 * time.Second,
	}
}

func TestRunConnectivityReachableTarget(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("favicon"))
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split test address: %v", err)
	}
	port, _ := strconv.Atoi(portText)
	target := shared.ServerTestTarget{
		ID: "connectivity:gl:test", Category: shared.ServerTestConnectivity,
		Label: "Test", Province: "全球", AddressFamily: shared.ServerTestDualStack,
		Host: host, Port: port, Path: "/favicon.ico",
	}
	runner := NewRunner("test", t.TempDir())
	originalFactory := connectivityClientFactory
	connectivityClientFactory = func(bool) *http.Client { return insecureConnectivityClient() }
	t.Cleanup(func() { connectivityClientFactory = originalFactory })
	result := runner.runConnectivity(context.Background(), shared.ServerTestConnectivity,
		[]shared.ServerTestTarget{target},
		func(int, int, string) {},
	)
	if result.Status != "available" {
		t.Fatalf("status = %q (%+v)", result.Status, result)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d", len(result.Items))
	}
	item := result.Items[0]
	if item["group"] != "全球" {
		t.Fatalf("group = %v", item["group"])
	}
	if _, ok := item["rtt_median_ms"].(float64); !ok {
		t.Fatalf("rtt_median_ms missing: %+v", item)
	}
	samples, ok := item["samples"].([]float64)
	if !ok || len(samples) != connectivityProbeAttempts {
		t.Fatalf("samples = %#v", item["samples"])
	}
}

func TestRunConnectivityUnreachableTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close() // 端口已释放，连接必然被拒
	host, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	target := shared.ServerTestTarget{
		ID: "connectivity:us:dead", Category: shared.ServerTestConnectivity,
		Label: "Dead", Province: "美国", AddressFamily: shared.ServerTestIPv4,
		Host: host, Port: port, Path: "/",
	}
	result := runConnectivityTarget(context.Background(), target, insecureConnectivityClient())
	if result.ErrorCode != "target_unreachable" || result.LossPercent != 100 {
		t.Fatalf("result = %+v", result)
	}
	if result.Sent != connectivityProbeAttempts || result.Received != 0 {
		t.Fatalf("sent/received = %d/%d", result.Sent, result.Received)
	}
}

func TestMedianMinMax(t *testing.T) {
	median, min, max := medianMinMax([]float64{30, 10, 20})
	if median != 20 || min != 10 || max != 30 {
		t.Fatalf("odd: %v/%v/%v", median, min, max)
	}
	median, min, max = medianMinMax([]float64{40, 10, 30, 20})
	if median != 25 || min != 10 || max != 40 {
		t.Fatalf("even: %v/%v/%v", median, min, max)
	}
}
