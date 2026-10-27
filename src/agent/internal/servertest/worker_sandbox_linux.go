//go:build linux

package servertest

import (
	"os/exec"
	"syscall"
)

// workerNamespacesSupported 报告当前平台是否支持 PID/mount/IPC 命名空间隔离。
func workerNamespacesSupported() bool { return true }

// applyWorkerSandbox 为 worker 子进程配置沙箱：namespaced 时以 PID/mount/IPC
// 命名空间隔离（网络与宿主共享），并以 Pdeathsig 兜底回收。
func applyWorkerSandbox(cmd *exec.Cmd, namespaced bool) {
	if !namespaced {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWIPC,
		Pdeathsig:  syscall.SIGKILL,
	}
}
