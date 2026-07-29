package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// userDTO 是用户对象的 API 表示。
type userDTO struct {
	ID              int64       `json:"id"`
	Name            string      `json:"name"`
	UUID            string      `json:"uuid"`
	SubToken        string      `json:"sub_token"`
	SubURL          string      `json:"sub_url"`
	SubLinksURL     string      `json:"sub_links_url"`
	NodeIDs         []int64     `json:"node_ids"`
	Traffic         *trafficDTO `json:"traffic"`
	ExpiresAt       *time.Time  `json:"expires_at"`
	Expired         bool        `json:"expired"`
	Disabled        bool        `json:"disabled"`
	TrafficLimit    int64       `json:"traffic_limit"`
	TrafficResetDay int         `json:"traffic_reset_day"`
	SubTitle        string      `json:"sub_title"`
	SubAnnouncement string      `json:"sub_announcement"`
	PlanName        string      `json:"plan_name"`
	AppURL          string      `json:"app_url"`
	CreatedAt       time.Time   `json:"created_at"`
}

func (s *Server) toUserDTO(r *http.Request, u store.User, nodeIDs []int64) userDTO {
	return userDTO{
		ID:              u.ID,
		Name:            u.Name,
		UUID:            u.UUID,
		SubToken:        u.SubToken,
		SubURL:          fmt.Sprintf("%s/sub/%s", s.panelBase(r), u.SubToken),
		SubLinksURL:     fmt.Sprintf("%s/sub/%s?format=links", s.panelBase(r), u.SubToken),
		NodeIDs:         nodeIDs,
		ExpiresAt:       u.ExpiresAt,
		Expired:         u.Expired,
		Disabled:        u.Disabled,
		TrafficLimit:    u.TrafficLimit,
		TrafficResetDay: u.TrafficResetDay,
		SubTitle:        u.SubTitle,
		SubAnnouncement: u.SubAnnouncement,
		PlanName:        u.PlanName,
		AppURL:          u.AppURL,
		CreatedAt:       u.CreatedAt,
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
// 创建时可选带 node_ids 预选产品层链路（内部仍映射业务 node_id），省略则维持默认全关（§16）。
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string  `json:"name"`
		ExpiresAt *string `json:"expires_at"` // RFC3339，省略/null = 长期
		NodeIDs   []int64 `json:"node_ids"`   // 可选：预选链路对应的业务节点
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
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

	// 可选预选链路（§16）：校验底层业务节点存在后按差量扇出 add_user。
	nodeIDs := []int64{}
	if len(req.NodeIDs) > 0 {
		nodes, err := s.st.ListNodes(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		validNodes := map[int64]bool{}
		for _, n := range nodes {
			validNodes[n.ID] = true
		}
		seen := map[int64]bool{}
		for _, nodeID := range req.NodeIDs {
			if !validNodes[nodeID] {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("链路对应节点 %d 不存在", nodeID))
				return
			}
			if !seen[nodeID] {
				seen[nodeID] = true
				nodeIDs = append(nodeIDs, nodeID)
			}
		}
		added, _, err := s.st.SetUserNodes(r.Context(), id, nodeIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.fanoutUserDiff(r.Context(), created.UUID, nodes, added, nil)
	}
	s.audit(r, "user.create", nil, nil, map[string]any{
		"user_id": created.ID, "name": created.Name, "node_count": len(nodeIDs),
	})
	writeJSON(w, http.StatusCreated, s.toUserDTO(r, *created, nodeIDs))
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

// handleUpdateUser 处理 PATCH /api/users/{id}：修改/清除有效期（§9）与显式停用/启用（§16）。
// 载荷 {"expires_at": "RFC3339" 或 null, "disabled": bool}，省略的字段保持不变；
// expires_at 传 null = 清除（长期）。与创建一致，expires_at 不允许设为过去时间（400）——
// "借到期立即停权"由 disabled 开关承担。
// 有效停权态 = disabled OR expired（§9/§16）：add_user/remove_user 扇出只在有效停权态
// 跃迁时发生——已 expired 的用户再 disable（或反之）不重复扇出；恢复需两者都解除。
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID    int64           `json:"user_id"`
		ExpiresAt json.RawMessage `json:"expires_at"` // 省略 = 不变；null = 清除（长期）
		Disabled  *bool           `json:"disabled"`   // 省略 = 不变
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.UserID
	if id <= 0 {
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
	now := time.Now()
	stoppedBefore := u.Disabled || u.Expired

	expiryTouched := len(req.ExpiresAt) > 0
	if expiryTouched {
		var expiresAt *time.Time
		if string(req.ExpiresAt) != "null" {
			var raw string
			if err := json.Unmarshal(req.ExpiresAt, &raw); err != nil {
				writeError(w, http.StatusBadRequest, "expires_at 格式无效（需 RFC3339 或 null）")
				return
			}
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "expires_at 格式无效（需 RFC3339）")
				return
			}
			expiresAt = &t
		}
		if expiresAt != nil && !expiresAt.After(now) {
			writeError(w, http.StatusBadRequest, "expires_at 不能是过去的时间")
			return
		}
		if err := s.st.SetUserExpiry(r.Context(), id, expiresAt, now); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	disabledAfter := u.Disabled
	if req.Disabled != nil && *req.Disabled != u.Disabled {
		if err := s.st.SetUserDisabled(r.Context(), id, *req.Disabled); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		disabledAfter = *req.Disabled
	}

	// 有效停权态跃迁才扇出（§9/§16）：expiry 被修改后只可能是未来/清除，expired 必已复位。
	stoppedAfter := disabledAfter || (u.Expired && !expiryTouched)
	if stoppedBefore != stoppedAfter {
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
		if stoppedAfter {
			s.fanoutUserDiff(r.Context(), u.UUID, nodes, nil, assigned)
			log.Printf("panel: user %d (%s) 已停权，扇出 remove_user (%d 节点)", id, u.Name, len(assigned))
		} else {
			s.fanoutUserDiff(r.Context(), u.UUID, nodes, assigned, nil)
			log.Printf("panel: user %d (%s) 已恢复，扇出 add_user (%d 节点)", id, u.Name, len(assigned))
		}
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
	changes := changedValues(
		map[string]any{"expires_at": logTime(u.ExpiresAt), "disabled": u.Disabled},
		map[string]any{"expires_at": logTime(updated.ExpiresAt), "disabled": updated.Disabled},
	)
	if len(changes) > 0 {
		changes["user"] = map[string]any{"id": updated.ID, "name": updated.Name}
		s.audit(r, "user.updated", nil, nil, changes)
	}
	writeJSON(w, http.StatusOK, s.toUserDTO(r, *updated, nodeIDs))
}

func logTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

// handleSetUserNodes 处理 PUT /api/users/{id}/nodes：整体替换用户的节点分配（§16），
// 按差量向相关服务器扇出 add_user / remove_user（载荷仅含受影响的节点）。
func (s *Server) handleSetUserNodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID  int64   `json:"user_id"`
		NodeIDs []int64 `json:"node_ids"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.UserID
	if id <= 0 {
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
	beforeNodeIDs, err := s.st.UserNodeIDs(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	added, removed, err := s.st.SetUserNodes(r.Context(), id, req.NodeIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.fanoutUserDiff(r.Context(), u.UUID, nodes, added, removed)
	if len(added) > 0 || len(removed) > 0 {
		s.audit(r, "user.nodes_updated", nil, nil, map[string]any{
			"user":     map[string]any{"id": u.ID, "name": u.Name},
			"node_ids": map[string]any{"before": beforeNodeIDs, "after": req.NodeIDs},
		})
	}
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
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.UserID
	if id <= 0 {
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
	// 删除后对象不存在，审计行存 name 快照留痕（§log）。
	s.audit(r, "user.delete", nil, nil, map[string]any{"user_id": u.ID, "name": u.Name})
	writeJSON(w, http.StatusOK, nil)
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

// handleUpdateUserSubSettings 处理 POST /api/user/sub-settings：更新用户级订阅配置。
func (s *Server) handleUpdateUserSubSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID          int64  `json:"user_id"`
		TrafficLimit    int64  `json:"traffic_limit"`     // 字节，0=不限
		TrafficResetDay int    `json:"traffic_reset_day"` // 0=创建日，1-28
		SubTitle        string `json:"sub_title"`
		SubAnnouncement string `json:"sub_announcement"`
		PlanName        string `json:"plan_name"` // 套餐名（空=用全局）
		AppURL          string `json:"app_url"`   // 客户端跳转链接（空=用全局）
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if req.TrafficLimit < 0 {
		req.TrafficLimit = 0
	}
	if req.TrafficResetDay < 0 || req.TrafficResetDay > 28 {
		writeError(w, http.StatusBadRequest, "重置日须为 0–28（0=创建日）")
		return
	}
	if err := s.st.SetUserSubSettings(r.Context(), req.UserID, req.TrafficLimit, req.TrafficResetDay, req.SubTitle, req.SubAnnouncement, strings.TrimSpace(req.PlanName), strings.TrimSpace(req.AppURL)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "user.sub_settings.updated", nil, nil, map[string]any{
		"user_id":           req.UserID,
		"traffic_limit":     req.TrafficLimit,
		"traffic_reset_day": req.TrafficResetDay,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleUserTrafficHistory 处理 GET /api/user/traffic-history?user_id=N。
func (s *Server) handleUserTrafficHistory(w http.ResponseWriter, r *http.Request) {
	idParam := r.URL.Query().Get("user_id")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user_id")
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
	history, err := s.st.ListUserTrafficHistory(r.Context(), u.UUID, 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, history)
}
