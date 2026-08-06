// 链跳配置件（§21.1 piece）渲染与生命周期：portal/bridge/forward 三种角色
// 按载荷渲染并入受管 config.json（配置形状对照 scripts/dev/poc-reverse.sh 的 PoC）。
// piece 涉及 reverse/routing 顶层段，无法走 gRPC 热操作——一律走
// "落盘 → xray -test → systemctl restart → 失败回滚上一份"的兜底流水线（§6）。
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"lattix/agent/internal/state"
	"lattix/shared"
)

// directOutboundTag 是骨架配置中 freedom outbound 的 tag（见 manager.go skeletonJSON）。
const directOutboundTag = "direct"

// chainPortalReverseTag 是 reverse.portals 条目的 tag（PoC 中为 "portal"，按跳区分）。
func chainPortalReverseTag(hopID int64) string { return fmt.Sprintf("portal_%d", hopID) }

// chainBridgeReverseTag 是 reverse.bridges 条目的 tag（PoC 中为 "bridge"，按跳区分）。
func chainBridgeReverseTag(hopID int64) string { return fmt.Sprintf("bridge_%d", hopID) }

// SetChainPieces 注入落盘的链 piece 记录（启动时调用，重建 config.json 的依据，§21.1）。
func (m *Manager) SetChainPieces(pieces []state.ChainPiece) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chainPieces = pieces
}

// ChainPieces 返回当前链 piece 记录（main 落盘用）。
func (m *Manager) ChainPieces() []state.ChainPiece {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]state.ChainPiece, len(m.chainPieces))
	copy(out, m.chainPieces)
	return out
}

// findChainPiece 查找已记录的链 piece（重发幂等复用端口/密钥）；无记录返回 nil。
func (m *Manager) findChainPiece(hopID int64, kind string) *state.ChainPiece {
	for i := range m.chainPieces {
		if m.chainPieces[i].HopID == hopID && m.chainPieces[i].Kind == kind {
			return &m.chainPieces[i]
		}
	}
	return nil
}

// upsertChainPiece 记录/替换一个链 piece（apply 成功后调用）。
func (m *Manager) upsertChainPiece(rec state.ChainPiece) {
	for i := range m.chainPieces {
		if m.chainPieces[i].HopID == rec.HopID && m.chainPieces[i].Kind == rec.Kind {
			m.chainPieces[i] = rec
			return
		}
	}
	m.chainPieces = append(m.chainPieces, rec)
}

// deleteChainPiece 移除一个链 piece 记录（remove 成功后调用；不存在为空操作）。
func (m *Manager) deleteChainPiece(hopID int64, kind string) {
	out := m.chainPieces[:0]
	for _, p := range m.chainPieces {
		if p.HopID == hopID && p.Kind == kind {
			continue
		}
		out = append(out, p)
	}
	m.chainPieces = out
}

// mergePieces 把落盘的链 piece 记录合并进配置（重建路径：config 缺失/损坏/漂移净化，§21.1）。
func (m *Manager) mergePieces(fc fullConfig) fullConfig {
	for _, rec := range m.chainPieces {
		fc = applyChainPiece(fc, rec)
	}
	return fc
}

// mergeExpectedPieces 只重放面板期望集内的链 piece（重建专用，§docs/rebuild-xray-config-design.md）：
// 本地 chainPieces 中已删除链的残留记录（不在 ExpectedPieces）不得重放进 config，
// 否则 rebuild 会复活面板已不管理的监听，与 xray.cleanup 按期望集清理的语义互相打架。
// 返回重放后的配置与实际重放的 piece 键列表（回执汇总用）。
func (m *Manager) mergeExpectedPieces(fc fullConfig, expected []string) (fullConfig, []string) {
	want := make(map[string]bool, len(expected))
	for _, key := range expected {
		want[key] = true
	}
	replayed := []string{}
	for _, rec := range m.chainPieces {
		key := fmt.Sprintf("%s/%d", rec.Kind, rec.HopID)
		if !want[key] {
			log.Printf("xray: rebuild skip unexpected local piece %s (not in panel expected set)", key)
			continue
		}
		fc = applyChainPiece(fc, rec)
		replayed = append(replayed, key)
	}
	return fc, replayed
}

