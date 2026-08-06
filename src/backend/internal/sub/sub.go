// Package sub 实现订阅端点（设计文档 §9）：
// GET /sub/{sub_token} → 按 UA / ?format= 返回多格式订阅内容；浏览器→ SPA 落地页。
package sub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/progress"
	"lattix/backend/internal/store"
	"lattix/shared"
	external "lattix/shared/requester"
)

// Server 实现订阅端点。
type Server struct {
	st            *store.Store
	base          func(*http.Request) string // 面板对外地址（落地页绝对链接，同 panel.PanelBase 判定链）
	spaHTML       []byte                     // 内嵌前端 index.html（浏览器访问时返回 SPA 壳）
	files         external.FileRequester
	downloadFiles external.FileRequester
	cacheDir      string
	refresh       sync.Mutex
	publish       sync.Mutex

	queueMu   sync.Mutex
	queued    map[int64]string
	queueWake chan struct{}
	queueWG   sync.WaitGroup
	startOnce sync.Once
	baseMu    sync.RWMutex
	lastBase  string

	downloadMu      sync.Mutex
	downloadTasks   map[string]*clientDownloadTask
	activeDownloads map[string]string

	observer *progress.Registry // 旁路观察（nil = 关闭，发布循环零开销）
}

// SetObserver 注入旁路观察注册表（panel 初始化时调用；可多次调用，最后者生效）。
func (s *Server) SetObserver(r *progress.Registry) {
	s.observer = r
}

// New 创建订阅服务；base 返回请求对应的面板对外地址（可为 nil，落地页退回请求推断）。
// spaHTML 为前端构建产物的 index.html 内容（浏览器访问时返回 SPA 壳）。
func New(st *store.Store, base func(*http.Request) string, spaHTML []byte) *Server {
	return NewWithCacheDir(st, base, spaHTML, "")
}

// NewWithCacheDir creates the subscription service and stores downloaded client
// packages under cacheDir. An empty directory disables package persistence,
// which is useful for tests and embedded callers.
func NewWithCacheDir(st *store.Store, base func(*http.Request) string, spaHTML []byte, cacheDir string) *Server {
	if base == nil {
		base = func(r *http.Request) string {
			scheme := "http"
			if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
				scheme = "https"
			}
			return fmt.Sprintf("%s://%s", scheme, r.Host)
		}
	}
	server := &Server{
		st: st, base: base, spaHTML: spaHTML, cacheDir: cacheDir,
		files:         external.ExternalFileRequester{Doer: &http.Client{Timeout: 30 * time.Second}},
		downloadFiles: external.ExternalFileRequester{Doer: &http.Client{Timeout: 30 * time.Minute}},
		queued:        make(map[int64]string), queueWake: make(chan struct{}, 1),
		downloadTasks: make(map[string]*clientDownloadTask), activeDownloads: make(map[string]string),
	}
	server.ensureBuiltInTemplateSources(context.Background())
	return server
}

// setSubHeaders 写订阅通用响应头（§9）：
// subscription-userinfo（upload/download/total/expire/reset_day/plan_name/app_url）与 profile-update-interval。
func (s *Server) setSubHeaders(w http.ResponseWriter, r *http.Request, user *store.User) {
	t, err := s.st.UserTraffic(r.Context(), user.UUID)
	if err != nil {
		t = store.TrafficTotals{} // 统计查询失败不阻断订阅
	}
	merged := s.mergedUserTraffic(r.Context(), user, t)
	v := fmt.Sprintf("upload=%d; download=%d", merged.Upload, merged.Download)
	if merged.Total > 0 {
		v += fmt.Sprintf("; total=%d", merged.Total)
		v += fmt.Sprintf("; reset_day=%d", daysUntilReset(user, time.Now()))
	}
	if merged.Expire != nil {
		v += fmt.Sprintf("; expire=%d", *merged.Expire)
	}
	// 套餐名：用户级 > 全局设置。
	if planName := s.effectivePlanName(r, user); planName != "" {
		v += "; plan_name=" + planName
	}
	// 客户端跳转链接：用户级 > 全局设置。
	appURL := user.AppURL
	if appURL == "" {
		appURL, _ = s.st.GetSetting(r.Context(), store.SettingSubAppURL)
	}
	if appURL != "" {
		v += "; app_url=" + appURL
	}
	w.Header().Set("Subscription-Userinfo", v)
	// 更新间隔：优先用户级，否则全局设置，默认 24h。
	interval := "24"
	if global, _ := s.st.GetSetting(r.Context(), store.SettingSubUpdateInterval); global != "" {
		interval = global
	}
	w.Header().Set("Profile-Update-Interval", interval)
}

