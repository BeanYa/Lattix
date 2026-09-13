package panel

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// trafficDTO 是流量合计的 API 表示（§13 仅统计）。
type trafficDTO struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// nodeDTO 是节点对象的 API 表示。
type nodeDTO struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	ServerID       int64           `json:"server_id"`
	ServerAlias    string          `json:"server_alias"`
	Protocol       string          `json:"protocol"`
	Port           *int            `json:"port"` // null = Agent 自动挑选（§7）
	Status         string          `json:"status"`
	Error          string          `json:"error"`
	Traffic        *trafficDTO     `json:"traffic"` // 节点流量合计（§13），无数据为 null
	ConfigTemplate json.RawMessage `json:"config_template"`
	RealizedConfig json.RawMessage `json:"realized_config"`
	CreatedAt      time.Time       `json:"created_at"`
}

func toNodeDTO(n store.Node) nodeDTO {
	return nodeDTO{
		ID:             n.ID,
		Name:           n.Name,
		ServerID:       n.ServerID,
		ServerAlias:    n.ServerAlias,
		Protocol:       n.Protocol,
		Port:           n.Port,
		Status:         n.Status,
		Error:          n.Error,
		ConfigTemplate: n.ConfigTemplate,
		RealizedConfig: n.RealizedConfig,
		CreatedAt:      n.CreatedAt,
	}
}

