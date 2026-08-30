package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lattix/shared"
)

func queuedEnvelope(commandType string) shared.Envelope {
	id := shared.NewMessageID()
	return shared.Envelope{
		Kind: shared.KindRequest, Type: commandType,
		RequestID: id, TraceID: id, Data: []byte(`{}`),
	}
}

func TestPersistentCommandQueueDoesNotAdvanceUntilAfterCompletes(t *testing.T) {
	release := make(chan struct{})
	executed := make(chan string, 2)
	queue, err := newPersistentCommandQueue(filepath.Join(t.TempDir(), "commands.json"), func(envelope shared.Envelope) {
		if envelope.Type == shared.TypeServerTestRun {
			<-release
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	queue.Attach(func(envelope shared.Envelope) { executed <- envelope.Type })
	if err := queue.Submit(queuedEnvelope(shared.TypeServerTestRun)); err != nil {
		t.Fatal(err)
	}
	if got := receiveCommand(t, executed); got != shared.TypeServerTestRun {
		t.Fatalf("first command = %q, want server test", got)
	}
	if err := queue.Submit(queuedEnvelope(shared.TypeUpgradeAgent)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-executed:
		t.Fatalf("command %q advanced before test delivery completed", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if got := receiveCommand(t, executed); got != shared.TypeUpgradeAgent {
		t.Fatalf("second command = %q, want Agent upgrade", got)
	}
}

func receiveCommand(t *testing.T, commands <-chan string) string {
	t.Helper()
	select {
	case command := <-commands:
		return command
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for command execution")
		return ""
	}
}

func TestPersistentCommandQueuePrioritizesUpgradeAndRecoversRunningEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.json")
	queue, err := newPersistentCommandQueue(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	testCommand := queuedEnvelope(shared.TypeServerTestRun)
	normalCommand := queuedEnvelope(shared.TypeApplyNode)
	upgradeCommand := queuedEnvelope(shared.TypeUpgradeAgent)
	for _, command := range []shared.Envelope{testCommand, normalCommand, upgradeCommand} {
		if err := queue.Submit(command); err != nil {
			t.Fatal(err)
		}
	}
	queue.mu.Lock()
	index := queue.nextLocked()
	if index < 0 || queue.commands[index].Envelope.RequestID != upgradeCommand.RequestID {
		queue.mu.Unlock()
		t.Fatalf("next command = %+v, want Agent upgrade", queue.commands[index])
	}
	queue.commands[index].State = commandStateRunning
	if err := queue.saveLocked(); err != nil {
		queue.mu.Unlock()
		t.Fatal(err)
	}
	queue.mu.Unlock()

	recovered, err := newPersistentCommandQueue(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	recovered.mu.Lock()
	defer recovered.mu.Unlock()
	if len(recovered.commands) != 3 {
		t.Fatalf("recovered command count = %d", len(recovered.commands))
	}
	for _, command := range recovered.commands {
		if command.State != commandStateQueued {
			t.Fatalf("recovered state = %q, want queued", command.State)
		}
	}
	index = recovered.nextLocked()
	if recovered.commands[index].Envelope.RequestID != upgradeCommand.RequestID {
		t.Fatalf("recovered priority changed: %+v", recovered.commands[index])
	}
}

func TestCommandTransitions(t *testing.T) {
	legal := []struct{ from, to commandState }{
		{commandStateQueued, commandStateRunning},
	}
	for _, c := range legal {
		if !validCommandTransition(c.from, c.to) {
			t.Errorf("missing transition edge %s → %s", c.from, c.to)
		}
	}
	illegal := []struct{ from, to commandState }{
		{commandStateRunning, commandStateQueued}, // re-queue only happens via load recovery
		{commandStateQueued, "bogus"},
		{"", commandStateQueued},
	}
	for _, c := range illegal {
		if validCommandTransition(c.from, c.to) {
			t.Errorf("illegal transition not rejected %s → %s", c.from, c.to)
		}
	}
	// Same-state transitions stay idempotent (nextLocked may return the running entry).
	for _, s := range []commandState{commandStateQueued, commandStateRunning} {
		if !validCommandTransition(s, s) {
			t.Errorf("same-state transition %s should be idempotent", s)
		}
	}
}

func TestPersistentCommandQueueRejectsUnknownState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.json")
	journal := commandQueueJournal{Version: 1, Commands: []queuedAgentCommand{{
		Envelope: queuedEnvelope(shared.TypeUpgradeAgent), State: "bogus", CreatedAt: time.Now().UTC(),
	}}}
	raw, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newPersistentCommandQueue(path, nil); err == nil {
		t.Fatal("queue accepted an unknown persisted command state")
	}
}
