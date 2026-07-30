package cdncatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/scanner"
	"time"
	"unicode"
)

const (
	DefaultSourceURL = "https://lf3-ips.zstaticcdn.com/nodes_data.js"
	SchemaVersion    = 1
	StatusNormal     = "Normal"
	StatusFailed     = "Failed"

	maxSourceBytes = 2 << 20
	lookupWorkers  = 16
	targetSuffix   = ".ip.zstaticcdn.com"
	cityPort       = 443
	dnsCheckDelay  = 250 * time.Millisecond
)

var catalogNotes = []string{
	"host/port 是检测脚本使用的公开节点入口。",
	"target 是维护侧真实节点，用于同步 DNS 或排查。",
	"Cloudflare DNS 记录必须 DNS only，不能开启代理。",
}

type Resolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

type Backup struct {
	Port   int    `json:"port"`
	Target string `json:"target"`
	IP     string `json:"ip"`
	Status string `json:"status"`
}

type Node struct {
	Type         string  `json:"type"`
	Province     string  `json:"province"`
	ProvinceCode string  `json:"provinceCode"`
	ISP          string  `json:"isp"`
	ISPCode      string  `json:"ispCode"`
	IPVersion    int     `json:"ipVersion"`
	Port         int     `json:"port"`
	Target       string  `json:"target"`
	Backup       *Backup `json:"backup,omitempty"`
	IP           string  `json:"ip"`
	Status       string  `json:"status"`
}

type Document struct {
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generatedAt"`
	Notes       []string  `json:"notes"`
	CDN         []Node    `json:"cdn"`
}

type DNSMismatch struct {
	Role        string   `json:"role"`
	Province    string   `json:"province"`
	ISP         string   `json:"isp"`
	Target      string   `json:"target"`
	ExpectedIP  string   `json:"expectedIp"`
	ResolvedIPs []string `json:"resolvedIps"`
	Error       string   `json:"error,omitempty"`
}

type sourceDocument struct {
	ProvinceBaseData []sourceProvince  `json:"provinceBaseData"`
	CityKeyList      []string          `json:"cityKeyList"`
	ExtraCityMeta    map[string]string `json:"extraCityNodeMeta"`
}

type sourceProvince struct {
	Province string            `json:"province"`
	Carriers map[string]string `json:"carriers"`
}

type carrier struct {
	Name string
	Code string
	Key  string
}

var carriers = []carrier{
	{Name: "电信", Code: "ct", Key: "telecom"},
	{Name: "联通", Code: "cu", Key: "unicom"},
	{Name: "移动", Code: "cm", Key: "mobile"},
}

// Fetch downloads Zstatic CDN's public node catalog and turns it into the
// stable document consumed by panel inspections. Remote JavaScript is parsed
// as a restricted object literal and is never executed.
func Fetch(
	ctx context.Context,
	client *http.Client,
	resolver Resolver,
	sourceURL string,
	now time.Time,
) (Document, error) {
	if client == nil {
		return Document{}, errors.New("cdn catalog HTTP client is nil")
	}
	if resolver == nil {
		return Document{}, errors.New("cdn catalog DNS resolver is nil")
	}
	if strings.TrimSpace(sourceURL) == "" {
		return Document{}, errors.New("cdn catalog source URL is empty")
	}

	requestURL, err := url.Parse(sourceURL)
	if err != nil {
		return Document{}, fmt.Errorf("parse CDN catalog source URL: %w", err)
	}
	query := requestURL.Query()
	query.Set("lattix_refresh", strconv.FormatInt(now.UnixNano(), 10))
	requestURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return Document{}, fmt.Errorf("create CDN catalog request: %w", err)
	}
	req.Header.Set("Accept", "application/javascript, text/javascript;q=0.9")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "Lattix-panel")
	resp, err := client.Do(req)
	if err != nil {
		return Document{}, fmt.Errorf("fetch CDN catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Document{}, fmt.Errorf("fetch CDN catalog: HTTP %d", resp.StatusCode)
	}
	if encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); encoding != "" && encoding != "identity" {
		return Document{}, fmt.Errorf("fetch CDN catalog: unsupported content encoding %q", resp.Header.Get("Content-Encoding"))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceBytes+1))
	if err != nil {
		return Document{}, fmt.Errorf("read CDN catalog: %w", err)
	}
	if len(body) > maxSourceBytes {
		return Document{}, fmt.Errorf("CDN catalog exceeds %d bytes", maxSourceBytes)
	}

	source, err := parseSource(body)
	if err != nil {
		return Document{}, fmt.Errorf("parse CDN catalog: %w", err)
	}
	nodes, targets, err := buildNodes(source)
	if err != nil {
		return Document{}, err
	}
	ips, err := resolveTargets(ctx, resolver, targets)
	if err != nil {
		return Document{}, err
	}
	for i := range nodes {
		nodes[i].IP = ips[nodes[i].Target]
		if nodes[i].Backup != nil {
			nodes[i].Backup.IP = ips[nodes[i].Backup.Target]
		}
	}
	return Document{
		Version: SchemaVersion, GeneratedAt: now.UTC(),
		Notes: append([]string(nil), catalogNotes...), CDN: nodes,
	}, nil
}

