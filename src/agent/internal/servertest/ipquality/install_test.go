package ipquality

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestParseOSRelease(t *testing.T) {
	id, version, ok := parseOSRelease([]byte("NAME=\"Ubuntu\"\nID=ubuntu\nVERSION_ID=\"22.04\"\nID_LIKE=debian\n"))
	if !ok || id != "ubuntu" || version != "22.04" {
		t.Fatalf("id=%q version=%q ok=%v", id, version, ok)
	}
	id, version, ok = parseOSRelease([]byte("ID=centos\nVERSION_ID=\"7.9.2003\"\n"))
	if !ok || id != "centos" || version != "7.9.2003" {
		t.Fatalf("id=%q version=%q ok=%v", id, version, ok)
	}
	if id, version, ok := parseOSRelease([]byte("ID=arch\n")); !ok || id != "arch" || version != "" {
		t.Fatalf("rolling distro id=%q version=%q ok=%v", id, version, ok)
	}
	if _, _, ok = parseOSRelease([]byte("NAME=\"unknown\"\n")); ok {
		t.Fatal("content without ID was accepted")
	}
}

func TestInstallPlanCoversEveryRequiredCommand(t *testing.T) {
	distros := []struct {
		id, version string
	}{
		{"debian", "12"}, {"ubuntu", "24.04"}, {"linuxmint", "21"}, {"kali", "2026.3"}, {"raspbian", "11"},
		{"rhel", "9.4"}, {"centos", "7.9.2003"}, {"almalinux", "9"}, {"rocky", "8.10"}, {"anolis", "8"}, {"alinux", "3"},
		{"fedora", "40"}, {"arch", ""}, {"manjaro", ""}, {"alpine", "3.20"},
		{"opensuse-leap", "15.5"}, {"opensuse-tumbleweed", ""}, {"sles", "15"},
	}
	for _, distro := range distros {
		for _, command := range requiredCommands {
			plan, err := planInstall(distro.id, distro.version, []string{command})
			if err != nil {
				t.Fatalf("%s: %s: %v", distro.id, command, err)
			}
			if len(plan.install) == 0 || len(plan.install[len(plan.install)-1]) < 2 {
				t.Fatalf("%s: %s: install steps = %v", distro.id, command, plan.install)
			}
			pkg := packageForCommand(distro.id, command)
			if !slicesContains(plan.install[len(plan.install)-1][1:], pkg) {
				t.Fatalf("%s: %s: package %q missing from %v", distro.id, command, pkg, plan.install)
			}
		}
	}
}

