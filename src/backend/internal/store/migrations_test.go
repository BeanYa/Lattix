package store

import (
	"context"
	"database/sql"
	"path/filepath"
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
	if err := st.SaveServerMetrics(context.Background(), 1, ServerMetrics{}); err != nil {
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
