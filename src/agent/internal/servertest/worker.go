package servertest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"lattix/shared"
)

type workerInput struct {
	AgentVersion  string                      `json:"agent_version"`
	SandboxState  string                      `json:"sandbox_state"`
	SandboxReason string                      `json:"sandbox_reason,omitempty"`
	Payload       shared.ServerTestRunPayload `json:"payload"`
}

type workerMessage struct {
	Kind     string                            `json:"kind"`
	Progress *shared.ServerTestProgressPayload `json:"progress,omitempty"`
	Result   *shared.ServerTestReport          `json:"result,omitempty"`
}

func WorkerMain(stdin io.Reader, stdout io.Writer) error {
	var input workerInput
	decoder := json.NewDecoder(io.LimitReader(stdin, 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode worker input: %w", err)
	}
	if err := input.Payload.Validate(); err != nil {
		return err
	}
	applyWorkerLimits()
	encoder := json.NewEncoder(stdout)
	runner := NewRunner(input.AgentVersion)
	report := runner.Run(context.Background(), input.Payload, func(progress shared.ServerTestProgressPayload) {
		_ = encoder.Encode(workerMessage{Kind: "progress", Progress: &progress})
	}, input.SandboxState, input.SandboxReason)
	return encoder.Encode(workerMessage{Kind: "result", Result: &report})
}

func applyWorkerLimits() {
	limits := []struct {
		resource int
		limit    unix.Rlimit
	}{
		{unix.RLIMIT_NOFILE, unix.Rlimit{Cur: 1024, Max: 1024}},
		{unix.RLIMIT_NPROC, unix.Rlimit{Cur: 128, Max: 128}},
		{unix.RLIMIT_CORE, unix.Rlimit{Cur: 0, Max: 0}},
		{unix.RLIMIT_FSIZE, unix.Rlimit{Cur: 32 << 20, Max: 32 << 20}},
	}
	for _, item := range limits {
		_ = unix.Setrlimit(item.resource, &item.limit)
	}
}

// RunWorker starts the fixed-function worker in PID/mount/IPC namespaces when
// permitted. Network is deliberately shared with the host. If namespace setup
// is denied, it retries with process/temp-dir/rlimit isolation and reports the
// degraded sandbox in the final result.
func RunWorker(ctx context.Context, dataDir, agentVersion string, payload shared.ServerTestRunPayload, progress ProgressFunc) (shared.ServerTestReport, error) {
	executable, err := os.Executable()
	if err != nil {
		return shared.ServerTestReport{}, err
	}
	tempRoot := filepath.Join(dataDir, "server-test-tmp")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		return shared.ServerTestReport{}, fmt.Errorf("create test temp root: %w", err)
	}
	tempDir, err := os.MkdirTemp(tempRoot, payload.TaskID+"-")
	if err != nil {
		return shared.ServerTestReport{}, err
	}
	if err := os.Chmod(tempDir, 0o700); err != nil {
		_ = os.RemoveAll(tempDir)
		return shared.ServerTestReport{}, err
	}
	defer os.RemoveAll(tempDir)

	state, reason := "isolated", ""
	cloneFlags := uintptr(0)
	if runtime.GOOS == "linux" {
		cloneFlags = syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWIPC
	} else {
		state, reason = "degraded", "namespace isolation is unavailable on "+runtime.GOOS
	}
	report, stderr, err := runWorkerProcess(ctx, executable, tempDir, agentVersion, payload, state, reason, cloneFlags, progress)
	if err == nil || cloneFlags == 0 || (!errors.Is(err, syscall.EPERM) && !strings.Contains(strings.ToLower(err.Error()), "operation not permitted")) {
		return report, err
	}
	state, reason = "degraded", "PID/mount/IPC namespaces were denied; using process, temp-dir and rlimit isolation"
	report, fallbackStderr, fallbackErr := runWorkerProcess(ctx, executable, tempDir, agentVersion, payload, state, reason, 0, progress)
	if fallbackErr != nil {
		return report, fmt.Errorf("sandbox fallback failed: %w (isolated stderr=%s; fallback stderr=%s)", fallbackErr, stderr, fallbackStderr)
	}
	return report, nil
}

func runWorkerProcess(
	ctx context.Context,
	executable, tempDir, agentVersion string,
	payload shared.ServerTestRunPayload,
	sandboxState, sandboxReason string,
	cloneFlags uintptr,
	progress ProgressFunc,
) (shared.ServerTestReport, string, error) {
	input, err := json.Marshal(workerInput{
		AgentVersion: agentVersion, SandboxState: sandboxState, SandboxReason: sandboxReason, Payload: payload,
	})
	if err != nil {
		return shared.ServerTestReport{}, "", err
	}
	cmd := exec.CommandContext(ctx, executable, "--server-test-worker")
	cmd.Dir = tempDir
	cmd.Stdin = strings.NewReader(string(input))
	if cloneFlags != 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: cloneFlags}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return shared.ServerTestReport{}, "", err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return shared.ServerTestReport{}, stderr.String(), err
	}
	var report *shared.ServerTestReport
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 9<<20)
	for scanner.Scan() {
		var message workerMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return shared.ServerTestReport{}, stderr.String(), fmt.Errorf("decode worker output: %w", err)
		}
		switch message.Kind {
		case "progress":
			if message.Progress != nil && progress != nil {
				progress(*message.Progress)
			}
		case "result":
			report = message.Result
		default:
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return shared.ServerTestReport{}, stderr.String(), fmt.Errorf("unknown worker message %q", message.Kind)
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return shared.ServerTestReport{}, stderr.String(), err
	}
	if err := cmd.Wait(); err != nil {
		return shared.ServerTestReport{}, stderr.String(), err
	}
	if report == nil {
		return shared.ServerTestReport{}, stderr.String(), errors.New("worker exited without a result")
	}
	return *report, stderr.String(), nil
}
