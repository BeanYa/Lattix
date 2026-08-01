package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedInstallRoot(t *testing.T) {
	cases := []struct {
		exe      string
		wantOK   bool
		wantRoot string
	}{
		{"/opt/lattix-agent/bin/lattix-agent", true, "/opt/lattix-agent"},
		{"/data/prefix/opt/lattix-agent/bin/lattix-agent", true, "/data/prefix/opt/lattix-agent"},
		{"/home/user/.lattix-agent/bin/lattix-agent", true, "/home/user/.lattix-agent"},
		{"/tmp/dev/lattix-agent", false, ""},
		{"/opt/lattix-agent/lattix-agent", false, ""},
		{"/usr/local/bin/lattix-agent", false, ""},
		{"", false, ""},
	}
	for _, tc := range cases {
		root, ok := managedInstallRoot(tc.exe)
		if ok != tc.wantOK {
			t.Fatalf("exe=%s ok=%v want %v", tc.exe, ok, tc.wantOK)
		}
		if ok && root != tc.wantRoot {
			t.Fatalf("exe=%s root=%s want %s", tc.exe, root, tc.wantRoot)
		}
	}
}

func TestInstallPathsSystemStyle(t *testing.T) {
	p := installPathsFor("/opt/lattix-agent")
	if !p.SystemStyle {
		t.Fatal("expected system style")
	}
	if p.Prefix != "" {
		t.Fatalf("prefix=%q", p.Prefix)
	}
	if p.UnitFile != "/etc/systemd/system/lattix-agent.service" {
		t.Fatalf("unit=%s", p.UnitFile)
	}
	if p.LatxAgLink != "/usr/local/bin/latx-ag" {
		t.Fatalf("latx-ag link=%s", p.LatxAgLink)
	}
	if p.ConnectionFile != "/opt/lattix-agent/data/connection.json" {
		t.Fatalf("connection=%s", p.ConnectionFile)
	}
	if p.CommandQueue != "/opt/lattix-agent/data/command-queue.json" {
		t.Fatalf("queue=%s", p.CommandQueue)
	}
	if p.LockFile != "/opt/lattix-agent/data/lattix-agent.lock" {
		t.Fatalf("lock=%s", p.LockFile)
	}
	if p.AgentLog != "/opt/lattix-agent/logs/agent.log" {
		t.Fatalf("log=%s", p.AgentLog)
	}
}

func TestInstallPathsPrefixed(t *testing.T) {
	p := installPathsFor("/srv/latx/opt/lattix-agent")
	if !p.SystemStyle || p.Prefix != "/srv/latx" {
		t.Fatalf("system=%v prefix=%q", p.SystemStyle, p.Prefix)
	}
	if p.UnitFile != "/srv/latx/etc/systemd/system/lattix-agent.service" {
		t.Fatalf("unit=%s", p.UnitFile)
	}
}

func TestInstallPathsUserStyle(t *testing.T) {
	root := filepath.Join("/home/alice", ".lattix-agent")
	p := installPathsFor(root)
	if p.SystemStyle {
		t.Fatal("user style should not be system")
	}
	if p.UnitFile != "" || p.LatxAgLink != "" {
		t.Fatalf("user style should not set unit/link: %q %q", p.UnitFile, p.LatxAgLink)
	}
	if p.RunScript != filepath.Join(root, "bin", "lattix-agent-run") {
		t.Fatalf("run script=%s", p.RunScript)
	}
}

