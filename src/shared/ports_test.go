package shared

import "testing"

func TestValidatePortRanges(t *testing.T) {
	cases := []struct {
		name    string
		rs      []PortRange
		wantErr bool
	}{
		{"空", nil, false},
		{"单端口", []PortRange{{PubStart: 10000, PubEnd: 10000}}, false},
		{"范围 1:1", []PortRange{{PubStart: 10001, PubEnd: 10010}}, false},
		{"非 1:1", []PortRange{{PubStart: 20000, PubEnd: 20009, ListenStart: 30000, ListenEnd: 30009}}, false},
		{"越界", []PortRange{{PubStart: 0, PubEnd: 100}}, true},
		{"超上限", []PortRange{{PubStart: 65530, PubEnd: 65536}}, true},
		{"倒置", []PortRange{{PubStart: 10010, PubEnd: 10001}}, true},
		{"listen 只填一半", []PortRange{{PubStart: 10000, PubEnd: 10000, ListenStart: 20000}}, true},
		{"宽度不一致", []PortRange{{PubStart: 20000, PubEnd: 20009, ListenStart: 30000, ListenEnd: 30005}}, true},
		{"外部段重叠", []PortRange{{PubStart: 10000, PubEnd: 10010}, {PubStart: 10005, PubEnd: 10020}}, true},
		{"监听段重叠", []PortRange{
			{PubStart: 10000, PubEnd: 10009, ListenStart: 30000, ListenEnd: 30009},
			{PubStart: 20000, PubEnd: 20009, ListenStart: 30005, ListenEnd: 30014},
		}, true},
		{"不相交多段", []PortRange{
			{PubStart: 10000, PubEnd: 10009},
			{PubStart: 20000, PubEnd: 20009, ListenStart: 30000, ListenEnd: 30009},
		}, false},
	}
	for _, c := range cases {
		if err := ValidatePortRanges(c.rs); (err != nil) != c.wantErr {
			t.Errorf("%s: wantErr=%v, err=%v", c.name, c.wantErr, err)
		}
	}
}

func TestPortRangeExpandAndMap(t *testing.T) {
	rs := []PortRange{
		{PubStart: 10000, PubEnd: 10002},
		{PubStart: 20000, PubEnd: 20001, ListenStart: 30000, ListenEnd: 30001},
	}
	if got := ListenCandidates(rs); len(got) != 5 || got[0] != 10000 || got[3] != 30000 || got[4] != 30001 {
		t.Errorf("ListenCandidates = %v", got)
	}
	if !InListenRanges(rs, 10001) || !InListenRanges(rs, 30000) || InListenRanges(rs, 20000) {
		t.Errorf("InListenRanges 判定错误")
	}
	if pub, ok := PublicPort(rs, 10001); !ok || pub != 10001 {
		t.Errorf("1:1 PublicPort = %d,%v", pub, ok)
	}
	if pub, ok := PublicPort(rs, 30001); !ok || pub != 20001 {
		t.Errorf("非 1:1 PublicPort = %d,%v", pub, ok)
	}
	if _, ok := PublicPort(rs, 40000); ok {
		t.Errorf("段外端口不应有映射")
	}
}

func TestParsePortRanges(t *testing.T) {
	if rs, err := ParsePortRanges(""); err != nil || rs != nil {
		t.Errorf("空串应返回 nil,nil，got %v,%v", rs, err)
	}
	if _, err := ParsePortRanges(`[{"pub_start":10000,"pub_end":10001}]`); err != nil {
		t.Errorf("合法 JSON 不应报错: %v", err)
	}
	if _, err := ParsePortRanges(`[{"pub_start":10001,"pub_end":10000}]`); err == nil {
		t.Errorf("非法段应报错")
	}
	if _, err := ParsePortRanges(`not-json`); err == nil {
		t.Errorf("坏 JSON 应报错")
	}
}
