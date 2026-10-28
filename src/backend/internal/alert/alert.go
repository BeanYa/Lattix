// Package alert 实现事件告警（设计文档 §19）：状态跃迁事件经 Webhook / Telegram Bot
// 双通道异步推送（单人运维离线通知）；三项设置全空 = 关闭。
package alert

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/store"
	external "lattix/shared/requester"
)

// 告警事件类型（webhook JSON 的 event 字段）。
const (
	EventServerOffline = "server_offline" // WS 断开导致 online→offline（仅跃迁，重连不重复）
	EventConfigDrift   = "config_drift"   // drift_report 上报漂移
	EventNodeFailed    = "node_failed"    // apply 失败 / 死信导致节点置 failed
	EventChainDegraded = "chain_degraded" // 链任一跳 server 离线导致 active→degraded（§21）

	// Ban 告警（§告警分类）：三网目录测速连续全丢包驱动，由 panel 的 ban-watch 巡检发出。
	EventServerBanSuspected = "server_ban_suspected" // 连续 5 轮三网 100% 丢包，疑似被 Ban
	EventServerBanned       = "server_banned"        // 连续 10 轮以上，判定已被 Ban
	EventServerBanRecovered = "server_ban_recovered" // 三网恢复在线，取消被 Ban 告警

	// 到期预警：billing.lifecycle 每日巡检按状态跃迁/预警窗口发出。
	EventBillingDueSoon  = "billing_due_soon"  // 续费日 7 天内（每日提醒直至到期或续费）
	EventBillingDueToday = "billing_due_today" // 续费日为今天
	EventBillingExpired  = "billing_expired"   // 续费日已过且服务器离线，标记已过期
)

// 告警分类（webhook JSON 的 category 字段；Telegram 文案前缀）。
const (
	CategoryRuntime = "runtime" // 运行警告：离线/漂移/节点失败/链路降级（未知事件默认归此类）
	CategoryExpiry  = "expiry"  // 到期预警：服务器计费到期相关
	CategoryBan     = "ban"     // Ban 告警：三网测速判定疑似/已被 Ban
)

// eventCategories 是事件→分类的登记表（新增事件须在此登记分类）。
var eventCategories = map[string]string{
	EventServerOffline:      CategoryRuntime,
	EventConfigDrift:        CategoryRuntime,
	EventNodeFailed:         CategoryRuntime,
	EventChainDegraded:      CategoryRuntime,
	EventServerBanSuspected: CategoryBan,
	EventServerBanned:       CategoryBan,
	EventServerBanRecovered: CategoryBan,
	EventBillingDueSoon:     CategoryExpiry,
	EventBillingDueToday:    CategoryExpiry,
	EventBillingExpired:     CategoryExpiry,
}

// categoryOf 返回事件的告警分类；未登记事件按运行警告处理。
func categoryOf(event string) string {
	if category, ok := eventCategories[event]; ok {
		return category
	}
	return CategoryRuntime
}

// categoryLabels 是分类的中文展示名（Telegram 文案前缀）。
var categoryLabels = map[string]string{
	CategoryRuntime: "运行警告",
	CategoryExpiry:  "到期预警",
	CategoryBan:     "Ban告警",
}

// debounceDefault 是默认防抖动窗口：同一服务器同一事件在该窗口内不重复发送。
const debounceDefault = 5 * time.Minute

// sendTimeout 是单通道发送超时；发送全程异步，失败仅记日志，不阻塞主路径。
const sendTimeout = 5 * time.Second

