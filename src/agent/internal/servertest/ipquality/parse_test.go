package ipquality

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lattix/shared"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(content)
}

func TestParseSingleIPv4(t *testing.T) {
	families, err := ParseScriptOutput(loadFixture(t, "single_ipv4.json"))
	if err != nil {
		t.Fatalf("ParseScriptOutput: %v", err)
	}
	if len(families) != 1 {
		t.Fatalf("families = %d, want 1", len(families))
	}
	family := families[0]
	if family.Family != shared.ServerTestIPv4 {
		t.Errorf("family = %s, want ipv4", family.Family)
	}
	if family.Head.IP != "36.235.123.45" {
		t.Errorf("head.IP = %q", family.Head.IP)
	}
	if family.Head.Version != "v2026-01-15" {
		t.Errorf("head.Version = %q", family.Head.Version)
	}
	if family.Info.ASN != "3462" || family.Info.Organization != "Data Communication Business Group" {
		t.Errorf("info = %+v", family.Info)
	}
	if family.Info.City.PostalCode != "" {
		t.Errorf("City.PostalCode = %q, want empty (null normalized)", family.Info.City.PostalCode)
	}
	if family.Info.Type != "Geo-consistent" {
		t.Errorf("info.Type = %q", family.Info.Type)
	}
	if family.Type.Usage["AbuseIPDB"] != "Line ISP" {
		t.Errorf("usage = %v", family.Type.Usage)
	}
	if family.Score["ipapi"] != "0.47%" || family.Score["IPQS"] != "null" {
		t.Errorf("score = %v (scores stay verbatim)", family.Score)
	}
	if family.Factor.Proxy["IP2LOCATION"] == nil || *family.Factor.Proxy["IP2LOCATION"] {
		t.Errorf("Factor.Proxy[IP2LOCATION] = %v, want false", family.Factor.Proxy["IP2LOCATION"])
	}
	if family.Factor.Tor["DBIP"] != nil {
		t.Errorf("Factor.Tor[DBIP] = %v, want nil", family.Factor.Tor["DBIP"])
	}
	if family.Factor.CountryCode["IPQS"] != "TW" {
		t.Errorf("Factor.CountryCode = %v", family.Factor.CountryCode)
	}
	if family.Media["Netflix"].Status != "Yes" || family.Media["Netflix"].Type != "Native" {
		t.Errorf("media = %v", family.Media)
	}
	if family.Mail.Port25 == nil || *family.Mail.Port25 {
		t.Errorf("Mail.Port25 = %v, want false", family.Mail.Port25)
	}
	if family.Mail.Providers["Gmail"] == nil || *family.Mail.Providers["Gmail"] {
		t.Errorf("Mail.Gmail = %v, want false", family.Mail.Providers["Gmail"])
	}
	if family.Mail.DNSBlacklist != (shared.IPQualityDNSBlacklist{Total: 439, Clean: 411, Marked: 28}) {
		t.Errorf("dnsbl = %+v", family.Mail.DNSBlacklist)
	}
	if len(family.Raw) == 0 {
		t.Error("raw copy is empty")
	}
}

func TestParseDualStack(t *testing.T) {
	families, err := ParseScriptOutput(loadFixture(t, "dualstack.json"))
	if err != nil {
		t.Fatalf("ParseScriptOutput: %v", err)
	}
	if len(families) != 2 {
		t.Fatalf("families = %d, want 2", len(families))
	}
	if families[0].Family != shared.ServerTestIPv4 || families[1].Family != shared.ServerTestIPv6 {
		t.Errorf("families = %s, %s; want ipv4, ipv6", families[0].Family, families[1].Family)
	}
	if families[1].Head.IP != "240e:390:caf2:6e00:85af:0:d0:0" {
		t.Errorf("family[1].Head.IP = %q", families[1].Head.IP)
	}
	if families[1].Mail.Port25 == nil || !*families[1].Mail.Port25 {
		t.Errorf("family[1] port25 = %v, want true", families[1].Mail.Port25)
	}
}

func TestParseEdges(t *testing.T) {
	families, err := ParseScriptOutput(loadFixture(t, "edges.json"))
	if err != nil {
		t.Fatalf("ParseScriptOutput: %v", err)
	}
	if len(families) != 1 {
		t.Fatalf("families = %d, want 1", len(families))
	}
	family := families[0]
	if family.Head.IP != "192.0.2.1" {
		t.Errorf("head.IP = %q", family.Head.IP)
	}
	if family.Head.Command != "" || family.Info.ASN != "" || family.Info.City.Name != "" {
		t.Errorf("null strings not normalized: %+v %+v", family.Head, family.Info)
	}
	if family.Type.Usage["IPinfo"] != "" {
		t.Errorf("usage null = %q", family.Type.Usage["IPinfo"])
	}
	if family.Score["IPQS"] != "null" {
		t.Errorf("score IPQS = %q, want verbatim \"null\"", family.Score["IPQS"])
	}
	if family.Factor.Proxy["IPinfo"] != nil || family.Factor.Tor["IPinfo"] == nil || !*family.Factor.Tor["IPinfo"] {
		t.Errorf("factors not decoded: %+v", family.Factor)
	}
	media, ok := family.Media["FutureStream"]
	if !ok || media.Status != "" {
		t.Errorf("unknown media service not preserved: %+v", family.Media)
	}
	if family.Mail.Port25 != nil {
		t.Errorf("Port25 = %v, want nil", family.Mail.Port25)
	}
	if family.Mail.Providers["NewMailService"] == nil || !*family.Mail.Providers["NewMailService"] {
		t.Errorf("new mail provider missing: %+v", family.Mail.Providers)
	}
	if family.Mail.Providers["Gmail"] != nil {
		t.Errorf("Gmail = %v, want nil", family.Mail.Providers["Gmail"])
	}
}

func TestParseNoDocument(t *testing.T) {
	if _, err := ParseScriptOutput(""); !errors.Is(err, ErrNoFamily) {
		t.Errorf("err = %v, want ErrNoFamily", err)
	}
	if _, err := ParseScriptOutput("not json at all"); err == nil || errors.Is(err, ErrNoFamily) {
		t.Errorf("err = %v, want a decode error", err)
	}
}
