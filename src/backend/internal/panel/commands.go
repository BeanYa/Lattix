package panel

import (
	"errors"
	"net/http"
	"strconv"

	"lattix/backend/internal/store"
)

// commandDTO 是 GET /api/servers/{id}/commands 的响应条目（操作日志，§4）。
// 不含 payload（可能携带 token 等敏感字段），仅暴露状态与失败原因。
type commandDTO struct {
	ID        int64  `json:"id"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
	Type      string `json:"type"`
	Status    string `json:"status"` // queued/sent/acked/failed
	Error     string `json:"error,omitempty"`
	Attempts  int    `json:"attempts"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// handleListCommands 处理 GET /api/servers/{id}/commands?limit=50：
// 返回该服务器最近的命令记录（升级失败等场景排查用）。
func (s *Server) handleListCommands(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("server_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	if _, err := s.st.ServerByID(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 200 {
			writeError(w, http.StatusBadRequest, "limit 须为 1–200")
			return
		}
		limit = n
	}
	cmds, err := s.st.RecentCommands(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]commandDTO, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, commandDTO{
			ID:        c.ID,
			RequestID: c.RequestID,
			TraceID:   c.TraceID,
			Type:      c.Type,
			Status:    c.Status,
			Error:     c.Error,
			Attempts:  c.Attempts,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
