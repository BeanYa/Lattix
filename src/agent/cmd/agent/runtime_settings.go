package main

import (
	"math/rand"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"lattix/shared"
)

type runtimeSettings struct {
	mu              sync.RWMutex
	value           shared.AgentSettings
	panelInstanceID string
	appliedRevision int64
	lastApplyError  string
	changed         chan struct{}
}

func newRuntimeSettings(document shared.AgentSettingsDocument) *runtimeSettings {
	r := &runtimeSettings{
		value:   shared.DefaultAgentSettings(),
		changed: make(chan struct{}),
	}
	if document.Validate() == nil {
		r.value = document.Agent
		r.panelInstanceID = document.Panel.InstanceID
		r.appliedRevision = document.Agent.Revision
	}
	return r
}

func (r *runtimeSettings) snapshot() (shared.AgentSettings, string, int64, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.value, r.panelInstanceID, r.appliedRevision, r.lastApplyError
}

func (r *runtimeSettings) apply(document shared.AgentSettingsDocument) {
	r.mu.Lock()
	r.value = document.Agent
	r.panelInstanceID = document.Panel.InstanceID
	r.appliedRevision = document.Agent.Revision
	r.lastApplyError = ""
	changed := r.changed
	r.changed = make(chan struct{})
	r.mu.Unlock()
	close(changed)
}

func (r *runtimeSettings) fail(message string) {
	r.mu.Lock()
	r.lastApplyError = message
	r.mu.Unlock()
}

func (r *runtimeSettings) resetForPanelRebind() {
	r.mu.Lock()
	r.value = shared.DefaultAgentSettings()
	r.panelInstanceID = ""
	r.appliedRevision = 0
	r.lastApplyError = ""
	changed := r.changed
	r.changed = make(chan struct{})
	r.mu.Unlock()
	close(changed)
}

func (r *runtimeSettings) waitInterval(done <-chan struct{}, interval func(shared.AgentSettings) time.Duration) bool {
	for {
		r.mu.RLock()
		settings := r.value
		changed := r.changed
		r.mu.RUnlock()
		timer := time.NewTimer(interval(settings))
		select {
		case <-timer.C:
			return true
		case <-changed:
			if !timer.Stop() {
				<-timer.C
			}
		case <-done:
			if !timer.Stop() {
				<-timer.C
			}
			return false
		}
	}
}

func reconnectDelay(settings shared.AgentSettings, failures int, closeCode int) time.Duration {
	if closeCode == websocket.CloseServiceRestart {
		return time.Duration(200+rand.Intn(301)) * time.Millisecond
	}
	if closeCode == 4001 {
		return 5 * time.Minute
	}
	if settings.Reconnect.Mode == shared.ReconnectModeLimited &&
		failures > settings.Reconnect.MaxRetries {
		return 5 * time.Minute
	}
	delay := 500 * time.Millisecond
	for i := 1; i < failures && delay < 30*time.Second; i++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	// Full delay with ±20% jitter.
	factor := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(delay) * factor)
}

func websocketCloseCode(err error) int {
	if closeErr, ok := err.(*websocket.CloseError); ok {
		return closeErr.Code
	}
	return 0
}

func selectInitialToken(stored, install string) string {
	if install == "" {
		return stored
	}
	incoming, incomingErr := shared.ParseCredential(install)
	current, currentErr := shared.ParseCredential(stored)
	if incomingErr == nil && currentErr == nil &&
		incoming.PanelInstanceID == current.PanelInstanceID &&
		incoming.Epoch == current.Epoch {
		return stored
	}
	return install
}
