package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lattix/shared"
)

// ErrNotFound 表示查询的行不存在。
var ErrNotFound = errors.New("store: not found")

// 机器类型（§21 NAT 两档）：direct = 独立 IP；nat = NAT（allowed_ports 非空 = 受限直连，
// 留空 = 仅出口档）。建后不允许互转。
const (
	MachineTypeDirect = "direct"
	MachineTypeNAT    = "nat"
	AddressModeAuto   = "auto"
	AddressModeManual = "manual"
)

// Server 是 servers 表的一行（§4）。
type Server struct {
	ID                       int64
	Alias                    string
	Token                    string // 长期凭证；创建时先存 bootstrap token，session.open 后换发（§11）
	LastSeenAt               *time.Time
	XrayVersion              string
	AgentVersion             string // session.open 上报的 agent 版本（§18 升级管理）
	Address                  string // 公网地址（session.open 时按 WS RemoteAddr 记录，订阅用，§9）
	AddressMode              string // auto|manual；manual 地址不被后续 session.open 覆盖
	LearnedAddr              string // 每次 session.open 学习的公网地址；容器网关对端回退到 agent 公网网卡（§9）
	NICAddresses             string // agent 上报的网卡非回环地址 JSON 数组（§9 公网地址候选）；空串 = 无
	ConfigDrift              bool   // 配置漂移标志（§17，agent drift_report 驱动）
	MachineType              string // direct|nat（§21）
	AllowedPorts             string // NAT 可用端口段 JSON（shared.PortRange 数组，§21）；空串 = 无段
	Tags                     string // 管理标签 JSON 数组；名称模板 {{TAG_n}} 的来源
	CountryCode              string // ISO 3166-1 alpha-2；管理员在服务器资料中选择
	Location                 string // 城市或机房位置；管理员填写
	CredentialEpoch          int64
	CredentialCommitted      bool
	CredentialPendingToken   string
	CredentialExchangeID     string
	LastConnectedAt          *time.Time
	LastDisconnectedAt       *time.Time
	LastReconnectedAt        *time.Time
	ReconnectCount           int64
	LastDisconnectReason     string
	AgentSettingsRevision    int64
	AgentSettingsError       string
	AgentSettingsReportedAt  *time.Time
	CustomSettings           string // 服务器级覆盖 JSON（空串 = 无覆盖）
	ServerSettingsRevision   int64
	ServerSettingsError      string
	ServerSettingsReportedAt *time.Time
	CreatedAt                time.Time
}

// serverCols 是 Server 各字段对应的列清单。
const serverCols = `id, alias, token, last_seen_at, xray_version, agent_version, address, address_mode, learned_addr, nic_addresses, config_drift, machine_type, allowed_ports, tags, country_code, location, credential_epoch, credential_committed, credential_pending_token, credential_exchange_id, last_connected_at, last_disconnected_at, last_reconnected_at, reconnect_count, last_disconnect_reason, agent_settings_revision, agent_settings_error, agent_settings_reported_at, custom_settings, server_settings_revision, server_settings_error, server_settings_reported_at, created_at`

func scanServer(row interface{ Scan(...any) error }) (*Server, error) {
	var srv Server
	var lastSeen, lastConnected, lastDisconnected, lastReconnected, settingsReported, serverSettingsReported sql.NullTime
	var xrayVer, agentVer sql.NullString
	err := row.Scan(&srv.ID, &srv.Alias, &srv.Token, &lastSeen, &xrayVer, &agentVer, &srv.Address, &srv.AddressMode, &srv.LearnedAddr, &srv.NICAddresses, &srv.ConfigDrift, &srv.MachineType, &srv.AllowedPorts, &srv.Tags, &srv.CountryCode, &srv.Location, &srv.CredentialEpoch, &srv.CredentialCommitted, &srv.CredentialPendingToken, &srv.CredentialExchangeID, &lastConnected, &lastDisconnected, &lastReconnected, &srv.ReconnectCount, &srv.LastDisconnectReason, &srv.AgentSettingsRevision, &srv.AgentSettingsError, &settingsReported, &srv.CustomSettings, &srv.ServerSettingsRevision, &srv.ServerSettingsError, &serverSettingsReported, &srv.CreatedAt)
	if err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		t := lastSeen.Time
		srv.LastSeenAt = &t
	}
	if lastConnected.Valid {
		t := lastConnected.Time
		srv.LastConnectedAt = &t
	}
	if lastDisconnected.Valid {
		t := lastDisconnected.Time
		srv.LastDisconnectedAt = &t
	}
	if lastReconnected.Valid {
		t := lastReconnected.Time
		srv.LastReconnectedAt = &t
	}
	srv.XrayVersion = xrayVer.String
	srv.AgentVersion = agentVer.String
	if settingsReported.Valid {
		t := settingsReported.Time
		srv.AgentSettingsReportedAt = &t
	}
	if serverSettingsReported.Valid {
		t := serverSettingsReported.Time
		srv.ServerSettingsReportedAt = &t
	}
	return &srv, nil
}

