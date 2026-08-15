// Package shared 定义 Backend 与 Agent 之间控制通道的协议类型，
// 以及两端共用的虚拟配置类型，保证协议两端类型一致。
package shared

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Envelope 是控制通道的统一 RPC 消息信封。
type Envelope struct {
	Kind      string          `json:"kind"`
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	TraceID   string          `json:"trace_id"`
	Code      string          `json:"code,omitempty"`
	Message   string          `json:"message,omitempty"`
	Data      json.RawMessage `json:"data"`
}

// MarshalJSON guarantees that responses always carry code and message, while
// requests/events omit both response-only fields.
func (e Envelope) MarshalJSON() ([]byte, error) {
	type base struct {
		Kind      string          `json:"kind"`
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		TraceID   string          `json:"trace_id"`
		Data      json.RawMessage `json:"data"`
	}
	value := base{
		Kind: e.Kind, Type: e.Type, RequestID: e.RequestID, TraceID: e.TraceID, Data: e.Data,
	}
	if e.Kind != KindResponse {
		return json.Marshal(value)
	}
	return json.Marshal(struct {
		base
		Code    string `json:"code"`
		Message string `json:"message"`
	}{base: value, Code: e.Code, Message: e.Message})
}

const (
	KindRequest  = "request"
	KindResponse = "response"
	KindEvent    = "event"
)

// RPC 业务结果码。HTTP 与 WebSocket 共用同一语义。
const (
	CodeOK                     = "OK"
	CodeAccepted               = "ACCEPTED"
	CodeAuthRequired           = "AUTH_REQUIRED"
	CodeAuthInvalidCredentials = "AUTH_INVALID_CREDENTIALS"
	CodeInvalidArgument        = "INVALID_ARGUMENT"
	CodeNotFound               = "NOT_FOUND"
	CodeConflict               = "CONFLICT"
	CodeOperationLocked        = "OPERATION_LOCKED"
	CodeUnsupportedAction      = "UNSUPPORTED_ACTION"
	CodeInternalError          = "INTERNAL_ERROR"
	CodeUpstreamError          = "UPSTREAM_ERROR"
	CodeServiceUnavailable     = "SERVICE_UNAVAILABLE"
	CodeServerOffline          = "SERVER_OFFLINE"
	CodePortOutOfRange         = "PORT_OUT_OF_RANGE"
	CodeUpdateInProgress       = "UPDATE_IN_PROGRESS"
)

// 消息类型使用 domain.action，响应沿用对应请求的 Type。
const (
	TypeSessionOpen          = "agent.session.open"
	TypeSessionReady         = "agent.session.ready"
	TypeCredentialCommit     = "agent.credential.commit"
	TypeLifecycleChanged     = "panel.lifecycle.changed"
	TypeSettingsSync         = "agent.settings.sync"
	TypeSettingsChanged      = "agent.settings.changed"
	TypeServerSettingsSync    = "server.settings.sync"
	TypeServerSettingsChanged = "server.settings.changed"
	TypeApplyNode            = "node.apply"
	TypeRemoveNode           = "node.remove"
	TypeAddUser              = "user.add"
	TypeRemoveUser           = "user.remove"
	TypeUninstall            = "agent.uninstall"
	TypeUpgradeXray          = "xray.upgrade"
	TypeUpgradeAgent         = "agent.upgrade"
	TypeTelemetry            = "telemetry.report"
	TypeDriftReport          = "config.drift"
	TypeApplyChainHop        = "chain-hop.apply"
	TypeRemoveChainHop       = "chain-hop.remove"
	TypeApplySharedEndpoint  = "shared-endpoint.apply"
	TypeRemoveSharedEndpoint = "shared-endpoint.remove"
	TypeCleanupXray          = "xray.cleanup"
	TypeRebuildXray          = "xray.rebuild"
)

// NewMessageID 返回用于 request_id/trace_id 的 32 位小写十六进制随机值。
func NewMessageID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

