package store

import (
	"context"
	"encoding/json"
	"testing"
)

// TestCommandResponseAcceptedWhileQueued 覆盖 Flush 的 send → MarkCommandSent 落库窗口：
// Agent 回执可能先于 MarkCommandSent 到达，此时命令仍为 queued；回执必须照常落终态，
// 否则命令会永久停在 sent、关联 revision 任务永久 queued（重连前不再补发）。
func TestCommandResponseAcceptedWhileQueued(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "s", Address: "s.example.com",
		BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := st.EnqueueCommand(ctx, "req-1", "trace-1", serverID,
		"chain-hop.remove", json.RawMessage(`{"hop_id":1,"kind":"forward"}`))
	if err != nil {
		t.Fatal(err)
	}
	// 回执先到：命令仍是 queued。
	acked, err := st.MarkCommandAcked(ctx, commandID)
	if err != nil || !acked {
		t.Fatalf("ack queued command = %v err=%v, want true", acked, err)
	}
	// Flush 随后的 MarkCommandSent 不得把已终态的 acked 翻回 sent。
	if err := st.MarkCommandSent(ctx, commandID); err != nil {
		t.Fatal(err)
	}
	command, err := st.CommandByID(ctx, commandID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != CommandStatusAcked {
		t.Fatalf("command status = %s, want acked", command.Status)
	}
	if command.Attempts != 0 {
		t.Fatalf("command attempts = %d, want 0 (sent mark must be a no-op)", command.Attempts)
	}
	// 迟到/重复回执不得翻回终态。
	if again, err := st.MarkCommandAcked(ctx, commandID); err != nil || again {
		t.Fatalf("duplicate ack = %v err=%v, want false", again, err)
	}
}

// TestRequeueSentCommandCAS 覆盖清理超时重发的 store 语义：仅 sent → queued 可迁移，
// 已终态命令不得被复位（迟到回执/并发终态优先）。
func TestRequeueSentCommandCAS(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "s", Address: "s.example.com",
		BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := st.EnqueueCommand(ctx, "req-requeue", "trace-requeue", serverID,
		"chain-hop.remove", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkCommandSent(ctx, commandID); err != nil {
		t.Fatal(err)
	}
	requeued, err := st.RequeueSentCommand(ctx, commandID, 1)
	if err != nil || !requeued {
		t.Fatalf("requeue sent command = %v err=%v, want true", requeued, err)
	}
	if command, _ := st.CommandByID(ctx, commandID); command.Status != CommandStatusQueued {
		t.Fatalf("command status = %s, want queued", command.Status)
	}
	if again, err := st.RequeueSentCommand(ctx, commandID, 1); err != nil || again {
		t.Fatalf("requeue queued command = %v err=%v, want false", again, err)
	}
	if err := st.MarkCommandSent(ctx, commandID); err != nil {
		t.Fatal(err)
	}
	// A timeout observed before reconnect cannot requeue the newer delivery.
	if stale, err := st.RequeueSentCommand(ctx, commandID, 1); err != nil || stale {
		t.Fatalf("requeue stale delivery = %v err=%v, want false", stale, err)
	}
	if acked, err := st.MarkCommandAcked(ctx, commandID); err != nil || !acked {
		t.Fatalf("ack resent command = %v err=%v", acked, err)
	}
	if requeued, err := st.RequeueSentCommand(ctx, commandID, 2); err != nil || requeued {
		t.Fatalf("requeue acked command = %v err=%v, want false", requeued, err)
	}
	if command, _ := st.CommandByID(ctx, commandID); command.Status != CommandStatusAcked {
		t.Fatalf("terminal command flipped: %s", command.Status)
	}
}

// TestCommandFailureAcceptedWhileQueued 覆盖失败回执同窗口：queued → failed 合法且携带原因。
func TestCommandFailureAcceptedWhileQueued(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, ServerDraft{Alias: "s", Address: "s.example.com",
		BootstrapToken: "token", MachineType: MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := st.EnqueueCommand(ctx, "req-2", "trace-2", serverID,
		"shared-endpoint.apply", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	failed, err := st.MarkCommandFailedWithError(ctx, commandID, "apply failed")
	if err != nil || !failed {
		t.Fatalf("fail queued command = %v err=%v, want true", failed, err)
	}
	command, err := st.CommandByID(ctx, commandID)
	if err != nil {
		t.Fatal(err)
	}
	if command.Status != CommandStatusFailed || command.Error != "apply failed" {
		t.Fatalf("command = %+v, want failed with reason", command)
	}
	if again, err := st.MarkCommandFailedWithError(ctx, commandID, "late"); err != nil || again {
		t.Fatalf("duplicate failure = %v err=%v, want false", again, err)
	}
	if command, _ := st.CommandByID(ctx, commandID); command.Error != "apply failed" {
		t.Fatalf("terminal error overwritten: %q", command.Error)
	}
}
