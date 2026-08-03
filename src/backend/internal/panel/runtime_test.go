package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandlePanelRuntimeReturnsProcessAndTaskSnapshot(t *testing.T) {
	scheduler := newTaskScheduler(func(context.Context) *time.Location { return time.UTC })
	scheduler.register(scheduledTask{
		name:    "runtime-test",
		trigger: func(context.Context) taskTrigger { return intervalTrigger(time.Hour) },
		run:     func(context.Context) error { return nil },
	})
	server := &Server{
		cfg:       Config{Version: "v-test"},
		startedAt: time.Now().Add(-2 * time.Minute),
		scheduler: scheduler,
	}
	recorder := httptest.NewRecorder()
	server.handlePanelRuntime(recorder, httptest.NewRequest(http.MethodGet, "/api/panel/runtime", nil))

	var response struct {
		Code string          `json:"code"`
		Data panelRuntimeDTO `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode runtime response: %v", err)
	}
	if response.Code != "OK" {
		t.Fatalf("code = %q, want OK", response.Code)
	}
	if response.Data.Panel.Version != "v-test" || response.Data.Panel.PID <= 0 {
		t.Fatalf("panel = %+v", response.Data.Panel)
	}
	if response.Data.Panel.UptimeSeconds < 119 || response.Data.Process.GoVersion == "" {
		t.Fatalf("runtime snapshot = %+v", response.Data)
	}
	if len(response.Data.Tasks) != 1 || response.Data.Tasks[0].Name != "runtime-test" {
		t.Fatalf("tasks = %+v", response.Data.Tasks)
	}
}

func TestRuntimeReadersReturnConsistentLinuxValues(t *testing.T) {
	total, active := readHostMemory()
	if total > 0 && active > total {
		t.Fatalf("active memory %d exceeds total %d", active, total)
	}
	rss, virtual := readProcessMemory()
	if virtual > 0 && rss > virtual {
		t.Fatalf("RSS %d exceeds virtual memory %d", rss, virtual)
	}
}
