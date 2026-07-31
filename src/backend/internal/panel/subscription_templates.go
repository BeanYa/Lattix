package panel

import (
	"errors"
	"net/http"
	"strings"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

func (s *Server) handleSubscriptionCategories(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, sub.Categories())
}

func (s *Server) handleSubscriptionTemplates(w http.ResponseWriter, r *http.Request) {
	if s.subscriptions == nil {
		writeError(w, http.StatusServiceUnavailable, "subscription service is unavailable")
		return
	}
	templates, err := s.subscriptions.Templates(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) handleSaveSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		SourceURL string `json:"source_url"`
		Content   string `json:"content"`
		License   string `json:"license"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	template, err := s.subscriptions.SaveTemplate(r.Context(), store.SubscriptionTemplate{
		ID: strings.TrimSpace(req.ID), Name: req.Name, Kind: req.Kind,
		SourceURL: req.SourceURL, Content: req.Content, License: strings.TrimSpace(req.License),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "subscription.template.saved", nil, nil, map[string]any{
		"template_id": template.ID, "name": template.Name, "kind": template.Kind,
	})
	writeJSON(w, http.StatusOK, template)
}

func (s *Server) handleCloneSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	template, err := s.subscriptions.CloneTemplate(r.Context(), req.ID, req.Name)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, template)
}

func (s *Server) handleDeleteSubscriptionTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.subscriptions.DeleteTemplate(r.Context(), req.ID); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.audit(r, "subscription.template.deleted", nil, nil, map[string]any{"template_id": req.ID})
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) handleRefreshSubscriptionTemplates(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.subscriptions.RefreshTemplates(r.Context(), strings.TrimSpace(req.ID)); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.handleSubscriptionTemplates(w, r)
}
