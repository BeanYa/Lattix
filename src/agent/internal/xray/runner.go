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
	// Stop 停止 xray（uninstall purge_xray 时调用，§5）。
	Stop(ctx context.Context) error
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

// Stop 实现 Runner。
func (r *SystemdRunner) Stop(ctx context.Context) error {
	return exec.CommandContext(ctx, "systemctl", "stop", r.unit).Run()
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
	// dev 联调善后：agent 自身重启后 r.cmd 为空，上一代 xray 孤儿仍在运行
	// （占用 API/业务端口，新实例会 bind 失败），按配置路径清场并等待退出。
	_ = exec.Command("pkill", "-9", "-f", "run -config "+r.configPath).Run()
	for i := 0; i < 20; i++ {
		if err := exec.Command("pgrep", "-f", "run -config "+r.configPath).Run(); err != nil {
			break // 无匹配进程（pgrep 退出码 1）
		}
		time.Sleep(50 * time.Millisecond)
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

// Stop 实现 Runner：杀掉 xray 子进程（未运行时为空操作）。
func (r *ExecRunner) Stop(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		r.cmd.Process.Kill()
		r.cmd.Wait()
		r.cmd = nil
	}
	return nil
}
