package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/xtls/xray-core/app/proxyman/command"
	statscommand "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"

	"lattix/shared"
)

// rpcTimeout 是单次 gRPC 调用的超时。
const rpcTimeout = 5 * time.Second

// HotClient 是 xray gRPC API 客户端（§6 热操作主路径）：
// AddInbound / AlterInbound（AddUser/RemoveUserOperation，零重启增删用户）/ RemoveInbound，
// 以及 StatsService 流量计数器查询（§13 遥测）。
type HotClient struct {
	addr string
}

// NewHotClient 创建指向 api inbound（dokodemo-door）的客户端。
func NewHotClient(addr string) *HotClient {
	return &HotClient{addr: addr}
}

func (c *HotClient) callConn(fn func(ctx context.Context, conn *grpc.ClientConn) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	conn, err := grpc.NewClient(c.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(ctx, conn)
}

func (c *HotClient) call(fn func(ctx context.Context, h command.HandlerServiceClient) error) error {
	return c.callConn(func(ctx context.Context, conn *grpc.ClientConn) error {
		return fn(ctx, command.NewHandlerServiceClient(conn))
	})
}

// QueryStats 拉取 xray 全部流量计数器（计数器名 → 自 xray 启动的累计值，§13）。
func (c *HotClient) QueryStats() (map[string]int64, error) {
	out := map[string]int64{}
	err := c.callConn(func(ctx context.Context, conn *grpc.ClientConn) error {
		resp, err := statscommand.NewStatsServiceClient(conn).QueryStats(ctx, &statscommand.QueryStatsRequest{})
		if err != nil {
			return err
		}
		for _, s := range resp.GetStat() {
			out[s.GetName()] = s.GetValue()
		}
		return nil
	})
	return out, err
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

// AddUser 向指定 inbound 热加入一个用户（AlterInbound AddUserOperation，零重启，§6）。
// 仅 vless/vmess/trojan 支持热操作；其余协议返回错误，由 Manager 回退重启兜底。
func (c *HotClient) AddUser(tag string, p shared.UserNodeParams, uuid string) error {
	return c.alterUser(tag, p, uuid, true)
}

// RemoveUser 从指定 inbound 热移除一个用户（RemoveUserOperation 按 email 匹配）。
func (c *HotClient) RemoveUser(tag string, p shared.UserNodeParams, uuid string) error {
	return c.alterUser(tag, p, uuid, false)
}

func (c *HotClient) alterUser(tag string, p shared.UserNodeParams, uuid string, add bool) error {
	if p.Protocol != shared.ProtocolVLESS &&
		p.Protocol != shared.ProtocolVMess && p.Protocol != shared.ProtocolTrojan {
		return fmt.Errorf("协议 %s 不支持热操作用户", p.Protocol)
	}
	return c.call(func(ctx context.Context, h command.HandlerServiceClient) error {
		var op *serial.TypedMessage
		if add {
			op = serial.ToTypedMessage(&command.AddUserOperation{
				User: &protocol.User{
					Email:   uuid,
					Account: userAccount(p, uuid),
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

// userAccount 按协议构造热加用户的 account（与 config 文件中的用户条目一致）。
func userAccount(p shared.UserNodeParams, uuid string) *serial.TypedMessage {
	switch p.Protocol {
	case shared.ProtocolVMess:
		return serial.ToTypedMessage(&vmess.Account{Id: uuid})
	case shared.ProtocolTrojan:
		return serial.ToTypedMessage(&trojan.Account{Password: uuid})
	default: // vless
		return serial.ToTypedMessage(&vless.Account{Id: uuid, Flow: p.Flow})
	}
}
