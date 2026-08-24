package panel

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackagedLatxVersionDoesNotRequireBash(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("release scripts target Linux")
	}

	const version = "v9.9.9"
	sourcePath := filepath.Join("..", "..", "..", "..", "scripts", "latx.sh")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	stamped := strings.ReplaceAll(string(source), "{{LATTIX_VERSION}}", version)
	stamped = strings.ReplaceAll(stamped, "{{GITHUB_REPO}}", "BeanYa/Lattix")

	root := t.TempDir()
	latxPath := filepath.Join(root, "latx")
	if err := os.WriteFile(latxPath, []byte(stamped), 0o755); err != nil {
		t.Fatal(err)
	}
	backendPath := filepath.Join(root, "lattix-backend")
	backend := "#!/bin/sh\n[ \"${1:-}\" = \"-version\" ] && printf '%s\\n' " + version + "\n"
	if err := os.WriteFile(backendPath, []byte(backend), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(latxPath, "version")
	cmd.Env = []string{"LATX_ROOT=" + root, "PATH=/path-without-bash"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("latx version failed without bash: %v\n%s", err, out)
	}
	want := "latx 版本: " + version + "\n面板版本: " + version + "\n"
	if string(out) != want {
		t.Fatalf("latx version output = %q, want %q", out, want)
	}
}