// handleListNodes 处理 GET /api/nodes。
func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	traffic, err := s.st.TrafficByNode(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]nodeDTO, 0, len(nodes))
	for _, n := range nodes {
		dto := toNodeDTO(n)
		if t, ok := traffic[n.ID]; ok {
			dto.Traffic = &trafficDTO{Up: t.Up, Down: t.Down}
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

// createNodeRequest 是节点创建向导的提交（§10）：端口可空 = 自动（§7）。
// 各协议有效字段见设计文档"全协议向导"：reality 系（vless/vmess/trojan）使用
// short_id/dest/server_names/fingerprint/network 及 grpc/xhttp/ws/httpupgrade 子选项；security 仅 reality 系协议有效（reality/tls/none）；tls 的 cert_mode/tls_domain 见 §3.3 证书策略；flow 仅 vless+tcp；
// method 仅 shadowsocks；cipher 仅 vmess；target_address/target_port 仅 dokodemo-door；
// obfs_password/up_mbps/down_mbps/port_hop 仅 hysteria（恒 QUIC+TLS，无 network/security 选择）。
type createNodeRequest struct {
	Name          string   `json:"name"`
	ServerID      int64    `json:"server_id"`
	Protocol      string   `json:"protocol"`       // 默认 vless
	Port          *int     `json:"port"`           // 留空 = Agent 自动挑选
	ShortID       string   `json:"short_id"`       // 默认随机 8 字节 hex
	Dest          string   `json:"dest"`           // 默认 dl.google.com:443
	ServerNames   []string `json:"server_names"`   // 默认 [dl.google.com]
	Fingerprint   string   `json:"fingerprint"`    // 默认 chrome
	Network       string   `json:"network"`        // tcp（默认）/ grpc / xhttp / ws / httpupgrade
	Security      string   `json:"security"`       // reality（默认推导）/ tls / none
	CertMode      string   `json:"cert_mode"`      // security=tls：selfsign（默认）/ acme
	TLSDomain     string   `json:"tls_domain"`     // selfsign=伪装域名（留空随机）；acme=落地服务器域名（处理器检测填充）
	ServiceName   string   `json:"service_name"`   // grpc，默认 "grpc"
	Path          string   `json:"path"`           // xhttp/ws/httpupgrade，默认 "/"
	Mode          string   `json:"mode"`           // xhttp，默认 auto
	Host          string   `json:"host"`           // xhttp/ws/httpupgrade，可空
	Flow          string   `json:"flow"`           // vless 默认 xtls-rprx-vision（仅 tcp）
	Encryption    string   `json:"encryption"`     // vless：VLESS Encryption 认证方式（x25519/mlkem768），可与 flow 组合（§15）
	Method        string   `json:"method"`         // shadowsocks，默认 2022-blake3-aes-128-gcm
	Cipher        string   `json:"cipher"`         // vmess 客户端 cipher，默认 auto
	TargetAddress string   `json:"target_address"` // dokodemo-door 转发目标
	TargetPort    *int     `json:"target_port"`
	ObfsPassword  string   `json:"obfs_password"` // hysteria：salamander 混淆密码，留空自动生成
	UpMbps        int      `json:"up_mbps"`       // hysteria：brutal 上行声明，默认 50（0=不声明，回退 BBR）
	DownMbps      int      `json:"down_mbps"`     // hysteria：brutal 下行声明，默认 100
	PortHop       string   `json:"port_hop"`      // hysteria："off"=关闭跳跃；""=自动分配 32 段；"a-b"=显式段
}

// normalize 填默认值并校验协议参数组合，返回用户可读的校验错误。
func (req *createNodeRequest) normalize() error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Protocol == "" {
		req.Protocol = shared.ProtocolVLESS
	}
	if !shared.ValidValue(req.Protocol, shared.Protocols) {
		return fmt.Errorf("不支持的协议: %s", req.Protocol)
	}
	// cipher 仅 vmess 有效：其他协议一律清空（单一真相点，vmess 分支只做默认值/校验）。
	if req.Protocol != shared.ProtocolVMess {
		req.Cipher = ""
	}

	// hysteria：无 network 概念、恒 QUIC+TLS（spec §2 矩阵）；证书模式复用 TLS 双模式。
	if req.Protocol == shared.ProtocolHysteria2 {
		if req.Network != "" {
			return fmt.Errorf("hysteria2 无传输层选项（protocol 与 network 冲突，自带 QUIC）")
		}
		if req.Security != "" && req.Security != shared.SecurityTLS {
			return fmt.Errorf("hysteria2 强制 TLS 安全层（protocol 与 security 冲突）")
		}
		req.Security = shared.SecurityTLS
		req.Flow, req.Method, req.Cipher, req.Encryption = "", "", "", ""
		req.ShortID, req.Dest, req.ServerNames = "", "", nil
		req.ServiceName, req.Path, req.Mode, req.Host = "", "", "", ""
		if req.Fingerprint == "" {
			req.Fingerprint = shared.FingerprintChrome
		}
		if !shared.ValidValue(req.Fingerprint, shared.Fingerprints) {
			return fmt.Errorf("不支持的 uTLS 指纹: %s", req.Fingerprint)
		}
		if req.CertMode == "" {
			req.CertMode = shared.CertModeSelfSign
		}
		if !shared.ValidValue(req.CertMode, shared.CertModes) {
			return fmt.Errorf("不支持的证书模式: %s", req.CertMode)
		}
		if req.CertMode == shared.CertModeSelfSign {
			if req.TLSDomain == "" {
				req.TLSDomain = tlsCamouflagePool[randomInt(len(tlsCamouflagePool))]
			}
			if err := validateTLSDomain(req.TLSDomain); err != nil {
				return err
			}
		} else {
			req.TLSDomain = "" // acme：由 applyACMEDomain 从落地服务器地址检测填充
		}
		if req.ObfsPassword == "" {
			req.ObfsPassword = randomHex(16) // salamander 混淆密码（panel 生成，订阅回显）
		}
		if req.UpMbps == 0 && req.DownMbps == 0 {
			req.UpMbps, req.DownMbps = 50, 100
		}
		if req.UpMbps < 0 || req.DownMbps < 0 {
			return fmt.Errorf("hy2 带宽声明须为非负整数（up_mbps/down_mbps）")
		}
		// port_hop 三段语义：off 保留哨兵（由 resolveHy2PortHop 归一为空，与"默认空=自动
		// 分配"区分）；""→默认开启，由 resolveHy2PortHop 自动分配（需服务器上下文，不在
		// normalize 内）；显式段此处做语法与长度校验。
		if req.PortHop != "" && req.PortHop != "off" {
			start, end, err := shared.ParsePortHop(req.PortHop)
			if err != nil {
				return err
			}
			if end-start+1 < shared.Hy2PortHopMinLen || end-start+1 > shared.Hy2PortHopMaxLen {
				return fmt.Errorf("端口跳跃段长须为 %d-%d（当前 %d）",
					shared.Hy2PortHopMinLen, shared.Hy2PortHopMaxLen, end-start+1)
			}
			req.PortHop = shared.FormatPortHop(start, end)
		}
		return nil
	}

	if shared.IsRealityProtocol(req.Protocol) {
		if req.Network == "" {
			req.Network = shared.NetworkTCP
		}
		if !shared.ValidValue(req.Network, shared.Networks) {
			return fmt.Errorf("不支持的传输方式: %s", req.Network)
		}
		// security 缺省推导：reality 兼容传输默认 reality；ws/httpupgrade 只能 none（矩阵）。
		if req.Security == "" {
			if shared.ValidValue(req.Network, shared.RealityNetworks) {
				req.Security = shared.SecurityReality
			} else {
				req.Security = shared.SecurityNone
			}
		}
		if !shared.ValidValue(req.Security, shared.Securities) {
			return fmt.Errorf("不支持的 security: %s", req.Security)
		}
		if req.Security == shared.SecurityReality && !shared.ValidValue(req.Network, shared.RealityNetworks) {
			return fmt.Errorf("network=%s 与 security=reality 冲突：Reality 仅支持 tcp/grpc/xhttp", req.Network)
		}
		switch req.Network {
		case shared.NetworkGRPC:
			if req.ServiceName == "" {
				req.ServiceName = "grpc"
			}
			req.Path, req.Mode, req.Host = "", "", ""
		case shared.NetworkXHTTP:
			if req.Path == "" {
				req.Path = "/"
			}
			if req.Mode == "" {
				req.Mode = "auto"
			}
			if !shared.ValidValue(req.Mode, shared.XHTTPModes) {
				return fmt.Errorf("不支持的 xhttp mode: %s", req.Mode)
			}
			req.ServiceName = ""
		case shared.NetworkWS, shared.NetworkHTTPUpgrade:
			if req.Path == "" {
				req.Path = "/"
			}
			req.ServiceName, req.Mode = "", ""
		default: // tcp
			req.ServiceName, req.Path, req.Mode, req.Host = "", "", "", ""
		}
		switch req.Security {
		case shared.SecurityReality:
			if req.Fingerprint == "" {
				req.Fingerprint = shared.FingerprintChrome
			}
			if !shared.ValidValue(req.Fingerprint, shared.Fingerprints) {
				return fmt.Errorf("不支持的 uTLS 指纹: %s", req.Fingerprint)
			}
			if req.ShortID == "" {
				req.ShortID = randomHex(8)
			}
			if req.Dest == "" {
				req.Dest = "dl.google.com:443"
			}
			if len(req.ServerNames) == 0 {
				req.ServerNames = []string{"dl.google.com"}
			}
		case shared.SecurityTLS:
			// reality 专有字段无意义一律清空；fingerprint 保留（tls 客户端同样下发 uTLS 指纹）。
			req.ShortID, req.Dest, req.ServerNames = "", "", nil
			if req.Fingerprint == "" {
				req.Fingerprint = shared.FingerprintChrome
			}
			if !shared.ValidValue(req.Fingerprint, shared.Fingerprints) {
				return fmt.Errorf("不支持的 uTLS 指纹: %s", req.Fingerprint)
			}
			if req.CertMode == "" {
				req.CertMode = shared.CertModeSelfSign
			}
			if !shared.ValidValue(req.CertMode, shared.CertModes) {
				return fmt.Errorf("不支持的证书模式: %s", req.CertMode)
			}
			if req.CertMode == shared.CertModeSelfSign {
				if req.TLSDomain == "" {
					req.TLSDomain = tlsCamouflagePool[randomInt(len(tlsCamouflagePool))]
				}
				if err := validateTLSDomain(req.TLSDomain); err != nil {
					return err
				}
			} else {
				// acme：域名由处理器从落地服务器公网地址检测填充（applyACMEDomain），用户输入忽略。
				req.TLSDomain = ""
			}
		default: // none
			// security=none：Reality 专有字段无意义，一律清空；并按协议执行矩阵约束。
			req.ShortID, req.Dest, req.ServerNames, req.Fingerprint = "", "", nil, ""
			if req.Protocol == shared.ProtocolTrojan {
				return fmt.Errorf("trojan 不允许 security=none（protocol 与 security 冲突；trojan 需 reality 或 tls）")
			}
			if req.Protocol == shared.ProtocolVLESS && req.Encryption == "" {
				return fmt.Errorf("vless 在 security=none 下必须启用 VLESS Encryption（security 与 encryption 冲突）")
			}
		}
	} else {
		// ss/socks/http/dokodemo 无传输/安全层选项（矩阵外组合 400 并指明冲突字段）。
		if req.Network != "" {
			return fmt.Errorf("协议 %s 无传输层选项（protocol 与 network 冲突）", req.Protocol)
		}
		if req.Security != "" {
			return fmt.Errorf("协议 %s 无安全层选项（protocol 与 security 冲突）", req.Protocol)
		}
	}

	switch req.Protocol {
	case shared.ProtocolVLESS:
		// flow 语义：未填 + tcp + reality → 默认 vision；显式 "none" → 无 flow；其余组合必须无 flow。
		if req.Flow == "" && req.Network == shared.NetworkTCP && req.Security == shared.SecurityReality {
			req.Flow = shared.FlowVision
		}
		if req.Flow == "none" {
			req.Flow = ""
		}
		if req.Flow != "" && req.Flow != shared.FlowVision {
			return fmt.Errorf("不支持的 flow: %s", req.Flow)
		}
		if req.Flow == shared.FlowVision && req.Network != shared.NetworkTCP {
			return fmt.Errorf("flow=%s 仅适用于 tcp 传输（grpc/xhttp/ws/httpupgrade 请选择无 flow）", shared.FlowVision)
		}
		if req.Flow == shared.FlowVision && req.Security == shared.SecurityNone {
			return fmt.Errorf("flow=%s 与 security=none 冲突：vision 仅 reality/tls（§2）", shared.FlowVision)
		}
		if req.Encryption != "" {
			if !shared.ValidValue(req.Encryption, shared.VLessEncMethods) {
				return fmt.Errorf("不支持的 VLESS Encryption 认证方式: %s", req.Encryption)
			}
			// vision + Encryption 允许组合（native 拼接），客户端字符串按 1-RTT 下发（§15）。
		}
	case shared.ProtocolVMess:
		req.Flow = ""
		if req.Cipher == "" {
			req.Cipher = shared.VMessCipherAuto
		}
		if !shared.ValidValue(req.Cipher, shared.VMessCiphers) {
			return fmt.Errorf("不支持的 vmess cipher: %s", req.Cipher)
		}
	case shared.ProtocolTrojan:
		req.Flow = ""
	case shared.ProtocolShadowsocks:
		if req.Method == "" {
			req.Method = shared.SSMethod2022AES128GCM
		}
		if !shared.ValidValue(req.Method, shared.SSMethods) {
			return fmt.Errorf("不支持的 shadowsocks 加密方式: %s", req.Method)
		}
	case shared.ProtocolDokodemo:
		if req.TargetAddress == "" {
			return fmt.Errorf("dokodemo-door 需要目标地址")
		}
		if req.TargetPort == nil || *req.TargetPort < 1 || *req.TargetPort > 65535 {
			return fmt.Errorf("dokodemo-door 需要合法的目标端口（1-65535）")
		}
	}
	return nil
}

// handleCreateNode 处理 POST /api/nodes：生成虚拟配置模板 → pending → 下发 apply_node（§8 全量用户）。
func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var req createNodeRequest
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := req.normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv, err := s.st.ServerByID(r.Context(), req.ServerID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "服务器不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// ACME 证书模式：落地服务器须有域名型公网地址（§3.3 模式 B，无则 400）。
	if err := applyACMEDomain(&req, srv); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// hy2 端口跳跃：自动分配 / 显式段 NAT+冲突校验（spec §2/§3.2）。
	if err := s.resolveHy2PortHop(r.Context(), &req, srv, 0); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 受限直连 NAT 机（allowed_ports 非空）：用户指定端口必须在段内（§21，400）；
	// 留空则由 enqueueApply 把监听侧候选展开进 port_candidates 下发。
	if req.Port != nil {
		if err := checkPortInRanges(srv, *req.Port); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("端口 %d 不在该 NAT 服务器可用段内", *req.Port))
			return
		}
		if err := s.checkPortConflict(r.Context(), req.ServerID, req.Protocol, *req.Port, 0, 0); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	name, err := resolveNameTemplate(req.Name, nameTemplateValues{
		Protocol:   req.Protocol,
		Port:       req.Port,
		Servers:    []nameTemplateServer{nameServer(srv)},
		PanelShort: store.EffectivePanelShort(s.getSetting(r.Context(), store.SettingPanelShort)),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = name
	o := s.observeStart(r, "node.create", "创建节点", chainNodeObserveStages)
	defer o.Close()
	vc := buildVirtualConfig(req)
	id, err := s.applyNewNode(r, req.Name, req.ServerID, req.Port, vc)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "节点已保存")
	o.Report("publish", 100, "下发命令已入队")
	n, err := s.st.NodeByID(r.Context(), id)
	if err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("regenerate", 0, "等待订阅重生成")
	srvID, nodeID := n.ServerID, n.ID
	s.audit(r, "node.create", &srvID, &nodeID, map[string]any{
		"name": n.Name, "protocol": n.Protocol, "port": n.Port,
	})
	writeJSON(w, http.StatusCreated, toNodeDTO(*n))
}

// handleRetryNode 处理 POST /api/nodes/{id}/retry：failed 节点重新下发（§6 重试按钮）。
func (s *Server) handleRetryNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID int64 `json:"node_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.NodeID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	n, err := s.st.NodeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "节点不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var vc shared.VirtualConfig
	if err := json.Unmarshal(n.ConfigTemplate, &vc); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("节点虚拟配置损坏: %v", err))
		return
	}
	o := s.observeStart(r, "node.retry", "重试节点", chainNodeObserveStages)
	defer o.Close()
	if err := s.enqueueApply(r, n.ServerID, n.ID, vc); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "节点状态已更新")
	o.Report("publish", 100, "重发命令已入队")
	o.Report("regenerate", 0, "等待订阅重生成")
	srvID, nodeID := n.ServerID, n.ID
	s.audit(r, "node.retry", &srvID, &nodeID, nil)
	writeJSON(w, http.StatusOK, toNodeDTO(*n))
}

// handleDeleteNode 处理 DELETE /api/nodes/{id}：下发 remove_node（离线留队列补发）后删除记录。
func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID int64 `json:"node_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.NodeID
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	n, err := s.st.NodeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "节点不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	affectedUsers, err := s.st.SubscriptionUserIDsForNode(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o := s.observeStart(r, "node.delete", "删除节点", chainNodeObserveStages)
	defer o.CloseIfPending()
	if _, err := s.disp.Enqueue(r.Context(), n.ServerID, shared.TypeRemoveNode, shared.RemoveNodePayload{NodeID: n.ID}); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("publish", 100, "删除命令已下发")
	if err := s.st.DeleteNode(r.Context(), id); err != nil {
		o.Fail(err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o.Report("db", 100, "节点已删除")
	if s.subscriptions != nil {
		s.subscriptions.EnqueueUsers(affectedUsers, s.panelBase(r))
		o.WatchUsers(affectedUsers)
	}
	o.Report("regenerate", 0, "等待订阅重生成")
	// 删除后对象不存在，审计行存 protocol/port 快照留痕（§log）。
	srvID, nodeID := n.ServerID, n.ID
	s.audit(r, "node.delete", &srvID, &nodeID, map[string]any{
		"protocol": n.Protocol, "port": n.Port,
	})
	writeJSON(w, http.StatusOK, nil)
}

// applyNewNode 落库新节点并下发 apply_node，返回节点 id。
func (s *Server) applyNewNode(r *http.Request, name string, serverID int64, port *int, vc shared.VirtualConfig) (int64, error) {
	vcJSON, err := json.Marshal(vc)
	if err != nil {
		return 0, err
	}
	id, err := s.st.InsertNode(r.Context(), name, serverID, vc.Protocol, port, vcJSON)
	if err != nil {
		return 0, err
	}
	if err := s.enqueueApply(r, serverID, id, vc); err != nil {
		return 0, err
	}
	return id, nil
}

// enqueueApply 节点进入 applying 并下发 apply_node（携带分配到该节点的用户 UUID §16 与 dest 白名单 §6）。
// 受限直连 NAT 机（allowed_ports 非空）总是携带监听侧候选（§21）：
// 自动端口 → Agent 段内挑空闲；手动端口 → Agent 校验段内归属（面板已校验，双保险）。
func (s *Server) enqueueApply(r *http.Request, serverID, nodeID int64, vc shared.VirtualConfig) error {
	if err := s.st.SetNodeApplying(r.Context(), nodeID); err != nil {
		return err
	}
	uuids, err := s.st.NodeUserUUIDs(r.Context(), nodeID)
	if err != nil {
		return err
	}
	payload := shared.ApplyNodePayload{
		NodeID:         nodeID,
		Config:         vc,
		UserUUIDs:      uuids,
		DestCandidates: destCandidates,
	}
	if srv, err := s.st.ServerByID(r.Context(), serverID); err == nil {
		if ranges, err := shared.ParsePortRanges(srv.AllowedPorts); err == nil && len(ranges) > 0 {
			payload.PortCandidates = shared.ListenCandidates(ranges)
		}
	}
	_, err = s.disp.Enqueue(r.Context(), serverID, shared.TypeApplyNode, payload)
	return err
}

// destCandidates 是面板内置的 dest 白名单（§6 预检 fallback），
// 覆盖全球主要大厂网络（TLS1.3 支持好、各地理位置可达性高），随版本更新。
// 注意：官方文档警告 CDN 目标可能使服务器被当作转发器滥用，因此不推荐 Cloudflare；
// www.microsoft.com 的超大证书记录也会触发 Reality 握手限制，二者均不收录。
var destCandidates = []string{
	"dl.google.com:443",
	"www.amazon.com:443",
	"gateway.icloud.com:443",
	"developer.apple.com:443",
	"cdn.discord.com:443",
	"github.com:443",
	"www.samsung.com:443",
	"www.tesla.com:443",
	"www.bing.com:443",
	"www.yahoo.com:443",
	"slack.com:443",
	"yandex.com:443",
}

// buildVirtualConfig 生成虚拟配置（§7 参数分工：UUID/short_id/dest/serverNames 面板，
// 密钥对与自动端口由 Agent 填占位符）。模板以 map 构造后序列化，
// 占位符以字符串值形式嵌入（"{{PORT}}"/"{{CLIENTS}}"/"{{PRIVATE_KEY}}"/"{{TAG}}"）。
func buildVirtualConfig(req createNodeRequest) shared.VirtualConfig {
	settings := map[string]any{}
	switch req.Protocol {
	case shared.ProtocolVLESS:
		settings["clients"] = shared.PlaceholderClients
		if req.Encryption != "" {
			// VLESS Encryption：decryption 由 Agent 执行 `xray vlessenc` 生成填入。
			settings["decryption"] = shared.PlaceholderVLessDecryption
		} else {
			settings["decryption"] = "none"
		}
	case shared.ProtocolVMess, shared.ProtocolTrojan:
		settings["clients"] = shared.PlaceholderClients
	case shared.ProtocolShadowsocks:
		settings["method"] = req.Method
		settings["clients"] = shared.PlaceholderClients
		settings["network"] = "tcp,udp"
		if shared.Is2022Method(req.Method) {
			// 2022-blake3 多用户：inbound 需要节点级 PSK（订阅按 "PSK:用户密钥" 拼接）。
			psk, err := shared.GenerateSSKey(req.Method)
			if err != nil {
				panic(err) // crypto/rand 失败属致命异常，同 randomHex
			}
			settings["password"] = psk
		}
	case shared.ProtocolSocks:
		settings["auth"] = "password"
		settings["accounts"] = shared.PlaceholderClients
		settings["udp"] = true
	case shared.ProtocolHTTP:
		settings["accounts"] = shared.PlaceholderClients
	case shared.ProtocolDokodemo:
		settings["address"] = req.TargetAddress
		settings["port"] = *req.TargetPort
		settings["network"] = "tcp,udp"
	case shared.ProtocolHysteria2:
		settings["version"] = 2
		settings["clients"] = shared.PlaceholderClients // hy2 用户列表键为 clients（非 users，Task 1 实测）
	}

	inbound := map[string]any{
		"tag":      shared.PlaceholderTag,
		"protocol": req.Protocol,
		"port":     shared.PlaceholderPort,
		"settings": settings,
	}
	if req.Protocol == shared.ProtocolHysteria2 {
		inbound["streamSettings"] = hy2StreamSettings(req)
	} else if shared.IsRealityProtocol(req.Protocol) {
		switch req.Security {
		case shared.SecurityNone:
			inbound["streamSettings"] = plainStreamSettings(req)
		case shared.SecurityTLS:
			inbound["streamSettings"] = tlsStreamSettings(req)
		default:
			inbound["streamSettings"] = realityStreamSettings(req)
		}
	}
	if req.Protocol != shared.ProtocolDokodemo && req.Protocol != shared.ProtocolHysteria2 {
		inbound["sniffing"] = map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}}
	}
	template, _ := json.Marshal(inbound) // map 序列化不会失败

	port := 0
	if req.Port != nil {
		port = *req.Port
	}
	return shared.VirtualConfig{
		Protocol:     req.Protocol,
		Port:         port,
		Flow:         req.Flow,
		Network:      req.Network,
		Security:     req.Security,
		CertMode:     req.CertMode,
		TLSDomain:    req.TLSDomain,
		ServiceName:  req.ServiceName,
		Path:         req.Path,
		Mode:         req.Mode,
		Host:         req.Host,
		Method:       req.Method,
		Fingerprint:  req.Fingerprint,
		Encryption:   req.Encryption,
		Cipher:       req.Cipher,
		ObfsPassword: req.ObfsPassword,
		UpMbps:       req.UpMbps,
		DownMbps:     req.DownMbps,
		PortHop:      req.PortHop,
		Template:     json.RawMessage(template),
	}
}

