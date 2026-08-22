package servertest

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"lattix/agent/internal/fileutil"
	"lattix/shared/requester"
)

const (
	// ooklaCLIVersion is the pinned official Speedtest CLI release. Packages
	// are fetched from Ookla's own CDN and verified against the SHA256 below.
	ooklaCLIVersion = "1.2.0"
	ooklaRunTimeout = 90 * time.Second
	ooklaMaxOutput  = 4 << 20
)

// ooklaPackage maps a GOARCH to Ookla's linux package name and SHA256.
// GOARCH "arm" is assumed to be armv7 (armhf).
var ooklaPackages = map[string]struct {
	name   string
	sha256 string
}{
	"amd64": {"x86_64", "5690596c54ff9bed63fa3732f818a05dbc2db19ad36ed68f21ca5f64d5cfeeb7"},
	"arm64": {"aarch64", "3953d231da3783e2bf8904b6dd72767c5c6e533e163d3742fd0437affa431bd3"},
	"arm":   {"armhf", "e45fcdebbd8a185553535533dd032d6b10bc8c64eee4139b1147b9c09835d08d"},
}

// ooklaDownloader fetches a URL to a local file. requester.ExternalFileRequester
// implements it.
type ooklaDownloader interface {
	Download(ctx context.Context, url, path string, onProgress func(float64)) error
}

// ooklaResult is the parsed outcome of one speedtest CLI run.
type ooklaResult struct {
	DownloadMbps  float64
	UploadMbps    float64
	DownloadBytes int64
	UploadBytes   int64
	DownloadMS    int64
	UploadMS      int64
	LatencyMS     float64
	ServerName    string
	ResultURL     string
}

// EnsureOoklaCLI returns a usable speedtest binary under dir, downloading and
// verifying the pinned release on first use or after a version bump.
func EnsureOoklaCLI(ctx context.Context, downloader ooklaDownloader, dir string) (string, error) {
	pkg, ok := ooklaPackages[runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("ookla speedtest CLI is not available for linux/%s", runtime.GOARCH)
	}
	binPath := filepath.Join(dir, "speedtest")
	stampPath := filepath.Join(dir, "version")
	if stamp, err := os.ReadFile(stampPath); err == nil && strings.TrimSpace(string(stamp)) == ooklaCLIVersion {
		if info, err := os.Stat(binPath); err == nil && info.Mode()&0o111 != 0 {
			return binPath, nil
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create ookla dir: %w", err)
	}
	url := fmt.Sprintf("https://install.speedtest.net/app/cli/ookla-speedtest-%s-linux-%s.tgz", ooklaCLIVersion, pkg.name)
	archive, err := os.CreateTemp(dir, "ookla-*.tgz")
	if err != nil {
		return "", fmt.Errorf("create ookla archive temp: %w", err)
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	if err := archive.Close(); err != nil {
		return "", fmt.Errorf("close ookla archive temp: %w", err)
	}
	if err := downloader.Download(ctx, url, archivePath, nil); err != nil {
		return "", fmt.Errorf("download ookla CLI: %w", err)
	}
	if err := verifySHA256(archivePath, pkg.sha256); err != nil {
		return "", err
	}
	if err := extractSpeedtestBinary(archivePath, binPath); err != nil {
		return "", err
	}
	if err := os.WriteFile(stampPath, []byte(ooklaCLIVersion+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write ookla version stamp: %w", err)
	}
	return binPath, nil
}

func verifySHA256(path, want string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return fmt.Errorf("hash ookla archive: %w", err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return fmt.Errorf("ookla CLI checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

func extractSpeedtestBinary(archivePath, binPath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("read ookla archive: %w", err)
	}
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("ookla archive does not contain the speedtest binary")
		}
		if err != nil {
			return fmt.Errorf("read ookla archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "speedtest" {
			continue
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			return fmt.Errorf("extract speedtest binary: %w", err)
		}
		if err := fileutil.WriteFileAtomic(binPath, data, 0o700); err != nil {
			return fmt.Errorf("install speedtest binary: %w", err)
		}
		return nil
	}
}

// runOoklaServer probes one speedtest.net server by ID and parses the JSON
// report. Bandwidth values are bytes/s and are converted to Mbps.
func runOoklaServer(ctx context.Context, binPath, serverID string) (ooklaResult, error) {
	ctx, cancel := context.WithTimeout(ctx, ooklaRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath,
		"--accept-license", "--accept-gdpr", "--format=json", "--progress=no", "--server-id="+serverID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr cappedBuffer
	stdout.max = ooklaMaxOutput
	stderr.max = ooklaMaxOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return ooklaResult{}, fmt.Errorf("start speedtest CLI: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		_ = killProcessGroup(cmd)
		<-done
		return ooklaResult{}, fmt.Errorf("speedtest CLI timed out after %s", ooklaRunTimeout)
	case err := <-done:
		if err != nil {
			if tail := cappedTail(&stderr); tail != "" {
				return ooklaResult{}, fmt.Errorf("speedtest CLI failed: %w (stderr: %s)", err, tail)
			}
			return ooklaResult{}, fmt.Errorf("speedtest CLI failed: %w", err)
		}
	}
	return parseOoklaOutput(stdout.String())
}

type ooklaOutput struct {
	Download struct {
		Bandwidth int64 `json:"bandwidth"`
		Bytes     int64 `json:"bytes"`
		Elapsed   int64 `json:"elapsed"`
	} `json:"download"`
	Upload struct {
		Bandwidth int64 `json:"bandwidth"`
		Bytes     int64 `json:"bytes"`
		Elapsed   int64 `json:"elapsed"`
	} `json:"upload"`
	Ping struct {
		Latency float64 `json:"latency"`
	} `json:"ping"`
	Server struct {
		Name     string `json:"name"`
		Location string `json:"location"`
	} `json:"server"`
	Result struct {
		URL string `json:"url"`
	} `json:"result"`
}

func parseOoklaOutput(raw string) (ooklaResult, error) {
	var output ooklaOutput
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		return ooklaResult{}, fmt.Errorf("parse speedtest JSON: %w", err)
	}
	if output.Download.Bandwidth <= 0 && output.Upload.Bandwidth <= 0 {
		return ooklaResult{}, errors.New("speedtest JSON carries no bandwidth data")
	}
	return ooklaResult{
		DownloadMbps:  float64(output.Download.Bandwidth) * 8 / 1_000_000,
		UploadMbps:    float64(output.Upload.Bandwidth) * 8 / 1_000_000,
		DownloadBytes: output.Download.Bytes,
		UploadBytes:   output.Upload.Bytes,
		DownloadMS:    output.Download.Elapsed,
		UploadMS:      output.Upload.Elapsed,
		LatencyMS:     output.Ping.Latency,
		ServerName:    strings.TrimSpace(strings.Join([]string{output.Server.Name, output.Server.Location}, " ")),
		ResultURL:     output.Result.URL,
	}, nil
}

// ooklaCLIFetcher fetches the pinned CLI release with a generous timeout.
func ooklaCLIFetcher() requester.ExternalFileRequester {
	return requester.ExternalFileRequester{Doer: &http.Client{Timeout: 2 * time.Minute}}
}

// cappedBuffer is an io.Writer that keeps at most max bytes.
type cappedBuffer struct {
	buf strings.Builder
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > b.max {
		p = p[:max(0, b.max-b.buf.Len())]
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) String() string { return b.buf.String() }

func cappedTail(buffer *cappedBuffer) string {
	tail := strings.TrimSpace(buffer.buf.String())
	if len(tail) > 2048 {
		tail = tail[len(tail)-2048:]
	}
	return tail
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
