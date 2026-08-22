package ipquality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"lattix/agent/internal/fileutil"
)

const (
	maxScriptBytes = 2 << 20

	cachedScriptName = "ip.sh"
)

// ScriptURL is the upstream ip.sh source pinned to a specific commit; the
// script runs as root, so the fetched content is only used after its SHA256
// matches scriptSHA256 below.
var ScriptURL = "https://raw.githubusercontent.com/xykt/IPQuality/0ee5f192fed70c04615852efba0e4b8bd43546c7/ip.sh"

// scriptSHA256 is the content hash of the pinned ip.sh. Bumping the upstream
// script means updating the commit in ScriptURL and this hash together.
var scriptSHA256 = "9823c560e0d19769eb627329a31cb47da655d087166d86e40d9b6c77bc7f32fb"

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
// pinned upstream script, verifies its SHA256, and atomically replaces the
// cache when the version differs. A failed fetch falls back to the cache —
// also SHA256-verified — and reports stale=true. Content that fails
// verification is never cached, returned, or executed.
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
		// The cache may be stale, but it still must pass the pinned-hash
		// check before it is executed.
		if err := verifyCachedScriptSHA256(path); err != nil {
			return "", "", false, err
		}
		return path, cached, true, nil
	}
	if err := verifyScriptSHA256(fresh); err != nil {
		return "", "", false, err
	}
	freshVersion, ok := ExtractScriptVersion(fresh)
	if !ok {
		return "", "", false, errors.New("ip.sh: script_version not found in fetched content")
	}
	if cached == freshVersion && verifyCachedScriptSHA256(path) == nil {
		return path, cached, false, nil
	}
	// The cache is missing, outdated, or tampered with (same version line but
	// a different body); replace it with the verified download.
	if err := fileutil.WriteFileAtomic(path, []byte(fresh), 0o700); err != nil {
		return "", "", false, fmt.Errorf("replace cached script: %w", err)
	}
	return path, freshVersion, false, nil
}

// verifyScriptSHA256 checks content against the pinned scriptSHA256; the
// error carries both hashes for diagnostics.
func verifyScriptSHA256(content string) error {
	sum := sha256.Sum256([]byte(content))
	if got := hex.EncodeToString(sum[:]); got != scriptSHA256 {
		return fmt.Errorf("ip.sh checksum mismatch: got %s, want %s", got, scriptSHA256)
	}
	return nil
}

// verifyCachedScriptSHA256 checks the cached script file against the pinned
// scriptSHA256; a tampered or corrupted cache is never executed.
func verifyCachedScriptSHA256(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read cached ip.sh: %w", err)
	}
	if err := verifyScriptSHA256(string(content)); err != nil {
		return fmt.Errorf("cached %w", err)
	}
	return nil
}
