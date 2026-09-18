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
	// hy2 出口共享监听（P4 §3.2）：无路由（出口即终点，freedom 出站）；
	// clients = 入口终结链的 tunnel 身份 ∪ 端到端链的业务用户 UUID。
	if endpoint.Protocol == shared.ProtocolHysteria2 {
		chains, err := d.st.ChainsByServiceEndpoint(ctx, endpointID)
		if err != nil {
			return err
		}
		payload := shared.ApplySharedEndpointPayload{EndpointID: endpointID, Config: config}
		server, err := d.st.ServerByID(ctx, endpoint.ServerID)
		if err != nil {
			return err
		}
		if server.MachineType == "nat" {
			payload.PortCandidates = listenCandidatesOf(server)
		}
		for _, chain := range chains {
			if chain.EndpointID != 0 && chain.ServiceUUID != "" {
				payload.Clients = append(payload.Clients, shared.ClientCredential{
					ID: chain.ServiceUUID, Email: "tunnel:" + chain.ServiceUUID})
			}
		}
		uuids, err := d.st.ActiveServiceEndpointUsers(ctx, endpointID)
		if err != nil {
			return err
		}
		for _, uuid := range uuids {
			payload.Clients = append(payload.Clients, shared.ClientCredential{ID: uuid, Email: uuid})
		}
		sort.Slice(payload.Clients, func(i, j int) bool { return payload.Clients[i].Email < payload.Clients[j].Email })
		if err := d.efsm.Transition(ctx, endpointID, store.EndpointStatusApplying, "下发端点部署命令", nil); err != nil {
			return err
		}
		_, err = d.Enqueue(ctx, endpoint.ServerID, shared.TypeApplySharedEndpoint, payload)
		return err
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
		EndpointID: endpointID, Config: config, DestCandidates: d.opts.DestCandidates,
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
			identity := assignment.Identity()
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
			if err := json.Unmarshal(revision.Snapshot.ServiceRealized, &route.Target); err != nil {
				continue
			}
			var svc struct {
				Protocol string `json:"protocol"`
			}
			_ = json.Unmarshal(revision.Snapshot.ServiceConfig, &svc)
			// 入口终结 2 跳 hy2（P4 §3.2）：端点直接以 hy2 outbound 拨出口
			//（出口公网地址 + realized 端口），不再绕回环管道；其余维持回环现状。
			if svc.Protocol == shared.ProtocolHysteria2 && len(revision.Snapshot.Hops) == 2 &&
				revision.Snapshot.Hops[0].Transport == "hy2" {
				exitHop := revision.Snapshot.Hops[1]
				exitSrv, err := d.st.ServerByID(ctx, exitHop.ServerID)
				if err != nil || route.Target.Port == 0 {
					continue
				}
				route.ExitProtocol = shared.ProtocolHysteria2
				route.TargetAddress = store.ResolveServerAddress(exitSrv, exitHop.Address)
				route.TargetPort = publicPortOf(exitSrv, route.Target.Port)
			} else {
				if entry.ForwardPort == 0 || len(revision.Snapshot.ServiceRealized) == 0 {
					continue
				}
				route.TargetAddress = "127.0.0.1"
				route.TargetPort = entry.ForwardPort
			}
		}
		sort.Strings(route.Users)
		payload.Routes = append(payload.Routes, route)
	}
	sort.Slice(payload.Clients, func(i, j int) bool { return payload.Clients[i].Email < payload.Clients[j].Email })
	sort.Slice(payload.Routes, func(i, j int) bool { return payload.Routes[i].ChainID < payload.Routes[j].ChainID })
	// 建链即部署监听：即使 routes/clients 为空也下发 apply（端口 + Reality 密钥即刻生效）。
	// 用户分配仅做增量用户添加，不再是部署前提。
	// 端点状态机入口：pending/failed/applying/active → applying（重试/重部署/幂等自愈）。
	if err := d.efsm.Transition(ctx, endpointID, store.EndpointStatusApplying, "下发端点部署命令", nil); err != nil {
		return err
	}
	_, err = d.Enqueue(ctx, endpoint.ServerID, shared.TypeApplySharedEndpoint, payload)
	return err
}
