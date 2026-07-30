package main

import (
	"path/filepath"
	"testing"

	"lattix/shared"
)

func queuedEnvelope(commandType string) shared.Envelope {
	id := shared.NewMessageID()
	return shared.Envelope{
		Kind: shared.KindRequest, Type: commandType,
		RequestID: id, TraceID: id, Data: []byte(`{}`),
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
	queue.commands[index].State = "running"
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
		if command.State != "queued" {
			t.Fatalf("recovered state = %q, want queued", command.State)
		}
	}
	index = recovered.nextLocked()
	if recovered.commands[index].Envelope.RequestID != upgradeCommand.RequestID {
		t.Fatalf("recovered priority changed: %+v", recovered.commands[index])
	}
}
