package shared

import "testing"

func TestDefaultAgentSettingsValidate(t *testing.T) {
	settings := DefaultAgentSettings()
	if err := settings.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	if settings.Reconnect.Mode != ReconnectModeInfinite {
		t.Fatalf("default reconnect mode = %q", settings.Reconnect.Mode)
	}
}

func TestAgentSettingsBounds(t *testing.T) {
	settings := DefaultAgentSettings()
	settings.Reconnect.MaxRetries = 101
	if err := settings.Validate(); err == nil {
		t.Fatal("expected max retries validation error")
	}
	settings = DefaultAgentSettings()
	settings.Telemetry.IntervalSeconds = 9
	if err := settings.Validate(); err == nil {
		t.Fatal("expected telemetry validation error")
	}
}