// Notifier 读取 settings 表配置并发送告警；内存 map 记上次发送时间做防抖动（重启清零可接受）。
type Notifier struct {
	st        *store.Store
	requester external.WebhookRequester
	debounce  time.Duration

	mu       sync.Mutex
	lastSent map[string]time.Time // key: "<serverID>|<event>"
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// New 创建 Notifier。LATTIX_ALERT_DEBOUNCE（Go duration）可覆盖防抖动窗口（dev/e2e 用）。
func New(st *store.Store) *Notifier {
	debounce := debounceDefault
	if v := os.Getenv("LATTIX_ALERT_DEBOUNCE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Printf("LATTIX_ALERT_DEBOUNCE invalid, using %s: %v", debounce, err)
		} else {
			debounce = d
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Notifier{
		st: st,
		// webhook 是管理员配置 URL，同样走 SSRF 拨号防护（拒绝解析到内网/保留段）。
		requester: external.ExternalWebhookRequester{
			Doer: external.NewExternalHTTPClient(sendTimeout),
		},
		debounce: debounce,
		lastSent: make(map[string]time.Time),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// config 是 settings 表中的告警配置快照。
type config struct {
	webhookURL string
	tgToken    string
	tgChatID   string
}

// enabled 报告告警是否开启：三项全空 = 关闭。
func (c config) enabled() bool {
	return c.webhookURL != "" || c.tgToken != "" || c.tgChatID != ""
}

func (n *Notifier) loadConfig(ctx context.Context) config {
	get := func(key string) string {
		v, err := n.st.GetSetting(ctx, key)
		if err != nil {
			return ""
		}
		return v
	}
	return config{
		webhookURL: get(store.SettingAlertWebhookURL),
		tgToken:    get(store.SettingAlertTelegramBotToken),
		tgChatID:   get(store.SettingAlertTelegramChatID),
	}
}

// payload 是 webhook 的 JSON 载荷。
type payload struct {
	Event    string `json:"event"`
	Category string `json:"category"` // runtime|expiry|ban（§告警分类）
	Server   string `json:"server"`
	Node     string `json:"node,omitempty"`
	Detail   string `json:"detail"`
	Time     string `json:"time"`
}

// Notify 上报一次状态跃迁事件：防抖动命中则丢弃；否则异步发送到各已配置通道。
// server/node 为空字符串表示该维度不适用。失败仅记日志，绝不阻塞调用方主路径。
func (n *Notifier) Notify(serverID int64, event, node, detail string) {
	ctx := n.ctx
	cfg := n.loadConfig(ctx)
	if !cfg.enabled() {
		return
	}
	if !n.allow(fmt.Sprintf("%d|%s", serverID, event)) {
		return
	}
	server := fmt.Sprintf("server_%d", serverID)
	if srv, err := n.st.ServerByID(ctx, serverID); err == nil && srv.Alias != "" {
		server = srv.Alias
	}
	p := payload{
		Event:    event,
		Category: categoryOf(event),
		Server:   server,
		Node:     node,
		Detail:   detail,
		Time:     time.Now().Format(time.RFC3339),
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.send(n.ctx, cfg, p)
	}()
}

// allow 实现防抖动：窗口内同 key 已发过则返回 false。
func (n *Notifier) allow(key string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if t, ok := n.lastSent[key]; ok && time.Since(t) < n.debounce {
		return false
	}
	n.lastSent[key] = time.Now()
	return true
}

// text 渲染 Telegram 纯文本消息（首行带告警分类前缀）。
func (p payload) text() string {
	label := categoryLabels[p.Category]
	if label == "" {
		label = p.Category
	}
	s := fmt.Sprintf("[Lattix·%s] %s\n服务器: %s", label, p.Event, p.Server)
	if p.Node != "" {
		s += "\n节点: " + p.Node
	}
	if p.Detail != "" {
		s += "\n详情: " + p.Detail
	}
	return s + "\n时间: " + p.Time
}

// send 同步发送到各已配置通道（webhook / telegram 各自独立判定），返回每通道错误。
func (n *Notifier) send(ctx context.Context, cfg config, p payload) map[string]error {
	errs := make(map[string]error)
	if cfg.webhookURL != "" {
		errs["webhook"] = n.sendWebhook(ctx, cfg.webhookURL, p)
	}
	if cfg.tgToken != "" && cfg.tgChatID != "" {
		errs["telegram"] = n.sendTelegram(ctx, cfg.tgToken, cfg.tgChatID, p.text())
	}
	for ch, err := range errs {
		if err != nil {
			log.Printf("alert: %s 发送 %s 失败: %v", ch, p.Event, err)
		}
	}
	return errs
}

// ChannelResult 是测试发送的单通道结果。
type ChannelResult struct {
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// Test 同步发送一条测试消息到各通道并返回结果（POST /api/settings/alerts/test）。
// 未配置的通道返回 configured=false；未启用 telegram（token/chat_id 不齐）同样视为未配置。
func (n *Notifier) Test(ctx context.Context) map[string]ChannelResult {
	cfg := n.loadConfig(ctx)
	p := payload{
		Event:  "test",
		Server: "lattix-panel",
		Detail: "告警通道测试消息",
		Time:   time.Now().Format(time.RFC3339),
	}
	res := map[string]ChannelResult{
		"webhook":  {Configured: cfg.webhookURL != ""},
		"telegram": {Configured: cfg.tgToken != "" && cfg.tgChatID != ""},
	}
	for ch, err := range n.send(ctx, cfg, p) {
		r := res[ch]
		r.OK = err == nil
		if err != nil {
			r.Error = err.Error()
		}
		res[ch] = r
	}
	return res
}

// sendWebhook POST JSON 载荷到 webhook 接收端；非 2xx 视为失败。
func (n *Notifier) sendWebhook(ctx context.Context, url string, p payload) error {
	return n.requester.PostJSON(ctx, url, p)
}

// sendTelegram 经 Bot API sendMessage 发送纯文本。
func (n *Notifier) sendTelegram(ctx context.Context, token, chatID, text string) error {
	err := n.requester.PostJSON(ctx,
		"https://api.telegram.org/bot"+token+"/sendMessage",
		map[string]string{"chat_id": chatID, "text": text})
	if err == nil {
		return nil
	}
	return &secretRedactedError{cause: err, secret: token}
}

type secretRedactedError struct {
	cause  error
	secret string
}

func (e *secretRedactedError) Error() string {
	if e.secret == "" {
		return e.cause.Error()
	}
	return strings.ReplaceAll(e.cause.Error(), e.secret, "[REDACTED]")
}

func (e *secretRedactedError) Unwrap() error { return e.cause }

// Close cancels in-flight alert delivery and waits within the caller's
// shutdown budget.
func (n *Notifier) Close(ctx context.Context) error {
	n.cancel()
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
