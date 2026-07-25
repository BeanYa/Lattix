package shared

import (
	"encoding/json"
	"fmt"
)

// PortRange 是 NAT 服务器可用端口段的一项（servers.allowed_ports 的 JSON 元素，§21）：
// 外部段 [PubStart,PubEnd] 映射到内部监听段 [ListenStart,ListenEnd]；
// ListenStart/ListenEnd 为 0 表示 1:1 映射（监听端口 = 外部端口，JSON 省略）。
// 非 1:1 时 realized_config 区分 listen_port/public_port，订阅取 public_port。
type PortRange struct {
	PubStart    int `json:"pub_start"`
	PubEnd      int `json:"pub_end"`
	ListenStart int `json:"listen_start,omitempty"`
	ListenEnd   int `json:"listen_end,omitempty"`
}

// ParsePortRanges 解析 servers.allowed_ports 的存储值（空串 = 无端口段，返回 nil,nil）。
func ParsePortRanges(raw string) ([]PortRange, error) {
	if raw == "" {
		return nil, nil
	}
	var rs []PortRange
	if err := json.Unmarshal([]byte(raw), &rs); err != nil {
		return nil, fmt.Errorf("解析端口段: %w", err)
	}
	if err := ValidatePortRanges(rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// ValidatePortRanges 校验端口段合法性（§21 建机/PATCH 校验，panel 与下发展开两端共用）：
// 每项 1-65535 且 start<=end；listen_* 为 0 表示 1:1（须同时为 0），
// 非 1:1 时外部段与监听段等宽；段间两侧均不得重叠。
func ValidatePortRanges(rs []PortRange) error {
	for i, r := range rs {
		if r.PubStart < 1 || r.PubEnd > 65535 || r.PubStart > r.PubEnd {
			return fmt.Errorf("端口段 %d 非法：外部段须 1-65535 且 start<=end", i)
		}
		if (r.ListenStart == 0) != (r.ListenEnd == 0) {
			return fmt.Errorf("端口段 %d 非法：listen_start/listen_end 须同时省略（1:1）或同时填写", i)
		}
		if r.ListenStart != 0 {
			if r.ListenStart < 1 || r.ListenEnd > 65535 || r.ListenStart > r.ListenEnd {
				return fmt.Errorf("端口段 %d 非法：监听段须 1-65535 且 start<=end", i)
			}
			if r.ListenEnd-r.ListenStart != r.PubEnd-r.PubStart {
				return fmt.Errorf("端口段 %d 非法：外部段与监听段宽度不一致", i)
			}
		}
	}
	// 重叠检查：段数极小，两两区间比较即可（不展开点集）。
	for i := 0; i < len(rs); i++ {
		for j := i + 1; j < len(rs); j++ {
			if rangesOverlap(rs[i].PubStart, rs[i].PubEnd, rs[j].PubStart, rs[j].PubEnd) {
				return fmt.Errorf("端口段 %d 与 %d 的外部段重叠", i, j)
			}
			ls, le := rs[i].listenRange()
			os, oe := rs[j].listenRange()
			if rangesOverlap(ls, le, os, oe) {
				return fmt.Errorf("端口段 %d 与 %d 的监听段重叠", i, j)
			}
		}
	}
	return nil
}

// listenRange 返回该段的监听侧区间（1:1 时即外部段）。
func (r PortRange) listenRange() (int, int) {
	if r.ListenStart == 0 {
		return r.PubStart, r.PubEnd
	}
	return r.ListenStart, r.ListenEnd
}

func rangesOverlap(s1, e1, s2, e2 int) bool { return s1 <= e2 && s2 <= e1 }

// ListenCandidates 展开全部监听侧候选端口（apply_node/apply_chain_hop 的
// port_candidates：受限直连 NAT 机上 Agent 按序挑空闲，§21）。
func ListenCandidates(rs []PortRange) []int {
	var out []int
	for _, r := range rs {
		s, e := r.listenRange()
		for p := s; p <= e; p++ {
			out = append(out, p)
		}
	}
	return out
}

// InListenRanges 报告端口是否落在某段的监听侧（建节点/建链指定端口与 PATCH 收窄校验用）。
func InListenRanges(rs []PortRange, port int) bool {
	for _, r := range rs {
		s, e := r.listenRange()
		if port >= s && port <= e {
			return true
		}
	}
	return false
}

// PublicPort 返回监听端口对应的外部端口（listen→public 映射，订阅取 public_port，§21）；
// 1:1 段返回端口本身。端口不在任何段内时 ok=false（调用方回退按监听端口处理）。
func PublicPort(rs []PortRange, listen int) (pub int, ok bool) {
	for _, r := range rs {
		s, e := r.listenRange()
		if listen >= s && listen <= e {
			return r.PubStart + (listen - s), true
		}
	}
	return 0, false
}