// ValidMessageID 校验 request_id/trace_id 的固定格式。
func ValidMessageID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// Validate 校验与具体动作 data 无关的 WS 信封结构。
func (e Envelope) Validate() error {
	switch e.Kind {
	case KindRequest, KindResponse, KindEvent:
	default:
		return fmt.Errorf("invalid kind %q", e.Kind)
	}
	if e.Type == "" {
		return errors.New("type is required")
	}
	if !ValidMessageID(e.RequestID) {
		return errors.New("invalid request_id")
	}
	if !ValidMessageID(e.TraceID) {
		return errors.New("invalid trace_id")
	}
	if len(e.Data) == 0 || !json.Valid(e.Data) {
		return errors.New("data must be valid JSON")
	}
	if e.Kind == KindResponse && e.Code == "" {
		return errors.New("response code is required")
	}
	if e.Kind != KindResponse && (e.Code != "" || e.Message != "") {
		return errors.New("request/event cannot contain code or message")
	}
	return nil
}

// ApplyNodePayload 是 apply_node 的载荷：虚拟配置模板 + 全量用户 UUID 列表（§8）。
// DestCandidates 是面板内置的 dest 白名单（§6 预检 fallback，随版本更新，尽量丰富）。
// PortCandidates 是受限直连 NAT 机上节点端口的段内候选（§21，Port 为 0 时由 Agent 依次挑选）。
type ApplyNodePayload struct {
	RevisionID     int64         `json:"revision_id,omitempty"`
	NodeID         int64         `json:"node_id"`
	Config         VirtualConfig `json:"config"`
	UserUUIDs      []string      `json:"user_uuids"`
	DestCandidates []string      `json:"dest_candidates,omitempty"`
	PortCandidates []int         `json:"port_candidates,omitempty"`
}

// RemoveNodePayload 是 remove_node 的载荷。
type RemoveNodePayload struct {
	NodeID int64 `json:"node_id"`
}

// AddUserPayload 是 add_user 的载荷：向 Nodes 列出的节点 inbound 热加入一个用户。
// Nodes 携带该服务器各节点的用户条目参数（key 为 node tag，见 NodeTag），
// Agent 据此构造各协议正确的 account（vless id+flow / vmess id / trojan password /
// ss method+password / socks+http user+pass）；dokodemo 节点无用户概念，不在其中。
// Nodes 必填：缺失/为空时 Agent 回执错误（不做全量作用）。
type AddUserPayload struct {
	UUID  string                    `json:"uuid"`
	Nodes map[string]UserNodeParams `json:"nodes,omitempty"`
}

// UserNodeParams 描述一个节点的用户条目构造参数。
type UserNodeParams struct {
	Protocol string `json:"protocol"`
	Flow     string `json:"flow,omitempty"`   // vless
	Method   string `json:"method,omitempty"` // shadowsocks
}

// RemoveUserPayload 是 remove_user 的载荷：从 Nodes 列出的节点 inbound 热移除一个用户。
// Nodes 同 AddUserPayload（必填，缺失/为空回执错误）：Agent 据此判断各 inbound 协议能否热删，不能则走重启兜底。
type RemoveUserPayload struct {
	UUID  string                    `json:"uuid"`
	Nodes map[string]UserNodeParams `json:"nodes,omitempty"`
}

// UninstallPayload 是 uninstall 的载荷（§5）。
type UninstallPayload struct {
	PurgeXray bool `json:"purge_xray"` // true = 连同 install.sh 安装的 xray 与配置一并清除
}

