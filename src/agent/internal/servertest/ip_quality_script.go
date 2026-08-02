package servertest

import (
	"context"
	"errors"
	"time"

	"lattix/agent/internal/servertest/ipquality"
	"lattix/shared"
)

const ipQualityScriptTimeout = 15 * time.Minute

// runIPQualityScript executes the upstream xykt/IPQuality script in privacy
// JSON mode and maps the parsed report into the Lattix category result.
func (r *Runner) runIPQualityScript(parent context.Context, category shared.ServerTestCategory, _ []shared.ServerTestTarget, update func(int, int, string)) shared.ServerTestCategoryResult {
	ctx, cancel := context.WithTimeout(parent, ipQualityScriptTimeout)
	defer cancel()
	runner := ipquality.Runner{DataDir: r.DataDir, Timeout: ipQualityScriptTimeout}
	result, err := runner.Run(ctx, func(message string) { update(0, 1, message) })
	if err != nil {
		status := "failed"
		code := "ipquality_script_failed"
		if errors.Is(err, ipquality.ErrNoFamily) {
			status, code = "unavailable", "no_public_address"
		}
		update(1, 1, status)
		return shared.ServerTestCategoryResult{
			Category: category, Status: status, ErrorCode: code, ErrorMessage: err.Error(),
		}
	}
	update(1, 1, "完成")
	status := "available"
	if len(result.Output) == 0 {
		status = "unavailable"
	}
	families, parseErr := ipquality.ParseScriptOutput(result.Output)
	if parseErr != nil {
		update(1, 1, "解析失败")
		return shared.ServerTestCategoryResult{
			Category: category, Status: "failed",
			ErrorCode: "ipquality_parse_failed", ErrorMessage: parseErr.Error(),
			Summary: map[string]any{
				"script_version": result.ScriptVersion,
				"script_stale":   result.ScriptStale,
			},
		}
	}
	return shared.ServerTestCategoryResult{
		Category: category, Status: status,
		Summary: map[string]any{
			"script_version": result.ScriptVersion,
			"script_stale":   result.ScriptStale,
			"families":       len(families),
		},
		IPQuality: &shared.IPQualityResult{
			SchemaVersion: shared.ServerTestSchemaVersion,
			ScriptVersion: result.ScriptVersion,
			ScriptStale:   result.ScriptStale,
			Families:      families,
		},
	}
}
