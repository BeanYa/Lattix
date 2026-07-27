package panel

// 面板自更新：以 GitHub release 最新版本为标准检测更新，下载当前架构的单二进制
// 面板包，按 checksums.txt 校验后原子替换自身，最后经 restartSelf 重启完成切换。
//
// 进度经 GET /api/panel/update/status 轮询（阶段 + 百分比）；更新进行中
// requireAuth 拒绝其余 API 操作（423），防止用户在切换窗口内继续改动。
//
// 下载基址：默认 GitHub release（cfg.GitHubRepo）；环境变量 LATX_RELEASE_BASE
// 可覆盖为镜像根（e2e/运维用），布局为 <base>/<version>/<asset> + <base>/latest.txt。
// version 为 dev（非 release 构建）且无镜像基址时不可更新。

import (
	"archive/tar"
	"compress/gzip"
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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/logging"
)

// 更新阶段（panelUpdateStatus.Stage 取值）。
const (
	updStageCheck    = "check"    // 解析目标版本
	updStageDownload = "download" // 下载面板包
	updStageVerify   = "verify"   // checksums.txt SHA256 校验
	updStageExtract  = "extract"  // 解压面板包
	updStageApply    = "apply"    // 原子替换单二进制
	updStageRestart  = "restart"  // 自重启切换
	updStageDone     = "done"     // 完成（目标版本与当前一致时无需重启）
	updStageFailed   = "failed"
)

