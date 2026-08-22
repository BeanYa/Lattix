// Package state 管理 agent 本地状态：session.open 换发的长期凭证（设计文档 §11）
// 与链跳配置件记录（§21.1，重启重建 config.json 与重发幂等的依据）。
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lattix/agent/internal/fileutil"
	"lattix/shared"
)

// ConnectionState 取值（ConnectionStatus.State）：agent 进程自身的连接循环状态机
// （与面板侧服务器级连接观测正交，§panel-lifecycle 设计 §2）。
// 转换表：
//
//	connecting → online | backoff | auth_rejected
//	online     → connecting | backoff | auth_rejected（connecting = 断开后立即重拨）
//	backoff    → connecting | auth_rejected
//	auth_rejected →（进程内终态）
//
// auth_rejected 后进程存活等待 SIGTERM（systemd/watchdog 不再循环重试）；
// 管理员运行新安装命令并重启 Agent 后，状态机随进程重置回到 connecting。
const (
	ConnStateConnecting   = "connecting"
	ConnStateOnline       = "online"
	ConnStateBackoff      = "backoff"
	ConnStateAuthRejected = "auth_rejected"
)

var connectionTransitions = map[string]map[string]bool{
	ConnStateConnecting: {
		ConnStateOnline:       true,
		ConnStateBackoff:      true,
		ConnStateAuthRejected: true,
	},
	ConnStateOnline: {
		ConnStateConnecting:   true, // 断开后立即重拨（无退避路径）
		ConnStateBackoff:      true,
		ConnStateAuthRejected: true,
	},
	ConnStateBackoff: {
		ConnStateConnecting:   true,
		ConnStateAuthRejected: true,
	},
	ConnStateAuthRejected: {},
}

// ValidConnectionTransition 校验连接状态转换是否合法（同状态幂等）。
func ValidConnectionTransition(from, to string) bool {
	if from == to {
		return true
	}
	targets, ok := connectionTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

// ConnectionStatus is a credential-free runtime snapshot consumed by latx-ag.
type ConnectionStatus struct {
	Connected    bool      `json:"connected"`
	State        string    `json:"state,omitempty"` // connecting|online|backoff|auth_rejected
	Panel        string    `json:"panel"`
	ServerID     int64     `json:"server_id,omitempty"`
	AgentVersion string    `json:"agent_version"`
	PID          int       `json:"pid"`
	ChangedAt    time.Time `json:"changed_at"`
	LastError    string    `json:"last_error,omitempty"`
}

// State 是 agent 落盘的本地状态。mu 保护跨 goroutine 的读写（WS 读循环与
// 离线命令队列执行器并发变更 state），不参与 JSON 序列化（评审 P2-2）。
type State struct {
	mu sync.Mutex `json:"-"`

	Token            string                         `json:"token"`     // 长期服务器 token（session.open 换发）
	ServerID         int64                          `json:"server_id"` // 面板分配的服务器 id
	PanelInstanceID  string                         `json:"panel_instance_id"`
	CredentialEpoch  int64                          `json:"credential_epoch"`
	PanelObservation *shared.PanelLifecycleSnapshot `json:"panel_observation,omitempty"`
	AuthRejected     bool                           `json:"auth_rejected,omitempty"`
	ChainPieces      []ChainPiece                   `json:"chain_pieces,omitempty"`
}

// ChainPiece 是一个链跳配置件（§21.1 piece）的落盘记录：
// 渲染后的配置件原样保存，agent 重启重建 config.json 时与节点同等并入（§17 净化路径同理）；
// Port/PrivateKey/PublicKey 供重发幂等复用（端口与公钥不变，下游 bridge 凭证不失效）。
type ChainPiece struct {
	HopID      int64             `json:"hop_id"`
	Kind       string            `json:"kind"`                  // portal|bridge|forward
	Port       int               `json:"port,omitempty"`        // portal/forward：已分配端口
	PrivateKey string            `json:"private_key,omitempty"` // portal：Reality 私钥（不出本机）
	PublicKey  string            `json:"public_key,omitempty"`  // portal：对应公钥（回执值）
	Inbound    json.RawMessage   `json:"inbound,omitempty"`     // portal/forward 的 inbound
	Outbound   json.RawMessage   `json:"outbound,omitempty"`    // bridge 的 interconn outbound
	Outbounds  []json.RawMessage `json:"outbounds,omitempty"`   // shared endpoint 的逐链 outbound
	Reverse    json.RawMessage   `json:"reverse,omitempty"`     // reverse.portals/bridges 条目
	Rules      []json.RawMessage `json:"rules,omitempty"`       // routing 规则
}

// Load 读取状态文件；不存在或为空时返回零值 State（首次启动）。
func Load(path string) (*State, error) {
	st := &State{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if len(b) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(b, st); err != nil {
		return st, err
	}
	return st, nil
}

// Save 在 st.mu 保护下原子落盘状态（tmp + rename，0600）。
func Save(path string, st *State) error {
	return SaveWith(path, st, nil)
}

// SaveWith 在互斥下应用变更并原子落盘（读-改-写一体）：WS 读循环与离线命令
// 队列执行器并发变更 state 时，避免共用 .tmp 文件互相破坏导致状态损坏
// （评审 P2-2）。mutate 内不得重入 State 的锁方法。
func SaveWith(path string, st *State, mutate func(*State)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if mutate != nil {
		mutate(st)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, b, 0o600)
}

// Reset 清空状态（跨面板重绑，§5）：在互斥下执行，避免与并发变更竞争。
func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Token, s.ServerID, s.PanelInstanceID, s.CredentialEpoch = "", 0, "", 0
	s.PanelObservation = nil
	s.AuthRejected = false
	s.ChainPieces = nil
}

func SaveConnectionStatus(path string, status ConnectionStatus) error {
	b, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, b, 0o600)
}

func LoadSettings(path string) (shared.AgentSettingsDocument, error) {
	var settings shared.AgentSettingsDocument
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		return settings, err
	}
	if err := settings.Validate(); err != nil {
		return shared.AgentSettingsDocument{}, err
	}
	return settings, nil
}

func SaveSettings(path string, settings shared.AgentSettingsDocument) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, b, 0o600)
}

func LoadServerSettings(path string) (shared.ServerSettingsDocument, error) {
	var settings shared.ServerSettingsDocument
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		return settings, err
	}
	if err := settings.Validate(); err != nil {
		return shared.ServerSettingsDocument{}, err
	}
	return settings, nil
}

func SaveServerSettings(path string, settings shared.ServerSettingsDocument) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, b, 0o600)
}
