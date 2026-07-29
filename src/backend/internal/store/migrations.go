package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// schemaVersion must be incremented whenever Schema changes. Migrations run
// before the rest of the backend starts, in the same transaction as schema setup.
const schemaVersion = 6

type columnMigration struct {
	name       string
	definition string
}

func initializeSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if _, err := tx.Exec(Schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	if version < schemaVersion {
		if err := migrateSchema(tx); err != nil {
			return fmt.Errorf("migrate schema from version %d to %d: %w", version, schemaVersion, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
	}
	if err := migrateCustomExchangeRates(tx); err != nil {
		return fmt.Errorf("migrate custom exchange rates: %w", err)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO server_traffic_plans
		(server_id, quota_bytes, accounting_mode, reset_anchor_on, reset_count, reset_unit, tracking_started_on)
		SELECT id, NULL, 'outbound', date('now'), 1, 'month', date('now') FROM servers`); err != nil {
		return fmt.Errorf("initialize server traffic plans: %w", err)
	}
	return tx.Commit()
}

func migrateSchema(tx *sql.Tx) error {
	serversAdded, err := ensureColumns(tx, "servers", []columnMigration{
		{"config_drift", "INTEGER NOT NULL DEFAULT 0"},
		{"agent_version", "TEXT"},
		{"address", "TEXT NOT NULL DEFAULT ''"},
		{"learned_addr", "TEXT NOT NULL DEFAULT ''"},
		{"nic_addresses", "TEXT NOT NULL DEFAULT ''"},
		{"machine_type", "TEXT NOT NULL DEFAULT 'direct'"},
		{"allowed_ports", "TEXT NOT NULL DEFAULT ''"},
		{"tags", "TEXT NOT NULL DEFAULT ''"},
		{"country_code", "TEXT NOT NULL DEFAULT ''"},
		{"location", "TEXT NOT NULL DEFAULT ''"},
		{"credential_epoch", "INTEGER NOT NULL DEFAULT 1"},
		{"credential_committed", "INTEGER NOT NULL DEFAULT 0"},
		{"credential_pending_token", "TEXT NOT NULL DEFAULT ''"},
		{"credential_exchange_id", "TEXT NOT NULL DEFAULT ''"},
		{"last_connected_at", "DATETIME"},
		{"last_disconnected_at", "DATETIME"},
		{"last_reconnected_at", "DATETIME"},
		{"reconnect_count", "INTEGER NOT NULL DEFAULT 0"},
		{"last_disconnect_reason", "TEXT NOT NULL DEFAULT ''"},
		{"agent_settings_revision", "INTEGER NOT NULL DEFAULT 0"},
		{"agent_settings_error", "TEXT NOT NULL DEFAULT ''"},
		{"agent_settings_reported_at", "DATETIME"},
		{"address_mode", "TEXT NOT NULL DEFAULT 'auto'"},
	})
	if err != nil {
		return err
	}
	if serversAdded["address_mode"] {
		// Before address_mode existed, matching address/learned_addr values were
		// learned automatically; other non-empty addresses were administrator supplied.
		if _, err := tx.Exec(`UPDATE servers SET address_mode = CASE
			WHEN address = '' OR address = learned_addr THEN 'auto' ELSE 'manual' END`); err != nil {
			return fmt.Errorf("backfill servers.address_mode: %w", err)
		}
	}
	if serversAdded["credential_committed"] {
		columns, err := tableColumns(tx, "servers")
		if err != nil {
			return err
		}
		backfill := `UPDATE servers SET credential_committed = 1`
		if columns["last_seen_at"] {
			backfill = `UPDATE servers SET credential_committed = CASE
				WHEN last_seen_at IS NULL THEN 0 ELSE 1 END`
		}
		if _, err := tx.Exec(backfill); err != nil {
			return fmt.Errorf("backfill servers.credential_committed: %w", err)
		}
	}

	for table, columns := range map[string][]columnMigration{
		"users": {
			{"expires_at", "INTEGER"},
			{"expired", "INTEGER NOT NULL DEFAULT 0"},
			{"disabled", "INTEGER NOT NULL DEFAULT 0"},
			{"traffic_limit", "INTEGER NOT NULL DEFAULT 0"},
			{"traffic_reset_day", "INTEGER NOT NULL DEFAULT 0"},
			{"sub_title", "TEXT NOT NULL DEFAULT ''"},
			{"sub_announcement", "TEXT NOT NULL DEFAULT ''"},
		},
		"traffic": {
			{"period_start", "TEXT NOT NULL DEFAULT ''"},
		},
		"nodes": {{"name", "TEXT NOT NULL DEFAULT ''"}},
		"chains": {
			{"name", "TEXT NOT NULL DEFAULT ''"},
			{"service_node_id", "INTEGER NOT NULL DEFAULT 0"},
			{"published_revision_id", "INTEGER NOT NULL DEFAULT 0"},
			{"desired_revision_id", "INTEGER NOT NULL DEFAULT 0"},
			{"traffic_multiplier_milli", "INTEGER NOT NULL DEFAULT 1000"},
			{"deleted_at", "DATETIME"},
			{"updated_at", "DATETIME"},
		},
		"server_metrics": {
			{"load5", "REAL NOT NULL DEFAULT 0"},
			{"load15", "REAL NOT NULL DEFAULT 0"},
			{"disk_total", "INTEGER NOT NULL DEFAULT 0"},
			{"disk_used", "INTEGER NOT NULL DEFAULT 0"},
			{"network_interface", "TEXT NOT NULL DEFAULT ''"},
			{"network_tx_bytes", "INTEGER NOT NULL DEFAULT 0"},
			{"network_rx_bytes", "INTEGER NOT NULL DEFAULT 0"},
			{"network_tx_bps", "REAL"},
			{"network_rx_bps", "REAL"},
			{"uptime_seconds", "INTEGER NOT NULL DEFAULT 0"},
			{"latency_ms", "REAL"},
		},
	} {
		if _, err := ensureColumns(tx, table, columns); err != nil {
			return err
		}
	}
	if err := migrateServerMetrics(tx); err != nil {
		return err
	}
	return migrateCommands(tx)
}

func ensureColumns(tx *sql.Tx, table string, migrations []columnMigration) (map[string]bool, error) {
	columns, err := tableColumns(tx, table)
	if err != nil {
		return nil, err
	}
	added := make(map[string]bool)
	for _, migration := range migrations {
		if columns[migration.name] {
			continue
		}
		query := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, migration.name, migration.definition)
		if _, err := tx.Exec(query); err != nil {
			return nil, fmt.Errorf("add %s.%s: %w", table, migration.name, err)
		}
		added[migration.name] = true
	}
	return added, nil
}

func tableColumns(tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, fmt.Errorf("inspect table %s: %w", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, typ string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("inspect table %s: %w", table, err)
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func migrateCommands(tx *sql.Tx) error {
	columns, err := tableColumns(tx, "commands")
	if err != nil {
		return err
	}
	required := []string{"request_id", "trace_id", "data", "error"}
	current := true
	for _, name := range required {
		current = current && columns[name]
	}
	if current {
		return nil
	}
	if !columns["payload"] && !columns["data"] {
		return fmt.Errorf("commands table has neither payload nor data column")
	}

	expression := func(column, fallback string) string {
		if columns[column] {
			return column
		}
		return fallback
	}
	values := []string{
		"id",
		expression("request_id", `printf('legacy-%d', id)`),
		expression("trace_id", `printf('legacy-%d', id)`),
		"server_id", "type",
		expression("data", "payload"),
		expression("status", `'queued'`),
		expression("error", `''`),
		expression("attempts", "0"),
		expression("created_at", "CURRENT_TIMESTAMP"),
		expression("updated_at", "CURRENT_TIMESTAMP"),
	}
	statements := []string{
		`ALTER TABLE commands RENAME TO commands_legacy`,
		`CREATE TABLE commands (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			request_id TEXT NOT NULL UNIQUE,
			trace_id TEXT NOT NULL,
			server_id INTEGER NOT NULL REFERENCES servers(id),
			type TEXT NOT NULL,
			data TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'queued',
			error TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO commands
			(id, request_id, trace_id, server_id, type, data, status, error, attempts, created_at, updated_at)
			SELECT ` + strings.Join(values, ", ") + ` FROM commands_legacy`,
		`DROP TABLE commands_legacy`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("migrate commands table: %w", err)
		}
	}
	return nil
}

func migrateServerMetrics(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(server_metrics)`)
	if err != nil {
		return fmt.Errorf("inspect server_metrics: %w", err)
	}
	cpuNotNull := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, typ string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("inspect server_metrics: %w", err)
		}
		if name == "cpu_percent" {
			cpuNotNull = notNull != 0
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !cpuNotNull {
		return nil
	}

	statements := []string{
		`ALTER TABLE server_metrics RENAME TO server_metrics_legacy`,
		`CREATE TABLE server_metrics (
			server_id INTEGER PRIMARY KEY REFERENCES servers(id),
			load1 REAL NOT NULL DEFAULT 0,
			load5 REAL NOT NULL DEFAULT 0,
			load15 REAL NOT NULL DEFAULT 0,
			cpu_percent REAL,
			mem_total INTEGER NOT NULL DEFAULT 0,
			mem_used INTEGER NOT NULL DEFAULT 0,
			disk_total INTEGER NOT NULL DEFAULT 0,
			disk_used INTEGER NOT NULL DEFAULT 0,
			network_interface TEXT NOT NULL DEFAULT '',
			network_tx_bytes INTEGER NOT NULL DEFAULT 0,
			network_rx_bytes INTEGER NOT NULL DEFAULT 0,
			network_tx_bps REAL,
			network_rx_bps REAL,
			uptime_seconds INTEGER NOT NULL DEFAULT 0,
			latency_ms REAL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO server_metrics
			SELECT server_id, load1, load5, load15, cpu_percent, mem_total, mem_used,
				disk_total, disk_used, network_interface, network_tx_bytes, network_rx_bytes,
				network_tx_bps, network_rx_bps, uptime_seconds, latency_ms, updated_at
			FROM server_metrics_legacy`,
		`DROP TABLE server_metrics_legacy`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("migrate server_metrics table: %w", err)
		}
	}
	return nil
}

func migrateCustomExchangeRates(tx *sql.Tx) error {
	// The latest row wins for databases that allowed several destinations for
	// the same source currency.
	if _, err := tx.Exec(`DELETE FROM custom_exchange_rates
		WHERE id NOT IN (SELECT MAX(id) FROM custom_exchange_rates GROUP BY source_currency)`); err != nil {
		return err
	}
	_, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_custom_exchange_rates_source
		ON custom_exchange_rates(source_currency)`)
	return err
}
