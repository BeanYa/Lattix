package panel

import (
	"net/http"

	"lattix/shared"
)

// handleGetObserveTask 处理 GET /api/observe-task/get：返回旁路观察进度快照。
// 404 = 观察不存在或已清理（终态保留 5 分钟）。
func (s *Server) handleGetObserveTask(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("observe_id")
	if id == "" || !shared.ValidMessageID(id) {
		writeProtocolError(w, http.StatusBadRequest, "observe_id 必须为 32 位十六进制")
		return
	}
	obs, ok := s.observes.Get(id)
	if !ok {
		writeProtocolError(w, http.StatusNotFound, "观察不存在或已清理")
		return
	}
	writeJSON(w, http.StatusOK, obs)
}
