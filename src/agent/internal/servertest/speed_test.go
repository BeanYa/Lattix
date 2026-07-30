package servertest

import (
	"io"
	"testing"
	"time"

	"lattix/shared"
)

func TestRunSpeedTargetRejectsUnauthorizedTOS(t *testing.T) {
	result := runSpeedTarget(t.Context(), shared.ServerTestTarget{
		ID: "speed:beijing-telecom", Label: "北京电信", Host: "tos-cn-beijing.volces.com",
		AddressFamily: shared.ServerTestIPv4,
	})
	if result.Status != "provider_access_unavailable" || result.ErrorCode != "provider_access_unavailable" {
		t.Fatalf("unexpected TOS result: %#v", result)
	}
}

func TestSpeedHelpers(t *testing.T) {
	if !validSpeedPath("/api/v1/gm/large") || validSpeedPath("api") || validSpeedPath("/api?token=x") {
		t.Fatal("speed path policy mismatch")
	}
	if got := speedMbps(1_000_000, time.Second); got != 8 {
		t.Fatalf("speed = %v Mbps", got)
	}
	reader := &countingReader{remaining: 10}
	data, err := io.ReadAll(reader)
	if err != nil || len(data) != 10 || reader.count.Load() != 10 {
		t.Fatalf("counting reader: bytes=%d count=%d err=%v", len(data), reader.count.Load(), err)
	}
}
