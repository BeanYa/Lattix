package sub

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lattix/backend/internal/store"
)

var ErrInvalidSubscriptionVisibility = errors.New("hidden_ids must be an array of valid subscription item IDs")

type SubscriptionVisibilityResponse struct {
	Links                 []SubLinkStatus                    `json:"links"`
	ExternalSubscriptions []subscriptionExternalItem         `json:"external_subscriptions"`
	HiddenIDs             []string                           `json:"hidden_ids"`
	Jobs                  []store.SubscriptionPublicationJob `json:"jobs"`
}

func (s *Server) SubscriptionVisibility(r *http.Request, user *store.User) (SubscriptionVisibilityResponse, error) {
	links, external, err := s.subscriptionVisibilityCatalog(r, user)
	if err != nil {
		return SubscriptionVisibilityResponse{}, err
	}
	jobs, err := s.st.SubscriptionPublicationJobs(r.Context(), user.ID)
	if err != nil {
		return SubscriptionVisibilityResponse{}, err
	}
	hidden, err := s.st.UserRequestedHiddenSubscriptionItems(r.Context(), user.ID)
	if err != nil {
		return SubscriptionVisibilityResponse{}, err
	}
	if links == nil {
		links = []SubLinkStatus{}
	}
	if external == nil {
		external = []subscriptionExternalItem{}
	}
	return SubscriptionVisibilityResponse{Links: links, ExternalSubscriptions: external, HiddenIDs: hidden, Jobs: jobs}, nil
}

func (s *Server) UpdateSubscriptionVisibility(r *http.Request, user *store.User, hiddenIDs []string) (store.SubscriptionPublicationJob, error) {
	if hiddenIDs == nil || len(hiddenIDs) > 4096 {
		return store.SubscriptionPublicationJob{}, ErrInvalidSubscriptionVisibility
	}
	for _, id := range hiddenIDs {
		if !validSubscriptionItemID(id) {
			return store.SubscriptionPublicationJob{}, ErrInvalidSubscriptionVisibility
		}
	}
	links, external, err := s.subscriptionVisibilityCatalog(r, user)
	if err != nil {
		return store.SubscriptionPublicationJob{}, err
	}
	available := make(map[string]bool, len(links)+len(external))
	for _, link := range links {
		available[link.ID] = true
	}
	for _, source := range external {
		available[source.ID] = true
	}
	baseURL := s.base(r)
	job, err := s.st.EnqueueSubscriptionVisibility(r.Context(), user.ID, hiddenIDs, available, baseURL)
	if err == nil {
		s.rememberBaseURL(baseURL)
		s.wakeRegenerator()
	}
	return job, err
}

func validSubscriptionItemID(id string) bool {
	kind, raw, _ := strings.Cut(id, ":")
	if kind != "chain" && kind != "node" && kind != "external" {
		return false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == raw
}

func (s *Server) activeUserBySubToken(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	user, ok := s.userBySubToken(w, r)
	if !ok {
		return nil, false
	}
	if blocked, reason := subscriptionBlocked(user, time.Now()); blocked {
		writeSubError(w, http.StatusForbidden, reason)
		return nil, false
	}
	if user.Disabled {
		writeSubError(w, http.StatusForbidden, "subscription disabled")
		return nil, false
	}
	return user, true
}

func (s *Server) HandleSubVisibility(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	user, ok := s.activeUserBySubToken(w, r)
	if !ok {
		return
	}
	response, err := s.SubscriptionVisibility(r, user)
	if err != nil {
		writeInternalError(w, "serve subscription visibility", err)
		return
	}
	writeSubJSON(w, http.StatusOK, response)
}

func (s *Server) HandleSubUpdateVisibility(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	user, ok := s.activeUserBySubToken(w, r)
	if !ok {
		return
	}
	var input struct {
		HiddenIDs []string `json:"hidden_ids"`
	}
	if err := decodeSubscriptionRequest(w, r, &input); err != nil {
		writeSubError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	job, err := s.UpdateSubscriptionVisibility(r, user, input.HiddenIDs)
	if err != nil {
		writePublicationEnqueueError(w, err)
		return
	}
	writeSubJSON(w, http.StatusAccepted, job)
}

func (s *Server) HandleSubPublicationJobs(w http.ResponseWriter, r *http.Request) {
	disableSharedCache(w)
	user, ok := s.activeUserBySubToken(w, r)
	if !ok {
		return
	}
	jobs, err := s.st.SubscriptionPublicationJobs(r.Context(), user.ID)
	if err != nil {
		writeInternalError(w, "list subscription publication jobs", err)
		return
	}
	writeSubJSON(w, http.StatusOK, struct {
		Jobs []store.SubscriptionPublicationJob `json:"jobs"`
	}{Jobs: jobs})
}

func decodeSubscriptionRequest(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("expected a single JSON object")
	}
	return nil
}

func writePublicationEnqueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidSubscriptionVisibility), errors.Is(err, store.ErrSubscriptionItemUnavailable):
		writeSubError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrSubscriptionPublicationQueueFull):
		writeSubError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeSubError(w, http.StatusNotFound, "subscription not found")
	default:
		writeInternalError(w, "enqueue subscription publication", err)
	}
}

func writeSubJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
