package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lattix/shared"
)

// extractPrevInbound 提取备份 inbound 的当前生效值（重建时复用，保证客户端不断连）：
// 监听端口、Reality 私钥（streamSettings.realitySettings.privateKey）、
// VLESS Encryption decryption（settings.decryption）。
func extractPrevInbound(raw json.RawMessage) (port int, privateKey, decryption string) {
	var ib struct {
		Port     int `json:"port"`
		Settings struct {
			Decryption string `json:"decryption"`
		} `json:"settings"`
		StreamSettings struct {
			RealitySettings struct {
				PrivateKey string `json:"privateKey"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(raw, &ib); err != nil {
		return 0, "", ""
	}
	return ib.Port, ib.StreamSettings.RealitySettings.PrivateKey, ib.Settings.Decryption
}

// rebuildInbound 以保留模式重渲染一个 node inbound（重建专用，§docs/rebuild-xray-config-design.md）：
// 端口/私钥/decryption 优先复用 prev（备份）中的当前生效值——占位符预替换后
// fillTemplate 不再重新生成；prev 缺失时回退 fillTemplate 生成路径。
// minClientVer 由 fillTemplate 内 pinRealityMinClientVer 兜底注入。
func (m *Manager) rebuildInbound(tag string, vc shared.VirtualConfig, userUUIDs, destCandidates []string, portCandidates []int, prev json.RawMessage) (json.RawMessage, *shared.RealizedConfig, error) {
	port, privateKey, decryption := extractPrevInbound(prev)
	t := string(vc.Template)
	if privateKey != "" {
		t = strings.ReplaceAll(t, shared.PlaceholderRealityPrivateKey, privateKey)
	}
	if decryption != "" {
		t = strings.ReplaceAll(t, shared.PlaceholderVLessDecryption, decryption)
	}
	if port == 0 {
		var err error
		if port, err = m.pickPort(vc.Port, portCandidates); err != nil {
			return nil, nil, err
		}
	}
	rebuilt := vc
	rebuilt.Template = json.RawMessage(t)
	return m.fillTemplate(port, tag, rebuilt, userUUIDs, destCandidates, portCandidates)
}

// rebuildBackupSuffix 是重建前备份文件的缀名（失败回滚源）。
const rebuildBackupSuffix = ".rebuild.bak"

// RebuildXray 重建 xray.json（xray.rebuild，§docs/rebuild-xray-config-design.md）：
// 停止 xray → 备份 → 按面板下发节点规格重渲染 + 本地 chain piece 重放 →
// xray run -test 校验 → 原子落盘 → 重启 → 自检（期望 tag/piece 齐全 + 进程存活）。
// 任一步失败：恢复备份并重启，回执错误且 result.RolledBack=true；
// 回滚本身失败时错误信息显式标注备份路径（需人工处理）。
func (m *Manager) RebuildXray(p shared.RebuildXrayPayload) (*shared.RebuildXrayResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := &shared.RebuildXrayResult{
		RebuiltInbounds: []shared.RebuiltInbound{},
		RebuiltPieces:   []string{},
	}
	// 1. 停止 xray。
	if err := m.runner.Stop(context.Background()); err != nil {
		return result, fmt.Errorf("停止 xray 失败: %w", err)
	}
	// 2. 备份当前配置。
	backupPath := m.configPath + rebuildBackupSuffix
	hadPrev := false
	if b, err := os.ReadFile(m.configPath); err == nil {
		hadPrev = true
		if err := os.WriteFile(backupPath, b, 0o600); err != nil {
			_ = m.runner.Restart(context.Background())
			return result, fmt.Errorf("备份 xray.json 失败: %w", err)
		}
		if err := os.Chmod(backupPath, 0o600); err != nil {
			log.Printf("xray: secure rebuild backup: %v", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = m.runner.Restart(context.Background())
		return result, fmt.Errorf("读取 xray.json 失败: %w", err)
	}
	// 统一回滚：恢复备份（或无备份时删除新配置）→ 重启 → 更新漂移基线。
	rollback := func(cause error) (*shared.RebuildXrayResult, error) {
		result.RolledBack = true
		if hadPrev {
			if rerr := os.Rename(backupPath, m.configPath); rerr != nil {
				_ = m.runner.Restart(context.Background())
				return result, fmt.Errorf("重建失败：%v；回滚失败（备份位于 %s，需人工处理）: %w", cause, backupPath, rerr)
			}
			if cerr := os.Chmod(m.configPath, 0o600); cerr != nil {
				log.Printf("xray: secure restored config: %v", cerr)
			}
		} else {
			if rerr := os.Remove(m.configPath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				_ = m.runner.Restart(context.Background())
				return result, fmt.Errorf("重建失败：%v；回滚失败（清理新配置失败）: %w", cause, rerr)
			}
		}
		if rerr := m.runner.Restart(context.Background()); rerr != nil {
			return result, fmt.Errorf("重建失败：%v；且回滚后重启 xray 失败: %w", cause, rerr)
		}
		if b, err := os.ReadFile(m.configPath); err == nil {
			m.lastHash = hashBytes(b)
		}
		m.drifted = false
		return result, fmt.Errorf("重建失败：%v（已恢复备份 xray.json 并重启）", cause)
	}
	// 3. 重建候选：骨架 + 重渲染 node inbound（复用备份生效值）+ chain piece 重放。
	prevByTag := map[string]json.RawMessage{}
	if hadPrev {
		if b, err := os.ReadFile(backupPath); err == nil {
			var prev fullConfig
			if json.Unmarshal(b, &prev) == nil {
				for _, raw := range prev.inbounds() {
					prevByTag[inboundTag(raw)] = raw
				}
			}
		}
	}
	cand := m.skeleton()
	for _, spec := range p.Nodes {
		tag := shared.NodeTag(spec.NodeID)
		inbound, _, err := m.rebuildInbound(tag, spec.Config, spec.UserUUIDs,
			spec.DestCandidates, spec.PortCandidates, prevByTag[tag])
		if err != nil {
			return rollback(fmt.Errorf("重建节点 %s 失败: %w", tag, err))
		}
		cand = cand.upsertInbound(tag, inbound)
	}
	cand = m.mergePieces(cand)
	// 3b. 规范化扫描：补全 xray 版本升级后缺省的字段（minClientVer 等，全配置生效）。
	cand = normalizeRebuiltConfig(cand)
	// 4-5. 校验 + 原子落盘。
	b, err := json.MarshalIndent(cand, "", "  ")
	if err != nil {
		return rollback(fmt.Errorf("序列化重建配置失败: %w", err))
	}
	if err := m.writeValidated(b); err != nil {
		return rollback(err)
	}
	// 6. 重启。
	if err := m.runner.Restart(context.Background()); err != nil {
		return rollback(fmt.Errorf("重启 xray 失败: %w", err))
	}
	// 7. 自检。
	if missing := m.selfCheck(p); len(missing) > 0 {
		return rollback(fmt.Errorf("重建后自检缺失: %s", strings.Join(missing, ", ")))
	}
	if !m.runner.IsRunning(context.Background()) {
		return rollback(fmt.Errorf("重建后 xray 未在运行"))
	}
	// 8. 成功：清理备份，汇总回执。
	if hadPrev {
		if err := os.Remove(backupPath); err != nil {
			log.Printf("xray: remove rebuild backup: %v", err)
		}
	}
	for _, raw := range cand.inbounds() {
		result.RebuiltInbounds = append(result.RebuiltInbounds, shared.RebuiltInbound{
			Tag: inboundTag(raw), Port: inboundPort(raw), Kind: inboundProtocol(raw),
		})
	}
	for _, rec := range m.chainPieces {
		result.RebuiltPieces = append(result.RebuiltPieces, fmt.Sprintf("%s/%d", rec.Kind, rec.HopID))
	}
	log.Printf("xray: rebuild done inbounds=%d pieces=%d", len(result.RebuiltInbounds), len(result.RebuiltPieces))
	return result, nil
}

// inboundProtocol 提取 inbound 的协议（重建结果展示用）。
func inboundProtocol(raw json.RawMessage) string {
	var p struct {
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	return p.Protocol
}

// normalizers 是按序应用的全配置规范化器（重建专用，§docs/rebuild-xray-config-design.md）：
// 用于补全 xray 版本升级后缺省/新增的字段。每个规范化器必须幂等；
// 未来升级后出现同类问题，只需在此追加一个函数。
var normalizers = []func(fullConfig) fullConfig{
	normalizeRealityMinClientVer,
}

// normalizeRebuiltConfig 依次应用全部规范化器（跳过配置为 nil 的情况）。
func normalizeRebuiltConfig(fc fullConfig) fullConfig {
	for _, n := range normalizers {
		if n == nil {
			continue
		}
		fc = n(fc)
	}
	return fc
}

// normalizeRealityMinClientVer 遍历全部 inbound（含重放的链路/共享端点 piece），
// 为缺少 minClientVer 的 realitySettings 注入 "0"（复用 pinRealityMinClientVer；
// node 重渲染经 fillTemplate 已注入，此处幂等）。显式值原样保留。
func normalizeRealityMinClientVer(fc fullConfig) fullConfig {
	list := fc.inbounds()
	out := make([]json.RawMessage, 0, len(list))
	changed := false
	for _, raw := range list {
		var ib map[string]json.RawMessage
		if err := json.Unmarshal(raw, &ib); err != nil {
			out = append(out, raw)
			continue
		}
		before, _ := json.Marshal(ib)
		pinRealityMinClientVer(ib)
		after, _ := json.Marshal(ib)
		if string(before) != string(after) {
			changed = true
			out = append(out, after)
			continue
		}
		out = append(out, raw)
	}
	if !changed {
		return fc
	}
	nc := fc.clone()
	nc.setInbounds(out)
	return nc
}

// selfCheck 核对重建后配置：期望 inbound tag 全部存在、期望 piece 全部在
// chainPieces 记录中；返回缺失项列表（空 = 通过）。
func (m *Manager) selfCheck(p shared.RebuildXrayPayload) []string {
	cur, err := m.loadConfig()
	if err != nil {
		return []string{fmt.Sprintf("重新加载配置失败: %v", err)}
	}
	present := map[string]bool{}
	for _, raw := range cur.inbounds() {
		present[inboundTag(raw)] = true
	}
	var missing []string
	for _, tag := range p.ExpectedInboundTags {
		if !present[tag] {
			missing = append(missing, tag)
		}
	}
	have := map[string]bool{}
	for _, rec := range m.chainPieces {
		have[fmt.Sprintf("%s/%d", rec.Kind, rec.HopID)] = true
	}
	for _, key := range p.ExpectedPieces {
		if !have[key] {
			missing = append(missing, key)
		}
	}
	return missing
}

// writeValidated 校验并原子落盘重建配置（复用 commitConfig 的临时文件 +
// xray run -test 校验路径；不走 .prev 备份——重建自有 .rebuild.bak 回滚语义）。
func (m *Manager) writeValidated(b []byte) error {
	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if out, err := exec.Command(m.bin, "run", "-test", "-config", tmpPath).CombinedOutput(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("xray 配置校验失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmpPath, m.configPath); err != nil {
		return err
	}
	if err := os.Chmod(m.configPath, 0o600); err != nil {
		return err
	}
	m.lastHash = hashBytes(b)
	m.drifted = false
	return nil
}
