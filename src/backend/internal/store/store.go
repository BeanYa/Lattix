// Package store 提供 SQLite 存储（设计文档 §4）。
package store

import (
	"context"
	"database/sql"
	"fmt"
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
    learned_addr TEXT    NOT NULL DEFAULT '',
    nic_addresses TEXT   NOT NULL DEFAULT '', -- JSON 字符串数组
    machine_type TEXT    NOT NULL DEFAULT 'direct', -- direct|nat
    allowed_ports TEXT   NOT NULL DEFAULT '', -- JSON NAT 端口映射范围
    tags         TEXT    NOT NULL DEFAULT '', -- JSON 字符串数组；名称模板 {{TAG_n}} 的来源
    country_code TEXT    NOT NULL DEFAULT '', -- ISO 3166-1 alpha-2；名称模板国家/国旗来源
    location     TEXT    NOT NULL DEFAULT '', -- 管理员填写的城市/机房位置
    credential_epoch INTEGER NOT NULL DEFAULT 1,
    agent_settings_revision INTEGER NOT NULL DEFAULT 0,
    agent_settings_error TEXT NOT NULL DEFAULT '',
    agent_settings_reported_at DATETIME,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    uuid       TEXT    NOT NULL UNIQUE, -- 同一用户跨所有服务器使用同一 UUID（§7）
    sub_token  TEXT    NOT NULL UNIQUE,
    expires_at INTEGER,
    expired    INTEGER NOT NULL DEFAULT 0,
    disabled   INTEGER NOT NULL DEFAULT 0,
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
CREATE TABLE IF NOT EXISTS traffic (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id    INTEGER NOT NULL DEFAULT 0,
    user_uuid  TEXT    NOT NULL DEFAULT '',
    up         INTEGER NOT NULL DEFAULT 0,
    down       INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (node_id, user_uuid)
);

CREATE TABLE IF NOT EXISTS chains (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL DEFAULT '',
    status     TEXT    NOT NULL DEFAULT 'pending',
    error      TEXT    NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
	if _, err := db.Exec(Schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close 关闭底层数据库连接。
func (s *Store) Close() error { return s.db.Close() }

// Backup 经 VACUUM INTO 导出一致性快照到 destPath（§19 备份下载）。
// destPath 须不存在或为空文件；单连接 + busy_timeout 下与并发读写安全共存。
func (s *Store) Backup(ctx context.Context, destPath string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, destPath); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	return nil
}
