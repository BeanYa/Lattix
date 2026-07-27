package panel

import (
	"net/http"
	"strconv"
	"time"

	"lattix/backend/internal/logging"
)

type operationLogDTO struct {
	ID        int64            `json:"id"`
	EventID   string           `json:"event_id"`
	Timestamp string           `json:"timestamp"`
	Severity  logging.Severity `json:"severity"`
	Category  logging.Category `json:"category"`
	Action    string           `json:"action"`
	ServerID  *int64           `json:"server_id,omitempty"`
	Server    string           `json:"server,omitempty"`
	NodeID    *int64           `json:"node_id,omitempty"`
	Detail    string           `json:"detail"`
	Operator  string           `json:"operator,omitempty"`
	IP        string           `json:"ip,omitempty"`
	RequestID string           `json:"request_id,omitempty"`
}

type operationLogPage struct {
	Items []operationLogDTO `json:"items"`
	Total int               `json:"total"`
}

type requestLogPage struct {
	Items  []logging.RequestEntry   `json:"items"`
	Status logging.RequestLogStatus `json:"status"`
}

func (s *Server) handleListOperationLog(w http.ResponseWriter, r *http.Request) {
	if s.opLog == nil {
		writeError(w, http.StatusServiceUnavailable, "操作日志未启用")
		return
	}
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset, _ := strconv.Atoi(query.Get("offset"))
	filter := logging.OperationFilter{
		Severity: logging.Severity(query.Get("severity")),
		Category: logging.Category(query.Get("category")),
		Operator: query.Get("operator"),
		Query:    query.Get("q"),
		From:     query.Get("from"),
		To:       query.Get("to"),
	}
	if raw := query.Get("server_id"); raw != "" {
		if serverID, err := strconv.ParseInt(raw, 10, 64); err == nil {
			filter.ServerID = &serverID
		}
	}
	items, total, err := s.opLog.List(r.Context(), filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	aliasByID := map[int64]string{}
	if servers, err := s.st.ListServers(r.Context()); err == nil {
		for _, server := range servers {
			aliasByID[server.ID] = server.Alias
		}
	}
	result := make([]operationLogDTO, 0, len(items))
	for _, item := range items {
		dto := operationLogDTO{
			ID: item.ID, EventID: item.EventID, Timestamp: item.Timestamp.Format(time.RFC3339Nano),
			Severity: item.Severity, Category: item.Category, Action: item.Action,
			ServerID: item.ServerID, NodeID: item.NodeID, Detail: item.Detail,
			Operator: item.Operator, IP: item.IP, RequestID: item.RequestID,
		}
		if item.ServerID != nil {
			dto.Server = aliasByID[*item.ServerID]
		}
		result = append(result, dto)
	}
	writeJSON(w, http.StatusOK, operationLogPage{Items: result, Total: total})
}

func (s *Server) handleListRequestLog(w http.ResponseWriter, r *http.Request) {
	if s.reqLog == nil {
		writeError(w, http.StatusServiceUnavailable, "请求日志未启用")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, status, err := s.reqLog.Tail(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, requestLogPage{Items: items, Status: status})
}

func (s *Server) handleClearOperationLog(w http.ResponseWriter, r *http.Request) {
	if s.opLog == nil {
		writeError(w, http.StatusServiceUnavailable, "操作日志未启用")
		return
	}
	operator, _ := s.currentUser(r)
	_, total, _ := s.opLog.List(r.Context(), logging.OperationFilter{}, 1, 0)
	if err := s.opLog.Clear(r.Context(), logging.OperationEvent{
		Operator: operator, IP: logging.ClientIP(r), RequestID: logging.RequestID(r.Context()),
		Detail: map[string]int{"removed": total},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClearRequestLog(w http.ResponseWriter, r *http.Request) {
	if s.reqLog == nil {
		writeError(w, http.StatusServiceUnavailable, "请求日志未启用")
		return
	}
	status, _ := s.reqLog.Status(r.Context())
	if err := s.reqLog.Clear(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "request_log.cleared", nil, nil, map[string]any{
		"removed_bytes": status.UsageBytes,
		"segments":      status.SegmentCount,
	})
	w.WriteHeader(http.StatusNoContent)
}
