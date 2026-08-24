package sub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

const (
	defaultClientCacheTTL   = 72 * time.Hour
	maxClientPackageBytes   = int64(512 << 20)
	clientDownloadTicketTTL = 3 * time.Hour
	// 任意有效订阅 token 都能触发 ≤512MB 的上游下载落盘，按 token 做滑动窗口
	// 限流，防止磁盘/带宽被反复消耗（安全评审 M3）。
	maxClientDownloadTasksPerTokenHour = 10
	clientDownloadLimitWindow          = time.Hour
	maxTrackedDownloadTokens           = 4096
)

type ClientDownloadVariant struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type clientDownloadSpec struct {
	ID      string
	Label   string
	Repo    string
	Pattern *regexp.Regexp
}

type clientRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []clientReleaseAsset `json:"assets"`
}

type clientReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"` // GitHub API 提供的 "sha256:<hex>" 形式校验值（可能为空）
}

type clientCacheMetadata struct {
	VariantID    string    `json:"variant_id"`
	ReleaseTag   string    `json:"release_tag"`
	AssetURL     string    `json:"asset_url"`
	Filename     string    `json:"filename"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha256,omitempty"`
	DownloadedAt time.Time `json:"downloaded_at"`
}

type clientDownloadTask struct {
	ID        string
	VariantID string
	Status    string
	Progress  float64
	Size      int64
	Filename  string
	FilePath  string
	SourceURL string
	SHA256    string
	Error     string
	CreatedAt time.Time
}

// clientDownloadTicket 是一次会话级的下载票据：绑定下载任务与订阅 token，
// 有效期内可多次使用（断点续传需对同一 URL 发起多次 Range 请求），过期即失效。
type clientDownloadTicket struct {
	TaskID    string
	Token     string
	ExpiresAt time.Time
}

