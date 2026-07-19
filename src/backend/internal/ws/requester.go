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

// Requester 隔离"发送命令"与"具体传输"（§2）。MVP 仅 WebSocket 一个实现（Hub），
// gRPC/HTTP 等其他实现属后续迭代。
type Requester interface {
	// Send 向指定服务器投递一个信封；不在线时返回 ErrOffline。
	Send(ctx context.Context, serverID int64, env shared.Envelope) error
	// IsOnline 报告服务器当前是否有活跃连接。
	IsOnline(serverID int64) bool
}

// Authenticator 校验 hello 首连认证（§5）。由业务层（dispatcher）实现并注入 Hub。
type Authenticator interface {
	// AuthenticateHello 校验 hello 载荷；remoteAddr 为 WS 对端 IP（服务器公网地址，§9）。
	// 成功返回 serverID 与响应（含换发的长期凭证，§11）。
	AuthenticateHello(ctx context.Context, p shared.HelloPayload, remoteAddr string) (serverID int64, result shared.HelloResult, err error)
}
