package extsub

import (
	"time"

	"lattix/backend/internal/store"
)

// Traffic 是合并后的流量统计（字节）。
type Traffic struct {
	Upload   int64
	Download int64
	Total    int64
	Expire   *int64 // Unix 秒
}

// MergeUserTraffic 按引入模式合并用户面板流量与外部订阅流量：
//   - 面板无限流量（Total=0）：不合并任何外部订阅数据——订阅保持「无限流量/长期」，
//     外部订阅仅引入节点，其统计在面板用户详情单独展示；
//   - stack：总额度与已用全部相加（独立配额池）；已到期（expire <= now）的叠加订阅
//     不再参与合并（其额度与已用一并移出，恢复管理员设定的配额视图），仅引入节点；
//   - merge：已用并入面板配额池，总额度不变（外部订阅 total 忽略）；
//   - nodes：不参与合并。
//
// 外部订阅的到期从不合并进结果：订阅的「有效期」只反映面板用户自身 expires_at。
// total=0（未知额度）的订阅不参与任何合并计算（仅引入节点）。
func MergeUserTraffic(now time.Time, panel Traffic, attached []store.UserExternalSubscriptionJoined) Traffic {
	out := panel
	if out.Total <= 0 {
		return out
	}
	for _, sub := range attached {
		if sub.Total <= 0 {
			continue
		}
		switch sub.Mode {
		case store.ExtSubModeStack:
			if sub.Expire != nil && *sub.Expire <= now.Unix() {
				continue
			}
			out.Total += sub.Total
			out.Upload += sub.Upload
			out.Download += sub.Download
		case store.ExtSubModeMerge:
			out.Upload += sub.Upload
			out.Download += sub.Download
		}
	}
	return out
}
