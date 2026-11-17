package panel

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

// TestProbeExternalServersRecordsLatency 验证外部直链节点的面板侧 TCP 探测：
// 监听端口可连通 → 记录延迟与时间戳；监听关闭 → 延迟为空但时间戳更新（显示「超时」）。
func TestProbeExternalServersRecordsLatency(t *testing.T) {
	ctx := context.Background()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	st, err := store.Open(t.TempDir() + "/probe.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	config := []byte(`{"protocol":"socks","address":"127.0.0.1","port":` + fmtPort(port) + `}`)
	id, err := st.CreateServer(ctx, store.ServerDraft{
		Alias: "ext", Address: "127.0.0.1", BootstrapToken: "tok",
		MachineType: store.MachineTypeNAT, Kind: store.ServerKindExternal,
		ExternalConfig: string(config),
	})
	if err != nil {
		t.Fatal(err)
	}
	api := &Server{st: st}
	done := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = listener.Close()
		close(done)
	}()
	api.probeExternalServers(ctx)
	<-done
	row, err := st.ServerByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.PanelProbedAt == nil {
		t.Fatal("探测时间戳未写入")
	}
	if row.PanelLatencyMS == nil {
		t.Fatal("监听可连通时应记录延迟")
	}
	// 监听关闭后再探测：延迟清空（超时），时间戳继续更新。
	before := *row.PanelProbedAt
	time.Sleep(5 * time.Millisecond)
	api.probeExternalServers(ctx)
	row, err = st.ServerByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.PanelLatencyMS != nil {
		t.Fatalf("监听关闭后延迟应为空: %v", *row.PanelLatencyMS)
	}
	if row.PanelProbedAt == nil || !row.PanelProbedAt.After(before) {
		t.Fatalf("探测时间戳应更新: %v -> %v", before, row.PanelProbedAt)
	}
}

func fmtPort(port int) string {
	return strconv.Itoa(port)
}