// mergedUserTraffic 合并面板实时流量与用户引入的外部订阅流量（叠加/并入）。
func (s *Server) mergedUserTraffic(ctx context.Context, user *store.User, t store.TrafficTotals) extsub.Traffic {
	var panelExpire *int64
	if user.ExpiresAt != nil {
		v := user.ExpiresAt.Unix()
		panelExpire = &v
	}
	attached, err := s.st.EffectiveUserExternalSubscriptions(ctx, user.ID)
	if err != nil {
		attached = nil
	}
	return extsub.MergeUserTraffic(extsub.Traffic{
		Upload: t.Up, Download: t.Down, Total: user.TrafficLimit, Expire: panelExpire,
	}, attached)
}

// effectivePlanName 返回生效套餐名：用户级 > 全局设置。
func (s *Server) effectivePlanName(r *http.Request, user *store.User) string {
	if user.PlanName != "" {
		return user.PlanName
	}
	planName, _ := s.st.GetSetting(r.Context(), store.SettingSubPlanName)
	return planName
}

// subContentDisposition 生成订阅下载文件名（Content-Disposition 头），形如
// {套餐名|Lattix}：Clash Party 等客户端按该文件名命名保存订阅，从而在界面上
// 展示套餐别名；不附加用户名与格式扩展名，保证客户端显示的订阅名与套餐名一致。
func (s *Server) subContentDisposition(r *http.Request, user *store.User) string {
	label := sanitizeSubName(s.effectivePlanName(r, user))
	if label == "" {
		label = "Lattix"
	}
	plain := strings.ReplaceAll(asciiOnly(label), `"`, "")
	v := "attachment; filename*=UTF-8''" + rfc5987Encode(label)
	if plain != "" {
		v = `attachment; filename="` + plain + `"; filename*=UTF-8''` + rfc5987Encode(label)
	}
	return v
}

// sanitizeSubName 清洗订阅显示名：移除控制字符（防响应头注入），并按字符截断。
func sanitizeSubName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	runes := []rune(s)
	const maxNameRunes = 40
	if len(runes) > maxNameRunes {
		s = string(runes[:maxNameRunes])
	}
	return s
}

// asciiOnly 保留 ASCII 字符（Content-Disposition filename= 参数的兼容回退值）。
func asciiOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// rfc5987Encode 按 RFC 5987 对 header 参数值做百分号编码（`filename*=UTF-8%27%27...`），
// 支持中文等非 ASCII 字符；Clash Party 等客户端用 decodeURIComponent 解码。
func rfc5987Encode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

// daysUntilReset 计算距下次流量重置的天数（与 sweeper 重置语义一致：
// reset_day=0 取创建日，月份缺少配置日期时取月末；当天已过重置时刻则计入下月）。
func daysUntilReset(user *store.User, now time.Time) int {
	next := user.TrafficResetAt(now.Year(), now.Month(), now.Location())
	if !next.After(now) {
		year, month := now.Year(), now.Month()+1
		if month > time.December {
			year++
			month = time.January
		}
		next = user.TrafficResetAt(year, month, now.Location())
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return int(next.Sub(today).Hours() / 24)
}

type clashRealityOpts struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id,omitempty"`
}

type clashGrpcOpts struct {
	ServiceName string `yaml:"grpc-service-name"`
	GrpcMode    string `yaml:"grpc-mode,omitempty"`
}

type clashXHTTPOpts struct {
	Path string `yaml:"path,omitempty"`
	Mode string `yaml:"mode,omitempty"`
	Host string `yaml:"host,omitempty"`
}

type clashH2Opts struct {
	Path string   `yaml:"path,omitempty"`
	Host []string `yaml:"host,omitempty"`
}