// realityStreamSettings 构造 reality 安全层的 streamSettings（Reality 仅支持 tcp/grpc/xhttp）。
func realityStreamSettings(req createNodeRequest) map[string]any {
	ss := map[string]any{
		"network":  req.Network,
		"security": "reality",
		"realitySettings": map[string]any{
			"show":         false,
			"dest":         req.Dest,
			"xver":         0,
			"minClientVer": "0", // 26.7.11+ 缺省默认 26.3.27，会拒绝版本声明旧的客户端（mihomo/clash）
			"serverNames":  req.ServerNames,
			"privateKey":   shared.PlaceholderRealityPrivateKey,
			"shortIds":     []string{req.ShortID},
		},
	}
	for k, v := range networkSubSettings(req) {
		ss[k] = v
	}
	return ss
}

// plainStreamSettings 构造 security=none 的 streamSettings（无 realitySettings）：
// vmess 自带 AEAD；vless 由 VLESS Encryption 提供认证（spec §2 脚注 1）。
func plainStreamSettings(req createNodeRequest) map[string]any {
	ss := map[string]any{"network": req.Network, "security": "none"}
	for k, v := range networkSubSettings(req) {
		ss[k] = v
	}
	return ss
}

// networkSubSettings 返回各传输的子配置段（tcp 无子段）：
// xray 25.x 起 ws 在 wsSettings（host 在 headers.Host）、httpupgrade 在 httpupgradeSettings。
func networkSubSettings(req createNodeRequest) map[string]any {
	switch req.Network {
	case shared.NetworkGRPC:
		return map[string]any{"grpcSettings": map[string]any{"serviceName": req.ServiceName}}
	case shared.NetworkXHTTP:
		x := map[string]any{"path": req.Path, "mode": req.Mode}
		if req.Host != "" {
			x["host"] = req.Host
		}
		return map[string]any{"xhttpSettings": x}
	case shared.NetworkWS:
		w := map[string]any{"path": req.Path}
		if req.Host != "" {
			w["headers"] = map[string]any{"Host": req.Host}
		}
		return map[string]any{"wsSettings": w}
	case shared.NetworkHTTPUpgrade:
		h := map[string]any{"path": req.Path}
		if req.Host != "" {
			h["host"] = req.Host
		}
		return map[string]any{"httpupgradeSettings": h}
	}
	return nil
}

