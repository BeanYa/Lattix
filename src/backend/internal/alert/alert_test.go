package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"lattix/backend/internal/store"
)

type echoURLRequester struct{}

func (echoURLRequester) PostJSON(_ context.Context, url string, _ any) error {
	return errors.New("request failed: " + url)
}

// capturedPost 记录一次 webhook/telegram POST（url + JSON body）。
type capturedPost struct {
	url  string
	body []byte
}

// captureRequester 是测试用发送端：记录每次 POST 而不发真实请求。
type captureRequester struct {
	mu    sync.Mutex
	posts []capturedPost
}

func (r *captureRequester) PostJSON(_ context.Context, url string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.posts = append(r.posts, capturedPost{url: url, body: raw})
	return nil
}

func (r *captureRequester) snapshot() []capturedPost {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capturedPost(nil), r.posts...)
}

// waitPosts 等待至少 want 条 POST（2s 超时）。
func (r *captureRequester) waitPosts(t *testing.T, want int) []capturedPost {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		posts := r.snapshot()
		if len(posts) >= want {
			return posts
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 %d 条告警发送，实际 %d 条", want, len(posts))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// newAlertTestNotifier 构造接入 in-memory store 与捕获发送端的 Notifier；
// debounce/mergeWindow 以环境变量注入（New 时读取）。
func newAlertTestNotifier(t *testing.T, debounce, mergeWindow string) (*Notifier, *captureRequester, *store.Store) {
	t.Helper()
	t.Setenv("LATTIX_ALERT_DEBOUNCE", debounce)
	t.Setenv("LATTIX_ALERT_MERGE_WINDOW", mergeWindow)
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetSetting(context.Background(), store.SettingAlertWebhookURL, "http://hooks.test/lattix"); err != nil {
		t.Fatal(err)
	}
	n := New(st)
	capture := &captureRequester{}
	n.requester = capture
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = n.Close(ctx)
	})
	return n, capture, st
}

