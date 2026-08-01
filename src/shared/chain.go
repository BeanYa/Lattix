package shared

import "fmt"

// 链跳配置件类型（§21.1 piece）：apply_chain_hop/remove_chain_hop 的 kind 取值。
const (
	HopKindPortal  = "portal"  // 反向链上游机：VLESS+Reality interconn inbound + reverse portal
	HopKindBridge  = "bridge"  // 反向链下游机：reverse bridge + interconn outbound + routing
	HopKindForward = "forward" // 入口/中间跳：dokodemo-door 透传 inbound + 路由
)

// TunnelDomain 是链内 reverse 路由键（§21.1）：c<chainID>h<hopID>.lx，
// hopID 取该反向链 portal 所在跳（上游机）的 chain_hops.id。
func TunnelDomain(chainID, hopID int64) string { return fmt.Sprintf("c%dh%d.lx", chainID, hopID) }

// ChainForwardTag 是跳 forward inbound 的 tag（面板编排与 Agent 渲染两端共用）。
func ChainForwardTag(hopID int64) string { return fmt.Sprintf("chainfwd_%d", hopID) }

// ChainPortalTag 是跳 portal inbound 的 tag。
func ChainPortalTag(hopID int64) string { return fmt.Sprintf("chainportal_%d", hopID) }

// ChainBridgeTag 是跳 bridge outbound/routing 的 tag。
func ChainBridgeTag(hopID int64) string { return fmt.Sprintf("chainbr_%d", hopID) }

// SharedEndpointTag 是共享端点 inbound 的 tag。
func SharedEndpointTag(id int64) string { return fmt.Sprintf("shared_endpoint_%d", id) }
