package servertest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"lattix/shared"
)

const (
	standardProbeCount      = 30
	internationalProbeCount = 15
	targetConcurrency       = 16
	connectTimeout          = 10 * time.Second
	targetTimeout           = 30 * time.Second
)

type tcpTargetResult struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Carrier       string   `json:"carrier,omitempty"`
	Province      string   `json:"province,omitempty"`
	AddressFamily string   `json:"address_family"`
	ProbeMethod   string   `json:"probe_method"`
	Degraded      bool     `json:"degraded"`
	Sent          int      `json:"sent"`
	Received      int      `json:"received"`
	LossPercent   float64  `json:"loss_percent"`
	RTTMinMS      *float64 `json:"rtt_min_ms,omitempty"`
	RTTAvgMS      *float64 `json:"rtt_avg_ms,omitempty"`
	RTTMaxMS      *float64 `json:"rtt_max_ms,omitempty"`
	ResponseType  string   `json:"response_type,omitempty"`
	Selected      string   `json:"selected"`
	ErrorCode     string   `json:"error_code,omitempty"`
	ErrorMessage  string   `json:"error_message,omitempty"`
	FallbackChain []string `json:"fallback_chain,omitempty"`
}

func (r *Runner) runTCP(ctx context.Context, category shared.ServerTestCategory, targets []shared.ServerTestTarget, update func(int, int, string)) shared.ServerTestCategoryResult {
	if len(targets) == 0 {
		return unsupportedResult(category, "catalog_targets_unavailable", "no targets were supplied for this test category")
	}
	probeCount := standardProbeCount
	if category == shared.ServerTestInternational {
		probeCount = internationalProbeCount
	}
	results := make([]tcpTargetResult, len(targets))
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := targetConcurrency
	if len(targets) < workers {
		workers = len(targets)
	}
	completed := 0
	var completedMu sync.Mutex
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results[index] = runTCPTarget(ctx, targets[index], probeCount)
				completedMu.Lock()
				completed++
				current := completed
				completedMu.Unlock()
				update(current, len(targets), targets[index].Label)
			}
		}()
	}
sendJobs:
	for index := range targets {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendJobs
		}
	}
	close(jobs)
	wg.Wait()
	for index := range results {
		if results[index].ID == "" {
			results[index] = interruptedTCPTarget(targets[index], ctx.Err())
		}
	}
	items := make([]map[string]any, 0, len(results))
	failed := 0
	for _, result := range results {
		encoded := map[string]any{
			"id": result.ID, "label": result.Label, "carrier": result.Carrier, "province": result.Province,
			"address_family": result.AddressFamily, "probe_method": result.ProbeMethod, "degraded": result.Degraded,
			"sent": result.Sent, "received": result.Received, "loss_percent": result.LossPercent,
			"response_type": result.ResponseType, "selected": result.Selected,
		}
		if result.RTTMinMS != nil {
			encoded["rtt_min_ms"], encoded["rtt_avg_ms"], encoded["rtt_max_ms"] = *result.RTTMinMS, *result.RTTAvgMS, *result.RTTMaxMS
		}
		if result.ErrorCode != "" {
			encoded["error_code"], encoded["error_message"] = result.ErrorCode, result.ErrorMessage
			encoded["fallback_chain"] = result.FallbackChain
			failed++
		}
		items = append(items, encoded)
	}
	status := "available"
	if failed == len(results) {
		status = "unavailable"
	} else if failed > 0 {
		status = "limited"
	}
	return shared.ServerTestCategoryResult{
		Category: category, Status: status,
		Summary: map[string]any{"targets": len(results), "failed_targets": failed},
		Items:   items,
	}
}

