package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// userDTO 是用户对象的 API 表示。
type userDTO struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	UUID        string      `json:"uuid"`
	SubToken    string      `json:"sub_token"`
	SubURL      string      `json:"sub_url"`       // mihomo YAML 订阅链接（§9，浏览器打开为落地页）
	SubLinksURL string      `json:"sub_links_url"` // 分享链接集合订阅（§14）
	NodeIDs     []int64     `json:"node_ids"`      // 分配到的节点（§16，默认全关）
	Traffic     *trafficDTO `json:"traffic"`       // 用户流量合计（§13），无数据为 null
	ExpiresAt   *time.Time  `json:"expires_at"`    // 到期时刻（§9），null = 长期
	Expired     bool        `json:"expired"`       // 已到期停权（sweeper 扇出 remove_user 后置位，§9）
	CreatedAt   time.Time   `json:"created_at"`
}

func (s *Server) toUserDTO(r *http.Request, u store.User, nodeIDs []int64) userDTO {
	return userDTO{
		ID:          u.ID,
		Name:        u.Name,
		UUID:        u.UUID,
		SubToken:    u.SubToken,
		SubURL:      fmt.Sprintf("%s/sub/%s", s.panelBase(r), u.SubToken),
		SubLinksURL: fmt.Sprintf("%s/sub/%s/links", s.panelBase(r), u.SubToken),
		NodeIDs:     nodeIDs,
		ExpiresAt:   u.ExpiresAt,
		Expired:     u.Expired,
		CreatedAt:   u.CreatedAt,
	}
}

// handleListUsers 处理 GET /api/users。
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	traffic, err := s.st.TrafficByUser(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		nodeIDs, err := s.st.UserNodeIDs(r.Context(), u.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		dto := s.toUserDTO(r, u, nodeIDs)
		if t, ok := traffic[u.UUID]; ok {
			dto.Traffic = &trafficDTO{Up: t.Up, Down: t.Down}
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateUser 处理 POST /api/users：生成 UUID 与 sub_token；可带 expires_at（RFC3339，§9）。
// 新用户默认全关（§16）：不分配任何节点，不下发 add_user；
// 管理员经 PUT /api/users/{id}/nodes 分配后才增量扇出。
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string  `json:"name"`
		ExpiresAt *string `json:"expires_at"` // RFC3339，省略/null = 长期
	}
	if err := readJSON(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name 不能为空")
		return
	}
	expiresAt, err := parseExpiresAt(req.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "expires_at 不能是过去的时间")
		return
	}
	u := store.User{
		Name:     req.Name,
		UUID:     uuid.NewString(),
		SubToken: randomHex(16),
	}
	id, err := s.st.InsertUser(r.Context(), u.Name, u.UUID, u.SubToken, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	created, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.toUserDTO(r, *created, []int64{}))
}

// parseExpiresAt 解析 RFC3339 有效期；nil/空串 = 长期。
func parseExpiresAt(raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, fmt.Errorf("expires_at 格式无效（需 RFC3339）")
	}
	return &t, nil
}

