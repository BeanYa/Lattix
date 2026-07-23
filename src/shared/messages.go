// Package shared 定义 Backend 与 Agent 之间控制通道的协议类型（设计文档 §5），
// 以及两端共用的虚拟配置类型（§7），保证协议两端类型一致。
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
	TypeHello       = "hello"        // agent→panel 首连认证
	TypeApplyNode   = "apply_node"   // panel→agent 下发节点
	TypeRemoveNode  = "remove_node"  // panel→agent 删除节点
	TypeAddUser     = "add_user"     // panel→agent 热加入一个用户
	TypeRemoveUser  = "remove_user"  // panel→agent 热移除一个用户
	TypeApplyResult = "apply_result" // agent→panel 上报执行结果
	TypeUninstall   = "uninstall"    // panel→agent 卸载 agent（先回执再自毁）
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
type ApplyNodePayload struct {
	NodeID    int64         `json:"node_id"`
	Config    VirtualConfig `json:"config"`
	UserUUIDs []string      `json:"user_uuids"`
}

// RemoveNodePayload 是 remove_node 的载荷。
type RemoveNodePayload struct {
	NodeID int64 `json:"node_id"`
}

// AddUserPayload 是 add_user 的载荷：向该服务器所有 inbound 热加入一个用户。
type AddUserPayload struct {
	UUID string `json:"uuid"`
}

// RemoveUserPayload 是 remove_user 的载荷：从该服务器所有 inbound 热移除一个用户。
type RemoveUserPayload struct {
	UUID string `json:"uuid"`
}

// ApplyResultPayload 是 apply_result 的载荷：成功返回 RealizedConfig，失败返回 Error（§5）。
type ApplyResultPayload struct {
	NodeID         int64           `json:"node_id"`
	OK             bool            `json:"ok"`
	RealizedConfig *RealizedConfig `json:"realized_config,omitempty"`
	Error          string          `json:"error,omitempty"`
}
