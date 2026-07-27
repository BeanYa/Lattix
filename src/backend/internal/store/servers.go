package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
)

// Server 是 servers 表的一行（§4）。
type Server struct {
	ID                      int64
	Alias                   string
	Token                   string // 长期凭证；创建时先存 bootstrap token，hello 认证后换发（§11）
	LastSeenAt              *time.Time
	XrayVersion             string
	AgentVersion            string // hello 上报的 agent 版本（§18 升级管理）
	Address                 string // 公网地址（hello 时按 WS RemoteAddr 记录，订阅用，§9）
	LearnedAddr             string // 每次 hello 学习的拨入地址（受信回环代理时取 XFF 首 IP，§9），仅作候选不覆盖 Address
	NICAddresses            string // agent 上报的网卡非回环地址 JSON 数组（§9 公网地址候选）；空串 = 无
	ConfigDrift             bool   // 配置漂移标志（§17，agent drift_report 驱动）
	MachineType             string // direct|nat（§21）
	AllowedPorts            string // NAT 可用端口段 JSON（shared.PortRange 数组，§21）；空串 = 无段
	Tags                    string // 管理标签 JSON 数组；名称模板 {{TAG_n}} 的来源
	CountryCode             string // ISO 3166-1 alpha-2；管理员在服务器资料中选择
	Location                string // 城市或机房位置；管理员填写
	CredentialEpoch         int64
	AgentSettingsRevision   int64
	AgentSettingsError      string
	AgentSettingsReportedAt *time.Time
	CreatedAt               time.Time
}

// serverCols 是 Server 各字段对应的列清单。
const serverCols = `id, alias, token, last_seen_at, xray_version, agent_version, address, learned_addr, nic_addresses, config_drift, machine_type, allowed_ports, tags, country_code, location, credential_epoch, agent_settings_revision, agent_settings_error, agent_settings_reported_at, created_at`

func scanServer(row interface{ Scan(...any) error }) (*Server, error) {
	var srv Server
	var lastSeen, settingsReported sql.NullTime
	var xrayVer, agentVer sql.NullString
	err := row.Scan(&srv.ID, &srv.Alias, &srv.Token, &lastSeen, &xrayVer, &agentVer, &srv.Address, &srv.LearnedAddr, &srv.NICAddresses, &srv.ConfigDrift, &srv.MachineType, &srv.AllowedPorts, &srv.Tags, &srv.CountryCode, &srv.Location, &srv.CredentialEpoch, &srv.AgentSettingsRevision, &srv.AgentSettingsError, &settingsReported, &srv.CreatedAt)
	if err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		t := lastSeen.Time
		srv.LastSeenAt = &t
	}
	srv.XrayVersion = xrayVer.String
	srv.AgentVersion = agentVer.String
	if settingsReported.Valid {
		t := settingsReported.Time
		srv.AgentSettingsReportedAt = &t
	}
	return &srv, nil
}

// CreateServer 插入一台服务器，token 为一次性 bootstrap token（§11），返回服务器 id。
// address 为管理员指定的公网地址（§4）；空串表示留待 hello 时按 RemoteAddr 自动学习
// （NAT 类型强制必填，由 panel 校验）。machineType/allowedPorts 为 NAT 元数据（§21，面板侧，不下发 agent）。
func (s *Store) CreateServer(ctx context.Context, alias, address, bootstrapToken, machineType, allowedPorts, tags, countryCode, location string) (int64, error) {
	epoch := int64(1)
	if credential, err := shared.ParseCredential(bootstrapToken); err == nil {
		epoch = credential.Epoch
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO servers (alias, address, token, machine_type, allowed_ports, tags, country_code, location, credential_epoch) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		alias, address, bootstrapToken, machineType, allowedPorts, tags, countryCode, location, epoch)
	if err != nil {
		return 0, fmt.Errorf("insert server: %w", err)
	}
	return res.LastInsertId()
}

// ServerByToken 按 token（bootstrap 或长期）查找服务器。
func (s *Store) ServerByToken(ctx context.Context, token string) (*Server, error) {
	srv, err := scanServer(s.db.QueryRowContext(ctx,
		`SELECT `+serverCols+` FROM servers WHERE token = ?`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query server by token: %w", err)
	}
	return srv, nil
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

// RotateServerToken 重写服务器 token：hello 认证成功后 bootstrap token 换发为长期凭证（§11）。
func (s *Store) RotateServerToken(ctx context.Context, id int64, newToken string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET token = ? WHERE id = ?`, newToken, id)
	return err
}

// UpdateServerAddress 由管理员修改服务器公网地址（§4"地址变更由管理员修改"）；
// 置空则下次 hello 时按 RemoteAddr 重新自动学习（NAT 类型禁止置空，由 panel 校验）。
func (s *Store) UpdateServerAddress(ctx context.Context, id int64, address string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET address = ? WHERE id = ?`, address, id)
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

// TouchServer 更新 last_seen_at、xray 版本与 agent 版本（hello 携带，§13）及公网地址（§9）：
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
// （last_seen_at 置空，下次 hello 重新换发长期凭证，§5/§11）。
func (s *Store) ResetServerBootstrap(ctx context.Context, id int64, newBootstrapToken string) error {
	credential, err := shared.ParseCredential(newBootstrapToken)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE servers SET token = ?, credential_epoch = ?, last_seen_at = NULL WHERE id = ?`,
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

// DeleteServerCascade 删除服务器及其节点与命令记录（§10 删除服务器）。
func (s *Store) DeleteServerCascade(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM user_nodes WHERE node_id IN (SELECT id FROM nodes WHERE server_id = ?)`,
		`DELETE FROM commands WHERE server_id = ?`,
		`DELETE FROM nodes WHERE server_id = ?`,
		// 链（§21）：删除该服务器的跳；不再有任何跳的链一并删除。
		`DELETE FROM chain_hops WHERE server_id = ?`,
		`DELETE FROM chains WHERE id NOT IN (SELECT DISTINCT chain_id FROM chain_hops)`,
		`DELETE FROM servers WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete server cascade: %w", err)
		}
	}
	return tx.Commit()
}
