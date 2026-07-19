package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/vless"

	"lattix/shared"
)

// rpcTimeout 是单次 gRPC 调用的超时。
const rpcTimeout = 5 * time.Second

// HotClient 是 xray gRPC API 客户端（§6 热操作主路径）：
// AddInbound / AlterInbound（AddUser/RemoveUserOperation，零重启增删用户）/ RemoveInbound。
type HotClient struct {
	addr string
}

// NewHotClient 创建指向 api inbound（dokodemo-door）的客户端。
func NewHotClient(addr string) *HotClient {
	return &HotClient{addr: addr}
}

func (c *HotClient) call(fn func(ctx context.Context, h command.HandlerServiceClient) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	conn, err := grpc.NewClient(c.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(ctx, command.NewHandlerServiceClient(conn))
}

// ReplaceInbound 幂等地下发 inbound：先移除同 tag 旧 inbound（不存在属预期），再添加。
// inbound 为填充后的 xray inbound JSON，经 infra/conf 转为 protobuf。
func (c *HotClient) ReplaceInbound(tag string, inbound json.RawMessage) error {
	return c.call(func(ctx context.Context, h command.HandlerServiceClient) error {
		_, _ = h.RemoveInbound(ctx, &command.RemoveInboundRequest{Tag: tag})
		var ic conf.InboundDetourConfig
		if err := json.Unmarshal(inbound, &ic); err != nil {
			return fmt.Errorf("解析 inbound 配置: %w", err)
		}
		pb, err := ic.Build()
		if err != nil {
			return fmt.Errorf("构建 inbound 配置: %w", err)
		}
		if _, err := h.AddInbound(ctx, &command.AddInboundRequest{Inbound: pb}); err != nil {
			return fmt.Errorf("AddInbound: %w", err)
		}
		return nil
	})
}

// RemoveInbound 热删除一个 inbound。
func (c *HotClient) RemoveInbound(tag string) error {
	return c.call(func(ctx context.Context, h command.HandlerServiceClient) error {
		if _, err := h.RemoveInbound(ctx, &command.RemoveInboundRequest{Tag: tag}); err != nil {
			return fmt.Errorf("RemoveInbound: %w", err)
		}
		return nil
	})
}

// AddUser 向指定 inbound 热加入一个 VLESS 用户（AlterInbound AddUserOperation，零重启，§6）。
func (c *HotClient) AddUser(tag, uuid string) error {
	return c.alterUser(tag, uuid, true)
}

// RemoveUser 从指定 inbound 热移除一个用户（RemoveUserOperation 按 email 匹配）。
func (c *HotClient) RemoveUser(tag, uuid string) error {
	return c.alterUser(tag, uuid, false)
}

func (c *HotClient) alterUser(tag, uuid string, add bool) error {
	return c.call(func(ctx context.Context, h command.HandlerServiceClient) error {
		var op *serial.TypedMessage
		if add {
			op = serial.ToTypedMessage(&command.AddUserOperation{
				User: &protocol.User{
					Email:   uuid,
					Account: serial.ToTypedMessage(&vless.Account{Id: uuid, Flow: shared.FlowVision}),
				},
			})
		} else {
			op = serial.ToTypedMessage(&command.RemoveUserOperation{Email: uuid})
		}
		if _, err := h.AlterInbound(ctx, &command.AlterInboundRequest{Tag: tag, Operation: op}); err != nil {
			return fmt.Errorf("AlterInbound(%s): %w", tag, err)
		}
		return nil
	})
}