// clashProxy 是 mihomo 代理项并集结构：覆盖完整 proxy schema，
// 按协议填充相关字段（omitempty 裁剪）；Raw 透传未被类型化字段消费的
// Extra 键（yaml:",inline" 直接并入生成的 YAML 映射）。
type clashProxy struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Server string `yaml:"server"`
	Port   int    `yaml:"port"`

	UUID           string   `yaml:"uuid,omitempty"`     // vless / vmess
	AlterID        *int     `yaml:"alterId,omitempty"`  // vmess
	Cipher         string   `yaml:"cipher,omitempty"`   // vmess=auto / ss=method
	Password       string   `yaml:"password,omitempty"` // trojan / ss / socks / http / anytls
	Username       string   `yaml:"username,omitempty"` // socks / http
	Network        string   `yaml:"network,omitempty"`
	PacketEncoding string   `yaml:"packet-encoding,omitempty"` // vless
	TLS            bool     `yaml:"tls,omitempty"`
	Servername     string   `yaml:"servername,omitempty"` // vless / vmess
	SNI            string   `yaml:"sni,omitempty"`        // trojan / anytls / hysteria2 / tuic / http(tls)
	Flow           string   `yaml:"flow,omitempty"`       // vless
	Encryption     string   `yaml:"encryption,omitempty"` // vless
	ALPN           []string `yaml:"alpn,omitempty"`
	UDP            bool     `yaml:"udp"`

	RealityOpts       *clashRealityOpts `yaml:"reality-opts,omitempty"`
	ClientFingerprint string            `yaml:"client-fingerprint,omitempty"`
	GrpcOpts          *clashGrpcOpts    `yaml:"grpc-opts,omitempty"`
	XhttpOpts         *clashXHTTPOpts   `yaml:"xhttp-opts,omitempty"`
	H2Opts            *clashH2Opts      `yaml:"h2-opts,omitempty"`
	HTTPOpts          *clashHTTPOpts    `yaml:"http-opts,omitempty"`
	WsOpts            *clashWsOpts      `yaml:"ws-opts,omitempty"`
	Plugin            string            `yaml:"plugin,omitempty"`      // ss
	PluginOpts        map[string]any    `yaml:"plugin-opts,omitempty"` // ss
	Smux              map[string]any    `yaml:"smux,omitempty"`
	ObfsOpts          map[string]any    `yaml:"obfs-opts,omitempty"` // snell
	Fragment          map[string]any    `yaml:"fragment,omitempty"`
	DialerProxy       string            `yaml:"dialer-proxy,omitempty"`
	IPVersion         string            `yaml:"ip-version,omitempty"`

	Ports                    string `yaml:"ports,omitempty"` // hysteria2 多端口
	SkipCertVerify           *bool  `yaml:"skip-cert-verify,omitempty"`
	Obfs                     string `yaml:"obfs,omitempty"` // hysteria2 / snell
	ObfsPassword             string `yaml:"obfs-password,omitempty"`
	Up                       string `yaml:"up,omitempty"`       // hysteria2
	Down                     string `yaml:"down,omitempty"`     // hysteria2
	Protocol                 string `yaml:"protocol,omitempty"` // ssr
	ProtocolParam            string `yaml:"protocol-param,omitempty"`
	ObfsParam                string `yaml:"obfs-param,omitempty"`
	PSK                      string `yaml:"psk,omitempty"`      // snell
	Version                  *int   `yaml:"version,omitempty"`  // snell
	IP                       string `yaml:"ip,omitempty"`       // wireguard
	IPv6                     string `yaml:"ipv6,omitempty"`     // wireguard
	Reserved                 string `yaml:"reserved,omitempty"` // wireguard
	PrivateKey               string `yaml:"private-key,omitempty"`
	PublicKey                string `yaml:"public-key,omitempty"`
	PresharedKey             string `yaml:"preshared-key,omitempty"`
	MTU                      *int   `yaml:"mtu,omitempty"`
	CongestionController     string `yaml:"congestion-controller,omitempty"`       // tuic
	UDPRelayMode             string `yaml:"udp-relay-mode,omitempty"`              // tuic
	ReduceRTT                *bool  `yaml:"reduce-rtt,omitempty"`                  // tuic
	IdleSessionCheckInterval int    `yaml:"idle-session-check-interval,omitempty"` // anytls
	IdleSessionTimeout       int    `yaml:"idle-session-timeout,omitempty"`        // anytls
	MinIdleSession           int    `yaml:"min-idle-session,omitempty"`            // anytls

	Raw map[string]any `yaml:",inline"` // 未消费的 Extra 键原样回填
}

type clashWsOpts struct {
	Path                 string            `yaml:"path,omitempty"`
	Headers              map[string]string `yaml:"headers,omitempty"`
	MaxEarlyData         int               `yaml:"max-early-data,omitempty"`
	EarlyDataHeaderName  string            `yaml:"early-data-header-name,omitempty"`
	V2rayHTTPUpgrade     bool              `yaml:"v2ray-http-upgrade,omitempty"`
	V2rayHTTPUpgradeHost string            `yaml:"v2ray-http-upgrade-host,omitempty"`
	V2rayHTTPUpgradePath string            `yaml:"v2ray-http-upgrade-path,omitempty"`
}

