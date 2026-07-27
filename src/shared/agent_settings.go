package shared

import (
	"errors"
	"fmt"
)

const (
	AgentSettingsSchemaVersion = 1

	ReconnectModeInfinite = "infinite"
	ReconnectModeLimited  = "limited"

	DefaultReconnectMaxRetries = 10
	DefaultTelemetrySeconds    = 60
	DefaultDriftSeconds        = 15
)

// PanelMetadata is informational. The Agent must not use PublicURL or WSURL to
// replace the connection address supplied by the install command.
type PanelMetadata struct {
	InstanceID string `json:"instance_id"`
	Version    string `json:"version"`
	PublicURL  string `json:"public_url"`
	WSURL      string `json:"ws_url"`
}

type AgentReconnectSettings struct {
	Mode       string `json:"mode"`
	MaxRetries int    `json:"max_retries"`
}

type AgentIntervalSettings struct {
	IntervalSeconds int `json:"interval_seconds"`
}

// AgentSettings is the complete panel-managed Agent configuration. Both panel
// and Agent use this type directly so there is no DTO conversion layer.
type AgentSettings struct {
	Revision       int64                  `json:"revision"`
	Reconnect      AgentReconnectSettings `json:"reconnect"`
	Telemetry      AgentIntervalSettings  `json:"telemetry"`
	DriftDetection AgentIntervalSettings  `json:"drift_detection"`
}

// AgentSettingsDocument is persisted verbatim by the Agent. Panel metadata is
// refreshed on every sync and is intentionally outside the revisioned object.
type AgentSettingsDocument struct {
	SchemaVersion int           `json:"schema_version"`
	Panel         PanelMetadata `json:"panel"`
	Agent         AgentSettings `json:"agent"`
}

func DefaultAgentSettings() AgentSettings {
	return AgentSettings{
		Revision: 1,
		Reconnect: AgentReconnectSettings{
			Mode:       ReconnectModeInfinite,
			MaxRetries: DefaultReconnectMaxRetries,
		},
		Telemetry:      AgentIntervalSettings{IntervalSeconds: DefaultTelemetrySeconds},
		DriftDetection: AgentIntervalSettings{IntervalSeconds: DefaultDriftSeconds},
	}
}

func (s AgentSettings) Validate() error {
	if s.Revision < 1 {
		return errors.New("agent.revision must be at least 1")
	}
	if s.Reconnect.Mode != ReconnectModeInfinite && s.Reconnect.Mode != ReconnectModeLimited {
		return fmt.Errorf("agent.reconnect.mode must be %q or %q", ReconnectModeInfinite, ReconnectModeLimited)
	}
	if s.Reconnect.MaxRetries < 1 || s.Reconnect.MaxRetries > 100 {
		return errors.New("agent.reconnect.max_retries must be between 1 and 100")
	}
	if v := s.Telemetry.IntervalSeconds; v < 10 || v > 3600 {
		return errors.New("agent.telemetry.interval_seconds must be between 10 and 3600")
	}
	if v := s.DriftDetection.IntervalSeconds; v < 5 || v > 3600 {
		return errors.New("agent.drift_detection.interval_seconds must be between 5 and 3600")
	}
	return nil
}

func (d AgentSettingsDocument) Validate() error {
	if d.SchemaVersion != AgentSettingsSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", d.SchemaVersion)
	}
	if d.Panel.InstanceID == "" {
		return errors.New("panel.instance_id is required")
	}
	return d.Agent.Validate()
}

// AgentSettingsSyncPayload reports what the Agent has successfully applied.
// LastApplyError is safe diagnostic text and must not contain credentials.
type AgentSettingsSyncPayload struct {
	PanelInstanceID string `json:"panel_instance_id"`
	AppliedRevision int64  `json:"applied_revision"`
	LastApplyError  string `json:"last_apply_error,omitempty"`
}

type AgentSettingsSyncResult struct {
	Changed  bool                   `json:"changed"`
	Settings *AgentSettingsDocument `json:"settings,omitempty"`
}

type AgentSettingsChangedPayload struct {
	Revision int64 `json:"revision"`
}