// CreateServer 插入一台服务器，token 为一次性 bootstrap token（§11），返回服务器 id。
// address 为管理员指定的公网地址（§4）；空串表示留待 session.open 时按 RemoteAddr 自动学习
// （NAT 类型强制必填，由 panel 校验）。machineType/allowedPorts 为 NAT 元数据（§21，面板侧，不下发 agent）。
func (s *Store) CreateServer(ctx context.Context, alias, address, bootstrapToken, machineType, allowedPorts, tags, countryCode, location string) (int64, error) {
	return s.createServer(ctx, s.db, alias, address, bootstrapToken, machineType, allowedPorts, tags, countryCode, location)
}

func (s *Store) createServer(ctx context.Context, exec contextExecer, alias, address, bootstrapToken, machineType, allowedPorts, tags, countryCode, location string) (int64, error) {
	epoch := int64(1)
	if credential, err := shared.ParseCredential(bootstrapToken); err == nil {
		epoch = credential.Epoch
	}
	addressMode := AddressModeAuto
	if address != "" {
		addressMode = AddressModeManual
	}
	res, err := exec.ExecContext(ctx,
		`INSERT INTO servers (alias, address, address_mode, token, machine_type, allowed_ports, tags, country_code, location, credential_epoch) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		alias, address, addressMode, bootstrapToken, machineType, allowedPorts, tags, countryCode, location, epoch)
	if err != nil {
		return 0, fmt.Errorf("insert server: %w", err)
	}
	return res.LastInsertId()
}

// CreateServerWithPlans makes the server and its initial accounting policies
// visible together. Callers never observe a server missing its default plan.
func (s *Store) CreateServerWithPlans(
	ctx context.Context,
	alias, address, bootstrapToken, machineType, allowedPorts, tags, countryCode, location string,
	billing *ServerBilling,
	traffic ServerTrafficPlan,
) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin server creation: %w", err)
	}
	defer tx.Rollback()
	id, err := s.createServer(ctx, tx, alias, address, bootstrapToken, machineType, allowedPorts, tags, countryCode, location)
	if err != nil {
		return 0, err
	}
	if billing != nil {
		billing.ServerID = id
		if err := upsertServerBilling(ctx, tx, *billing); err != nil {
			return 0, fmt.Errorf("save initial server billing: %w", err)
		}
	}
	traffic.ServerID = id
	if err := upsertServerTrafficPlan(ctx, tx, traffic); err != nil {
		return 0, fmt.Errorf("save initial server traffic plan: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit server creation: %w", err)
	}
	return id, nil
}

// ServerByToken 按 token（bootstrap 或长期）查找服务器。
func (s *Store) ServerByToken(ctx context.Context, token string) (*Server, error) {
	srv, err := scanServer(s.db.QueryRowContext(ctx,
		`SELECT `+serverCols+` FROM servers WHERE token = ? OR credential_pending_token = ?`, token, token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query server by token: %w", err)
	}
	return srv, nil
}

func (s *Store) SetPendingCredential(ctx context.Context, id int64, token, exchangeID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE servers
		SET credential_pending_token = ?, credential_exchange_id = ?
		WHERE id = ? AND credential_committed = 0 AND credential_pending_token = ''`,
		token, exchangeID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) CommitPendingCredential(ctx context.Context, id int64, exchangeID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE servers
		SET token = credential_pending_token, credential_pending_token = '',
			credential_exchange_id = '', credential_committed = 1
		WHERE id = ? AND credential_committed = 0 AND credential_exchange_id = ?
			AND credential_pending_token != ''`, id, exchangeID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) RecordServerConnected(ctx context.Context, id int64, reconnect bool) error {
	if reconnect {
		_, err := s.db.ExecContext(ctx, `UPDATE servers SET
			last_connected_at = CURRENT_TIMESTAMP,
			last_reconnected_at = CURRENT_TIMESTAMP,
			reconnect_count = reconnect_count + 1 WHERE id = ?`, id)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET last_connected_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

func (s *Store) RecordServerDisconnected(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET
		last_disconnected_at = CURRENT_TIMESTAMP, last_disconnect_reason = ? WHERE id = ?`, reason, id)
	return err
}

