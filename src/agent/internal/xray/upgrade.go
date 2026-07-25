package xray

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// upgradeHTTPTimeout 是升级相关单次 HTTP 请求的超时。
const upgradeHTTPTimeout = 120 * time.Second

// UpgradeXray 升级 xray 到指定版本（§18 版本升级管理）：
// 解析 latest（GitHub API；设镜像基址时跳过 API 走 latest/download 约定）→
// 下载 release 包与 .dgst → 校验 SHA2-256 →
// 备份旧二进制（.bak）→ 原子替换 → 重启 → 校验实际版本；任一步失败回滚并重启。
func (m *Manager) UpgradeXray(version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if version == "" || version == "latest" {
		if m.mirrorBase {
			// 镜像基址（-xray-release-base）：跳过 GitHub API（官方 API 不可达正是该参数的目标场景），
			// 走 release 的 latest 重定向约定 {base}/latest/download/{asset}（镜像站同样支持）；
			// 实际版本由升级后的版本校验与 telemetry 上报，无需 API 返回的版本号。
			return m.upgradeXray("latest", "latest/download")
		}
		v, err := resolveLatestXrayVersion()
		if err != nil {
			return err
		}
		version = v
	}
	return m.upgradeXray(version, version)
}

// upgradeXray 下载并替换 xray：dlRef 为下载 URL 中的版本段（显式版本号或 latest/download），
// expectVer 为升级后期望版本（"latest" 时仅校验运行状态，不比对具体版本号）。
func (m *Manager) upgradeXray(expectVer, dlRef string) error {
	version := expectVer
	if version != "latest" && !strings.HasPrefix(version, "v") {
		return fmt.Errorf("版本号须形如 vX.Y.Z 或 latest: %s", version)
	}

	var asset string
	switch runtime.GOARCH {
	case "amd64":
		asset = "Xray-linux-64.zip"
	case "arm64":
		asset = "Xray-linux-arm64-v8a.zip"
	default:
		return fmt.Errorf("unsupported arch: %s", runtime.GOARCH)
	}

	tmp, err := os.MkdirTemp("", "lattix-xray-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	zipPath := filepath.Join(tmp, "xray.zip")
	base := m.releaseBase + "/" + dlRef
	if err := downloadFile(base+"/"+asset, zipPath); err != nil {
		return fmt.Errorf("下载 xray 包失败: %w", err)
	}
	// 校验官方 .dgst 中的 SHA2-256（同 install.sh §11）；获取不到校验文件视为失败。
	dgstPath := filepath.Join(tmp, "xray.dgst")
	if err := downloadFile(base+"/"+asset+".dgst", dgstPath); err != nil {
		return fmt.Errorf("未获取到官方 .dgst 校验文件: %w", err)
	}
	if err := verifyDgst(dgstPath, zipPath); err != nil {
		return err
	}

	// 解出 xray 二进制。
	binPath := filepath.Join(tmp, "xray")
	if err := unzipOne(zipPath, "xray", binPath); err != nil {
		return fmt.Errorf("解压 xray 包失败: %w", err)
	}

	// 备份并原子替换（运行中的进程持有旧 inode，重启后生效）。
	backup := m.bin + ".bak"
	if err := copyFile(m.bin, backup, 0o755); err != nil {
		return fmt.Errorf("备份旧 xray 失败: %w", err)
	}
	if err := copyFile(binPath, m.bin, 0o755); err != nil {
		return fmt.Errorf("替换 xray 二进制失败: %w", err)
	}

	if err := m.runner.Restart(context.Background()); err != nil {
		m.rollbackXray(backup)
		return fmt.Errorf("重启 xray 失败(%v)，已回滚", err)
	}
	// 校验实际运行版本（latest 无 API 版本号可比，仅要求新二进制正常拉起）。
	ver, running := m.Version()
	if !running || (version != "latest" && !strings.Contains(ver, strings.TrimPrefix(version, "v"))) {
		m.rollbackXray(backup)
		return fmt.Errorf("升级后版本校验失败（期望 %s，实际 %s running=%v），已回滚", version, ver, running)
	}
	os.Remove(backup)
	return nil
}

// rollbackXray 从备份恢复旧二进制并尽力重启。
func (m *Manager) rollbackXray(backup string) {
	if err := copyFile(backup, m.bin, 0o755); err != nil {
		log.Printf("xray rollback: 从 %s 恢复二进制失败: %v", backup, err)
		return
	}
	if err := m.runner.Restart(context.Background()); err != nil {
		log.Printf("xray rollback: 回滚后重启失败: %v", err)
	}
}

// resolveLatestXrayVersion 经 GitHub API 解析最新 release tag（§11 同款逻辑）。
func resolveLatestXrayVersion() (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/XTLS/Xray-core/releases/latest")
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

// downloadFile 下载 URL 到本地文件。
func downloadFile(url, path string) error {
	client := &http.Client{Timeout: upgradeHTTPTimeout}
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

// verifyDgst 校验 .dgst 中的 SHA2-256 与文件实际值一致。
func verifyDgst(dgstPath, filePath string) error {
	b, err := os.ReadFile(dgstPath)
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "SHA2-256=") {
			expected = strings.TrimSpace(strings.TrimPrefix(line, "SHA2-256="))
			break
		}
	}
	if expected == "" {
		return fmt.Errorf(".dgst 中未找到 SHA2-256")
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
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("xray 包校验和不匹配（期望 %s，实际 %s）", expected, actual)
	}
	return nil
}

// unzipOne 从 zip 中解出指定文件到目标路径。
func unzipOne(zipPath, name, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if filepath.Base(f.Name) != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, rc)
		return err
	}
	return fmt.Errorf("zip 中未找到 %s", name)
}

// copyFile 复制文件并设置权限（目标先写临时文件再原子替换）。
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