func runTCPTarget(parent context.Context, target shared.ServerTestTarget, probeCount int) tcpTargetResult {
	result := tcpTargetResult{
		ID: target.ID, Label: target.Label, Carrier: target.Carrier, Province: target.Province,
		AddressFamily: string(target.AddressFamily), ProbeMethod: "tcp_connect", Degraded: true,
		Selected: "primary",
	}
	ctx, cancel := context.WithTimeout(parent, targetTimeout)
	defer cancel()
	selected := target
	address, err := resolvePublicTarget(ctx, target)
	if err == nil {
		err = preflightConnect(ctx, address, target.Port)
	}
	if err != nil {
		result.FallbackChain = append(result.FallbackChain, "primary: "+err.Error())
		if target.Backup == nil {
			result.ErrorCode, result.ErrorMessage = "target_unreachable", err.Error()
			result.LossPercent = 100
			return result
		}
		selected = *target.Backup
		result.Selected = "backup"
		address, err = resolvePublicTarget(ctx, selected)
		if err == nil {
			err = preflightConnect(ctx, address, selected.Port)
		}
		if err != nil {
			result.FallbackChain = append(result.FallbackChain, "backup: "+err.Error())
			result.ErrorCode, result.ErrorMessage = "target_unreachable", err.Error()
			result.LossPercent = 100
			return result
		}
		result.Label = selected.Label
	}
	raw, rawErr := newRawProber(address, selected.Port)
	if rawErr == nil {
		defer raw.Close()
		result.ProbeMethod = "raw_syn"
		result.Degraded = false
		var samples []float64
		responses := make(map[string]int)
		for i := 0; i < probeCount; i++ {
			if ctx.Err() != nil {
				break
			}
			result.Sent++
			response, rtt, err := raw.Probe(0, time.Second)
			if err != nil {
				result.ErrorCode, result.ErrorMessage = "raw_probe_failed", err.Error()
				break
			}
			if response != "" {
				responses[response]++
				samples = append(samples, float64(rtt.Microseconds())/1000)
			}
		}
		applyProbeSamples(&result, result.Sent, samples)
		if responses["syn_ack"] > 0 && responses["rst"] > 0 {
			result.ResponseType = "syn_ack,rst"
		} else if responses["syn_ack"] > 0 {
			result.ResponseType = "syn_ack"
		} else if responses["rst"] > 0 {
			result.ResponseType = "rst"
		}
		return result
	}
	if !errors.Is(rawErr, errRawPermission) {
		result.ErrorCode, result.ErrorMessage = "raw_probe_failed", rawErr.Error()
		result.LossPercent = 100
		return result
	}
	result.ProbeMethod = "tcp_connect"
	result.Degraded = true
	var samples []float64
	for i := 0; i < probeCount; i++ {
		if ctx.Err() != nil {
			break
		}
		result.Sent++
		started := time.Now()
		conn, err := (&net.Dialer{Timeout: connectTimeout}).DialContext(ctx, networkForFamily(selected.AddressFamily), net.JoinHostPort(address.String(), fmt.Sprint(selected.Port)))
		if err != nil {
			continue
		}
		_ = conn.Close()
		samples = append(samples, float64(time.Since(started).Microseconds())/1000)
	}
	applyProbeSamples(&result, result.Sent, samples)
	result.ResponseType = "connected"
	return result
}

func applyProbeSamples(result *tcpTargetResult, probeCount int, samples []float64) {
	result.Received = len(samples)
	if probeCount <= 0 {
		if result.ErrorCode == "" {
			result.ErrorCode = "probe_not_run"
			result.ErrorMessage = "the measurement window ended before a probe could be sent"
		}
		return
	}
	result.LossPercent = float64(probeCount-len(samples)) * 100 / float64(probeCount)
	if len(samples) == 0 && result.ErrorCode == "" {
		result.ErrorCode = "probe_no_response"
		result.ErrorMessage = "the target returned no responses during the measurement window"
	}
	if len(samples) > 0 {
		sort.Float64s(samples)
		minimum, maximum := samples[0], samples[len(samples)-1]
		total := 0.0
		for _, sample := range samples {
			total += sample
		}
		average := total / float64(len(samples))
		result.RTTMinMS, result.RTTAvgMS, result.RTTMaxMS = &minimum, &average, &maximum
	}
}

func interruptedTCPTarget(target shared.ServerTestTarget, cause error) tcpTargetResult {
	message := "the target was not executed before the task stopped"
	if cause != nil {
		message += ": " + cause.Error()
	}
	return tcpTargetResult{
		ID: target.ID, Label: target.Label, Carrier: target.Carrier, Province: target.Province,
		AddressFamily: string(target.AddressFamily), ProbeMethod: "not_run", Degraded: true,
		Selected: "primary", ErrorCode: "task_interrupted", ErrorMessage: message,
	}
}

func preflightConnect(ctx context.Context, address netip.Addr, port int) error {
	conn, err := (&net.Dialer{Timeout: connectTimeout}).DialContext(ctx, networkForAddr(address), net.JoinHostPort(address.String(), fmt.Sprint(port)))
	if err != nil {
		return err
	}
	return conn.Close()
}

func resolvePublicTarget(ctx context.Context, target shared.ServerTestTarget) (netip.Addr, error) {
	network := networkForFamily(target.AddressFamily)
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, network, target.Host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("resolve %s: %w", target.Host, err)
	}
	for _, address := range addresses {
		address = address.Unmap()
		if publicAddress(address) {
			return address, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("target_policy_rejected: %s has no public address", target.Host)
}

func publicAddress(address netip.Addr) bool {
	return address.IsValid() && address.IsGlobalUnicast() && !address.IsPrivate() &&
		!address.IsLoopback() && !address.IsLinkLocalUnicast() && !address.IsMulticast() && !address.IsUnspecified()
}

func networkForFamily(family shared.ServerTestAddressFamily) string {
	if family == shared.ServerTestIPv6 {
		return "tcp6"
	}
	return "tcp4"
}

func networkForAddr(address netip.Addr) string {
	if address.Is6() {
		return "tcp6"
	}
	return "tcp4"
}