var clientDownloadSpecs = map[string]clientDownloadSpec{
	"clash-verge-windows-x64":          {ID: "clash-verge-windows-x64", Label: "Windows x64 安装包", Repo: "clash-verge-rev/clash-verge-rev", Pattern: regexp.MustCompile(`^Clash\.Verge_[^_]+_x64-setup\.exe$`)},
	"clash-verge-windows-arm64":        {ID: "clash-verge-windows-arm64", Label: "Windows arm64 安装包", Repo: "clash-verge-rev/clash-verge-rev", Pattern: regexp.MustCompile(`^Clash\.Verge_[^_]+_arm64-setup\.exe$`)},
	"clash-verge-macos-x64":            {ID: "clash-verge-macos-x64", Label: "macOS x64 安装包", Repo: "clash-verge-rev/clash-verge-rev", Pattern: regexp.MustCompile(`^Clash\.Verge_[^_]+_x64\.dmg$`)},
	"clash-verge-macos-arm64":          {ID: "clash-verge-macos-arm64", Label: "macOS arm64 安装包", Repo: "clash-verge-rev/clash-verge-rev", Pattern: regexp.MustCompile(`^Clash\.Verge_[^_]+_aarch64\.dmg$`)},
	"clash-verge-macos-x64-portable":   {ID: "clash-verge-macos-x64-portable", Label: "macOS x64 portable", Repo: "clash-verge-rev/clash-verge-rev", Pattern: regexp.MustCompile(`^Clash\.Verge_[^_]+_x64\.app\.tar\.gz$`)},
	"clash-verge-macos-arm64-portable": {ID: "clash-verge-macos-arm64-portable", Label: "macOS arm64 portable", Repo: "clash-verge-rev/clash-verge-rev", Pattern: regexp.MustCompile(`^Clash\.Verge_[^_]+_aarch64\.app\.tar\.gz$`)},

	"mihomo-party-windows-x64":            {ID: "mihomo-party-windows-x64", Label: "Windows x64 安装包", Repo: "mihomo-party-org/mihomo-party", Pattern: regexp.MustCompile(`^mihomo-party-windows-[^-]+-x64-setup\.exe$`)},
	"mihomo-party-windows-arm64":          {ID: "mihomo-party-windows-arm64", Label: "Windows arm64 安装包", Repo: "mihomo-party-org/mihomo-party", Pattern: regexp.MustCompile(`^mihomo-party-windows-[^-]+-arm64-setup\.exe$`)},
	"mihomo-party-windows-x64-portable":   {ID: "mihomo-party-windows-x64-portable", Label: "Windows x64 portable", Repo: "mihomo-party-org/mihomo-party", Pattern: regexp.MustCompile(`^mihomo-party-windows-[^-]+-x64-portable\.7z$`)},
	"mihomo-party-windows-arm64-portable": {ID: "mihomo-party-windows-arm64-portable", Label: "Windows arm64 portable", Repo: "mihomo-party-org/mihomo-party", Pattern: regexp.MustCompile(`^mihomo-party-windows-[^-]+-arm64-portable\.7z$`)},
	"mihomo-party-macos-x64":              {ID: "mihomo-party-macos-x64", Label: "macOS x64 安装包", Repo: "mihomo-party-org/mihomo-party", Pattern: regexp.MustCompile(`^mihomo-party-macos-[^-]+-x64\.pkg$`)},
	"mihomo-party-macos-arm64":            {ID: "mihomo-party-macos-arm64", Label: "macOS arm64 安装包", Repo: "mihomo-party-org/mihomo-party", Pattern: regexp.MustCompile(`^mihomo-party-macos-[^-]+-arm64\.pkg$`)},

	"flclash-android-arm64":          {ID: "flclash-android-arm64", Label: "Android arm64", Repo: "chen08209/FlClash", Pattern: regexp.MustCompile(`^FlClash-[^-]+-android-arm64-v8a\.apk$`)},
	"flclash-android-armv7":          {ID: "flclash-android-armv7", Label: "Android armeabi-v7a", Repo: "chen08209/FlClash", Pattern: regexp.MustCompile(`^FlClash-[^-]+-android-armeabi-v7a\.apk$`)},
	"flclash-android-x64":            {ID: "flclash-android-x64", Label: "Android x86_64", Repo: "chen08209/FlClash", Pattern: regexp.MustCompile(`^FlClash-[^-]+-android-x86_64\.apk$`)},
	"flclash-windows-x64":            {ID: "flclash-windows-x64", Label: "Windows x64 安装包", Repo: "chen08209/FlClash", Pattern: regexp.MustCompile(`^FlClash-[^-]+-windows-amd64-setup\.exe$`)},
	"flclash-windows-arm64":          {ID: "flclash-windows-arm64", Label: "Windows arm64 安装包", Repo: "chen08209/FlClash", Pattern: regexp.MustCompile(`^FlClash-[^-]+-windows-arm64-setup\.exe$`)},
	"flclash-windows-x64-portable":   {ID: "flclash-windows-x64-portable", Label: "Windows x64 portable", Repo: "chen08209/FlClash", Pattern: regexp.MustCompile(`^FlClash-[^-]+-windows-amd64\.zip$`)},
	"flclash-windows-arm64-portable": {ID: "flclash-windows-arm64-portable", Label: "Windows arm64 portable", Repo: "chen08209/FlClash", Pattern: regexp.MustCompile(`^FlClash-[^-]+-windows-arm64\.zip$`)},
	"flclash-macos-x64":              {ID: "flclash-macos-x64", Label: "macOS x64 安装包", Repo: "chen08209/FlClash", Pattern: regexp.MustCompile(`^FlClash-[^-]+-macos-amd64\.dmg$`)},
	"flclash-macos-arm64":            {ID: "flclash-macos-arm64", Label: "macOS arm64 安装包", Repo: "chen08209/FlClash", Pattern: regexp.MustCompile(`^FlClash-[^-]+-macos-arm64\.dmg$`)},

	"surfboard-android-arm64": {ID: "surfboard-android-arm64", Label: "Android arm64", Repo: "getsurfboard/surfboard", Pattern: regexp.MustCompile(`^mobile-arm64-v8a-release\.apk$`)},
	"surfboard-android-armv7": {ID: "surfboard-android-armv7", Label: "Android armeabi-v7a", Repo: "getsurfboard/surfboard", Pattern: regexp.MustCompile(`^mobile-armeabi-v7a-release\.apk$`)},
	"surfboard-android-x64":   {ID: "surfboard-android-x64", Label: "Android x86_64", Repo: "getsurfboard/surfboard", Pattern: regexp.MustCompile(`^mobile-x86_64-release\.apk$`)},
	"surfboard-android-x86":   {ID: "surfboard-android-x86", Label: "Android x86", Repo: "getsurfboard/surfboard", Pattern: regexp.MustCompile(`^mobile-x86-release\.apk$`)},

	"singbox-android-arm64": {ID: "singbox-android-arm64", Label: "Android arm64", Repo: "SagerNet/sing-box", Pattern: regexp.MustCompile(`^SFA-[^-]+-arm64-v8a\.apk$`)},
	"singbox-android-armv7": {ID: "singbox-android-armv7", Label: "Android armeabi-v7a", Repo: "SagerNet/sing-box", Pattern: regexp.MustCompile(`^SFA-[^-]+-armeabi-v7a\.apk$`)},
	"singbox-android-x64":   {ID: "singbox-android-x64", Label: "Android x86_64", Repo: "SagerNet/sing-box", Pattern: regexp.MustCompile(`^SFA-[^-]+-x86_64\.apk$`)},
	"singbox-android-x86":   {ID: "singbox-android-x86", Label: "Android x86", Repo: "SagerNet/sing-box", Pattern: regexp.MustCompile(`^SFA-[^-]+-x86\.apk$`)},

	"v2rayng-android-arm64": {ID: "v2rayng-android-arm64", Label: "Android arm64", Repo: "2dust/v2rayNG", Pattern: regexp.MustCompile(`^v2rayNG_[^_]+_arm64-v8a\.apk$`)},
	"v2rayng-android-armv7": {ID: "v2rayng-android-armv7", Label: "Android armeabi-v7a", Repo: "2dust/v2rayNG", Pattern: regexp.MustCompile(`^v2rayNG_[^_]+_armeabi-v7a\.apk$`)},
	"v2rayng-android-x64":   {ID: "v2rayng-android-x64", Label: "Android x86_64", Repo: "2dust/v2rayNG", Pattern: regexp.MustCompile(`^v2rayNG_[^_]+_x86_64\.apk$`)},
	"v2rayng-android-x86":   {ID: "v2rayng-android-x86", Label: "Android x86", Repo: "2dust/v2rayNG", Pattern: regexp.MustCompile(`^v2rayNG_[^_]+_x86\.apk$`)},

	"nekobox-android-arm64": {ID: "nekobox-android-arm64", Label: "Android arm64", Repo: "MatsuriDayo/NekoBoxForAndroid", Pattern: regexp.MustCompile(`^NekoBox-[^-]+-arm64-v8a\.apk$`)},
	"nekobox-android-armv7": {ID: "nekobox-android-armv7", Label: "Android armeabi-v7a", Repo: "MatsuriDayo/NekoBoxForAndroid", Pattern: regexp.MustCompile(`^NekoBox-[^-]+-armeabi-v7a\.apk$`)},
	"nekobox-android-x64":   {ID: "nekobox-android-x64", Label: "Android x86_64", Repo: "MatsuriDayo/NekoBoxForAndroid", Pattern: regexp.MustCompile(`^NekoBox-[^-]+-x86_64\.apk$`)},
	"nekobox-android-x86":   {ID: "nekobox-android-x86", Label: "Android x86", Repo: "MatsuriDayo/NekoBoxForAndroid", Pattern: regexp.MustCompile(`^NekoBox-[^-]+-x86\.apk$`)},
}

