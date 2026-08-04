package sub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lattix/backend/internal/store"
	"lattix/shared"
)

const (
	defaultClientCacheTTL = 72 * time.Hour
	maxClientPackageBytes = int64(512 << 20)
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
}

type clientCacheMetadata struct {
	VariantID    string    `json:"variant_id"`
	ReleaseTag   string    `json:"release_tag"`
	AssetURL     string    `json:"asset_url"`
	Filename     string    `json:"filename"`
	Size         int64     `json:"size"`
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
	Error     string
	CreatedAt time.Time
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
			s.downloadMu.Unlock()
			writeClientDownloadJSON(w, http.StatusOK, clientDownloadTaskResponse{TaskID: task.ID, Status: task.Status})
			return
		}
	}
	task := &clientDownloadTask{ID: shared.NewMessageID(), VariantID: spec.ID, Status: "queued", CreatedAt: time.Now().UTC()}
	s.downloadTasks[task.ID] = task
	s.activeDownloads[spec.ID] = task.ID
	s.downloadMu.Unlock()

	go s.runClientDownload(task.ID, spec)
	writeClientDownloadJSON(w, http.StatusAccepted, clientDownloadTaskResponse{TaskID: task.ID, Status: task.Status})
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
	resp := clientDownloadTaskResponse{TaskID: task.ID, Status: task.Status, Progress: task.Progress, Size: task.Size, Filename: task.Filename, Error: task.Error}
	s.downloadMu.Unlock()
	writeClientDownloadJSON(w, http.StatusOK, resp)
}

func writeClientDownloadJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) HandleSubClientDownloadFile(w http.ResponseWriter, r *http.Request) {
	if _, err := s.subDownloadUser(r); err != nil {
		subDownloadError(w, err)
		return
	}
	taskID := r.URL.Query().Get("task")
	s.downloadMu.Lock()
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

type clientDownloadTaskResponse struct {
	TaskID   string  `json:"task_id"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Size     int64   `json:"size"`
	Filename string  `json:"filename,omitempty"`
	Error    string  `json:"error,omitempty"`
}

func (s *Server) subDownloadUser(r *http.Request) (*store.User, error) {
	user, err := s.st.UserBySubToken(r.Context(), r.PathValue("token"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("订阅不存在")
		}
		return nil, err
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
	setTask(func(task *clientDownloadTask) { task.Size = asset.Size })
	if metadata, ok := readClientCacheMetadata(metadataPath); ok && metadata.VariantID == spec.ID && metadata.ReleaseTag == release.TagName && metadata.AssetURL == asset.BrowserDownloadURL && time.Since(metadata.DownloadedAt) < s.clientCacheTTL(ctx) {
		if info, statErr := os.Stat(targetPath); statErr == nil && info.Size() == metadata.Size {
			setTask(func(task *clientDownloadTask) {
				task.Status, task.Progress, task.Size, task.Filename, task.FilePath = "done", 1, metadata.Size, metadata.Filename, targetPath
			})
			finish("done", "")
			return
		}
	}

	tmpPath := filepath.Join(targetDir, ".download-"+taskID)
	_ = os.Remove(tmpPath)
	err = s.downloadFiles.Download(ctx, asset.BrowserDownloadURL, tmpPath, func(progress float64) {
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
		Filename: safeClientFilename(asset.Name), Size: info.Size(), DownloadedAt: time.Now().UTC()}
	metadataBytes, _ := json.Marshal(metadata)
	if err := os.WriteFile(metadataPath, metadataBytes, 0o600); err != nil {
		finish("failed", "写入客户端缓存信息失败")
		return
	}
	setTask(func(task *clientDownloadTask) {
		task.Status, task.Progress, task.Size, task.Filename, task.FilePath = "done", 1, info.Size(), metadata.Filename, targetPath
	})
	finish("done", "")
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
