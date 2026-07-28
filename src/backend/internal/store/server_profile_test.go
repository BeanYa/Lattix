package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestServerProfileAndAddressMode(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "profile.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	id, err := st.CreateServer(ctx, "old name", "", "token", MachineTypeDirect, "", "", "US", "Seattle")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := st.ServerByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if srv.AddressMode != AddressModeAuto {
		t.Fatalf("initial address mode = %q, want auto", srv.AddressMode)
	}
	if err := st.UpdateServerAlias(ctx, id, "new name"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateServerAddress(ctx, id, "server.example.com"); err != nil {
		t.Fatal(err)
	}
	srv, err = st.ServerByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Alias != "new name" || srv.AddressMode != AddressModeManual {
		t.Fatalf("updated server = alias %q, mode %q", srv.Alias, srv.AddressMode)
	}
	if err := st.UpdateServerAddress(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	srv, _ = st.ServerByID(ctx, id)
	if srv.AddressMode != AddressModeAuto {
		t.Fatalf("cleared address mode = %q, want auto", srv.AddressMode)
	}
}

func TestOpenMigratesLegacyServerAddressMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE servers (
			id INTEGER PRIMARY KEY, alias TEXT NOT NULL, token TEXT NOT NULL UNIQUE,
			address TEXT NOT NULL DEFAULT '', learned_addr TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO servers (id, alias, token, address, learned_addr) VALUES
			(1, 'auto', 'a', '192.168.32.1', '192.168.32.1'),
			(2, 'manual', 'b', 'panel.example.com', '192.168.32.1');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.db.Query(`SELECT id, address_mode FROM servers ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []string{AddressModeAuto, AddressModeManual}
	for i := 0; rows.Next(); i++ {
		var id int
		var mode string
		if err := rows.Scan(&id, &mode); err != nil {
			t.Fatal(err)
		}
		if mode != want[i] {
			t.Fatalf("server %d address mode = %q, want %q", id, mode, want[i])
		}
	}
}
