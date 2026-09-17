package xray

import (
	"encoding/json"
	"fmt"

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
// its chain routes. Reapplying preserves the Reality key pair, the VLESS
// Encryption pair and realized port, so assignment changes do not invalidate
// existing subscriptions.
func (m *Manager) ApplySharedEndpoint(p shared.ApplySharedEndpointPayload) (*shared.RealizedConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.EndpointID <= 0 {
		return nil, fmt.Errorf("endpoint_id 缺失")
	}
	if p.Config.Protocol != shared.ProtocolVLESS && p.Config.Protocol != shared.ProtocolHysteria2 {
		return nil, fmt.Errorf("共享端点仅支持 VLESS/hysteria2")
	}
	// hy2 出口共享监听同版本门控（ApplyNode 同款）。
	if p.Config.Protocol == shared.ProtocolHysteria2 {
		version, _ := m.Version()
		if !xrayVersionAtLeast(version, shared.XrayMinVersionHy2) {
			return nil, fmt.Errorf("节点 xray 版本过低（hysteria2 需要 xray ≥ %s，当前 %s），请先在节点页升级 xray",
				shared.XrayMinVersionHy2, version)
		}
	}
	prev := m.findChainPiece(p.EndpointID, sharedEndpointPieceKind)
	config := p.Config
	config.StaticClients = append([]shared.ClientCredential(nil), p.Clients...)
	if prev != nil {
		if prev.Port != 0 {
			config.Port = prev.Port
		}
		// 密钥对保留（重建同构，rebuild.go preserveTemplate）：占位符预替换后
		// fillTemplate 不再轮换 Reality/VLESS Encryption 密钥对，既有订阅不失效。
		config.Template = json.RawMessage(preserveTemplate(config, prev.Inbound))
	}
	portCandidates := endpointPortCandidates(config.Port, p.PortCandidates, prev)
	// 重发幂等：同端点已落地端口直接复用（xray 运行中本就持有该端口，
	// 重复占用探测会误判冲突——与 pickChainPort 语义一致）；新端口才做占用检查。
	port, err := m.pickChainPort(config.Port, portCandidates, prev, shared.SharedEndpointTag(p.EndpointID), shared.PortLayers(config.Protocol))
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
		if realized.Encryption == "" {
			realized.Encryption = prev.Encryption
		}
	}
	rec := state.ChainPiece{
		HopID: p.EndpointID, Kind: sharedEndpointPieceKind, Port: realized.Port,
		PrivateKey: privateKey, PublicKey: realized.PublicKey, Encryption: realized.Encryption,
		Inbound: inbound,
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
	// hy2 端口跳跃：段 → 监听端口的 DNAT（spec §3.2 DNAT 路径）。无条件先清
	// 陈旧规则（端点重发/协议或跳跃段变更），再按期望状态建立（空段 ensure = 清理）。
	if err := m.removeUdpHopDNAT(shared.SharedEndpointTag(p.EndpointID)); err != nil {
		return nil, err
	}
	if config.Protocol == shared.ProtocolHysteria2 {
		if err := m.ensureUdpHopDNAT(shared.SharedEndpointTag(p.EndpointID), config.PortHop, realized.Port); err != nil {
			return nil, err
		}
	}
	if err := m.restartApply(); err != nil {
		// 配置已回滚（inbound 不存在），DNAT 规则与 inbound 同生共死，一并回收。
		_ = m.removeUdpHopDNAT(shared.SharedEndpointTag(p.EndpointID))
		return nil, err
	}
	m.upsertChainPiece(rec)
	return realized, nil
}

func (m *Manager) RemoveSharedEndpoint(endpointID int64) error {
	if err := m.removeUdpHopDNAT(shared.SharedEndpointTag(endpointID)); err != nil {
		return err
	}
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
	// 入口终结模式（P4 §3.2）：出口为 hy2 时末段以 hy2 outbound 直拨出口（UDP）。
	if route.ExitProtocol == shared.ProtocolHysteria2 {
		return renderHy2Outbound(tag, shared.Hy2DialSpec{
			Address: route.TargetAddress, Port: route.TargetPort,
			Auth:         shared.Hy2UserPassword(route.TunnelUUID),
			SNI:          route.Target.SNI,
			CertSHA256:   route.Target.CertSHA256,
			ObfsPassword: route.Target.ObfsPassword,
			UpMbps:       route.Target.UpMbps,
			DownMbps:     route.Target.DownMbps,
			PortHop:      route.Target.PortHop,
		})
	}
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
	// 隧道段安全层跟随出口业务 inbound：reality 出口（公钥非空）→ reality；
	// tls 出口（SNI 非空）→ tls（自签用 pinnedPeerCertSha256 钉住证书——xray 26.x 起为
	// hex 字符串，allowInsecure 已移除；ACME 走系统根验证）；其余 → 明文（spec §3.2/P3）。
	stream := map[string]any{"network": network}
	switch {
	case route.Target.PublicKey != "":
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"serverName": route.Target.ServerName, "publicKey": route.Target.PublicKey,
			"shortId": route.Target.ShortID, "fingerprint": shared.FingerprintChrome,
		}
	case route.Target.SNI != "":
		stream["security"] = "tls"
		tlsSettings := map[string]any{
			"serverName":  route.Target.SNI,
			"fingerprint": shared.FingerprintChrome,
		}
		if route.Target.CertSHA256 != "" {
			tlsSettings["pinnedPeerCertSha256"] = route.Target.CertSHA256
		}
		stream["tlsSettings"] = tlsSettings
	default:
		stream["security"] = "none"
	}
	switch network {
	case shared.NetworkGRPC:
		stream["grpcSettings"] = map[string]any{"serviceName": route.Target.ServiceName}
	case shared.NetworkXHTTP:
		stream["xhttpSettings"] = map[string]any{
			"path": route.Target.Path, "mode": route.Target.Mode, "host": route.Target.Host,
		}
	case shared.NetworkWS:
		w := map[string]any{"path": route.Target.Path}
		if route.Target.Host != "" {
			w["headers"] = map[string]any{"Host": route.Target.Host}
		}
		stream["wsSettings"] = w
	case shared.NetworkHTTPUpgrade:
		h := map[string]any{"path": route.Target.Path}
		if route.Target.Host != "" {
			h["host"] = route.Target.Host
		}
		stream["httpupgradeSettings"] = h
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

// renderHy2Outbound 渲染 hy2 直拨 outbound（Task 1 实测定稿形态）：
// network 恒 "hysteria"、tlsSettings 带 alpn [h3]（两者缺失分别退化为 TCP 承载/
// 握手失败）；客户端口令在 transport hysteriaSettings.auth；salamander/brutal/udpHop
// 在 finalmask；自签证书以 pinnedPeerCertSha256 钉住（hex），ACME 走系统根验证。
// 入口共享端点路由与链末段 forward（chain.go）共用。
func renderHy2Outbound(tag string, dial shared.Hy2DialSpec) map[string]any {
	tlsSettings := map[string]any{
		"serverName":  dial.SNI,
		"alpn":        []string{"h3"},
		"fingerprint": shared.FingerprintChrome,
	}
	if dial.CertSHA256 != "" {
		tlsSettings["pinnedPeerCertSha256"] = dial.CertSHA256
	}
	fm := map[string]any{}
	if dial.ObfsPassword != "" {
		fm["udp"] = []map[string]any{{
			"type":     "salamander",
			"settings": map[string]any{"password": dial.ObfsPassword},
		}}
	}
	quic := map[string]any{}
	if dial.UpMbps > 0 || dial.DownMbps > 0 {
		quic["congestion"] = "brutal"
		if dial.UpMbps > 0 {
			quic["brutalUp"] = fmt.Sprintf("%d mbps", dial.UpMbps)
		}
		if dial.DownMbps > 0 {
			quic["brutalDown"] = fmt.Sprintf("%d mbps", dial.DownMbps)
		}
	}
	// PortHop 非空时 dial.Port 即段起点（契约见 shared.Hy2DialSpec.Port；
	// dispatch 保证），客户端 udpHop 在段内换端口，出口 DNAT 收敛到监听端口。
	if dial.PortHop != "" {
		if _, _, err := shared.ParsePortHop(dial.PortHop); err == nil {
			quic["udpHop"] = map[string]any{"ports": dial.PortHop, "interval": shared.Hy2PortHopInterval}
		}
	}
	if len(quic) > 0 {
		fm["quicParams"] = quic
	}
	stream := map[string]any{
		"network":          "hysteria",
		"security":         "tls",
		"tlsSettings":      tlsSettings,
		"hysteriaSettings": map[string]any{"version": 2, "auth": dial.Auth},
	}
	if len(fm) > 0 {
		stream["finalmask"] = fm
	}
	return map[string]any{
		"tag": tag, "protocol": "hysteria",
		"settings":       map[string]any{"version": 2, "address": dial.Address, "port": dial.Port},
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
