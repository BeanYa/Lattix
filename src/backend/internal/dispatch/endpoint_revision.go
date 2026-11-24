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
	if err != nil || task.Status != store.RevisionTaskQueued {
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
		err = d.st.SetRevisionTaskResult(ctx, task.ID, false, cmd.Error)
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
	var applied shared.ApplySharedEndpointPayload
	if success && (json.Unmarshal(cmd.Data, &applied) != nil || result.EndpointID != revision.Snapshot.EndpointID || result.RealizedConfig == nil ||
		result.RealizedConfig.Port <= 0 || (applied.Config.Port != 0 && applied.Config.Port != result.RealizedConfig.Port)) {
		success, detail = false, "共享入口回执没有确认本次部署的监听端口"
	}
	if err := d.applyEndpointResult(ctx, revision.Snapshot.EndpointID, result.RealizedConfig, success, detail); err != nil {
		success, detail = false, err.Error()
	}
	if err := d.st.SetRevisionTaskResult(ctx, task.ID, success, detail); err != nil {
		log.Printf("dispatch: revision %d entry endpoint result: %v", revision.ID, err)
		return
	}
	d.advanceChain(ctx, revision.ChainID)
}

func (d *Dispatcher) applyEndpointResult(ctx context.Context, id int64, config *shared.RealizedConfig, success bool, detail string) error {
	if !success {
		return d.efsm.Transition(ctx, id, store.EndpointStatusFailed, detail, nil)
	}
	d.clearEndpointRetry(id)
	realized, _ := json.Marshal(config)
	return d.efsm.Transition(ctx, id, store.EndpointStatusActive, "部署回执确认", realized)
}
