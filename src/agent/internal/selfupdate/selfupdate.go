// Package selfupdate 实现 agent 自升级（§18 upgrade_agent 命令）：
// 从 GitHub release 下载目标版本二进制与 checksums.txt，校验 SHA256 后
// 原子替换自身二进制与同包的 latx-ag；调用方回执后退出进程，由服务管理器拉起新版。
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lattix/agent/internal/fileutil"
	external "lattix/shared/requester"
)

const httpTimeout = 120 * time.Second

// Apply 将 agent 自身升级到 version（vX.Y.Z 或 latest，latest 经 GitHub API 解析）。
// releaseBase 形如 https://github.com/<org>/<repo>/releases/download；空时用 defaultRepo。
// 返回 upgraded=true 表示二进制已替换，调用方回执后应退出进程（systemd 拉起完成切换）；
// 目标版本与当前一致且 force=false 时返回 upgraded=false（幂等，无需重启）。
func Apply(version, releaseBase, currentVersion, defaultRepo string, force bool) (upgraded bool, err error) {
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	exe = strings.TrimSuffix(exe, " (deleted)")
	exe, err = filepath.Abs(exe)
	if err != nil {
		return false, err
	}
	return applyTo(version, releaseBase, currentVersion, defaultRepo, exe, force)
}

func applyTo(version, releaseBase, currentVersion, defaultRepo, executable string, force bool) (upgraded bool, err error) {
	base := releaseBase
	repo := defaultRepo
	if base == "" {
		base = "https://github.com/" + repo + "/releases/download"
	} else if strings.Contains(base, "github.com/") {
		// GitHub 基址时从中推导仓库路径（latest 解析用）；镜像基址无法推导。
		rest := strings.SplitN(base, "github.com/", 2)[1]
		repo = strings.TrimSuffix(strings.TrimSuffix(rest, "/releases/download"), "/")
	}
	if err := validateReleaseBase(base); err != nil {
		return false, err
	}

	if version == "" || version == "latest" {
		if repo == "" {
			return false, fmt.Errorf("使用镜像下载基址时须显式指定版本（vX.Y.Z），不支持 latest")
		}
		v, err := resolveLatest("https://api.github.com/repos/" + repo)
		if err != nil {
			return false, err
		}
		version = v
	}
	if !strings.HasPrefix(version, "v") {
		return false, fmt.Errorf("版本号须形如 vX.Y.Z 或 latest: %s", version)
	}
	if version == currentVersion && !force {
		return false, nil // 已是目标版本，幂等
	}

	// release 资产为 tarball（lattix-agent-linux-<arch>.tar.gz，内含
	// lattix-agent/lattix-agent + latx-ag，与 install.sh §11 同构）。
	asset := "lattix-agent-linux-" + runtime.GOARCH + ".tar.gz"
	tmp, err := os.MkdirTemp("", "lattix-agent-upgrade-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)

	rel := base + "/" + version
	archivePath := filepath.Join(tmp, asset)
	if err := download(rel+"/"+asset, archivePath); err != nil {
		return false, fmt.Errorf("下载 agent 包失败: %w", err)
	}
	sumsPath := filepath.Join(tmp, "checksums.txt")
	if err := download(rel+"/checksums.txt", sumsPath); err != nil {
		return false, fmt.Errorf("未获取到 checksums.txt，中止升级: %w", err)
	}
	if err := verifySHA256(sumsPath, asset, archivePath); err != nil {
		return false, err
	}

	// 解包取出 agent 与管理命令；两者必须来自同一个已校验发布包。
	binPath := filepath.Join(tmp, "lattix-agent")
	cliPath := filepath.Join(tmp, "latx-ag")
	if err := extractAgentBundle(archivePath, binPath, cliPath); err != nil {
		return false, fmt.Errorf("解压 agent 包失败: %w", err)
	}

	// 预检：新二进制须能运行并打印版本，否则放弃替换——
	// 无运行时回滚，坏二进制会让 systemd 陷入崩溃循环。
	if err := os.Chmod(binPath, 0o755); err != nil {
		return false, fmt.Errorf("新 agent 二进制赋可执行权限失败: %w", err)
	}
	out, err := exec.Command(binPath, "-version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return false, fmt.Errorf("新 agent 二进制自检失败(-version): %v: %s", err, strings.TrimSpace(string(out)))
	}
	if got := strings.TrimSpace(string(out)); got != version {
		return false, fmt.Errorf("新 agent 二进制版本不符（期望 %s，实际 %s）", version, got)
	}
	if err := os.Chmod(cliPath, 0o755); err != nil {
		return false, fmt.Errorf("新 latx-ag 赋可执行权限失败: %w", err)
	}

	// 先备份两件套，再替换管理命令和 agent。任一步失败都恢复到同一旧版本。
	agentBackup := executable + ".bak"
	cliTarget := filepath.Join(filepath.Dir(executable), "latx-ag")
	cliBackup := cliTarget + ".bak"
	if err := fileutil.CopyFileAtomic(executable, agentBackup, 0o755); err != nil {
		return false, fmt.Errorf("备份旧 agent 失败: %w", err)
	}
	cliExisted := true
	if err := fileutil.CopyFileAtomic(cliTarget, cliBackup, 0o755); err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("备份旧 latx-ag 失败: %w", err)
		}
		cliExisted = false
	}
	rollbackCLI := func() {
		if cliExisted {
			_ = fileutil.CopyFileAtomic(cliBackup, cliTarget, 0o755)
		} else {
			_ = os.Remove(cliTarget)
		}
	}
	if err := fileutil.CopyFileAtomic(cliPath, cliTarget, 0o755); err != nil {
		return false, fmt.Errorf("替换 latx-ag 失败: %w", err)
	}
	if err := fileutil.CopyFileAtomic(binPath, executable, 0o755); err != nil {
		rollbackCLI()
		_ = fileutil.CopyFileAtomic(agentBackup, executable, 0o755)
		return false, fmt.Errorf("替换 agent 二进制失败: %w", err)
	}
	return true, nil
}

