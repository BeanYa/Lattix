package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"lattix/backend/internal/cdncatalog"
	"lattix/backend/internal/panel/cdn"
	"lattix/backend/internal/store"
)

// 三网目录测速（§三网测速）的面板读取侧：目标新鲜度窗口与链路统计同窗
// （约 3 轮测速间隔）；丢包率按更长的统计窗口计算（约 36 个 5 分钟样本），
// 让单轮失败不会立刻把丢包率顶满。色块条展示最近 roundsWindow 内至多
// roundsLimit 轮结果（约 1 小时，5 分钟一轮）。
const (
	catalogProbeFreshWindow  = 15 * time.Minute
	catalogProbeLossWindow   = 3 * time.Hour
	catalogProbeRoundsWindow = time.Hour
	catalogProbeRoundsLimit  = 12
)

// catalogProbeRoundDTO 是色块条上的单轮测速结果：LatencyMS 为 null = 该轮失败/超时。
type catalogProbeRoundDTO struct {
	LatencyMS  *float64  `json:"latency_ms"`
	MeasuredAt time.Time `json:"measured_at"`
}

// catalogProbeRowDTO 是单台服务器到一个目录目标的测速行：
// LatencyMS 为 null = 最近一次探测失败/超时；LossPercent 为窗口内失败占比
// （null = 窗口内无样本）；Samples 为窗口内样本数（前端提示数据积累程度）；
// Rounds 为最近若干轮结果（时间升序，末位即最新轮）。
type catalogProbeRowDTO struct {
	Label       string                 `json:"label"`
	Carrier     string                 `json:"carrier"`
	LatencyMS   *float64               `json:"latency_ms"`
	LossPercent *float64               `json:"loss_percent"`
	Samples     int64                  `json:"samples"`
	Rounds      []catalogProbeRoundDTO `json:"rounds"`
	MeasuredAt  time.Time              `json:"measured_at"`
}

type catalogProbeServerDTO struct {
	Rows []catalogProbeRowDTO `json:"rows"`
}

// catalogProbesResponse 是 GET /api/server/catalog-probes 的响应：
// Region 为当前生效的测速省市设置；Servers 以服务器 ID（十进制字符串）为键，
// 只含新鲜窗口内有样本的服务器。
type catalogProbesResponse struct {
	Province string                           `json:"province"`
	City     string                           `json:"city"`
	Servers  map[string]catalogProbeServerDTO `json:"servers"`
}

// handleGetServerCatalogProbes 处理 GET /api/server/catalog-probes（服务器卡片
// 三网测速区轮询）：目标集来自当前区域设置与目录缓存，目录未就绪时返回空集。
func (s *Server) handleGetServerCatalogProbes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	targets, region, err := s.st.CatalogProbeTargets(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := catalogProbesResponse{
		Province: region.Province, City: region.City,
		Servers: map[string]catalogProbeServerDTO{},
	}
	if len(targets) == 0 {
		writeJSON(w, http.StatusOK, response)
		return
	}
	fresh, err := s.st.FreshServerCatalogProbes(ctx, catalogProbeFreshWindow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lossRows, err := s.st.CatalogProbeLossStats(ctx, catalogProbeLossWindow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lossByKey := make(map[string]store.CatalogProbeLossRow, len(lossRows))
	for _, row := range lossRows {
		lossByKey[catalogProbeLossKey(row.FromServer, row.TargetKey)] = row
	}
	roundRows, err := s.st.RecentServerCatalogProbeSamples(ctx, catalogProbeRoundsWindow, catalogProbeRoundsLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	roundsByKey := make(map[string][]catalogProbeRoundDTO, len(roundRows))
	for _, row := range roundRows {
		key := catalogProbeLossKey(row.FromServer, row.TargetKey)
		roundsByKey[key] = append(roundsByKey[key], catalogProbeRoundDTO{
			LatencyMS: row.LatencyMS, MeasuredAt: row.MeasuredAt,
		})
	}
	rowsByServer := make(map[int64][]catalogProbeRowDTO, len(fresh))
	for _, target := range targets {
		for _, row := range fresh {
			if row.TargetKey != target.Key {
				continue
			}
			loss := lossByKey[catalogProbeLossKey(row.FromServer, target.Key)]
			rowsByServer[row.FromServer] = append(rowsByServer[row.FromServer], catalogProbeRowDTO{
				Label: target.Label, Carrier: target.Carrier,
				LatencyMS: row.LatencyMS, LossPercent: loss.LossPercent(),
				Samples: loss.Samples, MeasuredAt: row.MeasuredAt,
				Rounds: roundsByKey[catalogProbeLossKey(row.FromServer, target.Key)],
			})
		}
	}
	for serverID, rows := range rowsByServer {
		response.Servers[strconv.FormatInt(serverID, 10)] = catalogProbeServerDTO{Rows: rows}
	}
	writeJSON(w, http.StatusOK, response)
}

func catalogProbeLossKey(serverID int64, targetKey string) string {
	return strconv.FormatInt(serverID, 10) + "\x00" + targetKey
}

// cdnCatalogStatusDTO 是目录状态 + 省市选项（设置页三网测速下拉数据源）。
type cdnCatalogStatusDTO struct {
	cdn.Status
	Provinces []string            `json:"provinces,omitempty"`
	Cities    map[string][]string `json:"cities,omitempty"`
}

// loadCDNCatalogDocument 读取并解析目录缓存；无缓存/解析失败/版本不符返回 nil。
func (s *Server) loadCDNCatalogDocument(ctx context.Context) *cdncatalog.Document {
	raw, err := s.st.GetSetting(ctx, store.SettingCDNNodeCatalog)
	if err != nil || raw == "" {
		return nil
	}
	var document cdncatalog.Document
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return nil
	}
	if document.Version != cdncatalog.SchemaVersion {
		return nil
	}
	return &document
}

// catalogRegionOptions 展开目录中的省市选项（省份升序；城市按目录顺序去重）。
func catalogRegionOptions(document *cdncatalog.Document) ([]string, map[string][]string) {
	if document == nil {
		return nil, nil
	}
	provinces := make([]string, 0, len(document.Provinces))
	provinceSet := make(map[string]struct{}, len(document.Provinces))
	cities := make(map[string][]string, len(document.Provinces))
	for _, province := range document.Provinces {
		if _, ok := provinceSet[province.Name]; ok {
			continue
		}
		provinceSet[province.Name] = struct{}{}
		provinces = append(provinces, province.Name)
	}
	sort.Strings(provinces)
	for _, city := range document.Cities {
		if city.Province == "" || city.City == "" {
			continue
		}
		list := cities[city.Province]
		duplicated := false
		for _, existing := range list {
			if existing == city.City {
				duplicated = true
				break
			}
		}
		if !duplicated {
			cities[city.Province] = append(list, city.City)
		}
	}
	return provinces, cities
}
