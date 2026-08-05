package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const DefaultTrafficTimezone = "Asia/Shanghai"

type TrafficCounterSnapshot struct {
	NodeID     int64
	EndpointID int64
	HopID      int64
	User       string
	Up         int64
	Down       int64
}

type ChainTraffic struct {
	ChainID       int64
	HopID         int64
	RawUp         int64
	RawDown       int64
	EffectiveUp   int64
	EffectiveDown int64
}

type ChainTrafficBucket struct {
	Date          string `json:"date"`
	RawUp         int64  `json:"raw_up"`
	RawDown       int64  `json:"raw_down"`
	EffectiveUp   int64  `json:"effective_up"`
	EffectiveDown int64  `json:"effective_down"`
}

func (s *Store) ApplyTrafficSnapshot(ctx context.Context, serverID int64, instanceID string, counters []TrafficCounterSnapshot, sampledAt time.Time) error {
	if instanceID == "" {
		return fmt.Errorf("xray instance id is required")
	}
	timezone, location := s.trafficLocation(ctx)
	usageDate := sampledAt.In(location).Format("2006-01-02")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, counter := range counters {
		if counter.Up < 0 || counter.Down < 0 {
			continue
		}
		key := trafficCounterKey(counter)
		if key == "" {
			continue
		}
		up, down, err := trafficDelta(tx, serverID, key, instanceID, counter.Up, counter.Down)
		if err != nil {
			return err
		}
		if up == 0 && down == 0 {
			continue
		}
		if counter.User != "" {
			if strings.HasPrefix(counter.User, "tunnel:") {
				continue
			}
			userUUID, chainID, revisionID, multiplier, access, err := accessTrafficOwner(tx, serverID, counter.User)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if !access {
				userUUID = counter.User
			}
			if err := addTrafficTx(tx, 0, userUUID, up, down); err != nil {
				return err
			}
			if access {
				if err := addChainTrafficTx(tx, chainID, 0, revisionID, multiplier, up, down, usageDate, timezone); err != nil {
					return err
				}
			}
			continue
		}
		if counter.EndpointID != 0 {
			if _, err := tx.Exec(`INSERT INTO endpoint_traffic_totals (endpoint_id, up, down, updated_at)
				VALUES (?, ?, ?, CURRENT_TIMESTAMP) ON CONFLICT(endpoint_id) DO UPDATE SET
				up=up+excluded.up, down=down+excluded.down, updated_at=excluded.updated_at`,
				counter.EndpointID, up, down); err != nil {
				return err
			}
			continue
		}
		if counter.NodeID != 0 {
			if err := addTrafficTx(tx, counter.NodeID, "", up, down); err != nil {
				return err
			}
			chainID, exitHopID, revisionID, multiplier, sharedEntry, err := publishedChainForCounter(tx, serverID, counter.NodeID, 0)
			if err != nil {
				if err == sql.ErrNoRows {
					continue
				}
				return err
			}
			if !sharedEntry {
				if err := addChainTrafficTx(tx, chainID, 0, revisionID, multiplier, up, down, usageDate, timezone); err != nil {
					return err
				}
			}
			if exitHopID != 0 {
				if err := addChainTrafficTx(tx, chainID, exitHopID, revisionID, multiplier, up, down, usageDate, timezone); err != nil {
					return err
				}
			}
			continue
		}
		chainID, _, revisionID, multiplier, _, err := publishedChainForCounter(tx, serverID, 0, counter.HopID)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
		if err := addChainTrafficTx(tx, chainID, counter.HopID, revisionID, multiplier, up, down, usageDate, timezone); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func trafficCounterKey(counter TrafficCounterSnapshot) string {
	switch {
	case counter.NodeID > 0:
		return "node:" + strconv.FormatInt(counter.NodeID, 10)
	case counter.HopID > 0:
		return "hop:" + strconv.FormatInt(counter.HopID, 10)
	case counter.EndpointID > 0:
		return "endpoint:" + strconv.FormatInt(counter.EndpointID, 10)
	case counter.User != "":
		return "user:" + counter.User
	default:
		return ""
	}
}

func trafficDelta(tx *sql.Tx, serverID int64, key, instanceID string, currentUp, currentDown int64) (int64, int64, error) {
	var previousInstance string
	var previousUp, previousDown int64
	err := tx.QueryRow(`SELECT instance_id, up, down FROM traffic_cursors WHERE server_id = ? AND counter_key = ?`, serverID, key).
		Scan(&previousInstance, &previousUp, &previousDown)
	up, down := currentUp, currentDown
	if err == nil && previousInstance == instanceID {
		if currentUp >= previousUp {
			up = currentUp - previousUp
		}
		if currentDown >= previousDown {
			down = currentDown - previousDown
		}
	} else if err != nil && err != sql.ErrNoRows {
		return 0, 0, fmt.Errorf("read traffic cursor: %w", err)
	}
	_, err = tx.Exec(`INSERT INTO traffic_cursors (server_id, counter_key, instance_id, up, down, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(server_id, counter_key) DO UPDATE SET instance_id=excluded.instance_id,
		up=excluded.up, down=excluded.down, updated_at=excluded.updated_at`, serverID, key, instanceID, currentUp, currentDown)
	return up, down, err
}

func addTrafficTx(tx *sql.Tx, nodeID int64, user string, up, down int64) error {
	_, err := tx.Exec(`INSERT INTO traffic (node_id, user_uuid, up, down, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(node_id, user_uuid) DO UPDATE SET up=up+excluded.up, down=down+excluded.down,
		updated_at=excluded.updated_at`, nodeID, user, up, down)
	return err
}

func publishedChainForCounter(tx *sql.Tx, serverID, nodeID, hopID int64) (chainID, exitHopID, revisionID int64, multiplier int, sharedEntry bool, err error) {
	query := `SELECT c.id, c.published_revision_id, c.traffic_multiplier_milli, r.snapshot FROM chains c
		JOIN chain_revisions r ON r.id=c.published_revision_id
		WHERE c.deleted_at IS NULL`
	args := []any{}
	if nodeID != 0 {
		query += ` AND c.service_node_id=?`
		args = append(args, nodeID)
	}
	query += ` ORDER BY c.id`
	rows, err := tx.Query(query, args...)
	if err != nil {
		return 0, 0, 0, 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var publishedID int64
		var milli int
		var raw string
		if err := rows.Scan(&id, &publishedID, &milli, &raw); err != nil {
			return 0, 0, 0, 0, false, err
		}
		var snapshot ChainRevisionSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil || len(snapshot.Hops) == 0 {
			continue
		}
		exit := snapshot.Hops[len(snapshot.Hops)-1]
		if nodeID != 0 && snapshot.ServiceNodeID == nodeID && snapshot.ServiceServerID == serverID {
			return id, exit.HopID, publishedID, milli, snapshot.EndpointID != 0, nil
		}
		if hopID != 0 {
			for _, hop := range snapshot.Hops {
				if hop.HopID == hopID && hop.ServerID == serverID {
					return id, exit.HopID, publishedID, milli, snapshot.EndpointID != 0, nil
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, 0, false, err
	}
	return 0, 0, 0, 0, false, sql.ErrNoRows
}

func accessTrafficOwner(tx *sql.Tx, serverID int64, identity string) (userUUID string,
	chainID, revisionID int64, multiplier int, access bool, err error) {
	if strings.HasPrefix(identity, "group:") {
		// 分组派生身份 group:<user_uuid>:<chain_id>（store.UserChainAssignment.Identity），
		// 用户 UUID 内嵌于身份；仅校验链路归属本服务器即入账。
		rest := strings.TrimPrefix(identity, "group:")
		idx := strings.LastIndex(rest, ":")
		if idx <= 0 {
			return "", 0, 0, 0, false, sql.ErrNoRows
		}
		chainID, err = strconv.ParseInt(rest[idx+1:], 10, 64)
		if err != nil || chainID <= 0 {
			return "", 0, 0, 0, false, sql.ErrNoRows
		}
		err = tx.QueryRow(`SELECT c.published_revision_id, c.traffic_multiplier_milli
			FROM chains c JOIN shared_endpoints e ON e.id=c.endpoint_id
			WHERE c.id=? AND e.server_id=? AND c.published_revision_id!=0 AND c.deleted_at IS NULL`,
			chainID, serverID).Scan(&revisionID, &multiplier)
		if err != nil {
			return "", 0, 0, 0, false, err
		}
		return rest[:idx], chainID, revisionID, multiplier, true, nil
	}
	if !strings.HasPrefix(identity, "access:") {
		return "", 0, 0, 0, false, nil
	}
	assignmentID, parseErr := strconv.ParseInt(strings.TrimPrefix(identity, "access:"), 10, 64)
	if parseErr != nil || assignmentID <= 0 {
		return "", 0, 0, 0, false, sql.ErrNoRows
	}
	err = tx.QueryRow(`SELECT u.uuid, c.id, c.published_revision_id, c.traffic_multiplier_milli
		FROM user_chain_assignments a JOIN users u ON u.id=a.user_id
		JOIN chains c ON c.id=a.chain_id JOIN shared_endpoints e ON e.id=c.endpoint_id
		WHERE a.id=? AND e.server_id=? AND c.published_revision_id!=0 AND c.deleted_at IS NULL`,
		assignmentID, serverID).Scan(&userUUID, &chainID, &revisionID, &multiplier)
	return userUUID, chainID, revisionID, multiplier, err == nil, err
}

func addChainTrafficTx(tx *sql.Tx, chainID, hopID, revisionID int64, multiplier int, up, down int64, usageDate, timezone string) error {
	var remainderUp, remainderDown int64
	err := tx.QueryRow(`SELECT remainder_up, remainder_down FROM chain_traffic_totals WHERE chain_id=? AND hop_id=?`, chainID, hopID).
		Scan(&remainderUp, &remainderDown)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	effectiveUp, nextRemainderUp := multiplyTraffic(up, multiplier, remainderUp)
	effectiveDown, nextRemainderDown := multiplyTraffic(down, multiplier, remainderDown)
	_, err = tx.Exec(`INSERT INTO chain_traffic_totals
		(chain_id, hop_id, raw_up, raw_down, effective_up, effective_down, remainder_up, remainder_down, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chain_id, hop_id) DO UPDATE SET
		raw_up=raw_up+excluded.raw_up, raw_down=raw_down+excluded.raw_down,
		effective_up=effective_up+excluded.effective_up, effective_down=effective_down+excluded.effective_down,
		remainder_up=excluded.remainder_up, remainder_down=excluded.remainder_down,
		updated_at=excluded.updated_at`, chainID, hopID, up, down, effectiveUp, effectiveDown, nextRemainderUp, nextRemainderDown)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO chain_traffic_daily
		(chain_id, hop_id, revision_id, usage_date, timezone, raw_up, raw_down, effective_up, effective_down)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chain_id, hop_id, revision_id, usage_date, timezone) DO UPDATE SET
		raw_up=raw_up+excluded.raw_up, raw_down=raw_down+excluded.raw_down,
		effective_up=effective_up+excluded.effective_up, effective_down=effective_down+excluded.effective_down`,
		chainID, hopID, revisionID, usageDate, timezone, up, down, effectiveUp, effectiveDown)
	return err
}

func multiplyTraffic(value int64, multiplier int, remainder int64) (int64, int64) {
	whole := value / 1000
	fraction := value % 1000
	effective := whole*int64(multiplier) + (fraction*int64(multiplier)+remainder)/1000
	nextRemainder := (fraction*int64(multiplier) + remainder) % 1000
	return effective, nextRemainder
}

func (s *Store) trafficLocation(ctx context.Context) (string, *time.Location) {
	name, _ := s.GetSetting(ctx, SettingTrafficTimezone)
	if name == "" {
		name = DefaultTrafficTimezone
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return DefaultTrafficTimezone, mustLocation(DefaultTrafficTimezone)
	}
	return name, location
}

func (s *Store) TrafficLocation(ctx context.Context) (string, *time.Location) {
	return s.trafficLocation(ctx)
}

func mustLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return location
}

func (s *Store) ChainTrafficTotals(ctx context.Context, chainID int64) ([]ChainTraffic, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.chain_id, t.hop_id,
		t.raw_up-COALESCE(b.raw_up,0), t.raw_down-COALESCE(b.raw_down,0),
		t.effective_up-COALESCE(b.effective_up,0), t.effective_down-COALESCE(b.effective_down,0)
		FROM chain_traffic_totals t LEFT JOIN chain_traffic_baselines b
		ON b.chain_id=t.chain_id AND b.hop_id=t.hop_id WHERE t.chain_id=? ORDER BY t.hop_id`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChainTraffic
	for rows.Next() {
		var item ChainTraffic
		if err := rows.Scan(&item.ChainID, &item.HopID, &item.RawUp, &item.RawDown, &item.EffectiveUp, &item.EffectiveDown); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ResetChainTraffic(ctx context.Context, chainID int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO chain_traffic_baselines
		(chain_id, hop_id, raw_up, raw_down, effective_up, effective_down, reset_at)
		SELECT chain_id, hop_id, raw_up, raw_down, effective_up, effective_down, CURRENT_TIMESTAMP
		FROM chain_traffic_totals WHERE chain_id=?
		ON CONFLICT(chain_id, hop_id) DO UPDATE SET raw_up=excluded.raw_up, raw_down=excluded.raw_down,
		effective_up=excluded.effective_up, effective_down=excluded.effective_down, reset_at=excluded.reset_at`, chainID)
	return err
}

func (s *Store) SetChainTrafficMultiplier(ctx context.Context, chainID int64, milli int) error {
	if milli < 1 || milli > 1_000_000 {
		return fmt.Errorf("traffic multiplier must be between 0.001 and 1000.000")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE chains SET traffic_multiplier_milli=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, milli, chainID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chain_multiplier_events (chain_id, multiplier_milli) VALUES (?, ?)`, chainID, milli); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ChainTrafficDaily(ctx context.Context, chainID, hopID int64, since string) ([]ChainTrafficBucket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT usage_date, SUM(raw_up), SUM(raw_down),
		SUM(effective_up), SUM(effective_down) FROM chain_traffic_daily
		WHERE chain_id=? AND hop_id=? AND usage_date>=? GROUP BY usage_date ORDER BY usage_date`, chainID, hopID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChainTrafficBucket, 0)
	for rows.Next() {
		var bucket ChainTrafficBucket
		if err := rows.Scan(&bucket.Date, &bucket.RawUp, &bucket.RawDown, &bucket.EffectiveUp, &bucket.EffectiveDown); err != nil {
			return nil, err
		}
		out = append(out, bucket)
	}
	return out, rows.Err()
}
