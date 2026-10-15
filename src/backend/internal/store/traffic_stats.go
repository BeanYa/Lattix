package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// addTrafficStatTx 将一笔遥测增量同时累加进分钟明细桶与日汇总（同一事务）。
// 记录原始字节，不乘链路倍率（计费口径归 chain_traffic_daily）。
// dimType: "user" | "node" | "endpoint" | "chain_hop"；dimID: 用户UUID或十进制ID字符串。
func (s *Store) addTrafficStatTx(tx *sql.Tx, serverID int64, bucketTS int64, usageDate, dimType, dimID string, up, down int64) error {
	if _, err := tx.Exec(`INSERT INTO traffic_stats_bucket (server_id, dim_type, dim_id, bucket_ts, up, down)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id, dim_type, dim_id, bucket_ts) DO UPDATE SET
		up=up+excluded.up, down=down+excluded.down`,
		serverID, dimType, dimID, bucketTS, up, down); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO traffic_stats_daily (server_id, dim_type, dim_id, usage_date, up, down)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(server_id, dim_type, dim_id, usage_date) DO UPDATE SET
		up=up+excluded.up, down=down+excluded.down`,
		serverID, dimType, dimID, usageDate, up, down)
	return err
}

// TrafficStatPoint 单个聚合桶：TS 语义按粒度——粒度≤1h 为桶起始 Unix 秒，
// day 为本地当日零点，month 为本地当月一日零点。
type TrafficStatPoint struct {
	TS   int64 `json:"ts"`
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// TrafficVolume 上/下行原始字节合计。
type TrafficVolume struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// TrafficStatGranularity 聚合粒度："5m" | "15m" | "1h" | "day" | "month"。
type TrafficStatGranularity string

// TrafficStatTopItem Top 榜条目：top_servers 用 ServerID/servers.alias，
// top_users 用 UserUUID/users.name。
type TrafficStatTopItem struct {
	ServerID int64  `json:"server_id"`
	UserUUID string `json:"user_uuid"`
	Label    string `json:"label"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
}

// TrafficStatsOverview 全网概览：总量/环比取用户维度（端到端只计一次），
// Top 服务器取实体维度（可与供应商对账），Top 用户取用户维度；Top 榜按本月日汇总。
type TrafficStatsOverview struct {
	Today      TrafficVolume        `json:"today"`
	TodayPrev  TrafficVolume        `json:"today_prev"`
	Month      TrafficVolume        `json:"month"`
	MonthPrev  TrafficVolume        `json:"month_prev"`
	TopServers []TrafficStatTopItem `json:"top_servers"`
	TopUsers   []TrafficStatTopItem `json:"top_users"`
}

// TrafficStatBreakdownRow 下钻明细行：按服务器时 dim_type 为原始维度，
// 按用户时为 "server"（GROUP BY server_id）。
type TrafficStatBreakdownRow struct {
	DimType string `json:"dim_type"`
	DimID   string `json:"dim_id"`
	Label   string `json:"label"`
	Up      int64  `json:"up"`
	Down    int64  `json:"down"`
}

// trafficStatsDimWhere 返回 dim 口径的 WHERE 片段（不含 WHERE 关键字）：
// fleet=仅用户维度（全网总量去重口径）；server=该服务器实体维度；user=该用户跨服务器。
func trafficStatsDimWhere(dim string, serverID int64, userUUID string) (string, []any, error) {
	switch dim {
	case "fleet":
		return "dim_type='user'", nil, nil
	case "server":
		if serverID <= 0 {
			return "", nil, fmt.Errorf("server dim requires server_id")
		}
		return "server_id=? AND dim_type IN ('node','chain_hop','endpoint')", []any{serverID}, nil
	case "user":
		if userUUID == "" {
			return "", nil, fmt.Errorf("user dim requires user_uuid")
		}
		return "dim_type='user' AND dim_id=?", []any{userUUID}, nil
	default:
		return "", nil, fmt.Errorf("unknown traffic stat dim %q", dim)
	}
}

