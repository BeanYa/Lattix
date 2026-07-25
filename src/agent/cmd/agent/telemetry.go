package main

import (
	"bufio"
	"log"
	"os"
	"strconv"
	"strings"

	"lattix/agent/internal/xray"
	"lattix/shared"
)

// telemetry 采集周期遥测（§13）：xray 版本/运行状态、主机指标（/proc）、
// xray 流量计数器增量（节点维度 inbound>>>tag 与用户维度 user>>>email）。
type telemetry struct {
	mgr *xray.Manager

	lastStats               map[string]int64 // 计数器名 → 上次累计值
	statsPrimed             bool             // 流量采样基线是否已建立
	lastCPUIdle, lastCPUTot uint64
	cpuPrimed               bool
}

func newTelemetry(mgr *xray.Manager) *telemetry {
	return &telemetry{mgr: mgr, lastStats: map[string]int64{}}
}

// collect 组装一帧遥测载荷。
func (t *telemetry) collect() shared.TelemetryPayload {
	ver, running := t.mgr.Version()
	return shared.TelemetryPayload{
		XrayVersion: ver,
		XrayRunning: running,
		Host:        t.hostMetrics(),
		Traffic:     t.trafficDeltas(),
	}
}

// trafficDeltas 计算各计数器自上次采样以来的增量（xray 重启计数器清零时按全量计）。
// 首次采样仅建立基线、不上报流量：否则 WS 重连（newTelemetry 重置 lastStats）会把
// xray 启动以来的全量当增量重复上报，backend 累加导致重复计数（§13）。
func (t *telemetry) trafficDeltas() []shared.TrafficDelta {
	cur, err := t.mgr.QueryStats()
	if err != nil {
		return nil // xray 未运行或 stats 不可用，跳过本期
	}
	if !t.statsPrimed {
		t.lastStats, t.statsPrimed = cur, true
		return nil // 基线帧：只携带版本/主机指标
	}
	type key struct{ node, user string }
	agg := map[key]*shared.TrafficDelta{}
	for name, v := range cur {
		delta := v - t.lastStats[name]
		if delta < 0 {
			delta = v // 计数器随 xray 重启清零
		}
		if delta == 0 {
			continue
		}
		// 计数器名形如 "inbound>>>node_3>>>traffic>>>uplink" / "user>>>uuid>>>traffic>>>downlink"。
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[2] != "traffic" {
			continue
		}
		k := key{}
		switch parts[0] {
		case "inbound":
			if !strings.HasPrefix(parts[1], "node_") {
				continue // api 等非节点 inbound
			}
			k.node = parts[1]
		case "user":
			k.user = parts[1]
		default:
			continue
		}
		td := agg[k]
		if td == nil {
			td = &shared.TrafficDelta{Node: k.node, User: k.user}
			agg[k] = td
		}
		switch parts[3] {
		case "uplink":
			td.Up += delta
		case "downlink":
			td.Down += delta
		}
	}
	t.lastStats = cur
	out := make([]shared.TrafficDelta, 0, len(agg))
	for _, td := range agg {
		out = append(out, *td)
	}
	return out
}

// hostMetrics 从 /proc 采集负载、内存与 CPU 使用率（CPU 为两次采样区间值，首帧为 0）。
func (t *telemetry) hostMetrics() *shared.HostMetrics {
	m := &shared.HostMetrics{}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			m.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	var memTotal, memAvail uint64
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 {
				continue
			}
			v, _ := strconv.ParseUint(fields[1], 10, 64)
			switch fields[0] {
			case "MemTotal:":
				memTotal = v * 1024
			case "MemAvailable:":
				memAvail = v * 1024
			}
		}
	}
	m.MemTotal = memTotal
	if memTotal > memAvail {
		m.MemUsed = memTotal - memAvail
	}
	if idle, total, ok := readCPUTicks(); ok {
		if t.cpuPrimed && total > t.lastCPUTot {
			dTotal := total - t.lastCPUTot
			dIdle := idle - t.lastCPUIdle
			m.CPUPercent = float64(dTotal-dIdle) / float64(dTotal) * 100
		}
		t.lastCPUIdle, t.lastCPUTot, t.cpuPrimed = idle, total, true
	}
	return m
}

// readCPUTicks 读取 /proc/stat 首行的 idle 与总 tick 数。
func readCPUTicks() (idle, total uint64, ok bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	fields := strings.Fields(line) // cpu user nice system idle iowait irq softirq steal ...
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += v
		if i == 3 || i == 4 { // idle + iowait
			idle += v
		}
	}
	return idle, total, true
}

// logTelemetryError 记录遥测发送失败（连接断开时外层重连会重建遥测循环）。
func logTelemetryError(err error) {
	log.Printf("telemetry: %v", err)
}
