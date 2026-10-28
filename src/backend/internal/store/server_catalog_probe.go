package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lattix/backend/internal/cdncatalog"
)

// 三网目录测速（§三网测速）：面板按「测速省市」设置从 zstatic 目录（cdn_node_catalog）
// 推导每台服务器的测速目标（省市 × 电信/联通/移动），随 latency.probe 轮次与链路
// 目标一并下发；agent 测速上报后按目录目标 key 单独落库。TCP 握手失败/超时记为
// 失败样本，丢包率按历史窗口内的失败占比在读取侧计算。

// DefaultCatalogProbeProvince 是测速省市未设置时的默认省份。
const DefaultCatalogProbeProvince = "广东"

// catalogProbeCarriers 是目录目标的运营商顺序（电信→联通→移动，与目录一致）。
var catalogProbeCarriers = []struct {
	Key  string // telecom|unicom|mobile（目标 key 与上报行共用）
	Name string // 电信|联通|移动（展示标签）
	Code string // ct|cu|cm（城市端点的 carrier_code）
}{
	{"telecom", "电信", "ct"},
	{"unicom", "联通", "cu"},
	{"mobile", "移动", "cm"},
}

// CatalogProbeRegion 是三网测速的目标区域设置：province 必填，city 非空时测该市
// 城市节点（否则测省份节点）。存储为 settings 的 JSON。
type CatalogProbeRegion struct {
	Province string `json:"province"`
	City     string `json:"city"`
}

// Normalized 去除空白并回填默认省份（读取侧宽容：未设置/空省份 → 默认广东省）。
func (r CatalogProbeRegion) Normalized() CatalogProbeRegion {
	normalized := CatalogProbeRegion{Province: strings.TrimSpace(r.Province), City: strings.TrimSpace(r.City)}
	if normalized.Province == "" {
		normalized.Province = DefaultCatalogProbeProvince
	}
	return normalized
}

// CatalogProbeRegionFromRaw 解析 settings 中的区域 JSON；空值/损坏按未设置处理
// （回默认省份），读取侧不做硬失败——测速是非关键路径。
func CatalogProbeRegionFromRaw(raw string) CatalogProbeRegion {
	var region CatalogProbeRegion
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &region)
	}
	return region.Normalized()
}

// CatalogProbeRegion 返回当前保存的测速省市（未设置回默认）。
func (s *Store) CatalogProbeRegion(ctx context.Context) (CatalogProbeRegion, error) {
	raw, err := s.GetSetting(ctx, SettingCatalogProbeRegion)
	if err != nil {
		return CatalogProbeRegion{}, err
	}
	return CatalogProbeRegionFromRaw(raw), nil
}

// CatalogProbeTarget 是推导出的单个目录测速目标（所有服务器同集）。
type CatalogProbeTarget struct {
	Key     string // catalog:<省>:<市|*>:<telecom|unicom|mobile>，随探测下发并作为落库键
	Label   string // 展示标签：广东电信 / 深圳电信
	Carrier string // telecom|unicom|mobile
	Address string
	Port    int
}

// CatalogProbeTargets 返回当前区域设置下的目录测速目标。目录缺失/版本不符/省份或
// 城市不在目录内时返回空集（非错误）——面板按无目标处理，等待目录刷新后再测。
func (s *Store) CatalogProbeTargets(ctx context.Context) ([]CatalogProbeTarget, CatalogProbeRegion, error) {
	region, err := s.CatalogProbeRegion(ctx)
	if err != nil {
		return nil, region, err
	}
	raw, err := s.GetSetting(ctx, SettingCDNNodeCatalog)
	if err != nil {
		return nil, region, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, region, nil
	}
	var doc cdncatalog.Document
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, region, fmt.Errorf("decode cached CDN catalog: %w", err)
	}
	if doc.Version != cdncatalog.SchemaVersion {
		return nil, region, nil
	}
	return CatalogProbeTargetsForRegion(&doc, region), region, nil
}

// CatalogProbeTargetsForRegion 从目录文档推导目标集（省份节点取 IPv4 端点，
// 城市节点取目录预构建的城市端点）；store 包外（设置校验）也复用同一推导规则。
func CatalogProbeTargetsForRegion(doc *cdncatalog.Document, region CatalogProbeRegion) []CatalogProbeTarget {
	region = region.Normalized()
	targets := make([]CatalogProbeTarget, 0, len(catalogProbeCarriers))
	if region.City != "" {
		for _, city := range doc.Cities {
			if city.Province != region.Province || city.City != region.City {
				continue
			}
			for _, carrier := range catalogProbeCarriers {
				if city.CarrierCode != carrier.Code || city.Host == "" || city.Port <= 0 {
					continue
				}
				targets = append(targets, CatalogProbeTarget{
					Key: catalogProbeTargetKey(region, carrier.Key), Label: region.City + carrier.Name,
					Carrier: carrier.Key, Address: city.Host, Port: city.Port,
				})
			}
		}
		return targets
	}
	for _, province := range doc.Provinces {
		if province.Name != region.Province {
			continue
		}
		for _, carrier := range catalogProbeCarriers {
			endpoints, ok := provinceCarrierEndpoints(&province, carrier.Key)
			if !ok {
				continue
			}
			endpoint := endpoints.IPv4
			if endpoint.Host == "" || endpoint.Port <= 0 {
				continue
			}
			targets = append(targets, CatalogProbeTarget{
				Key: catalogProbeTargetKey(region, carrier.Key), Label: region.Province + carrier.Name,
				Carrier: carrier.Key, Address: endpoint.Host, Port: endpoint.Port,
			})
		}
	}
	return targets
}

