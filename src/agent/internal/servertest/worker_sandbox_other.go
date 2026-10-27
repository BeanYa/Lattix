//go:build !linux

package servertest

import "os/exec"

// workerNamespacesSupported 报告当前平台是否支持 PID/mount/IPC 命名空间隔离。
func workerNamespacesSupported() bool { return false }

// applyWorkerSandbox 在无命名空间支持的平台上不配置额外沙箱
// （RunWorker 侧直接以 degraded 状态走进程/临时目录/rlimit 隔离）。
func applyWorkerSandbox(cmd *exec.Cmd, namespaced bool) {}
