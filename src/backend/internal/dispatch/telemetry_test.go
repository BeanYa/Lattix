package dispatch

import (
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

func TestLatencyProbeActiveRequiresPanelAndAgentAcceptance(t *testing.T) {
	boolPtr := func(value bool) *bool { return &value }
	tests := []struct {
		name       string
		panelState string
		reported   *bool
		want       bool
	}{
		{name: "legacy agent without lifecycle provider", want: true},
		{name: "legacy agent while active", panelState: shared.PanelStateActive, want: true},
		{name: "legacy agent while updating", panelState: shared.PanelStateUpdating, want: false},
		{name: "active timeout remains accepted", panelState: shared.PanelStateActive, reported: boolPtr(true), want: true},
		{name: "agent pause wins after panel resumes", panelState: shared.PanelStateActive, reported: boolPtr(false), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dispatcher := &Dispatcher{}
			if test.panelState != "" {
				dispatcher.PanelLifecycle = func() shared.PanelLifecycleSnapshot {
					return shared.PanelLifecycleSnapshot{State: test.panelState}
				}
			}
			if got := dispatcher.latencyProbeActive(test.reported); got != test.want {
				t.Fatalf("latencyProbeActive() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHandleTelemetryAppliesOnlineUsersSnapshot(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := New(st, &fakeRequester{online: map[int64]bool{}})
	var gotServer int64
	var got []shared.OnlineUserStat
	d.OnOnlineUsers = func(serverID int64, users []shared.OnlineUserStat, _ time.Time) {
		gotServer = serverID
		got = users
	}
	env := shared.Envelope{Kind: shared.KindEvent, Type: shared.TypeTelemetry, Data: marshalMessageData(shared.TelemetryPayload{
		XrayVersion: "v1.8.0",
		OnlineUsers: []shared.OnlineUserStat{{User: "user-uuid-1", IPs: []string{"1.2.3.4", "1.2.3.5"}}},
	})}
	d.handleTelemetry(7, env)
	if gotServer != 7 || len(got) != 1 || got[0].User != "user-uuid-1" || len(got[0].IPs) != 2 {
		t.Fatalf("online_users not applied: server=%d users=%+v", gotServer, got)
	}
}

func TestHandleTelemetryNullOnlineUsersKeepsSnapshot(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := New(st, &fakeRequester{online: map[int64]bool{}})
	called := false
	d.OnOnlineUsers = func(serverID int64, users []shared.OnlineUserStat, _ time.Time) {
		called = true
	}
	// 降级帧：online_users 缺失（或为 null，nil 序列化结果），不得触碰既有快照。
	env := shared.Envelope{Kind: shared.KindEvent, Type: shared.TypeTelemetry, Data: marshalMessageData(shared.TelemetryPayload{XrayVersion: "v1.8.0"})}
	d.handleTelemetry(7, env)
	if called {
		t.Fatal("degraded frame without online_users must not touch the snapshot")
	}
}

func TestHandleTelemetryEmptyOnlineUsersSnapshotClears(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := New(st, &fakeRequester{online: map[int64]bool{}})
	called := false
	d.OnOnlineUsers = func(serverID int64, users []shared.OnlineUserStat, _ time.Time) {
		called = true
		if serverID != 7 || users == nil || len(users) != 0 {
			t.Fatalf("expected empty non-nil snapshot for server 7, got server=%d users=%+v", serverID, users)
		}
	}
	// 成功空查询帧：online_users:[]，必须投递（空快照 = 清除该服务器在线记录）。
	env := shared.Envelope{Kind: shared.KindEvent, Type: shared.TypeTelemetry, Data: marshalMessageData(shared.TelemetryPayload{
		XrayVersion: "v1.8.0",
		OnlineUsers: []shared.OnlineUserStat{},
	})}
	d.handleTelemetry(7, env)
	if !called {
		t.Fatal("empty [] online_users snapshot not applied (empty snapshot = clear server)")
	}
}

func TestHandleTelemetryWithoutOnlineUsersSinkIsSafe(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := New(st, &fakeRequester{online: map[int64]bool{}})
	env := shared.Envelope{Kind: shared.KindEvent, Type: shared.TypeTelemetry, Data: marshalMessageData(shared.TelemetryPayload{
		OnlineUsers: []shared.OnlineUserStat{{User: "user-uuid-1", IPs: []string{"1.2.3.4"}}},
	})}
	d.handleTelemetry(7, env)
}
