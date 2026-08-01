package xray

import (
	"encoding/json"
	"fmt"

	"lattix/agent/internal/state"
	"lattix/shared"
)

// CleanupXray 清理 config.json 中未被面板有效管理的配置件（xray.cleanup，§docs/xray-cleanup-design.md）：
// 面板下发期望的 inbound tag 与 piece key 集合，agent 对比自身 config.json 与
// chainPieces 记录计算差异；DryRun 只报告，执行走 commitConfig + restartApply 流水线。
// 幂等：无差异时直接返回空结果，不落盘不重启。
func (m *Manager) CleanupXray(p shared.CleanupXrayPayload) (*shared.CleanupXrayResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, err := m.loadConfig()
	if err != nil {
		return nil, err
	}
	expectedTags := make(map[string]bool, len(p.ExpectedInboundTags)+1)
	for _, tag := range p.ExpectedInboundTags {
		expectedTags[tag] = true
	}
	expectedPieces := make(map[string]bool, len(p.ExpectedPieces))
	for _, key := range p.ExpectedPieces {
		expectedPieces[key] = true
	}

	result := &shared.CleanupXrayResult{}

	// 1. inbounds：期望集之外的全部移除（api 为 agent 基础设施，恒保留；
	// 非受管前缀的未知 inbound 同样移除——agent 独占管理 config.json，无合法残留）。
	out := make([]json.RawMessage, 0, len(cur.inbounds()))
	for _, raw := range cur.inbounds() {
		tag := inboundTag(raw)
		if tag == "api" || expectedTags[tag] {
			out = append(out, raw)
			continue
		}
		result.RemovedInbounds = append(result.RemovedInbounds, shared.CleanupInbound{Tag: tag, Port: inboundPort(raw)})
	}
	if len(result.RemovedInbounds) > 0 {
		nc := cur.clone()
		nc.setInbounds(out)
		cur = nc
	}

	// 2. chainPieces 记录：期望集之外的 piece 连 outbound/reverse/routing 一并移除并删除记录
	// （否则 agent 重启重建 config.json 时经 mergePieces 复活）。
	keptPieces := make([]state.ChainPiece, 0, len(m.chainPieces))
	for _, rec := range m.chainPieces {
		key := fmt.Sprintf("%s/%d", rec.Kind, rec.HopID)
		if expectedPieces[key] {
			keptPieces = append(keptPieces, rec)
			continue
		}
		cur, _ = removeChainPieceItems(cur, rec.HopID, rec.Kind)
		result.RemovedPieces = append(result.RemovedPieces, key)
	}

	if p.DryRun || (len(result.RemovedInbounds) == 0 && len(result.RemovedPieces) == 0) {
		return result, nil
	}
	if err := m.commitConfig(cur); err != nil {
		return nil, err
	}
	if err := m.restartApply(); err != nil {
		return nil, err
	}
	m.chainPieces = keptPieces
	return result, nil
}

// inboundPort 提取 inbound 的监听端口（清理结果展示/日志用）。
func inboundPort(raw json.RawMessage) int {
	var p struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0
	}
	return p.Port
}