func TestBuildUninstallScriptSystemdNoSleepBeforeDisable(t *testing.T) {
	p := installPathsFor("/opt/lattix-agent")
	script := buildUninstallScript(p, true, false)
	if strings.Contains(script, "sleep ") {
		t.Fatalf("script should not sleep before disable:\n%s", script)
	}
	disableIdx := strings.Index(script, "systemctl disable --now lattix-agent.service")
	rmIdx := strings.Index(script, "rm -f '/opt/lattix-agent/bin/lattix-agent'")
	if disableIdx < 0 || rmIdx < 0 || disableIdx > rmIdx {
		t.Fatalf("disable must precede rm:\n%s", script)
	}
	for _, want := range []string{
		"lattix-agent.bak", "agent.env", "/usr/local/bin/latx-ag",
		"rm -rf '/opt/lattix-agent/data' '/opt/lattix-agent/logs'",
		"rmdir '/opt/lattix-agent/bin' '/opt/lattix-agent/config'",
		"rmdir '/opt/lattix-agent'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	for _, notWant := range []string{
		"state.json", "connection.json", "settings.json",
		"command-queue.json", "lattix-agent.lock", "agent.log",
	} {
		if strings.Contains(script, notWant) {
			t.Fatalf("script should not reference %q (covered by data/logs dir removal):\n%s", notWant, script)
		}
	}
	if strings.Contains(script, "disable --now xray.service") {
		t.Fatal("agent-only must not stop xray")
	}
}

func TestBuildUninstallScriptSystemdPurge(t *testing.T) {
	p := installPathsFor("/opt/lattix-agent")
	script := buildUninstallScript(p, true, true)
	for _, want := range []string{
		"systemctl disable --now xray.service",
		"xray.json.rebind-backup",
		"rm -rf '/opt/lattix-agent'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("purge script missing %q:\n%s", want, script)
		}
	}
}

func TestBuildUninstallScriptUserMode(t *testing.T) {
	root := "/home/alice/.lattix-agent"
	p := installPathsFor(root)
	script := buildUninstallScript(p, false, false)
	if strings.Contains(script, "systemctl") {
		t.Fatalf("user script must not use systemctl:\n%s", script)
	}
	for _, want := range []string{
		"pkill -f '/home/alice/.lattix-agent/bin/lattix-agent-run'",
		"pkill -f '/home/alice/.lattix-agent/bin/lattix-agent -panel'",
		"crontab", "lattix-agent-run",
		"rm -rf '/home/alice/.lattix-agent/data' '/home/alice/.lattix-agent/logs'",
		"rmdir '/home/alice/.lattix-agent/bin' '/home/alice/.lattix-agent/config'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("user script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "/usr/local/bin/latx-ag") {
		t.Fatal("user script should not touch /usr/local/bin/latx-ag")
	}
}

func TestBuildUninstallScriptUserPurge(t *testing.T) {
	p := installPathsFor("/home/alice/.lattix-agent")
	script := buildUninstallScript(p, false, true)
	if !strings.Contains(script, "pkill -f '/home/alice/.lattix-agent/bin/xray run'") {
		t.Fatalf("purge should stop xray:\n%s", script)
	}
	if !strings.Contains(script, "rm -rf '/home/alice/.lattix-agent'") {
		t.Fatalf("purge should remove app root:\n%s", script)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'"'"'b'` {
		t.Fatalf("got %s", got)
	}
}

func swapSpawnHooks(t *testing.T) {
	t.Helper()
	orig := struct {
		look  func(string) (string, error)
		run   func(string, ...string) ([]byte, error)
		start func(string) (int, error)
		write func(string, []byte, os.FileMode) error
		rem   func(string) error
		stat  func(string) (os.FileInfo, error)
	}{lookPathFn, runCmdFn, startSetsidFn, writeFileFn, removeFn, statFn}
	t.Cleanup(func() {
		lookPathFn, runCmdFn, startSetsidFn = orig.look, orig.run, orig.start
		writeFileFn, removeFn, statFn = orig.write, orig.rem, orig.stat
	})
}

func TestSpawnCleanerPrefersSystemdRun(t *testing.T) {
	swapSpawnHooks(t)
	var ran []string
	lookPathFn = func(file string) (string, error) {
		if file == "systemd-run" || file == "systemctl" {
			return "/bin/" + file, nil
		}
		return "", errors.New("not found")
	}
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if name != "systemd-run" {
			t.Fatalf("unexpected command %s", name)
		}
		return []byte("Running as unit: lattix-agent-uninstall-1.service"), nil
	}
	startSetsidFn = func(string) (int, error) {
		t.Fatal("setsid should not run when systemd-run succeeds")
		return 0, nil
	}
	method := spawnCleaner("echo ok", true)
	if method != spawnSystemdRun {
		t.Fatalf("method=%s", method)
	}
	if len(ran) != 1 || !strings.Contains(ran[0], "--no-block") || !strings.Contains(ran[0], "--collect") {
		t.Fatalf("ran=%v", ran)
	}
}

