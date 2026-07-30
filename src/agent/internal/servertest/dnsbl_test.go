package servertest

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestDNSBLQueryName(t *testing.T) {
	got := dnsblQueryName(netip.MustParseAddr("203.0.113.9"), "dnsbl.example")
	if got != "9.113.0.203.dnsbl.example" {
		t.Fatalf("query name = %q", got)
	}
}

func TestClassifyDNSBL(t *testing.T) {
	tests := []struct {
		name    string
		answers []string
		err     error
		want    string
	}{
		{name: "clean empty", want: "clean"},
		{name: "clean nxdomain", err: &net.DNSError{Err: "no such host", IsNotFound: true}, want: "clean"},
		{name: "blacklisted", answers: []string{"127.0.0.2"}, want: "blacklisted"},
		{name: "marked", answers: []string{"127.0.0.10"}, want: "marked"},
		{name: "unknown", err: errors.New("SERVFAIL"), want: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyDNSBL("dnsbl.example", test.answers, test.err).Status; got != test.want {
				t.Fatalf("status = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRunDNSBLSummaryDoesNotExposeAddress(t *testing.T) {
	original := dnsblSnapshot
	dnsblSnapshot = "a.example\nb.example\nc.example\n"
	t.Cleanup(func() { dnsblSnapshot = original })
	result := runDNSBLWithLookup(context.Background(), netip.MustParseAddr("203.0.113.9"), func(_ context.Context, host string) ([]string, error) {
		switch {
		case strings.HasSuffix(host, "a.example"):
			return []string{"127.0.0.2"}, nil
		case strings.HasSuffix(host, "b.example"):
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		default:
			return nil, &net.DNSError{Err: "server misbehaving", Name: host, Server: "192.0.2.53:53"}
		}
	})
	if result["status"] != "listed" || result["blacklisted"] != 1 || result["clean"] != 1 || result["unknown"] != 1 {
		t.Fatalf("unexpected summary: %#v", result)
	}
	encoded := strings.ReplaceAll(strings.TrimSpace(toJSON(t, result)), "\\u002e", ".")
	for _, private := range []string{"203.0.113.9", "9.113.0.203", "192.0.2.53"} {
		if strings.Contains(encoded, private) {
			t.Fatalf("report exposed DNS query detail %q", private)
		}
	}
}

func toJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
