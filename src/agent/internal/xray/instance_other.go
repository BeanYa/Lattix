//go:build !linux

package xray

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// processInstanceID 是 Linux 以外平台（macOS/BSD 开发环境）的进程标识回退：
// /proc 不可用，用 ps 的进程启动时刻（lstart）派生——同一进程稳定、重启后变化。
func processInstanceID(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	started := strings.TrimSpace(string(out))
	if started == "" {
		return ""
	}
	return fmt.Sprintf("%d:%s", pid, started)
}
