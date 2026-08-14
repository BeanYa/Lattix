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
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	files := map[string][]byte{
		"lattix-agent/lattix-agent": content,
		"lattix-agent/latx-ag":      []byte("#!/bin/sh\necho 'latx-ag 版本: v0.0.9'\n"),
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
	asset := "lattix-agent-linux-" + runtime.GOARCH + ".tar.gz"
	tarball := makeAgentTarball(t, content)
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
