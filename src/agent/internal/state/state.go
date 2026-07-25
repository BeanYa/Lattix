// Package state 管理 agent 本地状态：hello 换发的长期凭证（设计文档 §11）
// 与链跳配置件记录（§21.1，重启重建 config.json 与重发幂等的依据）。
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// State 是 agent 落盘的本地状态。
type State struct {
	Token       string       `json:"token"`     // 长期服务器 token（hello 换发）
	ServerID    int64        `json:"server_id"` // 面板分配的服务器 id
	ChainPieces []ChainPiece `json:"chain_pieces,omitempty"`
}

// ChainPiece 是一个链跳配置件（§21.1 piece）的落盘记录：
// 渲染后的配置件原样保存，agent 重启重建 config.json 时与节点同等并入（§17 净化路径同理）；
// Port/PrivateKey/PublicKey 供重发幂等复用（端口与公钥不变，下游 bridge 凭证不失效）。
type ChainPiece struct {
	HopID      int64             `json:"hop_id"`
	Kind       string            `json:"kind"` // portal|bridge|forward
	Port       int               `json:"port,omitempty"`        // portal/forward：已分配端口
	PrivateKey string            `json:"private_key,omitempty"` // portal：Reality 私钥（不出本机）
	PublicKey  string            `json:"public_key,omitempty"`  // portal：对应公钥（回执值）
	Inbound    json.RawMessage   `json:"inbound,omitempty"`     // portal/forward 的 inbound
	Outbound   json.RawMessage   `json:"outbound,omitempty"`    // bridge 的 interconn outbound
	Reverse    json.RawMessage   `json:"reverse,omitempty"`     // reverse.portals/bridges 条目
	Rules      []json.RawMessage `json:"rules,omitempty"`       // routing 规则
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
