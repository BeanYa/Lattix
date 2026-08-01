package xray

import (
	"encoding/json"
	"fmt"
	"strings"

	"lattix/agent/internal/state"
	"lattix/shared"
)

const sharedEndpointPieceKind = "shared-endpoint"

func sharedEndpointRoutePrefix(id int64) string {
	return fmt.Sprintf("shared_endpoint_route_%d_", id)
}

func sharedEndpointRouteTag(endpointID, chainID int64) string {
	return fmt.Sprintf("%s%d", sharedEndpointRoutePrefix(endpointID), chainID)
}

// ApplySharedEndpoint atomically replaces a server-level listener and all of
// its chain routes. Reapplying preserves the Reality key pair and realized
// port, so assignment changes do not invalidate existing subscriptions.
func (m *Manager) ApplySharedEndpoint(p shared.ApplySharedEndpointPayload) (*shared.RealizedConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.EndpointID <= 0 {
		return nil, fmt.Errorf("endpoint_id 缺失")
	}
	if p.Config.Protocol != shared.ProtocolVLESS {
		return nil, fmt.Errorf("共享端点仅支持 VLESS+REALITY")
	}
	prev := m.findChainPiece(p.EndpointID, sharedEndpointPieceKind)
	config := p.Config
	config.StaticClients = append([]shared.ClientCredential(nil), p.Clients...)
	if prev != nil {
		if prev.Port != 0 {
			config.Port = prev.Port
		}
		if prev.PrivateKey != "" {
			config.Template = json.RawMessage(strings.ReplaceAll(string(config.Template),
				shared.PlaceholderRealityPrivateKey, prev.PrivateKey))
		}
	}
	portCandidates := endpointPortCandidates(config.Port, p.PortCandidates, prev)
	// 重发幂等：同端点已落地端口直接复用（xray 运行中本就持有该端口，
	// 重复占用探测会误判冲突——与 pickChainPort 语义一致）；新端口才做占用检查。
	port, err := m.pickChainPort(config.Port, portCandidates, prev)
	if err != nil {
		return nil, err
	}
	inbound, realized, err := m.fillTemplate(port, shared.SharedEndpointTag(p.EndpointID), config, nil,
		p.DestCandidates, portCandidates)
	if err != nil {
		return nil, err
	}
	privateKey := endpointPrivateKey(inbound)
	if prev != nil {
		if privateKey == "" {
			privateKey = prev.PrivateKey
		}
		if realized.PublicKey == "" {
			realized.PublicKey = prev.PublicKey
		}
	}
	rec := state.ChainPiece{
		HopID: p.EndpointID, Kind: sharedEndpointPieceKind, Port: realized.Port,
		PrivateKey: privateKey, PublicKey: realized.PublicKey, Inbound: inbound,
	}
	for _, route := range p.Routes {
		if route.ChainID <= 0 || len(route.Users) == 0 {
			continue
		}
		outboundTag := directOutboundTag
		if !route.Direct {
			if route.TargetAddress == "" || route.TargetPort == 0 || route.TunnelUUID == "" {
				return nil, fmt.Errorf("链 %d 的共享端点路由缺少目标或 tunnel UUID", route.ChainID)
			}
			outboundTag = sharedEndpointRouteTag(p.EndpointID, route.ChainID)
			outbound, err := json.Marshal(renderSharedEndpointOutbound(route, outboundTag))
			if err != nil {
				return nil, err
			}
			rec.Outbounds = append(rec.Outbounds, outbound)
		}
		rule, err := json.Marshal(map[string]any{
			"type": "field", "inboundTag": []string{shared.SharedEndpointTag(p.EndpointID)},
			"user": route.Users, "outboundTag": outboundTag,
		})
		if err != nil {
			return nil, err
		}
		rec.Rules = append(rec.Rules, rule)
	}
	cur, err := m.loadConfig()
	if err != nil {
		return nil, err
	}
	if err := m.commitConfig(applyChainPiece(cur, rec)); err != nil {
		return nil, err
	}
	if err := m.restartApply(); err != nil {
		return nil, err
	}
	m.upsertChainPiece(rec)
	return realized, nil
}

func (m *Manager) RemoveSharedEndpoint(endpointID int64) error {
	return m.RemoveChainHop(endpointID, sharedEndpointPieceKind)
}

// endpointPortCandidates 决定共享端点部署的端口候选（§21）：
//   - 端口留空且面板未下发候选（普通直连机）→ 空候选，pickPort 挑随机空闲端口；
//   - NAT 受限机 → 透传面板段内候选（AllowedPorts 展开）；
//   - 显式端口 / 重发复用已落地端口 → 不传候选（config.Port 已非 0）。
func endpointPortCandidates(configPort int, panelCandidates []int, prev *state.ChainPiece) []int {
	if configPort == 0 && len(panelCandidates) == 0 && prev == nil {
		return nil
	}
	return panelCandidates
}

func renderSharedEndpointOutbound(route shared.SharedEndpointRoute, tag string) map[string]any {
	user := map[string]any{"id": route.TunnelUUID, "encryption": "none"}
	if route.Target.Flow != "" {
		user["flow"] = route.Target.Flow
	}
	if route.Target.Encryption != "" {
		user["encryption"] = route.Target.Encryption
	}
	network := route.Target.Network
	if network == "" {
		network = shared.NetworkTCP
	}
	stream := map[string]any{
		"network": network, "security": "reality",
		"realitySettings": map[string]any{
			"serverName": route.Target.ServerName, "publicKey": route.Target.PublicKey,
			"shortId": route.Target.ShortID, "fingerprint": shared.FingerprintChrome,
		},
	}
	switch network {
	case shared.NetworkGRPC:
		stream["grpcSettings"] = map[string]any{"serviceName": route.Target.ServiceName}
	case shared.NetworkXHTTP:
		stream["xhttpSettings"] = map[string]any{
			"path": route.Target.Path, "mode": route.Target.Mode, "host": route.Target.Host,
		}
	}
	return map[string]any{
		"tag": tag, "protocol": "vless",
		"settings": map[string]any{"vnext": []map[string]any{{
			"address": route.TargetAddress, "port": route.TargetPort,
			"users": []map[string]any{user},
		}}},
		"streamSettings": stream,
	}
}

func endpointPrivateKey(inbound json.RawMessage) string {
	var value struct {
		StreamSettings struct {
			RealitySettings struct {
				PrivateKey string `json:"privateKey"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	_ = json.Unmarshal(inbound, &value)
	return value.StreamSettings.RealitySettings.PrivateKey
}
