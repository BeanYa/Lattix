package panel

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// IdentityResolver 把 xray 用户身份（email）映射为用户 UUID；返回空串表示无法归属。
// 共享端点身份为 access:<assignment_id>（dispatch 生成），需经 user_chain_assignments
// 换算为用户 UUID（面板注入 store 实现）；用户 UUID 身份不需要解析。
type IdentityResolver func(identity string) string

// OnlineUsersTracker 聚合各服务器上报的在线用户快照（telemetry 帧全量覆盖）。
type OnlineUsersTracker struct {
	mu        sync.Mutex
	servers   map[int64]map[string]map[string]struct{} // serverID → user → IP set
	updatedAt map[int64]time.Time
	resolve   IdentityResolver // 将 access:<assignment_id> 身份换算为用户 UUID（nil = 不换算）
	isRelay   func(ip string) bool // 过滤面板自身服务器的中继源地址（直连链路出口回环）；nil = 不过滤
}

// FreshnessWindow 是服务器快照的新鲜度窗口：窗口内无更新的服务器记录不计入。
const FreshnessWindow = 2 * time.Minute

// ApplySnapshot 用某服务器一帧全量快照替换该服务器记录（空快照 = 清除该服务器）。
// 顺带清扫超出 FreshnessWindow 的陈旧服务器记录（O(servers) 每帧），
// 查询时的新鲜度检查仍保留（ConnectionsByUser 按 updatedAt 过滤）。
func (t *OnlineUsersTracker) ApplySnapshot(serverID int64, users []shared.OnlineUserStat, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.servers == nil {
		t.servers = make(map[int64]map[string]map[string]struct{})
		t.updatedAt = make(map[int64]time.Time)
	}
	for id, updatedAt := range t.updatedAt {
		if now.Sub(updatedAt) > FreshnessWindow {
			delete(t.servers, id)
			delete(t.updatedAt, id)
		}
	}
	if len(users) == 0 {
		delete(t.servers, serverID)
		delete(t.updatedAt, serverID)
		return
	}
	byUser := make(map[string]map[string]struct{}, len(users))
	for _, u := range users {
		key := u.User
		switch {
		case strings.HasPrefix(key, "tunnel:"):
			continue // 链内部转发身份，不是业务用户
		case strings.HasPrefix(key, "access:"), strings.HasPrefix(key, "group:"):
			if t.resolve == nil {
				continue
			}
			if mapped := t.resolve(key); mapped != "" {
				key = mapped
			} else {
				continue // 分配已删除等无法归属的 access/group 身份
			}
		}
		ips := make(map[string]struct{}, len(u.IPs))
		for _, ip := range u.IPs {
			ip = shared.NormalizeIP(ip)
			if ip == "" {
				continue
			}
			// 直连多跳链路：客户端握手经 dokodemo 透传直达出口业务 inbound，出口侧
			// xray 记录的源地址是上一跳服务器的公网地址（中继 IP）而非客户端地址。
			// 按用户计入会随链路数虚高（同一面板服务器 IP 也会混入多个用户），此处剔除。
			if t.isRelay != nil && t.isRelay(ip) {
				continue
			}
			ips[ip] = struct{}{}
		}
		byUser[key] = ips
	}
	t.servers[serverID] = byUser
	t.updatedAt[serverID] = now
}

// onlineUserResolver 构造把 xray 身份映射为用户 UUID 的解析器（面板注入 store）：
// access:<assignment_id> 经 user_chain_assignments 换算为用户 UUID；group:<user_uuid>:<chain_id>
// （分组派生身份）直接取内嵌用户 UUID；其余身份返回空串（用户 UUID 原样使用；tunnel: 与
// 未知 access:/group: 由 tracker 丢弃）。
func onlineUserResolver(st *store.Store) IdentityResolver {
	return func(identity string) string {
		switch {
		case strings.HasPrefix(identity, "access:"):
			id, err := strconv.ParseInt(strings.TrimPrefix(identity, "access:"), 10, 64)
			if err != nil || id <= 0 {
				return ""
			}
			uuid, err := st.UserUUIDByAssignment(context.Background(), id)
			if err != nil {
				return ""
			}
			return uuid
		case strings.HasPrefix(identity, "group:"):
			rest := strings.TrimPrefix(identity, "group:")
			if idx := strings.LastIndex(rest, ":"); idx > 0 {
				return rest[:idx]
			}
		}
		return ""
	}
}

// serverRelayFilter 构造"面板自身服务器地址"过滤器（在线快照剔除中继源地址用）：
// 集合 = 每台服务器的地址列表 ∪ 默认地址 ∪ 学习地址 ∪ agent 上报 NIC 地址 ∪ 回环地址，
// 全部经 shared.NormalizeIP 归一化后比较（xray 上报的 IPv6 带方括号）。
// 集合按 1 分钟缓存刷新；ListServers 失败时沿用旧集合（首次失败则暂不过滤，宁可
// 显示偏多也不因 DB 抖动整体丢在线数据）。返回的过滤函数幂等（自行归一化输入）。
func serverRelayFilter(st *store.Store) func(string) bool {
	var mu sync.Mutex
	nextRefresh := time.Time{}
	set := map[string]struct{}{}
	return func(ip string) bool {
		ip = shared.NormalizeIP(ip)
		mu.Lock()
		defer mu.Unlock()
		if time.Now().After(nextRefresh) {
			next := map[string]struct{}{"127.0.0.1": {}, "::1": {}}
			if servers, err := st.ListServers(context.Background()); err == nil {
				for i := range servers {
					for addr := range store.ServerAddressSet(&servers[i]) {
						if n := shared.NormalizeIP(addr); n != "" {
							next[n] = struct{}{}
						}
					}
					for _, addr := range store.ParseServerAddresses(servers[i].NICAddresses) {
						if n := shared.NormalizeIP(addr); n != "" {
							next[n] = struct{}{}
						}
					}
				}
				set = next
			}
			nextRefresh = time.Now().Add(time.Minute)
		}
		_, ok := set[ip]
		return ok
	}
}

// ConnectionsByUser 返回某用户跨服务器去重后的在线连接数；超过 FreshnessWindow 的服务器记录不计入。
func (t *OnlineUsersTracker) ConnectionsByUser(userUUID string, now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := make(map[string]struct{})
	for serverID, byUser := range t.servers {
		updatedAt, ok := t.updatedAt[serverID]
		if !ok || now.Sub(updatedAt) > FreshnessWindow {
			continue
		}
		for ip := range byUser[userUUID] {
			seen[ip] = struct{}{}
		}
	}
	return len(seen)
}
