// Package inspection derives the compact server-inspection summary persisted by
// the panel. The weekly inspection reuses the existing server-test task and only
// enables ip_quality, tcp_ipv4 and connectivity; the full report stays in
// server_test_tasks (agent keeps the latest result), while this package turns it
// into a small, history-friendly form for the runtime monitor.
package inspection

import (
	"math"
	"strings"

	"lattix/shared"
)

// Summary is the panel-owned, compact result of one inspection run. It is
// intentionally independent of any storage type so it can be embedded in the
// run table and returned as JSON without leaking full category payloads.
type Summary struct {
	Status       shared.ServerTestTaskStatus `json:"status"`
	IPQuality    *IPQualitySummary           `json:"ip_quality,omitempty"`
	TCPIPv4      *TCPIPv4Summary             `json:"tcp_ipv4,omitempty"`
	Connectivity *ConnectivitySummary        `json:"connectivity,omitempty"`
	Warnings     []string                    `json:"warnings,omitempty"`
}

// IPQualitySummary aggregates the two address-family reports produced by the
// upstream IPQuality script. DNSBL and media counters are useful at a glance;
// per-database score rows remain in the full report.
type IPQualitySummary struct {
	Status        string                   `json:"status"`
	ScriptVersion string                   `json:"script_version,omitempty"`
	ScriptStale   bool                     `json:"script_stale,omitempty"`
	Families      []IPQualityFamilySummary `json:"families,omitempty"`
}

type IPQualityFamilySummary struct {
	Family           string `json:"family"`
	IP               string `json:"ip,omitempty"`
	Type             string `json:"type,omitempty"`
	ASN              string `json:"asn,omitempty"`
	Organization     string `json:"organization,omitempty"`
	CountryCode      string `json:"country_code,omitempty"`
	DNSBLTotal       int    `json:"dnsbl_total"`
	DNSBLClean       int    `json:"dnsbl_clean"`
	DNSBLMarked      int    `json:"dnsbl_marked"`
	DNSBLBlacklisted int    `json:"dnsbl_blacklisted"`
	MediaUnlocked    int    `json:"media_unlocked"`
	MediaTotal       int    `json:"media_total"`
	RiskFlags        int    `json:"risk_flags"`
}

// TCPIPv4Summary aggregates the 31 provincial three-carrier targets.
type TCPIPv4Summary struct {
	Status         string   `json:"status"`
	Targets        int      `json:"targets"`
	Failed         int      `json:"failed"`
	AvgRTTMS       *float64 `json:"avg_rtt_ms,omitempty"`
	MaxLossPercent *float64 `json:"max_loss_percent,omitempty"`
	// WorstTarget is the label of the target with the highest loss, useful for
	// pointing the administrator at the problematic carrier/province.
	WorstTarget string `json:"worst_target,omitempty"`
}

// ConnectivitySummary aggregates the HTTPS reachability targets.
type ConnectivitySummary struct {
	Status   string   `json:"status"`
	Targets  int      `json:"targets"`
	Failed   int      `json:"failed"`
	AvgRTTMS *float64 `json:"avg_rtt_ms,omitempty"`
}

// Build converts a terminal server-test report into a compact summary. It
// tolerates missing categories and unknown item shapes: a malformed report must
// never prevent the panel from recording that the inspection ran.
func Build(report *shared.ServerTestReport) Summary {
	summary := Summary{}
	if report == nil {
		summary.Status = shared.ServerTestFailed
		summary.Warnings = append(summary.Warnings, "报告为空")
		return summary
	}
	summary.Status = report.Status
	for _, category := range report.Categories {
		switch category.Category {
		case shared.ServerTestIPQuality:
			summary.IPQuality = buildIPQuality(category)
		case shared.ServerTestTCPIPv4:
			summary.TCPIPv4 = buildTCPIPv4(category)
		case shared.ServerTestConnectivity:
			summary.Connectivity = buildConnectivity(category)
		}
		if category.ErrorCode != "" || category.ErrorMessage != "" {
			summary.Warnings = append(summary.Warnings, categoryWarning(category))
		}
	}
	if summary.IPQuality == nil {
		summary.Warnings = append(summary.Warnings, "IP 质量结果缺失")
	}
	if summary.TCPIPv4 == nil {
		summary.Warnings = append(summary.Warnings, "IPv4 检测结果缺失")
	}
	if summary.Connectivity == nil {
		summary.Warnings = append(summary.Warnings, "连通性结果缺失")
	}
	return summary
}

