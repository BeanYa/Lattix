package panel

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"lattix/backend/internal/store"
)

type externalSubscriptionInput struct {
	ID                  int64  `json:"id"`
	Name                string `json:"name"`
	URL                 string `json:"url"`
	UserAgent           string `json:"user_agent"`
	SkipCertVerify      bool   `json:"skip_cert_verify"`
	AutoUpdate          bool   `json:"auto_update"`
	UpdateIntervalHours int    `json:"update_interval_hours"`
}

func (s *Server) handleListExternalSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.st.ListExternalSubscriptions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, subs)
}

func (s *Server) handleCreateExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req externalSubscriptionInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UpdateIntervalHours == 0 {
		req.UpdateIntervalHours = 24
	}
	sub, err := s.extSubs.Create(r.Context(), req.Name, strings.TrimSpace(req.URL),
		req.UserAgent, req.SkipCertVerify, req.AutoUpdate, req.UpdateIntervalHours)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "external_subscription.created", nil, nil, map[string]any{
		"id": sub.ID, "name": sub.Name, "node_count": sub.NodeCount,
	})
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleUpdateExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req externalSubscriptionInput
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	if req.UpdateIntervalHours == 0 {
		req.UpdateIntervalHours = 24
	}
	sub, err := s.extSubs.Update(r.Context(), req.ID, req.Name, strings.TrimSpace(req.URL),
		req.UserAgent, req.SkipCertVerify, req.AutoUpdate, req.UpdateIntervalHours)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.audit(r, "external_subscription.updated", nil, nil, map[string]any{
		"id": sub.ID, "name": sub.Name,
	})
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleDeleteExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	if err := s.st.DeleteExternalSubscription(r.Context(), req.ID); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.audit(r, "external_subscription.deleted", nil, nil, map[string]any{"id": req.ID})
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) handleSyncExternalSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	sub, err := s.extSubs.Sync(r.Context(), req.ID)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.audit(r, "external_subscription.synced", nil, nil, map[string]any{
		"id": sub.ID, "node_count": sub.NodeCount,
	})
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleListExternalChains(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id 必须为正整数")
		return
	}
	chains, err := s.st.ListExternalChains(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chains)
}
