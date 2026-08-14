package state

import (
	"encoding/json"
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
