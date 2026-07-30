package servertest

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	"lattix/shared"
)

const taskTimeout = 60 * time.Minute

type ProgressFunc func(shared.ServerTestProgressPayload)

type Runner struct {
	AgentVersion string
	Now          func() time.Time
}

func NewRunner(agentVersion string) *Runner {
	return &Runner{AgentVersion: agentVersion, Now: time.Now}
}

func (r *Runner) Run(parent context.Context, payload shared.ServerTestRunPayload, progress ProgressFunc, sandboxState, sandboxReason string) shared.ServerTestReport {
	started := r.Now().UTC()
	report := shared.ServerTestReport{
		SchemaVersion: shared.ServerTestSchemaVersion,
		TaskID:        payload.TaskID, Generation: payload.Generation,
		Status:       shared.ServerTestSucceeded,
		StartedAt:    started.Format(time.RFC3339Nano),
		AgentVersion: r.AgentVersion, CatalogVersion: payload.Catalog.Version,
		Environment: inspectEnvironment(sandboxState, sandboxReason),
	}
	ctx, cancel := context.WithTimeout(parent, taskTimeout)
	defer cancel()

	totalByCategory := make(map[shared.ServerTestCategory]int)
	for _, target := range payload.Catalog.Targets {
		totalByCategory[target.Category]++
	}
	states := make(map[shared.ServerTestCategory]shared.ServerTestCategoryProgress)
	for _, category := range payload.Categories {
		total := totalByCategory[category]
		if total == 0 {
			total = 1
		}
		states[category] = shared.ServerTestCategoryProgress{Category: category, Status: "pending", Total: total}
	}
	sequence := uint64(0)
	emit := func(phase string, current shared.ServerTestCategory, completed, total int, message string) {
		if progress == nil {
			return
		}
		sequence++
		items := make([]shared.ServerTestCategoryProgress, 0, len(payload.Categories))
		allCompleted, allTotal := 0, 0
		for _, category := range payload.Categories {
			state := states[category]
			if category == current {
				state.Status = "running"
				state.Completed = completed
				state.Total = total
				state.Message = message
				states[category] = state
			}
			items = append(items, state)
			allCompleted += state.Completed
			allTotal += state.Total
		}
		progress(shared.ServerTestProgressPayload{
			SchemaVersion: shared.ServerTestSchemaVersion,
			TaskID:        payload.TaskID, Generation: payload.Generation, Sequence: sequence,
			Status: shared.ServerTestRunning, Phase: phase,
			Completed: allCompleted, Total: allTotal, Message: message, Categories: items,
		})
	}
	finish := func(category shared.ServerTestCategory, result shared.ServerTestCategoryResult) {
		state := states[category]
		state.Completed = state.Total
		state.Status = result.Status
		state.Message = result.ErrorMessage
		states[category] = state
		report.Categories = append(report.Categories, result)
		if result.ErrorCode != "" || result.Status == "limited" || result.Status == "unavailable" || result.Status == "failed" {
			if report.Status == shared.ServerTestSucceeded {
				report.Status = shared.ServerTestCompletedWithErrors
			}
		}
	}

	phases := []struct {
		name       string
		categories []shared.ServerTestCategory
		run        func(context.Context, shared.ServerTestCategory, []shared.ServerTestTarget, func(int, int, string)) shared.ServerTestCategoryResult
	}{
		{name: "ip_quality", categories: []shared.ServerTestCategory{shared.ServerTestIPQuality}, run: r.runIPQuality},
		{name: "tcp", categories: []shared.ServerTestCategory{shared.ServerTestTCPIPv4, shared.ServerTestTCPIPv6, shared.ServerTestCERNETIPv4, shared.ServerTestCERNET2IPv6}, run: r.runTCP},
		{name: "large_packet", categories: []shared.ServerTestCategory{shared.ServerTestLargePacketIPv4}, run: r.runLargePacket},
		{name: "routes", categories: []shared.ServerTestCategory{shared.ServerTestReturnRouteIPv4, shared.ServerTestReturnRouteIPv6}, run: r.runRoute},
		{name: "international", categories: []shared.ServerTestCategory{shared.ServerTestInternational}, run: r.runTCP},
		{name: "speed", categories: []shared.ServerTestCategory{shared.ServerTestSpeed}, run: r.runSpeed},
	}
	selected := make(map[shared.ServerTestCategory]bool, len(payload.Categories))
	for _, category := range payload.Categories {
		selected[category] = true
	}
	for _, phase := range phases {
		for _, category := range phase.categories {
			if !selected[category] {
				continue
			}
			targets := filterTargets(payload.Catalog.Targets, category)
			total := len(targets)
			if total == 0 {
				total = 1
			}
			emit(phase.name, category, 0, total, "")
			result := phase.run(ctx, category, targets, func(done, targetTotal int, message string) {
				emit(phase.name, category, done, targetTotal, message)
			})
			finish(category, result)
			if ctx.Err() != nil {
				report.Status = shared.ServerTestFailed
				report.ErrorCode = "task_timeout"
				report.ErrorMessage = ctx.Err().Error()
				break
			}
		}
		if report.Status == shared.ServerTestFailed {
			break
		}
	}
	report.CompletedAt = r.Now().UTC().Format(time.RFC3339Nano)
	return report
}

func inspectEnvironment(sandboxState, sandboxReason string) shared.ServerTestEnvironment {
	privileges := "unprivileged"
	if os.Geteuid() == 0 {
		privileges = "root"
	}
	probeMethod := "raw_syn"
	degraded, degradedReason := false, ""
	if err := rawSocketCapability(); err != nil {
		probeMethod, degraded, degradedReason = "tcp_connect", true, err.Error()
	}
	return shared.ServerTestEnvironment{
		ProbeMethod: probeMethod, Degraded: degraded, DegradedReason: degradedReason,
		Sandbox: sandboxState, SandboxReason: sandboxReason, Privileges: privileges,
	}
}

func filterTargets(targets []shared.ServerTestTarget, category shared.ServerTestCategory) []shared.ServerTestTarget {
	filtered := make([]shared.ServerTestTarget, 0)
	for _, target := range targets {
		if target.Category == category {
			filtered = append(filtered, target)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })
	return filtered
}

func unsupportedResult(category shared.ServerTestCategory, code, message string) shared.ServerTestCategoryResult {
	return shared.ServerTestCategoryResult{Category: category, Status: "unavailable", ErrorCode: code, ErrorMessage: message}
}

func runtimeSummary() map[string]any {
	return map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "runtime": fmt.Sprintf("go%s", runtime.Version())}
}
