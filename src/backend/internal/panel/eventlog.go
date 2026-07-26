package panel

import (
	"net/http"
	"strconv"

	"lattix/backend/internal/store"
)

// eventLogDTO 是 event_log 表一行的对外结构；可空字段用指针表示，JSON 序列化为 null。
type eventLogDTO struct {
	ID       int64              `json:"id"`
	Ts       string             `json:"ts"`
	Category string             `json:"category"`
	Action   string             `json:"action"`
	ServerID *int64             `json:"server_id,omitempty"`
	Server   string             `json:"server,omitempty"` // alias，便于 UI 直接展示
	NodeID   *int64             `json:"node_id,omitempty"`
	Detail   string             `json:"detail"` // JSON 串；UI 按需 parse
	Operator string             `json:"operator,omitempty"`
	IP       string             `json:"ip,omitempty"`
}

// eventLogPage 是分页查询结果。
type eventLogPage struct {
	Items []eventLogDTO `json:"items"`
	Total int           `json:"total"`
}

// handleListEventLog 处理 GET /api/event-log（§log 日志审查页面）。
// 查询参数：category、server_id、operator、q（action/detail 模糊）、limit（默认 50，上限 200）、offset。
func (s *Server) handleListEventLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	var f store.EventFilter
	f.Category = q.Get("category")
	f.Operator = q.Get("operator")
	f.Q = q.Get("q")
	if raw := q.Get("server_id"); raw != "" {
		if sid, err := strconv.ParseInt(raw, 10, 64); err == nil {
			f.ServerID = &sid
		}
	}

	items, total, err := s.st.ListEvents(r.Context(), f, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 一次性拉取服务器列表构建 id→alias 映射，供 UI 直接展示服务器名（避免逐条查询）。
	aliasByID := map[int64]string{}
	if servers, err := s.st.ListServers(r.Context()); err == nil {
		for _, srv := range servers {
			aliasByID[srv.ID] = srv.Alias
		}
	}

	dtos := make([]eventLogDTO, 0, len(items))
	for _, e := range items {
		dto := eventLogDTO{
			ID:       e.ID,
			Ts:       e.Ts,
			Category: e.Category,
			Action:   e.Action,
			Detail:   e.Detail,
			Operator: e.Operator,
			IP:       e.IP,
		}
		if e.ServerID.Valid {
			sid := e.ServerID.Int64
			dto.ServerID = &sid
			dto.Server = aliasByID[sid]
		}
		if e.NodeID.Valid {
			nid := e.NodeID.Int64
			dto.NodeID = &nid
		}
		dtos = append(dtos, dto)
	}
	writeJSON(w, http.StatusOK, eventLogPage{Items: dtos, Total: total})
}
