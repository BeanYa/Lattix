// Package xray 实现 agent 侧 xray 管理（设计文档 §6）：
// 模板填充 → xray run -test 校验 → 原子落盘（agent 独占管理 config.json）→
// gRPC 热操作（主路径）→ systemctl/子进程重启（兜底）→ 回滚（重启再失败）。
package xray

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"lattix/agent/internal/state"
	"lattix/shared"
)

// Manager 管理一台服务器上的 xray：配置文件、热操作、服务重启。
type Manager struct {
	bin         string // xray 二进制路径
	configPath  string // config.json 路径（agent 独占管理，§6）
	apiAddr     string // gRPC API 地址（同时决定骨架配置中 api inbound 的端口）
	hot         *HotClient
	runner      Runner
	releaseBase string // xray release 下载基址（§18，可指向镜像）
	mirrorBase  bool   // releaseBase 为显式设置的镜像：latest 不经 GitHub API 解析（§18）

	lastHash string // 最后一次由本 agent 落盘的配置哈希（§17 漂移检测基线）
	drifted  bool   // 已检出外部漂移：下一次 loadConfig 以净化配置为基（§17）

	chainPieces []state.ChainPiece // 链跳配置件落盘记录（§21.1，重启重建与重发幂等）

	mu sync.Mutex // 串行化一切配置变更，避免并发写坏 config.json
}

// defaultReleaseBase 是 xray 官方 release 下载基址。
const defaultReleaseBase = "https://github.com/XTLS/Xray-core/releases/download"

// NewManager 创建 xray 管理器。
func NewManager(bin, configPath, apiAddr string, runner Runner) *Manager {
	return &Manager{
		bin:         bin,
		configPath:  configPath,
		apiAddr:     apiAddr,
		hot:         NewHotClient(apiAddr),
		runner:      runner,
		releaseBase: defaultReleaseBase,
	}
}

// SetReleaseBase 覆盖 xray release 下载基址（§18 镜像/代理场景）。
func (m *Manager) SetReleaseBase(base string) {
	m.releaseBase = strings.TrimSuffix(base, "/")
	m.mirrorBase = true
}

// Version 返回 xray 版本与运行状态（hello 遥测，§13），尽力而为。
func (m *Manager) Version() (string, bool) {
	out, err := exec.Command(m.bin, "version").Output()
	ver := ""
	if err == nil {
		if f := strings.Fields(string(out)); len(f) >= 2 {
			ver = f[1] // "Xray 25.8.3 ..."
		}
	}
	return ver, m.runner.IsRunning(context.Background())
}

// ApplyNode 落地一个节点（§6 流水线），成功返回实际生效值（§7）。
// portCandidates 为受限直连 NAT 机的段内候选（§21，模板端口为 0 时按序挑选）。
func (m *Manager) ApplyNode(nodeID int64, vc shared.VirtualConfig, userUUIDs, destCandidates []string, portCandidates []int) (*shared.RealizedConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tag := shared.NodeTag(nodeID)
	// 1. 填充模板占位符（§7）+ dest 预检（§6 步骤 2）
	inbound, realized, err := m.fillTemplate(tag, vc, userUUIDs, destCandidates, portCandidates)
	if err != nil {
		return nil, err
	}
	// 2-4. 组装候选配置 → 校验 → 落盘
	cur, err := m.loadConfig()
	if err != nil {
		return nil, err
	}
	cand := cur.upsertInbound(tag, inbound)
	if err := m.commitConfig(cand); err != nil {
		return nil, err
	}
	// 5. gRPC 热操作（幂等：先删后加）
	if err := m.hot.ReplaceInbound(tag, inbound); err != nil {
		log.Printf("xray: hot apply %s failed: %v (fallback to restart)", tag, err)
		// 6. 热操作失败才重启
		if rerr := m.runner.Restart(context.Background()); rerr != nil {
			// 7. 重启失败：恢复上一份可用配置并再次重启
			m.restorePrev()
			_ = m.runner.Restart(context.Background())
			return nil, fmt.Errorf("热操作失败(%v)且重启失败(%v)，已回滚配置", err, rerr)
		}
	}
	return realized, nil
}

// RemoveNode 删除一个节点（幂等）。
func (m *Manager) RemoveNode(nodeID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tag := shared.NodeTag(nodeID)
	cur, err := m.loadConfig()
	if err != nil {
		return err
	}
	cand, existed := cur.removeInbound(tag)
	if !existed {
		return nil // 已不存在
	}
	if err := m.commitConfig(cand); err != nil {
		return err
	}
	if err := m.hot.RemoveInbound(tag); err != nil {
		log.Printf("xray: hot remove %s failed: %v (fallback to restart)", tag, err)
		if rerr := m.runner.Restart(context.Background()); rerr != nil {
			m.restorePrev()
			_ = m.runner.Restart(context.Background())
			return fmt.Errorf("热删除失败(%v)且重启失败(%v)，已回滚配置", err, rerr)
		}
	}
	return nil
}

