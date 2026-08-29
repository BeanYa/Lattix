package sub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func TestClientDownloadSpecsMatchCurrentReleaseAssets(t *testing.T) {
	tests := map[string]string{
		"clash-verge-windows-x64":           "Clash.Verge_2.5.2_x64-setup.exe",
		"clash-verge-macos-arm64":           "Clash.Verge_2.5.2_aarch64.dmg",
		"mihomo-party-windows-x64":          "mihomo-party-windows-2.0.0-x64-setup.exe",
		"mihomo-party-windows-x64-portable": "mihomo-party-windows-2.0.0-x64-portable.7z",
		"flclash-android-armv7":             "FlClash-0.8.94-android-armeabi-v7a.apk",
		"surfboard-android-x86":             "mobile-x86-release.apk",
		"singbox-android-x86":               "SFA-1.13.16-x86.apk",
		"v2rayng-android-x86":               "v2rayNG_2.2.6_x86.apk",
		"nekobox-android-x86":               "NekoBox-1.4.2-x86.apk",
	}
	for id, filename := range tests {
		spec, ok := clientDownloadSpecs[id]
		if !ok {
			t.Fatalf("missing client download spec %q", id)
		}
		if !spec.Pattern.MatchString(filename) {
			t.Errorf("spec %q does not match %q", id, filename)
		}
	}
}

func TestSafeClientFilename(t *testing.T) {
	if got := safeClientFilename(`../../client".zip`); got != "client_.zip" {
		t.Fatalf("safeClientFilename() = %q", got)
	}
	if got := safeClientFilename(""); got != "client-package.bin" {
		t.Fatalf("empty filename = %q", got)
	}
}

type cacheProbeRequester struct {
	releaseJSON     string
	downloadContent []byte
	metadataCalls   int
	downloadCalls   int
}

func (f *cacheProbeRequester) GetText(context.Context, string, int64) (string, error) {
	f.metadataCalls++
	if f.releaseJSON == "" {
		return "", errors.New("missing release fixture")
	}
	return f.releaseJSON, nil
}

func (f *cacheProbeRequester) Download(ctx context.Context, url, path string, onProgress func(float64)) error {
	return f.DownloadLimited(ctx, url, path, 0, onProgress)
}

func (f *cacheProbeRequester) DownloadLimited(_ context.Context, _ string, path string, _ int64, onProgress func(float64)) error {
	f.downloadCalls++
	if f.downloadContent == nil {
		return errors.New("download fixture is not configured")
	}
	if err := os.WriteFile(path, f.downloadContent, 0o600); err != nil {
		return err
	}
	if onProgress != nil {
		onProgress(1)
	}
	return nil
}

