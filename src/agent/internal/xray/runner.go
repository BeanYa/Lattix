package xray

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Runner 抽象 xray 服务控制（§6 兜底重启）：
// 生产环境用 systemd（systemctl restart xray），dev 环境用 exec（直接拉起子进程）。
type Runner interface {
	// Restart 以当前 config.json 重启 xray。
	Restart(ctx context.Context) error
	// IsRunning 报告 xray 是否在运行（遥测，§13）。
	IsRunning(ctx context.Context) bool
}

// NewRunner 按 kind 创建 Runner：systemd（默认）| exec（dev）。
func NewRunner(kind, bin, configPath string) Runner {
	if kind == "exec" {
		return &ExecRunner{bin: bin, configPath: configPath}
	}
	return &SystemdRunner{unit: "xray"}
}

// SystemdRunner 通过 systemctl 管理 xray.service（生产，install.sh 注册的单元）。
type SystemdRunner struct {
	unit string
}

// Restart 实现 Runner。
func (r *SystemdRunner) Restart(ctx context.Context) error {
	return exec.CommandContext(ctx, "systemctl", "restart", r.unit).Run()
}

// IsRunning 实现 Runner。
func (r *SystemdRunner) IsRunning(ctx context.Context) bool {
	return exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", r.unit).Run() == nil
}

// ExecRunner 直接拉起 xray 子进程（dev 联调用，输出并入 agent 日志）。
type ExecRunner struct {
	bin        string
	configPath string

	mu      sync.Mutex
	cmd     *exec.Cmd
	running atomic.Bool
}

// Restart 实现 Runner：杀掉旧子进程后以当前配置重新拉起。
func (r *ExecRunner) Restart(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		r.cmd.Process.Kill()
		r.cmd.Wait()
		r.cmd = nil
	}
	cmd := exec.Command(r.bin, "run", "-config", r.configPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	r.cmd = cmd
	r.running.Store(true)
	log.Printf("xray: started child pid=%d", cmd.Process.Pid)
	go func() {
		cmd.Wait()
		r.running.Store(false)
	}()
	// 短暂观察，尽早发现"启动即崩"（如配置实际不可用）。
	time.Sleep(500 * time.Millisecond)
	if !r.running.Load() {
		return fmt.Errorf("xray 启动后立即退出")
	}
	return nil
}

// IsRunning 实现 Runner。
func (r *ExecRunner) IsRunning(context.Context) bool {
	return r.running.Load()
}
