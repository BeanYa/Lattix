package ipquality

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lattix/shared/requester"
)

const (
	defaultRunTimeout = 15 * time.Minute
	maxOutputBytes    = 16 << 20
)

// RunResult carries the script metadata and its raw stdout for the caller.
type RunResult struct {
	ScriptVersion string
	ScriptStale   bool
	Output        string // raw script stdout, one or two JSON documents
}

// Runner executes the xykt/IPQuality script for the Lattix agent.
type Runner struct {
	DataDir string
	// Fetcher defaults to requester.ExternalFileRequester with a 30s client.
	Fetcher ScriptFetcher
	// Timeout bounds one full script run; defaults to 15 minutes.
	Timeout time.Duration
	// Missing lists absent script dependencies; defaults to MissingDependencies.
	Missing func() []string
}

// Run prepares the script, ensures dependencies, executes it with
// "-p -j -f" and returns the raw JSON output for the caller to parse.
func (r *Runner) Run(ctx context.Context, progress func(string)) (RunResult, error) {
	if progress == nil {
		progress = func(string) {}
	}
	fetcher := r.Fetcher
	if fetcher == nil {
		fetcher = requester.ExternalFileRequester{Doer: &http.Client{Timeout: 30 * time.Second}}
	}
	missing := r.Missing
	if missing == nil {
		missing = MissingDependencies
	}

	progress("下载脚本")
	scriptPath, scriptVersion, stale, err := EnsureScript(ctx, fetcher, filepath.Join(r.DataDir, "scripts"))
	if err != nil {
		return RunResult{}, err
	}

	progress("检查依赖")
	if len(missing()) > 0 {
		progress("安装依赖")
		if err := InstallDependencies(ctx, scriptPath, missing); err != nil {
			return RunResult{}, err
		}
	}

	progress("运行检测")
	stdout, err := runScript(ctx, scriptPath, r.Timeout)
	if err != nil {
		if families, parseErr := ParseScriptOutput(stdout); parseErr == nil && len(families) > 0 {
			// Upstream ip.sh exits 1 when its trailing IPv6 gate
			// `[[ $IPV6work -ne 0 && ... ]]&&check_IP "$IPV6" 6` is false on
			// hosts without public IPv6 (e.g. NAT-tier machines), even though
			// the IPv4 family document was fully printed to stdout. Accept the
			// completed report; the exit code is a script artifact.
			progress("解析结果")
			return RunResult{
				ScriptVersion: scriptVersion,
				ScriptStale:   stale,
				Output:        stdout,
			}, nil
		}
		return RunResult{}, err
	}
	if strings.TrimSpace(stdout) == "" {
		return RunResult{}, ErrNoFamily
	}

	progress("解析结果")
	return RunResult{
		ScriptVersion: scriptVersion,
		ScriptStale:   stale,
		Output:        stdout,
	}, nil
}

func runScript(ctx context.Context, scriptPath string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", scriptPath, "-p", "-j", "-f")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.max = maxOutputBytes
	stderr.max = maxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start ip.sh: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		_ = killProcessGroup(cmd)
		<-done
		return "", fmt.Errorf("ip.sh timed out after %s", timeout)
	case err := <-done:
		if stdout.exceeded {
			return "", fmt.Errorf("ip.sh output exceeds %d bytes", maxOutputBytes)
		}
		if err != nil {
			tail := strings.TrimSpace(stderr.buf.String())
			if len(tail) > 2048 {
				tail = tail[len(tail)-2048:]
			}
			if tail != "" {
				return stdout.buf.String(), fmt.Errorf("ip.sh failed: %w (stderr: %s)", err, tail)
			}
			return stdout.buf.String(), fmt.Errorf("ip.sh failed: %w", err)
		}
	}
	return stdout.buf.String(), nil
}

type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if !b.exceeded && b.buf.Len()+len(p) > b.max {
		b.exceeded = true
		return 0, errors.New("output limit exceeded")
	}
	return b.buf.Write(p)
}