func clientDownloadVariants(ids ...string) []ClientDownloadVariant {
	out := make([]ClientDownloadVariant, 0, len(ids))
	for _, id := range ids {
		if spec, ok := clientDownloadSpecs[id]; ok {
			out = append(out, ClientDownloadVariant{ID: spec.ID, Label: spec.Label})
		}
	}
	return out
}

// clientDownloadLimiter 按订阅 token 限流新建下载任务：滑动窗口内每 token 最多
// maxClientDownloadTasksPerTokenHour 次。惰性清理窗口外时间戳，并限制跟踪的
// token 数量防内存膨胀（模式同 panel 的 loginLimiter）。
type clientDownloadLimiter struct {
	mu      sync.Mutex
	windows map[string]*clientDownloadWindow
	now     func() time.Time
}

type clientDownloadWindow struct {
	timestamps []time.Time // 窗口内新建任务的时间点
	lastSeen   time.Time
}

func newClientDownloadLimiter() *clientDownloadLimiter {
	return &clientDownloadLimiter{windows: make(map[string]*clientDownloadWindow), now: time.Now}
}

// allow 为 token 记录一次新建下载任务并返回是否放行；拒绝时返回窗口滑动到可用的等待时长
// （最早一条窗口内记录滑出为止，供 429 响应的 Retry-After 头使用，模式同 panel 的 loginLimiter）。
func (l *clientDownloadLimiter) allow(token string) (time.Duration, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	window, ok := l.windows[token]
	if !ok {
		if len(l.windows) >= maxTrackedDownloadTokens {
			l.evictOldest()
		}
		window = &clientDownloadWindow{}
		l.windows[token] = window
	} else {
		// 惰性清理窗口外的时间戳。
		kept := window.timestamps[:0]
		for _, ts := range window.timestamps {
			if now.Sub(ts) < clientDownloadLimitWindow {
				kept = append(kept, ts)
			}
		}
		window.timestamps = kept
		if len(window.timestamps) >= maxClientDownloadTasksPerTokenHour {
			window.lastSeen = now
			return clientDownloadLimitWindow - now.Sub(window.timestamps[0]), false
		}
	}
	window.timestamps = append(window.timestamps, now)
	window.lastSeen = now
	return 0, true
}

