package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"lattix/shared"
)

func upgradeTestConnection(t *testing.T) (*safeConn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			accepted <- c
		}
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	peer := <-accepted
	t.Cleanup(func() { peer.Close() })
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	return &safeConn{conn: peer}, client
}

func TestAgentUpgradeReportsLifecycleBeforeApplying(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upgraded bool
		err      error
		code     string
	}{
		{"success", true, nil, shared.CodeOK},
		{"failure", false, errors.New("download failed"), shared.CodeInternalError},
		{"already current", false, nil, shared.CodeOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, client := upgradeTestConnection(t)
			id := shared.NewMessageID()
			env := shared.Envelope{Kind: shared.KindRequest, Type: shared.TypeUpgradeAgent, RequestID: id, TraceID: id}
			started := make(chan struct{})
			release := make(chan struct{})
			done := make(chan struct{})
			restarted := false
			var localStates []string
			tracker := newAgentUpgradeTracker()
			if _, err := tracker.attach(sc, func(_ bool, value string, _ error) { localStates = append(localStates, value) }); err != nil {
				t.Fatal(err)
			}
			var initial shared.Envelope
			if err := client.ReadJSON(&initial); err != nil {
				t.Fatal(err)
			}
			localStates = nil
			go func() {
				defer close(done)
				performAgentUpgrade(tracker, env, func() (bool, error) { close(started); <-release; return tc.upgraded, tc.err }, func() { restarted = true })
			}()
			read := func() shared.Envelope {
				t.Helper()
				var v shared.Envelope
				if err := client.ReadJSON(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			first := read()
			if first.Kind != shared.KindEvent || first.Type != shared.TypeAgentLifecycleChanged || string(first.Data) != `{"state":"updating"}` {
				t.Fatalf("first frame=%+v", first)
			}
			<-started
			close(release)
			response := read()
			if response.Kind != shared.KindResponse || response.Code != tc.code || response.RequestID != id {
				t.Fatalf("response=%+v", response)
			}
			if !tc.upgraded {
				last := read()
				if last.Type != shared.TypeAgentLifecycleChanged || string(last.Data) != `{"state":"online"}` {
					t.Fatalf("final frame=%+v", last)
				}
			}
			<-done
			if restarted != tc.upgraded {
				t.Fatalf("restart=%v", restarted)
			}
			if len(localStates) == 0 || localStates[0] != "updating" {
				t.Fatalf("local states=%v", localStates)
			}
			if !tc.upgraded && localStates[len(localStates)-1] != "online" {
				t.Fatalf("local states=%v", localStates)
			}
		})
	}
}

func TestAgentUpgradeDoesNotStartWhenStatusCannotReachPanel(t *testing.T) {
	sc, _ := upgradeTestConnection(t)
	called, localStatus := false, false
	tracker := newAgentUpgradeTracker()
	if _, err := tracker.attach(sc, func(bool, string, error) { localStatus = true }); err != nil {
		t.Fatal(err)
	}
	localStatus = false
	sc.conn.Close()
	id := shared.NewMessageID()
	performAgentUpgrade(tracker, shared.Envelope{RequestID: id, TraceID: id}, func() (bool, error) { called = true; return true, nil }, func() { t.Error("must not restart") })
	if called {
		t.Fatal("upgrade started after failing to send updating")
	}
	if localStatus {
		t.Fatal("failed status delivery changed local connection state")
	}
}

func TestAgentUpgradeReplaysOnReplacementConnection(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			tracker := newAgentUpgradeTracker()
			first, client1 := upgradeTestConnection(t)
			attachment, err := tracker.attach(first, nil)
			if err != nil {
				t.Fatal(err)
			}
			read := func(c *websocket.Conn) shared.Envelope {
				t.Helper()
				var e shared.Envelope
				if err := c.ReadJSON(&e); err != nil {
					t.Fatal(err)
				}
				return e
			}
			read(client1) // Initial process state online.
			id := shared.NewMessageID()
			env := shared.Envelope{Kind: shared.KindRequest, Type: shared.TypeUpgradeAgent, RequestID: id, TraceID: id}
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				performAgentUpgrade(tracker, env, func() (bool, error) {
					close(started)
					<-release
					if success {
						return true, nil
					}
					return false, errors.New("download failed")
				}, func() {})
			}()
			if e := read(client1); string(e.Data) != `{"state":"updating"}` {
				t.Fatalf("first state=%s", e.Data)
			}
			<-started
			tracker.detach(attachment)
			first.conn.Close()
			next, client2 := upgradeTestConnection(t)
			if _, err := tracker.attach(next, nil); err != nil {
				t.Fatal(err)
			}
			if e := read(client2); string(e.Data) != `{"state":"updating"}` {
				t.Fatalf("reconnect state=%s", e.Data)
			}
			close(release)
			response := read(client2)
			if response.Kind != shared.KindResponse || response.RequestID != id {
				t.Fatalf("completion lost on old socket: %+v", response)
			}
			if !success {
				if e := read(client2); string(e.Data) != `{"state":"online"}` {
					t.Fatalf("failed upgrade state=%s", e.Data)
				}
			}
			<-done
			// A new Agent process has no active operation and clears maintenance.
			fresh := newAgentUpgradeTracker()
			third, client3 := upgradeTestConnection(t)
			if _, err := fresh.attach(third, nil); err != nil {
				t.Fatal(err)
			}
			if e := read(client3); string(e.Data) != `{"state":"online"}` {
				t.Fatalf("new process state=%s", e.Data)
			}
		})
	}
}
