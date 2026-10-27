package servertest

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestCapFileSizeLowersSoftLimitOnly(t *testing.T) {
	var orig unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &orig); err != nil {
		t.Skipf("getrlimit unavailable: %v", err)
	}
	defer func() { _ = unix.Setrlimit(unix.RLIMIT_FSIZE, &orig) }()

	raised := orig
	raised.Cur = raised.Max
	if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &raised); err != nil {
		t.Skipf("setrlimit unavailable: %v", err)
	}

	capFileSize()

	var now unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &now); err != nil {
		t.Fatal(err)
	}
	if now.Max != orig.Max {
		t.Fatalf("hard limit changed: got %d, want %d", now.Max, orig.Max)
	}
	if want := min(orig.Max, uint64(workerFileSizeCap)); now.Cur != want {
		t.Fatalf("soft limit = %d, want %d", now.Cur, want)
	}
}