// tlsStreamSettings 构造 tls 安全层的 streamSettings（§3.2）：serverName 为 TLS 域名
// （selfsign=伪装域名 / acme=落地服务器域名），证书文件路径为占位符，由 agent 按
// CertMode 落地后替换（§3.3）；传输子段与 reality/none 共用 networkSubSettings。
func tlsStreamSettings(req createNodeRequest) map[string]any {
	ss := map[string]any{
		"network":  req.Network,
		"security": "tls",
		"tlsSettings": map[string]any{
			"serverName": req.TLSDomain,
			"certificates": []map[string]any{{
				"certificateFile": shared.PlaceholderTLSCertFile,
				"keyFile":         shared.PlaceholderTLSKeyFile,
			}},
		},
	}
	for k, v := range networkSubSettings(req) {
		ss[k] = v
	}
	return ss
}

// tlsCamouflagePool 是自签证书的伪装域名预设池（§3.3 模式 A，复用 RealityDestPicker
// 预设思路）：仅作 TLS 伪装身份（证书 CN/SAN 与客户端 SNI），不要求指向本机。
var tlsCamouflagePool = []string{
	"dl.google.com", "www.amazon.com", "gateway.icloud.com", "developer.apple.com",
	"cdn.discord.com", "github.com", "www.samsung.com", "www.tesla.com",
	"www.bing.com", "www.yahoo.com",
}

