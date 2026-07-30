package cdncatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"text/scanner"
	"time"
	"unicode"
)

const (
	DefaultSourceURL = "https://lf3-ips.zstaticcdn.com/nodes_data.js"
	SchemaVersion    = 1
	ParserVersion    = "zstatic-node-data-v2"

	maxSourceBytes = 2 << 20
	targetSuffix   = ".ip.zstaticcdn.com"
	provincePort   = 80
	cityPort       = 443
)

type SourceMetadata struct {
	URL           string    `json:"url"`
	FetchedAt     time.Time `json:"fetched_at"`
	ParserVersion string    `json:"parser_version"`
	SourceSHA256  string    `json:"source_sha256"`
	CatalogSHA256 string    `json:"catalog_sha256"`
}

type Endpoint struct {
	Label         string  `json:"label"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	AddressFamily string  `json:"address_family"`
	Backup        *Backup `json:"backup,omitempty"`
}

type Backup struct {
	Label         string `json:"label"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	AddressFamily string `json:"address_family"`
}

type ProtocolEndpoints struct {
	IPv4      Endpoint `json:"ipv4"`
	IPv6      Endpoint `json:"ipv6"`
	DualStack Endpoint `json:"dualstack"`
}

type CarrierEndpoints struct {
	Telecom ProtocolEndpoints `json:"telecom"`
	Unicom  ProtocolEndpoints `json:"unicom"`
	Mobile  ProtocolEndpoints `json:"mobile"`
}

type Province struct {
	Name     string           `json:"name"`
	Code     string           `json:"code"`
	Carriers CarrierEndpoints `json:"carriers"`
}