// CheckDNS independently and sequentially resolves every stored primary and
// backup target. It only updates Status; stored targets, IPs and timestamps are
// left unchanged. Per-target DNS failures are returned as mismatches rather
// than aborting the remaining low-rate checks.
func CheckDNS(ctx context.Context, resolver Resolver, document *Document) ([]DNSMismatch, error) {
	return checkDNS(ctx, resolver, document, dnsCheckDelay)
}

func checkDNS(ctx context.Context, resolver Resolver, document *Document, delay time.Duration) ([]DNSMismatch, error) {
	if resolver == nil {
		return nil, errors.New("cdn catalog DNS resolver is nil")
	}
	if document == nil {
		return nil, errors.New("CDN catalog document is nil")
	}
	if document.Version != SchemaVersion {
		return nil, fmt.Errorf("unsupported CDN catalog version %d", document.Version)
	}
	if len(document.CDN) == 0 {
		return nil, errors.New("CDN catalog contains no nodes")
	}

	targetSet := make(map[string]struct{})
	for _, node := range document.CDN {
		if ip := net.ParseIP(node.IP); ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("invalid stored IPv4 address %q for %s", node.IP, node.Target)
		}
		targetSet[node.Target] = struct{}{}
		if node.Backup != nil {
			if ip := net.ParseIP(node.Backup.IP); ip == nil || ip.To4() == nil {
				return nil, fmt.Errorf("invalid stored IPv4 address %q for %s", node.Backup.IP, node.Backup.Target)
			}
			targetSet[node.Backup.Target] = struct{}{}
		}
	}
	targets := make([]string, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	resolved, err := lookupTargetsSlowly(ctx, resolver, targets, delay)
	if err != nil {
		return nil, err
	}

	var mismatches []DNSMismatch
	for i := range document.CDN {
		node := &document.CDN[i]
		node.Status = StatusNormal
		observation := resolved[node.Target]
		if observation.err != nil || !containsIP(observation.ips, node.IP) {
			node.Status = StatusFailed
			mismatches = append(mismatches, DNSMismatch{
				Role: "primary", Province: node.Province, ISP: node.ISP,
				Target: node.Target, ExpectedIP: node.IP, ResolvedIPs: observation.ips,
				Error: errorText(observation.err),
			})
		}
		if node.Backup != nil {
			node.Backup.Status = StatusNormal
			observation = resolved[node.Backup.Target]
			if observation.err != nil || !containsIP(observation.ips, node.Backup.IP) {
				node.Backup.Status = StatusFailed
				mismatches = append(mismatches, DNSMismatch{
					Role: "backup", Province: node.Province, ISP: node.ISP,
					Target: node.Backup.Target, ExpectedIP: node.Backup.IP, ResolvedIPs: observation.ips,
					Error: errorText(observation.err),
				})
			}
		}
	}
	return mismatches, nil
}

func buildNodes(source sourceDocument) ([]Node, []string, error) {
	if len(source.ProvinceBaseData) == 0 {
		return nil, nil, errors.New("CDN catalog contains no province nodes")
	}
	cityKeys := append([]string(nil), source.CityKeyList...)
	extraKeys := make([]string, 0, len(source.ExtraCityMeta))
	for key := range source.ExtraCityMeta {
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	cityKeys = append(cityKeys, extraKeys...)

	backups := make(map[string]string)
	for _, key := range cityKeys {
		provinceCode, ispCode, ok := cityCodes(key)
		if !ok {
			continue
		}
		group := provinceCode + ":" + ispCode
		if _, exists := backups[group]; !exists {
			backups[group] = strings.ToLower(key) + targetSuffix
		}
	}

	nodes := make([]Node, 0, len(source.ProvinceBaseData)*len(carriers))
	targetSet := make(map[string]struct{})
	for _, province := range source.ProvinceBaseData {
		if strings.TrimSpace(province.Province) == "" {
			return nil, nil, errors.New("CDN catalog contains a province without a name")
		}
		for _, carrier := range carriers {
			endpoint := strings.TrimSpace(province.Carriers[carrier.Key])
			target, port, provinceCode, err := parseProvinceEndpoint(endpoint, carrier.Code)
			if err != nil {
				return nil, nil, fmt.Errorf("%s%s: %w", province.Province, carrier.Name, err)
			}
			node := Node{
				Type: "cdn", Province: province.Province, ProvinceCode: provinceCode,
				ISP: carrier.Name, ISPCode: carrier.Code, IPVersion: 4,
				Port: port, Target: target, Status: StatusNormal,
			}
			targetSet[target] = struct{}{}
			if backupTarget := backups[provinceCode+":"+carrier.Code]; backupTarget != "" {
				node.Backup = &Backup{
					Port: cityPort, Target: backupTarget, Status: StatusNormal,
				}
				targetSet[backupTarget] = struct{}{}
			}
			nodes = append(nodes, node)
		}
	}
	targets := make([]string, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return nodes, targets, nil
}

func parseProvinceEndpoint(endpoint, expectedISP string) (target string, port int, provinceCode string, err error) {
	target, rawPort, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", 0, "", fmt.Errorf("invalid endpoint %q: %w", endpoint, err)
	}
	target = strings.ToLower(strings.TrimSuffix(target, "."))
	if !strings.HasSuffix(target, targetSuffix) {
		return "", 0, "", fmt.Errorf("target %q is outside zstaticcdn.com", target)
	}
	prefix := strings.TrimSuffix(target, targetSuffix)
	parts := strings.Split(prefix, "-")
	if len(parts) != 3 || parts[1] != expectedISP || parts[2] != "v4" || len(parts[0]) != 2 {
		return "", 0, "", fmt.Errorf("unexpected province target %q", target)
	}
	port, err = strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, "", fmt.Errorf("invalid endpoint port %q", rawPort)
	}
	return target, port, parts[0], nil
}

