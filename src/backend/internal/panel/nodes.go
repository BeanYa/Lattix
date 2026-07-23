package panel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// nodeDTO 是节点对象的 API 表示。
type nodeDTO struct {
	ID             int64           `json:"id"`
	ServerID       int64           `json:"server_id"`
	ServerAlias    string          `json:"server_alias"`
	Protocol       string          `json:"protocol"`
	Port           *int            `json:"port"` // null = Agent 自动挑选（§7）
	Status         string          `json:"status"`
	Error          string          `json:"error"`
	ConfigTemplate json.RawMessage `json:"config_template"`
	RealizedConfig json.RawMessage `json:"realized_config"`
	CreatedAt      time.Time       `json:"created_at"`
}

func toNodeDTO(n store.Node) nodeDTO {
	return nodeDTO{
		ID:             n.ID,
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
	out := make([]nodeDTO, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, toNodeDTO(n))
	}
	writeJSON(w, http.StatusOK, out)
}

// createNodeRequest 是节点创建向导的提交（§10）：端口可空 = 自动（§7）。
type createNodeRequest struct {
	ServerID    int64    `json:"server_id"`
	Port        *int     `json:"port"`
	ShortID     string   `json:"short_id"`     // 默认随机 8 字节 hex
	Dest        string   `json:"dest"`         // 默认 www.microsoft.com:443
	ServerNames []string `json:"server_names"` // 默认 [www.microsoft.com]
}

// handleCreateNode 处理 POST /api/nodes：生成虚拟配置模板 → pending → 下发 apply_node（§8 全量用户）。
func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var req createNodeRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, err := s.st.ServerByID(r.Context(), req.ServerID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "服务器不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	vc := buildVirtualConfig(req)
	id, err := s.applyNewNode(r, req.ServerID, req.Port, vc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := s.st.NodeByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toNodeDTO(*n))
}

// handleRetryNode 处理 POST /api/nodes/{id}/retry：failed 节点重新下发（§6 重试按钮）。
func (s *Server) handleRetryNode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
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
	writeJSON(w, http.StatusOK, toNodeDTO(*n))
}

// handleDeleteNode 处理 DELETE /api/nodes/{id}：下发 remove_node（离线留队列补发）后删除记录。
func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
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
	w.WriteHeader(http.StatusNoContent)
}

// applyNewNode 落库新节点并下发 apply_node，返回节点 id。
func (s *Server) applyNewNode(r *http.Request, serverID int64, port *int, vc shared.VirtualConfig) (int64, error) {
	vcJSON, err := json.Marshal(vc)
	if err != nil {
		return 0, err
	}
	id, err := s.st.InsertNode(r.Context(), serverID, port, vcJSON)
	if err != nil {
		return 0, err
	}
	if err := s.enqueueApply(r, serverID, id, vc); err != nil {
		return 0, err
	}
	return id, nil
}

// enqueueApply 节点进入 applying 并下发 apply_node（携带全量用户 UUID，§8）。
func (s *Server) enqueueApply(r *http.Request, serverID, nodeID int64, vc shared.VirtualConfig) error {
	if err := s.st.SetNodeApplying(r.Context(), nodeID); err != nil {
		return err
	}
	uuids, err := s.st.AllUserUUIDs(r.Context())
	if err != nil {
		return err
	}
	_, err = s.disp.Enqueue(r.Context(), serverID, shared.TypeApplyNode, shared.ApplyNodePayload{
		NodeID:    nodeID,
		Config:    vc,
		UserUUIDs: uuids,
	})
	return err
}

// buildVirtualConfig 生成 VLESS+Reality 虚拟配置（§7 参数分工：UUID/short_id/dest/serverNames 面板，
// 密钥对与自动端口由 Agent 填占位符；flow/uTLS 固定，§1）。
func buildVirtualConfig(req createNodeRequest) shared.VirtualConfig {
	shortID := req.ShortID
	if shortID == "" {
		shortID = randomHex(8)
	}
	dest := req.Dest
	if dest == "" {
		dest = "dl.google.com:443"
	}
	serverNames := req.ServerNames
	if len(serverNames) == 0 {
		serverNames = []string{"dl.google.com"}
	}
	names, _ := json.Marshal(serverNames)
	template := fmt.Sprintf(`{
  "tag": %q,
  "protocol": "vless",
  "port": %q,
  "settings": {
    "clients": %q,
    "decryption": "none"
  },
  "streamSettings": {
    "network": "tcp",
    "security": "reality",
    "realitySettings": {
      "show": false,
      "dest": %q,
      "xver": 0,
      "serverNames": %s,
      "privateKey": %q,
      "shortIds": [%q]
    }
  },
  "sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"]}
}`, shared.PlaceholderTag, shared.PlaceholderPort, shared.PlaceholderClients,
		dest, names, shared.PlaceholderRealityPrivateKey, shortID)
	port := 0
	if req.Port != nil {
		port = *req.Port
	}
	return shared.VirtualConfig{
		Protocol: shared.ProtocolVLESS,
		Port:     port,
		Template: json.RawMessage(template),
	}
}
