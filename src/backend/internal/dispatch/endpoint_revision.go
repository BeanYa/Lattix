package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// confirmRevisionEntryEndpoint runs after the forwarding pieces have applied,
// so the staged route contains their realized targets. Only the revision's
// own endpoint command can unblock publication.
func (d *Dispatcher) confirmRevisionEntryEndpoint(ctx context.Context, revisionID int64, snapshot *store.ChainRevisionSnapshot, hops []store.ChainHop, node *store.Node) bool {
	if snapshot == nil || !snapshot.AwaitEntryEndpoint {
		return true
	}
	task, err := d.st.EntryEndpointRevisionTask(ctx, revisionID)
	if errors.Is(err, store.ErrNotFound) {
		// Continue an in-flight gate saved before it had a dedicated task.
		_, err = d.st.AddRevisionTask(ctx, store.ChainRevisionTask{
			RevisionID: revisionID, TaskKey: store.RevisionPieceEntry, Kind: store.RevisionPieceEntry,
			Phase: "apply", Action: "apply", HopID: hops[0].ID, ServerID: hops[0].ServerID,
		})
		task.Status = store.RevisionTaskPending
	}
	if err != nil {
		log.Printf("dispatch: revision %d entry endpoint task: %v", revisionID, err)
		return false
	}
	switch task.Status {
	case store.RevisionTaskAcked:
		return true
	case store.RevisionTaskFailed:
		d.failHop(ctx, &hops[0], fmt.Sprintf("共享入口（endpoint %d）部署失败: %s", snapshot.EndpointID, task.Error))
	case store.RevisionTaskPending:
		if !snapshot.EntryEndpointStaged {
			revision, err := d.st.ChainRevisionByID(ctx, revisionID)
			if err != nil {
				log.Printf("dispatch: load revision %d to stage its entry endpoint: %v", revisionID, err)
				return false
			}
			if node != nil {
				snapshot.ServiceRealized = append(json.RawMessage(nil), node.RealizedConfig...)
			}
			mergeSnapshotHopFields(snapshot, hops, false)
			snapshot.EntryEndpointStaged = true
			if err := d.st.UpdateChainRevision(ctx, revisionID, revision.Status, revision.Error, *snapshot); err != nil {
				log.Printf("dispatch: stage revision %d entry endpoint: %v", revisionID, err)
				return false
			}
		}
		if err := d.reconcileSharedEndpoint(ctx, snapshot.EndpointID, revisionID, nil); err != nil {
			log.Printf("dispatch: apply revision %d entry endpoint: %v", revisionID, err)
		}
	}
	return false
}

func (d *Dispatcher) confirmedEntryPayload(ctx context.Context, revisionID int64) json.RawMessage {
	task, err := d.st.EntryEndpointRevisionTask(ctx, revisionID)
	if err != nil || task.Status != store.RevisionTaskAcked {
		return nil
	}
	cmd, err := d.st.CommandByID(ctx, task.CommandID)
	if err != nil || cmd.Type != shared.TypeApplySharedEndpoint {
		return nil
	}
	return cmd.Data
}

func (d *Dispatcher) recoverEntryEndpointTask(ctx context.Context, revisionID int64) {
	task, err := d.st.EntryEndpointRevisionTask(ctx, revisionID)
	if err != nil {
		return
	}
	if task.Status == store.RevisionTaskFailed {
		// Includes a crash after the publication transaction rejected an
		// obsolete confirmation but before the chain FSM saw the failure.
		if hop, err := d.st.ChainHopByID(ctx, task.HopID); err == nil {
			d.failHop(ctx, hop, task.Error)
		}
		return
	}
	if task.Status != store.RevisionTaskQueued {
		return
	}
	cmd, err := d.st.CommandByID(ctx, task.CommandID)
	if err != nil {
		return
	}
	// A terminal command with a queued task was interrupted before its
	// endpoint outcome was stored. Replay success to obtain the realization.
	switch cmd.Status {
	case store.CommandStatusAcked:
		_, err = d.st.ResetQueuedRevisionTask(ctx, task.ID, cmd.ID)
	case store.CommandStatusFailed:
		err = d.st.SetRevisionTaskResult(ctx, task.ID, false, cmd.Error, cmd.ID)
	}
	if err != nil {
		log.Printf("dispatch: recover revision %d entry endpoint result: %v", revisionID, err)
	}
}

func (d *Dispatcher) entryEndpointByCommand(ctx context.Context, commandID int64) (*store.ChainRevision, *store.ChainRevisionTask) {
	revision, task, err := d.st.RevisionByCommandID(ctx, commandID)
	if err != nil || task.Kind != store.RevisionPieceEntry {
		return nil, nil
	}
	return revision, task
}

func (d *Dispatcher) applyEntryEndpointResult(ctx context.Context, cmd *store.Command, revision *store.ChainRevision, task *store.ChainRevisionTask, result shared.ApplyResultPayload, success bool, detail string) {
	var requested shared.ApplySharedEndpointPayload
	if success && (json.Unmarshal(cmd.Data, &requested) != nil || result.EndpointID != revision.Snapshot.EndpointID || result.RealizedConfig == nil ||
		result.RealizedConfig.Port <= 0 || (requested.Config.Port != 0 && requested.Config.Port != result.RealizedConfig.Port)) {
		success, detail = false, "共享入口回执没有确认本次部署的监听端口"
	}
	if _, _, err := d.efsm.ApplyResult(ctx, revision.Snapshot.EndpointID, cmd.ID, result.RealizedConfig, success, detail); err != nil {
		// No endpoint receipt committed. Fail only the task still owned by
		// this command; a retry may already have replaced its command ID.
		if taskErr := d.st.SetRevisionTaskResult(ctx, task.ID, false, err.Error(), cmd.ID); taskErr != nil {
			log.Printf("dispatch: revision %d entry endpoint result: %v (task: %v)", revision.ID, err, taskErr)
			return
		}
	}
	d.advanceChain(ctx, revision.ChainID)
}
