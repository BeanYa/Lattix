package panel

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lattix/backend/internal/panel/scheduler"
)

type runtimePanelDTO struct {
	Version       string `json:"version"`
	State         string `json:"state"`
	StartedAt     string `json:"started_at"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	PID           int    `json:"pid"`
}

type runtimeHostDTO struct {
	Hostname     string   `json:"hostname"`
	OS           string   `json:"os"`
	Arch         string   `json:"arch"`
	CPUCores     int      `json:"cpu_cores"`
	CPUPercent   *float64 `json:"cpu_percent"`
	Load1        float64  `json:"load1"`
	Load5        float64  `json:"load5"`
	Load15       float64  `json:"load15"`
	MemoryTotal  uint64   `json:"memory_total"`
	MemoryActive uint64   `json:"memory_active"`
}

type runtimeProcessDTO struct {
	GoVersion    string `json:"go_version"`
	Goroutines   int    `json:"goroutines"`
	RSSBytes     uint64 `json:"rss_bytes"`
	VirtualBytes uint64 `json:"virtual_bytes"`
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapSys      uint64 `json:"heap_sys"`
	HeapInuse    uint64 `json:"heap_inuse"`
	StackInuse   uint64 `json:"stack_inuse"`
	GCCycles     uint32 `json:"gc_cycles"`
	LastGCAt     string `json:"last_gc_at,omitempty"`
}

type runtimeServicesDTO struct {
	DatabaseHealthy   bool    `json:"database_healthy"`
	DatabaseLatency   float64 `json:"database_latency_ms"`
	AgentsOnline      int     `json:"agents_online"`
	AgentsTotal       int     `json:"agents_total"`
	RequestLogUsage   int64   `json:"request_log_usage"`
	RequestLogLimit   int64   `json:"request_log_limit"`
	RequestLogDropped uint64  `json:"request_log_dropped"`
}

type panelRuntimeDTO struct {
	SampledAt string                          `json:"sampled_at"`
	Panel     runtimePanelDTO                 `json:"panel"`
	Host      runtimeHostDTO                  `json:"host"`
	Process   runtimeProcessDTO               `json:"process"`
	Services  runtimeServicesDTO              `json:"services"`
	Tasks     []scheduler.ScheduledTaskStatus `json:"tasks"`
}

type runtimeCPUSample struct {
	Total uint64
	Idle  uint64
	Valid bool
}

func (s *Server) handlePanelRuntime(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	startedAt := s.startedAt
	if startedAt.IsZero() {
		startedAt = now
	}

	state := "startup"
	if s.lifecycle != nil {
		state = s.lifecycle.Snapshot().State
	}
	hostname, _ := os.Hostname()
	load1, load5, load15 := readLoadAverage()
	memoryTotal, memoryActive := readHostMemory()
	rssBytes, virtualBytes := readProcessMemory()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)

	lastGCAt := ""
	if memory.LastGC != 0 {
		lastGCAt = time.Unix(0, int64(memory.LastGC)).UTC().Format(time.RFC3339Nano)
	}

	services := runtimeServicesDTO{}
	if s.st != nil {
		pingCtx, cancel := context.WithTimeout(r.Context(), time.Second)
		pingStarted := time.Now()
		err := s.st.Ping(pingCtx)
		services.DatabaseLatency = float64(time.Since(pingStarted).Microseconds()) / 1000
		services.DatabaseHealthy = err == nil
		cancel()
		if servers, err := s.st.ListServers(r.Context()); err == nil {
			services.AgentsTotal = len(servers)
			if s.req != nil {
				for _, server := range servers {
					if s.req.IsOnline(server.ID) {
						services.AgentsOnline++
					}
				}
			}
		}
	}
	if s.reqLog != nil {
		if status, err := s.reqLog.Status(r.Context()); err == nil {
			services.RequestLogUsage = status.UsageBytes
			services.RequestLogLimit = status.MaxBytes
			services.RequestLogDropped = status.Dropped
		}
	}

	tasks := []scheduler.ScheduledTaskStatus{}
	if s.scheduler != nil {
		tasks = s.scheduler.StatusSnapshot()
	}

	writeJSON(w, http.StatusOK, panelRuntimeDTO{
		SampledAt: now.UTC().Format(time.RFC3339Nano),
		Panel: runtimePanelDTO{
			Version: s.cfg.Version, State: state, StartedAt: startedAt.UTC().Format(time.RFC3339Nano),
			UptimeSeconds: int64(now.Sub(startedAt).Seconds()), PID: os.Getpid(),
		},
		Host: runtimeHostDTO{
			Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH, CPUCores: runtime.NumCPU(),
			CPUPercent: s.sampleCPUPercent(), Load1: load1, Load5: load5, Load15: load15,
			MemoryTotal: memoryTotal, MemoryActive: memoryActive,
		},
		Process: runtimeProcessDTO{
			GoVersion: runtime.Version(), Goroutines: runtime.NumGoroutine(), RSSBytes: rssBytes,
			VirtualBytes: virtualBytes, HeapAlloc: memory.HeapAlloc, HeapSys: memory.HeapSys,
			HeapInuse: memory.HeapInuse, StackInuse: memory.StackInuse, GCCycles: memory.NumGC,
			LastGCAt: lastGCAt,
		},
		Services: services,
		Tasks:    tasks,
	})
}

func (s *Server) sampleCPUPercent() *float64 {
	current, ok := readCPUSample()
	if !ok {
		return nil
	}
	s.runtimeMu.Lock()
	previous := s.lastCPU
	s.lastCPU = current
	s.runtimeMu.Unlock()
	if !previous.Valid || current.Total <= previous.Total || current.Idle < previous.Idle {
		return nil
	}
	total := current.Total - previous.Total
	idle := current.Idle - previous.Idle
	value := float64(total-idle) / float64(total) * 100
	return &value
}

func readCPUSample() (runtimeCPUSample, bool) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return runtimeCPUSample{}, false
	}
	defer file.Close()
	line, err := bufio.NewReader(file).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "cpu ") {
		return runtimeCPUSample{}, false
	}
	fields := strings.Fields(line)[1:]
	if len(fields) < 4 {
		return runtimeCPUSample{}, false
	}
	var total uint64
	values := make([]uint64, len(fields))
	for index, field := range fields {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return runtimeCPUSample{}, false
		}
		values[index] = value
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return runtimeCPUSample{Total: total, Idle: idle, Valid: true}, true
}

func readLoadAverage() (float64, float64, float64) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	return parseFloat(fields[0]), parseFloat(fields[1]), parseFloat(fields[2])
}

func readHostMemory() (uint64, uint64) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	values := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var key, unit string
		var value uint64
		if _, err := fmt.Sscanf(scanner.Text(), "%s %d %s", &key, &value, &unit); err == nil {
			values[strings.TrimSuffix(key, ":")] = value * 1024
		}
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if available > total {
		available = total
	}
	return total, total - available
}

func readProcessMemory() (uint64, uint64) {
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	var rss, virtual uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var value uint64
		switch {
		case strings.HasPrefix(scanner.Text(), "VmRSS:"):
			_, _ = fmt.Sscanf(scanner.Text(), "VmRSS: %d kB", &value)
			rss = value * 1024
		case strings.HasPrefix(scanner.Text(), "VmSize:"):
			_, _ = fmt.Sscanf(scanner.Text(), "VmSize: %d kB", &value)
			virtual = value * 1024
		}
	}
	return rss, virtual
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}
