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
    address      TEXT    NOT NULL DEFAULT '', -- 默认公网地址：管理员填写，留空按 agent 拨入 RemoteAddr 学习（§4/§9）
    addresses    TEXT    NOT NULL DEFAULT '', -- 公网地址列表 JSON 字符串数组（含默认地址；访问流学习 + NIC 上报合并，§9）
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
    custom_settings TEXT    NOT NULL DEFAULT '', -- 服务器级覆盖（JSON：{revision, xray_version, ...}，字段级覆盖面板默认）
    server_settings_revision INTEGER NOT NULL DEFAULT 0, -- agent 上报的已应用 effective revision
    server_settings_error TEXT   NOT NULL DEFAULT '',    -- agent 应用错误信息（透出给列表状态）
    server_settings_reported_at DATETIME,                -- 最近一次上报时间
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
    traffic_reset_day INTEGER NOT NULL DEFAULT 0, -- 每月重置日（day-of-month，0=创建日，1-31）
    sub_title         TEXT    NOT NULL DEFAULT '', -- 订阅落地页标题覆盖（空=用全局）
    sub_announcement  TEXT    NOT NULL DEFAULT '', -- 订阅公告覆盖（Markdown，空=用全局）
    plan_name         TEXT    NOT NULL DEFAULT '', -- 套餐名（subscription-userinfo plan_name，空=用全局）
    app_url           TEXT    NOT NULL DEFAULT '', -- 客户端跳转链接（subscription-userinfo app_url，空=用全局）
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

