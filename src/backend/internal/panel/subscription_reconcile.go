package panel

import (
	"context"
	"log"
	"strings"

	"lattix/backend/internal/store"
)

// 订阅交付内容是发布时预编译的快照，而重生成队列只存在于内存：进程重启即丢失。
// 若用户切换订阅设置（如地区分组开关）后未能完成重发布（如 v0.1.13 旧库迁移故障
// 期间切换、升级重启清空队列），交付快照会永久停留在旧内容上，直到出现下一个
// 无关触发。启动对账在两个方向消除这种永久滞后：
//  1. 面板版本变化（升级/回滚）：全员重发布一次，保证交付内容总是由当前二进制
//     的生成逻辑产出；同版本重启凭 settings 标记跳过，避免无意义的快照重写。
//  2. 生成处于错误状态的用户：每次启动无条件重发布，底层故障修复后重启即自愈。
//
// 无法确定面板对外地址时整体跳过且不记录标记（下次启动重试）：重发布会把规则
// 文件等绝对链接烙进交付内容，主机名错误比内容滞后更危险，宁缺毋错。
// dev 构建没有可靠的版本身份，不做对账。
func (s *Server) reconcileSubscriptionDeliveries(ctx context.Context) {
	if s.subscriptions == nil {
		return
	}
	version := strings.TrimSpace(s.cfg.Version)
	if version == "" || version == "dev" {
		return
	}
	errored, err := s.st.SubscriptionUserIDsGenerationErrored(ctx)
	if err != nil {
		log.Printf("panel: subscription delivery reconcile: list errored users: %v", err)
	}
	stored, err := s.st.GetSetting(ctx, store.SettingSubscriptionReconcileVersion)
	fullReconcile := err != nil || stored != version
	var userIDs []int64
	if fullReconcile {
		users, err := s.st.ListUsers(ctx)
		if err != nil {
			log.Printf("panel: subscription delivery reconcile: list users: %v", err)
		} else {
			userIDs = make([]int64, 0, len(users)+len(errored))
			seen := make(map[int64]bool, len(users)+len(errored))
			for _, user := range users {
				seen[user.ID] = true
				userIDs = append(userIDs, user.ID)
			}
			for _, id := range errored {
				if !seen[id] {
					seen[id] = true
					userIDs = append(userIDs, id)
				}
			}
		}
	} else {
		userIDs = errored
	}
	if len(userIDs) == 0 {
		return
	}
	base := s.cfg.PublicURL
	if setting, err := s.st.GetSetting(ctx, store.SettingPublicURL); err == nil && setting != "" {
		base = setting
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		log.Printf("panel: skip subscription delivery reconcile (%d user(s)): public URL not configured", len(userIDs))
		return
	}
	s.subscriptions.EnqueueUsers(userIDs, base)
	if fullReconcile {
		if err := s.st.SetSetting(ctx, store.SettingSubscriptionReconcileVersion, version); err != nil {
			log.Printf("panel: subscription delivery reconcile: record version: %v", err)
		}
	}
	log.Printf("panel: subscription delivery reconcile: enqueued %d user(s) (version %s)", len(userIDs), version)
}