func currentPanelTarball() (string, error) {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return "lattix-panel-linux-" + runtime.GOARCH + ".tar.gz", nil
	default:
		return "", fmt.Errorf("不支持的更新架构: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

// panelUpdateStatus 是 GET /api/panel/update/status 的响应（更新进度快照）。
type panelUpdateStatus struct {
	Running        bool   `json:"running"`
	Stage          string `json:"stage"` // check|download|verify|extract|apply|restart|done|failed；空 = 未更新过
	Percent        int    `json:"percent"`
	Message        string `json:"message"`
	Error          string `json:"error,omitempty"`
	CurrentVersion string `json:"current_version"`
	TargetVersion  string `json:"target_version"`
}

// panelVersionInfo 是 GET /api/panel/version 的响应（更新检测结果）。
type panelVersionInfo struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	CanUpdate       bool   `json:"can_update"` // dev 构建且无镜像基址时 false
	Message         string `json:"message,omitempty"`
}

// panelUpdater 持有更新状态机；同时只允许一个更新流程。
type panelUpdater struct {
	s   *Server
	mu  sync.Mutex
	st  panelUpdateStatus
	ver panelVersionInfo // 最近一次检测结果缓存（status 之外给 version 端点复用）
}

func newPanelUpdater(s *Server) *panelUpdater {
	return &panelUpdater{s: s, st: panelUpdateStatus{CurrentVersion: s.cfg.Version}}
}

// releaseBase 返回下载基址根（镜像覆盖 > GitHub release download 根）。
func (s *Server) releaseBase() string {
	if b := strings.TrimRight(strings.TrimSpace(os.Getenv("LATX_RELEASE_BASE")), "/"); b != "" {
		return b
	}
	if s.cfg.GitHubRepo == "" {
		return ""
	}
	return "https://github.com/" + s.cfg.GitHubRepo + "/releases/download"
}

// resolveLatest 解析最新 release tag：镜像基址读 <base>/latest.txt，否则走 GitHub API。
func (s *Server) resolveLatest() (string, error) {
	if b := os.Getenv("LATX_RELEASE_BASE"); strings.TrimSpace(b) != "" {
		body, err := httpGet(s.releaseBase() + "/latest.txt")
		if err != nil {
			return "", fmt.Errorf("解析 latest 失败（镜像 latest.txt）: %w", err)
		}
		v := strings.TrimSpace(body)
		if v == "" {
			return "", errors.New("解析 latest 失败：镜像 latest.txt 为空")
		}
		return v, nil
	}
	if s.cfg.GitHubRepo == "" {
		return "", errors.New("未配置 GitHub 仓库，无法解析 latest")
	}
	body, err := httpGet("https://api.github.com/repos/" + s.cfg.GitHubRepo + "/releases/latest")
	if err != nil {
		return "", fmt.Errorf("解析 latest 失败: %w", err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal([]byte(body), &rel); err != nil || rel.TagName == "" {
		return "", fmt.Errorf("解析 latest 失败：无法读取 tag_name")
	}
	return rel.TagName, nil
}

// canUpdate 报告面板是否具备自更新条件（非 dev 构建，或配置了镜像基址）。
func (s *Server) canUpdate() bool {
	return s.cfg.Version != "dev" || strings.TrimSpace(os.Getenv("LATX_RELEASE_BASE")) != ""
}

// handlePanelVersion 处理 GET /api/panel/version：以 GitHub release 最新版本检测更新。
func (s *Server) handlePanelVersion(w http.ResponseWriter, r *http.Request) {
	info := panelVersionInfo{Current: s.cfg.Version, CanUpdate: s.canUpdate()}
	if !info.CanUpdate {
		info.Message = "dev 构建无对应 release，无法检测更新"
		writeJSON(w, http.StatusOK, info)
		return
	}
	latest, err := s.resolveLatest()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	info.Latest = latest
	info.UpdateAvailable = latest != s.cfg.Version
	if !info.UpdateAvailable {
		info.Message = "已是最新版本"
	}
	s.upd.mu.Lock()
	s.upd.ver = info
	s.upd.mu.Unlock()
	writeJSON(w, http.StatusOK, info)
}

// handlePanelUpdateStart 处理 POST /api/panel/update：启动自更新（异步）。
// body 可指定 {"version":"vX.Y.Z"}，缺省更新到 latest。
func (s *Server) handlePanelUpdateStart(w http.ResponseWriter, r *http.Request) {
	if !s.canUpdate() {
		writeError(w, http.StatusBadRequest, "dev 构建无对应 release，无法自更新")
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if r.Body != nil {
		_ = readJSON(r, &req) // 空 body 合法（默认 latest）
	}
	u := s.upd
	u.mu.Lock()
	if u.st.Running {
		u.mu.Unlock()
		writeError(w, http.StatusConflict, "面板更新已在进行中")
		return
	}
	u.st = panelUpdateStatus{
		Running:        true,
		Stage:          updStageCheck,
		Message:        "解析目标版本…",
		CurrentVersion: s.cfg.Version,
		TargetVersion:  strings.TrimSpace(req.Version),
	}
	u.mu.Unlock()

	s.audit(r, "panel.update_started", nil, nil, map[string]any{
		"current_version": s.cfg.Version,
		"target_version":  strings.TrimSpace(req.Version),
	})
	go u.run()
	writeJSON(w, http.StatusAccepted, u.snapshot())
}

// handlePanelUpdateStatus 处理 GET /api/panel/update/status（更新进行中豁免 423 守卫）。
func (s *Server) handlePanelUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.upd.snapshot())
}

func (u *panelUpdater) snapshot() panelUpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

// running 报告更新是否进行中（requireAuth 守卫用）。
func (u *panelUpdater) running() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st.Running
}

func (u *panelUpdater) setStage(stage string, percent int, msg string) {
	u.mu.Lock()
	u.st.Stage = stage
	u.st.Percent = percent
	u.st.Message = msg
	u.mu.Unlock()
	log.Printf("panel update: [%s] %d%% %s", stage, percent, msg)
}

func (u *panelUpdater) fail(err error) {
	u.mu.Lock()
	u.st.Running = false
	u.st.Stage = updStageFailed
	u.st.Error = err.Error()
	u.st.Message = "更新失败"
	target := u.st.TargetVersion
	u.mu.Unlock()
	log.Printf("panel update: failed: %v", err)
	if logErr := u.s.recordOperation(context.Background(), logging.OperationEvent{
		Severity: logging.SeverityError, Category: logging.CategoryPanel, Action: "panel.update_failed",
		Detail: map[string]any{"error": err.Error(), "target_version": target},
	}); logErr != nil {
		log.Printf("panel update: record failure: %v", logErr)
	}
}

// run 执行更新流程：check → download → verify → extract → apply → restart。
// 失败时停留在 failed 状态（面板保持旧版本运行），成功则进程退出完成切换。
func (u *panelUpdater) run() {
	s := u.s
	st := u.snapshot()

	// --- check：解析目标版本 ---
	target := st.TargetVersion
	if target == "" || target == "latest" {
		v, err := s.resolveLatest()
		if err != nil {
			u.fail(err)
			return
		}
		target = v
	}
	if !strings.HasPrefix(target, "v") {
		u.fail(fmt.Errorf("版本号须形如 vX.Y.Z: %s", target))
		return
	}
	u.mu.Lock()
	u.st.TargetVersion = target
	u.mu.Unlock()
	if target == st.CurrentVersion {
		u.mu.Lock()
		u.st.Running = false
		u.st.Stage = updStageDone
		u.st.Percent = 100
		u.st.Message = "已是最新版本，无需更新"
		u.mu.Unlock()
		if err := s.recordOperation(context.Background(), logging.OperationEvent{
			Severity: logging.SeverityInfo, Category: logging.CategoryPanel, Action: "panel.update_skipped",
			Detail: map[string]string{"version": target, "reason": "already_current"},
		}); err != nil {
			log.Printf("panel update: record skipped update: %v", err)
		}
		return
	}
	u.setStage(updStageCheck, 100, "目标版本 "+target)

	// 工作目录放在二进制同级（通常即安装根目录）：与替换目标同文件系统，
	// 保证后续 rename 原子交换可用；失败时整体清理，不污染部署。
	exe, err := os.Executable()
	if err != nil {
		u.fail(err)
		return
	}
	exe = strings.TrimSuffix(exe, " (deleted)") // 运行期间二进制被替换过
	if exe, err = filepath.Abs(exe); err != nil {
		u.fail(err)
		return
	}
	work, err := os.MkdirTemp(filepath.Dir(exe), ".panel-update-*")
	if err != nil {
		u.fail(fmt.Errorf("创建工作目录失败: %w", err))
	}

	base := s.releaseBase() + "/" + target
	panelTarball, err := currentPanelTarball()
	if err != nil {
		os.RemoveAll(work)
		u.fail(err)
		return
	}
	assets := []string{panelTarball}

	// --- download：先 checksums.txt（校验依据，获取不到即中止），再逐一资产带进度下载 ---
	u.setStage(updStageDownload, 0, "下载 checksums.txt")
	sumsPath := filepath.Join(work, "checksums.txt")
	if err := downloadFile(base+"/checksums.txt", sumsPath, nil); err != nil {
		os.RemoveAll(work)
		u.fail(fmt.Errorf("未获取到 release 校验文件 checksums.txt，中止更新: %w", err))
		return
	}
	for i, asset := range assets {
		basePct := (i + 1) * 100 / (len(assets) + 1) // checksums 占一份
		msg := fmt.Sprintf("下载 %s（%d/%d）", asset, i+1, len(assets))
		u.setStage(updStageDownload, basePct, msg)
		onProgress := func(frac float64) {
			p := basePct
			if frac >= 0 {
				p = basePct - 100/(len(assets)+1) + int(frac*float64(100/(len(assets)+1)))
			}
			u.mu.Lock()
			u.st.Percent = p
			u.mu.Unlock()
		}
		if err := downloadFile(base+"/"+asset, filepath.Join(work, asset), onProgress); err != nil {
			os.RemoveAll(work)
			u.fail(fmt.Errorf("下载失败 %s/%s: %w", base, asset, err))
			return
		}
	}
	u.setStage(updStageDownload, 100, "下载完成")

	// --- verify：全部资产过 checksums.txt（与安装脚本同规，不降级跳过）---
	u.setStage(updStageVerify, 50, "SHA256 校验更新包")
	for _, asset := range assets {
		if err := verifyAsset(sumsPath, asset, filepath.Join(work, asset)); err != nil {
			os.RemoveAll(work)
			u.fail(err)
			return
		}
	}
	u.setStage(updStageVerify, 100, "SHA256 校验通过")

	// --- extract：解压面板包 ---
	u.setStage(updStageExtract, 50, "解压面板包")
	if err := untargz(filepath.Join(work, panelTarball), work); err != nil {
		os.RemoveAll(work)
		u.fail(fmt.Errorf("解压面板包失败: %w", err))
		return
	}
	pkg := filepath.Join(work, "lattix-panel")
	newBin := filepath.Join(pkg, "lattix-backend")
	if !fileExists(newBin) {
		os.RemoveAll(work)
		u.fail(errors.New("面板包内容异常（缺 lattix-backend）"))
		return
	}
	u.setStage(updStageExtract, 100, "解压完成")

	// --- apply：预检新二进制 → 原子替换当前二进制 ---
	u.setStage(updStageApply, 10, "预检新版本二进制")
	if err := os.Chmod(newBin, 0o755); err != nil {
		os.RemoveAll(work)
		u.fail(fmt.Errorf("新二进制赋可执行权限失败: %w", err))
		return
	}
	// 预检：新二进制须能运行并打印目标版本，否则放弃替换——
	// 无运行时回滚，坏二进制会让 systemd 陷入崩溃循环（与 agent 自升级同规）。
	out, err := exec.Command(newBin, "-version").CombinedOutput()
	if v := strings.TrimSpace(string(out)); err != nil || v != target {
		os.RemoveAll(work)
		u.fail(fmt.Errorf("新二进制自检失败（期望 %s，实际 %q）: %v", target, v, err))
		return
	}

	u.setStage(updStageApply, 60, "原子替换面板二进制")
	if err := replaceExecutable(newBin, exe); err != nil {
		os.RemoveAll(work)
		u.fail(fmt.Errorf("替换面板二进制失败: %w", err))
		return
	}
	u.setStage(updStageApply, 100, "文件替换完成")
	if err := s.recordOperation(context.Background(), logging.OperationEvent{
		Severity: logging.SeverityInfo, Category: logging.CategoryPanel, Action: "panel.update_succeeded",
		Detail: map[string]string{"from": st.CurrentVersion, "to": target},
	}); err != nil {
		log.Printf("panel update: record success: %v", err)
	}
	os.RemoveAll(work) // restart 会 os.Exit，defer 清理不会执行，提前清理

	// --- restart：自重启切换到新版本（systemd 拉起或自派生，见 restartSelf）---
	u.setStage(updStageRestart, 100, "重启面板完成切换…")
	// 留出窗口让前端轮询到 restart 状态后再退出进程。
	time.Sleep(1500 * time.Millisecond)
	if s.cfg.RequestRestart == nil {
		u.fail(errors.New("重启回调未配置"))
		return
	}
	s.cfg.RequestRestart("update")
}

// httpGet 拉取 URL 全部内容（小文件：API 响应/latest.txt），非 200 报错。
func httpGet(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b), err
}

