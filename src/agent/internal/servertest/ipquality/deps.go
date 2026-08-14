package ipquality

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const installTimeout = 2 * time.Minute

var requiredCommands = []string{"jq", "curl", "bc", "nc", "dig", "ip"}

// MissingDependencies lists the script runtime commands absent from PATH.
func MissingDependencies() []string {
	var missing []string
	for _, command := range requiredCommands {
		if _, err := exec.LookPath(command); err != nil {
			missing = append(missing, command)
		}
	}
	return missing
}

// InstallDependencies runs the script's own installer (-y auto-install) for
// a short window and polls until every dependency appears. The script keeps
// running its v4 checks after installing; the process group is killed as soon
// as the dependencies are ready.
func InstallDependencies(ctx context.Context, scriptPath string, check func() []string) error {
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", scriptPath, "-y", "-p", "-4")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr limitedBuffer
	stdout.max = maxOutputBytes
	stderr.max = maxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dependency installer: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if remaining := check(); len(remaining) == 0 {
				// Upstream ip.sh exits 1 from its trailing IPv6 gate even
				// after a successful install (see Runner.Run); the
				// dependency state is the source of truth here.
				return nil
			} else if err != nil {
				if tail := outputTail(&stderr, &stdout); tail != "" {
					return fmt.Errorf("dependency install: %w (output: %s)", err, tail)
				}
				return fmt.Errorf("dependency install: %w (still missing: %s)", err, strings.Join(remaining, ", "))
			} else {
				return fmt.Errorf("dependency install finished but still missing: %s", strings.Join(remaining, ", "))
			}
		case <-ticker.C:
			if len(check()) == 0 {
				_ = killProcessGroup(cmd)
				<-done
				return nil
			}
		case <-ctx.Done():
			_ = killProcessGroup(cmd)
			<-done
			return fmt.Errorf("dependency install timed out after %s", installTimeout)
		}
	}
}

// outputTail returns the trimmed tail of the first non-empty buffer,
// capped at 2048 bytes, for installer diagnostics.
func outputTail(buffers ...*limitedBuffer) string {
	for _, buffer := range buffers {
		tail := strings.TrimSpace(buffer.buf.String())
		if tail == "" {
			continue
		}
		if len(tail) > 2048 {
			tail = tail[len(tail)-2048:]
		}
		return tail
	}
	return ""
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
