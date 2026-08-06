package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
	"lattix/shared"
)

// assertServerObserveDone 断言 envelope 携带合法 observe_id 且观察已 done、
// 阶段为 db/dispatch/ack 三阶段（重建/修复/清理共用）。
func assertServerObserveDone(t *testing.T, srv *Server, env groupEnv, kind string) {
	t.Helper()
	if env.ObserveID == "" {
		t.Fatalf("envelope 未携带 observe_id: %+v", env)
	}
	if !shared.ValidMessageID(env.ObserveID) {
		t.Fatalf("observe_id 非法: %q", env.ObserveID)
	}
	obs, ok := srv.observes.Get(env.ObserveID)
	if !ok {
		t.Fatalf("观察 %s 不存在", env.ObserveID)
	}
	if obs.Kind != kind {
		t.Fatalf("观察 kind = %s, want %s", obs.Kind, kind)
	}
	if obs.Status != progress.StatusDone {
		t.Fatalf("观察状态 = %s, want %s (obs = %+v)", obs.Status, progress.StatusDone, obs)
	}
	if len(obs.Stages) != 3 {
		t.Fatalf("stages = %+v, want 3 个阶段", obs.Stages)
	}
	for i, want := range []string{"db", "dispatch", "ack"} {
		if obs.Stages[i].Key != want {
			t.Fatalf("stage[%d].key = %s, want %s", i, obs.Stages[i].Key, want)
		}
	}
}

// cleanupRecordingRequester 记录是否在线，并模拟 agent：同步回执 xray.cleanup 结果
// （CleanupXraySync 等待回执，无回执会重试后报错）。
type cleanupRecordingRequester struct {
	settingsRequester
	disp *dispatch.Dispatcher
}

func (f *cleanupRecordingRequester) Send(ctx context.Context, serverID int64, env shared.Envelope) error {
	if env.Type == shared.TypeCleanupXray {
		f.disp.HandleMessage(serverID, shared.Envelope{
			Kind: shared.KindResponse, Type: env.Type,
			RequestID: env.RequestID, TraceID: env.TraceID,
			Code: shared.CodeOK,
			Data: json.RawMessage(`{"cleanup":{"removed_inbounds":[],"removed_pieces":[]}}`),
		})
	}
	return nil
}

// seedObserveServer 构造带 observes 注册表、在线的空服务器（无节点，重建/清理用）。
func seedObserveServer(t *testing.T) (*Server, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	requester := &cleanupRecordingRequester{settingsRequester: settingsRequester{online: map[int64]bool{}}}
	dispatcher := dispatch.New(st, requester)
	requester.disp = dispatcher
	serverID, err := st.CreateServer(ctx, "s1", "s1.test", "tok", store.MachineTypeDirect, "", "", "US", "Test")
	if err != nil {
		t.Fatal(err)
	}
	requester.online[serverID] = true
	srv := &Server{st: st, disp: dispatcher, req: requester, observes: progress.NewRegistry()}
	return srv, serverID
}

func TestRebuildXrayCarriesObserveID(t *testing.T) {
	serverAPI, _, _, _ := seedRebuildServer(t)
	serverAPI.observes = progress.NewRegistry()
	env, rec := postGroupObserve(t, serverAPI, serverAPI.handleRebuildXray, `{"server_id":1}`)
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("rebuild status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertServerObserveDone(t, serverAPI, env, "server.rebuild_xray")
	obs, _ := serverAPI.observes.Get(env.ObserveID)
	if obs.Stage != "ack" {
		t.Fatalf("同步等待回执后 stage = %s, want ack (obs = %+v)", obs.Stage, obs)
	}
}

func TestRepairServerCarriesObserveID(t *testing.T) {
	serverAPI, _, _, _ := seedRebuildServer(t)
	serverAPI.observes = progress.NewRegistry()
	env, rec := postGroupObserve(t, serverAPI, serverAPI.handleRepairServer, `{"server_id":1}`)
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("repair status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertServerObserveDone(t, serverAPI, env, "server.repair")
}

func TestCleanupXrayCarriesObserveID(t *testing.T) {
	serverAPI, serverID := seedObserveServer(t)
	env, rec := postGroupObserve(t, serverAPI, serverAPI.handleCleanupXray,
		`{"server_id":`+itoa(serverID)+`,"dry_run":true}`)
	if rec.Code != http.StatusOK || env.Code != shared.CodeOK {
		t.Fatalf("cleanup status = %d code = %q body = %s", rec.Code, env.Code, rec.Body.String())
	}
	assertServerObserveDone(t, serverAPI, env, "server.cleanup_xray")
	obs, _ := serverAPI.observes.Get(env.ObserveID)
	if obs.Stage != "ack" {
		t.Fatalf("同步等待回执后 stage = %s, want ack (obs = %+v)", obs.Stage, obs)
	}
}
