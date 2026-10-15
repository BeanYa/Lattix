package ipquality

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// installTimeout is generous because it only applies on a fresh host where an
// index refresh and package downloads (or waiting on the dpkg lock) can be
// slow. Once the commands are present this path is skipped entirely.
const installTimeout = 10 * time.Minute

// distroFamilies maps the ID field of /etc/os-release to the package-manager
// family the installer knows how to drive.
var distroFamilies = map[string]string{
	"debian": "apt", "ubuntu": "apt", "linuxmint": "apt", "kali": "apt", "raspbian": "apt",
	"rhel": "rhel", "centos": "rhel", "almalinux": "rhel", "rocky": "rhel", "anolis": "rhel",
	"fedora": "fedora", "alinux": "alinux",
	"arch": "arch", "manjaro": "arch",
	"alpine":        "alpine",
	"opensuse-leap": "suse", "opensuse-tumbleweed": "suse", "suse": "suse", "sles": "suse", "sled": "suse",
}

// commandPackages maps each required command to its owning package per
// family. The fedora and alinux families reuse the RHEL package names.
var commandPackages = map[string]map[string]string{
	"apt":    {"jq": "jq", "curl": "curl", "bc": "bc", "nc": "netcat-openbsd", "dig": "dnsutils", "ip": "iproute2"},
	"rhel":   {"jq": "jq", "curl": "curl", "bc": "bc", "nc": "nmap-ncat", "dig": "bind-utils", "ip": "iproute"},
	"arch":   {"jq": "jq", "curl": "curl", "bc": "bc", "nc": "gnu-netcat", "dig": "bind-tools", "ip": "iproute2"},
	"alpine": {"jq": "jq", "curl": "curl", "bc": "bc", "nc": "netcat-openbsd", "dig": "bind-tools", "ip": "iproute2"},
	"suse":   {"jq": "jq", "curl": "curl", "bc": "bc", "nc": "gnu-netcat", "dig": "bind-utils", "ip": "iproute2"},
}

// installPlan is the concrete command sequence for one host: an optional
// index refresh whose failure is tolerated but reported, then install steps
// that must all succeed.
type installPlan struct {
	refresh []string
	install [][]string
}

// InstallDependencies installs the missing script commands with the system
// package manager. Unlike delegating to ip.sh's own -y installer, the script
// never runs during installation and a failing package command is reported
// verbatim instead of being drowned by the script's later check output.
func InstallDependencies(ctx context.Context, check func() []string) error {
	if len(check()) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	content, err := detectOSRelease()
	if err != nil {
		return fmt.Errorf("dependency install: %w", err)
	}
	id, versionID, _ := parseOSRelease(content)
	plan, err := planInstall(id, versionID, check())
	if err != nil {
		return fmt.Errorf("dependency install: %w", err)
	}
	return installWithPlan(ctx, plan, check, runInstallCommand, privilegePrefix)
}

func installWithPlan(
	ctx context.Context,
	plan installPlan,
	check func() []string,
	run func(context.Context, []string) (string, error),
	privileges func() ([]string, error),
) error {
	if len(check()) == 0 {
		return nil
	}
	prefix, err := privileges()
	if err != nil {
		return fmt.Errorf("dependency install: %w", err)
	}
	refreshNote := ""
	if len(plan.refresh) > 0 {
		if _, err := run(ctx, joinCommand(prefix, plan.refresh)); err != nil && ctx.Err() == nil {
			refreshNote = fmt.Sprintf("index refresh %s also failed: %v", strings.Join(plan.refresh, " "), err)
		}
	}
	for _, step := range plan.install {
		output, err := run(ctx, joinCommand(prefix, step))
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return fmt.Errorf("dependency install timed out (command: %s, output: %s)",
				strings.Join(step, " "), outputSummary(output))
		}
		if refreshNote != "" {
			return fmt.Errorf("dependency install failed: %s (output: %s; %s)",
				strings.Join(step, " "), outputSummary(output), refreshNote)
		}
		return fmt.Errorf("dependency install failed: %s (output: %s)",
			strings.Join(step, " "), outputSummary(output))
	}
	if remaining := check(); len(remaining) > 0 {
		return fmt.Errorf("dependency install finished but %s still missing", strings.Join(remaining, ", "))
	}
	return nil
}

func joinCommand(prefix, argv []string) []string {
	full := make([]string, 0, len(prefix)+len(argv))
	return append(append(full, prefix...), argv...)
}