func TestClientDownloadReusesCacheWhenReleaseAssetIsUnchanged(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cacheDir := t.TempDir()
	server := NewWithCacheDir(st, nil, nil, cacheDir)
	spec := clientDownloadSpecs["flclash-android-arm64"]
	probe := &cacheProbeRequester{releaseJSON: `{"tag_name":"v0.8.94","assets":[{"name":"FlClash-0.8.94-android-arm64-v8a.apk","browser_download_url":"https://example.invalid/cached.apk","size":14}]}`}
	server.downloadFiles = probe
	task := &clientDownloadTask{ID: "cache-hit-task", VariantID: spec.ID, Status: "queued", CreatedAt: time.Now().UTC()}
	server.downloadTasks[task.ID] = task
	server.activeDownloads[spec.ID] = task.ID

	targetDir := filepath.Join(cacheDir, spec.ID)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("cached package")
	if err := os.WriteFile(filepath.Join(targetDir, "latest.bin"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(clientCacheMetadata{
		VariantID: spec.ID, ReleaseTag: "v0.8.94", AssetURL: "https://example.invalid/cached.apk", Filename: "cached.apk",
		Size: int64(len(content)), DownloadedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "latest.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}

	server.runClientDownload(task.ID, spec)
	if task.Status != "done" || task.Progress != 1 || task.Filename != "cached.apk" {
		t.Fatalf("cache task = %+v", task)
	}
	if probe.metadataCalls != 1 || probe.downloadCalls != 0 {
		t.Fatalf("upstream calls on cache hit: metadata=%d download=%d", probe.metadataCalls, probe.downloadCalls)
	}
}

func TestClientDownloadReplacesCacheWhenReleaseAssetChanges(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cacheDir := t.TempDir()
	server := NewWithCacheDir(st, nil, nil, cacheDir)
	spec := clientDownloadSpecs["flclash-android-arm64"]
	newContent := []byte("new package")
	probe := &cacheProbeRequester{
		releaseJSON:     `{"tag_name":"v0.8.95","assets":[{"name":"FlClash-0.8.95-android-arm64-v8a.apk","browser_download_url":"https://example.invalid/new.apk","size":11}]}`,
		downloadContent: newContent,
	}
	server.downloadFiles = probe
	task := &clientDownloadTask{ID: "cache-replace-task", VariantID: spec.ID, Status: "queued", CreatedAt: time.Now().UTC()}
	server.downloadTasks[task.ID] = task
	server.activeDownloads[spec.ID] = task.ID

	targetDir := filepath.Join(cacheDir, spec.ID)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldContent := []byte("old package")
	if err := os.WriteFile(filepath.Join(targetDir, "latest.bin"), oldContent, 0o600); err != nil {
		t.Fatal(err)
	}
	oldMetadata, err := json.Marshal(clientCacheMetadata{
		VariantID: spec.ID, ReleaseTag: "v0.8.94", AssetURL: "https://example.invalid/old.apk", Filename: "old.apk",
		Size: int64(len(oldContent)), DownloadedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "latest.json"), oldMetadata, 0o600); err != nil {
		t.Fatal(err)
	}

	server.runClientDownload(task.ID, spec)
	gotContent, err := os.ReadFile(filepath.Join(targetDir, "latest.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContent) != string(newContent) || task.Status != "done" || probe.downloadCalls != 1 {
		t.Fatalf("replacement task=%+v content=%q downloads=%d", task, gotContent, probe.downloadCalls)
	}
	metadata, ok := readClientCacheMetadata(filepath.Join(targetDir, "latest.json"))
	if !ok || metadata.AssetURL != "https://example.invalid/new.apk" || metadata.Filename != "FlClash-0.8.95-android-arm64-v8a.apk" {
		t.Fatalf("replacement metadata=%+v ok=%v", metadata, ok)
	}
}

func TestClientDownloadTicketFlow(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.InsertUser(ctx, "dave", "00000000-0000-0000-0000-0000000000d1", "dave-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertUser(ctx, "eve", "00000000-0000-0000-0000-0000000000e1", "eve-token", nil); err != nil {
		t.Fatal(err)
	}

	cacheDir := t.TempDir()
	server := NewWithCacheDir(st, nil, nil, cacheDir)
	content := []byte("package content")
	pkgPath := filepath.Join(cacheDir, "pkg.bin")
	if err := os.WriteFile(pkgPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	server.downloadTasks["ticket-task"] = &clientDownloadTask{
		ID: "ticket-task", Status: "done", Progress: 1, Size: int64(len(content)),
		Filename: "pkg.bin", FilePath: pkgPath, CreatedAt: time.Now().UTC(),
	}
	server.downloadTasks["pending-task"] = &clientDownloadTask{ID: "pending-task", Status: "downloading", CreatedAt: time.Now().UTC()}

	newReq := func(token, target string) *http.Request {
		req := httptest.NewRequest("GET", target, nil)
		req.SetPathValue("token", token)
		return req
	}

	// 未完成的任务不能签发票据。
	rec := httptest.NewRecorder()
	server.HandleSubClientDownloadTicket(rec, newReq("dave-token", "/api/sub/dave-token/client-download/ticket?task=pending-task"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ticket for pending task: status = %d", rec.Code)
	}

	// 完成的任务签发票据。
	rec = httptest.NewRecorder()
	server.HandleSubClientDownloadTicket(rec, newReq("dave-token", "/api/sub/dave-token/client-download/ticket?task=ticket-task"))
	if rec.Code != http.StatusOK {
		t.Fatalf("ticket issue: status = %d body = %s", rec.Code, rec.Body)
	}
	var ticketResp clientDownloadTicketResponse
	if err := json.NewDecoder(rec.Body).Decode(&ticketResp); err != nil || ticketResp.Ticket == "" {
		t.Fatalf("ticket response = %+v err = %v", ticketResp, err)
	}
	fileURL := "/api/sub/dave-token/client-download/file?task=ticket-task&ticket=" + ticketResp.Ticket

	// 无票据 → 403。
	rec = httptest.NewRecorder()
	server.HandleSubClientDownloadFile(rec, newReq("dave-token", "/api/sub/dave-token/client-download/file?task=ticket-task"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("file without ticket: status = %d", rec.Code)
	}

	// 有效票据 → 200，内容一致。
	rec = httptest.NewRecorder()
	server.HandleSubClientDownloadFile(rec, newReq("dave-token", fileURL))
	if rec.Code != http.StatusOK || rec.Body.String() != string(content) {
		t.Fatalf("file with ticket: status = %d body = %q", rec.Code, rec.Body)
	}

	// 同一票据可发起 Range 请求（断点续传）→ 206。
	rangeReq := newReq("dave-token", fileURL)
	rangeReq.Header.Set("Range", "bytes=0-6")
	rec = httptest.NewRecorder()
	server.HandleSubClientDownloadFile(rec, rangeReq)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "package" {
		t.Fatalf("range request: status = %d body = %q", rec.Code, rec.Body)
	}

	// 票据绑定订阅 token，跨订阅使用 → 403。
	rec = httptest.NewRecorder()
	server.HandleSubClientDownloadFile(rec, newReq("eve-token", strings.ReplaceAll(fileURL, "dave-token", "eve-token")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("file with foreign token: status = %d", rec.Code)
	}

	// 过期票据 → 403。
	server.downloadTickets["expired-ticket"] = &clientDownloadTicket{
		TaskID: "ticket-task", Token: "dave-token", ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}
	rec = httptest.NewRecorder()
	server.HandleSubClientDownloadFile(rec, newReq("dave-token", "/api/sub/dave-token/client-download/file?task=ticket-task&ticket=expired-ticket"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("file with expired ticket: status = %d", rec.Code)
	}
}

func TestClientDownloadVerifiesUpstreamSHA256(t *testing.T) {
	spec := clientDownloadSpecs["flclash-android-arm64"]
	content := []byte("verified package")
	sum := sha256.Sum256(content)
	goodDigest := hex.EncodeToString(sum[:])
	assetJSON := func(digest string) string {
		return `{"tag_name":"v0.8.94","assets":[{"name":"FlClash-0.8.94-android-arm64-v8a.apk","browser_download_url":"https://example.invalid/pkg.apk","size":16,"digest":"sha256:` + digest + `"}]}`
	}
	newTask := func(server *Server, id string) *clientDownloadTask {
		task := &clientDownloadTask{ID: id, VariantID: spec.ID, Status: "queued", CreatedAt: time.Now().UTC()}
		server.downloadTasks[id] = task
		server.activeDownloads[spec.ID] = id
		return task
	}

	// 校验码一致：任务完成并回显 SHA-256。
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := NewWithCacheDir(st, nil, nil, t.TempDir())
	server.downloadFiles = &cacheProbeRequester{releaseJSON: assetJSON(goodDigest), downloadContent: content}
	task := newTask(server, "verify-ok")
	server.runClientDownload(task.ID, spec)
	if task.Status != "done" || task.SHA256 != goodDigest || task.SourceURL != "https://example.invalid/pkg.apk" {
		t.Fatalf("matching digest: task = %+v", task)
	}

	// 校验码不一致：任务失败且不写入缓存。
	st2, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	server2 := NewWithCacheDir(st2, nil, nil, t.TempDir())
	server2.downloadFiles = &cacheProbeRequester{
		releaseJSON:     assetJSON("0000000000000000000000000000000000000000000000000000000000000000"),
		downloadContent: content,
	}
	task2 := newTask(server2, "verify-bad")
	server2.runClientDownload(task2.ID, spec)
	if task2.Status != "failed" || !strings.Contains(task2.Error, "校验码不一致") {
		t.Fatalf("mismatching digest: task = %+v", task2)
	}
	if _, err := os.Stat(filepath.Join(server2.cacheDir, spec.ID, "latest.bin")); !os.IsNotExist(err) {
		t.Fatalf("mismatching digest should not persist cache, stat err = %v", err)
	}
}

func TestFindSHA256ForAsset(t *testing.T) {
	digest := strings.Repeat("a", 64)
	sums := digest + "  FlClash-0.8.94-android-arm64-v8a.apk\n" + strings.Repeat("b", 64) + "  other.apk\n"
	if got := findSHA256ForAsset(sums, "FlClash-0.8.94-android-arm64-v8a.apk"); got != digest {
		t.Fatalf("sums file: got %q", got)
	}
	if got := findSHA256ForAsset(digest+"\n", "pkg.apk"); got != digest {
		t.Fatalf("companion file: got %q", got)
	}
	if got := findSHA256ForAsset("no hashes here", "pkg.apk"); got != "" {
		t.Fatalf("no match: got %q", got)
	}
}

// blockingDownloadRequester 卡住上游请求，让下载任务保持活跃，
// 避免限流测试触发真实网络或任务提前结束影响去重分支。
type blockingDownloadRequester struct {
	block chan struct{}
}

func (b *blockingDownloadRequester) GetText(context.Context, string, int64) (string, error) {
	<-b.block
	return "", errors.New("unblocked")
}

func (b *blockingDownloadRequester) Download(ctx context.Context, url, path string, onProgress func(float64)) error {
	return b.DownloadLimited(ctx, url, path, 0, onProgress)
}

func (b *blockingDownloadRequester) DownloadLimited(context.Context, string, string, int64, func(float64)) error {
	<-b.block
	return errors.New("unblocked")
}

func TestClientDownloadLimiterWindow(t *testing.T) {
	current := time.Now()
	limiter := newClientDownloadLimiter()
	limiter.now = func() time.Time { return current }

	// 窗口内第 11 次新建被拒。
	for i := 0; i < maxClientDownloadTasksPerTokenHour; i++ {
		if _, allowed := limiter.allow("token-a"); !allowed {
			t.Fatalf("allow #%d should pass", i+1)
		}
	}
	// 拒绝时返回窗口滑动到可用的等待时长（全部记录同一时刻落入 → 等满整个窗口）。
	if retryAfter, allowed := limiter.allow("token-a"); allowed || retryAfter != clientDownloadLimitWindow {
		t.Fatalf("11th task within the window = (%s, %t), want (%s, false)", retryAfter, allowed, clientDownloadLimitWindow)
	}
	// 不同 token 互不影响。
	if _, allowed := limiter.allow("token-b"); !allowed {
		t.Fatal("other token should have its own window")
	}
	// 窗口滑动后放行。
	current = current.Add(clientDownloadLimitWindow + time.Second)
	if _, allowed := limiter.allow("token-a"); !allowed {
		t.Fatal("should pass after the window slides")
	}
}

func TestHandleSubClientDownloadStartRateLimit(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.InsertUser(ctx, "dave", "00000000-0000-0000-0000-0000000000d2", "dave-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertUser(ctx, "eve", "00000000-0000-0000-0000-0000000000e2", "eve-token", nil); err != nil {
		t.Fatal(err)
	}

	server := NewWithCacheDir(st, nil, nil, t.TempDir())
	block := make(chan struct{})
	server.downloadFiles = &blockingDownloadRequester{block: block}
	// 放行并等全部下载 goroutine 退出后再交回 t.TempDir 清理（cleanup LIFO，
	// 本回调先于 RemoveAll 执行），消除 RemoveAll 与 goroutine 的 MkdirAll
	// 并发造成的预存在 flake。
	t.Cleanup(func() {
		close(block)
		deadline := time.Now().Add(10 * time.Second)
		for {
			server.downloadMu.Lock()
			pending := 0
			for _, task := range server.downloadTasks {
				if task.Status == "queued" || task.Status == "downloading" {
					pending++
				}
			}
			server.downloadMu.Unlock()
			if pending == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("%d 个下载任务在测试结束前未退出", pending)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})

	variants := []string{
		"clash-verge-windows-x64", "clash-verge-windows-arm64", "clash-verge-macos-x64",
		"clash-verge-macos-arm64", "mihomo-party-windows-x64", "mihomo-party-windows-arm64",
		"flclash-android-arm64", "flclash-android-armv7", "flclash-android-x64",
		"flclash-windows-x64", "surfboard-android-arm64", "singbox-android-arm64",
	}
	start := func(token, variant string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/sub/"+token+"/client-download/start?variant="+variant, nil)
		req.SetPathValue("token", token)
		rec := httptest.NewRecorder()
		server.HandleSubClientDownloadStart(rec, req)
		return rec
	}

	// 同 variant 已有活跃任务时去重直接返回（200），不消耗限流额度。
	rec := start("dave-token", variants[0])
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first start: status = %d body = %s", rec.Code, rec.Body)
	}
	var first clientDownloadTaskResponse
	if err := json.NewDecoder(rec.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	rec = start("dave-token", variants[0])
	if rec.Code != http.StatusOK {
		t.Fatalf("dedup start: status = %d body = %s", rec.Code, rec.Body)
	}
	var dup clientDownloadTaskResponse
	if err := json.NewDecoder(rec.Body).Decode(&dup); err != nil || dup.TaskID != first.TaskID {
		t.Fatalf("dedup start should return the active task: resp = %+v err = %v", dup, err)
	}
	if got := len(server.downloadLimiter.windows["dave-token"].timestamps); got != 1 {
		t.Fatalf("dedup path should not consume quota: counted = %d", got)
	}

	// 窗口内新建至上限均放行。
	for _, variant := range variants[1:maxClientDownloadTasksPerTokenHour] {
		if rec := start("dave-token", variant); rec.Code != http.StatusAccepted {
			t.Fatalf("start %s: status = %d body = %s", variant, rec.Code, rec.Body)
		}
	}
	// 超出窗口上限 → 429 + Retry-After + 业务化文案（不泄露内部细节）。
	rec = start("dave-token", variants[maxClientDownloadTasksPerTokenHour])
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit start: status = %d body = %s", rec.Code, rec.Body)
	}
	if retryAfter := rec.Header().Get("Retry-After"); retryAfter == "" || retryAfter == "0" {
		t.Fatalf("over-limit response Retry-After = %q, want 正数秒", retryAfter)
	}
	if !strings.Contains(rec.Body.String(), "过于频繁") {
		t.Fatalf("over-limit body = %q", rec.Body)
	}
	// 额度耗尽后，命中活跃任务的去重路径仍直接放行。
	if rec := start("dave-token", variants[0]); rec.Code != http.StatusOK {
		t.Fatalf("dedup start over limit: status = %d body = %s", rec.Code, rec.Body)
	}
	// 不同 token 互不影响。
	if rec := start("eve-token", variants[maxClientDownloadTasksPerTokenHour+1]); rec.Code != http.StatusAccepted {
		t.Fatalf("other token start: status = %d body = %s", rec.Code, rec.Body)
	}
}
