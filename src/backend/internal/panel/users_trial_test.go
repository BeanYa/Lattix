package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
	"lattix/shared"
)

// newSweepServer 构造带订阅重发布与 dispatcher 的最小 Server（lifecycle sweep 测试共用）。
func newSweepServer(t *testing.T) (*Server, *store.Store, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(":memory:")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	subSrv := sub.New(st, nil, nil)
	subSrv.StartRegenerator(ctx)
	requester := &chainEditRequester{online: map[int64]bool{}}
	server := &Server{
		st:            st,
		disp:          dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}),
		req:           requester,
		subscriptions: subSrv,
	}
	return server, st, cancel
}

// TestCreateTrialUserWithDaysAndGroup 试用创建主链路（§9）：is_trial + duration_days
// 自生效时刻（缺省=立即）起算精确到秒 + 直选用户分组 + 试用流量入库。
func TestCreateTrialUserWithDaysAndGroup(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	groupID, err := st.CreateUserGroup(ctx, "trial-group", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: nil}

	body := `{"name":"t1","is_trial":true,"duration_days":3,"user_group_id":` +
		strconv.FormatInt(groupID, 10) + `,"traffic_limit":1073741824}`
	rec := httptest.NewRecorder()
	before := time.Now()
	server.handleCreateUser(rec, httptest.NewRequest(http.MethodPost, "/api/user/create", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code string `json:"code"`
		Data struct {
			ID           int64      `json:"id"`
			IsTrial      bool       `json:"is_trial"`
			EffectiveAt  *time.Time `json:"effective_at"`
			ExpiresAt    *time.Time `json:"expires_at"`
			TrafficLimit int64      `json:"traffic_limit"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != shared.CodeOK {
		t.Fatalf("code = %q, body = %s", resp.Code, rec.Body.String())
	}
	got := resp.Data
	if !got.IsTrial {
		t.Fatal("is_trial 应为 true")
	}
	if got.EffectiveAt != nil {
		t.Fatalf("省略 effective_at 应为立即生效（null），got %v", got.EffectiveAt)
	}
	if got.ExpiresAt == nil {
		t.Fatal("duration_days=3 应折算出 expires_at")
	}
	// 3 天 = 72h；到期时刻以 Unix 秒存储（亚秒截断），允许创建耗时 10s 内的误差。
	want := before.Add(72 * time.Hour)
	if got.ExpiresAt.After(want) || want.Sub(*got.ExpiresAt) > 10*time.Second {
		t.Fatalf("expires_at = %v, want ~%v（生效时刻起 +72h）", *got.ExpiresAt, want)
	}
	if got.TrafficLimit != 1073741824 {
		t.Fatalf("traffic_limit = %d", got.TrafficLimit)
	}
	ids, err := st.UserGroupIDsForUser(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != groupID {
		t.Fatalf("用户应直接加入分组 %d，got %v", groupID, ids)
	}
}

// TestCreateTrialUserNotYetEffective 生效时刻在未来的用户为未生效态：
// 预选节点照常入库，但不立即扇出 add_user（到点由 sweeper 激活，§9）。
func TestCreateTrialUserNotYetEffective(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.InsertNode(ctx, "n1", serverID, shared.ProtocolVLESS, nil, json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{st: st, subscriptions: nil}

	effective := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	body := `{"name":"later","is_trial":true,"duration_days":1,"effective_at":"` + effective + `","node_ids":[` + strconv.FormatInt(nodeID, 10) + `]}`
	rec := httptest.NewRecorder()
	server.handleCreateUser(rec, httptest.NewRequest(http.MethodPost, "/api/user/create", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID          int64      `json:"id"`
			EffectiveAt *time.Time `json:"effective_at"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.EffectiveAt == nil {
		t.Fatal("effective_at 应写入")
	}
	// 未生效用户不立即上线：无 add_user 命令（任意状态）。
	recent, err := st.RecentCommands(ctx, serverID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range recent {
		if cmd.Type == shared.TypeAddUser {
			t.Fatalf("未生效用户不应扇出 add_user，got %s (%s)", cmd.Type, cmd.Status)
		}
	}
	// 分配已入库：生效时刻越过后 sweeper 激活可补扇出。
	nodeIDs, err := st.UserNodeIDs(ctx, resp.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodeIDs) != 1 || nodeIDs[0] != nodeID {
		t.Fatalf("节点分配应入库，got %v", nodeIDs)
	}
}

// TestCreateTrialUserValidation 试用创建的参数校验。
func TestCreateTrialUserValidation(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{st: st}

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	sooner := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
	for name, body := range map[string]string{
		"试用须有流量或有效期": `{"name":"x","is_trial":true}`,
		"天数与到期时间二选一": `{"name":"x","is_trial":true,"duration_days":3,"expires_at":"` + future + `"}`,
		"到期须晚于生效":    `{"name":"x","is_trial":true,"effective_at":"` + future + `","expires_at":"` + sooner + `"}`,
		"天数不能为负":     `{"name":"x","is_trial":true,"duration_days":-1}`,
		"分组须存在":      `{"name":"x","user_group_id":999}`,
	} {
		rec := httptest.NewRecorder()
		server.handleCreateUser(rec, httptest.NewRequest(http.MethodPost, "/api/user/create", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", name, rec.Code, rec.Body.String())
		}
		var resp struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.Code != shared.CodeInvalidArgument {
			t.Fatalf("%s: code = %q, want %q", name, resp.Code, shared.CodeInvalidArgument)
		}
	}
}

// TestSubSettingsUpdateEffectiveAt 订阅设置支持改生效时刻：推迟到未来即转未生效态并扇出
// 摘除；设回立即恢复（停权态跃迁语义，§9）。
func TestSubSettingsUpdateEffectiveAt(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.InsertNode(ctx, "n1", serverID, shared.ProtocolVLESS, nil, json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "u", "u-uuid", "u-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, userID, []int64{nodeID}); err != nil {
		t.Fatal(err)
	}
	requester := &chainEditRequester{online: map[int64]bool{serverID: false}}
	server := &Server{st: st, disp: dispatch.New(st, requester, dispatch.Options{}, dispatch.Events{}), req: requester, subscriptions: nil}

	post := func(payload string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost,
			"/api/user/sub-settings", strings.NewReader(payload)))
		return rec
	}

	// 立即生效用户保持生效：无扇出跃迁。
	rec := post(`{"user_id":` + strconv.FormatInt(userID, 10) + `,"traffic_limit":0,"traffic_reset_day":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("sub-settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	recent, err := st.RecentCommands(ctx, serverID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 0 {
		t.Fatalf("未跃迁不应扇出，got %+v", recent)
	}

	// 推迟生效时刻到未来：转未生效 → 已在线用户需摘除（remove_user 扇出）。
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	rec = post(`{"user_id":` + strconv.FormatInt(userID, 10) + `,"traffic_limit":0,"traffic_reset_day":0,"effective_at":"` + future + `"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("sub-settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	u, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !u.NotYetEffective(time.Now()) {
		t.Fatal("推迟后应判定为未生效")
	}
	recent, err = st.RecentCommands(ctx, serverID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) == 0 {
		t.Fatal("推迟生效时刻应扇出 remove_user（有效停权态跃迁）")
	}
	for _, cmd := range recent {
		if cmd.Type != shared.TypeRemoveUser {
			t.Fatalf("期望 remove_user，got %s (%s)", cmd.Type, cmd.Status)
		}
	}

	// 设回立即生效（null）：恢复 → add_user 补回。
	rec = post(`{"user_id":` + strconv.FormatInt(userID, 10) + `,"traffic_limit":0,"traffic_reset_day":0,"effective_at":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("sub-settings status = %d, body = %s", rec.Code, rec.Body.String())
	}
	u, err = st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.EffectiveAt != nil || u.StartedAt == nil {
		t.Fatalf("null 生效应恢复立即生效，got eff=%v started=%v", u.EffectiveAt, u.StartedAt)
	}
}

// TestLifecycleSweepActivatesScheduledUser sweeper 激活路径（§9）：
// 生效时刻越过后 started_at 置位并重发布订阅；未到时刻不动作。
func TestLifecycleSweepActivatesScheduledUser(t *testing.T) {
	server, st, cancel := newSweepServer(t)
	defer cancel()
	defer st.Close()

	effective := time.Now().Add(time.Hour)
	userID, err := st.InsertUserFull(context.Background(), store.User{
		Name: "trial", UUID: "u-uuid", SubToken: "u-token", EffectiveAt: &effective, IsTrial: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	server.sweepActivations(context.Background(), now)
	u, err := st.UserByID(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.StartedAt != nil {
		t.Fatal("生效时刻未到 sweeper 不应激活")
	}

	// 时刻越过：激活（标记置位 + 重发布订阅文件生成）。
	ctx := context.Background()
	if _, err := st.PublishedSubscriptionFile(ctx, userID, "links"); err == nil {
		t.Fatal("激活前不应有已发布订阅")
	}
	server.sweepActivations(ctx, now.Add(2*time.Hour))
	u, err = st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if u.StartedAt == nil {
		t.Fatal("生效时刻越过后应置位 started_at")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := st.PublishedSubscriptionFile(ctx, userID, "links"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("激活后订阅未重发布")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestLifecycleSweepQuotaStopsUser 流量配额强制执行（§9）：试用与普通用户一致，
// 用量达到 traffic_limit 即置 quota_exhausted 并重发布（节点清空）；未设配额的用户不受影响。
func TestLifecycleSweepQuotaStopsUser(t *testing.T) {
	server, st, cancel := newSweepServer(t)
	defer cancel()
	defer st.Close()

	ctx := context.Background()
	trialID, err := st.InsertUserFull(ctx, store.User{Name: "trial", UUID: "t-uuid", SubToken: "t-token", IsTrial: true})
	if err != nil {
		t.Fatal(err)
	}
	plainID, err := st.InsertUser(ctx, "plain", "p-uuid", "p-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	limitedID, err := st.InsertUser(ctx, "limited", "l-uuid", "l-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSubSettings(ctx, trialID, store.UserSubSettings{TrafficLimit: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSubSettings(ctx, limitedID, store.UserSubSettings{TrafficLimit: 100}); err != nil {
		t.Fatal(err)
	}
	for _, uuid := range []string{"t-uuid", "p-uuid", "l-uuid"} {
		if err := st.AddTraffic(ctx, 0, uuid, 60, 40); err != nil {
			t.Fatal(err)
		}
	}

	server.sweepQuotaExhaustions(ctx, time.Now())

	for _, tc := range []struct {
		id      int64
		name    string
		stopped bool
	}{
		{trialID, "试用用户", true},
		{limitedID, "设配额的普通用户", true},
	} {
		u, err := st.UserByID(ctx, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if u.QuotaExhausted != tc.stopped {
			t.Fatalf("%s quota_exhausted = %v, want %v", tc.name, u.QuotaExhausted, tc.stopped)
		}
		if u.Expired {
			t.Fatalf("%s 不应置 expired（到期与配额停权正交）", tc.name)
		}
	}
	plain, err := st.UserByID(ctx, plainID)
	if err != nil {
		t.Fatal(err)
	}
	if plain.QuotaExhausted || plain.Expired {
		t.Fatal("未设配额的普通用户不应被流量配额停权")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		file, err := st.PublishedSubscriptionFile(ctx, trialID, "links")
		if err == nil {
			if strings.TrimSpace(string(file.Content)) != "" {
				t.Fatalf("停权后订阅仍含内容: %q", file.Content)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("配额停权后订阅未重发布")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTrafficCycleResetRestoresQuotaStoppedUser 周期流量重置自动恢复（§9）：
// 配额停权用户在重置边界（traffic_reset_day）后配额清零 → 自动解除配额停权、
// 扇出 add_user 并重发布订阅；到期/停用等多重停权仅解除配额侧。
func TestTrafficCycleResetRestoresQuotaStoppedUser(t *testing.T) {
	server, st, cancel := newSweepServer(t)
	defer cancel()
	defer st.Close()
	ctx := context.Background()

	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.InsertNode(ctx, "n1", serverID, shared.ProtocolVLESS, nil, json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertUser(ctx, "u", "q-uuid", "q-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSubSettings(ctx, id, store.UserSubSettings{TrafficLimit: 100}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserNodes(ctx, id, []int64{nodeID}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddTraffic(ctx, 0, "q-uuid", 60, 40); err != nil {
		t.Fatal(err)
	}
	server.sweepQuotaExhaustions(ctx, time.Now())
	if code := serveSubContent(t, server.subscriptions, "q-token", "links"); code != http.StatusForbidden {
		t.Fatalf("配额停权后订阅状态码 = %d, want 403", code)
	}

	// 周期重置：AddTraffic 行 period_start 为空 → 立即到期重置边界。
	if err := server.sweepTrafficReset(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if u.QuotaExhausted {
		t.Fatal("周期重置后应解除配额停权")
	}
	traffic, err := st.UserTraffic(ctx, "q-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if traffic.Up+traffic.Down != 0 {
		t.Fatalf("周期重置应清零当期流量，got %d", traffic.Up+traffic.Down)
	}
	found := false
	cmds, err := st.RecentCommands(ctx, serverID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range cmds {
		if cmd.Type == shared.TypeAddUser {
			found = true
		}
	}
	if !found {
		t.Fatal("周期重置恢复应扇出 add_user")
	}
	waitFor(t, func() bool {
		return serveSubContent(t, server.subscriptions, "q-token", "links") == http.StatusOK
	}, "周期重置恢复后订阅应恢复 200")
}

// serveSubContent 以指定 format 请求订阅内容，返回状态码（复用 Server 的订阅服务）。
func serveSubContent(t *testing.T, srv *sub.Server, token, format string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/sub/"+token, nil)
	req.SetPathValue("token", token)
	req.URL.RawQuery = "format=" + format
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Code
}

// waitFor 条件轮询（订阅重发布为异步，50ms 步进、5s 上限）。
func waitFor(t *testing.T, cond func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestReactivateExpiredUserRestoresService 重新激活闭环校验（§9）：
// 到期用户 → 订阅 403、节点摘除 → 管理员重设流量与有效期（沿用原配额改期）
// → expired 复位、add_user 扇出、订阅重发布且同一 sub_token 恢复 200（客户端无感续用）；
// 试用用户重新激活流量清零重计，普通用户流量保留。
func TestReactivateExpiredUserRestoresService(t *testing.T) {
	server, st, cancel := newSweepServer(t)
	defer cancel()
	defer st.Close()
	ctx := context.Background()

	serverID, err := st.CreateServer(ctx, store.ServerDraft{Alias: "exit", Address: "exit.test", BootstrapToken: "token", MachineType: store.MachineTypeDirect, CountryCode: "US"})
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := st.InsertNode(ctx, "n1", serverID, shared.ProtocolVLESS, nil, json.RawMessage(`{"protocol":"vless","template":{}}`))
	if err != nil {
		t.Fatal(err)
	}

	newUser := func(name, uuid, token string, isTrial bool, limit int64) int64 {
		id, err := st.InsertUserFull(ctx, store.User{Name: name, UUID: uuid, SubToken: token, IsTrial: isTrial})
		if err != nil {
			t.Fatal(err)
		}
		if limit > 0 {
			if err := st.SetUserSubSettings(ctx, id, store.UserSubSettings{TrafficLimit: limit}); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := st.SetUserNodes(ctx, id, []int64{nodeID}); err != nil {
			t.Fatal(err)
		}
		if err := st.AddTraffic(ctx, 0, uuid, 600_000, 500_000); err != nil {
			t.Fatal(err)
		}
		return id
	}

	reactivate := func(id int64, trafficLimit int64) {
		u, err := st.UserByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		payload := map[string]any{
			"user_id":           id,
			"traffic_limit":     trafficLimit,
			"traffic_reset_day": u.TrafficResetDay,
			"sub_title":         "",
			"sub_announcement":  "",
			"plan_name":         "",
			"app_url":           "",
			"expires_at":        time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		}
		if u.EffectiveAt != nil {
			payload["effective_at"] = u.EffectiveAt.UTC().Format(time.RFC3339)
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		server.handleUpdateUserSubSettings(rec, httptest.NewRequest(http.MethodPost,
			"/api/user/sub-settings", bytes.NewReader(raw)))
		if rec.Code != http.StatusOK {
			t.Fatalf("reactivate status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}

	resetTraffic := func(id int64) {
		rec := httptest.NewRecorder()
		server.handleResetUserTraffic(rec, httptest.NewRequest(http.MethodPost,
			"/api/user/reset-traffic", strings.NewReader(`{"user_id":`+strconv.FormatInt(id, 10)+`}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("reset-traffic status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}

	addUserCmds := func() int {
		cmds, err := st.RecentCommands(ctx, serverID, 200)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, cmd := range cmds {
			if cmd.Type == shared.TypeAddUser {
				n++
			}
		}
		return n
	}

	// 场景 A：试用用户配额停权 → 手动流量重置 → 恢复 + 流量清零。
	trialQuotaID := newUser("trial-quota", "rq-uuid", "rq-token", true, 1_000_000)
	server.sweepQuotaExhaustions(ctx, time.Now())
	if code := serveSubContent(t, server.subscriptions, "rq-token", "links"); code != http.StatusForbidden {
		t.Fatalf("配额停权后订阅状态码 = %d, want 403", code)
	}
	if before := addUserCmds(); before != 0 {
		t.Fatalf("停权期间不应有 add_user，got %d", before)
	}
	resetTraffic(trialQuotaID)
	u, err := st.UserByID(ctx, trialQuotaID)
	if err != nil {
		t.Fatal(err)
	}
	if u.QuotaExhausted {
		t.Fatal("流量重置后应解除配额停权")
	}
	traffic, err := st.UserTraffic(ctx, "rq-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if traffic.Up+traffic.Down != 0 {
		t.Fatalf("流量重置应清零当期用量，got %d", traffic.Up+traffic.Down)
	}
	if got := addUserCmds(); got != 1 {
		t.Fatalf("流量重置恢复应扇出 1 次 add_user，got %d", got)
	}
	waitFor(t, func() bool {
		return serveSubContent(t, server.subscriptions, "rq-token", "links") == http.StatusOK
	}, "流量重置后订阅应恢复 200")

	// 场景 B：试用用户到期 → 重新激活（sub-settings 改有效期）→ 恢复 + 流量清零。
	trialID := newUser("trial-expired", "r-uuid", "r-token", true, 1_000_000)
	if err := st.SetUserExpired(ctx, trialID, true); err != nil {
		t.Fatal(err)
	}
	if code := serveSubContent(t, server.subscriptions, "r-token", "links"); code != http.StatusForbidden {
		t.Fatalf("到期后订阅状态码 = %d, want 403", code)
	}
	reactivate(trialID, 1_000_000)
	u, err = st.UserByID(ctx, trialID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Expired || u.QuotaExhausted {
		t.Fatalf("重新激活后停权标记应复位，expired=%v quota=%v", u.Expired, u.QuotaExhausted)
	}
	traffic, err = st.UserTraffic(ctx, "r-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if traffic.Up+traffic.Down != 0 {
		t.Fatalf("试用用户重新激活应清零流量，got %d", traffic.Up+traffic.Down)
	}
	waitFor(t, func() bool {
		return serveSubContent(t, server.subscriptions, "r-token", "links") == http.StatusOK
	}, "重新激活后订阅应恢复 200")

	// 场景 C：普通用户到期 → 重新激活 → 恢复但流量保留。
	plainID := newUser("plain", "rp-uuid", "rp-token", false, 0)
	if err := st.SetUserExpired(ctx, plainID, true); err != nil {
		t.Fatal(err)
	}
	reactivate(plainID, 0)
	traffic, err = st.UserTraffic(ctx, "rp-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if traffic.Up+traffic.Down != 1_100_000 {
		t.Fatalf("普通用户重新激活不应清零流量，got %d", traffic.Up+traffic.Down)
	}
	waitFor(t, func() bool {
		return serveSubContent(t, server.subscriptions, "rp-token", "links") == http.StatusOK
	}, "普通用户重新激活后订阅应恢复 200")

	// 场景 D：未停权用户手动重置流量 → 仅清零用量，不扇出。
	plain2ID := newUser("plain2", "r2-uuid", "r2-token", false, 0)
	before := addUserCmds()
	resetTraffic(plain2ID)
	traffic, err = st.UserTraffic(ctx, "r2-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if traffic.Up+traffic.Down != 0 {
		t.Fatalf("未停权用户重置应清零用量，got %d", traffic.Up+traffic.Down)
	}
	if got := addUserCmds(); got != before {
		t.Fatalf("未停权用户重置不应扇出 add_user，before=%d got=%d", before, got)
	}
}
