// Package store 提供 SQLite 存储（设计文档 §4）。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// Schema 数据模型（§4）。commands 表同时充当离线命令队列与操作日志；
// nodes.config_template 是面板侧虚拟配置，nodes.realized_config 是 Agent 上报的实际生效值。
const Schema = `
CREATE TABLE IF NOT EXISTS servers (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    alias        TEXT    NOT NULL,
    token        TEXT    NOT NULL UNIQUE, -- 长期凭证
    last_seen_at DATETIME,
    xray_version TEXT,
    config_drift INTEGER NOT NULL DEFAULT 0,
    agent_version TEXT,
    address      TEXT    NOT NULL DEFAULT '', -- 公网地址：管理员填写，留空按 agent 拨入 RemoteAddr 学习（§4/§9）
    address_mode TEXT    NOT NULL DEFAULT 'auto', -- auto|manual；自动地址可在每次 session.open 时重新学习
    learned_addr TEXT    NOT NULL DEFAULT '',
    nic_addresses TEXT   NOT NULL DEFAULT '', -- JSON 字符串数组
    machine_type TEXT    NOT NULL DEFAULT 'direct', -- direct|nat
    allowed_ports TEXT   NOT NULL DEFAULT '', -- JSON NAT 端口映射范围
    tags         TEXT    NOT NULL DEFAULT '', -- JSON 字符串数组；名称模板 {{TAG_n}} 的来源
    country_code TEXT    NOT NULL DEFAULT '', -- ISO 3166-1 alpha-2；名称模板国家/国旗来源
    location     TEXT    NOT NULL DEFAULT '', -- 管理员填写的城市/机房位置
    credential_epoch INTEGER NOT NULL DEFAULT 1,
    credential_committed INTEGER NOT NULL DEFAULT 0,
    credential_pending_token TEXT NOT NULL DEFAULT '',
    credential_exchange_id TEXT NOT NULL DEFAULT '',
    last_connected_at DATETIME,
    last_disconnected_at DATETIME,
    last_reconnected_at DATETIME,
    reconnect_count INTEGER NOT NULL DEFAULT 0,
    last_disconnect_reason TEXT NOT NULL DEFAULT '',
    agent_settings_revision INTEGER NOT NULL DEFAULT 0,
    agent_settings_error TEXT NOT NULL DEFAULT '',
    agent_settings_reported_at DATETIME,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS providers (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL COLLATE NOCASE UNIQUE,
    website_url TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS server_billing (
    server_id                 INTEGER PRIMARY KEY REFERENCES servers(id),
    enabled                   INTEGER NOT NULL DEFAULT 0,
    provider_id               INTEGER REFERENCES providers(id),
    amount_minor              INTEGER NOT NULL DEFAULT 0,
    currency                  TEXT NOT NULL DEFAULT 'CNY',
    service_started_on        TEXT NOT NULL DEFAULT '',
    interval_count            INTEGER NOT NULL DEFAULT 1,
    interval_unit             TEXT NOT NULL DEFAULT 'month',
    next_renewal_on           TEXT NOT NULL DEFAULT '',
    status                    TEXT NOT NULL DEFAULT 'disabled',
    assumed_valid_through     TEXT NOT NULL DEFAULT '',
    last_inspected_on         TEXT NOT NULL DEFAULT '',
    status_changed_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS server_traffic_plans (
    server_id          INTEGER PRIMARY KEY REFERENCES servers(id),
    quota_bytes        INTEGER,
    accounting_mode    TEXT NOT NULL DEFAULT 'outbound',
    reset_anchor_on    TEXT NOT NULL,
    reset_count        INTEGER NOT NULL DEFAULT 1,
    reset_unit         TEXT NOT NULL DEFAULT 'month',
    tracking_started_on TEXT NOT NULL,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS server_network_usage_daily (
    server_id   INTEGER NOT NULL REFERENCES servers(id),
    usage_date  TEXT NOT NULL,
    tx_bytes    INTEGER NOT NULL DEFAULT 0,
    rx_bytes    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, usage_date)
);

CREATE TABLE IF NOT EXISTS exchange_rates (
    base_currency  TEXT NOT NULL,
    quote_currency TEXT NOT NULL,
    rate           TEXT NOT NULL,
    rate_date      TEXT NOT NULL,
    source         TEXT NOT NULL,
    fetched_at     DATETIME NOT NULL,
    PRIMARY KEY (base_currency, quote_currency)
);

CREATE TABLE IF NOT EXISTS custom_exchange_rates (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    source_currency TEXT NOT NULL,
    source_amount   TEXT NOT NULL,
    target_currency TEXT NOT NULL,
    target_amount   TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 0,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (source_currency, target_currency)
);

CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    uuid       TEXT    NOT NULL UNIQUE, -- 同一用户跨所有服务器使用同一 UUID（§7）
    sub_token  TEXT    NOT NULL UNIQUE,
    expires_at INTEGER,
    expired    INTEGER NOT NULL DEFAULT 0,
    disabled   INTEGER NOT NULL DEFAULT 0,
    traffic_limit     INTEGER NOT NULL DEFAULT 0, -- 流量配额（字节），0=不限
    traffic_reset_day INTEGER NOT NULL DEFAULT 0, -- 每月重置日（day-of-month，0=创建日，max 28）
    sub_title         TEXT    NOT NULL DEFAULT '', -- 订阅落地页标题覆盖（空=用全局）
    sub_announcement  TEXT    NOT NULL DEFAULT '', -- 订阅公告覆盖（Markdown，空=用全局）
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS nodes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL DEFAULT '',
    server_id       INTEGER NOT NULL REFERENCES servers(id),
    protocol        TEXT    NOT NULL DEFAULT 'vless',
    port            INTEGER, -- NULL = Agent 自动挑选空闲端口（§7）
    config_template TEXT    NOT NULL, -- JSON，面板侧虚拟配置
    realized_config TEXT,             -- JSON，Agent 上报的实际生效值
    status          TEXT    NOT NULL DEFAULT 'pending', -- pending → applying → active | failed（§6）
    error           TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS commands (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id  TEXT    NOT NULL UNIQUE,
    trace_id    TEXT    NOT NULL,
    server_id   INTEGER NOT NULL REFERENCES servers(id),
    type        TEXT    NOT NULL,
    data        TEXT    NOT NULL, -- JSON
    status      TEXT    NOT NULL DEFAULT 'queued', -- queued/sent/acked/failed
    error       TEXT    NOT NULL DEFAULT '',
    attempts    INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS rpc_idempotency (
    operator      TEXT NOT NULL,
    route         TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash  TEXT NOT NULL,
    response_json TEXT NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (operator, route, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_rpc_idempotency_created_at ON rpc_idempotency(created_at);

-- 用户-节点关联（§16 逐节点用户分配，默认全关）：无关联即无访问权。
CREATE TABLE IF NOT EXISTS user_nodes (
    user_id INTEGER NOT NULL REFERENCES users(id),
    node_id INTEGER NOT NULL REFERENCES nodes(id),
    PRIMARY KEY (user_id, node_id)
);

-- 主机遥测（§13）：每服务器一行，最新值。
CREATE TABLE IF NOT EXISTS server_metrics (
    server_id          INTEGER PRIMARY KEY REFERENCES servers(id),
    load1              REAL    NOT NULL DEFAULT 0,
    load5              REAL    NOT NULL DEFAULT 0,
    load15             REAL    NOT NULL DEFAULT 0,
    cpu_percent        REAL,
    mem_total          INTEGER NOT NULL DEFAULT 0,
    mem_used           INTEGER NOT NULL DEFAULT 0,
    disk_total         INTEGER NOT NULL DEFAULT 0,
    disk_used          INTEGER NOT NULL DEFAULT 0,
    network_interface  TEXT    NOT NULL DEFAULT '',
    network_tx_bytes   INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes   INTEGER NOT NULL DEFAULT 0,
    network_tx_bps     REAL,
    network_rx_bps     REAL,
    uptime_seconds     INTEGER NOT NULL DEFAULT 0,
    latency_ms         REAL,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS server_metric_history (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id          INTEGER NOT NULL REFERENCES servers(id),
    load1              REAL    NOT NULL DEFAULT 0,
    load5              REAL    NOT NULL DEFAULT 0,
    load15             REAL    NOT NULL DEFAULT 0,
    cpu_percent        REAL,
    mem_total          INTEGER NOT NULL DEFAULT 0,
    mem_used           INTEGER NOT NULL DEFAULT 0,
    disk_total         INTEGER NOT NULL DEFAULT 0,
    disk_used          INTEGER NOT NULL DEFAULT 0,
    network_interface  TEXT    NOT NULL DEFAULT '',
    network_tx_bytes   INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes   INTEGER NOT NULL DEFAULT 0,
    network_tx_bps     REAL,
    network_rx_bps     REAL,
    uptime_seconds     INTEGER NOT NULL DEFAULT 0,
    latency_ms         REAL,
    sampled_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_server_metric_history_server_sampled
    ON server_metric_history(server_id, sampled_at);

-- 面板设置（§10 设置页）：key/value，DB 中的值优先于启动参数。
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- 流量累计（§13 仅统计）：节点维度（user_uuid=''）与用户维度（node_id=0）。
-- period_start 标记当期起始（ISO8601），sweeper 周期重置时归档到 traffic_history 后清零。
CREATE TABLE IF NOT EXISTS traffic (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id      INTEGER NOT NULL DEFAULT 0,
    user_uuid    TEXT    NOT NULL DEFAULT '',
    up           INTEGER NOT NULL DEFAULT 0,
    down         INTEGER NOT NULL DEFAULT 0,
    period_start TEXT    NOT NULL DEFAULT '',
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (node_id, user_uuid)
);

-- 流量历史归档（审计用）：sweeper 重置时从 traffic 表归档。
CREATE TABLE IF NOT EXISTS traffic_history (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id      INTEGER NOT NULL,
    user_uuid    TEXT    NOT NULL,
    up           INTEGER NOT NULL DEFAULT 0,
    down         INTEGER NOT NULL DEFAULT 0,
    period_start TEXT    NOT NULL,
    period_end   TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_traffic_history_user
    ON traffic_history(user_uuid, period_start);

CREATE TABLE IF NOT EXISTS chains (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    name                     TEXT    NOT NULL DEFAULT '',
    service_node_id          INTEGER NOT NULL DEFAULT 0,
    published_revision_id    INTEGER NOT NULL DEFAULT 0,
    desired_revision_id      INTEGER NOT NULL DEFAULT 0,
    traffic_multiplier_milli INTEGER NOT NULL DEFAULT 1000,
    status                   TEXT    NOT NULL DEFAULT 'pending',
    error                    TEXT    NOT NULL DEFAULT '',
    deleted_at               DATETIME,
    created_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chain_hops (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id           INTEGER NOT NULL REFERENCES chains(id),
    seq                INTEGER NOT NULL,
    server_id          INTEGER NOT NULL REFERENCES servers(id),
    role               TEXT    NOT NULL,
    node_id            INTEGER NOT NULL DEFAULT 0,
    status             TEXT    NOT NULL DEFAULT 'pending',
    error              TEXT    NOT NULL DEFAULT '',
    forward_port       INTEGER NOT NULL DEFAULT 0,
    portal_port        INTEGER NOT NULL DEFAULT 0,
    portal_public_key  TEXT    NOT NULL DEFAULT '',
    portal_server_name TEXT    NOT NULL DEFAULT '',
    tunnel_uuid        TEXT    NOT NULL DEFAULT '',
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS traffic_cursors (
    server_id    INTEGER NOT NULL REFERENCES servers(id),
    counter_key  TEXT    NOT NULL,
    instance_id  TEXT    NOT NULL,
    up           INTEGER NOT NULL DEFAULT 0,
    down         INTEGER NOT NULL DEFAULT 0,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (server_id, counter_key)
);

CREATE TABLE IF NOT EXISTS chain_traffic_totals (
    chain_id        INTEGER NOT NULL REFERENCES chains(id),
    hop_id          INTEGER NOT NULL DEFAULT 0,
    raw_up          INTEGER NOT NULL DEFAULT 0,
    raw_down        INTEGER NOT NULL DEFAULT 0,
    effective_up    INTEGER NOT NULL DEFAULT 0,
    effective_down  INTEGER NOT NULL DEFAULT 0,
    remainder_up    INTEGER NOT NULL DEFAULT 0,
    remainder_down  INTEGER NOT NULL DEFAULT 0,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (chain_id, hop_id)
);

CREATE TABLE IF NOT EXISTS chain_traffic_baselines (
    chain_id       INTEGER NOT NULL REFERENCES chains(id),
    hop_id         INTEGER NOT NULL DEFAULT 0,
    raw_up         INTEGER NOT NULL DEFAULT 0,
    raw_down       INTEGER NOT NULL DEFAULT 0,
    effective_up   INTEGER NOT NULL DEFAULT 0,
    effective_down INTEGER NOT NULL DEFAULT 0,
    reset_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (chain_id, hop_id)
);

CREATE TABLE IF NOT EXISTS chain_traffic_daily (
    chain_id       INTEGER NOT NULL REFERENCES chains(id),
    hop_id         INTEGER NOT NULL DEFAULT 0,
    revision_id    INTEGER NOT NULL DEFAULT 0,
    usage_date     TEXT    NOT NULL,
    timezone       TEXT    NOT NULL,
    raw_up         INTEGER NOT NULL DEFAULT 0,
    raw_down       INTEGER NOT NULL DEFAULT 0,
    effective_up   INTEGER NOT NULL DEFAULT 0,
    effective_down INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (chain_id, hop_id, revision_id, usage_date, timezone)
);

CREATE TABLE IF NOT EXISTS chain_multiplier_events (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id         INTEGER NOT NULL REFERENCES chains(id),
    multiplier_milli INTEGER NOT NULL,
    effective_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chain_revisions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id      INTEGER NOT NULL REFERENCES chains(id),
    revision_no   INTEGER NOT NULL,
    status        TEXT    NOT NULL DEFAULT 'applying',
    forced        INTEGER NOT NULL DEFAULT 0,
    snapshot      TEXT    NOT NULL,
    error         TEXT    NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at  DATETIME,
    UNIQUE (chain_id, revision_no)
);

CREATE TABLE IF NOT EXISTS chain_revision_tasks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id INTEGER NOT NULL REFERENCES chain_revisions(id),
    task_key    TEXT    NOT NULL,
    phase       TEXT    NOT NULL,
    action      TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    hop_id      INTEGER NOT NULL DEFAULT 0,
    server_id   INTEGER NOT NULL DEFAULT 0,
    status      TEXT    NOT NULL DEFAULT 'pending',
    command_id  INTEGER NOT NULL DEFAULT 0,
    error       TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (revision_id, task_key)
);

CREATE TABLE IF NOT EXISTS chain_hop_identities (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id    INTEGER NOT NULL REFERENCES chains(id),
    server_id   INTEGER NOT NULL,
    archived_at DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

`

