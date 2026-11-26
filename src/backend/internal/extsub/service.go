package extsub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared/requester"
)

const (
	defaultUserAgent         = "clash.meta/v2.0.0"
	maxSubscriptionBytes     = 2 << 20 // 2 MiB
	minSyncIntervalHours     = 1
	defaultSyncIntervalHours = 24
)

// Service 编排外部订阅的拉取、解析与入库。
type Service struct {
	st              *store.Store
	files           requester.ExternalFileRequester
	skipVerifyFiles requester.ExternalFileRequester
}

func New(st *store.Store, files, skipVerifyFiles requester.ExternalFileRequester) *Service {
	return &Service{st: st, files: files, skipVerifyFiles: skipVerifyFiles}
}

// Create 校验并保存订阅（同 URL 视为同一订阅并更新），随后立即同步一次。
// 拉取失败时记录已保留，LastError 写明原因。
func (s *Service) Create(ctx context.Context, name, rawURL, userAgent string,
	skipCertVerify, autoUpdate bool, intervalHours int) (store.ExternalSubscription, error) {
	if err := validateSubscriptionURL(rawURL); err != nil {
		return store.ExternalSubscription{}, err
	}
	if strings.TrimSpace(name) == "" {
		return store.ExternalSubscription{}, errors.New("订阅名称不能为空")
	}
	intervalHours = normalizeInterval(intervalHours)
	existing, err := s.st.ExternalSubscriptionByURL(ctx, rawURL)
	switch {
	case err == nil:
		existing.Name = strings.TrimSpace(name)
		existing.UserAgent = strings.TrimSpace(userAgent)
		existing.SkipCertVerify = skipCertVerify
		existing.AutoUpdate = autoUpdate
		existing.UpdateIntervalHours = intervalHours
		if err := s.st.UpdateExternalSubscription(ctx, existing); err != nil {
			return store.ExternalSubscription{}, fmt.Errorf("update external subscription: %w", err)
		}
		return s.Sync(ctx, existing.ID)
	case errors.Is(err, store.ErrNotFound):
		id, err := s.st.CreateExternalSubscription(ctx, store.ExternalSubscription{
			Name: strings.TrimSpace(name), URL: rawURL,
			UserAgent: strings.TrimSpace(userAgent), SkipCertVerify: skipCertVerify,
			AutoUpdate: autoUpdate, UpdateIntervalHours: intervalHours,
		})
		if err != nil {
			return store.ExternalSubscription{}, err
		}
		return s.Sync(ctx, id)
	default:
		return store.ExternalSubscription{}, err
	}
}

// Update 更新订阅设置；URL 变更时立即重新同步（与 Create 行为对齐，
// 失败保留记录并回填 LastError）。新地址与其他订阅冲突时拒绝，
// 避免落到数据库 url 唯一约束的原始报错。
func (s *Service) Update(ctx context.Context, id int64, name, rawURL, userAgent string,
	skipCertVerify, autoUpdate bool, intervalHours int) (store.ExternalSubscription, error) {
	if err := validateSubscriptionURL(rawURL); err != nil {
		return store.ExternalSubscription{}, err
	}
	if strings.TrimSpace(name) == "" {
		return store.ExternalSubscription{}, errors.New("订阅名称不能为空")
	}
	sub, err := s.st.ExternalSubscriptionByID(ctx, id)
	if err != nil {
		return store.ExternalSubscription{}, err
	}
	if other, err := s.st.ExternalSubscriptionByURL(ctx, rawURL); err == nil && other.ID != id {
		return store.ExternalSubscription{}, errors.New("订阅地址已被其他订阅使用")
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.ExternalSubscription{}, err
	}
	urlChanged := sub.URL != rawURL
	sub.Name = strings.TrimSpace(name)
	sub.URL = rawURL
	sub.UserAgent = strings.TrimSpace(userAgent)
	sub.SkipCertVerify = skipCertVerify
	sub.AutoUpdate = autoUpdate
	sub.UpdateIntervalHours = normalizeInterval(intervalHours)
	if err := s.st.UpdateExternalSubscription(ctx, sub); err != nil {
		return store.ExternalSubscription{}, fmt.Errorf("update external subscription: %w", err)
	}
	if urlChanged {
		return s.Sync(ctx, id)
	}
	return s.st.ExternalSubscriptionByID(ctx, id)
}

