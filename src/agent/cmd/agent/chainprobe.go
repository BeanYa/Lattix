package main

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"

	"lattix/agent/internal/state"
	"lattix/agent/internal/xray"
	"lattix/shared"
)

// chainProbeTimeout 是单段 TCP 握手探测超时；超时记为失败样本（latency 缺省，不中断其余段）。
const chainProbeTimeout = 5 * time.Second

// chainProbeSample 从本机链跳配置件推导可探测段并逐段做 TCP 握手测速（随遥测帧上报）。
// forward 直连段拨 dokodemo 目标（下一跳 forward 监听端口），bridge 反向段拨上游 portal；
// 握手在对端 xray 监听立即完成，RTT 即段网络往返。
func chainProbeSample(pieces []state.ChainPiece, timeout time.Duration) []shared.ChainProbeStat {
	targets := chainProbeTargets(pieces)
	if len(targets) == 0 {
		return nil
	}
	out := make([]shared.ChainProbeStat, 0, len(targets))
	for _, target := range targets {
		stat := shared.ChainProbeStat{HopID: target.hopID, Kind: target.kind,
			TargetAddress: target.address, TargetPort: target.port}
		if latency, err := tcpHandshakeMS(target.address, target.port, timeout); err == nil {
			stat.LatencyMS = &latency
		}
		out = append(out, stat)
	}
	return out
}

type chainProbeTarget struct {
	hopID   int64
	kind    string
	address string
	port    int
}

// chainProbeTargets 从链跳 piece 落盘配置推导探测目标。目标形状由面板渲染、本机落盘
// （§21.1 piece），存量 piece 同样适用，无需重新下发即可获得段延迟。
func chainProbeTargets(pieces []state.ChainPiece) []chainProbeTarget {
	out := make([]chainProbeTarget, 0, len(pieces))
	for _, piece := range pieces {
		switch piece.Kind {
		case shared.HopKindForward:
			if address, port, ok := forwardProbeTarget(piece); ok {
				out = append(out, chainProbeTarget{hopID: piece.HopID,
					kind: shared.ChainProbeKindForward, address: address, port: port})
			}
		case shared.HopKindBridge:
			if address, port, ok := bridgeProbeTarget(piece); ok {
				out = append(out, chainProbeTarget{hopID: piece.HopID,
					kind: shared.ChainProbeKindBridge, address: address, port: port})
			}
		}
	}
	return out
}

// forwardProbeTarget 解析 forward 直连段探测目标（dokodemo inbound 的透传目标）。
// 经 reverse portal 的段（路由指向 portal_* tag，与同跳 bridge 段同物理路径）与
// hy2 outbound 段（UDP/QUIC，无 TCP 握手可测）不探测。
func forwardProbeTarget(piece state.ChainPiece) (string, int, bool) {
	for _, raw := range pieceOutbounds(piece) {
		var probe struct {
			Protocol string `json:"protocol"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Protocol == shared.ProtocolHysteria2 {
			return "", 0, false
		}
	}
	for _, raw := range piece.Rules {
		var rule struct {
			OutboundTag string `json:"outboundTag"`
		}
		if json.Unmarshal(raw, &rule) == nil && strings.HasPrefix(rule.OutboundTag, "portal_") {
			return "", 0, false
		}
	}
	var inbound struct {
		Settings struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		} `json:"settings"`
	}
	if piece.Inbound == nil || json.Unmarshal(piece.Inbound, &inbound) != nil {
		return "", 0, false
	}
	if inbound.Settings.Address == "" || inbound.Settings.Port <= 0 {
		return "", 0, false
	}
	return inbound.Settings.Address, inbound.Settings.Port, true
}

// bridgeProbeTarget 解析 bridge 反向段探测目标（interconn outbound 的 vnext 地址/端口，
// 即上游 portal 监听）。
func bridgeProbeTarget(piece state.ChainPiece) (string, int, bool) {
	for _, raw := range pieceOutbounds(piece) {
		var outbound struct {
			Settings struct {
				Vnext []struct {
					Address string `json:"address"`
					Port    int    `json:"port"`
				} `json:"vnext"`
			} `json:"settings"`
		}
		if json.Unmarshal(raw, &outbound) != nil || len(outbound.Settings.Vnext) == 0 {
			continue
		}
		vnext := outbound.Settings.Vnext[0]
		if vnext.Address == "" || vnext.Port <= 0 {
			continue
		}
		return vnext.Address, vnext.Port, true
	}
	return "", 0, false
}

func pieceOutbounds(piece state.ChainPiece) []json.RawMessage {
	raws := make([]json.RawMessage, 0, 1+len(piece.Outbounds))
	if piece.Outbound != nil {
		raws = append(raws, piece.Outbound)
	}
	return append(raws, piece.Outbounds...)
}

// tcpHandshakeMS 测量 TCP 握手往返（毫秒）；连接建立即关闭，不发送业务数据。
func tcpHandshakeMS(address string, port int, timeout time.Duration) (float64, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(address, strconv.Itoa(port)), timeout)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return float64(time.Since(start).Microseconds()) / 1000, nil
}

// chainProbeSampler 返回遥测帧的链跳段探测器：每次采样对本机链跳段做一轮 TCP 握手测速。
func chainProbeSampler(mgr *xray.Manager) func() []shared.ChainProbeStat {
	return func() []shared.ChainProbeStat {
		return chainProbeSample(mgr.ChainPieces(), chainProbeTimeout)
	}
}