type CityEndpoint struct {
	Province     string `json:"province"`
	ProvinceCode string `json:"province_code"`
	City         string `json:"city"`
	Carrier      string `json:"carrier"`
	CarrierCode  string `json:"carrier_code"`
	Label        string `json:"label"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
}

type Counts struct {
	Provinces       int `json:"provinces"`
	ProvinceTargets int `json:"province_targets"`
	CityIPv4Targets int `json:"city_ipv4_targets"`
}

type Document struct {
	Version   int            `json:"version"`
	Source    SourceMetadata `json:"source"`
	Counts    Counts         `json:"counts"`
	Provinces []Province     `json:"provinces"`
	Cities    []CityEndpoint `json:"cities"`
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

var cityNames = map[string]string{
	"ankang": "安康", "changchun": "长春", "changsha": "长沙", "chengdu": "成都",
	"chenzhou": "郴州", "dongguan": "东莞", "fuzhou": "福州", "guangzhou": "广州",
	"guiyang": "贵阳", "haikou": "海口", "hangzhou": "杭州", "hefei": "合肥",
	"huaihua": "怀化", "huhehaote": "呼和浩特", "jieyang": "揭阳", "jinan": "济南",
	"langfang": "廊坊", "lanzhou": "兰州", "lasa": "拉萨", "liaoyang": "辽阳",
	"luoyang": "洛阳", "nanjing": "南京", "nanping": "南平", "ningbo": "宁波",
	"ningde": "宁德", "qingdao": "青岛", "qingyang": "庆阳", "shenzhen": "深圳",
	"suzhou": "苏州", "taiyuan": "太原", "taizhou": "台州", "weinan": "渭南",
	"wuhan": "武汉", "wuhu": "芜湖", "wulumuqi": "乌鲁木齐", "wuxi": "无锡",
	"xiamen": "厦门", "xian": "西安", "xianyang": "咸阳", "xiangyang": "襄阳",
	"xiaogan": "孝感", "xiongan": "雄安", "yichang": "宜昌", "yongzhou": "永州",
	"zhengzhou": "郑州", "zhenjiang": "镇江", "zhongwei": "中卫", "zhuzhou": "株洲",
}

// Fetch downloads the public Zstatic node data, parses it as a restricted
// JavaScript object literal, and atomically returns a complete catalog. It does
// not resolve or connect to any node; those checks belong to the Agent run.
func Fetch(ctx context.Context, client *http.Client, sourceURL string, now time.Time) (Document, error) {
	if client == nil {
		return Document{}, errors.New("cdn catalog HTTP client is nil")
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
	document, err := buildDocument(source)
	if err != nil {
		return Document{}, err
	}
	sourceSum := sha256.Sum256(body)
	document.Version = SchemaVersion
	document.Source = SourceMetadata{
		URL: sourceURL, FetchedAt: now.UTC(), ParserVersion: ParserVersion,
		SourceSHA256: hex.EncodeToString(sourceSum[:]),
	}
	normalized, err := json.Marshal(struct {
		Version   int            `json:"version"`
		Counts    Counts         `json:"counts"`
		Provinces []Province     `json:"provinces"`
		Cities    []CityEndpoint `json:"cities"`
	}{document.Version, document.Counts, document.Provinces, document.Cities})
	if err != nil {
		return Document{}, fmt.Errorf("hash CDN catalog: %w", err)
	}
	catalogSum := sha256.Sum256(normalized)
	document.Source.CatalogSHA256 = hex.EncodeToString(catalogSum[:])
	return document, nil
}

func buildDocument(source sourceDocument) (Document, error) {
	if len(source.ProvinceBaseData) == 0 {
		return Document{}, errors.New("CDN catalog contains no province nodes")
	}
	provinceNames := make(map[string]string, len(source.ProvinceBaseData))
	for _, item := range source.ProvinceBaseData {
		if strings.TrimSpace(item.Province) == "" {
			return Document{}, errors.New("CDN catalog contains a province without a name")
		}
		for _, carrier := range carriers {
			_, _, code, err := parseProvinceEndpoint(item.Carriers[carrier.Key], carrier.Code)
			if err != nil {
				return Document{}, fmt.Errorf("%s%s: %w", item.Province, carrier.Name, err)
			}
			if previous := provinceNames[code]; previous != "" && previous != item.Province {
				return Document{}, fmt.Errorf("province code %s is used by %s and %s", code, previous, item.Province)
			}
			provinceNames[code] = item.Province
		}
	}

	cities, backups, err := buildCities(source, provinceNames)
	if err != nil {
		return Document{}, err
	}
	provinces := make([]Province, 0, len(source.ProvinceBaseData))
	for _, item := range source.ProvinceBaseData {
		var province Province
		province.Name = item.Province
		for _, carrier := range carriers {
			host, _, code, err := parseProvinceEndpoint(item.Carriers[carrier.Key], carrier.Code)
			if err != nil {
				return Document{}, fmt.Errorf("%s%s: %w", item.Province, carrier.Name, err)
			}
			province.Code = code
			endpoints := buildProtocolEndpoints(item.Province, carrier, host, backups[code+":"+carrier.Code])
			switch carrier.Key {
			case "telecom":
				province.Carriers.Telecom = endpoints
			case "unicom":
				province.Carriers.Unicom = endpoints
			case "mobile":
				province.Carriers.Mobile = endpoints
			}
		}
		provinces = append(provinces, province)
	}
	return Document{
		Counts:    Counts{Provinces: len(provinces), ProvinceTargets: len(provinces) * 9, CityIPv4Targets: len(cities)},
		Provinces: provinces, Cities: cities,
	}, nil
}

func buildProtocolEndpoints(province string, carrier carrier, ipv4Host string, city *CityEndpoint) ProtocolEndpoints {
	baseLabel := province + carrier.Name
	ipv6Host := strings.Replace(ipv4Host, "-v4"+targetSuffix, "-v6"+targetSuffix, 1)
	dualHost := strings.Replace(ipv4Host, "-v4"+targetSuffix, "-dualstack"+targetSuffix, 1)
	ipv4 := Endpoint{Label: baseLabel, Host: ipv4Host, Port: provincePort, AddressFamily: "ipv4"}
	if city != nil {
		ipv4.Backup = &Backup{Label: city.Label, Host: city.Host, Port: city.Port, AddressFamily: "ipv4"}
	}
	return ProtocolEndpoints{
		IPv4: ipv4,
		IPv6: Endpoint{
			Label: baseLabel, Host: ipv6Host, Port: provincePort, AddressFamily: "ipv6",
			Backup: &Backup{Label: baseLabel, Host: dualHost, Port: provincePort, AddressFamily: "ipv6"},
		},
		DualStack: Endpoint{Label: baseLabel, Host: dualHost, Port: provincePort, AddressFamily: "dualstack"},
	}
}

func buildCities(source sourceDocument, provinceNames map[string]string) ([]CityEndpoint, map[string]*CityEndpoint, error) {
	keys := append([]string(nil), source.CityKeyList...)
	for key := range source.ExtraCityMeta {
		keys = append(keys, key)
	}
	seen := make(map[string]struct{}, len(keys))
	var cities []CityEndpoint
	for _, rawKey := range keys {
		key := strings.ToLower(strings.TrimSpace(rawKey))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		provinceCode, cityCode, carrierCode, ok := cityCodes(key)
		if !ok {
			return nil, nil, fmt.Errorf("invalid city node key %q", rawKey)
		}
		province := provinceNames[provinceCode]
		if province == "" {
			return nil, nil, fmt.Errorf("city node %q has unknown province %q", rawKey, provinceCode)
		}
		carrierName := carrierName(carrierCode)
		city := cityNames[cityCode]
		if metadata := compactLocationName(source.ExtraCityMeta[rawKey]); metadata != "" {
			city = strings.TrimPrefix(metadata, province+"省")
			city = strings.TrimPrefix(city, province)
			city = strings.TrimSuffix(strings.TrimSuffix(city, "市"), "地区")
		}
		if city == "" {
			city = cityCode
		}
		cities = append(cities, CityEndpoint{
			Province: province, ProvinceCode: provinceCode, City: city,
			Carrier: carrierName, CarrierCode: carrierCode,
			Label: province + city + carrierName,
			Host:  key + targetSuffix, Port: cityPort,
		})
	}
	sort.Slice(cities, func(i, j int) bool { return cities[i].Host < cities[j].Host })
	backups := make(map[string]*CityEndpoint)
	for i := range cities {
		group := cities[i].ProvinceCode + ":" + cities[i].CarrierCode
		if backups[group] == nil {
			backups[group] = &cities[i]
		}
	}
	return cities, backups, nil
}

func carrierName(code string) string {
	for _, carrier := range carriers {
		if carrier.Code == code {
			return carrier.Name
		}
	}
	return code
}

func compactLocationName(value string) string {
	value = strings.TrimSpace(value)
	for _, replacement := range [][2]string{
		{"广西壮族自治区", "广西"}, {"内蒙古自治区", "内蒙古"}, {"宁夏回族自治区", "宁夏"},
		{"新疆维吾尔自治区", "新疆"}, {"西藏自治区", "西藏"},
	} {
		value = strings.Replace(value, replacement[0], replacement[1], 1)
	}
	value = strings.TrimSuffix(strings.TrimSuffix(value, "节点"), "市")
	return value
}

func parseProvinceEndpoint(endpoint, expectedCarrier string) (target string, port int, provinceCode string, err error) {
	target, rawPort, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		return "", 0, "", fmt.Errorf("invalid endpoint %q: %w", endpoint, err)
	}
	target = strings.ToLower(strings.TrimSuffix(target, "."))
	if !strings.HasSuffix(target, targetSuffix) {
		return "", 0, "", fmt.Errorf("target %q is outside zstaticcdn.com", target)
	}
	parts := strings.Split(strings.TrimSuffix(target, targetSuffix), "-")
	if len(parts) != 3 || parts[1] != expectedCarrier || parts[2] != "v4" || len(parts[0]) != 2 {
		return "", 0, "", fmt.Errorf("unexpected province target %q", target)
	}
	port, err = strconv.Atoi(rawPort)
	if err != nil || port != provincePort {
		return "", 0, "", fmt.Errorf("province endpoint must use port %d", provincePort)
	}
	return target, port, parts[0], nil
}

func cityCodes(key string) (provinceCode, cityCode, carrierCode string, ok bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(key)), "-")
	if len(parts) < 4 || len(parts[0]) != 2 || parts[len(parts)-1] != "v4" {
		return "", "", "", false
	}
	carrierCode = parts[len(parts)-2]
	if carrierCode != "cm" && carrierCode != "cu" && carrierCode != "ct" {
		return "", "", "", false
	}
	cityCode = strings.Join(parts[1:len(parts)-2], "-")
	if cityCode == "" {
		return "", "", "", false
	}
	return parts[0], cityCode, carrierCode, true
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

func (p *literalParser) next() { p.token, p.text = p.scanner.Scan(), p.scanner.TokenText() }

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
	}
	p.next()
	return value, nil
}

func (p *literalParser) errorf(format string, args ...any) error {
	return fmt.Errorf("%s: %s", p.scanner.Position, fmt.Sprintf(format, args...))
}
