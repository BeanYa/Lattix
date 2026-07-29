// Package ws 实现 Agent 控制通道端点（设计文档 §5）：
// Agent 携带服务器 token 拨出至 /api/agent/ws，Backend 永不主动外连 Agent。
package ws

import (
	"context"
	"errors"

	"lattix/shared"
)

// ErrOffline 表示目标服务器当前不在线（无 WS 连接，§5 在线状态由连接存在性推导）。
var ErrOffline = errors.New("ws: agent offline")
var ErrDraining = errors.New("ws: hub draining")
var ErrPanelNotActive = errors.New("ws: panel not active")
var ErrAuthentication = errors.New("ws: authentication failed")

// AgentRequester 描述 Backend 通过 WebSocket 向 Agent 发起 RPC 的能力。
type AgentRequester interface {
	// Send 向指定服务器投递一个信封；不在线时返回 ErrOffline。
	Send(ctx context.Context, serverID int64, env shared.Envelope) error
	// IsOnline 报告服务器当前是否有活跃连接。
	IsOnline(serverID int64) bool
}

type AuthResult struct {
	ServerID  int64
	Reconnect bool
}

type OpenSessionResult struct {
	IssuedToken string
	ExchangeID  string
}

// Authenticator validates the HTTP Upgrade credential and completes the
// application session after the WebSocket has been established.
type Authenticator interface {
	AuthenticateToken(ctx context.Context, token string) (AuthResult, error)
	OpenSession(ctx context.Context, auth AuthResult, p shared.SessionOpenPayload, remoteAddr string) (OpenSessionResult, error)
	CommitCredential(ctx context.Context, serverID int64, exchangeID string) error
}
