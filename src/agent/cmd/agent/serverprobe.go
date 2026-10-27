package main

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"lattix/shared"
)

// 服务器间延迟测速执行器（§链路统计）：一轮 latency.probe 对 m 个目标并行测速
// （TCP 握手复用链路监听端口 / ICMP Echo），整轮在截止时间内全部完成/失败/超时。
const (
	latencyProbeDialTimeout = 5 * time.Second // 单目标探测超时（短于整轮截止）
	latencyICMPTimeout      = 3 * time.Second // ICMP Echo 等待回显超时
)

// runLatencyProbeRound 并行执行一轮探测并阻塞到全部完成或整轮截止。
// 每个目标恰好产出一条结果；失败/超时的 LatencyMS 为 nil（连通性参考）。
func runLatencyProbeRound(targets []shared.LatencyProbeTarget, roundTimeout time.Duration) []shared.LatencyProbeResult {
	deadline := time.Now().Add(roundTimeout)
	results := make([]shared.LatencyProbeResult, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target shared.LatencyProbeTarget) {
			defer wg.Done()
			results[i] = shared.LatencyProbeResult{ServerID: target.ServerID, Kind: target.Kind, Port: target.Port}
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return
			}
			switch target.Kind {
			case shared.LatencyKindTCP:
				if target.Port <= 0 || target.Address == "" {
					return
				}
				timeout := minDuration(latencyProbeDialTimeout, remaining)
				if latency, err := tcpHandshakeMS(target.Address, target.Port, timeout); err == nil {
					results[i].LatencyMS = &latency
				}
			case shared.LatencyKindICMP:
				if target.Address == "" {
					return
				}
				timeout := minDuration(latencyICMPTimeout, remaining)
				if latency, err := icmpEchoMS(target.Address, timeout); err == nil {
					results[i].LatencyMS = &latency
				}
			}
		}(i, target)
	}
	// 整轮硬截止：全部完成或到达 deadline 即返回（未完成的目标保持失败样本）。
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(roundTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	return results
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// tcpHandshakeMS 测量 TCP 握手往返（毫秒）；连接建立即关闭，不发送业务数据。
// 目标端口为复用的链路监听端口（forward/portal），握手在对端 xray 监听立即完成，
// RTT 即段网络往返。
func tcpHandshakeMS(address string, port int, timeout time.Duration) (float64, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(address, strconv.Itoa(port)), timeout)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return float64(time.Since(start).Microseconds()) / 1000, nil
}

// icmpEchoMS 发送一个 ICMP Echo 并等待回显，返回 RTT（毫秒）。
// 优先非特权 UDP ICMP（ping_group_range），被拒时回退 raw socket（root）。
func icmpEchoMS(address string, timeout time.Duration) (float64, error) {
	ip, err := net.ResolveIPAddr("ip", address)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	conn, err := dialICMP(ip)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return 0, err
	}
	body := &icmp.Echo{ID: int(uint16(time.Now().Nanosecond())), Seq: 1, Data: []byte("lattix")}
	// IPv4 的 Echo 请求类型名为 ICMPTypeEcho（=8）；IPv6 才区分 Request/Reply。
	// proto 为 IANA 协议号（ParseMessage 依据它选择解析器）。
	var typ icmp.Type
	var proto int
	if ip.IP.To4() != nil {
		typ, proto = ipv4.ICMPTypeEcho, 1
	} else {
		typ, proto = ipv6.ICMPTypeEchoRequest, 58
	}
	msg := icmp.Message{Type: typ, Code: 0, Body: body}
	raw, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}
	if _, err := conn.WriteTo(raw, ip); err != nil {
		return 0, err
	}
	rb := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(rb)
		if err != nil {
			return 0, err
		}
		reply, err := icmp.ParseMessage(proto, rb[:n])
		if err != nil {
			continue
		}
		if echo, ok := reply.Body.(*icmp.Echo); ok && echo.ID == body.ID && echo.Seq == body.Seq {
			return float64(time.Since(start).Microseconds()) / 1000, nil
		}
	}
}

// dialICMP 按目标族打开 ICMP 连接：非特权 UDP socket 优先，权限不足回退 raw socket。
func dialICMP(ip *net.IPAddr) (net.PacketConn, error) {
	var networks []string
	if ip.IP.To4() != nil {
		networks = []string{"udp4", "ip4:icmp"}
	} else {
		networks = []string{"udp6", "ip6:ipv6-icmp"}
	}
	var lastErr error
	for _, network := range networks {
		conn, err := icmp.ListenPacket(network, "")
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("icmp listen unavailable: %w", lastErr)
}
