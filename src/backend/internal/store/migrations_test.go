package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const legacySchema = `
CREATE TABLE servers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    alias TEXT NOT NULL,
    token TEXT NOT NULL UNIQUE,
    last_seen_at DATETIME,
    xray_version TEXT,
    config_drift INTEGER NOT NULL DEFAULT 0,
    agent_version TEXT,
    address TEXT NOT NULL DEFAULT '',
    learned_addr TEXT NOT NULL DEFAULT '',
    nic_addresses TEXT NOT NULL DEFAULT '',
    machine_type TEXT NOT NULL DEFAULT 'direct',
    allowed_ports TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '',
    country_code TEXT NOT NULL DEFAULT '',
    location TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    uuid TEXT NOT NULL UNIQUE,
    sub_token TEXT NOT NULL UNIQUE,
    expires_at INTEGER,
    expired INTEGER NOT NULL DEFAULT 0,
    disabled INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL DEFAULT '',
    server_id INTEGER NOT NULL REFERENCES servers(id),
    protocol TEXT NOT NULL DEFAULT 'vless',
    port INTEGER,
    config_template TEXT NOT NULL,
    realized_config TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE commands (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL REFERENCES servers(id),
    type TEXT NOT NULL,
    payload TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    error TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE chains (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE server_metrics (
    server_id INTEGER PRIMARY KEY REFERENCES servers(id),
    load1 REAL NOT NULL DEFAULT 0,
    cpu_percent REAL NOT NULL DEFAULT 0,
    mem_total INTEGER NOT NULL DEFAULT 0,
    mem_used INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
PRAGMA user_version = 2;
`

func TestOpenMigratesLegacySchemaAndPreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers
		(alias, token, address, learned_addr) VALUES ('legacy', 'token', '203.0.113.10', '198.51.100.5')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO commands
		(server_id, type, payload, status, attempts) VALUES (1, 'apply_node', '{"node":1}', 'acked', 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO server_metrics
		(server_id, load1, cpu_percent, mem_total, mem_used) VALUES (1, 0.5, 20, 1024, 512)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	var requestID, traceID, data, status, addressMode string
	var attempts int
	if err := st.db.QueryRow(`SELECT request_id, trace_id, data, status, attempts FROM commands WHERE id = 1`).
		Scan(&requestID, &traceID, &data, &status, &attempts); err != nil {
		t.Fatal(err)
	}
	if requestID != "legacy-1" || traceID != "legacy-1" || data != `{"node":1}` || status != "acked" || attempts != 2 {
		t.Fatalf("migrated command = %q %q %q %q %d", requestID, traceID, data, status, attempts)
	}
	if err := st.db.QueryRow(`SELECT address_mode FROM servers WHERE id = 1`).Scan(&addressMode); err != nil {
		t.Fatal(err)
	}
	if addressMode != "manual" {
		t.Fatalf("address_mode = %q, want manual", addressMode)
	}
	assertColumns(t, st.db, "servers", "credential_epoch", "agent_settings_revision", "agent_settings_error", "agent_settings_reported_at", "address_mode")
	assertColumns(t, st.db, "server_metrics", "load5", "load15", "disk_total", "network_interface", "latency_ms")
	assertColumns(t, st.db, "server_metric_history", "latency_probe_active")
	assertColumns(t, st.db, "chains", "service_node_id", "published_revision_id", "desired_revision_id", "traffic_multiplier_milli", "deleted_at", "updated_at")
	assertColumns(t, st.db, "chain_traffic_daily", "revision_id", "timezone", "effective_up", "effective_down")
	assertColumns(t, st.db, "chain_revision_tasks", "phase", "action", "kind", "command_id")
	assertColumns(t, st.db, "subscription_templates", "content_sha256", "last_attempt_at", "last_error")
	assertColumns(t, st.db, "subscription_template_rules", "template_sha256", "source_url", "content_sha256")
	assertColumns(t, st.db, "subscription_snapshots", "revision", "source_sha256", "generated_at", "warnings")
	assertColumns(t, st.db, "subscription_rule_files", "name", "format", "source_sha256", "content")
	if err := st.SaveServerMetrics(context.Background(), 1, ServerMetrics{}, true); err != nil {
		t.Fatalf("write nullable metrics after migration: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// A second start must not repeat the data migration or duplicate rows.
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM commands`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("command count after second open = %d, want 1", count)
	}
}

func TestOpenRollsBackFailedMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE servers (id INTEGER PRIMARY KEY, alias TEXT NOT NULL, token TEXT NOT NULL UNIQUE);
		CREATE TABLE commands (id INTEGER PRIMARY KEY, server_id INTEGER NOT NULL, type TEXT NOT NULL);
		PRAGMA user_version = 2;
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if st, err := Open(path); err == nil {
		st.Close()
		t.Fatal("Open succeeded for an unmigratable commands table")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("schema version after rollback = %d, want 2", version)
	}
	columns := columnNames(t, db, "servers")
	if columns["credential_epoch"] {
		t.Fatal("migration changes were not rolled back")
	}
}

func TestNormalizeNodeRealityMinClientVer(t *testing.T) {
	reality := `{"protocol":"vless","port":0,"template":{"tag":"node_1","port":443,` +
		`"protocol":"vless","streamSettings":{"network":"tcp","security":"reality",` +
		`"realitySettings":{"show":false,"dest":"dl.google.com:443","serverNames":["dl.google.com"],` +
		`"privateKey":"{{PRIVATE_KEY}}","shortIds":["0123abcd"]}}}}`
	fixed, err := normalizeNodeRealityMinClientVer([]byte(reality))
	if err != nil {
		t.Fatal(err)
	}
	var vc struct {
		Template struct {
			StreamSettings struct {
				RealitySettings map[string]any `json:"realitySettings"`
			} `json:"streamSettings"`
		} `json:"template"`
	}
	if err := json.Unmarshal(fixed, &vc); err != nil {
		t.Fatal(err)
	}
	if vc.Template.StreamSettings.RealitySettings["minClientVer"] != "0" {
		t.Fatalf("minClientVer 应被注入 0: %s", fixed)
	}
	if string(fixed) == reality {
		t.Fatal("缺 minClientVer 的模板应被改写")
	}

	// 已显式声明 → 原样保留。
	explicit := `{"protocol":"vless","template":{"streamSettings":{"security":"reality",` +
		`"realitySettings":{"minClientVer":"0"}}}}`
	if out, err := normalizeNodeRealityMinClientVer([]byte(explicit)); err != nil || string(out) != explicit {
		t.Fatalf("已声明 minClientVer 的模板不应被改写: %s %v", out, err)
	}

	// 非 reality 模板（无 realitySettings）→ 原样保留。
	plain := `{"protocol":"shadowsocks","template":{"port":8388}}`
	if out, err := normalizeNodeRealityMinClientVer([]byte(plain)); err != nil || string(out) != plain {
		t.Fatalf("非 reality 模板不应被改写: %s %v", out, err)
	}

	// 坏 JSON → 报错（交由调用方失败启动，避免静默吞损坏数据）。
	if _, err := normalizeNodeRealityMinClientVer([]byte(`not json`)); err == nil {
		t.Fatal("坏 JSON 应返回错误")
	}
}

func TestOpenMigratesNodeRealityMinClientVer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	serverID, err := st.CreateServer(context.Background(), "legacy", "203.0.113.10", "token", MachineTypeDirect, "", "", "JP", "")
	if err != nil {
		t.Fatal(err)
	}
	stale := `{"protocol":"vless","template":{"tag":"node_1","port":443,"protocol":"vless",` +
		`"streamSettings":{"network":"tcp","security":"reality",` +
		`"realitySettings":{"dest":"dl.google.com:443","serverNames":["dl.google.com"],` +
		`"privateKey":"{{PRIVATE_KEY}}","shortIds":["0123abcd"]}}}}`
	good := `{"protocol":"vless","template":{"streamSettings":{"security":"reality",` +
		`"realitySettings":{"minClientVer":"0","dest":"dl.google.com:443"}}}}`
	if _, err := st.InsertNode(context.Background(), "stale", serverID, "vless", nil, json.RawMessage(stale)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertNode(context.Background(), "good", serverID, "vless", nil, json.RawMessage(good)); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// 重启触发迁移：存量缺 minClientVer 的模板被修复，已修复的不动。
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.db.Query(`SELECT name, config_template FROM nodes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var name, tmpl string
		if err := rows.Scan(&name, &tmpl); err != nil {
			t.Fatal(err)
		}
		got[name] = tmpl
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"stale", "good"} {
		if !strings.Contains(got[name], `"minClientVer":"0"`) {
			t.Fatalf("节点 %s 模板应含 minClientVer=0: %s", name, got[name])
		}
	}
	if got["good"] != good {
		t.Fatalf("已修复模板不应被改写: %s", got["good"])
	}
	if !strings.Contains(got["stale"], `"shortIds":["0123abcd"]`) {
		t.Fatalf("修复不应丢失模板其他字段: %s", got["stale"])
	}
}

// TestMigrateLegacyPreservesSubToken 验证存量库迁移到 schemaVersion 12 后，
// 既有用户 sub_token 原样保留（订阅地址不变，用户硬约束）。
func TestMigrateLegacyPreservesSubToken(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	// 与 TestOpenMigratesLegacySchemaAndPreservesData 相同的 legacy 建表语句（users 含 sub_token）。
	if _, err := legacy.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL, uuid TEXT NOT NULL UNIQUE, sub_token TEXT NOT NULL UNIQUE,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO users (name, uuid, sub_token) VALUES ('a','ua','legacy-token-1')`); err != nil {
		t.Fatal(err)
	}
	_ = legacy.Close()

	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var version int
	if err := st.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 14 {
		t.Fatalf("schema version = %d, want 14", version)
	}
	var token string
	if err := st.db.QueryRowContext(ctx, `SELECT sub_token FROM users WHERE name='a'`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if token != "legacy-token-1" {
		t.Fatalf("迁移后 sub_token = %q, want legacy-token-1", token)
	}
	// 迁移后 6 张分组新表全部存在。
	rows, err := st.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"link_groups", "link_group_chains", "link_group_external_subscriptions",
		"user_groups", "user_group_members", "user_group_links",
	} {
		if !tables[want] {
			t.Fatalf("迁移后缺少分组表 %s", want)
		}
	}
}

func assertColumns(t *testing.T, db *sql.DB, table string, expected ...string) {
	t.Helper()
	columns := columnNames(t, db, table)
	for _, name := range expected {
		if !columns[name] {
			t.Errorf("%s.%s is missing after migration", table, name)
		}
	}
}

func columnNames(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, typ string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	return columns
}
