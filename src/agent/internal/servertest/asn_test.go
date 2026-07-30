package servertest

import (
	"context"
	"net/netip"
	"testing"
)

func TestCymruOriginQuery(t *testing.T) {
	if got := cymruOriginQuery(netip.MustParseAddr("1.2.3.4")); got != "4.3.2.1.origin.asn.cymru.com" {
		t.Fatalf("IPv4 query = %q", got)
	}
	want6 := "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.origin6.asn.cymru.com"
	if got := cymruOriginQuery(netip.MustParseAddr("2001:db8::1")); got != want6 {
		t.Fatalf("IPv6 query = %q", got)
	}
}

func TestRunASNEnrichment(t *testing.T) {
	result := runASNEnrichmentWithLookup(context.Background(), netip.MustParseAddr("1.1.1.1"), func(_ context.Context, query string) ([]string, error) {
		if query == "1.1.1.1.origin.asn.cymru.com" {
			return []string{"13335 | 1.1.1.0/24 | AU | apnic | 2011-08-11"}, nil
		}
		return []string{"13335 | AU | apnic | 2010-07-14 | CLOUDFLARENET"}, nil
	})
	if result["status"] != "available" || result["asn"] != int64(13335) || result["name"] != "CLOUDFLARENET" {
		t.Fatalf("unexpected ASN result: %#v", result)
	}
}
