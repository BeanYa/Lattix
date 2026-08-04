package panel

import (
	"sync"
	"time"

	"lattix/shared"
)

// OnlineUsersTracker 聚合各服务器上报的在线用户快照（telemetry 帧全量覆盖）。
type OnlineUsersTracker struct {
	mu        sync.Mutex
	servers   map[int64]map[string]map[string]struct{} // serverID → user → IP set
	updatedAt map[int64]time.Time
}

// FreshnessWindow 是服务器快照的新鲜度窗口：窗口内无更新的服务器记录不计入。
const FreshnessWindow = 2 * time.Minute

// ApplySnapshot 用某服务器一帧全量快照替换该服务器记录（空快照 = 清除该服务器）。
func (t *OnlineUsersTracker) ApplySnapshot(serverID int64, users []shared.OnlineUserStat, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(users) == 0 {
		if t.servers != nil {
			delete(t.servers, serverID)
			delete(t.updatedAt, serverID)
		}
		return
	}
	if t.servers == nil {
		t.servers = make(map[int64]map[string]map[string]struct{})
		t.updatedAt = make(map[int64]time.Time)
	}
	byUser := make(map[string]map[string]struct{}, len(users))
	for _, u := range users {
		ips := make(map[string]struct{}, len(u.IPs))
		for _, ip := range u.IPs {
			ips[ip] = struct{}{}
		}
		byUser[u.User] = ips
	}
	t.servers[serverID] = byUser
	t.updatedAt[serverID] = now
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