// AddUser 向 params 列出的节点 inbound 热加入一个用户（§5、§8、§16）；
// params 按 tag 提供各协议条目构造参数（热操作不支持的协议自动回退重启）。
func (m *Manager) AddUser(uuid string, params map[string]shared.UserNodeParams) error {
	return m.mutateUser(uuid, true, params)
}

// RemoveUser 从 params 列出的节点 inbound 热移除一个用户（§5、§8、§16）。
func (m *Manager) RemoveUser(uuid string, params map[string]shared.UserNodeParams) error {
	return m.mutateUser(uuid, false, params)
}

// PurgeXray 停止并移除 agent 管理的 xray（uninstall purge_xray=true，§5）：
// 停止运行中的 xray 后删除二进制（含升级备份 .bak）与 config.json。
func (m *Manager) PurgeXray() {
	if err := m.runner.Stop(context.Background()); err != nil {
		log.Printf("xray: purge stop: %v", err)
	}
	for _, p := range []string{m.bin, m.bin + ".bak", m.configPath} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("xray: purge remove %s: %v", p, err)
		}
	}
}

func (m *Manager) mutateUser(uuid string, add bool, params map[string]shared.UserNodeParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, err := m.loadConfig()
	if err != nil {
		return err
	}
	cand, tags := cur.mutateClients(uuid, add, params)
	if len(tags) == 0 {
		return nil // 无节点或用户已处于目标状态
	}
	if err := m.commitConfig(cand); err != nil {
		return err
	}
	verb := "add"
	if !add {
		verb = "remove"
	}
	for _, tag := range tags {
		var err error
		if add {
			err = m.hot.AddUser(tag, params[tag], uuid)
		} else {
			err = m.hot.RemoveUser(tag, params[tag], uuid)
		}
		if err != nil {
			log.Printf("xray: hot %s user on %s failed: %v (fallback to restart)", verb, tag, err)
			if rerr := m.runner.Restart(context.Background()); rerr != nil {
				m.restorePrev()
				_ = m.runner.Restart(context.Background())
				return fmt.Errorf("热%s用户失败(%v)且重启失败(%v)，已回滚配置", verb, err, rerr)
			}
			return nil // 重启后全部生效，无需继续逐 tag 热操作
		}
	}
	return nil
}

