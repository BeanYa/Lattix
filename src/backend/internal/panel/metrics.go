package panel

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"lattix/backend/internal/store"
)

const metricHistoryRetention = 24 * time.Hour

type serverMetricSeriesDTO struct {
	ServerID int64        `json:"server_id"`
	Samples  []metricsDTO `json:"samples"`
}

func toMetricsDTO(m store.ServerMetrics) metricsDTO {
	return metricsDTO{
		Load1: m.Load1, Load5: m.Load5, Load15: m.Load15,
		CPUPercent: m.CPUPercent, MemTotal: m.MemTotal, MemUsed: m.MemUsed,
		DiskTotal: m.DiskTotal, DiskUsed: m.DiskUsed,
		NetworkInterface: m.NetworkInterface,
		NetworkTXBytes:   m.NetworkTXBytes, NetworkRXBytes: m.NetworkRXBytes,
		NetworkTXBPS: m.NetworkTXBPS, NetworkRXBPS: m.NetworkRXBPS,
		UptimeSeconds: m.UptimeSeconds, LatencyMS: m.LatencyMS,
		UpdatedAt: m.UpdatedAt,
	}
}

func (s *Server) handleListMetricSamples(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 60 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 60")
			return
		}
		limit = parsed
	}
	series, err := s.st.RecentServerMetricSamples(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]serverMetricSeriesDTO, 0, len(series))
	serverIDs := make([]int64, 0, len(series))
	for serverID := range series {
		serverIDs = append(serverIDs, serverID)
	}
	sort.Slice(serverIDs, func(i, j int) bool { return serverIDs[i] < serverIDs[j] })
	for _, serverID := range serverIDs {
		samples := series[serverID]
		dto := serverMetricSeriesDTO{ServerID: serverID, Samples: make([]metricsDTO, 0, len(samples))}
		for _, sample := range samples {
			dto.Samples = append(dto.Samples, toMetricsDTO(sample))
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetMetricHistory(w http.ResponseWriter, r *http.Request) {
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
		if err != nil || parsed < 1 || parsed > 24 {
			writeError(w, http.StatusBadRequest, "hours must be between 1 and 24")
			return
		}
		hours = parsed
	}
	samples, err := s.st.ServerMetricHistory(r.Context(), serverID, hours)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]metricsDTO, 0, len(samples))
	for _, sample := range samples {
		out = append(out, toMetricsDTO(sample))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) cleanupMetricHistory(ctx context.Context) error {
	_, err := s.st.DeleteExpiredServerMetricHistory(ctx, metricHistoryRetention)
	return err
}
