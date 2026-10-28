package alert

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type echoURLRequester struct{}

func (echoURLRequester) PostJSON(_ context.Context, url string, _ any) error {
	return errors.New("request failed: " + url)
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
