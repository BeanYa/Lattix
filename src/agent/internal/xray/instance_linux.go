//go:build linux

package xray

import (
	"fmt"
	"os"
	"strings"
)

// processInstanceID 以 /proc/<pid>/stat 的 starttime 派生 Xray 进程标识（相对时刻，
// 同一进程稳定、重启后变化）；生产环境（Linux）用该实现。
func processInstanceID(pid int) string {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	// comm is parenthesized and may contain spaces. Fields after the closing
	// parenthesis start at proc field 3; starttime is field 22.
	close := strings.LastIndexByte(string(raw), ')')
	if close < 0 {
		return ""
	}
	fields := strings.Fields(string(raw)[close+1:])
	if len(fields) <= 19 {
		return ""
	}
	return fmt.Sprintf("%d:%s", pid, fields[19])
}
