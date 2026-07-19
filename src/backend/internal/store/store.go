// Package store 提供 SQLite 存储（设计文档 §4）。
package store

import (
	"database/sql"
	"fmt"

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
`

// Store 封装 SQLite 数据访问。
type Store struct {
	db *sql.DB
}

// Open 打开 SQLite 数据库并确保表结构存在。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(Schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close 关闭底层数据库连接。
func (s *Store) Close() error { return s.db.Close() }
