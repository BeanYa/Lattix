package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"lattix/shared"
)

type queuedAgentCommand struct {
	Envelope  shared.Envelope `json:"envelope"`
	Priority  int             `json:"priority"`
	State     string          `json:"state"` // queued|running
	CreatedAt time.Time       `json:"created_at"`
}

type commandQueueJournal struct {
	Version  int                  `json:"version"`
	Commands []queuedAgentCommand `json:"commands"`
}

type persistentCommandQueue struct {
	mu       sync.Mutex
	path     string
	commands []queuedAgentCommand
	execute  func(shared.Envelope)
	after    func(shared.Envelope)
	attachID uint64
	wake     chan struct{}
}

func newPersistentCommandQueue(path string, after func(shared.Envelope)) (*persistentCommandQueue, error) {
	queue := &persistentCommandQueue{path: path, after: after, wake: make(chan struct{}, 1)}
	if err := queue.load(); err != nil {
		return nil, err
	}
	go queue.loop()
	return queue, nil
}

func (q *persistentCommandQueue) Attach(execute func(shared.Envelope)) uint64 {
	q.mu.Lock()
	q.attachID++
	id := q.attachID
	q.execute = execute
	q.mu.Unlock()
	q.signal()
	return id
}

func (q *persistentCommandQueue) Detach(id uint64) {
	q.mu.Lock()
	if q.attachID == id {
		q.execute = nil
	}
	q.mu.Unlock()
}

func (q *persistentCommandQueue) Submit(envelope shared.Envelope) error {
	q.mu.Lock()
	for _, command := range q.commands {
		if command.Envelope.RequestID == envelope.RequestID {
			execute := q.execute
			isTest := envelope.Type == shared.TypeServerTestRun
			q.mu.Unlock()
			// A resent running test command needs a fresh ACCEPTED response on the
			// new connection. Manager.Accept is idempotent for the same task.
			if isTest && execute != nil {
				go execute(envelope)
			}
			return nil
		}
	}
	q.commands = append(q.commands, queuedAgentCommand{
		Envelope: envelope, Priority: commandPriority(envelope.Type), State: "queued", CreatedAt: time.Now().UTC(),
	})
	if err := q.saveLocked(); err != nil {
		q.commands = q.commands[:len(q.commands)-1]
		q.mu.Unlock()
		return fmt.Errorf("persist command queue: %w", err)
	}
	q.mu.Unlock()
	q.signal()
	return nil
}

func commandPriority(commandType string) int {
	switch commandType {
	case shared.TypeUpgradeAgent:
		return 100
	case shared.TypeUninstall:
		return 90
	case shared.TypeUpgradeXray:
		return 70
	case shared.TypeServerTestRun:
		return 10
	default:
		return 50
	}
}

func (q *persistentCommandQueue) loop() {
	for {
		<-q.wake
		for {
			q.mu.Lock()
			index := q.nextLocked()
			if index < 0 || q.execute == nil {
				q.mu.Unlock()
				break
			}
			q.commands[index].State = "running"
			_ = q.saveLocked()
			command := q.commands[index]
			execute := q.execute
			q.mu.Unlock()

			execute(command.Envelope)
			if q.after != nil {
				q.after(command.Envelope)
			}

			q.mu.Lock()
			for current := range q.commands {
				if q.commands[current].Envelope.RequestID == command.Envelope.RequestID {
					q.commands = append(q.commands[:current], q.commands[current+1:]...)
					break
				}
			}
			_ = q.saveLocked()
			q.mu.Unlock()
		}
	}
}

func (q *persistentCommandQueue) nextLocked() int {
	best := -1
	for index := range q.commands {
		if q.commands[index].State == "running" {
			return index
		}
		if best < 0 || q.commands[index].Priority > q.commands[best].Priority ||
			(q.commands[index].Priority == q.commands[best].Priority && q.commands[index].CreatedAt.Before(q.commands[best].CreatedAt)) {
			best = index
		}
	}
	return best
}

func (q *persistentCommandQueue) load() error {
	raw, err := os.ReadFile(q.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal commandQueueJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return fmt.Errorf("decode command queue: %w", err)
	}
	if journal.Version != 1 {
		return fmt.Errorf("unsupported command queue version %d", journal.Version)
	}
	seen := make(map[string]struct{}, len(journal.Commands))
	for index := range journal.Commands {
		command := &journal.Commands[index]
		if err := command.Envelope.Validate(); err != nil || command.Envelope.Kind != shared.KindRequest {
			return fmt.Errorf("invalid queued command: %w", err)
		}
		if _, exists := seen[command.Envelope.RequestID]; exists {
			return errors.New("duplicate request in command queue")
		}
		seen[command.Envelope.RequestID] = struct{}{}
		command.State = "queued"
		command.Priority = commandPriority(command.Envelope.Type)
	}
	q.commands = journal.Commands
	return q.saveLocked()
}

func (q *persistentCommandQueue) saveLocked() error {
	if len(q.commands) == 0 {
		if err := os.Remove(q.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	commands := append([]queuedAgentCommand(nil), q.commands...)
	sort.SliceStable(commands, func(i, j int) bool { return commands[i].CreatedAt.Before(commands[j].CreatedAt) })
	raw, err := json.MarshalIndent(commandQueueJournal{Version: 1, Commands: commands}, "", "  ")
	if err != nil {
		return err
	}
	tempPath := q.path + ".tmp"
	if err := os.WriteFile(tempPath, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tempPath, q.path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func (q *persistentCommandQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
