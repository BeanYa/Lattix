package selfupdate

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// newFakeRelease 起一个本地 HTTP 服务，托管指定版本的 agent 资产与 checksums.txt。
func newFakeRelease(t *testing.T, version string, content []byte) (base string) {
	t.Helper()
	asset := "lattix-agent-linux-" + runtime.GOARCH
	sum := sha256.Sum256(content)
	sums := fmt.Sprintf("%x  %s\n%x  %s\n", sum, asset, sum, "lattix-panel-linux-amd64.tar.gz")
	mux := http.NewServeMux()
	mux.HandleFunc("/"+version+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
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

	upgraded, err := Apply("v0.0.9", base, "v0.0.2", "unused/repo")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !upgraded {
		t.Fatal("expected upgraded=true")
	}
	exe, _ := os.Executable()
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
}

func TestApplyIdempotentSameVersion(t *testing.T) {
	base := newFakeRelease(t, "v0.0.2", []byte("x"))
	upgraded, err := Apply("v0.0.2", base, "v0.0.2", "unused/repo")
	if err != nil || upgraded {
		t.Fatalf("同版本应幂等返回 (false, nil)，实际 (%v, %v)", upgraded, err)
	}
}

func TestApplyRejectsChecksumMismatch(t *testing.T) {
	// 服务端给的期望值与文件内容不匹配。
	asset := "lattix-agent-linux-" + runtime.GOARCH
	sums := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("other")), asset)
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.0.9/"+asset, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("good")) })
	mux.HandleFunc("/v0.0.9/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sums)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := Apply("v0.0.9", srv.URL, "v0.0.2", "unused/repo"); err == nil {
		t.Fatal("校验和不匹配应报错")
	}
}

func TestApplyRejectsMissingChecksums(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if _, err := Apply("v0.0.9", srv.URL, "v0.0.2", "unused/repo"); err == nil {
		t.Fatal("缺 checksums.txt 应报错")
	}
}

func TestApplyRejectsBrokenBinary(t *testing.T) {
	// 校验和通过但二进制不可执行（-version 自检失败）时应放弃替换。
	base := newFakeRelease(t, "v0.0.9", []byte("not an executable"))
	if _, err := Apply("v0.0.9", base, "v0.0.2", "unused/repo"); err == nil {
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
