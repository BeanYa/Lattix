package main

import (
	"path/filepath"
	"testing"
)

func TestDefaultTLSDirUsesCurrentHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got, want := defaultTLSDir(), filepath.Join(home, "cert"); got != want {
		t.Fatalf("defaultTLSDir() = %q, want %q", got, want)
	}
}