// ApplyResultPayload 是命令 response 的 data；失败原因由信封 code/message 表达。
// HopID/Kind 为链跳配置件（apply_chain_hop/remove_chain_hop）回执（§21）：
// portal/forward 复用 RealizedConfig 的 port/public_key 字段上报生效值。
type ApplyResultPayload struct {
	NodeID         int64           `json:"node_id"`
	EndpointID     int64           `json:"endpoint_id,omitempty"`
	RealizedConfig *RealizedConfig `json:"realized_config,omitempty"`
	HopID          int64           `json:"hop_id,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	Cleanup        *CleanupXrayResult `json:"cleanup,omitempty"` // xray.cleanup 回执
	Rebuild        *RebuildXrayResult `json:"rebuild,omitempty"` // xray.rebuild 回执
}

// CleanupXrayPayload 是 xray.cleanup 的载荷：面板下发的期望状态快照。
// DryRun=true 时 agent 只报告差异，不改动配置（预览）。
type CleanupXrayPayload struct {
	DryRun             bool     `json:"dry_run"`
	ExpectedInboundTags []string `json:"expected_inbound_tags"` // node_/chainfwd_/chainportal_/shared_endpoint_
	ExpectedPieces      []string `json:"expected_pieces"`       // "forward/7"、"portal/9"、"bridge/3"、"shared-endpoint/5"
}

// CleanupInbound 是一条将被/已删除的 inbound 摘要（port 供展示与日志）。
type CleanupInbound struct {
	Tag  string `json:"tag"`
	Port int    `json:"port"`
}

// CleanupXrayResult 是 xray.cleanup 的回执数据：差异列表（dry-run 预览或实际删除结果）。
type CleanupXrayResult struct {
	RemovedInbounds []CleanupInbound `json:"removed_inbounds"`
	RemovedPieces   []string         `json:"removed_pieces"`
}

// RebuildXrayPayload 是 xray.rebuild 的载荷：面板下发的重建期望。
// Nodes 为该服务器全部活跃节点的完整 apply 规格（模板+用户，agent 重渲染）；
// ExpectedInboundTags/ExpectedPieces 为重建后自检用的期望集合。
type RebuildXrayPayload struct {
	Nodes               []ApplyNodePayload `json:"nodes"`
	ExpectedInboundTags []string           `json:"expected_inbound_tags"`
	ExpectedPieces      []string           `json:"expected_pieces"`
}

// RebuiltInbound 是重建后的一条 inbound 摘要（展示用）。
type RebuiltInbound struct {
	Tag  string `json:"tag"`
	Port int    `json:"port"`
	Kind string `json:"kind"` // 协议（vless/vmess/…）
}

// RebuildXrayResult 是 xray.rebuild 的回执数据：重建后的监听/piece 摘要与回滚标记。
// 失败时错误经信封 code/message 表达，RolledBack=true 表示已恢复备份配置。
type RebuildXrayResult struct {
	RebuiltInbounds []RebuiltInbound `json:"rebuilt_inbounds"`
	RebuiltPieces   []string         `json:"rebuilt_pieces"`
	RolledBack      bool             `json:"rolled_back"`
}

// MarshalJSON 保证数组字段始终为 [] 而非 null（前端直接读 .length，null 会崩溃）。
func (r RebuildXrayResult) MarshalJSON() ([]byte, error) {
	type alias RebuildXrayResult
	if r.RebuiltInbounds == nil {
		r.RebuiltInbounds = []RebuiltInbound{}
	}
	if r.RebuiltPieces == nil {
		r.RebuiltPieces = []string{}
	}
	return json.Marshal(alias(r))
}

// ApplySharedEndpointPayload replaces the complete managed state of one
// server-level VLESS+REALITY listener. Routes are grouped by chain while
// Clients retain assignment-level identities for quota accounting.
type ApplySharedEndpointPayload struct {
	EndpointID     int64                 `json:"endpoint_id"`
	Config         VirtualConfig         `json:"config"`
	Clients        []ClientCredential    `json:"clients"`
	Routes         []SharedEndpointRoute `json:"routes"`
	DestCandidates []string              `json:"dest_candidates,omitempty"`
	PortCandidates []int                 `json:"port_candidates,omitempty"`
}

type SharedEndpointRoute struct {
	ChainID       int64          `json:"chain_id"`
	Users         []string       `json:"users"`
	Direct        bool           `json:"direct,omitempty"`
	TargetAddress string         `json:"target_address,omitempty"`
	TargetPort    int            `json:"target_port,omitempty"`
	TunnelUUID    string         `json:"tunnel_uuid,omitempty"`
	Target        RealizedConfig `json:"target,omitempty"`
}

type RemoveSharedEndpointPayload struct {
	EndpointID int64 `json:"endpoint_id"`
}

// OnlineUserStat 是某服务器当前在线的一个用户（有活跃连接）及其源 IP 列表（去重）。
type OnlineUserStat struct {
	User string   `json:"user"` // 用户 UUID（xray email）
	IPs  []string `json:"ips"`
}

// TelemetryPayload 是 telemetry 的载荷（§13 遥测，周期上报，无需回执）：
// xray 版本/运行状态（升级管理据此刷新展示）、主机指标、流量增量。
type TelemetryPayload struct {
	XrayVersion    string           `json:"xray_version"`
	XrayRunning    bool             `json:"xray_running"`
	XrayInstanceID string           `json:"xray_instance_id,omitempty"`
	Host           *HostMetrics     `json:"host,omitempty"`
	Traffic        []TrafficCounter `json:"traffic,omitempty"`
	// OnlineUsers 该服务器在线用户全量快照；无 omitempty 以便区分
	// "成功空查询"（[]，序列化为 []）与"查询失败/不支持"（nil，序列化为 null）。
	OnlineUsers []OnlineUserStat `json:"online_users"`
}

// HostMetrics 是主机指标（/proc 采集）。
type HostMetrics struct {
	Load1              float64  `json:"load1"`                       // 1 分钟负载
	Load5              float64  `json:"load5"`                       // 5 分钟负载
	Load15             float64  `json:"load15"`                      // 15 分钟负载
	CPUPercent         *float64 `json:"cpu_percent,omitempty"`       // 采样区间 CPU 使用率（%）
	MemTotal           uint64   `json:"mem_total"`                   // 字节
	MemUsed            uint64   `json:"mem_used"`                    // 字节
	DiskTotal          uint64   `json:"disk_total"`                  // 根文件系统字节
	DiskUsed           uint64   `json:"disk_used"`                   // 根文件系统字节
	NetworkInterface   string   `json:"network_interface,omitempty"` // 默认路由出口网卡
	NetworkTXBytes     uint64   `json:"network_tx_bytes"`            // 开机以来上传字节
	NetworkRXBytes     uint64   `json:"network_rx_bytes"`            // 开机以来下载字节
	NetworkTXBPS       *float64 `json:"network_tx_bps,omitempty"`    // 采样区间上传速率
	NetworkRXBPS       *float64 `json:"network_rx_bps,omitempty"`    // 采样区间下载速率
	UptimeSeconds      uint64   `json:"uptime_seconds"`
	LatencyMS          *float64 `json:"latency_ms,omitempty"`           // 最近 3 次 WebSocket RTT 中位数
	LatencyProbeActive *bool    `json:"latency_probe_active,omitempty"` // 当前生命周期是否接受延迟探测；缺省兼容旧 Agent
}

// TrafficCounter 是一个 Xray 实例自启动以来的绝对业务流量计数器（字节）。
// Backend 持久化游标并计算增量，因而 WS 丢帧可由下一帧补齐且重发天然幂等。
type TrafficCounter struct {
	NodeID     int64  `json:"node_id,omitempty"`
	EndpointID int64  `json:"endpoint_id,omitempty"`
	HopID      int64  `json:"hop_id,omitempty"`
	User       string `json:"user,omitempty"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
}

