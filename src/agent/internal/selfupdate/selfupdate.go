// Package selfupdate 实现 agent 自升级（§18 upgrade_agent 命令）：
// 从 GitHub release 下载目标版本二进制与 checksums.txt，校验 SHA256 后
// 原子替换自身二进制；调用方回执后退出进程，由 systemd 拉起即完成升级。
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const httpTimeout = 120 * time.Second

// Apply 将 agent 自身升级到 version（vX.Y.Z 或 latest，latest 经 GitHub API 解析）。
// releaseBase 形如 https://github.com/<org>/<repo>/releases/download；空时用 defaultRepo。
// 返回 upgraded=true 表示二进制已替换，调用方回执后应退出进程（systemd 拉起完成切换）；
// 目标版本与当前一致时返回 upgraded=false（幂等，无需重启）。
func Apply(version, releaseBase, currentVersion, defaultRepo string) (upgraded bool, err error) {
	base := releaseBase
	repo := defaultRepo
	if base == "" {
		base = "https://github.com/" + repo + "/releases/download"
	} else if strings.Contains(base, "github.com/") {
		// GitHub 基址时从中推导仓库路径（latest 解析用）；镜像基址无法推导。
		rest := strings.SplitN(base, "github.com/", 2)[1]
		repo = strings.TrimSuffix(strings.TrimSuffix(rest, "/releases/download"), "/")
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
	if version == currentVersion {
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

	// 解包取出 agent 二进制（tarball 内 lattix-agent/lattix-agent）。
	binPath := filepath.Join(tmp, "lattix-agent")
	if err := extractAgentBinary(archivePath, binPath); err != nil {
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

	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	exe = strings.TrimSuffix(exe, " (deleted)") // 运行期间二进制被替换过
	if exe, err = filepath.Abs(exe); err != nil {
		return false, err
	}
	// 备份并原子替换（运行中的进程持有旧 inode，退出后由 systemd 拉起新二进制）。
	backup := exe + ".bak"
	if err := copyFile(exe, backup, 0o755); err != nil {
		return false, fmt.Errorf("备份旧 agent 失败: %w", err)
	}
	if err := copyFile(binPath, exe, 0o755); err != nil {
		return false, fmt.Errorf("替换 agent 二进制失败: %w", err)
	}
	return true, nil
}

// resolveLatest 经 GitHub API 解析最新 release tag。
func resolveLatest(apiRepos string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(apiRepos + "/releases/latest")
	if err != nil {
		return "", fmt.Errorf("解析 latest 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("解析 latest 失败: GitHub API 返回 %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil || rel.TagName == "" {
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
	client := &http.Client{Timeout: httpTimeout}
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
	_, err = io.Copy(f, resp.Body)
	return err
}

// extractAgentBinary 从 agent tarball（lattix-agent/lattix-agent + latx-ag）
// 中解出 agent 二进制到 dest。路径限制在 tarball 预期结构内，防 tar slip。
func extractAgentBinary(archivePath, dest string) error {
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
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	destDir := filepath.Dir(destAbs)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("tarball 中未找到 lattix-agent/lattix-agent")
		}
		if err != nil {
			return err
		}
		// 仅取 agent 主二进制（lattix-agent/lattix-agent），忽略 latx-ag 等。
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if strings.TrimPrefix(hdr.Name, "./") != "lattix-agent/lattix-agent" {
			continue
		}
		p := destAbs
		if !strings.HasPrefix(p, destDir+string(os.PathSeparator)) {
			return fmt.Errorf("解包目标越界: %s", p)
		}
		out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
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
