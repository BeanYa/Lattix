// Package xray 实现 agent 侧 xray 管理（设计文档 §6）：
// 模板填充 → xray run -test 校验 → 原子落盘（agent 独占管理 config.json）→
// gRPC 热操作（主路径）→ systemctl/子进程重启（兜底）→ 回滚（重启再失败）。
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
	"sync"

	"lattix/shared"
)

// Manager 管理一台服务器上的 xray：配置文件、热操作、服务重启。
type Manager struct {
	bin        string // xray 二进制路径
	configPath string // config.json 路径（agent 独占管理，§6）
	apiAddr    string // gRPC API 地址（同时决定骨架配置中 api inbound 的端口）
	hot        *HotClient
	runner     Runner

	mu sync.Mutex // 串行化一切配置变更，避免并发写坏 config.json
}

// NewManager 创建 xray 管理器。
func NewManager(bin, configPath, apiAddr string, runner Runner) *Manager {
	return &Manager{
		bin:        bin,
		configPath: configPath,
		apiAddr:    apiAddr,
		hot:        NewHotClient(apiAddr),
		runner:     runner,
	}
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
func (m *Manager) ApplyNode(nodeID int64, vc shared.VirtualConfig, userUUIDs []string) (*shared.RealizedConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tag := nodeTag(nodeID)
	// 1. 填充模板占位符（§7）
	inbound, realized, err := m.fillTemplate(tag, vc, userUUIDs)
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

	tag := nodeTag(nodeID)
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

// AddUser 向该服务器所有节点 inbound 热加入一个用户（§5、§8）。
func (m *Manager) AddUser(uuid string) error {
	return m.mutateUser(uuid, true)
}

// RemoveUser 从该服务器所有节点 inbound 热移除一个用户（§5、§8）。
func (m *Manager) RemoveUser(uuid string) error {
	return m.mutateUser(uuid, false)
}

func (m *Manager) mutateUser(uuid string, add bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, err := m.loadConfig()
	if err != nil {
		return err
	}
	cand, tags := cur.mutateClients(uuid, add)
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
			err = m.hot.AddUser(tag, uuid)
		} else {
			err = m.hot.RemoveUser(tag, uuid)
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
	return os.Rename(tmpPath, m.configPath)
}

// restorePrev 恢复上一份可用配置（§6 步骤 7）。
func (m *Manager) restorePrev() {
	if _, err := os.Stat(m.configPath + ".prev"); err == nil {
		if err := os.Rename(m.configPath+".prev", m.configPath); err != nil {
			log.Printf("xray: restore prev config: %v", err)
		}
	}
}

// loadConfig 读取 config.json；不存在或损坏时以骨架重建（agent 独占管理，§6）。
func (m *Manager) loadConfig() (fullConfig, error) {
	b, err := os.ReadFile(m.configPath)
	if errors.Is(err, os.ErrNotExist) {
		return m.skeleton(), nil
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
		return m.skeleton(), nil
	}
	return fc, nil
}

// skeleton 生成基础配置：api.listen 直接监听 gRPC（热操作通道）+ freedom outbound。
func (m *Manager) skeleton() fullConfig {
	var fc fullConfig
	if err := json.Unmarshal([]byte(fmt.Sprintf(skeletonJSON, m.apiAddr)), &fc); err != nil {
		panic(err) // 常量模板，必然合法
	}
	return fc
}

const skeletonJSON = `{
  "log": {"loglevel": "warning"},
  "api": {"tag": "api", "listen": %q, "services": ["HandlerService"]},
  "stats": {},
  "inbounds": [],
  "outbounds": [{"protocol": "freedom", "tag": "direct"}]
}`

func nodeTag(nodeID int64) string { return fmt.Sprintf("node_%d", nodeID) }
