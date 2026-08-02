package extsub

import "lattix/backend/internal/store"

// Traffic 是合并后的流量统计（字节）。
type Traffic struct {
	Upload   int64
	Download int64
	Total    int64
	Expire   *int64 // Unix 秒
}

// MergeUserTraffic 按引入模式合并用户面板流量与外部订阅流量：
//   - stack：总额度与已用全部相加（独立配额池），到期取最早；
//   - merge：已用并入面板配额池，总额度不变（外部订阅 total 忽略）；
//   - nodes：不参与合并。
//
// total=0（未知额度）的订阅不参与任何合并计算（仅引入节点）。
func MergeUserTraffic(panel Traffic, attached []store.UserExternalSubscriptionJoined) Traffic {
	out := panel
	for _, sub := range attached {
		if sub.Total <= 0 {
			continue
		}
		switch sub.Mode {
		case store.ExtSubModeStack:
			out.Total += sub.Total
			out.Upload += sub.Upload
			out.Download += sub.Download
			if out.Expire == nil || (sub.Expire != nil && *sub.Expire < *out.Expire) {
				out.Expire = sub.Expire
			}
		case store.ExtSubModeMerge:
			out.Upload += sub.Upload
			out.Download += sub.Download
		}
	}
	return out
}
