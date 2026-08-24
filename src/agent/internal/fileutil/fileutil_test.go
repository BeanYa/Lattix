package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

// 遗留 .tmp（权限宽于目标）被复用时，写后 Chmod 须把权限收到目标值（凭证落盘 0600）。
func TestWriteFileAtomicTightensStaleTmpPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path+".tmp", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm = %o, want 600（遗留 .tmp 权限须在 rename 前收紧）", got)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "secret" {
		t.Fatalf("content = %q, want secret", content)
	}
}