// downloadFile 下载 URL 到本地文件；onProgress 回调进度比例（0~1，Content-Length
// 未知时不回调比例、由调用方按阶段估算）。整体 10 分钟超时。
func downloadFile(url, path string, onProgress func(frac float64)) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	total := resp.ContentLength
	buf := make([]byte, 64*1024)
	var done int64
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if onProgress != nil && total > 0 {
				onProgress(float64(done) / float64(total))
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// verifyAsset 在 checksums.txt（sha256sum 标准格式）中查 asset 的期望值并校验文件。
func verifyAsset(sumsPath, asset, filePath string) error {
	b, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("checksums.txt 中未找到 %s", asset)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if actual := hex.EncodeToString(h.Sum(nil)); actual != expected {
		return fmt.Errorf("%s SHA256 校验失败（期望 %s，实际 %s）", asset, expected, actual)
	}
	return nil
}

// untargz 解压 .tar.gz 到目录（路径限制在目标目录内，防 tar slip）。
func untargz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p := filepath.Join(dest, filepath.Clean(hdr.Name))
		if !strings.HasPrefix(p, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("tar 包含越界路径: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// replaceExecutable keeps the old executable as .bak and swaps the new file
// into place with same-filesystem renames. This works for both systemd and a
// Docker container writable layer while the old inode is still executing.
func replaceExecutable(src, dest string) error {
	next := dest + ".new"
	backup := dest + ".bak"
	if err := copyFile(src, next, 0o755); err != nil {
		return err
	}
	_ = os.Remove(backup)
	if err := os.Rename(dest, backup); err != nil {
		_ = os.Remove(next)
		return fmt.Errorf("备份当前二进制失败: %w", err)
	}
	if err := os.Rename(next, dest); err != nil {
		_ = os.Rename(backup, dest)
		return fmt.Errorf("安装新二进制失败: %w", err)
	}
	return nil
}

// copyFile 复制文件并设置权限（先写临时文件再原子 rename）。
func copyFile(src, dest string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// replaceDir 用 src 目录整体替换 dst：先 rename 旧目录到旁边（备份），再 rename
// 新目录就位，最后删备份——中间窗口内旧内容仍可用；跨文件系统时回退为递归复制。
func replaceDir(src, dst string) error {
	backup := dst + ".old-" + time.Now().Format("20060102150405")
	hadOld := false
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, backup); err != nil {
			// 跨文件系统等 rename 失败场景：回退递归复制（先清空再拷入）。
			if rerr := os.RemoveAll(dst); rerr != nil {
				return fmt.Errorf("清空旧目录失败: %w（rename 失败: %v）", rerr, err)
			}
			return copyDir(src, dst)
		}
		hadOld = true
	}
	if err := os.Rename(src, dst); err != nil {
		if hadOld {
			os.Rename(backup, dst) // 尽力恢复旧目录
		}
		return copyDir(src, dst)
	}
	if hadOld {
		os.RemoveAll(backup)
	}
	return nil
}

// copyDir 递归复制目录（replaceDir 的跨文件系统回退）。
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target, info.Mode()&0o755)
	})
}
