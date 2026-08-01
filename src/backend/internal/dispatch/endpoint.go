package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// ReconcileSharedEndpoint sends one complete desired-state document. This
// makes assignment add/remove and chain publication naturally idempotent and
// keeps the number of Xray routing rules proportional to chains, not users.
func (d *Dispatcher) ReconcileSharedEndpoint(ctx context.Context, endpointID int64) error {
	endpoint, err := d.st.SharedEndpointByID(ctx, endpointID)
	if err != nil {
		return err
	}
	var config shared.VirtualConfig
	if err := json.Unmarshal(endpoint.ConfigTemplate, &config); err != nil {
		return fmt.Errorf("decode shared endpoint %d: %w", endpointID, err)
	}
	assignments, err := d.st.ActiveEndpointAssignments(ctx, endpointID)
	if err != nil {
		return err
	}
	assignmentsByChain := map[int64][]store.UserChainAssignment{}
	for _, assignment := range assignments {
		assignmentsByChain[assignment.ChainID] = append(assignmentsByChain[assignment.ChainID], assignment)
	}
	chains, err := d.st.ListChains(ctx)
	if err != nil {
		return err
	}
	payload := shared.ApplySharedEndpointPayload{
		EndpointID: endpointID, Config: config, DestCandidates: d.DestCandidates,
	}
	server, err := d.st.ServerByID(ctx, endpoint.ServerID)
	if err != nil {
		return err
	}
	if server.MachineType == "nat" {
		payload.PortCandidates = listenCandidatesOf(server)
	}
	for _, chain := range chains {
		if chain.EndpointID != endpointID || chain.PublishedRevisionID == 0 ||
			chain.Status == store.ChainStatusInvalid || chain.Status == store.ChainStatusDeleted {
			continue
		}
		revision, err := d.st.PublishedChainRevision(ctx, chain.ID)
		if err != nil || len(revision.Snapshot.Hops) == 0 {
			continue
		}
		route := shared.SharedEndpointRoute{ChainID: chain.ID, Direct: len(revision.Snapshot.Hops) == 1,
			TunnelUUID: revision.Snapshot.ServiceUUID}
		for _, assignment := range assignmentsByChain[chain.ID] {
			identity := fmt.Sprintf("access:%d", assignment.ID)
			payload.Clients = append(payload.Clients, shared.ClientCredential{
				ID: assignment.AccessUUID, Email: identity,
			})
			route.Users = append(route.Users, identity)
		}
		if len(route.Users) == 0 {
			continue
		}
		if !route.Direct {
			entry := revision.Snapshot.Hops[0]
			if entry.ForwardPort == 0 || len(revision.Snapshot.ServiceRealized) == 0 {
				continue
			}
			route.TargetAddress = "127.0.0.1"
			route.TargetPort = entry.ForwardPort
			if err := json.Unmarshal(revision.Snapshot.ServiceRealized, &route.Target); err != nil {
				continue
			}
		}
		sort.Strings(route.Users)
		payload.Routes = append(payload.Routes, route)
	}
	sort.Slice(payload.Clients, func(i, j int) bool { return payload.Clients[i].Email < payload.Clients[j].Email })
	sort.Slice(payload.Routes, func(i, j int) bool { return payload.Routes[i].ChainID < payload.Routes[j].ChainID })
	if len(payload.Routes) == 0 {
		// 无剩余路由：下发移除命令，释放 agent 上的监听端口（端点记录保留供复用）。
		if err := d.st.SetSharedEndpointPending(ctx, endpointID); err != nil {
			return err
		}
		_, err = d.Enqueue(ctx, endpoint.ServerID, shared.TypeRemoveSharedEndpoint,
			shared.RemoveSharedEndpointPayload{EndpointID: endpointID})
		return err
	}
	if err := d.st.SetSharedEndpointApplying(ctx, endpointID); err != nil {
		return err
	}
	_, err = d.Enqueue(ctx, endpoint.ServerID, shared.TypeApplySharedEndpoint, payload)
	return err
}
