package servertest

import (
	"context"
	_ "embed"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dnsblConcurrency  = 20
	dnsblQueryTimeout = 3 * time.Second
	dnsblTotalTimeout = 90 * time.Second
)

// dnsblSnapshot is sourced from xykt/IPQuality ref/dnsbl.list at commit
// 44a55baec6cdd166a68b37f9c07d62d9e0a04f23. It is sorted and deduplicated
// during maintenance, never downloaded while a server test is running.
//
//go:embed dnsbl_snapshot.txt
var dnsblSnapshot string

type dnsblLookup func(context.Context, string) ([]string, error)

type dnsblEntry struct {
	Provider string   `json:"provider"`
	Status   string   `json:"status"`
	Answers  []string `json:"answers,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func runDNSBL(parent context.Context, address netip.Addr) map[string]any {
	return runDNSBLWithLookup(parent, address, net.DefaultResolver.LookupHost)
}

func runDNSBLWithLookup(parent context.Context, address netip.Addr, lookup dnsblLookup) map[string]any {
	address = address.Unmap()
	if !address.Is4() {
		return map[string]any{
			"status": "not_applicable", "checked": 0,
			"error_code": "ipv4_required", "error_message": "DNSBL checks support IPv4 addresses only",
		}
	}
	providers := snapshotLines(dnsblSnapshot)
	if len(providers) == 0 {
		return map[string]any{
			"status": "unavailable", "checked": 0,
			"error_code": "dnsbl_snapshot_unavailable", "error_message": "the bundled DNSBL snapshot is empty",
		}
	}

	ctx, cancel := context.WithTimeout(parent, dnsblTotalTimeout)
	defer cancel()
	type job struct{ index int }
	jobs := make(chan job)
	results := make([]dnsblEntry, len(providers))
	var workers sync.WaitGroup
	for worker := 0; worker < dnsblConcurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for current := range jobs {
				provider := providers[current.index]
				queryCtx, queryCancel := context.WithTimeout(ctx, dnsblQueryTimeout)
				answers, err := lookup(queryCtx, dnsblQueryName(address, provider))
				queryCancel()
				results[current.index] = classifyDNSBL(provider, answers, err)
			}
		}()
	}
	for index := range providers {
		select {
		case jobs <- job{index: index}:
		case <-ctx.Done():
			for pending := index; pending < len(providers); pending++ {
				results[pending] = dnsblEntry{Provider: providers[pending], Status: "unknown", Error: ctx.Err().Error()}
			}
			close(jobs)
			workers.Wait()
			return summarizeDNSBL(results)
		}
	}
	close(jobs)
	workers.Wait()
	return summarizeDNSBL(results)
}

func dnsblQueryName(address netip.Addr, provider string) string {
	octets := address.As4()
	return strings.Join([]string{
		strconv.Itoa(int(octets[3])), strconv.Itoa(int(octets[2])), strconv.Itoa(int(octets[1])), strconv.Itoa(int(octets[0])), provider,
	}, ".")
}

func classifyDNSBL(provider string, answers []string, err error) dnsblEntry {
	entry := dnsblEntry{Provider: provider, Status: "clean"}
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return entry
		}
		entry.Status = "unknown"
		entry.Error = err.Error()
		return entry
	}
	if len(answers) == 0 {
		return entry
	}
	entry.Answers = append([]string(nil), answers...)
	sort.Strings(entry.Answers)
	entry.Status = "marked"
	for _, answer := range answers {
		if parsed, parseErr := netip.ParseAddr(answer); parseErr == nil && parsed.Unmap() == netip.MustParseAddr("127.0.0.2") {
			entry.Status = "blacklisted"
			break
		}
	}
	return entry
}

func summarizeDNSBL(entries []dnsblEntry) map[string]any {
	checked, clean, blacklisted, marked, unknown := 0, 0, 0, 0, 0
	hits := make([]dnsblEntry, 0)
	errors := make([]dnsblEntry, 0)
	for _, entry := range entries {
		switch entry.Status {
		case "clean":
			checked++
			clean++
		case "blacklisted":
			checked++
			blacklisted++
			hits = append(hits, entry)
		case "marked":
			checked++
			marked++
			hits = append(hits, entry)
		default:
			unknown++
			errors = append(errors, entry)
		}
	}
	status := "clean"
	if blacklisted > 0 || marked > 0 {
		status = "listed"
	} else if checked == 0 {
		status = "unavailable"
	} else if unknown > 0 {
		status = "limited"
	}
	return map[string]any{
		"status": status, "total": len(entries), "checked": checked, "clean": clean,
		"blacklisted": blacklisted, "marked": marked, "unknown": unknown,
		"hits": hits, "errors": errors,
	}
}

func snapshotLines(raw string) []string {
	lines := strings.Split(raw, "\n")
	result := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, exists := seen[line]; exists {
			continue
		}
		seen[line] = struct{}{}
		result = append(result, line)
	}
	sort.Strings(result)
	return result
}