// validateTLSDomain 校验伪装域名形态：纯主机名（不含端口/路径/空白，长度 ≤253）。
func validateTLSDomain(d string) error {
	if d == "" || len(d) > 253 || strings.ContainsAny(d, "/: \t\r\n") {
		return fmt.Errorf("伪装域名不合法: %q（应为纯域名，如 www.example.com）", d)
	}
	return nil
}

// randomInt 返回 [0,n) 的均匀随机数（伪装域名池选取；拒绝采样去偏，
// crypto/rand 失败与 randomHex 同语义 panic）。
func randomInt(n int) int {
	var b [1]byte
	limit := 256 / n * n
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic(err)
		}
		if int(b[0]) < limit {
			return int(b[0]) % n
		}
	}
}

// serverDomain 返回服务器公网地址列表中的首个域名条目（无则空串）。
func serverDomain(srv *store.Server) string {
	for _, a := range store.ParseServerAddresses(srv.Addresses) {
		if shared.AddressFamily(a) == shared.AddressFamilyDomain {
			return a
		}
	}
	return ""
}

// applyACMEDomain 校验并填充 ACME 模式的 TLS 域名（§3.3 模式 B；§4 前端镜像同一判定）：
// 落地服务器公网地址须含域名条目，无则 400 并给出指向性提示；有则沿用该域名。
func applyACMEDomain(req *createNodeRequest, srv *store.Server) error {
	if req.Security != shared.SecurityTLS || req.CertMode != shared.CertModeACME {
		return nil
	}
	d := serverDomain(srv)
	if d == "" {
		return fmt.Errorf("落地服务器 %s 未设置域名，请先在服务器地址中配置域名或改用自签模式", srv.Alias)
	}
	req.TLSDomain = d
	return nil
}

