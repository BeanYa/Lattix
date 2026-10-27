package panel

import (
	"errors"
	"net/http"
	"strconv"

	"lattix/backend/internal/store"
)

// handleChainShareLink 处理 GET /api/chain/share-link?chain_id=&user_id=：
// 导出指定用户视角下链路的分享链接（与订阅交付一致的协议 URI，管理端调试/分发用）。
func (s *Server) handleChainShareLink(w http.ResponseWriter, r *http.Request) {
	chainID, err := strconv.ParseInt(r.URL.Query().Get("chain_id"), 10, 64)
	if err != nil || chainID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid chain_id")
		return
	}
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	if s.subscriptions == nil {
		writeError(w, http.StatusInternalServerError, "订阅服务不可用")
		return
	}
	link, err := s.subscriptions.UserChainShareLink(r.Context(), userID, chainID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "链不存在")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "chain.share_link.exported", nil, nil,
		map[string]any{"chain_id": chainID, "user_id": userID, "protocol": link.Protocol})
	writeJSON(w, http.StatusOK, link)
}

// handleNodeShareLink 处理 GET /api/node/share-link?node_id=&user_id=：
// 导出指定用户视角下单机节点的分享链接（链路出口节点不单独导出）。
func (s *Server) handleNodeShareLink(w http.ResponseWriter, r *http.Request) {
	nodeID, err := strconv.ParseInt(r.URL.Query().Get("node_id"), 10, 64)
	if err != nil || nodeID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid node_id")
		return
	}
	userID, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return
	}
	if s.subscriptions == nil {
		writeError(w, http.StatusInternalServerError, "订阅服务不可用")
		return
	}
	link, err := s.subscriptions.UserNodeShareLink(r.Context(), userID, nodeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "节点不存在")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "node.share_link.exported", nil, &nodeID,
		map[string]any{"node_id": nodeID, "user_id": userID, "protocol": link.Protocol})
	writeJSON(w, http.StatusOK, link)
}
