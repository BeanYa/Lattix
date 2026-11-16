package xray

import (
	"os"
	"testing"
)

// TestProcessInstanceIDForCurrentProcess 验证进程标识在当前平台可用且稳定
// （Linux 用 /proc，macOS/BSD 用 ps lstart）：空标识会让面板丢弃整帧流量快照。
func TestProcessInstanceIDForCurrentProcess(t *testing.T) {
	first := processInstanceID(os.Getpid())
	if first == "" {
		t.Fatal("processInstanceID 返回空串（面板将丢弃流量快照）")
	}
	if second := processInstanceID(os.Getpid()); second != first {
		t.Fatalf("同一进程标识应稳定: %q vs %q", first, second)
	}
	if processInstanceID(-1) != "" {
		t.Fatal("不存在的进程应返回空串")
	}
}