func buildIPQuality(category shared.ServerTestCategoryResult) *IPQualitySummary {
	out := &IPQualitySummary{Status: category.Status}
	if category.IPQuality != nil {
		out.ScriptVersion = category.IPQuality.ScriptVersion
		out.ScriptStale = category.IPQuality.ScriptStale
		for _, family := range category.IPQuality.Families {
			dnsbl := family.Mail.DNSBlacklist
			mediaUnlocked, mediaTotal := countMedia(family.Media)
			out.Families = append(out.Families, IPQualityFamilySummary{
				Family:           string(family.Family),
				IP:               family.Head.IP,
				Type:             cleanString(family.Info.Type),
				ASN:              cleanString(family.Info.ASN),
				Organization:     cleanString(family.Info.Organization),
				CountryCode:      cleanString(family.Info.Region.Code),
				DNSBLTotal:       dnsbl.Total,
				DNSBLClean:       dnsbl.Clean,
				DNSBLMarked:      dnsbl.Marked,
				DNSBLBlacklisted: dnsbl.Blacklisted,
				MediaUnlocked:    mediaUnlocked,
				MediaTotal:       mediaTotal,
				RiskFlags:        countRiskFlags(family.Factor),
			})
		}
	}
	return out
}

func countMedia(media map[string]shared.IPQualityMediaStatus) (unlocked, total int) {
	for _, status := range media {
		total++
		if strings.EqualFold(strings.TrimSpace(status.Status), "Yes") {
			unlocked++
		}
	}
	return unlocked, total
}

func countRiskFlags(factor shared.IPQualityFactor) int {
	flagged := 0
	for _, values := range []map[string]*bool{
		factor.Proxy, factor.Tor, factor.VPN, factor.Server, factor.Abuser, factor.Robot,
	} {
		for _, value := range values {
			if value != nil && *value {
				flagged++
			}
		}
	}
	return flagged
}

func buildTCPIPv4(category shared.ServerTestCategoryResult) *TCPIPv4Summary {
	out := &TCPIPv4Summary{Status: category.Status}
	out.Targets, out.Failed = summaryCounts(category.Summary, category.Items)
	var rttSum float64
	var rttCount int
	var lossCount int
	var maxLoss float64
	for _, item := range category.Items {
		if value, ok := numberValue(item["rtt_avg_ms"]); ok {
			rttSum += value
			rttCount++
		}
		if value, ok := numberValue(item["loss_percent"]); ok {
			lossCount++
			if value > maxLoss {
				maxLoss = value
				out.WorstTarget = stringValue(item["label"])
			}
		}
	}
	if rttCount > 0 {
		average := round1(rttSum / float64(rttCount))
		out.AvgRTTMS = &average
	}
	if lossCount > 0 {
		max := round1(maxLoss)
		out.MaxLossPercent = &max
	}
	if out.Targets == 0 {
		out.Targets = len(category.Items)
	}
	return out
}

func buildConnectivity(category shared.ServerTestCategoryResult) *ConnectivitySummary {
	out := &ConnectivitySummary{Status: category.Status}
	out.Targets, out.Failed = summaryCounts(category.Summary, category.Items)
	var rttSum float64
	var rttCount int
	for _, item := range category.Items {
		if value, ok := numberValue(item["rtt_median_ms"]); ok {
			rttSum += value
			rttCount++
		}
	}
	if rttCount > 0 {
		average := round1(rttSum / float64(rttCount))
		out.AvgRTTMS = &average
	}
	if out.Targets == 0 {
		out.Targets = len(category.Items)
	}
	return out
}

// summaryCounts prefers the agent-provided summary counters and falls back to
// counting item error codes when a legacy/partial report omitted them.
func summaryCounts(summary map[string]any, items []map[string]any) (targets, failed int) {
	if value, ok := numberValue(summary["targets"]); ok {
		targets = int(value)
	}
	if value, ok := numberValue(summary["failed_targets"]); ok {
		failed = int(value)
	}
	if targets == 0 {
		targets = len(items)
	}
	if failed == 0 {
		for _, item := range items {
			if stringValue(item["error_code"]) != "" {
				failed++
			}
		}
	}
	if failed > targets {
		failed = targets
	}
	return targets, failed
}

func categoryWarning(category shared.ServerTestCategoryResult) string {
	label := string(category.Category)
	if category.ErrorCode != "" {
		label += ": " + category.ErrorCode
	}
	if category.ErrorMessage != "" {
		label += " · " + category.ErrorMessage
	}
	return label
}

func cleanString(value string) string {
	value = strings.TrimSpace(value)
	if value == "null" {
		return ""
	}
	return value
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case jsonNumber:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

// jsonNumber keeps the package free of an encoding/json import solely for the
// json.Number case; map values decoded by the caller are normally float64.
type jsonNumber interface {
	Float64() (float64, error)
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}
