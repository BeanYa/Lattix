package servertest

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"time"

	"lattix/shared"
)

var largePacketSizes = func() []int {
	large := []int{900, 950, 1000, 1050, 1100, 1150, 1200}
	small := []int{120, 240, 480}
	sizes := make([]int, 0, 30)
	for len(sizes) < 23 {
		sizes = append(sizes, large[len(sizes)%len(large)])
	}
	for index := 0; index < 7; index++ {
		sizes = append(sizes, small[index%len(small)])
	}
	return sizes
}()

func (r *Runner) runLargePacket(ctx context.Context, category shared.ServerTestCategory, targets []shared.ServerTestTarget, update func(int, int, string)) shared.ServerTestCategoryResult {
	if len(targets) == 0 {
		return unsupportedResult(category, "catalog_targets_unavailable", "no IPv4 large-packet targets were supplied")
	}
	if err := rawSocketCapability(); err != nil {
		if errors.Is(err, errRawPermission) {
			return unsupportedResult(category, "unsupported_without_raw_socket", "CAP_NET_RAW or root is required for large SYN testing")
		}
		return unsupportedResult(category, "raw_probe_failed", err.Error())
	}
	precheckTarget := shared.ServerTestTarget{
		ID: "large-packet-precheck", Label: "Cloudflare", Category: category,
		AddressFamily: shared.ServerTestIPv4, Host: "www.cloudflare.com", Port: 443, Source: "cloudflare",
	}
	address, err := resolvePublicTarget(ctx, precheckTarget)
	if err != nil {
		return unsupportedResult(category, "large_syn_environment_filtered", err.Error())
	}
	precheck, err := newRawProber(address, precheckTarget.Port)
	if err != nil {
		return unsupportedResult(category, "large_syn_environment_filtered", err.Error())
	}
	precheckReceived := 0
	for index := 0; index < 20; index++ {
		response, _, probeErr := precheck.Probe(1200, time.Second)
		if probeErr != nil {
			_ = precheck.Close()
			return unsupportedResult(category, "large_syn_environment_filtered", probeErr.Error())
		}
		if response != "" {
			precheckReceived++
		}
	}
	_ = precheck.Close()
	if precheckReceived <= 4 {
		return unsupportedResult(category, "large_syn_environment_filtered", "Cloudflare 1200-byte SYN precheck loss was at least 80%")
	}

	results := make([]tcpTargetResult, len(targets))
	jobs := make(chan int)
	workers := targetConcurrency
	if len(targets) < workers {
		workers = len(targets)
	}
	var wg sync.WaitGroup
	completed := 0
	var completedMu sync.Mutex
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results[index] = runLargePacketTarget(ctx, targets[index])
				completedMu.Lock()
				completed++
				current := completed
				completedMu.Unlock()
				update(current, len(targets), targets[index].Label)
			}
		}()
	}
sendLargeJobs:
	for index := range targets {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendLargeJobs
		}
	}
	close(jobs)
	wg.Wait()
	items := make([]map[string]any, 0, len(results))
	failed := 0
	for _, result := range results {
		item := map[string]any{
			"id": result.ID, "label": result.Label, "carrier": result.Carrier, "province": result.Province,
			"address_family": result.AddressFamily, "probe_method": result.ProbeMethod,
			"sent": result.Sent, "received": result.Received, "loss_percent": result.LossPercent,
			"response_type": result.ResponseType, "selected": result.Selected,
		}
		if result.RTTMinMS != nil {
			item["rtt_min_ms"], item["rtt_avg_ms"], item["rtt_max_ms"] = *result.RTTMinMS, *result.RTTAvgMS, *result.RTTMaxMS
		}
		if result.ErrorCode != "" {
			item["error_code"], item["error_message"] = result.ErrorCode, result.ErrorMessage
			item["fallback_chain"] = result.FallbackChain
			failed++
		}
		items = append(items, item)
	}
	status := "available"
	if failed == len(results) {
		status = "unavailable"
	} else if failed > 0 {
		status = "limited"
	}
	return shared.ServerTestCategoryResult{
		Category: category, Status: status,
		Summary: map[string]any{
			"targets": len(results), "failed_targets": failed,
			"precheck_sent": 20, "precheck_received": precheckReceived,
			"large_probes_per_target": 23, "small_probes_per_target": 7,
		},
		Items: items,
	}
}

func runLargePacketTarget(parent context.Context, target shared.ServerTestTarget) tcpTargetResult {
	result := tcpTargetResult{
		ID: target.ID, Label: target.Label, Carrier: target.Carrier, Province: target.Province,
		AddressFamily: string(target.AddressFamily), ProbeMethod: "raw_syn", Sent: len(largePacketSizes), Selected: "primary",
	}
	ctx, cancel := context.WithTimeout(parent, targetTimeout)
	defer cancel()
	selected, address, chain, err := selectReachableTarget(ctx, target)
	result.FallbackChain = chain
	if err != nil {
		result.ErrorCode, result.ErrorMessage, result.LossPercent = "target_unreachable", err.Error(), 100
		return result
	}
	if selected.ID != target.ID {
		result.Selected, result.Label = "backup", selected.Label
	}
	prober, err := newRawProber(address, selected.Port)
	if err != nil {
		result.ErrorCode, result.ErrorMessage, result.LossPercent = "raw_probe_failed", err.Error(), 100
		return result
	}
	defer prober.Close()
	var samples []float64
	responses := make(map[string]int)
	for _, size := range largePacketSizes {
		response, rtt, probeErr := prober.Probe(size, time.Second)
		if probeErr != nil {
			result.ErrorCode, result.ErrorMessage = "raw_probe_failed", probeErr.Error()
			break
		}
		if response != "" {
			responses[response]++
			samples = append(samples, float64(rtt.Microseconds())/1000)
		}
	}
	applyProbeSamples(&result, len(largePacketSizes), samples)
	if responses["syn_ack"] > 0 {
		result.ResponseType = "syn_ack"
	} else if responses["rst"] > 0 {
		result.ResponseType = "rst"
	}
	return result
}

func selectReachableTarget(ctx context.Context, target shared.ServerTestTarget) (shared.ServerTestTarget, netip.Addr, []string, error) {
	address, err := resolvePublicTarget(ctx, target)
	if err == nil {
		err = preflightConnect(ctx, address, target.Port)
	}
	if err == nil {
		return target, address, nil, nil
	}
	chain := []string{"primary: " + err.Error()}
	if target.Backup == nil {
		return target, netip.Addr{}, chain, err
	}
	backup := *target.Backup
	address, backupErr := resolvePublicTarget(ctx, backup)
	if backupErr == nil {
		backupErr = preflightConnect(ctx, address, backup.Port)
	}
	if backupErr != nil {
		chain = append(chain, "backup: "+backupErr.Error())
		return backup, netip.Addr{}, chain, backupErr
	}
	return backup, address, chain, nil
}
