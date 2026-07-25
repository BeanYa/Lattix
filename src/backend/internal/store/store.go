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
    address      TEXT    NOT NULL DEFAULT '', -- 公网地址：管理员填写，留空按 agent 拨入 RemoteAddr 学习（§4/§9）
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    uuid       TEXT    NOT NULL UNIQUE, -- 同一用户跨所有服务器使用同一 UUID（§7）
    sub_token  TEXT    NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS nodes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
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
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id  INTEGER NOT NULL REFERENCES servers(id),
    type       TEXT    NOT NULL,
    payload    TEXT    NOT NULL, -- JSON
    status     TEXT    NOT NULL DEFAULT 'queued', -- queued/sent/acked/failed
    attempts   INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 用户-节点关联（§16 逐节点用户分配，默认全关）：无关联即无访问权。
CREATE TABLE IF NOT EXISTS user_nodes (
    user_id INTEGER NOT NULL REFERENCES users(id),
    node_id INTEGER NOT NULL REFERENCES nodes(id),
    PRIMARY KEY (user_id, node_id)
);

-- 主机遥测（§13）：每服务器一行，最新值。
CREATE TABLE IF NOT EXISTS server_metrics (
    server_id   INTEGER PRIMARY KEY REFERENCES servers(id),
    load1       REAL    NOT NULL DEFAULT 0,
    cpu_percent REAL    NOT NULL DEFAULT 0,
    mem_total   INTEGER NOT NULL DEFAULT 0,
    mem_used    INTEGER NOT NULL DEFAULT 0,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

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
`

// Store 封装 SQLite 数据访问。
type Store struct {
	db *sql.DB
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
	// 轻量迁移：servers.address（订阅需要服务器公网地址 §9，agent 拨入时按 RemoteAddr 记录）。
	if _, err := db.Exec(`ALTER TABLE servers ADD COLUMN address TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate servers.address: %w", err)
		}
	}
	// 轻量迁移：servers.config_drift（§17 配置漂移标志，agent 上报驱动）。
	if _, err := db.Exec(`ALTER TABLE servers ADD COLUMN config_drift INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate servers.config_drift: %w", err)
		}
	}
	// 轻量迁移：servers.agent_version / agent_upgrade_needed（§18 兼容窗口与升级管理）。
	if _, err := db.Exec(`ALTER TABLE servers ADD COLUMN agent_version TEXT`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate servers.agent_version: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE servers ADD COLUMN agent_upgrade_needed INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate servers.agent_upgrade_needed: %w", err)
		}
	}
	// 轻量迁移：users.expires_at / expired（§9 用户有效期：到期停权标记，unix 秒，NULL=长期）。
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN expires_at INTEGER`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate users.expires_at: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN expired INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate users.expired: %w", err)
		}
	}
	// 一次性迁移（PRAGMA user_version 0→1）：user_nodes 引入前，成员关系隐含为
	// "全部用户 ∈ 全部节点"（§8）；为不破坏存量订阅，迁移时补全关联。
	// 此后新建用户/节点默认全关（§16）。
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		db.Close()
		return nil, fmt.Errorf("read user_version: %w", err)
	}
	if version < 1 {
		if _, err := db.Exec(`INSERT OR IGNORE INTO user_nodes (user_id, node_id)
			SELECT u.id, n.id FROM users u CROSS JOIN nodes n`); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate user_nodes: %w", err)
		}
		if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
			db.Close()
			return nil, fmt.Errorf("bump user_version: %w", err)
		}
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