// runInstallCommand executes one installer command and returns its capped
// combined output; the child runs in its own process group so the timeout can
// kill the whole tree.
func runInstallCommand(ctx context.Context, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output limitedBuffer
	output.max = maxOutputBytes
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		_ = killProcessGroup(cmd)
		<-done
		return output.buf.String(), ctx.Err()
	case err := <-done:
		return output.buf.String(), err
	}
}

// privilegePrefix resolves how package commands gain root: none for root,
// passwordless sudo otherwise; anything else refuses to guess.
func privilegePrefix() ([]string, error) {
	if syscall.Geteuid() == 0 {
		return nil, nil
	}
	if exec.Command("sudo", "-n", "true").Run() == nil {
		return []string{"sudo", "-n"}, nil
	}
	return nil, errors.New("not root and passwordless sudo unavailable; install the missing commands manually or run the agent as root")
}

// outputSummary keeps the head and tail of long command output so the failing
// package-manager error stays visible even when the middle is noise.
func outputSummary(output string) string {
	output = strings.TrimSpace(output)
	const chunk = 1024
	if len(output) <= 2*chunk {
		return output
	}
	return output[:chunk] + "\n...\n" + output[len(output)-chunk:]
}

func detectOSRelease() ([]byte, error) {
	for _, path := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if content, err := os.ReadFile(path); err == nil {
			return content, nil
		}
	}
	return nil, errors.New("read /etc/os-release: file missing; cannot detect the distribution")
}

// parseOSRelease extracts ID and VERSION_ID (quotes stripped); ok is false
// when the content carries no ID.
func parseOSRelease(content []byte) (id, versionID string, ok bool) {
	for _, line := range strings.Split(string(content), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "ID":
			id = value
		case "VERSION_ID":
			versionID = value
		}
	}
	return id, versionID, id != ""
}

func planInstall(id, versionID string, missing []string) (installPlan, error) {
	family, supported := distroFamilies[id]
	if !supported {
		return installPlan{}, fmt.Errorf("unsupported distribution %q: install %s manually, then re-run the test",
			id, strings.Join(missing, ", "))
	}
	packages, err := packagesFor(family, missing)
	if err != nil {
		return installPlan{}, err
	}
	var plan installPlan
	switch family {
	case "apt":
		plan.refresh = []string{"apt-get", "update"}
		plan.install = [][]string{append([]string{"apt-get", "install", "-y"}, packages...)}
	case "rhel", "fedora", "alinux":
		manager := "dnf"
		if major, ok := majorVersion(versionID); ok && major < 8 {
			manager = "yum"
		}
		// jq only lives in EPEL on the RHEL clones; fedora and alinux ship
		// their own repositories without needing it.
		if family == "rhel" {
			plan.install = append(plan.install, append([]string{manager, "install", "-y"}, "epel-release"))
		}
		plan.install = append(plan.install, append([]string{manager, "install", "-y"}, packages...))
	case "arch":
		plan.install = [][]string{append([]string{"pacman", "-Sy", "--noconfirm", "--needed"}, packages...)}
	case "alpine":
		plan.refresh = []string{"apk", "update"}
		plan.install = [][]string{append([]string{"apk", "add"}, packages...)}
	case "suse":
		plan.install = [][]string{append([]string{"zypper", "--non-interactive", "install"}, packages...)}
	}
	return plan, nil
}

func packagesFor(family string, missing []string) ([]string, error) {
	mapping, ok := commandPackages[resolvePackageFamily(family)]
	if !ok {
		return nil, fmt.Errorf("unsupported package family %q", family)
	}
	wanted := make(map[string]bool, len(missing))
	for _, command := range missing {
		wanted[command] = true
	}
	var packages []string
	seen := make(map[string]bool, len(missing))
	for _, command := range requiredCommands {
		if !wanted[command] {
			continue
		}
		pkg := mapping[command]
		if pkg == "" || seen[pkg] {
			continue
		}
		seen[pkg] = true
		packages = append(packages, pkg)
	}
	return packages, nil
}

// resolvePackageFamily folds the families that share RHEL package names into
// the rhel mapping.
func resolvePackageFamily(family string) string {
	if family == "fedora" || family == "alinux" {
		return "rhel"
	}
	return family
}

func packageForCommand(id, command string) string {
	return commandPackages[resolvePackageFamily(distroFamilies[id])][command]
}

func majorVersion(versionID string) (int, bool) {
	major, _, _ := strings.Cut(versionID, ".")
	value, err := strconv.Atoi(major)
	if err != nil {
		return 0, false
	}
	return value, true
}