// Sync 拉取、解析并全量替换该订阅的节点，回填流量与同步时间。
func (s *Service) Sync(ctx context.Context, id int64) (store.ExternalSubscription, error) {
	sub, err := s.st.ExternalSubscriptionByID(ctx, id)
	if err != nil {
		return store.ExternalSubscription{}, err
	}
	now := time.Now().UTC()
	sub.LastAttemptAt = &now
	if err := s.st.UpdateExternalSubscription(ctx, sub); err != nil {
		return store.ExternalSubscription{}, err
	}

	sub, result, err := s.fetchAndParse(ctx, sub)
	if err != nil {
		sub.LastError = err.Error()
		if updateErr := s.st.UpdateExternalSubscription(ctx, sub); updateErr != nil {
			return store.ExternalSubscription{}, updateErr
		}
		return sub, err
	}

	count, err := s.st.ReplaceExternalChains(ctx, sub.ID, result.chains)
	if err != nil {
		sub.LastError = err.Error()
		if updateErr := s.st.UpdateExternalSubscription(ctx, sub); updateErr != nil {
			return store.ExternalSubscription{}, updateErr
		}
		return sub, err
	}
	sub.Format = result.format
	sub.NodeCount = count
	sub.Upload = result.upload
	sub.Download = result.download
	sub.Total = result.total
	sub.Expire = result.expire
	sub.LastError = ""
	sub.LastSyncAt = &now
	if err := s.st.UpdateExternalSubscription(ctx, sub); err != nil {
		return store.ExternalSubscription{}, err
	}
	return sub, nil
}

type syncResult struct {
	chains   []store.ExternalChain
	format   string
	upload   int64
	download int64
	total    int64
	expire   *int64
}

func (s *Service) fetchAndParse(ctx context.Context, sub store.ExternalSubscription) (store.ExternalSubscription, syncResult, error) {
	files := s.files
	if sub.SkipCertVerify {
		files = s.skipVerifyFiles
	}
	userAgent := sub.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	fetch := func(ua string) (requester.FileFetchResult, error) {
		return files.GetWithOptions(ctx, sub.URL, maxSubscriptionBytes,
			requester.FileRequestOptions{UserAgent: ua})
	}
	result, err := fetch(userAgent)
	if err != nil {
		return sub, syncResult{}, err
	}
	upload, download, total, expire := parseTrafficUserinfo(result.Header.Get("subscription-userinfo"))
	nodes, format, parseErr := ParseSubscription([]byte(result.Body))
	// 机场常按 UA 放行并切换返回格式，也偶发返回限流/维护页。解析失败时
	// 换默认 clash UA 重拉一次再解析；流量头缺失且 UA 非 clash 的既有
	// 兜底合并进同一次重拉。
	if (parseErr != nil || (total == 0 && !strings.Contains(strings.ToLower(userAgent), "clash"))) &&
		userAgent != defaultUserAgent {
		if retry, retryErr := fetch(defaultUserAgent); retryErr == nil {
			retryUpload, retryDownload, retryTotal, retryExpire := parseTrafficUserinfo(
				retry.Header.Get("subscription-userinfo"))
			if parseErr != nil {
				if retryNodes, retryFormat, retryParseErr := ParseSubscription([]byte(retry.Body)); retryParseErr == nil {
					// 换 UA 拉到了可解析内容：整体采用重试结果
					result, nodes, format = retry, retryNodes, retryFormat
					if total == 0 && retryTotal > 0 {
						upload, download, total, expire = retryUpload, retryDownload, retryTotal, retryExpire
					}
					parseErr = nil
				} else {
					parseErr = fmt.Errorf("%s（改用 UA %s 拉取仍失败：%s）", parseErr, defaultUserAgent, retryParseErr)
				}
			} else if total == 0 && retryTotal > 0 {
				// 解析成功仅缺流量头：沿用既有兜底，只取流量信息
				upload, download, total, expire = retryUpload, retryDownload, retryTotal, retryExpire
			}
		}
	}
	if parseErr != nil {
		return sub, syncResult{}, fmt.Errorf("%w（已收到响应：%s）", parseErr, describeReceived(result))
	}
	chains := make([]store.ExternalChain, 0, len(nodes))
	seen := make(map[string]bool)
	for _, node := range nodes {
		config, err := json.Marshal(node)
		if err != nil {
			continue
		}
		sha := configSHA256(config)
		if seen[sha] {
			continue
		}
		seen[sha] = true
		chains = append(chains, store.ExternalChain{
			SubscriptionID: sub.ID, Name: node.Name, Protocol: node.Type,
			Server: node.Server, Port: node.Port, Config: config, ConfigSHA256: sha,
		})
	}
	if len(chains) == 0 {
		return sub, syncResult{}, errors.New("订阅中没有可解析的节点")
	}
	return sub, syncResult{
		chains: chains, format: format,
		upload: upload, download: download, total: total, expire: expire,
	}, nil
}

