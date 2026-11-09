package inspection

import (
	"testing"

	"lattix/shared"
)

func boolPtr(value bool) *bool { return &value }

func TestBuildSummarizesInspectionCategories(t *testing.T) {
	report := &shared.ServerTestReport{
		Status: shared.ServerTestCompletedWithErrors,
		Categories: []shared.ServerTestCategoryResult{
			{
				Category: shared.ServerTestIPQuality, Status: "available",
				Summary: map[string]any{"families": 2},
				IPQuality: &shared.IPQualityResult{
					ScriptVersion: "v1", ScriptStale: true,
					Families: []shared.IPQualityFamily{
						{
							Family: shared.ServerTestIPv4,
							Head:   shared.IPQualityHead{IP: "203.0.113.10"},
							Info:   shared.IPQualityInfo{Type: "机房", ASN: "AS64500", Organization: "Example", Region: shared.IPQualityRegion{Code: "JP"}},
							Factor: shared.IPQualityFactor{
								Proxy:  map[string]*bool{"db1": boolPtr(true)},
								VPN:    map[string]*bool{"db1": boolPtr(false)},
								Server: map[string]*bool{"db1": boolPtr(true)},
							},
							Media: map[string]shared.IPQualityMediaStatus{
								"Netflix": {Status: "Yes"},
								"Disney":  {Status: "No"},
							},
							Mail: shared.IPQualityMail{DNSBlacklist: shared.IPQualityDNSBlacklist{Total: 4, Clean: 2, Marked: 1, Blacklisted: 1}},
						},
					},
				},
			},
			{
				Category: shared.ServerTestTCPIPv4, Status: "limited",
				Summary: map[string]any{"targets": float64(3), "failed_targets": float64(1)},
				Items: []map[string]any{
					{"label": "广东电信", "rtt_avg_ms": 45.2, "loss_percent": 0.0},
					{"label": "广东联通", "rtt_avg_ms": 78.6, "loss_percent": 100.0, "error_code": "target_unreachable"},
					{"label": "广东移动", "rtt_avg_ms": 61.0, "loss_percent": 10.0},
				},
			},
			{
				Category: shared.ServerTestConnectivity, Status: "available",
				Summary: map[string]any{"targets": float64(2), "failed_targets": float64(0)},
				Items: []map[string]any{
					{"rtt_median_ms": 120.0},
					{"rtt_median_ms": 80.0},
				},
			},
			{
				Category: shared.ServerTestSpeed, Status: "failed",
				ErrorCode: "unsupported", ErrorMessage: "not selected",
			},
		},
	}

	summary := Build(report)
	if summary.Status != shared.ServerTestCompletedWithErrors {
		t.Fatalf("status = %q", summary.Status)
	}
	if summary.IPQuality == nil || len(summary.IPQuality.Families) != 1 {
		t.Fatalf("ip quality = %+v", summary.IPQuality)
	}
	family := summary.IPQuality.Families[0]
	if family.Family != "ipv4" || family.IP != "203.0.113.10" || family.Type != "机房" || family.CountryCode != "JP" {
		t.Fatalf("family identity = %+v", family)
	}
	if family.DNSBLBlacklisted != 1 || family.DNSBLMarked != 1 {
		t.Fatalf("dnsbl = %+v", family)
	}
	if family.MediaUnlocked != 1 || family.MediaTotal != 2 {
		t.Fatalf("media = %+v", family)
	}
	if family.RiskFlags != 2 {
		t.Fatalf("risk flags = %d", family.RiskFlags)
	}
	if summary.TCPIPv4 == nil || summary.TCPIPv4.Targets != 3 || summary.TCPIPv4.Failed != 1 {
		t.Fatalf("tcp = %+v", summary.TCPIPv4)
	}
	if summary.TCPIPv4.AvgRTTMS == nil || *summary.TCPIPv4.AvgRTTMS != 61.6 {
		t.Fatalf("tcp avg = %v", summary.TCPIPv4.AvgRTTMS)
	}
	if summary.TCPIPv4.MaxLossPercent == nil || *summary.TCPIPv4.MaxLossPercent != 100 {
		t.Fatalf("tcp max loss = %v", summary.TCPIPv4.MaxLossPercent)
	}
	if summary.TCPIPv4.WorstTarget != "广东联通" {
		t.Fatalf("worst target = %q", summary.TCPIPv4.WorstTarget)
	}
	if summary.Connectivity == nil || summary.Connectivity.AvgRTTMS == nil || *summary.Connectivity.AvgRTTMS != 100 {
		t.Fatalf("connectivity = %+v", summary.Connectivity)
	}
	if len(summary.Warnings) == 0 {
		t.Fatal("expected warning for failed speed category")
	}
}

func TestBuildToleratesMissingReport(t *testing.T) {
	summary := Build(nil)
	if summary.Status != shared.ServerTestFailed {
		t.Fatalf("status = %q", summary.Status)
	}
	if len(summary.Warnings) == 0 {
		t.Fatal("expected warnings")
	}
}

func TestBuildPrefersAgentSummaryCounts(t *testing.T) {
	report := &shared.ServerTestReport{
		Status: shared.ServerTestSucceeded,
		Categories: []shared.ServerTestCategoryResult{
			{
				Category: shared.ServerTestConnectivity, Status: "limited",
				Summary: map[string]any{"targets": float64(76), "failed_targets": float64(5)},
			},
		},
	}
	summary := Build(report)
	if summary.Connectivity == nil || summary.Connectivity.Targets != 76 || summary.Connectivity.Failed != 5 {
		t.Fatalf("connectivity = %+v", summary.Connectivity)
	}
}
