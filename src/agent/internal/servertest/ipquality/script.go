package ipquality

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const (
	maxScriptBytes = 2 << 20

	cachedScriptName = "ip.sh"
)

// ScriptURL is the upstream ip.sh source; the script is fetched from GitHub
// raw and cached under the agent data directory.
var ScriptURL = "https://raw.githubusercontent.com/xykt/IPQuality/main/ip.sh"

var scriptVersionPattern = regexp.MustCompile(`script_version="([^"]+)"`)

// ScriptFetcher fetches the upstream script text. requester.ExternalFileRequester
// implements it with a caller-provided HTTP client.
type ScriptFetcher interface {
	GetText(ctx context.Context, url string, maxBytes int64) (string, error)
}

// ExtractScriptVersion parses the script_version="..." line from ip.sh.
func ExtractScriptVersion(content string) (string, bool) {
	match := scriptVersionPattern.FindStringSubmatch(content)
	if len(match) != 2 || match[1] == "" {
		return "", false
	}
	return match[1], true
}

// CachedScriptVersion reads the version of the cached script, if any.
func CachedScriptVersion(cacheDir string) string {
	content, err := os.ReadFile(filepath.Join(cacheDir, cachedScriptName))
	if err != nil {
		return ""
	}
	version, _ := ExtractScriptVersion(string(content))
	return version
}

// EnsureScript returns the path of a usable local script. It fetches the
// upstream script, compares its version with the cache, and atomically
// replaces the cache when a newer version is available. A failed fetch falls
// back to the cache and reports stale=true.
func EnsureScript(ctx context.Context, fetcher ScriptFetcher, cacheDir string) (path, version string, stale bool, err error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", "", false, fmt.Errorf("create script cache dir: %w", err)
	}
	path = filepath.Join(cacheDir, cachedScriptName)
	cached := CachedScriptVersion(cacheDir)

	fresh, fetchErr := fetcher.GetText(ctx, ScriptURL, maxScriptBytes)
	if fetchErr != nil {
		if cached == "" {
			return "", "", false, fmt.Errorf("fetch ip.sh: %w", fetchErr)
		}
		return path, cached, true, nil
	}
	freshVersion, ok := ExtractScriptVersion(fresh)
	if !ok {
		if cached == "" {
			return "", "", false, errors.New("ip.sh: script_version not found in fetched content")
		}
		return path, cached, true, nil
	}
	if cached == freshVersion {
		return path, cached, false, nil
	}
	if err := replaceScript(path, fresh); err != nil {
		return "", "", false, err
	}
	return path, freshVersion, false, nil
}

func replaceScript(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "ip.sh-*")
	if err != nil {
		return fmt.Errorf("create script temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write script temp: %w", err)
	}
	if err := tmp.Chmod(0o700); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod script temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close script temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace cached script: %w", err)
	}
	return nil
}
