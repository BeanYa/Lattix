package extsub

import (
	"testing"

	"lattix/backend/internal/store"
)

func expiry(v int64) *int64 { return &v }

func TestMergeUserTraffic(t *testing.T) {
	cases := []struct {
		name     string
		panel    Traffic
		attached []store.UserExternalSubscriptionJoined
		want     Traffic
	}{
		{"无外部订阅", Traffic{Upload: 300, Download: 0, Total: 500, Expire: expiry(1)}, nil,
			Traffic{Upload: 300, Download: 0, Total: 500, Expire: expiry(1)}},
		{"叠加", Traffic{Upload: 300, Download: 0, Total: 500},
			[]store.UserExternalSubscriptionJoined{{Mode: store.ExtSubModeStack, Upload: 100, Download: 0, Total: 200, Expire: expiry(2)}},
			Traffic{Upload: 400, Download: 0, Total: 700, Expire: expiry(2)}},
		{"并入", Traffic{Upload: 300, Download: 0, Total: 500, Expire: expiry(1)},
			[]store.UserExternalSubscriptionJoined{{Mode: store.ExtSubModeMerge, Upload: 100, Download: 0, Total: 200, Expire: expiry(2)}},
			Traffic{Upload: 400, Download: 0, Total: 500, Expire: expiry(1)}},
		{"附加", Traffic{Upload: 300, Download: 0, Total: 500},
			[]store.UserExternalSubscriptionJoined{{Mode: store.ExtSubModeNodes, Upload: 100, Download: 0, Total: 200}},
			Traffic{Upload: 300, Download: 0, Total: 500}},
		{"未知额度不参与", Traffic{Upload: 300, Download: 0, Total: 500},
			[]store.UserExternalSubscriptionJoined{
				{Mode: store.ExtSubModeStack, Upload: 100, Download: 0, Total: 0},
				{Mode: store.ExtSubModeMerge, Upload: 50, Download: 0, Total: 0},
			},
			Traffic{Upload: 300, Download: 0, Total: 500}},
		{"混合", Traffic{Upload: 300, Download: 100, Total: 500, Expire: expiry(1)},
			[]store.UserExternalSubscriptionJoined{
				{Mode: store.ExtSubModeMerge, Upload: 100, Download: 0, Total: 200, Expire: expiry(2)},
				{Mode: store.ExtSubModeStack, Upload: 100, Download: 50, Total: 200, Expire: expiry(3)},
				{Mode: store.ExtSubModeNodes, Upload: 9, Download: 9, Total: 9},
			},
			Traffic{Upload: 500, Download: 150, Total: 700, Expire: expiry(1)}},
		{"叠加取最早到期", Traffic{Upload: 0, Download: 0, Total: 0, Expire: expiry(5)},
			[]store.UserExternalSubscriptionJoined{{Mode: store.ExtSubModeStack, Upload: 1, Download: 0, Total: 200, Expire: expiry(3)}},
			Traffic{Upload: 1, Download: 0, Total: 200, Expire: expiry(3)}},
	}
	for _, c := range cases {
		got := MergeUserTraffic(c.panel, c.attached)
		if got.Upload != c.want.Upload || got.Download != c.want.Download || got.Total != c.want.Total {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
		if (got.Expire == nil) != (c.want.Expire == nil) {
			t.Errorf("%s: expire nil mismatch, got %v want %v", c.name, got.Expire, c.want.Expire)
		}
		if got.Expire != nil && c.want.Expire != nil && *got.Expire != *c.want.Expire {
			t.Errorf("%s: expire = %d, want %d", c.name, *got.Expire, *c.want.Expire)
		}
	}
}