func (l *clientDownloadLimiter) evictOldest() {
	oldestKey := ""
	var oldest time.Time
	for key, window := range l.windows {
		if oldestKey == "" || window.lastSeen.Before(oldest) {
			oldestKey, oldest = key, window.lastSeen
		}
	}
	delete(l.windows, oldestKey)
}

func (s *Server) HandleSubClientDownloadStart(w http.ResponseWriter, r *http.Request) {
	if _, err := s.subDownloadUser(r); err != nil {
		subDownloadError(w, err)
		return
	}
	variantID := r.URL.Query().Get("variant")
	spec, ok := clientDownloadSpecs[variantID]
	if !ok || s.cacheDir == "" {
		subDownloadError(w, errors.New("客户端下载安装暂不可用"))
		return
	}

	s.downloadMu.Lock()
	if taskID := s.activeDownloads[spec.ID]; taskID != "" {
		if task := s.downloadTasks[taskID]; task != nil && (task.Status == "queued" || task.Status == "downloading") {
			resp := clientDownloadTaskResponse{TaskID: task.ID, Status: task.Status}
			s.downloadMu.Unlock()
			writeClientDownloadJSON(w, http.StatusOK, resp)
			return
		}
	}
	// 只对新建下载任务计数限流：去重命中活跃任务直接返回（不计数）；
	// 票据签发与 Range 断点续传不经过此入口（安全评审 M3）。
	if retryAfter, allowed := s.downloadLimiter.allow(r.PathValue("token")); !allowed {
		s.downloadMu.Unlock()
		// Retry-After 取窗口滑动到可用的秒数（向上取整，至少 1 秒）。
		seconds := int64((retryAfter + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		subDownloadErrorStatus(w, http.StatusTooManyRequests, errors.New("客户端下载请求过于频繁，请稍后重试"))
		return
	}
	task := &clientDownloadTask{ID: shared.NewMessageID(), VariantID: spec.ID, Status: "queued", CreatedAt: time.Now().UTC()}
	s.downloadTasks[task.ID] = task
	s.activeDownloads[spec.ID] = task.ID
	resp := clientDownloadTaskResponse{TaskID: task.ID, Status: task.Status}
	s.downloadMu.Unlock()

	go s.runClientDownload(task.ID, spec)
	writeClientDownloadJSON(w, http.StatusAccepted, resp)
}

func (s *Server) HandleSubClientDownloadStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := s.subDownloadUser(r); err != nil {
		subDownloadError(w, err)
		return
	}
	taskID := r.URL.Query().Get("task")
	s.downloadMu.Lock()
	task := s.downloadTasks[taskID]
	if task == nil {
		s.downloadMu.Unlock()
		subDownloadErrorStatus(w, http.StatusNotFound, errors.New("下载任务不存在"))
		return
	}
	resp := clientDownloadTaskResponse{TaskID: task.ID, Status: task.Status, Progress: task.Progress, Size: task.Size, Filename: task.Filename, SourceURL: task.SourceURL, SHA256: task.SHA256, Error: task.Error}
	s.downloadMu.Unlock()
	writeClientDownloadJSON(w, http.StatusOK, resp)
}

func writeClientDownloadJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// HandleSubClientDownloadTicket 为已完成的下载任务签发短期票据，
// 前端凭票据换取普通 HTTP 下载链接（浏览器原生下载，支持断点续传）。
func (s *Server) HandleSubClientDownloadTicket(w http.ResponseWriter, r *http.Request) {
	if _, err := s.subDownloadUser(r); err != nil {
		subDownloadError(w, err)
		return
	}
	taskID := r.URL.Query().Get("task")
	now := time.Now().UTC()
	s.downloadMu.Lock()
	task := s.downloadTasks[taskID]
	if task == nil || task.Status != "done" || task.FilePath == "" {
		s.downloadMu.Unlock()
		subDownloadErrorStatus(w, http.StatusNotFound, errors.New("下载文件不可用"))
		return
	}
	// 惰性清理过期票据。
	for id, ticket := range s.downloadTickets {
		if now.After(ticket.ExpiresAt) {
			delete(s.downloadTickets, id)
		}
	}
	ticketID := shared.NewMessageID()
	expiresAt := now.Add(clientDownloadTicketTTL)
	s.downloadTickets[ticketID] = &clientDownloadTicket{
		TaskID:    taskID,
		Token:     r.PathValue("token"),
		ExpiresAt: expiresAt,
	}
	s.downloadMu.Unlock()
	writeClientDownloadJSON(w, http.StatusOK, clientDownloadTicketResponse{Ticket: ticketID, ExpiresAt: expiresAt})
}

func (s *Server) HandleSubClientDownloadFile(w http.ResponseWriter, r *http.Request) {
	if _, err := s.subDownloadUser(r); err != nil {
		subDownloadError(w, err)
		return
	}
	taskID := r.URL.Query().Get("task")
	s.downloadMu.Lock()
	ticket := s.downloadTickets[r.URL.Query().Get("ticket")]
	if ticket == nil || ticket.TaskID != taskID || ticket.Token != r.PathValue("token") || time.Now().UTC().After(ticket.ExpiresAt) {
		s.downloadMu.Unlock()
		subDownloadErrorStatus(w, http.StatusForbidden, errors.New("下载链接已失效，请重新下载"))
		return
	}
	task := s.downloadTasks[taskID]
	if task == nil || task.Status != "done" || task.FilePath == "" {
		s.downloadMu.Unlock()
		subDownloadErrorStatus(w, http.StatusNotFound, errors.New("下载文件不可用"))
		return
	}
	path, filename := task.FilePath, task.Filename
	s.downloadMu.Unlock()
	if _, err := os.Stat(path); err != nil {
		subDownloadErrorStatus(w, http.StatusNotFound, errors.New("下载文件已过期"))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	http.ServeFile(w, r, path)
}

type clientDownloadTicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

type clientDownloadTaskResponse struct {
	TaskID    string  `json:"task_id"`
	Status    string  `json:"status"`
	Progress  float64 `json:"progress"`
	Size      int64   `json:"size"`
	Filename  string  `json:"filename,omitempty"`
	SourceURL string  `json:"source_url,omitempty"`
	SHA256    string  `json:"sha256,omitempty"`
	Error     string  `json:"error,omitempty"`
}

func (s *Server) subDownloadUser(r *http.Request) (*store.User, error) {
	user, err := s.st.UserBySubToken(r.Context(), r.PathValue("token"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("订阅不存在")
		}
		// 内部错误（DB 失败）不回显细节，原始错误只记日志（安全评审 L3）。
		log.Printf("sub: client download user: %v", err)
		return nil, errors.New("internal error")
	}
	if user.Expired || user.Disabled {
		return nil, errors.New("订阅不可用")
	}
	return user, nil
}

func subDownloadError(w http.ResponseWriter, err error) {
	subDownloadErrorStatus(w, http.StatusBadRequest, err)
}

func subDownloadErrorStatus(w http.ResponseWriter, status int, err error) {
	http.Error(w, err.Error()+"\n", status)
}

func (s *Server) runClientDownload(taskID string, spec clientDownloadSpec) {
	setTask := func(update func(*clientDownloadTask)) {
		s.downloadMu.Lock()
		if task := s.downloadTasks[taskID]; task != nil {
			update(task)
		}
		s.downloadMu.Unlock()
	}
	finish := func(status, message string) {
		setTask(func(task *clientDownloadTask) { task.Status, task.Error = status, message })
		s.downloadMu.Lock()
		if s.activeDownloads[spec.ID] == taskID {
			delete(s.activeDownloads, spec.ID)
		}
		s.downloadMu.Unlock()
	}
	setTask(func(task *clientDownloadTask) { task.Status = "downloading" })

	ctx := context.Background()
	targetDir := filepath.Join(s.cacheDir, spec.ID)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		finish("failed", "创建客户端缓存目录失败")
		return
	}
	targetPath := filepath.Join(targetDir, "latest.bin")
	metadataPath := filepath.Join(targetDir, "latest.json")
	release, asset, err := s.latestClientAsset(ctx, spec)
	if err != nil {
		finish("failed", err.Error())
		return
	}
	if asset.Size <= 0 || asset.Size > maxClientPackageBytes {
		finish("failed", "客户端安装包大小超出限制")
		return
	}
	setTask(func(task *clientDownloadTask) { task.Size, task.SourceURL = asset.Size, asset.BrowserDownloadURL })
	// 发布页提供的校验码（asset digest 或校验文件），用于下载后比对，防止文件损坏。
	expectedSHA256 := s.expectedClientSHA256(ctx, release, asset)
	if metadata, ok := readClientCacheMetadata(metadataPath); ok && metadata.VariantID == spec.ID && metadata.ReleaseTag == release.TagName && metadata.AssetURL == asset.BrowserDownloadURL && time.Since(metadata.DownloadedAt) < s.clientCacheTTL(ctx) {
		if info, statErr := os.Stat(targetPath); statErr == nil && info.Size() == metadata.Size {
			digest := metadata.SHA256
			if digest == "" {
				// 旧版本缓存元数据缺少校验码，补算一次。
				digest, _ = fileSHA256(targetPath)
			}
			if expectedSHA256 == "" || strings.EqualFold(digest, expectedSHA256) {
				setTask(func(task *clientDownloadTask) {
					task.Status, task.Progress, task.Size, task.Filename, task.FilePath, task.SHA256 = "done", 1, metadata.Size, metadata.Filename, targetPath, digest
				})
				finish("done", "")
				return
			}
			// 缓存文件与发布页校验码不一致，回退到重新下载。
		}
	}

	tmpPath := filepath.Join(targetDir, ".download-"+taskID)
	_ = os.Remove(tmpPath)
	err = s.downloadFiles.DownloadLimited(ctx, asset.BrowserDownloadURL, tmpPath, maxClientPackageBytes, func(progress float64) {
		setTask(func(task *clientDownloadTask) { task.Progress = progress })
	})
	if err != nil {
		_ = os.Remove(tmpPath)
		finish("failed", "下载客户端失败")
		return
	}
	info, err := os.Stat(tmpPath)
	if err != nil || info.Size() <= 0 || info.Size() > maxClientPackageBytes {
		_ = os.Remove(tmpPath)
		finish("failed", "下载文件校验失败")
		return
	}
	digest, err := fileSHA256(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		finish("failed", "计算文件校验码失败")
		return
	}
	if expectedSHA256 != "" && !strings.EqualFold(digest, expectedSHA256) {
		_ = os.Remove(tmpPath)
		finish("failed", "下载校验失败，文件与发布页校验码不一致")
		return
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		// Windows cannot rename over an existing file; retry after removing the
		// previous latest package while retaining the same per-variant target.
		if removeErr := os.Remove(targetPath); removeErr != nil || os.Rename(tmpPath, targetPath) != nil {
			_ = os.Remove(tmpPath)
			finish("failed", "写入客户端缓存失败")
			return
		}
	}
	metadata := clientCacheMetadata{VariantID: spec.ID, ReleaseTag: release.TagName, AssetURL: asset.BrowserDownloadURL,
		Filename: safeClientFilename(asset.Name), Size: info.Size(), SHA256: digest, DownloadedAt: time.Now().UTC()}
	metadataBytes, _ := json.Marshal(metadata)
	if err := os.WriteFile(metadataPath, metadataBytes, 0o600); err != nil {
		finish("failed", "写入客户端缓存信息失败")
		return
	}
	setTask(func(task *clientDownloadTask) {
		task.Status, task.Progress, task.Size, task.Filename, task.FilePath, task.SHA256 = "done", 1, info.Size(), metadata.Filename, targetPath, digest
	})
	finish("done", "")
}

// fileSHA256 流式计算文件的 SHA-256 校验码（十六进制），供下载方核对文件完整性。
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Server) latestClientAsset(ctx context.Context, spec clientDownloadSpec) (clientRelease, clientReleaseAsset, error) {
	body, err := s.downloadFiles.GetText(ctx, "https://api.github.com/repos/"+spec.Repo+"/releases/latest", 4<<20)
	if err != nil {
		return clientRelease{}, clientReleaseAsset{}, errors.New("读取客户端最新版本失败")
	}
	var release clientRelease
	if err := json.Unmarshal([]byte(body), &release); err != nil {
		return clientRelease{}, clientReleaseAsset{}, errors.New("解析客户端版本信息失败")
	}
	for _, asset := range release.Assets {
		if spec.Pattern.MatchString(asset.Name) {
			return release, asset, nil
		}
	}
	return clientRelease{}, clientReleaseAsset{}, errors.New("当前版本没有匹配的客户端安装包")
}

