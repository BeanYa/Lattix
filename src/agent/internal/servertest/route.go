package servertest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"lattix/shared"
)

const (
	// routeTimeout bounds the whole route category; with targetConcurrency
	// workers and a per-target budget of routeTargetTimeout, all targets of a
	// full province x carrier catalog still fit well within it.
	routeTimeout       = 1200 * time.Second
	routeTargetTimeout = 120 * time.Second
	routeMaxHops       = 30
	routeProbesPerHop  = 3
	routeProbeTimeout  = 2 * time.Second
	routeMaxSilentHops = 5
)

var (
	errRouteSilentLimit   = fmt.Errorf("stopped after %d consecutive silent hops", routeMaxSilentHops)
	errRouteProbeDeadline = errors.New("route probe deadline exceeded")
)

// traceRoute is the platform traceroute probe seam; tests replace it.
var traceRoute = traceTCPErrorQueue

type routeTargetResult struct {
	ID            string           `json:"id"`
	Label         string           `json:"label"`
	Carrier       string           `json:"carrier,omitempty"`
	Province      string           `json:"province,omitempty"`
	AddressFamily string           `json:"address_family"`
	ProbeMethod   string           `json:"probe_method"`
	Degraded      bool             `json:"degraded"`
	Reached       bool             `json:"reached"`
	Hops          []map[string]any `json:"hops"`
	ErrorCode     string           `json:"error_code,omitempty"`
	ErrorMessage  string           `json:"error_message,omitempty"`
}

func (r *Runner) runRoute(parent context.Context, category shared.ServerTestCategory, targets []shared.ServerTestTarget, update func(int, int, string)) shared.ServerTestCategoryResult {
	if len(targets) == 0 {
		return unsupportedResult(category, "catalog_targets_unavailable", "no route targets were supplied for this category")
	}
	ctx, cancel := context.WithTimeout(parent, routeTimeout)
	defer cancel()
	results := make([]routeTargetResult, len(targets))
	jobs := make(chan int)
	workers := targetConcurrency
	if len(targets) < workers {
		workers = len(targets)
	}
	var wg sync.WaitGroup
	var completedMu sync.Mutex
	completed := 0
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results[index] = runRouteTarget(ctx, targets[index])
				completedMu.Lock()
				completed++
				current := completed
				completedMu.Unlock()
				update(current, len(targets), targets[index].Label)
			}
		}()
	}
sendRouteJobs:
	for index := range targets {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendRouteJobs
		}
	}
	close(jobs)
	wg.Wait()
	for index := range results {
		if results[index].ID == "" {
			message := "the target was not executed before the task stopped"
			if ctx.Err() != nil {
				message += ": " + ctx.Err().Error()
			}
			results[index] = routeTargetResult{
				ID: targets[index].ID, Label: targets[index].Label, Carrier: targets[index].Carrier,
				Province: targets[index].Province, AddressFamily: string(targets[index].AddressFamily),
				ProbeMethod: "not_run", Degraded: true, ErrorCode: "task_interrupted", ErrorMessage: message,
			}
		}
	}
	items := make([]map[string]any, 0, len(results))
	failed := 0
	for _, result := range results {
		item := map[string]any{
			"id": result.ID, "label": result.Label, "carrier": result.Carrier,
			"province": result.Province, "address_family": result.AddressFamily,
			"probe_method": result.ProbeMethod, "degraded": result.Degraded,
			"reached": result.Reached, "hops": result.Hops,
		}
		if result.ErrorCode != "" {
			item["error_code"], item["error_message"] = result.ErrorCode, result.ErrorMessage
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
			"probe_method": "tcp_error_queue", "degraded": true,
			"asn_enrichment": "unavailable", "classifier_source": "lattix-bundled-routes-v1",
		},
		Items: items,
	}
}

func runRouteTarget(parent context.Context, target shared.ServerTestTarget) routeTargetResult {
	result := routeTargetResult{
		ID: target.ID, Label: target.Label, Carrier: target.Carrier, Province: target.Province,
		AddressFamily: string(target.AddressFamily), ProbeMethod: "tcp_error_queue", Degraded: true,
	}
	address, err := resolvePublicTarget(parent, target)
	if err != nil {
		result.ErrorCode, result.ErrorMessage = "target_policy_rejected", err.Error()
		return result
	}
	port := uint16(target.Port)
	if port == 0 {
		port = 443
	}
	ctx, cancel := context.WithTimeout(parent, routeTargetTimeout)
	defer cancel()
	hops, reached, err := traceRoute(ctx, address, port, routeMaxHops, routeProbesPerHop, routeProbeTimeout, routeMaxSilentHops)
	result.Hops, result.Reached = hops, reached
	switch {
	case errors.Is(err, errRouteSilentLimit), errors.Is(err, errRouteProbeDeadline):
		result.ErrorCode, result.ErrorMessage = "route_incomplete", err.Error()
	case err != nil:
		result.ErrorCode, result.ErrorMessage = "route_probe_failed", err.Error()
	case !reached:
		result.ErrorCode, result.ErrorMessage = "route_incomplete", "destination was not reached within 30 hops"
	}
	return result
}
