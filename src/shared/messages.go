// Package shared 定义 Backend 与 Agent 之间控制通道的协议类型（设计文档 §5），
// 以及两端共用的虚拟配置类型（§7），保证协议两端类型一致。
//
// 协议演化规则（兼容窗口内硬性约束，CI 经 scripts/check-protocol-compat.sh 强制）：
//   - 已有 TypeXxx 常量值不得修改；
//   - 已有 struct 的已有字段/json tag 不得删除或修改，只允许新增字段（带 omitempty）；
//   - 新语义走新字段或新消息类型（如 apply_node_v2），不改旧载荷语义；
//   - 废弃字段标注 deprecated 但窗口期内保留；
//   - 两端均不得使用 json.Decoder.DisallowUnknownFields（忽略未知字段是新旧互跑的基础）。
package shared

import "encoding/json"

// Envelope 是控制通道的统一消息信封，ID 用于请求/响应关联（§5）。
type Envelope struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// 消息类型（§5）。
const (
	TypeHello        = "hello"         // agent→panel 首连认证
	TypeApplyNode    = "apply_node"    // panel→agent 下发节点
	TypeRemoveNode   = "remove_node"   // panel→agent 删除节点
	TypeAddUser      = "add_user"      // panel→agent 热加入一个用户
	TypeRemoveUser   = "remove_user"   // panel→agent 热移除一个用户
	TypeApplyResult  = "apply_result"  // agent→panel 上报执行结果
	TypeUninstall    = "uninstall"     // panel→agent 卸载 agent（先回执再自毁）
	TypeUpgradeXray  = "upgrade_xray"  // panel→agent 升级 xray 版本（§18）
	TypeUpgradeAgent = "upgrade_agent" // panel→agent 升级 agent 自身（下载校验后自替换，退出由 systemd 拉起）
	TypeTelemetry    = "telemetry"     // agent→panel 周期遥测（流量 + 主机指标，§13）
	TypeDriftReport  = "drift_report"  // agent→panel 配置漂移状态变化（§17 reconcile）

	TypeApplyChainHop  = "apply_chain_hop"  // panel→agent 下发链跳配置件（portal/bridge/forward，§21）
	TypeRemoveChainHop = "remove_chain_hop" // panel→agent 删除链跳配置件（删链逐跳反向下发，§21）
)

// HelloPayload 是 hello 的载荷：token（bootstrap 或长期）、agent 版本、
// xray 版本与运行状态（§5、§13）。
type HelloPayload struct {
	Token        string `json:"token"`
	AgentVersion string `json:"agent_version"`
	XrayVersion  string `json:"xray_version"`
	XrayRunning  bool   `json:"xray_running"`
}

// HelloResult 是 panel 对 hello 的响应：bootstrap token 在此换发长期凭证（§11）。
type HelloResult struct {
	ServerID int64  `json:"server_id"`
	Token    string `json:"token"` // 长期服务器 token
}

// ApplyNodePayload 是 apply_node 的载荷：虚拟配置模板 + 全量用户 UUID 列表（§8）。
// DestCandidates 是面板内置的 dest 白名单（§6 预检 fallback，随版本更新，尽量丰富）。
// PortCandidates 是受限直连 NAT 机上节点端口的段内候选（§21，Port 为 0 时由 Agent 依次挑选）。
type ApplyNodePayload struct {
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

// ApplyResultPayload 是 apply_result 的载荷：成功返回 RealizedConfig，失败返回 Error（§5）。
// HopID/Kind 为链跳配置件（apply_chain_hop/remove_chain_hop）回执（§21）：
// portal/forward 复用 RealizedConfig 的 port/public_key 字段上报生效值。
type ApplyResultPayload struct {
	NodeID         int64           `json:"node_id"`
	OK             bool            `json:"ok"`
	RealizedConfig *RealizedConfig `json:"realized_config,omitempty"`
	Error          string          `json:"error,omitempty"`
	HopID          int64           `json:"hop_id,omitempty"`
	Kind           string          `json:"kind,omitempty"`
}

// TelemetryPayload 是 telemetry 的载荷（§13 遥测，周期上报，无需回执）：
// xray 版本/运行状态（升级管理据此刷新展示）、主机指标、流量增量。
type TelemetryPayload struct {
	XrayVersion string         `json:"xray_version"`
	XrayRunning bool           `json:"xray_running"`
	Host        *HostMetrics   `json:"host,omitempty"`
	Traffic     []TrafficDelta `json:"traffic,omitempty"`
}

// HostMetrics 是主机指标（/proc 采集）。
type HostMetrics struct {
	Load1      float64 `json:"load1"`       // 1 分钟负载
	CPUPercent float64 `json:"cpu_percent"` // 采样区间 CPU 使用率（%）
	MemTotal   uint64  `json:"mem_total"`   // 字节
	MemUsed    uint64  `json:"mem_used"`    // 字节
}

// TrafficDelta 是一个计数器在采样区间内的流量增量（字节）。
// Node 为 inbound tag（node_<id>，节点维度）；User 为用户 UUID（email，用户维度）。
type TrafficDelta struct {
	Node string `json:"node,omitempty"`
	User string `json:"user,omitempty"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
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
}

// ApplyChainHopPayload 是 apply_chain_hop 的载荷（§21.1）：
// 按 Kind 携带对应配置件规格，Agent 渲染并入受管 config.json。
// DestCandidates 同 ApplyNodePayload（portal 的 Reality dest 预检白名单，§6）。
type ApplyChainHopPayload struct {
	ChainID        int64        `json:"chain_id"`
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
}

// RemoveChainHopPayload 是 remove_chain_hop 的载荷（§21.1）：删链逐跳反向下发。
type RemoveChainHopPayload struct {
	HopID int64  `json:"hop_id"`
	Kind  string `json:"kind"` // portal|bridge|forward
}

// ErrUnsupportedPrefix 是 agent 对不认识的命令类型回执的错误前缀（协议演化规则）：
// 面板收到该前缀的失败即终态（命令 failed），不再重试——重试也不会变得被支持。
const ErrUnsupportedPrefix = "unsupported command"
