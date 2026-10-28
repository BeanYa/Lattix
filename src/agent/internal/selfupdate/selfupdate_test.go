package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeAgentTarball 打包 release 形态的 agent 与 latx-ag 两件套。
func makeAgentTarball(t *testing.T, content []byte) []byte {
	t.Helper()
	return makeAgentTarballWithCLI(t, content, []byte("#!/bin/sh\necho 'latx-ag 版本: v0.0.9'\n"))
}

// makeAgentTarballWithCLI 同上，但 latx-ag 内容可定制（兼容性迁移调用测试用）。
func makeAgentTarballWithCLI(t *testing.T, content, cli []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	files := map[string][]byte{
		"lattix-agent/lattix-agent": content,
		"lattix-agent/latx-ag":      cli,
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

// newFakeRelease 起一个本地 HTTP 服务，托管指定版本的 agent tarball 资产与 checksums.txt。
func newFakeRelease(t *testing.T, version string, content []byte) (base string) {
	t.Helper()
	return newFakeReleaseWithCLI(t, version, content, []byte("#!/bin/sh\necho 'latx-ag 版本: v0.0.9'\n"))
}

func newFakeReleaseWithCLI(t *testing.T, version string, content, cli []byte) (base string) {
	t.Helper()
	asset := "lattix-agent-linux-" + runtime.GOARCH + ".tar.gz"
	tarball := makeAgentTarballWithCLI(t, content, cli)
	sum := sha256.Sum256(tarball)
	sums := fmt.Sprintf("%x  %s\n%x  %s\n", sum, asset, sum, "lattix-panel-linux-amd64.tar.gz")
	mux := http.NewServeMux()
	mux.HandleFunc("/"+version+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	})
	mux.HandleFunc("/"+version+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sums))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestApplyReplacesExecutable(t *testing.T) {
	// 新二进制须能通过 -version 自检（shell 脚本即可执行并打印版本）。
	content := []byte("#!/bin/sh\necho v0.0.9\n")
	base := newFakeRelease(t, "v0.0.9", content)

	dir := t.TempDir()
	exe := filepath.Join(dir, "lattix-agent")
	cli := filepath.Join(dir, "latx-ag")
	os.WriteFile(exe, []byte("old agent"), 0o755)
	os.WriteFile(cli, []byte("old cli"), 0o755)

	upgraded, err := applyTo("v0.0.9", base, "v0.0.2", "unused/repo", exe, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !upgraded {
		t.Fatal("expected upgraded=true")
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read replaced exe: %v", err)
	}
	if string(got) != string(content) {
		t.Fatal("exe 内容未被替换为新二进制")
	}
	if _, err := os.Stat(exe + ".bak"); err != nil {
		t.Fatal("未生成 .bak 备份")
	}
	cliBody, err := os.ReadFile(cli)
	if err != nil || !bytes.Contains(cliBody, []byte("latx-ag 版本: v0.0.9")) {
		t.Fatalf("latx-ag 未随 agent 更新: %q, %v", cliBody, err)
	}
	if old, err := os.ReadFile(cli + ".bak"); err != nil || string(old) != "old cli" {
		t.Fatalf("latx-ag 备份异常: %q, %v", old, err)
	}
}

func TestApplyIdempotentSameVersion(t *testing.T) {
	base := newFakeRelease(t, "v0.0.2", []byte("x"))
	upgraded, err := applyTo("v0.0.2", base, "v0.0.2", "unused/repo", filepath.Join(t.TempDir(), "agent"), false)
	if err != nil || upgraded {
		t.Fatalf("同版本应幂等返回 (false, nil)，实际 (%v, %v)", upgraded, err)
	}
}

func TestApplyForceReplacesSameVersion(t *testing.T) {
	content := []byte("#!/bin/sh\necho v0.0.2\n")
	base := newFakeRelease(t, "v0.0.2", content)
	dir := t.TempDir()
	exe := filepath.Join(dir, "lattix-agent")
	cli := filepath.Join(dir, "latx-ag")
	if err := os.WriteFile(exe, []byte("old same-version agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, []byte("old cli"), 0o755); err != nil {
		t.Fatal(err)
	}

	upgraded, err := applyTo("v0.0.2", base, "v0.0.2", "unused/repo", exe, true)
	if err != nil {
		t.Fatalf("force Apply: %v", err)
	}
	if !upgraded {
		t.Fatal("同版本 force=true 应执行覆盖安装")
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != string(content) {
		t.Fatalf("强制更新后 agent 内容异常: %q, %v", got, err)
	}
}

func TestApplyRunsMigrationsAroundSwap(t *testing.T) {
	// 兼容性迁移以新 latx-ag 为单一来源：换二进制前后各调用一次（pre/post），
	// 数据目录指向安装布局的应用根 data。
	logFile := filepath.Join(t.TempDir(), "migrate.log")
	cli := []byte("#!/bin/sh\necho \"$*\" >> \"$LATX_TEST_MIGRATE_LOG\"\n")
	base := newFakeReleaseWithCLI(t, "v0.0.9", []byte("#!/bin/sh\necho v0.0.9\n"), cli)
	t.Setenv("LATX_TEST_MIGRATE_LOG", logFile)

	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "lattix-agent") // 应用根/bin，与 install.sh 布局一致
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old agent"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := applyTo("v0.0.9", base, "v0.0.2", "unused/repo", exe, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("迁移未被调用: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 2 {
		t.Fatalf("pre/post 应各调用一次迁移，实际 %d 次: %q", len(lines), log)
	}
	wantDataDir := filepath.Join(dir, "data")
	for i, phase := range []string{"pre", "post"} {
		for _, want := range []string{"--phase " + phase, "--from v0.0.2", "--data-dir " + wantDataDir} {
			if !strings.Contains(lines[i], want) {
				t.Fatalf("第 %d 次迁移（期望 %s 相）参数缺 %q: %q", i+1, phase, want, lines[i])
			}
		}
	}
}

func TestApplyMigrationFailureRollsBack(t *testing.T) {
	// post 相迁移失败：报错并回滚两件套到旧版本（面板收到失败回执，旧 agent 继续运行）。
	cli := []byte("#!/bin/sh\ncase \"$*\" in *--phase pre*) exit 0 ;; *) exit 1 ;; esac\n")
	base := newFakeReleaseWithCLI(t, "v0.0.9", []byte("#!/bin/sh\necho v0.0.9\n"), cli)
	dir := t.TempDir()
	exe := filepath.Join(dir, "lattix-agent")
	cliTarget := filepath.Join(dir, "latx-ag")
	if err := os.WriteFile(exe, []byte("old agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cliTarget, []byte("old cli"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := applyTo("v0.0.9", base, "v0.0.2", "unused/repo", exe, false); err == nil {
		t.Fatal("post 相迁移失败应报错")
	} else if !strings.Contains(err.Error(), "兼容性迁移") {
		t.Fatalf("错误应标注迁移失败: %v", err)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "old agent" {
		t.Fatalf("迁移失败后 agent 未回滚: %q, %v", got, err)
	}
	if got, err := os.ReadFile(cliTarget); err != nil || string(got) != "old cli" {
		t.Fatalf("迁移失败后 latx-ag 未回滚: %q, %v", got, err)
	}
}

func TestApplyRejectsNonLoopbackHTTPBase(t *testing.T) {
	_, err := applyTo("v0.0.9", "http://198.51.100.7/releases/download",
		"v0.0.2", "unused/repo", filepath.Join(t.TempDir(), "agent"), false)
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("non-loopback http base error = %v, want https requirement", err)
	}
}
func TestApplyRejectsChecksumMismatch(t *testing.T) {
	// 服务端给的期望值与 tarball 内容不匹配。
	asset := "lattix-agent-linux-" + runtime.GOARCH + ".tar.gz"
	good := makeAgentTarball(t, []byte("good"))
	sums := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("other")), asset)
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.0.9/"+asset, func(w http.ResponseWriter, r *http.Request) { w.Write(good) })
	mux.HandleFunc("/v0.0.9/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sums)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := applyTo("v0.0.9", srv.URL, "v0.0.2", "unused/repo", filepath.Join(t.TempDir(), "agent"), false); err == nil {
		t.Fatal("校验和不匹配应报错")
	}
}

func TestApplyRejectsMissingChecksums(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if _, err := applyTo("v0.0.9", srv.URL, "v0.0.2", "unused/repo", filepath.Join(t.TempDir(), "agent"), false); err == nil {
		t.Fatal("缺 checksums.txt 应报错")
	}
}

func TestApplyRejectsBrokenBinary(t *testing.T) {
	// 校验和通过但二进制不可执行（-version 自检失败）时应放弃替换。
	base := newFakeRelease(t, "v0.0.9", []byte("not an executable"))
	if _, err := applyTo("v0.0.9", base, "v0.0.2", "unused/repo", filepath.Join(t.TempDir(), "agent"), false); err == nil {
		t.Fatal("新二进制自检失败应报错")
	}
}

func TestVerifySHA256ParsesStandardFormat(t *testing.T) {
	dir := t.TempDir()
	content := []byte("hello")
	sum := sha256.Sum256(content)
	sums := filepath.Join(dir, "checksums.txt")
	bin := filepath.Join(dir, "bin")
	os.WriteFile(sums, []byte(fmt.Sprintf("%x  myfile\n", sum)), 0o644)
	os.WriteFile(bin, content, 0o644)
	if err := verifySHA256(sums, "myfile", bin); err != nil {
		t.Fatalf("verifySHA256: %v", err)
	}
}
