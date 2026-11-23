package state

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestConnectionStateTransitions(t *testing.T) {
	legal := []struct{ from, to string }{
		{ConnStateConnecting, ConnStateOnline},
		{ConnStateConnecting, ConnStateBackoff},
		{ConnStateConnecting, ConnStateAuthRejected},
		{ConnStateOnline, ConnStateConnecting},
		{ConnStateOnline, ConnStateBackoff},
		{ConnStateOnline, ConnStateAuthRejected},
		{ConnStateBackoff, ConnStateConnecting},
		{ConnStateBackoff, ConnStateAuthRejected},
	}
	for _, c := range legal {
		if !ValidConnectionTransition(c.from, c.to) {
			t.Errorf("缺少转换边 %s → %s", c.from, c.to)
		}
	}
	illegal := []struct{ from, to string }{
		{ConnStateAuthRejected, ConnStateOnline},
		{ConnStateAuthRejected, ConnStateBackoff},
		{ConnStateAuthRejected, ConnStateConnecting},
		{ConnStateBackoff, ConnStateOnline}, // 退避后必须先拨号
		{ConnStateOnline, "unknown"},
		{"", ConnStateOnline},
	}
	for _, c := range illegal {
		if ValidConnectionTransition(c.from, c.to) {
			t.Errorf("非法转换未被拒绝 %s → %s", c.from, c.to)
		}
	}
	// 同状态幂等。
	if !ValidConnectionTransition(ConnStateOnline, ConnStateOnline) {
		t.Fatal("同状态转换应幂等允许")
	}
}

func TestConnectionStatusSerializesState(t *testing.T) {
	raw, err := json.Marshal(ConnectionStatus{
		Connected: true, State: ConnStateOnline, Panel: "https://panel", ServerID: 7,
		AgentVersion: "v1", PID: 42, ChangedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["state"] != ConnStateOnline {
		t.Fatalf("state = %v", out["state"])
	}
	// 旧写入（无 state）反序列化兼容：空值合法。
	var legacy ConnectionStatus
	if err := json.Unmarshal([]byte(`{"connected":false}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.State != "" || legacy.Connected {
		t.Fatalf("legacy = %+v", legacy)
	}
}

func TestUpdatingConnectionTransitions(t *testing.T) {
	for _, edge := range [][2]string{{"online", "updating"}, {"updating", "online"}, {"updating", "backoff"}, {"updating", "connecting"}, {"updating", "auth_rejected"}} {
		if !ValidConnectionTransition(edge[0], edge[1]) {
			t.Errorf("missing upgrade transition %s -> %s", edge[0], edge[1])
		}
	}
	if ValidConnectionTransition("auth_rejected", "updating") {
		t.Fatal("terminal rejected credentials cannot start updating")
	}
}

// The upgrade executor can publish connection.json while the session loop
// commits refreshed credentials. Run with -race to protect that boundary.
func TestConnectionServerIDDuringCredentialCommit(t *testing.T) {
	st := &State{}
	path := filepath.Join(t.TempDir(), "state.json")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := int64(1); i <= 20; i++ {
			if err := SaveWith(path, st, func(s *State) { s.ServerID = i }); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for {
		id := st.ConnectionServerID()
		if id < 0 || id > 20 {
			t.Fatalf("invalid server ID %d", id)
		}
		select {
		case <-done:
			return
		default:
		}
	}
}
