package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/progress"
)

func TestObserveTaskGet(t *testing.T) {
	reg := progress.NewRegistry()
	srv := &Server{observes: reg}
	o := reg.Start("user_group.update", "更新用户分组",
		[]progress.Stage{{Key: "db", Label: "校验并写入数据库"}})
	o.Report("db", 50, "写入中")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/observe-task/get?observe_id="+o.ID, nil)
	srv.handleGetObserveTask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Data progress.Observation `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Data.ID != o.ID || got.Data.Percent != 50 || got.Data.Status != progress.StatusRunning {
		t.Fatalf("observation = %+v", got.Data)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/observe-task/get?observe_id=00000000000000000000000000000000", nil)
	srv.handleGetObserveTask(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("missing observe status = %d, want 404", rec2.Code)
	}
}
