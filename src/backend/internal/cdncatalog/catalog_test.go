package cdncatalog

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleSource = `window.nodeData = {
  provinceBaseData: [{
    province: "河北",
    carriers: {
      unicom: "he-cu-v4.ip.zstaticcdn.com:80",
      mobile: "he-cm-v4.ip.zstaticcdn.com:80",
      telecom: "he-ct-v4.ip.zstaticcdn.com:80",
    },
  }],
  cityKeyList: ["he-xiongan-ct-v4", "he-langfang-cu-v4"],
  extraCityNodeMeta: {"he-shijiazhuang-cm-v4": "河北省石家庄市"},
};`

type fakeResolver map[string][]net.IP

func (r fakeResolver) LookupIP(_ context.Context, network, host string) ([]net.IP, error) {
	if network != "ip4" {
		return nil, &net.DNSError{Err: "unexpected network", Name: host}
	}
	addresses, ok := r[host]
	if !ok {
		return nil, &net.DNSError{Err: "not found", Name: host, IsNotFound: true}
	}
	return addresses, nil
}

func TestFetchBuildsProvinceNodesAndSameProvinceBackups(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "Lattix-panel" {
			t.Fatalf("User-Agent = %q", got)
		}
		if r.URL.Query().Get("lattix_refresh") == "" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("request does not bypass the compressed cache: %s headers=%v", r.URL, r.Header)
		}
		_, _ = w.Write([]byte(sampleSource))
	}))
	defer server.Close()

	resolver := fakeResolver{
		"he-ct-v4.ip.zstaticcdn.com":              {net.ParseIP("219.148.62.1")},
		"he-xiongan-ct-v4.ip.zstaticcdn.com":      {net.ParseIP("144.7.111.241")},
		"he-cu-v4.ip.zstaticcdn.com":              {net.ParseIP("110.249.198.60")},
		"he-langfang-cu-v4.ip.zstaticcdn.com":     {net.ParseIP("123.182.51.2")},
		"he-cm-v4.ip.zstaticcdn.com":              {net.ParseIP("111.62.113.1")},
		"he-shijiazhuang-cm-v4.ip.zstaticcdn.com": {net.ParseIP("111.11.12.13")},
	}
	now := time.Date(2026, time.July, 30, 13, 15, 25, 780_000_000, time.UTC)
	document, err := Fetch(context.Background(), server.Client(), resolver, server.URL, now)
	if err != nil {
		t.Fatal(err)
	}
	if document.Version != 1 || document.GeneratedAt != now {
		t.Fatalf("unexpected document metadata: %+v", document)
	}
	if len(document.Notes) != 3 {
		t.Fatalf("note count = %d, want 3", len(document.Notes))
	}
	if len(document.CDN) != 3 {
		t.Fatalf("node count = %d, want 3", len(document.CDN))
	}
	telecom := document.CDN[0]
	if telecom.Province != "河北" || telecom.ProvinceCode != "he" || telecom.ISP != "电信" || telecom.ISPCode != "ct" {
		t.Fatalf("unexpected telecom metadata: %+v", telecom)
	}
	if telecom.Port != 80 || telecom.Target != "he-ct-v4.ip.zstaticcdn.com" || telecom.IP != "219.148.62.1" {
		t.Fatalf("unexpected telecom node: %+v", telecom)
	}
	if telecom.Status != StatusNormal {
		t.Fatalf("telecom status = %q, want %q", telecom.Status, StatusNormal)
	}
	if telecom.Backup == nil || telecom.Backup.Port != 443 ||
		telecom.Backup.Target != "he-xiongan-ct-v4.ip.zstaticcdn.com" || telecom.Backup.IP != "144.7.111.241" ||
		telecom.Backup.Status != StatusNormal {
		t.Fatalf("unexpected telecom backup: %+v", telecom.Backup)
	}
	mobile := document.CDN[2]
	if mobile.Backup == nil || mobile.Backup.Target != "he-shijiazhuang-cm-v4.ip.zstaticcdn.com" {
		t.Fatalf("extra city metadata was not used as backup: %+v", mobile.Backup)
	}
}

func TestParseSourceRejectsExecutableJavaScript(t *testing.T) {
	_, err := parseSource([]byte(`window.nodeData = buildNodeData();`))
	if err == nil || !strings.Contains(err.Error(), "not a literal value") {
		t.Fatalf("error = %v, want restricted literal rejection", err)
	}
}

func TestFetchDoesNotReturnPartialCatalogOnDNSFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleSource))
	}))
	defer server.Close()

	_, err := Fetch(context.Background(), server.Client(), fakeResolver{}, server.URL, time.Now())
	if err == nil || !strings.Contains(err.Error(), "resolve CDN target") {
		t.Fatalf("error = %v, want DNS failure", err)
	}
}

func TestCheckDNSChecksPrimaryAndBackup(t *testing.T) {
	document := Document{
		Version: SchemaVersion,
		CDN: []Node{{
			Province: "河北", ISP: "电信", Target: "he-ct-v4.ip.zstaticcdn.com", IP: "219.148.62.1",
			Backup: &Backup{Target: "he-xiongan-ct-v4.ip.zstaticcdn.com", IP: "144.7.111.241"},
		}},
	}
	resolver := fakeResolver{
		"he-ct-v4.ip.zstaticcdn.com": {
			net.ParseIP("219.148.62.2"),
			net.ParseIP("219.148.62.1"),
		},
		"he-xiongan-ct-v4.ip.zstaticcdn.com": {net.ParseIP("144.7.111.242")},
	}
	mismatches, err := checkDNS(context.Background(), resolver, &document, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 1 {
		t.Fatalf("mismatches = %+v, want one backup mismatch", mismatches)
	}
	if mismatch := mismatches[0]; mismatch.Role != "backup" || mismatch.ExpectedIP != "144.7.111.241" ||
		len(mismatch.ResolvedIPs) != 1 || mismatch.ResolvedIPs[0] != "144.7.111.242" {
		t.Fatalf("unexpected mismatch: %+v", mismatch)
	}
	if document.CDN[0].Status != StatusNormal || document.CDN[0].Backup.Status != StatusFailed {
		t.Fatalf("unexpected statuses after mismatch: %+v", document.CDN[0])
	}
}

func TestCheckDNSMarksLookupFailureAndRecovers(t *testing.T) {
	document := Document{
		Version: SchemaVersion,
		CDN: []Node{{
			Province: "河北", ISP: "电信", Target: "he-ct-v4.ip.zstaticcdn.com", IP: "219.148.62.1",
			Status: StatusFailed,
		}},
	}
	resolver := fakeResolver{}
	mismatches, err := checkDNS(context.Background(), resolver, &document, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 1 || mismatches[0].Error == "" || document.CDN[0].Status != StatusFailed {
		t.Fatalf("lookup failure result: mismatches=%+v node=%+v", mismatches, document.CDN[0])
	}
	resolver["he-ct-v4.ip.zstaticcdn.com"] = []net.IP{net.ParseIP("219.148.62.1")}
	mismatches, err = checkDNS(context.Background(), resolver, &document, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 0 || document.CDN[0].Status != StatusNormal {
		t.Fatalf("recovered result: mismatches=%+v node=%+v", mismatches, document.CDN[0])
	}
}
