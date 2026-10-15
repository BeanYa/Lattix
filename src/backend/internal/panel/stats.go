package panel

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"lattix/backend/internal/logging"
	"lattix/backend/internal/store"
)

// statsOverviewDTO 匿名嵌入 store 概览（JSON 打平），agents_online/agents_total
// 与 runtime.go 的 services 快照同源（ListServers + AgentRequester.IsOnline）。
type statsOverviewDTO struct {
	*store.TrafficStatsOverview
	AgentsOnline int `json:"agents_online"`
	AgentsTotal  int `json:"agents_total"`
}

// handleStatsTrafficOverview GET /api/stats/traffic/overview：今日/本月总量、
// 环比与 Top 榜（store 内固定 Top 10）+ 被控在线/总数。
func (s *Server) handleStatsTrafficOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.st.TrafficStatsOverview(r.Context(), 10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto := statsOverviewDTO{TrafficStatsOverview: overview}
	if servers, err := s.st.ListServers(r.Context()); err == nil {
		dto.AgentsTotal = len(servers)
		if s.req != nil {
			for _, server := range servers {
				if s.req.IsOnline(server.ID) {
					dto.AgentsOnline++
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, dto)
}

// parseStatsUnixSeconds 解析可选的 Unix 秒查询参数，返回（值, 是否提供, 是否合法）。
// 量程校验：秒值须在 (0, 1e11]（约公元 5138 年），超出视为单位错误（如前端误传
// 毫秒级时间戳），按非法参数拒绝而非静默返回空窗口。
func parseStatsUnixSeconds(raw string) (time.Time, bool, bool) {
	if raw == "" {
		return time.Time{}, false, true
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 || seconds > 1e11 {
		return time.Time{}, false, false
	}
	return time.Unix(seconds, 0), true, true
}

// resolveStatsWindow 计算 [from,to)：to 未给时恒取 now（day/month 的 to 含本地整日，
// 不为历史时刻构造 mid-day 上界）；from 未给时按粒度取默认窗——5m/15m→6h、
// 1h→24h、day→30 天、month→12 个月。返回 false 表示 from>=to。
func resolveStatsWindow(granularity store.TrafficStatGranularity, fromRaw, toRaw string) (time.Time, time.Time, bool) {
	from, fromPresent, fromOK := parseStatsUnixSeconds(fromRaw)
	to, toPresent, toOK := parseStatsUnixSeconds(toRaw)
	if !fromOK || !toOK {
		return time.Time{}, time.Time{}, false
	}
	if !toPresent {
		to = time.Now()
	}
	if !fromPresent {
		switch granularity {
		case "1h":
			from = to.Add(-24 * time.Hour)
		case "day":
			from = to.AddDate(0, 0, -30)
		case "month":
			from = to.AddDate(0, -12, 0)
		default: // 5m / 15m
			from = to.Add(-6 * time.Hour)
		}
	}
	return from, to, from.Before(to)
}

// handleStatsTrafficSeries GET /api/stats/traffic/series：按 dim（fleet/server/user）
// 与粒度聚合时序。校验顺序：dim/granularity 枚举 → dim 必带参数 → from<to。
func (s *Server) handleStatsTrafficSeries(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	dim := query.Get("dim")
	switch dim {
	case "fleet", "server", "user":
	default:
		writeError(w, http.StatusBadRequest, "dim must be fleet, server or user")
		return
	}
	granularity := store.TrafficStatGranularity(query.Get("granularity"))
	switch granularity {
	case "5m", "15m", "1h", "day", "month":
	default:
		writeError(w, http.StatusBadRequest, "granularity must be 5m, 15m, 1h, day or month")
		return
	}
	var serverID int64
	if dim == "server" {
		parsed, err := strconv.ParseInt(query.Get("server_id"), 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "server dim requires server_id")
			return
		}
		serverID = parsed
	}
	userUUID := ""
	if dim == "user" {
		if userUUID = query.Get("user_uuid"); userUUID == "" {
			writeError(w, http.StatusBadRequest, "user dim requires user_uuid")
			return
		}
	}
	from, to, valid := resolveStatsWindow(granularity, query.Get("from"), query.Get("to"))
	if !valid {
		writeError(w, http.StatusBadRequest, "from/to must be Unix seconds with from before to")
		return
	}
	points, err := s.st.TrafficStatSeries(r.Context(), dim, serverID, userUUID, granularity, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, points)
}

// handleStatsTrafficBreakdown GET /api/stats/traffic/breakdown：下钻明细
// （server_id 优先；两者皆缺 → 400）。默认窗最近 24 小时（分钟桶口径）。
func (s *Server) handleStatsTrafficBreakdown(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var serverID int64
	if raw := query.Get("server_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid server id")
			return
		}
		serverID = parsed
	}
	userUUID := query.Get("user_uuid")
	if serverID == 0 && userUUID == "" {
		writeError(w, http.StatusBadRequest, "server_id or user_uuid is required")
		return
	}
	// 粒度传 "1h"：分钟桶口径的默认窗即 24h。
	from, to, valid := resolveStatsWindow("1h", query.Get("from"), query.Get("to"))
	if !valid {
		writeError(w, http.StatusBadRequest, "from/to must be Unix seconds with from before to")
		return
	}
	rows, err := s.st.TrafficStatBreakdown(r.Context(), serverID, userUUID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleStatsTrafficServerHistory GET /api/stats/traffic/server-history：
// 单服务器逐分钟实体维度流量（hours 1–168，默认 24）。
func (s *Server) handleStatsTrafficServerHistory(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.ParseInt(r.URL.Query().Get("server_id"), 10, 64)
	if err != nil || serverID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	if _, err := s.st.ServerByID(r.Context(), serverID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "server not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hours := 24
	if value := r.URL.Query().Get("hours"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 168 {
			writeError(w, http.StatusBadRequest, "hours must be between 1 and 168")
			return
		}
		hours = parsed
	}
	to := time.Now()
	from := to.Add(-time.Duration(hours) * time.Hour)
	points, err := s.st.ServerTrafficBuckets(r.Context(), serverID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, points)
}

// statsErrorsSummaryDTO GET /api/stats/errors/summary 响应。
type statsErrorsSummaryDTO struct {
	TodayErrorCount int `json:"today_error_count"`
}

// handleStatsErrorsSummary GET /api/stats/errors/summary：今日（traffic 时区本地
// 零点起）severity=error 的操作日志条数；From 串格式与操作日志查询路径一致
// （UTC 毫秒级 RFC3339，同前端 toISOString）。
func (s *Server) handleStatsErrorsSummary(w http.ResponseWriter, r *http.Request) {
	if s.opLog == nil {
		writeError(w, http.StatusServiceUnavailable, "操作日志未启用")
		return
	}
	_, location := s.st.TrafficLocation(r.Context())
	now := time.Now().In(location)
	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	_, total, err := s.opLog.List(r.Context(), logging.OperationFilter{
		Severity: logging.SeverityError,
		From:     startToday.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
	}, 1, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, statsErrorsSummaryDTO{TodayErrorCount: total})
}