// DriftPayload 是 drift_report 的载荷（§17）：配置文件被外部修改时为 true，
// 修复（重放 apply）或外部恢复后为 false，仅在状态变化时上报。
type DriftPayload struct {
	Drifted bool `json:"drifted"`
}

// UpgradeXrayPayload 是 upgrade_xray 的载荷（§18）：
// version 为具体版本号（vX.Y.Z）或 latest（agent 执行时经 GitHub API 解析）。
type UpgradeXrayPayload struct {
	Version string `json:"version"`
}

// UpgradeAgentPayload 是 upgrade_agent 的载荷：
// agent 从 ReleaseBase/<version>/ 下载 lattix-agent-linux-<arch> 与 checksums.txt，
// 校验 SHA256 后原子替换自身二进制并退出（systemd 拉起即完成升级，§18）。
// ReleaseBase 为空时 agent 使用构建时注入的默认仓库。
type UpgradeAgentPayload struct {
	Version     string `json:"version"`                // 目标版本 tag（vX.Y.Z）
	ReleaseBase string `json:"release_base,omitempty"` // 形如 https://github.com/<org>/<repo>/releases/download
	Force       bool   `json:"force,omitempty"`        // true 时即使目标版本与当前版本相同也覆盖安装
}

// ApplyChainHopPayload 是 apply_chain_hop 的载荷（§21.1）：
// 按 Kind 携带对应配置件规格，Agent 渲染并入受管 config.json。
// DestCandidates 同 ApplyNodePayload（portal 的 Reality dest 预检白名单，§6）。
type ApplyChainHopPayload struct {
	ChainID        int64        `json:"chain_id"`
	RevisionID     int64        `json:"revision_id,omitempty"`
	HopID          int64        `json:"hop_id"`
	Kind           string       `json:"kind"` // portal|bridge|forward
	Portal         *PortalSpec  `json:"portal,omitempty"`
	Bridge         *BridgeSpec  `json:"bridge,omitempty"`
	Forward        *ForwardSpec `json:"forward,omitempty"`
	DestCandidates []string     `json:"dest_candidates,omitempty"`
}

