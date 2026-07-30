package servertest

import "testing"

func TestApplyProbeSamplesMarksZeroResponses(t *testing.T) {
	result := tcpTargetResult{Sent: 30}
	applyProbeSamples(&result, 30, nil)
	if result.Received != 0 || result.LossPercent != 100 || result.ErrorCode != "probe_no_response" {
		t.Fatalf("unexpected zero-response result: %+v", result)
	}
}

func TestApplyProbeSamplesPreservesExistingProbeError(t *testing.T) {
	result := tcpTargetResult{Sent: 30, ErrorCode: "raw_probe_failed", ErrorMessage: "socket closed"}
	applyProbeSamples(&result, 30, nil)
	if result.ErrorCode != "raw_probe_failed" || result.ErrorMessage != "socket closed" {
		t.Fatalf("probe error was replaced: %+v", result)
	}
}

func TestApplyProbeSamplesMarksNoProbeWindow(t *testing.T) {
	result := tcpTargetResult{}
	applyProbeSamples(&result, 0, nil)
	if result.ErrorCode != "probe_not_run" || result.LossPercent != 0 {
		t.Fatalf("unexpected no-probe result: %+v", result)
	}
}
