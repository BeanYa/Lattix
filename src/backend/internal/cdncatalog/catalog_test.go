package cdncatalog

import (
	"context"
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

func TestFetchBuildsReadableDualStackCatalogWithoutDNS(t *testing.T) {
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

	now := time.Date(2026, time.July, 30, 13, 15, 25, 780_000_000, time.UTC)
	document, err := Fetch(context.Background(), server.Client(), server.URL, now)
	if err != nil {
		t.Fatal(err)
	}
	if document.Version != 1 || document.Source.FetchedAt != now || document.Source.ParserVersion != ParserVersion {
		t.Fatalf("unexpected document metadata: %+v", document.Source)
	}
	if len(document.Source.SourceSHA256) != 64 || len(document.Source.CatalogSHA256) != 64 {
		t.Fatalf("missing hashes: %+v", document.Source)
	}
	if document.Counts != (Counts{Provinces: 1, ProvinceTargets: 9, CityIPv4Targets: 3}) {
		t.Fatalf("unexpected counts: %+v", document.Counts)
	}
	if len(document.Provinces) != 1 || len(document.Cities) != 3 {
		t.Fatalf("unexpected catalog sizes: provinces=%d cities=%d", len(document.Provinces), len(document.Cities))
	}
	telecom := document.Provinces[0].Carriers.Telecom
	if telecom.IPv4.Host != "he-ct-v4.ip.zstaticcdn.com" || telecom.IPv4.Port != 80 || telecom.IPv4.Label != "河北电信" {
		t.Fatalf("unexpected IPv4 endpoint: %+v", telecom.IPv4)
	}
	if telecom.IPv4.Backup == nil || telecom.IPv4.Backup.Host != "he-xiongan-ct-v4.ip.zstaticcdn.com" || telecom.IPv4.Backup.Label != "河北雄安电信" {
		t.Fatalf("unexpected IPv4 backup: %+v", telecom.IPv4.Backup)
	}
	if telecom.IPv6.Host != "he-ct-v6.ip.zstaticcdn.com" || telecom.IPv6.AddressFamily != "ipv6" {
		t.Fatalf("unexpected IPv6 endpoint: %+v", telecom.IPv6)
	}
	if telecom.IPv6.Backup == nil || telecom.IPv6.Backup.Host != "he-ct-dualstack.ip.zstaticcdn.com" || telecom.IPv6.Backup.AddressFamily != "ipv6" {
		t.Fatalf("unexpected IPv6 backup: %+v", telecom.IPv6.Backup)
	}
	if got := document.Provinces[0].Carriers.Mobile.IPv4.Backup.Label; got != "河北石家庄移动" {
		t.Fatalf("extra city metadata label = %q", got)
	}
}

func TestFetchRejectsRedirectInsteadOfParsingItsHTML(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>marketing page</body></html>"))
	}))
	defer target.Close()
	redirecter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirecter.Close()

	client, err := newHTTPClient(time.Second, unavailableSystemRoots)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Fetch(context.Background(), client, redirecter.URL, time.Now())
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("error = %v, want redirect rejection", err)
	}
}

func TestParseSourceRejectsExecutableJavaScript(t *testing.T) {
	_, _, err := parseSource([]byte(`window.nodeData = buildNodeData();`))
	if err == nil || !strings.Contains(err.Error(), "not a literal value") {
		t.Fatalf("error = %v, want restricted literal rejection", err)
	}
}

func TestBuildDocumentRejectsMalformedCarrierAndCity(t *testing.T) {
	source, _, err := parseSource([]byte(sampleSource))
	if err != nil {
		t.Fatal(err)
	}
	source.ProvinceBaseData[0].Carriers["telecom"] = "example.com:80"
	if _, err := buildDocument(source); err == nil || !strings.Contains(err.Error(), "outside zstaticcdn.com") {
		t.Fatalf("error = %v, want target policy failure", err)
	}
	source, _, _ = parseSource([]byte(sampleSource))
	source.CityKeyList = append(source.CityKeyList, "bad-key")
	if _, err := buildDocument(source); err == nil || !strings.Contains(err.Error(), "invalid city node key") {
		t.Fatalf("error = %v, want city key failure", err)
	}
}

