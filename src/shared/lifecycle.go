package shared

import "time"

const (
	PanelStateStartup  = "startup"
	PanelStateActive   = "active"
	PanelStateUpdating = "updating"
	PanelStateFaulted  = "faulted"

	ConnectionStateNeverConnected = "never_connected"
	ConnectionStateConnecting     = "connecting"
	ConnectionStateReconnecting   = "reconnecting"
	ConnectionStateOnline         = "online"
	ConnectionStateOffline        = "offline"
	ConnectionStateAuthRejected   = "auth_rejected"

	SessionKindInitial   = "initial"
	SessionKindReconnect = "reconnect"
)

type RetryPolicy struct {
	MinMS int `json:"min_ms"`
	MaxMS int `json:"max_ms"`
}

type LifecycleVersion struct {
	Epoch    string `json:"epoch"`
	Revision uint64 `json:"revision"`
}

type PanelLifecycleSnapshot struct {
	PanelInstanceID       string      `json:"panel_instance_id"`
	State                 string      `json:"state"`
	Epoch                 string      `json:"epoch"`
	Revision              uint64      `json:"revision"`
	EnteredAt             time.Time   `json:"entered_at"`
	Fault                 string      `json:"fault,omitempty"`
	RetryPolicy           RetryPolicy `json:"retry_policy"`
	LatencyResumeWindowMS int         `json:"latency_resume_window_ms"`
}

func (s PanelLifecycleSnapshot) Version() LifecycleVersion {
	return LifecycleVersion{Epoch: s.Epoch, Revision: s.Revision}
}

func ValidPanelState(value string) bool {
	switch value {
	case PanelStateStartup, PanelStateActive, PanelStateUpdating, PanelStateFaulted:
		return true
	default:
		return false
	}
}

type SessionOpenPayload struct {
	ProtocolVersion int               `json:"protocol_version"`
	AgentVersion    string            `json:"agent_version"`
	XrayVersion     string            `json:"xray_version"`
	XrayRunning     bool              `json:"xray_running"`
	NICAddresses    []string          `json:"nic_addresses,omitempty"`
	LastLifecycle   *LifecycleVersion `json:"last_lifecycle,omitempty"`
}

type SessionOpenResult struct {
	ServerID             int64                  `json:"server_id"`
	SessionID            string                 `json:"session_id"`
	SessionKind          string                 `json:"session_kind"`
	IssuedToken          string                 `json:"issued_token,omitempty"`
	CredentialExchangeID string                 `json:"credential_exchange_id,omitempty"`
	PanelState           PanelLifecycleSnapshot `json:"panel_state"`
}

type SessionReadyPayload struct {
	SessionID string           `json:"session_id"`
	Lifecycle LifecycleVersion `json:"lifecycle"`
}

type CredentialCommitPayload struct {
	ExchangeID string `json:"exchange_id"`
}

type LifecycleChangedPayload struct {
	PanelState PanelLifecycleSnapshot `json:"panel_state"`
}
