package panel

import (
	"context"
	"net/http"
	"time"

	"lattix/shared"
)

type lifecycleSyncer interface {
	SyncLifecycle(context.Context, shared.PanelLifecycleSnapshot) []int64
}

func (s *Server) handlePanelState(w http.ResponseWriter, r *http.Request) {
	if s.lifecycle == nil {
		writeError(w, http.StatusServiceUnavailable, "panel lifecycle is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.lifecycle.Snapshot())
}

func (s *Server) transitionLifecycle(ctx context.Context, state, fault string, wait bool) (shared.PanelLifecycleSnapshot, error) {
	if s.lifecycle == nil {
		return shared.PanelLifecycleSnapshot{}, nil
	}
	snapshot, changed, err := s.lifecycle.Transition(state, fault)
	if err != nil || !changed {
		return snapshot, err
	}
	if syncer, ok := s.req.(lifecycleSyncer); ok {
		if wait {
			syncer.SyncLifecycle(ctx, snapshot)
		} else {
			go func() {
				broadcastCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				syncer.SyncLifecycle(broadcastCtx, snapshot)
			}()
		}
	}
	return snapshot, nil
}
