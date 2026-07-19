// Package state 管理 agent 本地状态：hello 换发的长期凭证（设计文档 §11）。
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// State 是 agent 落盘的本地状态。
type State struct {
	Token    string `json:"token"`     // 长期服务器 token（hello 换发）
	ServerID int64  `json:"server_id"` // 面板分配的服务器 id
}

// Load 读取状态文件；不存在或为空时返回零值 State（首次启动）。
func Load(path string) (State, error) {
	var st State
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if len(b) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	return st, nil
}

// Save 原子写入状态文件（tmp + rename，0600）。
func Save(path string, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