-- 每台服务器仅保留最新一次原子测试任务与最终报告。进度是易失的，
-- 由 Panel 进程内存维护；generation 决定迟到结果是否可覆盖。
CREATE TABLE IF NOT EXISTS server_test_tasks (
    server_id       INTEGER PRIMARY KEY REFERENCES servers(id),
    task_id         TEXT    NOT NULL UNIQUE,
    generation      INTEGER NOT NULL,
    request_id      TEXT    NOT NULL UNIQUE,
    status          TEXT    NOT NULL DEFAULT 'queued',
    categories      TEXT    NOT NULL, -- JSON string array
    catalog_version TEXT    NOT NULL,
    catalog_hashes  TEXT    NOT NULL, -- JSON object
    result_json     TEXT,
    result_sha256   TEXT    NOT NULL DEFAULT '',
    error_code      TEXT    NOT NULL DEFAULT '',
    error_message   TEXT    NOT NULL DEFAULT '',
    agent_version   TEXT    NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    accepted_at     DATETIME,
    started_at      DATETIME,
    completed_at    DATETIME,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS server_test_result_chunks (
    server_id   INTEGER NOT NULL REFERENCES servers(id),
    task_id     TEXT    NOT NULL,
    generation  INTEGER NOT NULL,
    chunk_index INTEGER NOT NULL,
    chunk_count INTEGER NOT NULL,
    manifest    TEXT,
    data        BLOB NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (server_id, task_id, generation, chunk_index)
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

-- Server-level managed listener. Chains with a compatible profile share one
-- row and one public port; port=0 means the Agent has not realized it yet.
CREATE TABLE IF NOT EXISTS shared_endpoints (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id       INTEGER NOT NULL REFERENCES servers(id),
    protocol        TEXT    NOT NULL DEFAULT 'vless',
    port            INTEGER NOT NULL DEFAULT 0,
    profile_hash    TEXT    NOT NULL,
    config_template TEXT    NOT NULL,
    realized_config TEXT,
    status          TEXT    NOT NULL DEFAULT 'pending',
    error           TEXT    NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_shared_endpoints_server_port
    ON shared_endpoints(server_id, port);
CREATE INDEX IF NOT EXISTS idx_shared_endpoints_server_profile
    ON shared_endpoints(server_id, profile_hash);

-- A real user can consume many chains concurrently. access_uuid is the
-- assignment credential installed on the shared entry endpoint.
CREATE TABLE IF NOT EXISTS user_chain_assignments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    chain_id    INTEGER NOT NULL REFERENCES chains(id),
    access_uuid TEXT    NOT NULL UNIQUE,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id, chain_id)
);

-- Subscription templates are cached independently from published user files.
-- Refreshing a GitHub source updates this table only; user snapshots remain immutable.
CREATE TABLE IF NOT EXISTS subscription_templates (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL, -- portable|acl4ssr|mihomo|singbox|quanx
    origin          TEXT NOT NULL, -- local|github
    source_url      TEXT NOT NULL DEFAULT '',
    content         TEXT NOT NULL,
    content_sha256  TEXT NOT NULL,
    license         TEXT NOT NULL DEFAULT '',
    readonly        INTEGER NOT NULL DEFAULT 0,
    fetched_at      DATETIME,
    last_attempt_at DATETIME,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Remote rule sources referenced by a template are refreshed and committed with
-- the template cache. A failed refresh leaves the last complete cache usable.
CREATE TABLE IF NOT EXISTS subscription_template_rules (
    template_id     TEXT NOT NULL REFERENCES subscription_templates(id),
    template_sha256 TEXT NOT NULL,
    name            TEXT NOT NULL,
    source_url      TEXT NOT NULL,
    content         BLOB NOT NULL,
    content_sha256  TEXT NOT NULL,
    PRIMARY KEY (template_id, name)
);

CREATE TABLE IF NOT EXISTS user_subscription_profiles (
    user_id             INTEGER PRIMARY KEY REFERENCES users(id),
    mode                TEXT NOT NULL DEFAULT 'suggested', -- suggested|template
    preset              TEXT NOT NULL DEFAULT 'balanced',
    categories          TEXT NOT NULL DEFAULT '[]', -- JSON string array
    portable_template_id TEXT NOT NULL DEFAULT '',
    mihomo_template_id  TEXT NOT NULL DEFAULT '',
    singbox_template_id TEXT NOT NULL DEFAULT '',
    quanx_template_id   TEXT NOT NULL DEFAULT '',
    assigned_portable_template_id TEXT NOT NULL DEFAULT '',
    assigned_mihomo_template_id  TEXT NOT NULL DEFAULT '',
    assigned_singbox_template_id TEXT NOT NULL DEFAULT '',
    assigned_quanx_template_id   TEXT NOT NULL DEFAULT '',
    assigned_suggested_preset    TEXT NOT NULL DEFAULT '', -- 已废弃，保留兼容；新指派使用 assigned_suggested_categories
    assigned_suggested_categories TEXT NOT NULL DEFAULT '', -- 建议规则分组指派（JSON 数组，与模板指派互斥）
    assign_forced_portable INTEGER NOT NULL DEFAULT 0,
    assign_forced_mihomo  INTEGER NOT NULL DEFAULT 0,
    assign_forced_singbox INTEGER NOT NULL DEFAULT 0,
    assign_forced_quanx   INTEGER NOT NULL DEFAULT 0,
    generation_status   TEXT NOT NULL DEFAULT 'missing', -- missing|pending|ready|error
    generation_error    TEXT NOT NULL DEFAULT '',
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Every generation creates a complete immutable set of files. A single pointer
-- switch publishes all formats atomically.
CREATE TABLE IF NOT EXISTS subscription_snapshots (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER NOT NULL REFERENCES users(id),
    revision      INTEGER NOT NULL,
    source_label  TEXT NOT NULL,
    source_sha256 TEXT NOT NULL,
    warnings      TEXT NOT NULL DEFAULT '',
    generated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id, revision)
);

CREATE TABLE IF NOT EXISTS subscription_files (
    snapshot_id  INTEGER NOT NULL REFERENCES subscription_snapshots(id),
    format       TEXT NOT NULL,
    content_type TEXT NOT NULL,
    content      BLOB NOT NULL,
    PRIMARY KEY (snapshot_id, format)
);

-- Client-native rule artifacts are immutable members of the same snapshot as
-- the generated subscription files.
CREATE TABLE IF NOT EXISTS subscription_rule_files (
    snapshot_id  INTEGER NOT NULL REFERENCES subscription_snapshots(id),
    name         TEXT NOT NULL,
    format       TEXT NOT NULL, -- mihomo|singbox|quanx
    source_sha256 TEXT NOT NULL,
    content_type TEXT NOT NULL,
    content      BLOB NOT NULL,
    PRIMARY KEY (snapshot_id, name, format)
);

CREATE TABLE IF NOT EXISTS published_subscription_snapshots (
    user_id     INTEGER PRIMARY KEY REFERENCES users(id),
    snapshot_id INTEGER NOT NULL REFERENCES subscription_snapshots(id),
    published_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_subscription_snapshots_user
    ON subscription_snapshots(user_id, revision DESC);

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
    latency_probe_active INTEGER NOT NULL DEFAULT 1,
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
    endpoint_id              INTEGER NOT NULL DEFAULT 0,
    service_uuid             TEXT    NOT NULL DEFAULT '',
    published_revision_id    INTEGER NOT NULL DEFAULT 0,
    desired_revision_id      INTEGER NOT NULL DEFAULT 0,
    traffic_multiplier_milli INTEGER NOT NULL DEFAULT 1000,
    status                   TEXT    NOT NULL DEFAULT 'pending',
    error                    TEXT    NOT NULL DEFAULT '',
    deleted_at               DATETIME,
    created_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS endpoint_traffic_totals (
    endpoint_id INTEGER PRIMARY KEY REFERENCES shared_endpoints(id),
    up          INTEGER NOT NULL DEFAULT 0,
    down        INTEGER NOT NULL DEFAULT 0,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
    address            TEXT    NOT NULL DEFAULT '', -- 本跳所选公网地址（空 = 跟随服务器默认地址；引用语义，消费时实时校验回退）
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

-- 外部订阅（第三方机场等导入；下一版本关联用户订阅）
CREATE TABLE IF NOT EXISTS external_subscriptions (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    name                  TEXT NOT NULL,
    url                   TEXT NOT NULL UNIQUE,
    user_agent            TEXT NOT NULL DEFAULT '',
    skip_cert_verify      INTEGER NOT NULL DEFAULT 0,
    auto_update           INTEGER NOT NULL DEFAULT 1,
    update_interval_hours INTEGER NOT NULL DEFAULT 24,
    format                TEXT NOT NULL DEFAULT '',
    node_count            INTEGER NOT NULL DEFAULT 0,
    upload                INTEGER NOT NULL DEFAULT 0,
    download              INTEGER NOT NULL DEFAULT 0,
    total                 INTEGER NOT NULL DEFAULT 0,
    expire                INTEGER,
    last_sync_at          DATETIME,
    last_attempt_at       DATETIME,
    last_error            TEXT NOT NULL DEFAULT '',
    created_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS external_chains (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES external_subscriptions(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    protocol        TEXT NOT NULL,
    server          TEXT NOT NULL DEFAULT '',
    port            INTEGER NOT NULL DEFAULT 0,
    config          TEXT NOT NULL,
    config_sha256   TEXT NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_external_chains_subscription
    ON external_chains(subscription_id);

-- 用户引入外部订阅（叠加 stack / 并入 merge / 附加 nodes）；删除订阅或用户时级联清理。
CREATE TABLE IF NOT EXISTS user_external_subscriptions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subscription_id INTEGER NOT NULL REFERENCES external_subscriptions(id) ON DELETE CASCADE,
    mode            TEXT NOT NULL DEFAULT 'stack',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, subscription_id)
);

CREATE TABLE IF NOT EXISTS link_groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS link_group_chains (
    group_id   INTEGER NOT NULL REFERENCES link_groups(id) ON DELETE CASCADE,
    chain_id   INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (group_id, chain_id)
);
CREATE TABLE IF NOT EXISTS link_group_external_subscriptions (
    group_id        INTEGER NOT NULL REFERENCES link_groups(id) ON DELETE CASCADE,
    subscription_id INTEGER NOT NULL REFERENCES external_subscriptions(id) ON DELETE CASCADE,
    mode            TEXT NOT NULL DEFAULT 'stack',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (group_id, subscription_id)
);
CREATE TABLE IF NOT EXISTS user_groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS user_group_members (
    user_group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id       INTEGER NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_group_id, user_id)
);
CREATE TABLE IF NOT EXISTS user_group_links (
    user_group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    link_group_id INTEGER NOT NULL REFERENCES link_groups(id) ON DELETE CASCADE,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_group_id, link_group_id)
);
CREATE INDEX IF NOT EXISTS idx_user_group_members_user
    ON user_group_members(user_id);

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
