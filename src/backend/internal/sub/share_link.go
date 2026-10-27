package sub

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// ShareLink 是管理端导出的分享链接视图：与订阅交付完全一致的协议 URI
// （vless:// 等），凭据取所选用户的订阅凭据。
type ShareLink struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Link     string `json:"link"`
}

// userAccess 解析用户视角的节点/链路可用性（§16）：分组用户只按分组派生
// （allowed 恒空，单机节点不输出），非分组用户走直接分配（user_nodes +
// user_chain_assignments）。订阅条目组装与分享链接导出共用。
func (s *Server) userAccess(ctx context.Context, user *store.User) (map[int64]bool, []store.UserChainAssignment, error) {
	allowed := map[int64]bool{}
	groupIDs, err := s.st.UserGroupIDsForUser(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(groupIDs) > 0 {
		chainAssignments, err := s.st.EffectiveUserChainAssignments(ctx, user.ID)
		return allowed, chainAssignments, err
	}
	assigned, err := s.st.UserNodeIDs(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	for _, id := range assigned {
		allowed[id] = true
	}
	chainAssignments, err := s.st.UserChainAssignments(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	return allowed, chainAssignments, nil
}

// shareUser 加载并校验导出目标用户：与订阅交付同一门槛
// （过期/禁用/配额耗尽/未生效的用户不导出）。
func (s *Server) shareUser(ctx context.Context, userID int64) (*store.User, error) {
	user, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.Expired || user.Disabled || user.QuotaExhausted || user.NotYetEffective(time.Now()) {
		return nil, fmt.Errorf("用户不在订阅生效状态（过期/禁用/配额耗尽/未生效）")
	}
	return user, nil
}

// UserChainShareLink 导出指定用户视角下链路的分享链接（管理端）：以已发布
// 修订快照为准（§21），入口地址/端口 + 出口业务协议参数 + 用户订阅凭据，
// 与订阅交付一致；未发布有效修订或用户未分配时报错。
func (s *Server) UserChainShareLink(ctx context.Context, userID, chainID int64) (ShareLink, error) {
	user, err := s.shareUser(ctx, userID)
	if err != nil {
		return ShareLink{}, err
	}
	allowed, chainAssignments, err := s.userAccess(ctx, user)
	if err != nil {
		return ShareLink{}, err
	}
	chain, err := s.st.ChainByID(ctx, chainID)
	if err != nil {
		return ShareLink{}, err
	}
	assignment := store.UserChainAssignment{}
	hasAssignment := false
	for _, candidate := range chainAssignments {
		if candidate.ChainID == chainID {
			assignment, hasAssignment = candidate, true
			break
		}
	}
	if chain.EndpointID != 0 && !hasAssignment {
		return ShareLink{}, fmt.Errorf("用户未分配该链路，缺少订阅凭据")
	}
	item, err := s.chainSubscriptionItem(ctx, *chain, allowed, assignment)
	if err != nil {
		return ShareLink{}, err
	}
	return shareLinkFromItem(item.node, item.rc, item.credential, user.UUID)
}

// UserNodeShareLink 导出指定用户视角下单机节点的分享链接（管理端）：
// 与订阅交付一致；链路出口节点只能经链路消费，不单独导出。
func (s *Server) UserNodeShareLink(ctx context.Context, userID, nodeID int64) (ShareLink, error) {
	user, err := s.shareUser(ctx, userID)
	if err != nil {
		return ShareLink{}, err
	}
	allowed, _, err := s.userAccess(ctx, user)
	if err != nil {
		return ShareLink{}, err
	}
	node, err := s.st.NodeByID(ctx, nodeID)
	if err != nil {
		return ShareLink{}, err
	}
	exitIDs, err := s.st.ChainExitNodeIDs(ctx)
	if err != nil {
		return ShareLink{}, err
	}
	if exitIDs[nodeID] {
		return ShareLink{}, fmt.Errorf("节点是链路出口，请导出对应链路的分享链接")
	}
	if !allowed[nodeID] {
		return ShareLink{}, fmt.Errorf("节点未分配给该用户")
	}
	var rc shared.RealizedConfig
	if err := json.Unmarshal(node.RealizedConfig, &rc); err != nil {
		return ShareLink{}, fmt.Errorf("节点生效值不可用: %w", err)
	}
	return shareLinkFromItem(*node, rc, "", user.UUID)
}

// shareLinkFromItem 把订阅条目渲染为协议 URI；credential 为空时回退用户 UUID。
func shareLinkFromItem(n store.Node, rc shared.RealizedConfig, credential, userUUID string) (ShareLink, error) {
	if credential == "" {
		credential = userUUID
	}
	link, ok := buildShareLink(n, rc, credential)
	if !ok {
		return ShareLink{}, fmt.Errorf("协议 %s 不支持分享链接", n.Protocol)
	}
	return ShareLink{Protocol: n.Protocol, Address: n.ServerAddress, Port: rc.Port, Link: link}, nil
}