// hy2StreamSettings 构造 hy2 的 streamSettings（Task 1 实测定稿）：network 恒 "hysteria"
// （缺省 tcp 会退化为 TCP 承载）；tlsSettings 带 alpn [h3]（否则握手 no application
// protocol），证书占位符复用 P3 双模式；hysteriaSettings 仅声明 version=2（用户口令在
// settings.clients，见 Task 1 事实区）；salamander 混淆与 brutal 带宽在 finalmask。
// 服务端模板不含 udpHop——udpHop 仅客户端实现，服务端段收敛走 iptables DNAT（spec §3.2）。
func hy2StreamSettings(req createNodeRequest) map[string]any {
	ss := map[string]any{
		"network":  "hysteria",
		"security": "tls",
		"tlsSettings": map[string]any{
			"serverName": req.TLSDomain,
			"alpn":       []string{"h3"},
			"certificates": []map[string]any{{
				"certificateFile": shared.PlaceholderTLSCertFile,
				"keyFile":         shared.PlaceholderTLSKeyFile,
			}},
		},
		"hysteriaSettings": map[string]any{"version": 2},
	}
	fm := map[string]any{}
	if req.ObfsPassword != "" {
		fm["udp"] = []map[string]any{{
			"type":     "salamander",
			"settings": map[string]any{"password": req.ObfsPassword},
		}}
	}
	quic := map[string]any{}
	if req.UpMbps > 0 || req.DownMbps > 0 {
		quic["congestion"] = "brutal"
		if req.UpMbps > 0 {
			quic["brutalUp"] = fmt.Sprintf("%d mbps", req.UpMbps)
		}
		if req.DownMbps > 0 {
			quic["brutalDown"] = fmt.Sprintf("%d mbps", req.DownMbps)
		}
	}
	if len(quic) > 0 {
		fm["quicParams"] = quic
	}
	if len(fm) > 0 {
		ss["finalmask"] = fm
	}
	return ss
}

