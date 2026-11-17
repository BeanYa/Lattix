package panel

import (
	"context"
	"log"
	"net"
	"sync"
	"time"
)

// 外部直链节点的面板侧连通探测（§未托管服务器）：外部节点没有 Agent，无法参与三网
// 测速与主机性能监控；面板每 2 分钟对本机到节点的 TCP 监听做一次拨号测速，把延迟
// 写回 servers.panel_latency_ms（失败/超时写 NULL 并刷新时间戳），供服务器页的
// 外部直链卡片展示「面板 → 节点」读数。
const (
	externalProbeInterval = 2 * time.Minute
	externalProbeTimeout  = 3 * time.Second
	externalProbeWorkers  = 8
)

// probeExternalServers 对全部未托管外部直链节点做一轮 TCP 拨号测速并落库。
func (s *Server) probeExternalServers(ctx context.Context) {
	servers, err := s.st.ListServers(ctx)
	if err != nil {
		log.Printf("panel: external probe list servers: %v", err)
		return
	}
	type probeTarget struct {
		id      int64
		address string
	}
	targets := make([]probeTarget, 0, len(servers))
	for _, srv := range servers {
		if !srv.IsExternal() {
			continue
		}
		exit := srv.ParseExternalExit()
		if exit == nil || exit.Address == "" || exit.Port <= 0 {
			continue // 未配置完整地址/端口的占位行跳过（服务器表单没有必填项）
		}
		targets = append(targets, probeTarget{id: srv.ID, address: exit.HostPort()})
	}
	if len(targets) == 0 {
		return
	}
	sem := make(chan struct{}, externalProbeWorkers)
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			conn, dialErr := (&net.Dialer{Timeout: externalProbeTimeout}).DialContext(ctx, "tcp", target.address)
			probedAt := time.Now().UTC()
			var latency *int
			if dialErr == nil {
				_ = conn.Close()
				ms := int(time.Since(start).Milliseconds())
				if ms < 1 {
					ms = 1
				}
				latency = &ms
			}
			if err := s.st.UpdateServerPeerProbe(ctx, target.id, latency, probedAt); err != nil {
				log.Printf("panel: server %d panel probe: %v", target.id, err)
			}
		}()
	}
	wg.Wait()
}