func TestSpawnCleanerFallsBackToOneshot(t *testing.T) {
	swapSpawnHooks(t)
	lookPathFn = func(file string) (string, error) {
		if file == "systemd-run" {
			return "", errors.New("missing")
		}
		if file == "systemctl" {
			return "/bin/systemctl", nil
		}
		return "", errors.New("not found")
	}
	var cmds []string
	writeFileFn = func(name string, _ []byte, _ os.FileMode) error {
		if !strings.Contains(name, "lattix-agent-uninstall") {
			t.Fatalf("unexpected write %s", name)
		}
		return nil
	}
	// Use a real directory FileInfo for /run/systemd/system existence check.
	dirInfo, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	statFn = func(name string) (os.FileInfo, error) {
		if name == "/run/systemd/system" {
			return dirInfo, nil
		}
		return nil, os.ErrNotExist
	}
	removeFn = func(string) error { return nil }
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return []byte("ok"), nil
	}
	startSetsidFn = func(string) (int, error) {
		t.Fatal("setsid should not run when oneshot succeeds")
		return 0, nil
	}
	method := spawnCleaner("echo ok", true)
	if method != spawnSystemdOneshot {
		t.Fatalf("method=%s cmds=%v", method, cmds)
	}
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "daemon-reload") || !strings.Contains(joined, "start --no-block") {
		t.Fatalf("cmds=%s", joined)
	}
}

func TestSpawnCleanerFallsBackToSetsid(t *testing.T) {
	swapSpawnHooks(t)
	lookPathFn = func(string) (string, error) { return "", errors.New("none") }
	runCmdFn = func(string, ...string) ([]byte, error) {
		return nil, errors.New("should not run")
	}
	statFn = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	setsidCalled := false
	startSetsidFn = func(script string) (int, error) {
		setsidCalled = true
		if script != "echo ok" {
			t.Fatalf("script=%q", script)
		}
		return 4242, nil
	}
	method := spawnCleaner("echo ok", true)
	if method != spawnSetsid || !setsidCalled {
		t.Fatalf("method=%s setsid=%v", method, setsidCalled)
	}
}

func TestSpawnCleanerUserModeSkipsSystemd(t *testing.T) {
	swapSpawnHooks(t)
	lookPathFn = func(string) (string, error) {
		t.Fatal("user mode should not probe systemd tools")
		return "", nil
	}
	startSetsidFn = func(string) (int, error) { return 7, nil }
	if method := spawnCleaner("echo ok", false); method != spawnSetsid {
		t.Fatalf("method=%s", method)
	}
}

func TestSpawnViaSystemdOneshotCleansUpOnStartFailure(t *testing.T) {
	swapSpawnHooks(t)
	lookPathFn = func(file string) (string, error) {
		if file == "systemctl" {
			return "/bin/systemctl", nil
		}
		return "", errors.New("no")
	}
	dirInfo, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	statFn = func(name string) (os.FileInfo, error) {
		if name == "/run/systemd/system" {
			return dirInfo, nil
		}
		return nil, os.ErrNotExist
	}
	var written, removed []string
	writeFileFn = func(name string, _ []byte, _ os.FileMode) error {
		written = append(written, name)
		return nil
	}
	removeFn = func(name string) error {
		removed = append(removed, name)
		return nil
	}
	runCmdFn = func(name string, args ...string) ([]byte, error) {
		if name == "systemctl" && len(args) > 0 && args[0] == "daemon-reload" {
			return nil, nil
		}
		if name == "systemctl" && len(args) > 0 && args[0] == "start" {
			return []byte("fail"), errors.New("start failed")
		}
		return nil, nil
	}
	if _, err := spawnViaSystemdOneshot("echo hi"); err == nil {
		t.Fatal("expected error")
	}
	if len(written) < 2 {
		t.Fatalf("written=%v", written)
	}
	if len(removed) < 2 {
		t.Fatalf("should clean up script+unit on failure, removed=%v", removed)
	}
}
