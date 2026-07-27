package panel

import (
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
// short_id/dest/server_names/fingerprint/network 及 grpc/xhttp 子选项；flow 仅 vless+tcp；
// method 仅 shadowsocks；target_address/target_port 仅 dokodemo-door。
type createNodeRequest struct {
	Name          string   `json:"name"`
	ServerID      int64    `json:"server_id"`
	Protocol      string   `json:"protocol"`       // 默认 vless
	Port          *int     `json:"port"`           // 留空 = Agent 自动挑选
	ShortID       string   `json:"short_id"`       // 默认随机 8 字节 hex
	Dest          string   `json:"dest"`           // 默认 dl.google.com:443
	ServerNames   []string `json:"server_names"`   // 默认 [dl.google.com]
	Fingerprint   string   `json:"fingerprint"`    // 默认 chrome
	Network       string   `json:"network"`        // tcp（默认）/ grpc / xhttp
	ServiceName   string   `json:"service_name"`   // grpc，默认 "grpc"
	Path          string   `json:"path"`           // xhttp，默认 "/"
	Mode          string   `json:"mode"`           // xhttp，默认 auto
	Host          string   `json:"host"`           // xhttp，可空
	Flow          string   `json:"flow"`           // vless 默认 xtls-rprx-vision（仅 tcp）
	Encryption    string   `json:"encryption"`     // vless：VLESS Encryption 认证方式（x25519/mlkem768），可与 flow 组合（§15）
	Method        string   `json:"method"`         // shadowsocks，默认 2022-blake3-aes-128-gcm
	TargetAddress string   `json:"target_address"` // dokodemo-door 转发目标
	TargetPort    *int     `json:"target_port"`
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

	if shared.IsRealityProtocol(req.Protocol) {
		if req.Network == "" {
			req.Network = shared.NetworkTCP
		}
		if !shared.ValidValue(req.Network, shared.Networks) {
			return fmt.Errorf("Reality 仅支持 tcp/grpc/xhttp 传输，不支持: %s", req.Network)
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
		default: // tcp
			req.ServiceName, req.Path, req.Mode, req.Host = "", "", "", ""
		}
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
	}

	switch req.Protocol {
	case shared.ProtocolVLESS:
		// flow 语义：未填 + tcp → 默认 vision；显式 "none" → 无 flow；grpc/xhttp 必须无 flow。
		if req.Flow == "" && req.Network == shared.NetworkTCP {
			req.Flow = shared.FlowVision
		}
		if req.Flow == "none" {
			req.Flow = ""
		}
		if req.Flow != "" && req.Flow != shared.FlowVision {
			return fmt.Errorf("不支持的 flow: %s", req.Flow)
		}
		if req.Flow == shared.FlowVision && req.Network != shared.NetworkTCP {
			return fmt.Errorf("flow=%s 仅适用于 tcp 传输（grpc/xhttp 请选择无 flow）", shared.FlowVision)
		}
		if req.Encryption != "" {
			if !shared.ValidValue(req.Encryption, shared.VLessEncMethods) {
				return fmt.Errorf("不支持的 VLESS Encryption 认证方式: %s", req.Encryption)
			}
			// vision + Encryption 允许组合（native 拼接），客户端字符串按 1-RTT 下发（§15）。
		}
	case shared.ProtocolVMess, shared.ProtocolTrojan:
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
	// 受限直连 NAT 机（allowed_ports 非空）：用户指定端口必须在段内（§21，400）；
	// 留空则由 enqueueApply 把监听侧候选展开进 port_candidates 下发。
	if req.Port != nil {
		if err := checkPortInRanges(srv, *req.Port); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("端口 %d 不在该 NAT 服务器可用段内", *req.Port))
			return
		}
	}
	name, err := resolveNameTemplate(req.Name, nameTemplateValues{
		Protocol: req.Protocol,
		Port:     req.Port,
		Servers:  []nameTemplateServer{nameServer(srv)},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = name
	vc := buildVirtualConfig(req)
	id, err := s.applyNewNode(r, req.Name, req.ServerID, req.Port, vc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := s.st.NodeByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
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
	if err := s.enqueueApply(r, n.ServerID, n.ID, vc); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
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
	if _, err := s.disp.Enqueue(r.Context(), n.ServerID, shared.TypeRemoveNode, shared.RemoveNodePayload{NodeID: n.ID}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.st.DeleteNode(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
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
// 受限直连 NAT 机（allowed_ports 非空）自动端口时把监听侧候选展开进 port_candidates（§21）。
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
	if vc.Port == 0 {
		if srv, err := s.st.ServerByID(r.Context(), serverID); err == nil {
			if ranges, err := shared.ParsePortRanges(srv.AllowedPorts); err == nil && len(ranges) > 0 {
				payload.PortCandidates = shared.ListenCandidates(ranges)
			}
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
	}

	inbound := map[string]any{
		"tag":      shared.PlaceholderTag,
		"protocol": req.Protocol,
		"port":     shared.PlaceholderPort,
		"settings": settings,
	}
	if shared.IsRealityProtocol(req.Protocol) {
		inbound["streamSettings"] = realityStreamSettings(req)
	}
	if req.Protocol != shared.ProtocolDokodemo {
		inbound["sniffing"] = map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}}
	}
	template, _ := json.Marshal(inbound) // map 序列化不会失败

	port := 0
	if req.Port != nil {
		port = *req.Port
	}
	return shared.VirtualConfig{
		Protocol:    req.Protocol,
		Port:        port,
		Flow:        req.Flow,
		Network:     req.Network,
		ServiceName: req.ServiceName,
		Path:        req.Path,
		Mode:        req.Mode,
		Host:        req.Host,
		Method:      req.Method,
		Fingerprint: req.Fingerprint,
		Encryption:  req.Encryption,
		Template:    json.RawMessage(template),
	}
}

// realityStreamSettings 构造 reality 系协议共用的 streamSettings（Reality 仅支持 tcp/grpc/xhttp）。
func realityStreamSettings(req createNodeRequest) map[string]any {
	ss := map[string]any{
		"network":  req.Network,
		"security": "reality",
		"realitySettings": map[string]any{
			"show":        false,
			"dest":        req.Dest,
			"xver":        0,
			"serverNames": req.ServerNames,
			"privateKey":  shared.PlaceholderRealityPrivateKey,
			"shortIds":    []string{req.ShortID},
		},
	}
	switch req.Network {
	case shared.NetworkGRPC:
		ss["grpcSettings"] = map[string]any{"serviceName": req.ServiceName}
	case shared.NetworkXHTTP:
		x := map[string]any{"path": req.Path, "mode": req.Mode}
		if req.Host != "" {
			x["host"] = req.Host
		}
		ss["xhttpSettings"] = x
	}
	return ss
}
