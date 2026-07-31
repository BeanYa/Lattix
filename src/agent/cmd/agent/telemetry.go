package main

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lattix/agent/internal/xray"
	"lattix/shared"
)

// telemetry 采集周期遥测（§13）：xray 版本/运行状态、主机指标（/proc）、
// xray 绝对流量计数器（出口 node、每跳 forward 与用户维度）。
type telemetry struct {
	mgr *xray.Manager

	lastCPUIdle, lastCPUTot uint64
	cpuPrimed               bool
	lastNetworkInterface    string
	lastNetworkTX           uint64
	lastNetworkRX           uint64
	lastNetworkAt           time.Time
	latency                 func() (*float64, bool)
}

func newTelemetry(mgr *xray.Manager, latency func() (*float64, bool)) *telemetry {
	return &telemetry{mgr: mgr, latency: latency}
}

// collect 组装一帧遥测载荷。
func (t *telemetry) collect() shared.TelemetryPayload {
	ver, running := t.mgr.Version()
	return shared.TelemetryPayload{
		XrayVersion:    ver,
		XrayRunning:    running,
		XrayInstanceID: t.mgr.StatsInstanceID(),
		Host:           t.hostMetrics(),
		Traffic:        t.trafficCounters(),
	}
}

func (t *telemetry) trafficCounters() []shared.TrafficCounter {
	cur, err := t.mgr.QueryStats()
	if err != nil {
		return nil
	}
	return aggregateTrafficCounters(cur)
}

func aggregateTrafficCounters(cur map[string]int64) []shared.TrafficCounter {
	type key struct {
		nodeID     int64
		endpointID int64
		hopID      int64
		user       string
	}
	agg := map[key]*shared.TrafficCounter{}
	for name, v := range cur {
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[2] != "traffic" {
			continue
		}
		k := key{}
		switch parts[0] {
		case "inbound":
			switch {
			case strings.HasPrefix(parts[1], "node_"):
				k.nodeID, _ = strconv.ParseInt(strings.TrimPrefix(parts[1], "node_"), 10, 64)
			case strings.HasPrefix(parts[1], "chain_forward_"):
				k.hopID, _ = strconv.ParseInt(strings.TrimPrefix(parts[1], "chain_forward_"), 10, 64)
			case strings.HasPrefix(parts[1], "shared_endpoint_"):
				k.endpointID, _ = strconv.ParseInt(strings.TrimPrefix(parts[1], "shared_endpoint_"), 10, 64)
			}
			if k.nodeID <= 0 && k.hopID <= 0 && k.endpointID <= 0 {
				continue
			}
		case "user":
			k.user = parts[1]
		default:
			continue
		}
		counter := agg[k]
		if counter == nil {
			counter = &shared.TrafficCounter{NodeID: k.nodeID, EndpointID: k.endpointID, HopID: k.hopID, User: k.user}
			agg[k] = counter
		}
		switch parts[3] {
		case "uplink":
			counter.Up += v
		case "downlink":
			counter.Down += v
		}
	}
	out := make([]shared.TrafficCounter, 0, len(agg))
	for _, counter := range agg {
		out = append(out, *counter)
	}
	return out
}

// hostMetrics 从 /proc 采集负载、内存与 CPU 使用率（CPU 为两次采样区间值，首帧为 0）。
func (t *telemetry) hostMetrics() *shared.HostMetrics {
	m := &shared.HostMetrics{}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			m.Load1, _ = strconv.ParseFloat(f[0], 64)
			m.Load5, _ = strconv.ParseFloat(f[1], 64)
			m.Load15, _ = strconv.ParseFloat(f[2], 64)
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
			value := float64(dTotal-dIdle) / float64(dTotal) * 100
			m.CPUPercent = &value
		}
		t.lastCPUIdle, t.lastCPUTot, t.cpuPrimed = idle, total, true
	}
	if total, used, ok := rootDiskUsage(); ok {
		m.DiskTotal, m.DiskUsed = total, used
	}
	if uptime, ok := systemUptime(); ok {
		m.UptimeSeconds = uptime
	}
	t.collectNetwork(m)
	if t.latency != nil {
		latency, active := t.latency()
		m.LatencyMS = latency
		m.LatencyProbeActive = &active
	}
	return m
}

func (t *telemetry) collectNetwork(m *shared.HostMetrics) {
	iface := defaultRouteInterface()
	if iface == "" {
		return
	}
	rx, tx, ok := networkCounters(iface)
	if !ok {
		return
	}
	now := time.Now()
	m.NetworkInterface = iface
	m.NetworkTXBytes = tx
	m.NetworkRXBytes = rx
	if iface == t.lastNetworkInterface && !t.lastNetworkAt.IsZero() &&
		tx >= t.lastNetworkTX && rx >= t.lastNetworkRX {
		elapsed := now.Sub(t.lastNetworkAt).Seconds()
		if elapsed > 0 {
			txRate := float64(tx-t.lastNetworkTX) / elapsed
			rxRate := float64(rx-t.lastNetworkRX) / elapsed
			m.NetworkTXBPS = &txRate
			m.NetworkRXBPS = &rxRate
		}
	}
	t.lastNetworkInterface = iface
	t.lastNetworkTX = tx
	t.lastNetworkRX = rx
	t.lastNetworkAt = now
}

func rootDiskUsage() (total, used uint64, ok bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 0, 0, false
	}
	total = stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	if total >= free {
		used = total - free
	}
	return total, used, total > 0
}

func systemUptime() (uint64, bool) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return uint64(value), true
}

func defaultRouteInterface() string {
	if iface := ipv4DefaultRouteInterface(); iface != "" {
		return iface
	}
	return ipv6DefaultRouteInterface()
}

func ipv4DefaultRouteInterface() string {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer file.Close()
	bestIface := ""
	bestMetric := uint64(^uint64(0))
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || fields[1] != "00000000" || ignoredNetworkInterface(fields[0]) {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 64)
		if err != nil || flags&1 == 0 {
			continue
		}
		metric, err := strconv.ParseUint(fields[6], 10, 64)
		if err != nil {
			continue
		}
		if metric < bestMetric {
			bestIface, bestMetric = fields[0], metric
		}
	}
	return bestIface
}

func ipv6DefaultRouteInterface() string {
	file, err := os.Open("/proc/net/ipv6_route")
	if err != nil {
		return ""
	}
	defer file.Close()
	zeroDestination := strings.Repeat("0", 32)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || fields[0] != zeroDestination || fields[1] != "00" {
			continue
		}
		iface := fields[len(fields)-1]
		if !ignoredNetworkInterface(iface) {
			return iface
		}
	}
	return ""
}

func ignoredNetworkInterface(name string) bool {
	if name == "" || name == "lo" {
		return true
	}
	for _, prefix := range []string{"docker", "veth", "br-", "virbr", "tun", "tap"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func networkCounters(iface string) (rx, tx uint64, ok bool) {
	read := func(name string) (uint64, bool) {
		data, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "statistics", name))
		if err != nil {
			return 0, false
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		return value, err == nil
	}
	rx, rxOK := read("rx_bytes")
	tx, txOK := read("tx_bytes")
	return rx, tx, rxOK && txOK
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
