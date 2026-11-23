package xray

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lattix/shared"
)

// Opt-in acceptance with a real Xray. The same established VLESS TCP stream
// must survive desired-state replay, not just reconnect successfully afterward.
func TestSharedEndpointReplayKeepsLiveTCPStream(t *testing.T) {
	bin := os.Getenv("LATTIX_TEST_XRAY_BIN")
	if bin == "" {
		t.Skip("set LATTIX_TEST_XRAY_BIN for real data-plane acceptance")
	}
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		for {
			c, err := origin.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	api, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	apiAddr := api.Addr().String()
	api.Close()
	configPath := filepath.Join(t.TempDir(), "xray.json")
	runner := NewRunner("exec", bin, configPath)
	defer runner.Stop(context.Background())
	m := NewManager(bin, configPath, apiAddr, runner)
	payload := func(id int64) shared.ApplySharedEndpointPayload {
		return shared.ApplySharedEndpointPayload{EndpointID: id,
			Config:  shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Template: json.RawMessage(`{"tag":"{{TAG}}","listen":"127.0.0.1","port":"{{PORT}}","protocol":"vless","settings":{"clients":"{{CLIENTS}}","decryption":"none"},"streamSettings":{"network":"tcp"}}`)},
			Clients: []shared.ClientCredential{{ID: "11111111-1111-1111-1111-111111111111", Email: "live-test"}},
			Routes:  []shared.SharedEndpointRoute{{ChainID: id, Users: []string{"live-test"}, Direct: true}},
		}
	}
	first, second := payload(201), payload(202)
	realized, err := m.ApplySharedEndpoint(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApplySharedEndpoint(second); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(realized.Port)), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(10 * time.Second))
	// VLESS v0, fixed UUID above, no addons, TCP command, echo origin IPv4.
	header := append([]byte{0}, bytes.Repeat([]byte{0x11}, 16)...)
	header = append(header, 0, 1)
	port := origin.Addr().(*net.TCPAddr).Port
	header = binary.BigEndian.AppendUint16(header, uint16(port))
	header = append(header, 1, 127, 0, 0, 1)
	if _, err := client.Write(header); err != nil {
		t.Fatal(err)
	}
	roundTrip := func(message string, first bool) {
		t.Helper()
		if _, err := client.Write([]byte(message)); err != nil {
			t.Fatal(err)
		}
		if first {
			response := make([]byte, 2)
			if _, err := io.ReadFull(client, response); err != nil {
				t.Fatal(err)
			}
			if response[0] != 0 || response[1] != 0 {
				t.Fatalf("VLESS response=%v", response)
			}
		}
		received := make([]byte, len(message))
		if _, err := io.ReadFull(client, received); err != nil {
			t.Fatalf("established stream interrupted: %v", err)
		}
		if string(received) != message {
			t.Fatalf("echo=%q", received)
		}
	}
	roundTrip("before-replay", true)
	pieces := m.ChainPieces()
	m = NewManager(bin, configPath, apiAddr, runner)
	m.SetChainPieces(pieces)
	for i, p := range []shared.ApplySharedEndpointPayload{first, second, first, second} {
		if _, err := m.ApplySharedEndpoint(p); err != nil {
			t.Fatal(err)
		}
		roundTrip(fmt.Sprintf("after-replay-%d", i), false)
	}
}