func TestSendTelegramRedactsBotToken(t *testing.T) {
	n := &Notifier{requester: echoURLRequester{}}
	err := n.sendTelegram(context.Background(), "123456:secret-token", "chat", "test")
	if err == nil {
		t.Fatal("sendTelegram unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "123456:secret-token") {
		t.Fatalf("telegram token leaked in error: %s", err)
	}
}

// TestCategoryOf 校验事件→告警分类的登记表（§告警分类）：已知事件按登记返回，
// 未登记事件归运行警告。
func TestCategoryOf(t *testing.T) {
	cases := []struct {
		event    string
		category string
	}{
		{EventServerOffline, CategoryRuntime},
		{EventConfigDrift, CategoryRuntime},
		{EventNodeFailed, CategoryRuntime},
		{EventChainDegraded, CategoryRuntime},
		{EventServerBanSuspected, CategoryBan},
		{EventServerBanned, CategoryBan},
		{EventServerBanRecovered, CategoryBan},
		{EventBillingDueSoon, CategoryExpiry},
		{EventBillingDueToday, CategoryExpiry},
		{EventBillingExpired, CategoryExpiry},
		{"unknown_event", CategoryRuntime},
	}
	for _, c := range cases {
		if got := categoryOf(c.event); got != c.category {
			t.Fatalf("categoryOf(%q) = %q, want %q", c.event, got, c.category)
		}
	}
}

// TestPayloadTextCarriesCategory 校验 Telegram 文案带分类前缀，webhook 载荷带 category 字段。
func TestPayloadTextCarriesCategory(t *testing.T) {
	p := payload{Event: EventServerBanned, Category: categoryOf(EventServerBanned), Server: "hk-1", Time: "2026-10-28T00:00:00Z"}
	if text := p.text(); !strings.Contains(text, "[Lattix·Ban告警]") {
		t.Fatalf("telegram text missing ban category prefix: %q", text)
	}
	p = payload{Event: EventBillingDueSoon, Category: categoryOf(EventBillingDueSoon), Server: "hk-1", Time: "2026-10-28T00:00:00Z"}
	if text := p.text(); !strings.Contains(text, "[Lattix·到期预警]") {
		t.Fatalf("telegram text missing expiry category prefix: %q", text)
	}
	p = payload{Event: EventServerOffline, Category: categoryOf(EventServerOffline), Server: "hk-1", Time: "2026-10-28T00:00:00Z"}
	if text := p.text(); !strings.Contains(text, "[Lattix·运行警告]") {
		t.Fatalf("telegram text missing runtime category prefix: %q", text)
	}
}

// TestNotifyMergesSameCategoryIntoOneBatch 校验合并窗口内同分类多条告警只发一条
// webhook，载荷为 batch（event=batch + count + events）。
func TestNotifyMergesSameCategoryIntoOneBatch(t *testing.T) {
	n, capture, _ := newAlertTestNotifier(t, "0s", "50ms")
	n.Notify(1, EventServerOffline, "", "服务器 1 断开")
	n.Notify(2, EventServerOffline, "", "服务器 2 断开")
	capture.waitPosts(t, 1)
	// 窗口已过仍只有一条（合并而非逐条发送）。
	time.Sleep(120 * time.Millisecond)
	posts := capture.snapshot()
	if len(posts) != 1 {
		t.Fatalf("同分类告警应合并为 1 条，实际 %d 条", len(posts))
	}
	var batch batchPayload
	if err := json.Unmarshal(posts[0].body, &batch); err != nil {
		t.Fatalf("decode batch payload: %v", err)
	}
	if batch.Event != batchEvent || batch.Category != CategoryRuntime || batch.Count != 2 || len(batch.Events) != 2 {
		t.Fatalf("batch payload = %+v，期望 event=batch/category=runtime/count=2", batch)
	}
	if batch.Events[0].Server != "server_1" || batch.Events[1].Server != "server_2" {
		t.Fatalf("batch events = %+v，期望保留两条服务器", batch.Events)
	}
}

// TestNotifyKeepsCategoriesSeparate 校验不同分类各自一条，互不合并。
func TestNotifyKeepsCategoriesSeparate(t *testing.T) {
	n, capture, _ := newAlertTestNotifier(t, "0s", "40ms")
	n.Notify(1, EventServerOffline, "", "离线")
	n.Notify(1, EventServerBanned, "", "被 Ban")
	posts := capture.waitPosts(t, 2)
	categories := make(map[string]bool, 2)
	for _, post := range posts {
		var p payload
		if err := json.Unmarshal(post.body, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.Event == batchEvent {
			t.Fatalf("单条分类通知不应合并成 batch: %s", post.body)
		}
		categories[p.Category] = true
	}
	if !categories[CategoryRuntime] || !categories[CategoryBan] {
		t.Fatalf("分类通知 = %v，期望 runtime 与 ban 各一条", categories)
	}
}

// TestNotifySingleEventKeepsLegacyPayload 校验窗口内只有一条时保持原单事件载荷。
func TestNotifySingleEventKeepsLegacyPayload(t *testing.T) {
	n, capture, _ := newAlertTestNotifier(t, "0s", "40ms")
	n.Notify(7, EventBillingDueSoon, "", "7 天后到期")
	posts := capture.waitPosts(t, 1)
	var p payload
	if err := json.Unmarshal(posts[0].body, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.Event != EventBillingDueSoon || p.Category != CategoryExpiry || p.Server != "server_7" {
		t.Fatalf("单条 payload = %+v，期望保持原事件载荷", p)
	}
}

// TestNotifyWithoutMergeSendsEachEvent 校验合并窗口为 0 时退回逐条立即发送。
func TestNotifyWithoutMergeSendsEachEvent(t *testing.T) {
	n, capture, _ := newAlertTestNotifier(t, "0s", "0s")
	n.Notify(1, EventServerOffline, "", "a")
	n.Notify(2, EventServerOffline, "", "b")
	posts := capture.waitPosts(t, 2)
	for _, post := range posts {
		var p payload
		if err := json.Unmarshal(post.body, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.Event != EventServerOffline {
			t.Fatalf("关闭合并后应逐条发送，payload = %+v", p)
		}
	}
}

// TestNotifyDebouncesRepeatedEvent 校验合并前的同服务器同事件防抖动仍生效。
func TestNotifyDebouncesRepeatedEvent(t *testing.T) {
	n, capture, _ := newAlertTestNotifier(t, "1m", "30ms")
	n.Notify(1, EventServerOffline, "", "第一次")
	n.Notify(1, EventServerOffline, "", "第二次")
	capture.waitPosts(t, 1)
	time.Sleep(100 * time.Millisecond)
	if posts := capture.snapshot(); len(posts) != 1 {
		t.Fatalf("防抖动应只发 1 条，实际 %d 条", len(posts))
	}
}

// TestNotifyTelegramMergesCategory 校验 Telegram 通道按分类合并为一条摘要。
func TestNotifyTelegramMergesCategory(t *testing.T) {
	n, capture, st := newAlertTestNotifier(t, "0s", "40ms")
	ctx := context.Background()
	if err := st.SetSetting(ctx, store.SettingAlertTelegramBotToken, "123456:test-token"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, store.SettingAlertTelegramChatID, "-1001"); err != nil {
		t.Fatal(err)
	}
	n.Notify(1, EventServerBanSuspected, "", "连续 5 轮丢包，疑似被 Ban")
	n.Notify(2, EventServerBanned, "", "连续 10 轮丢包，判定 Banned")
	posts := capture.waitPosts(t, 2) // webhook + telegram 各一条
	var telegramText string
	for _, post := range posts {
		if !strings.HasPrefix(post.url, "https://api.telegram.org/") {
			continue
		}
		var body map[string]string
		if err := json.Unmarshal(post.body, &body); err != nil {
			t.Fatalf("decode telegram body: %v", err)
		}
		telegramText = body["text"]
	}
	if telegramText == "" {
		t.Fatal("未收到 Telegram 合并摘要")
	}
	for _, want := range []string{"[Lattix·Ban告警] 合并通知 2 条", "server_1", "server_2", EventServerBanSuspected, EventServerBanned} {
		if !strings.Contains(telegramText, want) {
			t.Fatalf("Telegram 摘要缺少 %q：%s", want, telegramText)
		}
	}
}

// TestTestSendsImmediatelyBypassesMerge 校验设置页测试消息不进入合并队列。
func TestTestSendsImmediatelyBypassesMerge(t *testing.T) {
	n, capture, _ := newAlertTestNotifier(t, "0s", "5s")
	res := n.Test(context.Background())
	if !res["webhook"].OK {
		t.Fatalf("测试发送失败: %+v", res["webhook"])
	}
	posts := capture.snapshot()
	if len(posts) != 1 {
		t.Fatalf("测试消息应立即发送 1 条，实际 %d 条", len(posts))
	}
	var p payload
	if err := json.Unmarshal(posts[0].body, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.Event != "test" {
		t.Fatalf("测试消息 payload = %+v，期望 event=test", p)
	}
}

// TestBatchTextTruncatesOversizedBatch 校验大批量摘要不超过 Telegram 上限且注明剩余条数。
func TestBatchTextTruncatesOversizedBatch(t *testing.T) {
	events := make([]payload, 200)
	for i := range events {
		events[i] = payload{
			Event:  EventServerOffline,
			Server: fmt.Sprintf("server_%d", i+1),
			Detail: strings.Repeat("很长很长的详情", 40),
			Time:   "2026-11-05T00:00:00Z",
		}
	}
	text := batchText(CategoryRuntime, events)
	if got := utf8.RuneCountInString(text); got > telegramTextLimit {
		t.Fatalf("合并摘要长度 %d 超过上限 %d", got, telegramTextLimit)
	}
	if !strings.Contains(text, "另有") {
		t.Fatalf("截断后应注明剩余条数：%s", text)
	}
}
