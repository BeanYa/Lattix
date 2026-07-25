package dispatch

import "testing"

func TestEvaluateAgentVersion(t *testing.T) {
	cases := []struct {
		name        string
		panel       string
		agent       string
		wantReject  bool
		wantUpgrade bool
	}{
		{"同版本", "v0.0.2", "v0.0.2", false, false},
		{"agent 落后 1（窗口内）", "v0.0.2", "v0.0.1", false, false},
		{"agent 落后 2（出窗口）", "v0.0.3", "v0.0.1", false, true},
		{"agent 落后多个", "v0.0.5", "v0.0.1", false, true},
		{"agent 更新（允许）", "v0.0.2", "v0.0.5", false, false},
		{"minor 落后 1（窗口内）", "v0.1.0", "v0.0.9", false, false},
		{"minor 落后 2（出窗口）", "v0.2.0", "v0.0.9", false, true},
		{"主版本不符（agent 旧）", "v1.0.0", "v0.9.9", true, false},
		{"主版本不符（agent 新）", "v0.0.2", "v1.0.0", true, false},
		{"dev 面板不门控", "dev", "v0.0.1", false, false},
		{"dev agent 不门控", "v0.0.3", "dev", false, false},
		{"空版本不门控", "v0.0.3", "", false, false},
		{"预发布后缀", "v0.0.3", "v0.0.1-rc1", false, true},
		{"省略 patch", "v0.1", "v0.0", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reject, upgrade := evaluateAgentVersion(c.panel, c.agent)
			if (reject != "") != c.wantReject {
				t.Errorf("reject=%q, want reject=%v", reject, c.wantReject)
			}
			if upgrade != c.wantUpgrade {
				t.Errorf("upgrade=%v, want %v", upgrade, c.wantUpgrade)
			}
		})
	}
}
