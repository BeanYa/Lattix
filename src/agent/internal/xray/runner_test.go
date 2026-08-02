package xray

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSystemdRunnerRestartErrorIncludesStderrAndJournal(t *testing.T) {
	orig := journalTail
	journalTail = func(context.Context, string, int) string { return "journal-boom" }
	defer func() { journalTail = orig }()

	r := &SystemdRunner{unit: "xray"}
	err := r.restartErr(errors.New("exit status 1"), "Job for xray.service failed\nSee systemctl status xray")
	for _, want := range []string{"exit status 1", "Job for xray.service failed", "journal-boom"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误应包含 %q: %v", want, err)
		}
	}
}

func TestSystemdRunnerRestartErrorTrimsLongOutput(t *testing.T) {
	orig := journalTail
	journalTail = func(context.Context, string, int) string { return "" }
	defer func() { journalTail = orig }()

	r := &SystemdRunner{unit: "xray"}
	err := r.restartErr(errors.New("exit status 1"), strings.Repeat("x", 5000))
	if len(err.Error()) > 2100 {
		t.Fatalf("错误消息过长: %d", len(err.Error()))
	}
}

func TestFirstLines(t *testing.T) {
	if got := firstLines("a\nb\nc\nd\ne", 3); got != "a | b | c" {
		t.Fatalf("firstLines = %q", got)
	}
	if got := firstLines("  \n single \n", 3); got != "single" {
		t.Fatalf("firstLines 去空白 = %q", got)
	}
}