func cityCodes(key string) (provinceCode, ispCode string, ok bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(key)), "-")
	if len(parts) < 4 || len(parts[0]) != 2 || parts[len(parts)-1] != "v4" {
		return "", "", false
	}
	ispCode = parts[len(parts)-2]
	if ispCode != "cm" && ispCode != "cu" && ispCode != "ct" {
		return "", "", false
	}
	if len(parts[1:len(parts)-2]) == 0 {
		return "", "", false
	}
	return parts[0], ispCode, true
}

func resolveTargets(ctx context.Context, resolver Resolver, targets []string) (map[string]string, error) {
	addresses, err := lookupTargets(ctx, resolver, targets)
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]string, len(addresses))
	for target, values := range addresses {
		resolved[target] = values[0]
	}
	return resolved, nil
}

func lookupTargets(ctx context.Context, resolver Resolver, targets []string) (map[string][]string, error) {
	type result struct {
		target string
		ips    []string
		err    error
	}
	jobs := make(chan string, len(targets))
	results := make(chan result, len(targets))
	for _, target := range targets {
		jobs <- target
	}
	close(jobs)

	workers := lookupWorkers
	if len(targets) < workers {
		workers = len(targets)
	}
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for target := range jobs {
				addresses, err := resolver.LookupIP(ctx, "ip4", target)
				if err != nil {
					results <- result{target: target, err: err}
					continue
				}
				valueSet := make(map[string]struct{}, len(addresses))
				for _, address := range addresses {
					if ip := address.To4(); ip != nil {
						valueSet[ip.String()] = struct{}{}
					}
				}
				values := make([]string, 0, len(valueSet))
				for value := range valueSet {
					values = append(values, value)
				}
				sort.Strings(values)
				if len(values) == 0 {
					results <- result{target: target, err: errors.New("no IPv4 address")}
					continue
				}
				results <- result{target: target, ips: values}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	resolved := make(map[string][]string, len(targets))
	errorsByTarget := make(map[string]error)
	for result := range results {
		if result.err != nil {
			errorsByTarget[result.target] = result.err
			continue
		}
		resolved[result.target] = result.ips
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(errorsByTarget) > 0 {
		failed := make([]string, 0, len(errorsByTarget))
		for target := range errorsByTarget {
			failed = append(failed, target)
		}
		sort.Strings(failed)
		target := failed[0]
		return nil, fmt.Errorf("resolve CDN target %s: %w", target, errorsByTarget[target])
	}
	return resolved, nil
}

type dnsObservation struct {
	ips []string
	err error
}

func lookupTargetsSlowly(
	ctx context.Context,
	resolver Resolver,
	targets []string,
	delay time.Duration,
) (map[string]dnsObservation, error) {
	resolved := make(map[string]dnsObservation, len(targets))
	for index, target := range targets {
		if index > 0 && delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		addresses, err := resolver.LookupIP(ctx, "ip4", target)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			resolved[target] = dnsObservation{err: err}
			continue
		}
		valueSet := make(map[string]struct{}, len(addresses))
		for _, address := range addresses {
			if ip := address.To4(); ip != nil {
				valueSet[ip.String()] = struct{}{}
			}
		}
		values := make([]string, 0, len(valueSet))
		for value := range valueSet {
			values = append(values, value)
		}
		sort.Strings(values)
		if len(values) == 0 {
			resolved[target] = dnsObservation{err: errors.New("no IPv4 address")}
			continue
		}
		resolved[target] = dnsObservation{ips: values}
	}
	return resolved, nil
}

func containsIP(addresses []string, expected string) bool {
	for _, address := range addresses {
		if address == expected {
			return true
		}
	}
	return false
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func parseSource(source []byte) (sourceDocument, error) {
	p := newLiteralParser(source)
	if err := p.expectIdentifier("window"); err != nil {
		return sourceDocument{}, err
	}
	if err := p.expect('.'); err != nil {
		return sourceDocument{}, err
	}
	if err := p.expectIdentifier("nodeData"); err != nil {
		return sourceDocument{}, err
	}
	if err := p.expect('='); err != nil {
		return sourceDocument{}, err
	}
	value, err := p.parseValue(0)
	if err != nil {
		return sourceDocument{}, err
	}
	if p.token == ';' {
		p.next()
	}
	if p.token != scanner.EOF {
		return sourceDocument{}, p.errorf("unexpected token %q after node data", p.text)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return sourceDocument{}, err
	}
	var parsed sourceDocument
	if err := json.Unmarshal(encoded, &parsed); err != nil {
		return sourceDocument{}, err
	}
	return parsed, nil
}

type literalParser struct {
	scanner scanner.Scanner
	token   rune
	text    string
}

func newLiteralParser(source []byte) *literalParser {
	p := &literalParser{}
	p.scanner.Init(bytes.NewReader(source))
	p.scanner.Mode = scanner.ScanIdents | scanner.ScanStrings | scanner.ScanRawStrings |
		scanner.ScanInts | scanner.SkipComments
	p.scanner.IsIdentRune = func(char rune, index int) bool {
		return char == '_' || char == '$' || unicode.IsLetter(char) || index > 0 && unicode.IsDigit(char)
	}
	p.next()
	return p
}

func (p *literalParser) next() {
	p.token = p.scanner.Scan()
	p.text = p.scanner.TokenText()
}

func (p *literalParser) expect(token rune) error {
	if p.token != token {
		return p.errorf("expected %q, got %q", string(token), p.text)
	}
	p.next()
	return nil
}

func (p *literalParser) expectIdentifier(value string) error {
	if p.token != scanner.Ident || p.text != value {
		return p.errorf("expected identifier %q, got %q", value, p.text)
	}
	p.next()
	return nil
}

func (p *literalParser) parseValue(depth int) (any, error) {
	if depth > 64 {
		return nil, p.errorf("object nesting exceeds 64 levels")
	}
	switch p.token {
	case '{':
		return p.parseObject(depth + 1)
	case '[':
		return p.parseArray(depth + 1)
	case scanner.String, scanner.RawString:
		value, err := strconv.Unquote(p.text)
		if err != nil {
			return nil, p.errorf("invalid string: %v", err)
		}
		p.next()
		return value, nil
	case scanner.Int:
		value, err := strconv.ParseInt(p.text, 0, 64)
		if err != nil {
			return nil, p.errorf("invalid integer: %v", err)
		}
		p.next()
		return value, nil
	case scanner.Ident:
		value := p.text
		p.next()
		switch value {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		default:
			return nil, p.errorf("identifier %q is not a literal value", value)
		}
	default:
		return nil, p.errorf("unexpected value token %q", p.text)
	}
}

func (p *literalParser) parseObject(depth int) (map[string]any, error) {
	p.next()
	value := make(map[string]any)
	for p.token != '}' {
		var key string
		switch p.token {
		case scanner.Ident:
			key = p.text
			p.next()
		case scanner.String, scanner.RawString:
			var err error
			key, err = strconv.Unquote(p.text)
			if err != nil {
				return nil, p.errorf("invalid object key: %v", err)
			}
			p.next()
		default:
			return nil, p.errorf("unexpected object key %q", p.text)
		}
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		item, err := p.parseValue(depth)
		if err != nil {
			return nil, err
		}
		value[key] = item
		if p.token == '}' {
			break
		}
		if err := p.expect(','); err != nil {
			return nil, err
		}
		if p.token == '}' {
			break
		}
	}
	p.next()
	return value, nil
}

func (p *literalParser) parseArray(depth int) ([]any, error) {
	p.next()
	value := make([]any, 0)
	for p.token != ']' {
		item, err := p.parseValue(depth)
		if err != nil {
			return nil, err
		}
		value = append(value, item)
		if p.token == ']' {
			break
		}
		if err := p.expect(','); err != nil {
			return nil, err
		}
		if p.token == ']' {
			break
		}
	}
	p.next()
	return value, nil
}

func (p *literalParser) errorf(format string, args ...any) error {
	return fmt.Errorf("%s: %s", p.scanner.Position, fmt.Sprintf(format, args...))
}