// ServerByID 按 id 查找服务器。
func (s *Store) ServerByID(ctx context.Context, id int64) (*Server, error) {
	srv, err := scanServer(s.db.QueryRowContext(ctx,
		`SELECT `+serverCols+` FROM servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query server by id: %w", err)
	}
	return srv, nil
}

// ListServers 列出全部服务器（按 id 升序）。
func (s *Store) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverCols+` FROM servers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan server: %w", err)
		}
		out = append(out, *srv)
	}
	return out, rows.Err()
}

// RotateServerToken 重写服务器 token：session.open 成功后 bootstrap token 换发为长期凭证（§11）。
func (s *Store) RotateServerToken(ctx context.Context, id int64, newToken string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET token = ?, credential_committed = 0,
			credential_pending_token = '', credential_exchange_id = '' WHERE id = ?`, newToken, id)
	return err
}

// UpdateServerAddress 由管理员修改服务器公网地址（§4"地址变更由管理员修改"）；
// 非空切换为手工模式，置空切换为自动模式并在下次 session.open 重新学习
// （NAT 类型禁止置空，由 panel 校验）。
func (s *Store) UpdateServerAddress(ctx context.Context, id int64, address string) error {
	mode := AddressModeManual
	if address == "" {
		mode = AddressModeAuto
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET address = ?, address_mode = ? WHERE id = ?`, address, mode, id)
	return err
}

// UpdateServerAlias modifies the administrator-facing server name.
func (s *Store) UpdateServerAlias(ctx context.Context, id int64, alias string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET alias = ? WHERE id = ?`, alias, id)
	return err
}

// UpdateServerAllowedPorts 修改 NAT 可用端口段（§21，JSON 串；收窄校验由 panel 负责）。
func (s *Store) UpdateServerAllowedPorts(ctx context.Context, id int64, allowedPorts string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET allowed_ports = ? WHERE id = ?`, allowedPorts, id)
	return err
}

// UpdateServerTags 整体替换服务器管理标签（JSON 字符串数组）。
func (s *Store) UpdateServerTags(ctx context.Context, id int64, tags string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE servers SET tags = ? WHERE id = ?`, tags, id)
	return err
}

// UpdateServerGeography 修改名称模板使用的国家与位置元数据。
func (s *Store) UpdateServerGeography(ctx context.Context, id int64, countryCode, location string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET country_code = ?, location = ? WHERE id = ?`, countryCode, location, id)
	return err
}

// TouchServer 更新 last_seen_at、xray 版本与 agent 版本（session.open 携带，§13）及公网地址（§9）：
// address 为生效公网地址（管理员已指定时与库中一致），learnedAddr 为本次拨入学习的地址，
// nicAddrs 为 agent 上报的网卡非回环地址 JSON 数组（空串 = 本次未上报，保留旧值）。
func (s *Store) TouchServer(ctx context.Context, id int64, xrayVersion, agentVersion, address, learnedAddr, nicAddrs string) error {
	if nicAddrs == "" {
		_, err := s.db.ExecContext(ctx,
			`UPDATE servers SET last_seen_at = CURRENT_TIMESTAMP, xray_version = ?, agent_version = ?, address = ?, learned_addr = ? WHERE id = ?`,
			xrayVersion, agentVersion, address, learnedAddr, id)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET last_seen_at = CURRENT_TIMESTAMP, xray_version = ?, agent_version = ?, address = ?, learned_addr = ?, nic_addresses = ? WHERE id = ?`,
		xrayVersion, agentVersion, address, learnedAddr, nicAddrs, id)
	return err
}

// UpdateServerVersion 仅更新 xray 版本（telemetry 周期携带，升级后据此刷新展示，§13）。
func (s *Store) UpdateServerVersion(ctx context.Context, id int64, xrayVersion string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET xray_version = ? WHERE id = ?`, xrayVersion, id)
	return err
}

// SetServerDrift 设置配置漂移标志（§17，agent drift_report 驱动）。
func (s *Store) SetServerDrift(ctx context.Context, id int64, drifted bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET config_drift = ? WHERE id = ?`, drifted, id)
	return err
}

// ResetServerBootstrap 换发 bootstrap token 并将服务器重置回 bootstrap 状态
// （last_seen_at 置空，下次 session.open 重新换发长期凭证，§5/§11）。
func (s *Store) ResetServerBootstrap(ctx context.Context, id int64, newBootstrapToken string) error {
	credential, err := shared.ParseCredential(newBootstrapToken)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE servers SET token = ?, credential_epoch = ?, last_seen_at = NULL,
			credential_committed = 0, credential_pending_token = '', credential_exchange_id = '' WHERE id = ?`,
		newBootstrapToken, credential.Epoch, id)
	return err
}