func TestInstallPlanRefreshAndEpelSteps(t *testing.T) {
	plan, err := planInstall("ubuntu", "24.04", []string{"dig"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"apt-get", "update"}; !slicesEqual(plan.refresh, want) {
		t.Fatalf("refresh = %v, want %v", plan.refresh, want)
	}
	if want := []string{"apt-get", "install", "-y", "dnsutils"}; !slicesEqual(plan.install[0], want) {
		t.Fatalf("install = %v, want %v", plan.install[0], want)
	}

	plan, err = planInstall("centos", "7.9.2003", []string{"dig"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"yum", "install", "-y", "epel-release"}; !slicesEqual(plan.install[0], want) {
		t.Fatalf("centos 7 first step = %v, want %v", plan.install[0], want)
	}
	if want := []string{"yum", "install", "-y", "bind-utils"}; !slicesEqual(plan.install[1], want) {
		t.Fatalf("centos 7 second step = %v, want %v", plan.install[1], want)
	}

	plan, err = planInstall("rocky", "9.4", []string{"dig"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"dnf", "install", "-y", "epel-release"}; !slicesEqual(plan.install[0], want) {
		t.Fatalf("rocky 9 first step = %v, want %v", plan.install[0], want)
	}

	plan, err = planInstall("fedora", "40", []string{"jq"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.install) != 1 || plan.install[0][0] != "dnf" {
		t.Fatalf("fedora steps = %v, want a single dnf step without EPEL", plan.install)
	}

	plan, err = planInstall("alpine", "3.20", []string{"jq", "curl"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"apk", "update"}; !slicesEqual(plan.refresh, want) {
		t.Fatalf("refresh = %v, want %v", plan.refresh, want)
	}
	if want := []string{"apk", "add", "jq", "curl"}; !slicesEqual(plan.install[0], want) {
		t.Fatalf("install = %v, want %v", plan.install[0], want)
	}
}

func TestInstallPlanRejectsUnsupportedDistro(t *testing.T) {
	_, err := planInstall("void", "", []string{"dig", "jq"})
	if err == nil || !strings.Contains(err.Error(), "unsupported") || !strings.Contains(err.Error(), "dig, jq") {
		t.Fatalf("err = %v, want unsupported distro naming missing commands", err)
	}
}

func TestInstallMissingRunsRefreshThenInstall(t *testing.T) {
	var ran [][]string
	remaining := []string{"dig", "ip"}
	check := func() []string { return remaining }
	run := func(_ context.Context, argv []string) (string, error) {
		ran = append(ran, argv)
		remaining = nil
		return "", nil
	}
	plan, err := planInstall("debian", "12", []string{"dig", "ip"})
	if err != nil {
		t.Fatal(err)
	}
	noopPrefix := func() ([]string, error) { return nil, nil }
	if err := installWithPlan(context.Background(), plan, check, run, noopPrefix); err != nil {
		t.Fatalf("installWithPlan: %v", err)
	}
	// A second round with satisfied dependencies must not run any command.
	if err := installWithPlan(context.Background(), plan, check, run, noopPrefix); err != nil {
		t.Fatalf("installWithPlan after success: %v", err)
	}
	if len(ran) != 2 {
		t.Fatalf("ran %d commands, want 2: %v", len(ran), ran)
	}
	if want := []string{"apt-get", "update"}; !slicesEqual(ran[0], want) {
		t.Fatalf("first = %v, want %v", ran[0], want)
	}
	if want := []string{"apt-get", "install", "-y", "dnsutils", "iproute2"}; !slicesEqual(ran[1], want) {
		t.Fatalf("second = %v, want %v", ran[1], want)
	}
}

func TestInstallMissingReportsInstallFailureWithOutput(t *testing.T) {
	run := func(_ context.Context, argv []string) (string, error) {
		if argv[1] == "update" {
			return "", errors.New("exit status 100")
		}
		return "E: Unable to locate package dnsutils", errors.New("exit status 100")
	}
	plan, err := planInstall("debian", "12", []string{"dig"})
	if err != nil {
		t.Fatal(err)
	}
	err = installWithPlan(context.Background(), plan, func() []string { return []string{"dig"} }, run,
		func() ([]string, error) { return nil, nil })
	if err == nil {
		t.Fatal("install failure was not reported")
	}
	for _, want := range []string{"apt-get install -y dnsutils", "Unable to locate package", "apt-get update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestInstallMissingReportsStillMissingAfterSuccess(t *testing.T) {
	plan, err := planInstall("debian", "12", []string{"dig"})
	if err != nil {
		t.Fatal(err)
	}
	err = installWithPlan(context.Background(), plan, func() []string { return []string{"dig"} },
		func(context.Context, []string) (string, error) { return "", nil },
		func() ([]string, error) { return nil, nil })
	if err == nil || !strings.Contains(err.Error(), "still missing") || !strings.Contains(err.Error(), "dig") {
		t.Fatalf("err = %v, want still-missing report", err)
	}
}

func TestInstallMissingTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	plan, err := planInstall("debian", "12", []string{"jq"})
	if err != nil {
		t.Fatal(err)
	}
	err = installWithPlan(ctx, plan, func() []string { return []string{"jq"} },
		func(ctx context.Context, _ []string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
		func() ([]string, error) { return nil, nil })
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
}

func TestInstallMissingRequiresPrivilegePrefix(t *testing.T) {
	plan, err := planInstall("debian", "12", []string{"jq"})
	if err != nil {
		t.Fatal(err)
	}
	err = installWithPlan(context.Background(), plan, func() []string { return []string{"jq"} },
		func(context.Context, []string) (string, error) {
			t.Fatal("commands must not run without privileges")
			return "", nil
		},
		func() ([]string, error) { return nil, errors.New("not root and passwordless sudo unavailable") })
	if err == nil || !strings.Contains(err.Error(), "passwordless sudo") {
		t.Fatalf("err = %v, want privilege failure", err)
	}
}

func TestRunInstallCommandExecutesFromPATH(t *testing.T) {
	shimDir := t.TempDir()
	shim := "#!/bin/sh\nif [ \"$1\" = fail ]; then exit 3; fi\necho \"$1-ok\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "fake-pkg"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir)
	output, err := runInstallCommand(context.Background(), []string{"fake-pkg", "install"})
	if err != nil {
		t.Fatalf("runInstallCommand: %v", err)
	}
	if strings.TrimSpace(output) != "install-ok" {
		t.Fatalf("output = %q", output)
	}
	if _, err := runInstallCommand(context.Background(), []string{"fake-pkg", "fail"}); err == nil {
		t.Fatal("failing command was accepted")
	}
}

func TestRunInstallCommandLiftsFileSizeCapForChild(t *testing.T) {
	var orig unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &orig); err != nil {
		t.Skipf("getrlimit unavailable: %v", err)
	}
	if orig.Max < 4<<20 {
		t.Skipf("hard limit %d too small to exercise the lift", orig.Max)
	}
	defer func() { _ = unix.Setrlimit(unix.RLIMIT_FSIZE, &orig) }()
	lowered := orig
	lowered.Cur = 1 << 20
	if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &lowered); err != nil {
		t.Skipf("setrlimit unavailable: %v", err)
	}

	// With the soft cap at 1 MiB the 3 MB write below dies with SIGXFSZ unless
	// runInstallCommand lifts the soft cap to the hard limit for the child.
	outputFile := filepath.Join(t.TempDir(), "payload.bin")
	if _, err := runInstallCommand(context.Background(), []string{
		"dd", "if=/dev/zero", "of=" + outputFile, "bs=1048576", "count=3",
	}); err != nil {
		t.Fatalf("runInstallCommand: %v", err)
	}
	var restored unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Cur != lowered.Cur {
		t.Fatalf("soft cap after restore = %d, want %d", restored.Cur, lowered.Cur)
	}
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func slicesContains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
