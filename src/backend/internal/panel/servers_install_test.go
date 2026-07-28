package panel

import (
	"strings"
	"testing"
)

func TestInstallCommandUsesUnifiedGitHubEntrypoint(t *testing.T) {
	server := &Server{cfg: Config{GitHubRepo: "BeanYa/Lattix", Version: "v1.2.3"}}
	command := server.installCommand("https://panel.example.com", "bootstrap")
	for _, want := range []string{
		"curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh",
		"| bash -s -- agent --version v1.2.3",
		"--panel https://panel.example.com",
		"--token bootstrap",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("install command %q does not contain %q", command, want)
		}
	}
	if strings.Contains(command, "--xray-version") {
		t.Fatalf("install command unexpectedly pins an xray version: %q", command)
	}
}

func TestDevInstallCommandDefaultsToLatest(t *testing.T) {
	server := &Server{cfg: Config{GitHubRepo: "BeanYa/Lattix", Version: "dev"}}
	command := server.installCommand("http://127.0.0.1:8080", "bootstrap")
	if strings.Contains(command, "--version") {
		t.Fatalf("dev install command unexpectedly pins a version: %q", command)
	}
}
