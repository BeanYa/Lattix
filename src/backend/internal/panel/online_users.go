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