// resolveHy2PortHop 落地 port_hop 三段语义的服务器侧部分（normalize 只做语法校验）：
// off（normalize 保留的哨兵）→ 归一为空 = 关闭跳跃；""（默认开）→ 按服务器空闲 udp 段
// 自动分配 32 段（避开全部已保留段与端口；NAT 机段整体落在监听侧段内，可用段不足最小
// 段长 8 时关闭跳跃——功能可用，仅失去跳跃的抗 QoS 能力，spec §2）；显式段 → NAT 落段
// 校验 + 段冲突校验。多跳链的"全跳同段"逐跳校验在链处理器（Task 4）做，这里只管落地机。
func (s *Server) resolveHy2PortHop(ctx context.Context, req *createNodeRequest, srv *store.Server, excludeChainID int64) error {
	if req.Protocol != shared.ProtocolHysteria2 {
		return nil
	}
	if req.PortHop == "off" {
		req.PortHop = ""
		return nil
	}
	ranges, err := shared.ParsePortRanges(srv.AllowedPorts)
	if err != nil {
		return err
	}
	occupants, err := s.st.PortOccupants(ctx, srv.ID)
	if err != nil {
		return err
	}
	if req.PortHop == "" {
		start, end, ok := allocUDPPortHop(occupants, ranges, shared.Hy2PortHopDefaultLen)
		if !ok {
			start, end, ok = allocUDPPortHop(occupants, ranges, shared.Hy2PortHopMinLen)
		}
		if ok {
			req.PortHop = shared.FormatPortHop(start, end)
		}
		return nil // 段不足：保持空 = 关闭跳跃（spec §2 允许降级）
	}
	start, end, _ := shared.ParsePortHop(req.PortHop)
	if len(ranges) > 0 && !shared.SpanInListenRanges(ranges, start, end) {
		return fmt.Errorf("端口跳跃段 %s 不在该 NAT 服务器可用段内", req.PortHop)
	}
	return findPortConflict(occupants, req.Protocol, start, end, excludeChainID)
}