// catalogProbeTargetKey 是目录目标的稳定键（落库主键的一部分）；换省/市即整体
// 换键，旧键样本随新鲜度窗口过期、历史随保留窗口清理。
func catalogProbeTargetKey(region CatalogProbeRegion, carrierKey string) string {
	city := region.City
	if city == "" {
		city = "*"
	}
	return fmt.Sprintf("catalog:%s:%s:%s", region.Province, city, carrierKey)
}

func provinceCarrierEndpoints(province *cdncatalog.Province, carrierKey string) (cdncatalog.ProtocolEndpoints, bool) {
	switch carrierKey {
	case "telecom":
		return province.Carriers.Telecom, true
	case "unicom":
		return province.Carriers.Unicom, true
	case "mobile":
		return province.Carriers.Mobile, true
	}
	return cdncatalog.ProtocolEndpoints{}, false
}

// CatalogProbeSample 是一轮回报里的单条目录测速结果。
type CatalogProbeSample struct {
	Key       string
	Kind      string // 目录目标恒 tcp；保留字段与服务器对样本对称
	LatencyMS *float64
}

// CatalogProbeRow 是 server_catalog_probes 最新值行。
type CatalogProbeRow struct {
	FromServer int64
	TargetKey  string
	Kind       string
	LatencyMS  *float64
	MeasuredAt time.Time
}

// SaveServerCatalogProbes 覆盖写目录目标最新值并插入历史（latency.report 驱动）。
func (s *Store) SaveServerCatalogProbes(ctx context.Context, fromServer int64, samples []CatalogProbeSample, measuredAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin catalog probe: %w", err)
	}
	defer tx.Rollback()
	for _, sample := range samples {
		if sample.Key == "" || (sample.Kind != "tcp" && sample.Kind != "icmp") {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_catalog_probes
			(from_server, target_key, kind, latency_ms, measured_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(from_server, target_key, kind) DO UPDATE SET
			latency_ms=excluded.latency_ms, measured_at=excluded.measured_at`,
			fromServer, sample.Key, sample.Kind, sample.LatencyMS, measuredAt.UTC()); err != nil {
			return fmt.Errorf("upsert catalog probe: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_catalog_probe_history
			(from_server, target_key, kind, latency_ms, measured_at) VALUES (?, ?, ?, ?, ?)`,
			fromServer, sample.Key, sample.Kind, sample.LatencyMS, measuredAt.UTC()); err != nil {
			return fmt.Errorf("insert catalog probe history: %w", err)
		}
	}
	return tx.Commit()
}

// FreshServerCatalogProbes 返回新鲜度窗口内的目录目标最新值行（窗口外视为失效：
// agent 离线或区域切换后未再刷新的样本不再展示）。
func (s *Store) FreshServerCatalogProbes(ctx context.Context, maxAge time.Duration) ([]CatalogProbeRow, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	rows, err := s.db.QueryContext(ctx, `SELECT from_server, target_key, kind, latency_ms, measured_at
		FROM server_catalog_probes WHERE measured_at >= ? ORDER BY from_server, target_key, kind`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("fresh catalog probes: %w", err)
	}
	defer rows.Close()
	out := make([]CatalogProbeRow, 0)
	for rows.Next() {
		var row CatalogProbeRow
		if err := rows.Scan(&row.FromServer, &row.TargetKey, &row.Kind, &row.LatencyMS, &row.MeasuredAt); err != nil {
			return nil, fmt.Errorf("scan catalog probe: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// CatalogProbeLossRow 是一个 (服务器, 目录目标) 在统计窗口内的丢包聚合。
type CatalogProbeLossRow struct {
	FromServer int64
	TargetKey  string
	Samples    int64
	Failed     int64
}

// LossPercent 返回窗口内失败占比（百分数）；无样本返回 nil。
func (row CatalogProbeLossRow) LossPercent() *float64 {
	if row.Samples <= 0 {
		return nil
	}
	loss := float64(row.Failed) / float64(row.Samples) * 100
	return &loss
}

// CatalogProbeLossStats 聚合统计窗口内各 (服务器, 目录目标) 的丢包率
// （失败/超时样本占全部样本的比例，与 server_latency 的失败样本约定一致）。
func (s *Store) CatalogProbeLossStats(ctx context.Context, window time.Duration) ([]CatalogProbeLossRow, error) {
	cutoff := time.Now().UTC().Add(-window)
	rows, err := s.db.QueryContext(ctx, `SELECT from_server, target_key,
			COUNT(*) AS samples,
			SUM(CASE WHEN latency_ms IS NULL THEN 1 ELSE 0 END) AS failed
		FROM server_catalog_probe_history WHERE measured_at >= ?
		GROUP BY from_server, target_key`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("catalog probe loss stats: %w", err)
	}
	defer rows.Close()
	out := make([]CatalogProbeLossRow, 0)
	for rows.Next() {
		var row CatalogProbeLossRow
		if err := rows.Scan(&row.FromServer, &row.TargetKey, &row.Samples, &row.Failed); err != nil {
			return nil, fmt.Errorf("scan catalog probe loss: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// DeleteExpiredServerCatalogProbeHistory 清理保留窗口外的历史（后台小时任务，
// 与服务器间延迟历史同窗口）。
func (s *Store) DeleteExpiredServerCatalogProbeHistory(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM server_catalog_probe_history WHERE measured_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired catalog probe history: %w", err)
	}
	return result.RowsAffected()
}