// PortalSpec 是反向链上游机的配置件（§21.1）：
// VLESS+Reality interconn inbound + reverse portal；密钥对由 Agent 生成随回执上报，
// UUID/short_id 由面板下发，dest 走 §6 预检+白名单。
type PortalSpec struct {
	Tag            string   `json:"tag"`
	TunnelDomain   string   `json:"tunnel_domain"`
	Port           int      `json:"port"` // 0 = 自动（从 PortCandidates 挑空闲）
	PortCandidates []int    `json:"port_candidates,omitempty"`
	TunnelUUID     string   `json:"tunnel_uuid"`
	ShortID        string   `json:"short_id"`
	Dest           string   `json:"dest"`
	ServerNames    []string `json:"server_names"`
	ListenFamily   string   `json:"listen_family,omitempty"` // "ipv6" → 监听 ::（双栈）；空 = 0.0.0.0（§9）
}

// BridgeSpec 是反向链下游机（仅出口档 NAT）的配置件（§21.1）：
// reverse bridge + VLESS+Reality interconn outbound + routing（bridge → freedom 拨回环业务 inbound）。
// 凭证（公钥/端口/UUID/shortID/SNI）来自对应 portal 的回执与下发值。
type BridgeSpec struct {
	TunnelDomain  string `json:"tunnel_domain"`
	PortalAddress string `json:"portal_address"`
	PortalPort    int    `json:"portal_port"`
	TunnelUUID    string `json:"tunnel_uuid"`
	PublicKey     string `json:"public_key"`
	ShortID       string `json:"short_id"`
	ServerName    string `json:"server_name"`
}

// ForwardSpec 是入口/中间跳的配置件（§21.1）：dokodemo-door 透传 inbound + 路由。
// 直连段：freedom 拨下一跳 TargetAddress:TargetPort（公网侧端口）；
// 反向段：经 ViaTunnelDomain 走 reverse portal，目标为下一跳回环地址与监听端口。
type ForwardSpec struct {
	Tag             string `json:"tag"`
	Port            int    `json:"port"` // 0 = 自动（从 PortCandidates 挑空闲）
	PortCandidates  []int  `json:"port_candidates,omitempty"`
	TargetAddress   string `json:"target_address"`
	TargetPort      int    `json:"target_port"`
	ViaTunnelDomain string `json:"via_tunnel_domain,omitempty"`
	LocalOnly       bool   `json:"local_only,omitempty"`
	ListenFamily    string `json:"listen_family,omitempty"` // "ipv6" → 监听 ::（双栈）；空 = 0.0.0.0（§9）
}

// RemoveChainHopPayload 是 remove_chain_hop 的载荷（§21.1）：删链逐跳反向下发。
type RemoveChainHopPayload struct {
	HopID int64  `json:"hop_id"`
	Kind  string `json:"kind"` // portal|bridge|forward
}
