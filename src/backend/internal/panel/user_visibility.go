package panel

import (
	"errors"
	"net/http"
	"strconv"

	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

func (s *Server) visibilityUser(w http.ResponseWriter, r *http.Request, id int64) *store.User {
	w.Header().Set("Cache-Control", "no-store")
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "请选择有效的用户")
		return nil
	}
	if s.subscriptions == nil {
		writeError(w, http.StatusServiceUnavailable, "订阅服务暂不可用")
		return nil
	}
	user, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		writeUserVisibilityError(w, err)
		return nil
	}
	return user
}

func (s *Server) handleUserSubscriptionVisibility(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "请选择有效的用户")
		return
	}
	user := s.visibilityUser(w, r, id)
	if user == nil {
		return
	}
	result, err := s.subscriptions.SubscriptionVisibility(r, user)
	if err != nil {
		writeUserVisibilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleUpdateUserSubscriptionVisibility(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID    int64    `json:"user_id"`
		HiddenIDs []string `json:"hidden_ids"`
	}
	if err := readJSON(r, &input); err != nil {
		writeProtocolError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.HiddenIDs == nil {
		writeError(w, http.StatusBadRequest, "请提供要隐藏的订阅项列表")
		return
	}
	user := s.visibilityUser(w, r, input.UserID)
	if user == nil {
		return
	}
	job, err := s.subscriptions.UpdateSubscriptionVisibility(r, user, input.HiddenIDs)
	if err != nil {
		writeUserVisibilityError(w, err)
		return
	}
	s.audit(r, "user.subscription_visibility", nil, nil, map[string]any{"user_id": user.ID})
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleUserSubscriptionPublicationJobs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "请选择有效的用户")
		return
	}
	user := s.visibilityUser(w, r, id)
	if user == nil {
		return
	}
	jobs, err := s.st.SubscriptionPublicationJobs(r.Context(), user.ID)
	if err != nil {
		writeUserVisibilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func writeUserVisibilityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sub.ErrInvalidSubscriptionVisibility):
		writeError(w, http.StatusBadRequest, "可见性设置无效，请刷新后重试")
	case errors.Is(err, store.ErrSubscriptionItemUnavailable):
		writeError(w, http.StatusBadRequest, "订阅项已不可用，请刷新后重试")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "用户不存在")
	case errors.Is(err, store.ErrSubscriptionPublicationQueueFull):
		writeProtocolError(w, http.StatusTooManyRequests, "订阅发布队列已满，请稍后重试")
	default:
		writeError(w, http.StatusInternalServerError, "暂时无法读取或保存订阅可见性")
	}
}