// ApplyChainHop 落地一个链跳配置件（§21.1）：渲染 → 合并 → 落盘校验 → 重启（失败回滚）。
// 幂等：同 hop_id+kind 重发替换同名 tag 的配置件，并复用已记录的端口与密钥。
// 成功返回回执上报值（portal：port/public_key/server_name；forward：port；bridge：nil）。
func (m *Manager) ApplyChainHop(p shared.ApplyChainHopPayload) (*shared.RealizedConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p.HopID == 0 {
		return nil, fmt.Errorf("hop_id 缺失")
	}
	cur, err := m.loadConfig()
	if err != nil {
		return nil, err
	}
	var realized *shared.RealizedConfig
	var rec state.ChainPiece
	switch p.Kind {
	case shared.HopKindPortal:
		realized, rec, err = m.renderPortal(p)
	case shared.HopKindBridge:
		realized, rec, err = m.renderBridge(p)
	case shared.HopKindForward:
		realized, rec, err = m.renderForward(p, cur)
	default:
		return nil, fmt.Errorf("未知链跳配置件类型: %q", p.Kind)
	}
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

// RemoveChainHop 移除一个链跳配置件（§21.1 删链逐跳反向下发）：
// 移除对应 inbound/outbound/routing 规则/reverse 段项后落盘重启；不存在视为成功（幂等）。
func (m *Manager) RemoveChainHop(hopID int64, kind string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, err := m.loadConfig()
	if err != nil {
		return err
	}
	cand, changed := removeChainPieceItems(cur, hopID, kind)
	if !changed {
		m.deleteChainPiece(hopID, kind) // 配置中已不存在，仅清记录
		return nil
	}
	if err := m.commitConfig(cand); err != nil {
		return err
	}
	if err := m.restartApply(); err != nil {
		return err
	}
	m.deleteChainPiece(hopID, kind)
	return nil
}

// restartApply 重启 xray 使落盘配置生效，失败恢复上一份并再次重启（§6 步骤 7-8）。
func (m *Manager) restartApply() error {
	if err := m.runner.Restart(context.Background()); err != nil {
		log.Printf("xray: restart apply failed: %v（回滚上一份配置并再次重启）", err)
		m.restorePrev()
		_ = m.runner.Restart(context.Background())
		return fmt.Errorf("重启失败(%v)，已回滚配置", err)
	}
	return nil
}

// renderPortal 渲染 portal piece（反向链上游机）：VLESS+Reality interconn inbound +
// reverse portal + routing（interconn → portal），对照 PoC portal.json。
// 密钥对由 agent 生成（重发复用已记录密钥），dest 走 §6 预检+白名单。
func (m *Manager) renderPortal(p shared.ApplyChainHopPayload) (*shared.RealizedConfig, state.ChainPiece, error) {
	spec := p.Portal
	if spec == nil {
		return nil, state.ChainPiece{}, fmt.Errorf("portal 配置件缺少 portal 规格")
	}
	prev := m.findChainPiece(p.HopID, p.Kind)
	tag := shared.ChainPortalTag(p.HopID)
	port, err := m.pickChainPort(spec.Port, spec.PortCandidates, prev, tag)
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	priv, pub := "", ""
	if prev != nil && prev.PrivateKey != "" {
		priv, pub = prev.PrivateKey, prev.PublicKey // 重发幂等：公钥不变，bridge 侧凭证不失效
	} else {
		if priv, pub, err = m.x25519(); err != nil {
			return nil, state.ChainPiece{}, err
		}
	}
	inbound, err := toRawTemplate(renderPortalInbound(spec, tag, port, priv))
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	// dest 预检（§6 步骤 2）：不可达按白名单逐个尝试并改写 dest/serverNames。
	if err := ensureDestReachable(inbound, p.DestCandidates); err != nil {
		return nil, state.ChainPiece{}, err
	}
	inboundRaw, err := json.Marshal(inbound)
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	reverseRaw, err := json.Marshal(map[string]any{
		"tag":    chainPortalReverseTag(p.HopID),
		"domain": spec.TunnelDomain,
	})
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	ruleRaw, err := json.Marshal(map[string]any{
		"type":        "field",
		"inboundTag":  []string{tag},
		"outboundTag": chainPortalReverseTag(p.HopID),
	})
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	rec := state.ChainPiece{
		HopID: p.HopID, Kind: p.Kind, Port: port,
		PrivateKey: priv, PublicKey: pub,
		Inbound: inboundRaw, Reverse: reverseRaw,
		Rules: []json.RawMessage{ruleRaw},
	}
	realized := &shared.RealizedConfig{
		Port:       port,
		PublicKey:  pub,
		ShortID:    spec.ShortID,
		ServerName: realityServerName(inboundRaw),
	}
	return realized, rec, nil
}

// renderPortalInbound 渲染 portal 的 VLESS+Reality interconn inbound（对照 PoC portal.json；
// 监听 0.0.0.0：隧道口有 UUID+Reality 认证，§21.1）。
func renderPortalInbound(spec *shared.PortalSpec, tag string, port int, privateKey string) map[string]any {
	return map[string]any{
		"tag": tag, "listen": "0.0.0.0", "port": port, "protocol": "vless",
		"settings": map[string]any{
			"clients":    []map[string]any{{"id": spec.TunnelUUID}},
			"decryption": "none",
		},
		"streamSettings": map[string]any{
			"network": "tcp", "security": "reality",
			"realitySettings": map[string]any{
				"show": false, "dest": spec.Dest, "xver": 0,
				"minClientVer": "0", // 26.7.11+ 缺省默认 26.3.27，会拒绝旧客户端
				"serverNames":  spec.ServerNames,
				"privateKey":   privateKey,
				"shortIds":     []string{spec.ShortID},
			},
		},
	}
}

// renderBridge 渲染 bridge piece（反向链下游机）：reverse bridge + VLESS+Reality
// interconn outbound + routing（tunnel_domain → interconn；bridge → freedom），
// 对照 PoC bridge.json。凭证全部来自载荷（对应 portal 的回执与下发值）。
func (m *Manager) renderBridge(p shared.ApplyChainHopPayload) (*shared.RealizedConfig, state.ChainPiece, error) {
	spec := p.Bridge
	if spec == nil {
		return nil, state.ChainPiece{}, fmt.Errorf("bridge 配置件缺少 bridge 规格")
	}
	if spec.PortalAddress == "" || spec.PortalPort == 0 || spec.PublicKey == "" {
		return nil, state.ChainPiece{}, fmt.Errorf("bridge 规格缺少 portal 凭证（address/port/public_key）")
	}
	tag := shared.ChainBridgeTag(p.HopID)
	outboundRaw, err := json.Marshal(renderBridgeOutbound(spec, tag))
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	reverseRaw, err := json.Marshal(map[string]any{
		"tag":    chainBridgeReverseTag(p.HopID),
		"domain": spec.TunnelDomain,
	})
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	ruleTunnel, err := json.Marshal(map[string]any{
		"type":        "field",
		"domain":      []string{"full:" + spec.TunnelDomain},
		"outboundTag": tag,
	})
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	ruleBridge, err := json.Marshal(map[string]any{
		"type":        "field",
		"inboundTag":  []string{chainBridgeReverseTag(p.HopID)},
		"outboundTag": directOutboundTag, // freedom 拨回环业务 inbound（PoC 的 out）
	})
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	rec := state.ChainPiece{
		HopID: p.HopID, Kind: p.Kind,
		Outbound: outboundRaw, Reverse: reverseRaw,
		Rules: []json.RawMessage{ruleTunnel, ruleBridge},
	}
	return nil, rec, nil // bridge 无回执字段（§21.1）
}

// renderBridgeOutbound 渲染 bridge 的 VLESS+Reality interconn outbound（对照 PoC bridge.json）。
func renderBridgeOutbound(spec *shared.BridgeSpec, tag string) map[string]any {
	return map[string]any{
		"tag": tag, "protocol": "vless",
		"settings": map[string]any{
			"vnext": []map[string]any{{
				"address": spec.PortalAddress, "port": spec.PortalPort,
				"users": []map[string]any{{"id": spec.TunnelUUID, "encryption": "none"}},
			}},
		},
		"streamSettings": map[string]any{
			"network": "tcp", "security": "reality",
			"realitySettings": map[string]any{
				"serverName":  spec.ServerName,
				"publicKey":   spec.PublicKey,
				"shortId":     spec.ShortID,
				"fingerprint": shared.FingerprintChrome,
			},
		},
	}
}

// renderForward 渲染 forward piece（入口/中间跳）：dokodemo-door 透传 inbound + 路由
// （via_tunnel_domain 非空 → 对应 reverse portal；空 → freedom 直连下一跳），
// 对照 PoC portal.json 的 entry inbound 与路由规则。
func (m *Manager) renderForward(p shared.ApplyChainHopPayload, cur fullConfig) (*shared.RealizedConfig, state.ChainPiece, error) {
	spec := p.Forward
	if spec == nil {
		return nil, state.ChainPiece{}, fmt.Errorf("forward 配置件缺少 forward 规格")
	}
	prev := m.findChainPiece(p.HopID, p.Kind)
	tag := shared.ChainForwardTag(p.HopID)
	port, err := m.pickChainPort(spec.Port, spec.PortCandidates, prev, tag)
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	outboundTag := directOutboundTag
	if spec.ViaTunnelDomain != "" {
		outboundTag = reversePortalTagByDomain(cur, spec.ViaTunnelDomain)
		if outboundTag == "" {
			return nil, state.ChainPiece{}, fmt.Errorf("via_tunnel_domain %q 对应的上游 portal 尚未就绪", spec.ViaTunnelDomain)
		}
	}
	inboundRaw, err := json.Marshal(renderForwardInbound(spec, tag, port))
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	ruleRaw, err := json.Marshal(map[string]any{
		"type":        "field",
		"inboundTag":  []string{tag},
		"outboundTag": outboundTag,
	})
	if err != nil {
		return nil, state.ChainPiece{}, err
	}
	rec := state.ChainPiece{
		HopID: p.HopID, Kind: p.Kind, Port: port,
		Inbound: inboundRaw,
		Rules:   []json.RawMessage{ruleRaw},
	}
	return &shared.RealizedConfig{Port: port}, rec, nil
}

// renderForwardInbound 渲染 forward 的 dokodemo-door 透传 inbound（对照 PoC entry inbound；
// 监听 0.0.0.0：固定目标无滥用面，§21.1）。
func renderForwardInbound(spec *shared.ForwardSpec, tag string, port int) map[string]any {
	listen := "0.0.0.0"
	if spec.LocalOnly {
		listen = "127.0.0.1"
	}
	return map[string]any{
		"tag": tag, "listen": listen, "port": port, "protocol": "dokodemo-door",
		"settings": map[string]any{
			"address": spec.TargetAddress, "port": spec.TargetPort, "network": "tcp",
		},
	}
}

// pickChainPort 挑选链 piece 监听端口：同一 piece 复用自己已记录的端口；
// 新的指定端口校验占用与段内归属（自身受管 xray 持有的端口可复用，§21），
// 未指定且无记录时从候选段挑空闲端口。tag 为本次 apply 的 inbound tag
// （同 tag 受管端口可复用，见 pickPort）。
func (m *Manager) pickChainPort(preferred int, candidates []int, prev *state.ChainPiece, tag string) (int, error) {
	if preferred != 0 {
		if prev != nil && prev.Port == preferred {
			return preferred, nil
		}
		return m.pickPort(preferred, candidates, tag)
	}
	if prev != nil && prev.Port != 0 {
		return prev.Port, nil
	}
	return m.pickPort(0, candidates, tag)
}

// realityServerName 提取 inbound realitySettings.serverNames[0]（dest 预检后的实际上报值）。
func realityServerName(inbound json.RawMessage) string {
	var probe struct {
		StreamSettings struct {
			RealitySettings struct {
				ServerNames []string `json:"serverNames"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(inbound, &probe); err != nil {
		return ""
	}
	return firstOrEmpty(probe.StreamSettings.RealitySettings.ServerNames)
}

// toRawTemplate 把渲染结果转为 ensureDestReachable 操作的模板形态。
func toRawTemplate(v map[string]any) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var tmpl map[string]json.RawMessage
	if err := json.Unmarshal(b, &tmpl); err != nil {
		return nil, err
	}
	return tmpl, nil
}

// reversePortalTagByDomain 在配置中查找 domain 匹配的 reverse portal 的 tag
// （forward 经反向隧道时的路由目标）；未找到返回空串。
func reversePortalTagByDomain(fc fullConfig, domain string) string {
	for _, raw := range fc.reverseEntries("portals") {
		var e struct {
			Tag    string `json:"tag"`
			Domain string `json:"domain"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Domain == domain {
			return e.Tag
		}
	}
	return ""
}

// --- fullConfig 的链 piece 段操作（outbounds/reverse/routing） ---

func (fc fullConfig) outbounds() []json.RawMessage {
	raw, ok := fc["outbounds"]
	if !ok {
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	return list
}

func (fc fullConfig) setOutbounds(list []json.RawMessage) {
	b, err := json.Marshal(list)
	if err != nil {
		panic(err)
	}
	fc["outbounds"] = b
}

// reverseEntries 返回 reverse 段的 portals/bridges 数组。
func (fc fullConfig) reverseEntries(key string) []json.RawMessage {
	raw, ok := fc["reverse"]
	if !ok {
		return nil
	}
	var rev map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rev); err != nil {
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(rev[key], &list); err != nil {
		return nil
	}
	return list
}

// setReverseEntries 写回 reverse 段的 portals/bridges 数组（两键均空时移除 reverse 段）。
func (fc fullConfig) setReverseEntries(key string, list []json.RawMessage) {
	rev := map[string]json.RawMessage{}
	if raw, ok := fc["reverse"]; ok {
		if err := json.Unmarshal(raw, &rev); err != nil {
			rev = map[string]json.RawMessage{}
		}
	}
	b, err := json.Marshal(list)
	if err != nil {
		panic(err)
	}
	rev[key] = b
	if isEmptyList(rev["portals"]) && isEmptyList(rev["bridges"]) {
		delete(fc, "reverse")
		return
	}
	out, err := json.Marshal(rev)
	if err != nil {
		panic(err)
	}
	fc["reverse"] = out
}

func isEmptyList(raw json.RawMessage) bool {
	var list []json.RawMessage
	return len(raw) == 0 || (json.Unmarshal(raw, &list) == nil && len(list) == 0)
}

// routingRules 返回 routing.rules 数组。
func (fc fullConfig) routingRules() []json.RawMessage {
	raw, ok := fc["routing"]
	if !ok {
		return nil
	}
	var routing map[string]json.RawMessage
	if err := json.Unmarshal(raw, &routing); err != nil {
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(routing["rules"], &list); err != nil {
		return nil
	}
	return list
}

// setRoutingRules 写回 routing.rules（保留 routing 段的其他键；规则为空时移除 routing 段）。
func (fc fullConfig) setRoutingRules(list []json.RawMessage) {
	routing := map[string]json.RawMessage{}
	if raw, ok := fc["routing"]; ok {
		if err := json.Unmarshal(raw, &routing); err != nil {
			routing = map[string]json.RawMessage{}
		}
	}
	b, err := json.Marshal(list)
	if err != nil {
		panic(err)
	}
	routing["rules"] = b
	if len(list) == 0 && len(routing) == 1 {
		delete(fc, "routing")
		return
	}
	out, err := json.Marshal(routing)
	if err != nil {
		panic(err)
	}
	fc["routing"] = out
}

// chainPieceTags 返回一个链 piece 涉及的 tag 集合（移除/幂等替换的匹配键）：
// inbounds/outbounds 按 tag 移除；routing 规则按 inboundTag/outboundTag 引用移除。
func chainPieceTags(hopID int64, kind string) (inboundTags, outboundTags map[string]bool, reverseKey string, reverseTags map[string]bool) {
	inboundTags = map[string]bool{}
	outboundTags = map[string]bool{}
	reverseTags = map[string]bool{}
	switch kind {
	case sharedEndpointPieceKind:
		inboundTags[shared.SharedEndpointTag(hopID)] = true
	case shared.HopKindPortal:
		inboundTags[shared.ChainPortalTag(hopID)] = true
		reverseKey = "portals"
		reverseTags[chainPortalReverseTag(hopID)] = true
		outboundTags[chainPortalReverseTag(hopID)] = true // routing 的 outboundTag 引用
	case shared.HopKindBridge:
		outboundTags[shared.ChainBridgeTag(hopID)] = true
		reverseKey = "bridges"
		reverseTags[chainBridgeReverseTag(hopID)] = true
		inboundTags[chainBridgeReverseTag(hopID)] = true // routing 的 inboundTag 引用
	case shared.HopKindForward:
		inboundTags[shared.ChainForwardTag(hopID)] = true
	}
	return inboundTags, outboundTags, reverseKey, reverseTags
}

// removeChainPieceItems 返回移除指定链 piece 全部配置件的候选配置；
// 第二个返回值表示是否有配置件被移除（幂等判断用）。
func removeChainPieceItems(fc fullConfig, hopID int64, kind string) (fullConfig, bool) {
	inboundTags, outboundTags, reverseKey, reverseTags := chainPieceTags(hopID, kind)
	changed := false
	nc := fc.clone()

	if len(inboundTags) > 0 {
		out := make([]json.RawMessage, 0, len(nc.inbounds()))
		for _, raw := range nc.inbounds() {
			if inboundTags[inboundTag(raw)] {
				changed = true
				continue
			}
			out = append(out, raw)
		}
		nc.setInbounds(out)
	}

	var keptOut []json.RawMessage
	for _, raw := range nc.outbounds() {
		var p struct {
			Tag string `json:"tag"`
		}
		json.Unmarshal(raw, &p)
		if outboundTags[p.Tag] || kind == sharedEndpointPieceKind && strings.HasPrefix(p.Tag, sharedEndpointRoutePrefix(hopID)) {
			changed = true
			continue
		}
		keptOut = append(keptOut, raw)
	}
	if changed || len(keptOut) != len(nc.outbounds()) {
		nc.setOutbounds(keptOut)
	}

	if reverseKey != "" {
		var keptRev []json.RawMessage
		for _, raw := range nc.reverseEntries(reverseKey) {
			var p struct {
				Tag string `json:"tag"`
			}
			json.Unmarshal(raw, &p)
			if reverseTags[p.Tag] {
				changed = true
				continue
			}
			keptRev = append(keptRev, raw)
		}
		nc.setReverseEntries(reverseKey, keptRev)
	}

	var keptRules []json.RawMessage
	for _, raw := range nc.routingRules() {
		if ruleReferences(raw, inboundTags, outboundTags) {
			changed = true
			continue
		}
		keptRules = append(keptRules, raw)
	}
	nc.setRoutingRules(keptRules)

	return nc, changed
}

// ruleReferences 报告 routing 规则是否引用了给定 tag 集合（inboundTag 交集或 outboundTag 命中）。
func ruleReferences(raw json.RawMessage, inboundTags, outboundTags map[string]bool) bool {
	var rule struct {
		InboundTag  []string `json:"inboundTag"`
		OutboundTag string   `json:"outboundTag"`
	}
	if err := json.Unmarshal(raw, &rule); err != nil {
		return false
	}
	for _, t := range rule.InboundTag {
		if inboundTags[t] {
			return true
		}
	}
	return outboundTags[rule.OutboundTag]
}

// applyChainPiece 返回"移除同 piece 旧件后并入新件"的候选配置（幂等：重发不产生重复项）。
func applyChainPiece(fc fullConfig, rec state.ChainPiece) fullConfig {
	nc, _ := removeChainPieceItems(fc, rec.HopID, rec.Kind)
	if len(rec.Inbound) > 0 {
		nc = nc.upsertInbound(inboundTag(rec.Inbound), rec.Inbound)
	}
	if len(rec.Outbound) > 0 {
		var p struct {
			Tag string `json:"tag"`
		}
		json.Unmarshal(rec.Outbound, &p)
		out := make([]json.RawMessage, 0, len(nc.outbounds())+1)
		for _, raw := range nc.outbounds() {
			var q struct {
				Tag string `json:"tag"`
			}
			json.Unmarshal(raw, &q)
			if q.Tag != p.Tag {
				out = append(out, raw)
			}
		}
		out = append(out, rec.Outbound)
		nc.setOutbounds(out)
	}
	if len(rec.Outbounds) > 0 {
		out := append([]json.RawMessage(nil), nc.outbounds()...)
		out = append(out, rec.Outbounds...)
		nc.setOutbounds(out)
	}
	if len(rec.Reverse) > 0 {
		_, _, reverseKey, _ := chainPieceTags(rec.HopID, rec.Kind)
		nc.setReverseEntries(reverseKey, append(nc.reverseEntries(reverseKey), rec.Reverse))
	}
	if len(rec.Rules) > 0 {
		nc.setRoutingRules(append(nc.routingRules(), rec.Rules...))
	}
	return nc
}