// 与 https://www.zstaticcdn.com/api/v1/DescribeAllNodes 的实际返回结构一致（字段名大小写敏感）。
const sampleJSONSource = `{
  "UpdatedAt": "2026-09-15T12:27:24Z",
  "ProvinceNodes": [
    {
      "province": "河北",
      "carriers": {
        "mobile": "he-cm-v4.ip.zstaticcdn.com:80",
        "unicom": "he-cu-v4.ip.zstaticcdn.com:80",
        "telecom": "he-ct-v4.ip.zstaticcdn.com:80"
      }
    }
  ],
  "CityNodes": [
    {"city": "雄安", "province": "河北", "carrier": "telecom", "endpoint": "he-xiongan-ct-v4.ip.zstaticcdn.com:443"},
    {"city": "石家庄", "province": "河北", "carrier": "mobile", "endpoint": "he-shijiazhuang-cm-v4.ip.zstaticcdn.com:443"}
  ]
}`

func TestFetchParsesDescribeAllNodesJSONCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(sampleJSONSource))
	}))
	defer server.Close()

	now := time.Date(2026, time.October, 27, 8, 0, 0, 0, time.UTC)
	document, err := Fetch(context.Background(), server.Client(), server.URL, now)
	if err != nil {
		t.Fatal(err)
	}
	if document.Source.ParserVersion != JSONParserVersion {
		t.Fatalf("ParserVersion = %q, want %q", document.Source.ParserVersion, JSONParserVersion)
	}
	if document.Counts != (Counts{Provinces: 1, ProvinceTargets: 9, CityIPv4Targets: 2}) {
		t.Fatalf("unexpected counts: %+v", document.Counts)
	}
	telecom := document.Provinces[0].Carriers.Telecom
	if telecom.IPv4.Host != "he-ct-v4.ip.zstaticcdn.com" || telecom.IPv4.Port != 80 || telecom.IPv4.Label != "河北电信" {
		t.Fatalf("unexpected IPv4 endpoint: %+v", telecom.IPv4)
	}
	if telecom.IPv4.Backup == nil || telecom.IPv4.Backup.Host != "he-xiongan-ct-v4.ip.zstaticcdn.com" || telecom.IPv4.Backup.Label != "河北雄安电信" {
		t.Fatalf("unexpected IPv4 backup: %+v", telecom.IPv4.Backup)
	}
	if telecom.IPv6.Host != "he-ct-v6.ip.zstaticcdn.com" || telecom.IPv6.Backup == nil || telecom.IPv6.Backup.Host != "he-ct-dualstack.ip.zstaticcdn.com" {
		t.Fatalf("unexpected IPv6 endpoints: %+v", telecom.IPv6)
	}
	if got := document.Provinces[0].Carriers.Mobile.IPv4.Backup; got == nil || got.Label != "河北石家庄移动" {
		t.Fatalf("city label from API metadata = %+v", got)
	}
}

func TestParseSourceReportsParserVersionPerFormat(t *testing.T) {
	if _, version, err := parseSource([]byte(sampleSource)); err != nil || version != ParserVersion {
		t.Fatalf("literal version = %q err = %v", version, err)
	}
	if _, version, err := parseSource([]byte(sampleJSONSource)); err != nil || version != JSONParserVersion {
		t.Fatalf("json version = %q err = %v", version, err)
	}
}

func TestParseJSONSourceRejectsInvalidDocuments(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"truncated json": {body: `{"ProvinceNodes": [`, want: "invalid DescribeAllNodes JSON"},
		"city endpoint outside suffix": {
			body: `{"ProvinceNodes":[{"province":"河北","carriers":{"telecom":"he-ct-v4.ip.zstaticcdn.com:80"}}],
			       "CityNodes":[{"city":"外地","carrier":"telecom","endpoint":"example.com:443"}]}`,
			want: "outside zstaticcdn.com",
		},
		"city endpoint wrong port": {
			body: `{"ProvinceNodes":[{"province":"河北","carriers":{"telecom":"he-ct-v4.ip.zstaticcdn.com:80"}}],
			       "CityNodes":[{"city":"雄安","carrier":"telecom","endpoint":"he-xiongan-ct-v4.ip.zstaticcdn.com:80"}]}`,
			want: "must use port 443",
		},
		"city carrier mismatch": {
			body: `{"ProvinceNodes":[{"province":"河北","carriers":{"telecom":"he-ct-v4.ip.zstaticcdn.com:80"}}],
			       "CityNodes":[{"city":"雄安","carrier":"mobile","endpoint":"he-xiongan-ct-v4.ip.zstaticcdn.com:443"}]}`,
			want: `declares carrier "mobile"`,
		},
		"city unknown carrier": {
			body: `{"ProvinceNodes":[{"province":"河北","carriers":{"telecom":"he-ct-v4.ip.zstaticcdn.com:80"}}],
			       "CityNodes":[{"city":"雄安","carrier":"broadband","endpoint":"he-xiongan-ct-v4.ip.zstaticcdn.com:443"}]}`,
			want: "unknown carrier",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseSource([]byte(testCase.body)); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}