// commitConfig 落地候选配置：写临时文件 → xray run -test 校验（§6 步骤 3）→
// 备份当前配置 → 原子替换（§6 步骤 4）。
func (m *Manager) commitConfig(cand fullConfig) error {
	b, err := json.MarshalIndent(cand, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	tmp.Close()

	// 校验失败则丢弃（§6 步骤 3）。
	if out, err := exec.Command(m.bin, "run", "-test", "-config", tmpPath).CombinedOutput(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("xray 配置校验失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// 备份上一份可用配置（回滚用，§6 步骤 7）。
	if prev, err := os.ReadFile(m.configPath); err == nil {
		if err := os.WriteFile(m.configPath+".prev", prev, 0o644); err != nil {
			os.Remove(tmpPath)
			return err
		}
	}
	if err := os.Rename(tmpPath, m.configPath); err != nil {
		return err
	}
	m.lastHash = hashBytes(b) // §17 漂移检测基线
	m.drifted = false         // 已按受管状态落盘，漂移视为修复
	return nil
}

// ConfigDrift 检测配置文件是否被外部修改（§17 reconcile）：
// 与最后一次由本 agent 落盘的哈希比对；首次调用以当前文件为基线
// （agent 停机期间的外部改动无法区分，视为基线）；文件被删除视为漂移。
func (m *Manager) ConfigDrift() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, err := os.ReadFile(m.configPath)
	if errors.Is(err, os.ErrNotExist) {
		m.drifted = m.lastHash != "" // 文件被删除视为漂移
		return m.drifted, nil
	}
	if err != nil {
		return false, err
	}
	h := hashBytes(b)
	if m.lastHash == "" {
		m.lastHash = h
		return false, nil
	}
	m.drifted = h != m.lastHash
	return m.drifted, nil
}

// hashBytes 计算配置内容的漂移检测哈希（§17）。
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// restorePrev 恢复上一份可用配置（§6 步骤 7）；同步漂移检测基线（§17），避免误报。
func (m *Manager) restorePrev() {
	if _, err := os.Stat(m.configPath + ".prev"); err == nil {
		if err := os.Rename(m.configPath+".prev", m.configPath); err != nil {
			log.Printf("xray: restore prev config: %v", err)
			return
		}
		if b, err := os.ReadFile(m.configPath); err == nil {
			m.lastHash = hashBytes(b)
		}
	}
}

// loadConfig 读取 config.json；不存在或损坏时以骨架重建（agent 独占管理，§6）。
// 重建（缺失/损坏/漂移净化）均并入落盘的链 piece 记录（§21.1，与节点同等地位）。
func (m *Manager) loadConfig() (fullConfig, error) {
	b, err := os.ReadFile(m.configPath)
	if errors.Is(err, os.ErrNotExist) {
		return m.mergePieces(m.skeleton()), nil
	}
	if err != nil {
		return nil, err
	}
	var fc fullConfig
	if err := json.Unmarshal(b, &fc); err != nil {
		broken := m.configPath + ".broken"
		log.Printf("xray: config corrupted, backup to %s and rebuild skeleton", broken)
		if rerr := os.Rename(m.configPath, broken); rerr != nil {
			return nil, rerr
		}
		return m.mergePieces(m.skeleton()), nil
	}
	if m.drifted {
		// §17 漂移修复：以骨架 + 受管节点 inbound + 链 piece 为基，丢弃外部改动的其他内容。
		san := m.skeleton()
		var kept []json.RawMessage
		for _, raw := range fc.inbounds() {
			if strings.HasPrefix(inboundTag(raw), "node_") {
				kept = append(kept, raw)
			}
		}
		san.setInbounds(kept)
		log.Printf("xray: 配置漂移修复，以净化配置为基（保留 %d 个节点 inbound）", len(kept))
		return m.mergePieces(san), nil
	}
	return fc, nil
}

// skeleton 生成基础配置：api.listen 直接监听 gRPC（热操作 + stats 查询通道）+ freedom outbound。
// policy 开启 inbound/用户级流量统计（§13）；用户级要求 clients 带 level: 0（见 clientEntry）。
func (m *Manager) skeleton() fullConfig {
	var fc fullConfig
	if err := json.Unmarshal([]byte(fmt.Sprintf(skeletonJSON, m.apiAddr)), &fc); err != nil {
		panic(err) // 常量模板，必然合法
	}
	return fc
}

const skeletonJSON = `{
  "log": {"loglevel": "warning"},
  "api": {"tag": "api", "listen": %q, "services": ["HandlerService", "StatsService"]},
  "stats": {},
  "policy": {
    "levels": {"0": {"statsUserUplink": true, "statsUserDownlink": true}},
    "system": {"statsInboundUplink": true, "statsInboundDownlink": true}
  },
  "inbounds": [],
  "outbounds": [{"protocol": "freedom", "tag": "direct"}]
}`

// QueryStats 拉取 xray 流量计数器（§13 遥测）。
func (m *Manager) QueryStats() (map[string]int64, error) {
	return m.hot.QueryStats()
}

// EnsureTelemetryFeatures 为旧版本生成的 config.json 补齐遥测所需的
// stats/policy/StatsService 配置项（缺失则落盘并重启 xray 生效，尽力而为）。
func (m *Manager) EnsureTelemetryFeatures() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, err := m.loadConfig()
	if err != nil {
		return err
	}
	cand := cur.clone()
	changed := false
	if _, ok := cand["stats"]; !ok {
		cand["stats"] = json.RawMessage(`{}`)
		changed = true
	}
	if _, ok := cand["policy"]; !ok {
		cand["policy"] = json.RawMessage(`{
  "levels": {"0": {"statsUserUplink": true, "statsUserDownlink": true}},
  "system": {"statsInboundUplink": true, "statsInboundDownlink": true}
}`)
		changed = true
	}
	if raw, ok := cand["api"]; ok {
		var api map[string]any
		if err := json.Unmarshal(raw, &api); err == nil {
			found := false
			if services, ok := api["services"].([]any); ok {
				for _, s := range services {
					if s == "StatsService" {
						found = true
					}
				}
			}
			if !found {
				services, _ := api["services"].([]any)
				api["services"] = append(services, "StatsService")
				b, err := json.Marshal(api)
				if err != nil {
					return err
				}
				cand["api"] = b
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	if err := m.commitConfig(cand); err != nil {
		return err
	}
	// policy/services 变更需重启生效。
	return m.runner.Restart(context.Background())
}