// expectedClientSHA256 取发布页为安装包提供的 SHA-256 校验码：
// 优先 GitHub API 的 asset digest 字段；否则在 release 中查找校验文件
// （<安装包名>.sha256、sha256sums、checksums 之类）并解析对应行。返回 "" 表示上游未提供。
func (s *Server) expectedClientSHA256(ctx context.Context, release clientRelease, asset clientReleaseAsset) string {
	if digest, ok := strings.CutPrefix(asset.Digest, "sha256:"); ok && isSHA256Hex(digest) {
		return strings.ToLower(digest)
	}
	for _, candidate := range release.Assets {
		if candidate.Name == asset.Name {
			continue
		}
		name := strings.ToLower(candidate.Name)
		companion := name == strings.ToLower(asset.Name)+".sha256"
		sums := strings.Contains(name, "sha256") || strings.Contains(name, "checksum")
		if (!companion && !sums) || candidate.Size <= 0 || candidate.Size > 1<<20 {
			continue
		}
		body, err := s.downloadFiles.GetText(ctx, candidate.BrowserDownloadURL, 1<<20)
		if err != nil {
			continue
		}
		if digest := findSHA256ForAsset(body, asset.Name); digest != "" {
			return digest
		}
	}
	return ""
}

var sha256HexPattern = regexp.MustCompile(`[0-9a-fA-F]{64}`)