// SyncDue 同步所有到达自动更新间隔的订阅；单订阅失败不影响其他订阅。
// 返回本次实际同步成功的订阅 ID 列表（供调用方触发关联用户订阅重发布）。
func (s *Service) SyncDue(ctx context.Context) ([]int64, error) {
	subs, err := s.st.ListExternalSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var synced []int64
	for _, sub := range subs {
		if !sub.AutoUpdate {
			continue
		}
		if sub.LastAttemptAt != nil &&
			sub.LastAttemptAt.Add(time.Duration(sub.UpdateIntervalHours)*time.Hour).After(now) {
			continue
		}
		if _, err := s.Sync(ctx, sub.ID); err != nil {
			// 记录保留 last_error；继续其他订阅
			continue
		}
		synced = append(synced, sub.ID)
	}
	return synced, nil
}

func normalizeInterval(hours int) int {
	if hours < minSyncIntervalHours {
		return minSyncIntervalHours
	}
	return hours
}

func configSHA256(config []byte) string {
	sum := sha256.Sum256(config)
	return hex.EncodeToString(sum[:])
}

// describeReceived 概括解析失败时实际收到的响应（类型/长度/内容前缀），
// 便于区分机场返回的 HTML 公告页、限流页与空 proxies。前缀会进入
// last_error 与创建报错，属于管理员自配订阅的可见数据，压缩为单行并截断。
func describeReceived(result requester.FileFetchResult) string {
	contentType := result.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "未知"
	}
	return fmt.Sprintf("Content-Type=%s，%d 字节，前缀 %q",
		contentType, len(result.Body), sanitizeBodyPrefix(result.Body, 160))
}

// sanitizeBodyPrefix 压缩空白为单空格并按 rune 截断。
func sanitizeBodyPrefix(body string, limit int) string {
	joined := strings.Join(strings.Fields(body), " ")
	runes := []rune(joined)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return joined
}

// validateSubscriptionURL 仅允许 https，并拒绝 localhost、内网与保留段地址
// （IP 字面量直接判定；主机名按常见内网后缀拒绝，不做 DNS 解析——
// 解析到内网的域名由 requester 的拨号防护在连接时拦截）。
func validateSubscriptionURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("订阅地址必须是有效的 https URL")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if requester.IsBlockedIP(ip) {
			return errors.New("订阅地址不能指向本机、内网或保留地址")
		}
		return nil
	}
	lower := strings.ToLower(host)
	if lower == "localhost" {
		return errors.New("订阅地址不能指向本机或内网地址")
	}
	for _, suffix := range []string{".local", ".internal", ".lan", ".home.arpa"} {
		if strings.HasSuffix(lower, suffix) {
			return errors.New("订阅地址不能指向本机或内网地址")
		}
	}
	return nil
}

// parseTrafficUserinfo 解析 subscription-userinfo 响应头：
// upload=..; download=..; total=..; expire=..（支持浮点取整；expire=0 忽略）。
func parseTrafficUserinfo(header string) (upload, download, total int64, expire *int64) {
	for _, part := range strings.Split(header, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(value, 64)
			if ferr != nil {
				continue
			}
			parsed = int64(f)
		}
		switch key {
		case "upload":
			upload = parsed
		case "download":
			download = parsed
		case "total":
			total = parsed
		case "expire":
			if parsed > 0 {
				expire = &parsed
			}
		}
	}
	return upload, download, total, expire
}