// Store 封装 SQLite 数据访问。
type Store struct {
	db *sql.DB
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Open 打开 SQLite 数据库并确保表结构存在。
func Open(path string) (*Store, error) {
	if err := secureDatabaseFile(path); err != nil {
		return nil, err
	}
	// 并发设置（§2）：单连接 + busy_timeout，避免 HTTP handler/遥测/Flush 并发写时偶发 database is locked。
	dsn := path
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := initializeSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("init/migrate schema: %w", err)
	}
	return &Store{db: db}, nil
}

// secureDatabaseFile ensures credentials and private keys stored in SQLite are
// never left world-readable. In-memory DSNs do not have a backing file.
func secureDatabaseFile(dsn string) error {
	path, ok, err := sqliteFilePath(dsn)
	if err != nil || !ok {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		f, createErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return fmt.Errorf("create sqlite database: %w", createErr)
		}
		if closeErr := f.Close(); closeErr != nil {
			return fmt.Errorf("close new sqlite database: %w", closeErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect sqlite database: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("sqlite database path must be a regular file: %s", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure sqlite database permissions: %w", err)
	}
	return nil
}

func sqliteFilePath(dsn string) (string, bool, error) {
	base, query, _ := strings.Cut(dsn, "?")
	if base == ":memory:" || base == "file::memory:" {
		return "", false, nil
	}
	if strings.HasPrefix(base, "file:") {
		values, err := url.ParseQuery(query)
		if err != nil {
			return "", false, fmt.Errorf("parse sqlite DSN: %w", err)
		}
		if values.Get("mode") == "memory" {
			return "", false, nil
		}
		base, err = url.PathUnescape(strings.TrimPrefix(base, "file:"))
		if err != nil {
			return "", false, fmt.Errorf("parse sqlite file path: %w", err)
		}
	}
	if base == "" {
		return "", false, errors.New("sqlite database path is empty")
	}
	return base, true, nil
}

// Close 关闭底层数据库连接。
func (s *Store) Close() error { return s.db.Close() }

// Backup 经 VACUUM INTO 导出一致性快照到 destPath（§19 备份下载）。
// destPath 须不存在或为空文件；单连接 + busy_timeout 下与并发读写安全共存。
func (s *Store) Backup(ctx context.Context, destPath string) error {
	created, err := prepareBackupFile(destPath)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, destPath); err != nil {
		if created {
			_ = os.Remove(destPath)
		}
		return fmt.Errorf("vacuum into: %w", err)
	}
	if err := os.Chmod(destPath, 0o600); err != nil {
		return fmt.Errorf("secure backup permissions: %w", err)
	}
	return nil
}

func prepareBackupFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		f, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return false, fmt.Errorf("create backup file: %w", createErr)
		}
		if closeErr := f.Close(); closeErr != nil {
			_ = os.Remove(path)
			return false, fmt.Errorf("close new backup file: %w", closeErr)
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect backup file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("backup path must be a regular file: %s", path)
	}
	if info.Size() != 0 {
		return false, fmt.Errorf("backup file must be empty: %s", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return false, fmt.Errorf("secure backup permissions: %w", err)
	}
	return false, nil
}