// findSHA256ForAsset 从校验文件内容中解析安装包对应的 SHA-256：
// 匹配 "<hex>  <文件名>" 行；文件只含单个哈希（伴随文件）时直接取该值。
func findSHA256ForAsset(body, assetName string) string {
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, assetName) {
			continue
		}
		if digest := sha256HexPattern.FindString(line); digest != "" {
			return strings.ToLower(digest)
		}
	}
	if all := sha256HexPattern.FindAllString(body, 2); len(all) == 1 {
		return strings.ToLower(all[0])
	}
	return ""
}

func isSHA256Hex(value string) bool {
	return len(value) == 64 && sha256HexPattern.MatchString(value)
}

func readClientCacheMetadata(path string) (clientCacheMetadata, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return clientCacheMetadata{}, false
	}
	var metadata clientCacheMetadata
	if json.Unmarshal(b, &metadata) != nil || metadata.DownloadedAt.IsZero() || metadata.Filename == "" {
		return clientCacheMetadata{}, false
	}
	return metadata, true
}

func (s *Server) clientCacheTTL(ctx context.Context) time.Duration {
	value, err := s.st.GetSetting(ctx, store.SettingClientCacheTTL)
	if err != nil {
		return defaultClientCacheTTL
	}
	hours, err := strconv.Atoi(value)
	if err != nil || hours <= 0 {
		return defaultClientCacheTTL
	}
	if hours > 24*30 {
		hours = 24 * 30
	}
	return time.Duration(hours) * time.Hour
}

func safeClientFilename(name string) string {
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		if r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "client-package.bin"
	}
	return name
}