// scanTrafficStatPoints 统一 (ts, up, down) 三列扫描循环，空结果返回非 nil 空切片。
func scanTrafficStatPoints(rows *sql.Rows) ([]TrafficStatPoint, error) {
	defer rows.Close()
	points := make([]TrafficStatPoint, 0)
	for rows.Next() {
		var point TrafficStatPoint
		if err := rows.Scan(&point.TS, &point.Up, &point.Down); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, rows.Err()
}

// TrafficStatSeries 按粒度聚合时序。5m/15m/1h 走明细桶 GROUP BY (bucket_ts/window)*window
// （半开区间 [from,to)）；day/month 走日汇总，按本地日期 / substr(usage_date,1,7) 分组，
// 日期边界取 from/to 的本地日期（含端点，与窗口重叠的本地日整日计入）。
// 排序按 TS 升序；空范围或无数据返回空切片。
func (s *Store) TrafficStatSeries(ctx context.Context, dim string, serverID int64, userUUID string, g TrafficStatGranularity, from, to time.Time) ([]TrafficStatPoint, error) {
	dimWhere, dimArgs, err := trafficStatsDimWhere(dim, serverID, userUUID)
	if err != nil {
		return nil, err
	}
	if !to.After(from) {
		return make([]TrafficStatPoint, 0), nil
	}
	switch g {
	case "5m", "15m", "1h":
		window := int64(0)
		switch g {
		case "5m":
			window = 300
		case "15m":
			window = 900
		default:
			window = 3600
		}
		// SELECT 中的两个 window 占位符位于 dim 片段之前，参数按出现顺序排列。
		args := append([]any{window, window}, dimArgs...)
		args = append(args, from.Unix(), to.Unix())
		rows, err := s.db.QueryContext(ctx, `SELECT (bucket_ts/?)*? AS window_ts, SUM(up), SUM(down)
			FROM traffic_stats_bucket
			WHERE `+dimWhere+` AND bucket_ts>=? AND bucket_ts<?
			GROUP BY window_ts ORDER BY window_ts`, args...)
		if err != nil {
			return nil, err
		}
		return scanTrafficStatPoints(rows)
	case "day", "month":
		_, location := s.trafficLocation(ctx)
		args := append([]any{}, dimArgs...)
		var query string
		if g == "day" {
			args = append(args, from.In(location).Format("2006-01-02"), to.In(location).Format("2006-01-02"))
			query = `SELECT usage_date, SUM(up), SUM(down)
				FROM traffic_stats_daily
				WHERE ` + dimWhere + ` AND usage_date>=? AND usage_date<=?
				GROUP BY usage_date ORDER BY usage_date`
		} else {
			args = append(args, from.In(location).Format("2006-01"), to.In(location).Format("2006-01"))
			query = `SELECT substr(usage_date,1,7) AS usage_month, SUM(up), SUM(down)
				FROM traffic_stats_daily
				WHERE ` + dimWhere + ` AND substr(usage_date,1,7)>=? AND substr(usage_date,1,7)<=?
				GROUP BY usage_month ORDER BY usage_month`
		}
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		layout := "2006-01-02"
		if g == "month" {
			layout = "2006-01"
		}
		points := make([]TrafficStatPoint, 0)
		for rows.Next() {
			var key string
			var point TrafficStatPoint
			if err := rows.Scan(&key, &point.Up, &point.Down); err != nil {
				return nil, err
			}
			local, err := time.ParseInLocation(layout, key, location)
			if err != nil {
				return nil, fmt.Errorf("parse traffic stats %s key %q: %w", g, key, err)
			}
			point.TS = local.Unix()
			points = append(points, point)
		}
		return points, rows.Err()
	default:
		return nil, fmt.Errorf("unknown traffic stat granularity %q", g)
	}
}

// TrafficStatsOverview 全网概览（用户维度）+ 本月 Top 榜。环比为同进度对比：
// TodayPrev = 昨日 [startToday-24h, now-24h)；MonthPrev = 上月全月 − 上月同进度
// 之后的尾部（[startLastMonth+elapsed, startMonth)，桶龄≤31 天，保留期最小值保证其存在）。
func (s *Store) TrafficStatsOverview(ctx context.Context, topN int) (*TrafficStatsOverview, error) {
	if topN <= 0 {
		// SQLite 的负数 LIMIT 表示无限制，兜底为默认条数。
		topN = 10
	}
	_, location := s.trafficLocation(ctx)
	now := time.Now().In(location)
	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	startMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location)
	startLastMonth := startMonth.AddDate(0, -1, 0)
	elapsed := now.Sub(startMonth)

	overview := &TrafficStatsOverview{TopServers: make([]TrafficStatTopItem, 0), TopUsers: make([]TrafficStatTopItem, 0)}
	// Today / Month：用户维度日汇总（本地今日 / 本月日期前缀）。
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(up),0), COALESCE(SUM(down),0) FROM traffic_stats_daily
			WHERE dim_type='user' AND usage_date=?`, now.Format("2006-01-02")).
		Scan(&overview.Today.Up, &overview.Today.Down); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(up),0), COALESCE(SUM(down),0) FROM traffic_stats_daily
			WHERE dim_type='user' AND substr(usage_date,1,7)=?`, startMonth.Format("2006-01")).
		Scan(&overview.Month.Up, &overview.Month.Down); err != nil {
		return nil, err
	}
	// TodayPrev：用户维度分钟桶，昨日同进度窗口。
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(up),0), COALESCE(SUM(down),0) FROM traffic_stats_bucket
			WHERE dim_type='user' AND bucket_ts>=? AND bucket_ts<?`,
		startToday.Add(-24*time.Hour).Unix(), now.Add(-24*time.Hour).Unix()).
		Scan(&overview.TodayPrev.Up, &overview.TodayPrev.Down); err != nil {
		return nil, err
	}
	// MonthPrev：上月全月日汇总 − 同进度之后的尾部分钟桶。
	lastMonth, tail := TrafficVolume{}, TrafficVolume{}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(up),0), COALESCE(SUM(down),0) FROM traffic_stats_daily
			WHERE dim_type='user' AND substr(usage_date,1,7)=?`, startLastMonth.Format("2006-01")).
		Scan(&lastMonth.Up, &lastMonth.Down); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(up),0), COALESCE(SUM(down),0) FROM traffic_stats_bucket
			WHERE dim_type='user' AND bucket_ts>=? AND bucket_ts<?`,
		startLastMonth.Add(elapsed).Unix(), startMonth.Unix()).
		Scan(&tail.Up, &tail.Down); err != nil {
		return nil, err
	}
	overview.MonthPrev = TrafficVolume{Up: lastMonth.Up - tail.Up, Down: lastMonth.Down - tail.Down}

	// Top 服务器：实体维度按本月日汇总 GROUP BY server_id，label 取 servers.alias。
	serverRows, err := s.db.QueryContext(ctx, `SELECT t.server_id, s.alias, SUM(t.up), SUM(t.down)
		FROM traffic_stats_daily t JOIN servers s ON s.id=t.server_id
		WHERE t.dim_type IN ('node','chain_hop','endpoint') AND substr(t.usage_date,1,7)=?
		GROUP BY t.server_id ORDER BY SUM(t.up)+SUM(t.down) DESC, t.server_id LIMIT ?`,
		startMonth.Format("2006-01"), topN)
	if err != nil {
		return nil, err
	}
	defer serverRows.Close()
	for serverRows.Next() {
		item := TrafficStatTopItem{}
		if err := serverRows.Scan(&item.ServerID, &item.Label, &item.Up, &item.Down); err != nil {
			return nil, err
		}
		overview.TopServers = append(overview.TopServers, item)
	}
	if err := serverRows.Err(); err != nil {
		return nil, err
	}

	// Top 用户：用户维度按本月日汇总 GROUP BY dim_id，label 取 users.name。
	userRows, err := s.db.QueryContext(ctx, `SELECT t.dim_id, u.name, SUM(t.up), SUM(t.down)
		FROM traffic_stats_daily t JOIN users u ON u.uuid=t.dim_id
		WHERE t.dim_type='user' AND substr(t.usage_date,1,7)=?
		GROUP BY t.dim_id ORDER BY SUM(t.up)+SUM(t.down) DESC, t.dim_id LIMIT ?`,
		startMonth.Format("2006-01"), topN)
	if err != nil {
		return nil, err
	}
	defer userRows.Close()
	for userRows.Next() {
		item := TrafficStatTopItem{}
		if err := userRows.Scan(&item.UserUUID, &item.Label, &item.Up, &item.Down); err != nil {
			return nil, err
		}
		overview.TopUsers = append(overview.TopUsers, item)
	}
	return overview, userRows.Err()
}

// TrafficStatBreakdown 下钻明细（分钟桶聚合 [from,to)，按 up+down 降序 LIMIT 50）：
// serverID>0 → 该服务器全部维度行（user 行 JOIN users.name、node 行 JOIN nodes.name、
// endpoint/chain_hop 行 label 为空串）；userUUID!="" → 该用户 GROUP BY server_id
//（dim_type="server"，JOIN servers.alias）。两者同给时按服务器口径。
func (s *Store) TrafficStatBreakdown(ctx context.Context, serverID int64, userUUID string, from, to time.Time) ([]TrafficStatBreakdownRow, error) {
	rows := make([]TrafficStatBreakdownRow, 0)
	if !to.After(from) {
		return rows, nil
	}
	if serverID > 0 {
		result, err := s.db.QueryContext(ctx, `SELECT t.dim_type, t.dim_id,
			CASE WHEN t.dim_type='user' THEN COALESCE(u.name,'')
			     WHEN t.dim_type='node' THEN COALESCE(n.name,'')
			     ELSE '' END AS label,
			SUM(t.up), SUM(t.down)
			FROM traffic_stats_bucket t
			LEFT JOIN users u ON t.dim_type='user' AND u.uuid=t.dim_id
			LEFT JOIN nodes n ON t.dim_type='node' AND n.id=CAST(t.dim_id AS INTEGER)
			WHERE t.server_id=? AND t.bucket_ts>=? AND t.bucket_ts<?
			GROUP BY t.dim_type, t.dim_id
			ORDER BY SUM(t.up)+SUM(t.down) DESC, t.dim_type, t.dim_id LIMIT 50`,
			serverID, from.Unix(), to.Unix())
		if err != nil {
			return nil, err
		}
		defer result.Close()
		for result.Next() {
			var row TrafficStatBreakdownRow
			if err := result.Scan(&row.DimType, &row.DimID, &row.Label, &row.Up, &row.Down); err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
		return rows, result.Err()
	}
	if userUUID != "" {
		result, err := s.db.QueryContext(ctx, `SELECT t.server_id, s.alias, SUM(t.up), SUM(t.down)
			FROM traffic_stats_bucket t JOIN servers s ON s.id=t.server_id
			WHERE t.dim_type='user' AND t.dim_id=? AND t.bucket_ts>=? AND t.bucket_ts<?
			GROUP BY t.server_id
			ORDER BY SUM(t.up)+SUM(t.down) DESC, t.server_id LIMIT 50`,
			userUUID, from.Unix(), to.Unix())
		if err != nil {
			return nil, err
		}
		defer result.Close()
		for result.Next() {
			var serverID int64
			var row TrafficStatBreakdownRow
			if err := result.Scan(&serverID, &row.Label, &row.Up, &row.Down); err != nil {
				return nil, err
			}
			row.DimType = "server"
			row.DimID = strconv.FormatInt(serverID, 10)
			rows = append(rows, row)
		}
		return rows, result.Err()
	}
	return rows, nil
}

// ServerTrafficBuckets 单服务器逐分钟流量（实体维度合计，明细桶原样），供
// server-history 端点绘制 24h 速率图；空范围返回空切片。
func (s *Store) ServerTrafficBuckets(ctx context.Context, serverID int64, from, to time.Time) ([]TrafficStatPoint, error) {
	if !to.After(from) {
		return make([]TrafficStatPoint, 0), nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT bucket_ts, SUM(up), SUM(down)
		FROM traffic_stats_bucket
		WHERE server_id=? AND dim_type IN ('node','chain_hop','endpoint') AND bucket_ts>=? AND bucket_ts<?
		GROUP BY bucket_ts ORDER BY bucket_ts`, serverID, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	return scanTrafficStatPoints(rows)
}
