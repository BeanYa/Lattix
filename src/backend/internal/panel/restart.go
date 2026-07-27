package panel

import (
	"net/http"

	"lattix/shared"
)

// handleRestart registers restart intent synchronously. The process lifecycle
// owner performs drain and exit after this RPC response has been written.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if err := readJSON(r, &struct{}{}); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if s.cfg.RequestRestart == nil {
		writeRPC(w, shared.CodeServiceUnavailable, "restart coordinator unavailable", nil)
		return
	}
	if err := s.cfg.RequestRestart("manual"); err != nil {
		writeRPC(w, shared.CodeOperationLocked, "restart already requested", nil)
		return
	}
	s.audit(r, "panel.restart_requested", nil, nil, nil)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
}
