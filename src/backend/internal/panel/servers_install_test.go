package panel

import (
	"strings"
	"testing"
)

func TestInstallCommandUsesUnifiedGitHubEntrypoint(t *testing.T) {
	server := &Server{cfg: Config{GitHubRepo: "BeanYa/Lattix", Version: "v1.2.3"}}
	command := server.installCommand("https://panel.example.com", "bootstrap", "latest")
	for _, want := range []string{
		"curl -fsSL https://raw.githubusercontent.com/BeanYa/Lattix/main/install.sh",
		"| bash -s -- agent --version v1.2.3",
		"--panel https://panel.example.com",
		"--token bootstrap",
		"--xray-version latest",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("install command %q does not contain %q", command, want)
		}
	}
}

func TestDevInstallCommandDefaultsToLatest(t *testing.T) {
	server := &Server{cfg: Config{GitHubRepo: "BeanYa/Lattix", Version: "dev"}}
	command := server.installCommand("http://127.0.0.1:8080", "bootstrap", "v26.3.27")
	if strings.Contains(command, "--version") {
		t.Fatalf("dev install command unexpectedly pins a version: %q", command)
	}
}
