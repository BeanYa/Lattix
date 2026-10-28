package sub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// linkStatusFixture 建一条已发布的两跳链路 + 三网目录，供链路状态测试使用。
type linkStatusFixture struct {
	chainID    int64
	entryID    int64
	exitID     int64
	catalogKey func(carrier string) string
}

func ptrFloat(v float64) *float64 {
	return &v
}

func newLinkStatusFixture(t *testing.T, st *store.Store, name string) linkStatusFixture {
	t.Helper()
	ctx := context.Background()
	// 目录文档：广东省三网运营商省份节点（province 节点取 IPv4 端点）。
	catalog := `{"version":1,"provinces":[{"name":"广东","code":"GD","carriers":{` +
		`"telecom":{"ipv4":{"host":"ct.example.com","port":80}},` +
		`"unicom":{"ipv4":{"host":"cu.example.com","port":80}},` +
		`"mobile":{"ipv4":{"host":"cm.example.com","port":80}}}}]}`
	if err := st.SetSetting(ctx, store.SettingCDNNodeCatalog, catalog); err != nil {
		t.Fatal(err)
	}
	chainID, _ := newTestEndpointChain(t, st, name)
	hops, err := st.ChainHops(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	return linkStatusFixture{
		chainID: chainID,
		entryID: hops[0].ServerID,
		exitID:  hops[len(hops)-1].ServerID,
		catalogKey: func(carrier string) string {
			return "catalog:广东:*:" + carrier
		},
	}
}

func subStatusLinks(t *testing.T, server *Server, token string) SubLinkStatusResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/sub/"+token+"/status", nil)
	req.SetPathValue("token", token)
	server.HandleSubStatus(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status code=%d body=%s", rec.Code, rec.Body.String())
	}
	var status SubLinkStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestSubStatusLinkLatencyData(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fx := newLinkStatusFixture(t, st, "lat")
	// 三网目录延迟：联通目标超时（nil），电信/移动有值。
	now := time.Now()
	if err := st.SaveServerCatalogProbes(ctx, fx.entryID, []store.CatalogProbeSample{
		{Key: fx.catalogKey("telecom"), Kind: "tcp", LatencyMS: ptrFloat(23.5)},
		{Key: fx.catalogKey("unicom"), Kind: "tcp", LatencyMS: nil},
		{Key: fx.catalogKey("mobile"), Kind: "tcp", LatencyMS: ptrFloat(88)},
	}, now); err != nil {
		t.Fatal(err)
	}
	// 链路内部段（单段复用出口 realized 端口 20001）：tcp 与 icmp 并存，首选 tcp。
	if err := st.SaveServerLatencyProbes(ctx, fx.entryID, []store.ServerLatencySample{
		{ToServer: fx.exitID, Kind: "icmp", Port: 0, LatencyMS: ptrFloat(50)},
		{ToServer: fx.exitID, Kind: "tcp", Port: 20001, LatencyMS: ptrFloat(30)},
	}, now); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000cc", "lat-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userID, []int64{fx.chainID}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)

	status := subStatusLinks(t, server, "lat-token")
	if len(status.Links) != 1 {
		t.Fatalf("links=%+v, want exactly the assigned chain", status.Links)
	}
	link := status.Links[0]
	if link.Entry != "入口" {
		t.Fatalf("entry=%q, want 入口", link.Entry)
	}
	if len(link.Carriers) != 3 {
		t.Fatalf("carriers=%+v, want three catalog targets", link.Carriers)
	}
	labels := make([]string, 0, len(link.Carriers))
	for _, carrier := range link.Carriers {
		labels = append(labels, carrier.Label)
	}
	// 电信→联通→移动，与目录目标顺序一致。
	if !slices.Equal(labels, []string{"广东电信", "广东联通", "广东移动"}) {
		t.Fatalf("carrier labels=%v", labels)
	}
	if link.Carriers[0].LatencyMS == nil || *link.Carriers[0].LatencyMS != 23.5 {
		t.Fatalf("telecom latency=%v, want 23.5", link.Carriers[0].LatencyMS)
	}
	if link.Carriers[1].LatencyMS != nil {
		t.Fatalf("unicom latency=%v, want null (probe timeout)", link.Carriers[1].LatencyMS)
	}
	if link.Carriers[1].SampledAt == nil {
		t.Fatalf("unicom sampled_at missing, want set (sample exists)")
	}
	if link.InternalMS == nil || *link.InternalMS != 30 {
		t.Fatalf("internal_ms=%v, want tcp preferred sum 30", link.InternalMS)
	}
}

func TestSubStatusSingleHopDirectNode(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fx := newLinkStatusFixture(t, st, "direct")
	directID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "direct-srv", Address: "direct.example.com", BootstrapToken: "tok-direct-srv", MachineType: store.MachineTypeDirect})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(shared.VirtualConfig{Protocol: shared.ProtocolVLESS, Network: shared.NetworkTCP, Template: json.RawMessage(`{}`)})
	nodeID, err := st.InsertNode(ctx, "direct-node", directID, shared.ProtocolVLESS, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeActive(ctx, nodeID, []byte(`{"port":443}`)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := st.SaveServerCatalogProbes(ctx, directID, []store.CatalogProbeSample{
		{Key: fx.catalogKey("telecom"), Kind: "tcp", LatencyMS: ptrFloat(12)},
	}, now); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000dd", "direct-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, userID, []int64{nodeID}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)

	status := subStatusLinks(t, server, "direct-token")
	if len(status.Links) != 1 {
		t.Fatalf("links=%+v, want the direct node only", status.Links)
	}
	link := status.Links[0]
	if link.Entry != "服务器" {
		t.Fatalf("entry=%q, want 服务器 for single-hop node", link.Entry)
	}
	if link.InternalMS == nil || *link.InternalMS != 0 {
		t.Fatalf("internal_ms=%v, want 0 (no internal segments)", link.InternalMS)
	}
	if len(link.Carriers) != 3 || link.Carriers[0].LatencyMS == nil || *link.Carriers[0].LatencyMS != 12 {
		t.Fatalf("carriers=%+v, want telecom 12ms on the direct server", link.Carriers)
	}
}

func TestSubStatusCacheTTLAndInvalidation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fx := newLinkStatusFixture(t, st, "cache")
	now := time.Now()
	if err := st.SaveServerCatalogProbes(ctx, fx.entryID, []store.CatalogProbeSample{
		{Key: fx.catalogKey("telecom"), Kind: "tcp", LatencyMS: ptrFloat(20)},
	}, now); err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "member", "00000000-0000-0000-0000-0000000000ee", "cache-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userID, []int64{fx.chainID}); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, nil)

	if got := *subStatusLinks(t, server, "cache-token").Links[0].Carriers[0].LatencyMS; got != 20 {
		t.Fatalf("first read telecom=%v, want 20", got)
	}
	// TTL 内探针表变化对读请求不可见（缓存生效）。
	if err := st.SaveServerCatalogProbes(ctx, fx.entryID, []store.CatalogProbeSample{
		{Key: fx.catalogKey("telecom"), Kind: "tcp", LatencyMS: ptrFloat(99)},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := *subStatusLinks(t, server, "cache-token").Links[0].Carriers[0].LatencyMS; got != 20 {
		t.Fatalf("cached read telecom=%v, want stale 20", got)
	}
	// 订阅文件快照更新路径（发布成功后失效）触发下一次读取重建。
	server.invalidateLinkStatus()
	if got := *subStatusLinks(t, server, "cache-token").Links[0].Carriers[0].LatencyMS; got != 99 {
		t.Fatalf("post-invalidation read telecom=%v, want refreshed 99", got)
	}
}