// handleUpdateUser 处理 PATCH /api/users/{id}：修改/清除有效期（§9）。
// 载荷 {"expires_at": "RFC3339" 或 null}；延长/清除使已停权用户恢复（expired 1→0），
// 对其已分配节点扇出 add_user；设为过去时间则留给 sweeper 在下一周期停权。
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req struct {
		ExpiresAt *string `json:"expires_at"` // null/省略 = 清除（长期）
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	expiresAt, err := parseExpiresAt(req.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now()
	if err := s.st.SetUserExpiry(r.Context(), id, expiresAt, now); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 恢复已停权用户：对其全部分配节点扇出 add_user。
	if u.Expired && (expiresAt == nil || expiresAt.After(now)) {
		nodes, err := s.st.ListNodes(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		assigned, err := s.st.UserNodeIDs(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.fanoutUserDiff(r.Context(), u.UUID, nodes, assigned, nil)
		log.Printf("panel: user %d (%s) 有效期恢复，扇出 add_user (%d 节点)", id, u.Name, len(assigned))
	}
	updated, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodeIDs, err := s.st.UserNodeIDs(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.toUserDTO(r, *updated, nodeIDs))
}

// handleSetUserNodes 处理 PUT /api/users/{id}/nodes：整体替换用户的节点分配（§16），
// 按差量向相关服务器扇出 add_user / remove_user（载荷仅含受影响的节点）。
func (s *Server) handleSetUserNodes(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req struct {
		NodeIDs []int64 `json:"node_ids"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	valid := map[int64]bool{}
	for _, n := range nodes {
		valid[n.ID] = true
	}
	for _, nid := range req.NodeIDs {
		if !valid[nid] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("节点 %d 不存在", nid))
			return
		}
	}
	added, removed, err := s.st.SetUserNodes(r.Context(), id, req.NodeIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.fanoutUserDiff(r.Context(), u.UUID, nodes, added, removed)
	writeJSON(w, http.StatusOK, map[string]any{"node_ids": req.NodeIDs})
}

// fanoutUserDiff 按分配差量扇出：新增节点 → add_user（仅含这些节点），
// 移除节点 → remove_user（仅含这些节点）。
func (s *Server) fanoutUserDiff(ctx context.Context, uuid string, nodes []store.Node, added, removed []int64) {
	inSet := func(id int64, set []int64) bool {
		for _, x := range set {
			if x == id {
				return true
			}
		}
		return false
	}
	var addNodes, removeNodes []store.Node
	for _, n := range nodes {
		if inSet(n.ID, added) {
			addNodes = append(addNodes, n)
		}
		if inSet(n.ID, removed) {
			removeNodes = append(removeNodes, n)
		}
	}
	for srvID, params := range nodeParamsByServer(addNodes) {
		if _, err := s.disp.Enqueue(ctx, srvID, shared.TypeAddUser,
			shared.AddUserPayload{UUID: uuid, Nodes: params}); err != nil {
			log.Printf("panel: fanout add_user user=%s server=%d: %v", uuid, srvID, err)
		}
	}
	for srvID, params := range nodeParamsByServer(removeNodes) {
		if _, err := s.disp.Enqueue(ctx, srvID, shared.TypeRemoveUser,
			shared.RemoveUserPayload{UUID: uuid, Nodes: params}); err != nil {
			log.Printf("panel: fanout remove_user user=%s server=%d: %v", uuid, srvID, err)
		}
	}
}

// handleDeleteUser 处理 DELETE /api/users/{id}：扇出 remove_user 后删除（§8）。
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.fanoutRemoveUser(r, u.UUID)
	if err := s.st.DeleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nodeParamsByServer 按服务器分组节点的用户条目参数（dokodemo 节点无用户概念，排除）。
func nodeParamsByServer(nodes []store.Node) map[int64]map[string]shared.UserNodeParams {
	byServer := map[int64]map[string]shared.UserNodeParams{}
	for _, n := range nodes {
		var vc shared.VirtualConfig
		if err := json.Unmarshal(n.ConfigTemplate, &vc); err != nil {
			continue // 模板损坏的节点跳过（异常留在 nodes 表）
		}
		if !shared.HasUserList(vc.Protocol) {
			continue
		}
		m := byServer[n.ServerID]
		if m == nil {
			m = map[string]shared.UserNodeParams{}
			byServer[n.ServerID] = m
		}
		m[shared.NodeTag(n.ID)] = shared.UserNodeParams{Protocol: vc.Protocol, Flow: vc.Flow, Method: vc.Method}
	}
	return byServer
}

// fanoutRemoveUser 扇出 remove_user（删除用户时调用）：向有用户节点的服务器下发，
// Agent 按 email/user 从各 inbound 幂等移除，未分配过该用户的节点为 no-op；
// 无用户节点的服务器跳过（Agent 要求 nodes 非空，空载荷只会回执错误）。
func (s *Server) fanoutRemoveUser(r *http.Request, uuid string) {
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		log.Printf("panel: fanout remove_user user=%s: list nodes: %v", uuid, err)
		return
	}
	byServer := nodeParamsByServer(nodes)
	servers, err := s.st.ListServers(r.Context())
	if err != nil {
		log.Printf("panel: fanout remove_user user=%s: list servers: %v", uuid, err)
		return
	}
	for _, srv := range servers {
		params := byServer[srv.ID]
		if len(params) == 0 {
			continue
		}
		payload := shared.RemoveUserPayload{UUID: uuid, Nodes: params}
		if _, err := s.disp.Enqueue(r.Context(), srv.ID, shared.TypeRemoveUser, payload); err != nil {
			log.Printf("panel: fanout remove_user user=%s server=%d: %v", uuid, srv.ID, err)
		}
	}
}
