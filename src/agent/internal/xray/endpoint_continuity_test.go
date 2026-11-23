package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"lattix/shared"
)

type endpointContinuityRunner struct {
	telemetryTestRunner
	running bool
}

func (r *endpointContinuityRunner) IsRunning(context.Context) bool { return r.running }

// Replaying desired state across either process restart must preserve running
// Xray and the exact on-disk config, including unrelated endpoints.
func TestSharedEndpointReplayPreservesRunningXray(t *testing.T) {
	m := newTestEndpointManager(t)
	runner := &endpointContinuityRunner{running: true}
	m.runner = runner
	first := endpointPortPayload(101, nil)
	first.Routes = []shared.SharedEndpointRoute{{ChainID: 1, Users: []string{"u1"}, Direct: true}}
	second := endpointPortPayload(102, nil)
	second.Routes = []shared.SharedEndpointRoute{{ChainID: 2, Users: []string{"u2"}, Direct: true}}
	for _, p := range []shared.ApplySharedEndpointPayload{first, second} {
		if _, err := m.ApplySharedEndpoint(p); err != nil {
			t.Fatal(err)
		}
	}
	original, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	restarts := runner.restarts
	pieces := m.ChainPieces()
	m = NewManager(m.bin, m.configPath, m.apiAddr, runner)
	m.SetChainPieces(pieces)
	for _, p := range []shared.ApplySharedEndpointPayload{first, second, first} {
		if _, err := m.ApplySharedEndpoint(p); err != nil {
			t.Fatal(err)
		}
	}
	if runner.restarts != restarts {
		t.Fatalf("unchanged replay restarted Xray %d times", runner.restarts-restarts)
	}
	after, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("replay rewrote live config")
	}
	first.Clients = []shared.ClientCredential{{ID: "new-user", Email: "new-user"}}
	if _, err := m.ApplySharedEndpoint(first); err != nil {
		t.Fatal(err)
	}
	if runner.restarts != restarts+1 {
		t.Fatal("changed config did not restart Xray")
	}
	runner.running = false
	if _, err := m.ApplySharedEndpoint(first); err != nil {
		t.Fatal(err)
	}
	if runner.restarts != restarts+2 {
		t.Fatal("stopped Xray was not recovered")
	}
}

func TestSharedEndpointReplayRepairsMissingConfig(t *testing.T) {
	m := newTestEndpointManager(t)
	runner := &endpointContinuityRunner{running: true}
	m.runner = runner
	p := endpointPortPayload(103, nil)
	if _, err := m.ApplySharedEndpoint(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApplySharedEndpoint(p); err != nil {
		t.Fatal(err)
	}
	if runner.restarts != 2 {
		t.Fatal("missing config was not restored")
	}
	if _, err := os.Stat(m.configPath); err != nil {
		t.Fatal(err)
	}
}

func TestSharedEndpointReplayRepairsDNATWithoutRestart(t *testing.T) {
	stubTLSCert(t)
	calls := stubIPTables(t, "")
	m := newVersionedTestManager(t, "26.10.1")
	runner := &endpointContinuityRunner{running: true}
	m.runner = runner
	p := shared.ApplySharedEndpointPayload{EndpointID: 5, Config: shared.VirtualConfig{Protocol: shared.ProtocolHysteria2, Security: shared.SecurityTLS, CertMode: shared.CertModeSelfSign, TLSDomain: "www.example.com", PortHop: "20000-20031", Template: json.RawMessage(hy2EndpointTemplate)}}
	realized, err := m.ApplySharedEndpoint(p)
	if err != nil {
		t.Fatal(err)
	}
	*calls = nil
	// Missing firewall state must recover without bouncing an unchanged listener.
	if _, err := m.ApplySharedEndpoint(p); err != nil {
		t.Fatal(err)
	}
	if len(*calls) == 0 {
		t.Fatal("missing DNAT was not restored")
	}
	if runner.restarts != 1 {
		t.Fatalf("firewall recovery restarted Xray %d times", runner.restarts)
	}
	*calls = nil
	listIPTablesRules = func(string, string) string {
		return fmt.Sprintf("-A PREROUTING -p udp -m udp --dport 20000:20031 -m comment --comment lattix:shared_endpoint_5 -j REDIRECT --to-ports %d", realized.Port)
	}
	if _, err := m.ApplySharedEndpoint(p); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("healthy replay rewrote firewall: %v", *calls)
	}
	if runner.restarts != 1 {
		t.Fatal("healthy replay restarted Xray")
	}
}

func TestSharedEndpointReplayAppliesPreviouslyUncommittedRuntime(t *testing.T) {
	for _, reloaded := range []bool{false, true} {
		t.Run(fmt.Sprintf("agent-reloaded=%v", reloaded), func(t *testing.T) {
			stubIPTables(t, "")
			m := newTestEndpointManager(t)
			runner := &endpointContinuityRunner{running: true}
			m.runner = runner
			p := endpointPortPayload(104, nil)
			p.Clients = []shared.ClientCredential{{ID: "old-user", Email: "old-user"}}
			if _, err := m.ApplySharedEndpoint(p); err != nil {
				t.Fatal(err)
			}
			// The candidate reaches disk, but firewall failure prevents Xray restart
			// and the last successfully applied piece still contains the old clients.
			p.Clients = []shared.ClientCredential{{ID: "new-user", Email: "new-user"}}
			listIPTablesRules = func(string, string) string {
				return "-A PREROUTING -p udp --dport 20000:20031 -m comment --comment lattix:shared_endpoint_104 -j REDIRECT --to-ports 12345"
			}
			runIPTables = func(string, ...string) error { return errTestIPTables }
			if _, err := m.ApplySharedEndpoint(p); err == nil {
				t.Fatal("expected post-commit firewall failure")
			}
			if runner.restarts != 1 {
				t.Fatal("failed attempt unexpectedly restarted Xray")
			}
			if reloaded {
				pieces := m.ChainPieces()
				m = NewManager(m.bin, m.configPath, m.apiAddr, runner)
				m.SetChainPieces(pieces)
			}
			runIPTables = func(string, ...string) error { return nil }
			if _, err := m.ApplySharedEndpoint(p); err != nil {
				t.Fatal(err)
			}
			if runner.restarts != 2 {
				t.Fatal("retry acknowledged disk config without applying it to Xray")
			}
		})
	}
}
