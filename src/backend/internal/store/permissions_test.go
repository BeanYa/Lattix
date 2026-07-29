package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSecuresDatabaseAndBackupPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	assertFileMode(t, path, 0o600)
	newPath := filepath.Join(t.TempDir(), "new-panel.db")
	newStore, err := Open(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := newStore.Close(); err != nil {
		t.Fatal(err)
	}
	assertFileMode(t, newPath, 0o600)

	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := st.Backup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	assertFileMode(t, backup, 0o600)
}

func TestOpenRejectsDatabaseSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "panel.db")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Open(link); err == nil {
		t.Fatal("Open accepted a database symlink")
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