// ReportAgentSettings records the last revision the Agent says it has applied.
// Synchronization status is derived by comparing this value to global desired.
func (s *Store) ReportAgentSettings(ctx context.Context, id, revision int64, applyError string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers
		 SET agent_settings_revision = ?, agent_settings_error = ?, agent_settings_reported_at = CURRENT_TIMESTAMP
		 WHERE id = ?`, revision, applyError, id)
	return err
}

// ServerCustomSettings 读取服务器覆盖；无覆盖返回 (nil, nil)。
func (s *Store) ServerCustomSettings(ctx context.Context, id int64) (*serverSettingsValue, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT custom_settings FROM servers WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get server custom settings: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var value serverSettingsValue
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("decode server custom settings: %w", err)
	}
	return &value, nil
}

// UpdateServerCustomSettings 写入服务器覆盖；settings 为 nil 时清除覆盖。
// 每次写入 revision+1，保证 effective revision 单调递增（清除也递增语义由 EffectiveServerSettings 处理）。
func (s *Store) UpdateServerCustomSettings(ctx context.Context, id int64, settings *shared.ServerSettings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT custom_settings FROM servers WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get server custom settings: %w", err)
	}
	revision := int64(0)
	if strings.TrimSpace(raw) != "" {
		var current serverSettingsValue
		if err := json.Unmarshal([]byte(raw), &current); err != nil {
			return fmt.Errorf("decode server custom settings: %w", err)
		}
		revision = current.Revision
	}
	if settings == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE servers SET custom_settings = '' WHERE id = ?`, id); err != nil {
			return fmt.Errorf("clear server custom settings: %w", err)
		}
		return tx.Commit()
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	value := serverSettingsValue{Revision: revision + 1, XrayVersion: settings.XrayVersion}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE servers SET custom_settings = ? WHERE id = ?`, string(encoded), id); err != nil {
		return fmt.Errorf("save server custom settings: %w", err)
	}
	return tx.Commit()
}

// EffectiveServerSettings 返回服务器生效设置 = 面板默认 + 字段级覆盖；
// effective revision = default.revision + custom.revision（单调递增）。
func (s *Store) EffectiveServerSettings(ctx context.Context, id int64) (shared.ServerSettings, int64, error) {
	settings, revision, err := s.DefaultServerSettings(ctx)
	if err != nil {
		return shared.ServerSettings{}, 0, err
	}
	custom, err := s.ServerCustomSettings(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return shared.ServerSettings{}, 0, err
	}
	if err != nil {
		return shared.ServerSettings{}, 0, err
	}
	if custom != nil {
		if custom.XrayVersion != nil {
			settings.XrayVersion = custom.XrayVersion
		}
		revision += custom.Revision
	}
	return settings, revision, nil
}

// ReportServerSettings records the last effective revision the Agent applied.
func (s *Store) ReportServerSettings(ctx context.Context, id, revision int64, applyError string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers
		 SET server_settings_revision = ?, server_settings_error = ?, server_settings_reported_at = CURRENT_TIMESTAMP
		 WHERE id = ?`, revision, applyError, id)
	return err
}

// DeleteServerCascade 删除服务器及其节点与命令记录（§10 删除服务器）。
func (s *Store) DeleteServerCascade(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM user_nodes WHERE node_id IN (SELECT id FROM nodes WHERE server_id = ?
			AND id NOT IN (SELECT service_node_id FROM chains WHERE deleted_at IS NULL))`,
		`DELETE FROM server_test_result_chunks WHERE server_id = ?`,
		`DELETE FROM server_test_tasks WHERE server_id = ?`,
		`DELETE FROM commands WHERE server_id = ?`,
		`DELETE FROM nodes WHERE server_id = ?
			AND id NOT IN (SELECT service_node_id FROM chains WHERE deleted_at IS NULL)`,
		// 受影响链已先标记 invalid；这里只清除残余工作引用，revision 快照保留历史。
		`DELETE FROM chain_hops WHERE server_id = ?`,
		`DELETE FROM traffic_cursors WHERE server_id = ?`,
		`DELETE FROM server_metric_history WHERE server_id = ?`,
		`DELETE FROM server_metrics WHERE server_id = ?`,
		`DELETE FROM server_network_usage_daily WHERE server_id = ?`,
		`DELETE FROM server_traffic_plans WHERE server_id = ?`,
		`DELETE FROM server_billing WHERE server_id = ?`,
		`DELETE FROM servers WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete server cascade: %w", err)
		}
	}
	return tx.Commit()
}
