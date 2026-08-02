package ipquality

import (
	"context"
	"fmt"
	"io"
	"os/exec"
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
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
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
			if err == nil && len(check()) == 0 {
				return nil
			}
			return fmt.Errorf("dependency install: %w", err)
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

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