type clashHTTPOpts struct {
	Method  string            `yaml:"method,omitempty"`
	Path    []string          `yaml:"path,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

type clashProxyGroup struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Proxies   []string `yaml:"proxies"`
	URL       string   `yaml:"url,omitempty"`
	Interval  int      `yaml:"interval,omitempty"`
	Tolerance int      `yaml:"tolerance,omitempty"`
}

type clashRuleProvider struct {
	Type     string `yaml:"type"`
	Behavior string `yaml:"behavior"`
	URL      string `yaml:"url"`
	Path     string `yaml:"path"`
	Interval int    `yaml:"interval"`
}

// clashDNS 是订阅内置的 fake-ip DNS 配置，使订阅在客户端开箱即用。
// 有意不输出 fallback/fallback-filter：fake-ip 模式下客户端查询不返回真实 IP，
// geoip 回退过滤失效；境外 DoH（dns.google/8.8.4.4 等）在国内不可达，回退查询
// 只会拖垮节点域名解析。节点域名经 proxy-server-nameserver 用国内可达解析器直查。
type clashDNS struct {
	Enable                bool     `yaml:"enable"`
	IPv6                  bool     `yaml:"ipv6"`
	EnhancedMode          string   `yaml:"enhanced-mode"`
	FakeIPRange           string   `yaml:"fake-ip-range,omitempty"`
	FakeIPFilter          []string `yaml:"fake-ip-filter,omitempty"`
	DefaultNameserver     []string `yaml:"default-nameserver,omitempty"`
	Nameserver            []string `yaml:"nameserver,omitempty"`
	ProxyServerNameserver []string `yaml:"proxy-server-nameserver,omitempty"`
}

// clashGeoxURL 指向 MetaCubeX/meta-rules-dat 的地理数据源，
// 配合 geodata-mode 使 GEOSITE/GEOIP 规则生效。
type clashGeoxURL struct {
	Geoip   string `yaml:"geoip,omitempty"`
	Geosite string `yaml:"geosite,omitempty"`
	MMDB    string `yaml:"mmdb,omitempty"`
	ASN     string `yaml:"asn,omitempty"`
}

type clashConfig struct {
	DNS               *clashDNS                    `yaml:"dns,omitempty"`
	GeodataMode       bool                         `yaml:"geodata-mode,omitempty"`
	GeoAutoUpdate     bool                         `yaml:"geo-auto-update,omitempty"`
	GeodataLoader     string                       `yaml:"geodata-loader,omitempty"`
	GeoUpdateInterval int                          `yaml:"geo-update-interval,omitempty"`
	GeoXURL           *clashGeoxURL                `yaml:"geox-url,omitempty"`
	Proxies           []clashProxy                 `yaml:"proxies"`
	ProxyGroups       []clashProxyGroup            `yaml:"proxy-groups"`
	RuleProviders     map[string]clashRuleProvider `yaml:"rule-providers,omitempty"`
	Rules             []string                     `yaml:"rules"`
}

// proxyGroupName 是 select 组名，MATCH 规则指向它。
const proxyGroupName = "PROXY"

// assignedActiveNodes 返回订阅用户及其分配到的 active 节点（§16 公共查询）。
// 分组用户的直连 user_nodes 被遮蔽（订阅 = 分组派生链路 + 外部订阅节点），返回空列表。
func (s *Server) assignedActiveNodes(r *http.Request) (*store.User, []store.Node, error) {
	user, err := s.st.UserBySubToken(r.Context(), r.PathValue("token"))
	if err != nil {
		return nil, nil, err
	}
	groupIDs, err := s.st.UserGroupIDsForUser(r.Context(), user.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(groupIDs) > 0 {
		return user, []store.Node{}, nil
	}
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		return nil, nil, err
	}
	assigned, err := s.st.UserNodeIDs(r.Context(), user.ID)
	if err != nil {
		return nil, nil, err
	}
	allowed := make(map[int64]bool, len(assigned))
	for _, id := range assigned {
		allowed[id] = true
	}
	out := []store.Node{}
	for _, n := range nodes {
		if !allowed[n.ID] {
			continue
		}
		if n.Status != store.NodeStatusActive || len(n.RealizedConfig) == 0 {
			continue
		}
		out = append(out, n)
	}
	return user, out, nil
}

// ServeHTTP 处理 GET /sub/{token}：按 UA / ?format= 返回对应格式订阅内容；
// 浏览器（Accept 含 text/html 且无 ?format=）返回 SPA 壳（index.html）。
// 有效停权态（expired=1 或 disabled=1）的用户订阅照常返回但节点为空。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, err := s.st.UserBySubToken(r.Context(), r.PathValue("token"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error()+"\n", status)
		return
	}
	s.setSubHeaders(w, r, user)

	format := r.URL.Query().Get("format")
	if format == "" {
		format = detectFormat(r)
	}
	if format == "browser" {
		s.serveSPA(w)
		return
	}
	if format != "clash" && format != "singbox" && format != "quanx" && format != "quanx-config" && format != "links" {
		format = "links"
	}
	// 下载文件名（Content-Disposition）：{套餐名|Lattix}，Clash Party 等客户端按该
	// 文件名命名订阅并展示套餐别名；面板侧缓存按 user_id 隔离，不依赖此名。
	w.Header().Set("Content-Disposition", s.subContentDisposition(r, user))
	file, err := s.st.PublishedSubscriptionFile(r.Context(), user.ID, format)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			w.Header().Set("Retry-After", "30")
			http.Error(w, "subscription snapshot has not been published\n", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, err.Error()+"\n", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("X-Lattix-Subscription-Revision", strconv.FormatInt(file.Revision, 10))
	w.Header().Set("Last-Modified", file.GeneratedAt.UTC().Format(http.TimeFormat))
	sum := sha256.Sum256(file.Content)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	_, _ = w.Write(file.Content)
}

// ServeRuleHTTP serves an immutable, client-native rule artifact pinned by its
// source hash. Access is scoped by the same subscription token as the config.
func (s *Server) ServeRuleHTTP(w http.ResponseWriter, r *http.Request) {
	user, err := s.st.UserBySubToken(r.Context(), r.PathValue("token"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error()+"\n", status)
		return
	}
	version := r.PathValue("version")
	format := r.PathValue("format")
	name := r.PathValue("name")
	if len(version) != 64 || !validHex(version) ||
		(format != "mihomo" && format != "singbox" && format != "quanx") || !safeTemplateID.MatchString(name) {
		http.Error(w, "invalid rule artifact path\n", http.StatusBadRequest)
		return
	}
	file, err := s.st.SubscriptionRuleFile(r.Context(), user.ID, version, format, name)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error()+"\n", status)
		return
	}
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("X-Lattix-Subscription-Revision", strconv.FormatInt(file.Revision, 10))
	w.Header().Set("Last-Modified", file.GeneratedAt.UTC().Format(http.TimeFormat))
	w.Header().Set("ETag", `"`+version+`-`+format+`"`)
	w.Header().Set("Cache-Control", "private, max-age=21600, immutable")
	_, _ = w.Write(file.Content)
}

func validHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

// serveSPA 返回内嵌的 index.html（SPA 壳），前端 React Router 匹配 /sub/:token 渲染落地页。
func (s *Server) serveSPA(w http.ResponseWriter) {
	if len(s.spaHTML) == 0 {
		http.Error(w, "frontend not embedded\n", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(s.spaHTML)
}

// serveClash 输出 mihomo（Clash.Meta）YAML。
func (s *Server) serveClash(w http.ResponseWriter, r *http.Request, user *store.User, items []proxyItem) {
	cfg := clashConfig{Proxies: []clashProxy{}}
	names := []string{}
	for _, it := range items {
		credential := it.credential
		if credential == "" {
			credential = user.UUID
		}
		p, err := buildProxy(it.node, it.rc, credential)
		if err != nil {
			continue
		}
		cfg.Proxies = append(cfg.Proxies, p)
		names = append(names, p.Name)
	}
	cfg.ProxyGroups = []clashProxyGroup{{Name: proxyGroupName, Type: "select", Proxies: names}}
	cfg.Rules = []string{"MATCH," + proxyGroupName}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		http.Error(w, err.Error()+"\n", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Write(out)
}

// serveLinks 输出 base64 编码的分享链接集合（vless:// 等）。
func (s *Server) serveLinks(w http.ResponseWriter, r *http.Request, user *store.User, items []proxyItem) {
	links := []string{}
	for _, it := range items {
		credential := it.credential
		if credential == "" {
			credential = user.UUID
		}
		if link, ok := buildShareLink(it.node, it.rc, credential); ok {
			links = append(links, link)
		}
	}
	body := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body + "\n"))
}

// detectFormat 根据 User-Agent 识别客户端类型，返回格式标识。
// 浏览器（Accept 含 text/html）返回 "browser"；未识别返回 "links"。
func detectFormat(r *http.Request) string {
	ua := r.UserAgent()
	// 浏览器检测：Accept 含 text/html 且 UA 含 Mozilla。
	if strings.Contains(r.Header.Get("Accept"), "text/html") && strings.Contains(ua, "Mozilla") {
		return "browser"
	}
	uaLower := strings.ToLower(ua)
	switch {
	// sing-box 系
	case strings.Contains(uaLower, "sing-box"), strings.Contains(uaLower, "sfi"),
		strings.Contains(uaLower, "sfa"), strings.Contains(uaLower, "sfm"):
		return "singbox"
	// Quantumult X
	case strings.Contains(uaLower, "quantumult"):
		return "quanx"
	// Clash 系（Clash Verge / mihomo / Stash / Surfboard / Loon / Egern / FlClash）
	case strings.Contains(uaLower, "clash"), strings.Contains(uaLower, "mihomo"),
		strings.Contains(uaLower, "stash"), strings.Contains(uaLower, "surfboard"),
		strings.Contains(uaLower, "loon"), strings.Contains(uaLower, "egern"),
		strings.Contains(uaLower, "flclash"):
		return "clash"
	// Shadowrocket / v2rayNG / NekoBox → links
	case strings.Contains(uaLower, "shadowrocket"), strings.Contains(uaLower, "v2ray"),
		strings.Contains(uaLower, "nekobox"), strings.Contains(uaLower, "v2box"):
		return "links"
	default:
		return "links"
	}
}

// nodeName 返回节点显示名（管理员设置名称 > 回退 {ServerAlias}-{Protocol}-{Port}）。
func nodeName(n store.Node, rc shared.RealizedConfig) string {
	if n.Name != "" {
		return n.Name
	}
	return fmt.Sprintf("%s-%s-%d", n.ServerAlias, n.Protocol, rc.Port)
}

// proxyItem 是一个订阅条目的来源：节点行 + 生效值
// （链条目已把别名/地址/端口替换为入口侧，§21；其余字段取出口 realized_config）。
// external 非空时表示该条目来自外部订阅节点（凭据取自 config，不派生用户 UUID）。
type proxyItem struct {
	node       store.Node
	rc         shared.RealizedConfig
	credential string
	external   *extsub.Node
}

// subscriptionItems 汇总单机节点与链条目（§21 订阅）：
//   - 链出口业务节点不再作为单机条目出现（只能经链入口消费）；
//   - 链条目：server/port 取入口（非 1:1 映射时经端口段助手换算 public 端口），
//     reality-opts/uuid/flow 等取出口节点 realized_config；命名优先使用链路名称；
//   - 只含 active/degraded 链（failed/pending/applying 不出）；degraded 不剔除（客户端测速规避）；
//   - 用户维度经 user_nodes 判出口节点分配（§16：UUID 只存在于出口 xray）。
func (s *Server) subscriptionItems(r *http.Request, user *store.User, nodes []store.Node) ([]proxyItem, []string) {
	if user.Expired || user.Disabled {
		return nil, nil
	}
	var warnings []string
	exitIDs, err := s.st.ChainExitNodeIDs(r.Context())
	if err != nil {
		exitIDs = map[int64]bool{} // 查询失败不阻断订阅
	}
	items := []proxyItem{}
	for _, n := range nodes {
		if exitIDs[n.ID] {
			continue
		}
		var rc shared.RealizedConfig
		if err := json.Unmarshal(n.RealizedConfig, &rc); err != nil {
			continue // 生效值损坏的节点跳过（异常留在 nodes 表）
		}
		items = append(items, proxyItem{node: n, rc: rc})
	}
	// 用户维度分配（§16）：分组用户只按分组派生（直接分配被遮蔽）；
	// 非分组用户走直接分配（user_nodes + user_chain_assignments）。
	groupIDs, err := s.st.UserGroupIDsForUser(r.Context(), user.ID)
	if err != nil {
		return items, warnings
	}
	allowed := map[int64]bool{}
	var chainAssignments []store.UserChainAssignment
	if len(groupIDs) > 0 {
		chainAssignments, err = s.st.EffectiveUserChainAssignments(r.Context(), user.ID)
	} else {
		assigned, err := s.st.UserNodeIDs(r.Context(), user.ID)
		if err != nil {
			return items, warnings
		}
		for _, id := range assigned {
			allowed[id] = true
		}
		chainAssignments, err = s.st.UserChainAssignments(r.Context(), user.ID)
	}
	if err != nil {
		return items, warnings
	}
	assignmentByChain := make(map[int64]store.UserChainAssignment, len(chainAssignments))
	for _, assignment := range chainAssignments {
		assignmentByChain[assignment.ChainID] = assignment
	}
	chains, err := s.st.ListChains(r.Context())
	if err != nil {
		return items, warnings
	}
	for _, c := range chains {
		// A published snapshot remains the subscription authority while a newer
		// revision is applying or has failed. Agent reachability is control-plane
		// state and must not withdraw an otherwise usable data-plane endpoint.
		if c.PublishedRevisionID == 0 || c.Status == store.ChainStatusInvalid || c.Status == store.ChainStatusDeleted {
			if _, hasAssignment := assignmentByChain[c.ID]; hasAssignment {
				warnings = append(warnings, fmt.Sprintf("链路「%s」已分配但未发布有效修订，未纳入订阅", chainDisplayName(c)))
			}
			continue
		}
		assignment, hasAssignment := assignmentByChain[c.ID]
		if c.EndpointID != 0 && !hasAssignment {
			continue
		}
		item, err := s.chainSubscriptionItem(r, c, allowed, assignment)
		if err != nil {
			if hasAssignment {
				warnings = append(warnings, fmt.Sprintf("链路「%s」未纳入订阅：%v", chainDisplayName(c), err))
			}
			continue
		}
		items = append(items, *item)
	}
	attached, err := s.st.EffectiveUserExternalSubscriptions(r.Context(), user.ID)
	if err != nil {
		return items, warnings
	}
	for _, sub := range attached {
		chains, err := s.st.ListExternalChains(r.Context(), sub.SubscriptionID)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("外部订阅「%s」节点读取失败：%v", sub.Name, err))
			continue
		}
		for _, chain := range chains {
			var ext extsub.Node
			if err := json.Unmarshal(chain.Config, &ext); err != nil {
				continue
			}
			items = append(items, proxyItem{external: &ext})
		}
	}
	return items, warnings
}

// chainDisplayName 返回链路显示名（名称为空时回退到 ID）。
func chainDisplayName(c store.Chain) string {
	if c.Name != "" {
		return c.Name
	}
	return fmt.Sprintf("#%d", c.ID)
}

// chainSubscriptionItem 构造单条链的订阅条目；不满足输出条件返回错误（调用方跳过）。
func (s *Server) chainSubscriptionItem(r *http.Request, chain store.Chain, allowed map[int64]bool,
	assignments ...store.UserChainAssignment) (*proxyItem, error) {
	assignment := store.UserChainAssignment{}
	if len(assignments) > 0 {
		assignment = assignments[0]
	}
	chainID := chain.ID
	revision, err := s.st.PublishedChainRevision(r.Context(), chainID)
	if err != nil {
		return nil, err
	}
	snapshot := revision.Snapshot
	if len(snapshot.Hops) < 1 {
		return nil, fmt.Errorf("链 %d 跳数不足", chainID)
	}
	entry := snapshot.Hops[0]
	if snapshot.EndpointID != 0 {
		endpoint, err := s.st.SharedEndpointByID(r.Context(), snapshot.EndpointID)
		if err != nil {
			return nil, fmt.Errorf("链 %d 共享入口不存在", chainID)
		}
		if endpoint.Status != store.EndpointStatusActive || len(endpoint.RealizedConfig) == 0 {
			if endpoint.Status == store.EndpointStatusFailed {
				return nil, fmt.Errorf("链 %d 共享入口部署失败: %s", chainID, endpoint.Error)
			}
			return nil, fmt.Errorf("链 %d 共享入口部署中，尚未生效", chainID)
		}
		var rc shared.RealizedConfig
		if err := json.Unmarshal(endpoint.RealizedConfig, &rc); err != nil || rc.Port == 0 {
			return nil, fmt.Errorf("链 %d 共享入口 realized_config 不可用", chainID)
		}
		entrySrv, err := s.st.ServerByID(r.Context(), endpoint.ServerID)
		if err != nil || entrySrv.Address == "" {
			return nil, fmt.Errorf("链 %d 入口地址不可用", chainID)
		}
		port := rc.Port
		if ranges, err := shared.ParsePortRanges(entrySrv.AllowedPorts); err == nil && len(ranges) > 0 {
			if public, ok := shared.PublicPort(ranges, port); ok {
				port = public
			}
		}
		rc.Port = port
		entryNode := store.Node{ID: snapshot.ServiceNodeID, Name: snapshot.Name,
			ServerID: snapshot.ServiceServerID, ServerAlias: entrySrv.Alias, ServerAddress: entrySrv.Address,
			Protocol: shared.ProtocolVLESS, ConfigTemplate: endpoint.ConfigTemplate,
			RealizedConfig: endpoint.RealizedConfig, Status: store.NodeStatusActive}
		return &proxyItem{node: entryNode, rc: rc, credential: assignment.AccessUUID}, nil
	}
	if !allowed[snapshot.ServiceNodeID] {
		return nil, fmt.Errorf("链 %d 出口节点未分配给该用户", chainID)
	}
	node, err := s.st.NodeByID(r.Context(), snapshot.ServiceNodeID)
	if err != nil {
		return nil, err
	}
	var virtual shared.VirtualConfig
	if err := json.Unmarshal(snapshot.ServiceConfig, &virtual); err != nil || virtual.Protocol == "" {
		return nil, fmt.Errorf("链 %d 已发布协议配置不可用", chainID)
	}
	var rc shared.RealizedConfig
	if err := json.Unmarshal(snapshot.ServiceRealized, &rc); err != nil || rc.Port == 0 {
		return nil, fmt.Errorf("链 %d 出口 realized_config 不可用", chainID)
	}
	entrySrv, err := s.st.ServerByID(r.Context(), entry.ServerID)
	if err != nil {
		return nil, err
	}
	if entrySrv.Address == "" || entry.ForwardPort == 0 {
		return nil, fmt.Errorf("链 %d 入口地址/端口未就绪", chainID)
	}
	// 订阅端口取入口公网侧（非 1:1 映射时经端口段助手换算，§21）。
	port := entry.ForwardPort
	if ranges, err := shared.ParsePortRanges(entrySrv.AllowedPorts); err == nil && len(ranges) > 0 {
		if pub, ok := shared.PublicPort(ranges, port); ok {
			port = pub
		}
	}
	entryNode := *node
	entryNode.Name = snapshot.Name
	entryNode.Protocol = virtual.Protocol
	entryNode.ConfigTemplate = append(json.RawMessage(nil), snapshot.ServiceConfig...)
	entryNode.RealizedConfig = append(json.RawMessage(nil), snapshot.ServiceRealized...)
	entryNode.ServerAlias = entrySrv.Alias
	entryNode.ServerAddress = entrySrv.Address
	rc.Port = port
	return &proxyItem{node: entryNode, rc: rc}, nil
}

// buildProxy 按节点协议构造 mihomo 代理项；uuid 为该订阅用户自己的 UUID（§8/§9）。
// 节点命名优先使用管理员设置名称；名称为空时回退到 {服务器别名}-{协议}-{端口}。
func buildProxy(n store.Node, rc shared.RealizedConfig, uuid string) (clashProxy, error) {
	if rc.Port == 0 {
		return clashProxy{}, fmt.Errorf("节点 %d 缺少生效端口", n.ID)
	}
	// 对当前协议允许省略的默认字段做归一化。
	if rc.Network == "" {
		rc.Network = shared.NetworkTCP
	}
	if rc.Fingerprint == "" {
		rc.Fingerprint = shared.FingerprintChrome
	}
	name := nodeName(n, rc)
	p := clashProxy{
		Name:   name,
		Type:   n.Protocol,
		Server: n.ServerAddress,
		Port:   rc.Port,
		UDP:    true,
	}
	switch n.Protocol {
	case shared.ProtocolVLESS:
		p.UUID = uuid
		p.Network = rc.Network
		p.TLS = true
		p.Servername = rc.ServerName
		p.Flow = rc.Flow
		p.Encryption = rc.Encryption
		applyReality(&p, rc)
	case shared.ProtocolVMess:
		zero := 0
		p.UUID = uuid
		p.AlterID = &zero
		p.Cipher = "auto"
		p.Network = rc.Network
		p.TLS = true
		p.Servername = rc.ServerName
		applyReality(&p, rc)
	case shared.ProtocolTrojan:
		p.Password = uuid
		p.Network = rc.Network
		p.SNI = rc.ServerName
		applyReality(&p, rc)
	case shared.ProtocolShadowsocks:
		p.Type = "ss" // mihomo 类型名
		p.Cipher = rc.Method
		if shared.Is2022Method(rc.Method) {
			// 2022-blake3 多用户：客户端密码为 "节点PSK:用户密钥"。
			p.Password = rc.PSK + ":" + shared.SSUserPassword(uuid, rc.Method)
		} else {
			p.Password = shared.SSUserPassword(uuid, rc.Method)
		}
	case shared.ProtocolSocks:
		p.Type = "socks5"
		p.Username = uuid
		p.Password = uuid
	case shared.ProtocolHTTP:
		p.Username = uuid
		p.Password = uuid
		p.UDP = false
	default:
		return clashProxy{}, fmt.Errorf("未知协议: %s", n.Protocol)
	}
	return p, nil
}

// applyReality 填充 reality 系协议共用的 TLS 指纹、reality-opts 与传输选项。
func applyReality(p *clashProxy, rc shared.RealizedConfig) {
	p.ClientFingerprint = rc.Fingerprint
	p.RealityOpts = &clashRealityOpts{PublicKey: rc.PublicKey, ShortID: rc.ShortID}
	switch rc.Network {
	case shared.NetworkGRPC:
		p.GrpcOpts = &clashGrpcOpts{ServiceName: rc.ServiceName}
	case shared.NetworkXHTTP:
		p.XhttpOpts = &clashXHTTPOpts{Path: rc.Path, Mode: rc.Mode, Host: rc.Host}
	}
}