// validateReleaseBase 校验升级下载基址：必须为 http(s) URL，且非回环地址的
// 明文 http 被拒绝（checksums.txt 与二进制同源，明文传输可被中间人整体替换，
// 评审 P2 供应链加固；本机回环镜像与 e2e 保留 http 能力）。
func validateReleaseBase(base string) error {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("无效的升级下载基址: %q", base)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("升级下载基址必须使用 https（明文 http 仅允许回环镜像）: %s", base)
}

// resolveLatest 经 GitHub API 解析最新 release tag。
func resolveLatest(apiRepos string) (string, error) {
	var rel struct {
		TagName string `json:"tag_name"`
	}
	client := external.ExternalJSONRequester{Doer: &http.Client{Timeout: 30 * time.Second}}
	if err := client.GetJSON(context.Background(), apiRepos+"/releases/latest", &rel); err != nil {
		return "", fmt.Errorf("解析 latest 失败: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("解析 latest 失败: 无法读取 tag_name")
	}
	return rel.TagName, nil
}

// verifySHA256 在 checksums.txt（sha256sum 标准格式）中查 asset 的期望值并校验文件。
func verifySHA256(sumsPath, asset, filePath string) error {
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
		return fmt.Errorf("agent 二进制校验和不匹配（期望 %s，实际 %s）", expected, actual)
	}
	return nil
}

// download 下载 URL 到本地文件。
func download(url, path string) error {
	client := external.ExternalFileRequester{Doer: &http.Client{Timeout: httpTimeout}}
	return client.Download(context.Background(), url, path, nil)
}

// extractAgentBundle 从发布包中提取 agent 与 latx-ag，忽略其他条目。
func extractAgentBundle(archivePath, agentDest, cliDest string) error {
	f, err := os.Open(archivePath)
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
	targets := map[string]string{
		"lattix-agent/lattix-agent": agentDest,
		"lattix-agent/latx-ag":      cliDest,
	}
	found := make(map[string]bool, len(targets))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			for name := range targets {
				if !found[name] {
					return fmt.Errorf("tarball 中未找到 %s", name)
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		dest, ok := targets[name]
		if !ok {
			continue
		}
		p, err := filepath.Abs(dest)
		if err != nil {
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
		found[name] = true
	}
}
