# IP 质量测试脚本化重构实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 删除 agent 侧 Go 原生 IP 质量实现，改为执行上游 xykt/IPQuality 脚本（`-p -j -f`），解析其 JSON 为 Lattix 强类型格式并经现有协议上报 panel，前端报告表按新结构渲染。

**Architecture:** 保留现有 worker 隔离与 server-test.result 分片上报协议；新增 `src/agent/internal/servertest/ipquality/` 子包（脚本缓存+版本校验、依赖处理、执行、解析映射）；`shared` 新增强类型 `IPQualityResult` 全字段结构；前端报告表重写。

**Tech Stack:** Go 1.26（agent/shared），bash 脚本执行（`os/exec`），`lattix/shared/requester`，React 19 + TypeScript（frontend）。

**Spec:** `docs/superpowers/specs/2026-08-02-ip-quality-script-based-design.md`

## Global Constraints

- 脚本参数固定为 `-p -j -f`（隐私模式禁止在线报告生成、stdout JSON、完整 IP）；不加 `-4`/`-6`（双栈默认）。
- 执行超时 15 分钟；依赖安装轮次超时 120 秒。
- 缓存路径 `<dataDir>/scripts/ip.sh`（`dataDir` 为 servertest Manager 的数据目录）。
- 新报告结构必须覆盖脚本 JSON 全部字段（Head/Info/Type/Score/Factor/Media/Mail），不沿用旧 Go 报告结构。
- 依赖命令：`jq curl bc nc dig ip`（`ip` 来自 iproute2）。
- 脚本版本取自 ip.sh 内 `script_version="..."`（第 2 行，如 `v2026-03-29`）。
- 脚本源 URL：`https://raw.githubusercontent.com/xykt/IPQuality/main/ip.sh`。
- 上报协议（run/progress/result 分片）与 panel 存储机制不变。
- 所有 Go 测试在 `src/agent` 与 `src/shared` 目录运行（`go test ./...`，agent 已有 `replace lattix/shared => ../shared`）。

---

### Task 1: shared 协议层 —— 强类型 IPQualityResult

**Files:**
- Modify: `src/shared/server_testing.go`（新增 import `encoding/json`；`ServerTestCategoryResult` 增加 `IPQuality` 字段；文件末尾追加类型定义）

**Interfaces:**
- Produces: `shared.IPQualityResult`、`shared.IPQualityFamily`、`shared.IPQualityHead`、`shared.IPQualityInfo`、`shared.IPQualityCity`、`shared.IPQualityRegion`、`shared.IPQualityType`、`shared.IPQualityFactor`、`shared.IPQualityMediaStatus`、`shared.IPQualityMail`、`shared.IPQualityDNSBlacklist` —— 供 Task 2-6 使用。`ServerTestCategoryResult.IPQuality *IPQualityResult json:"ip_quality,omitempty"`。

- [ ] **Step 1: 修改 `ServerTestCategoryResult` 并追加类型定义**

在 `src/shared/server_testing.go` 中：

1) import 块加 `"encoding/json"`：
```go
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)
```

2) `ServerTestCategoryResult`（现第 250-257 行）末尾字段追加：
```go
type ServerTestCategoryResult struct {
	Category     ServerTestCategory `json:"category"`
	Status       string             `json:"status"`
	Summary      map[string]any     `json:"summary,omitempty"`
	Items        []map[string]any   `json:"items,omitempty"`
	IPQuality    *IPQualityResult   `json:"ip_quality,omitempty"`
	ErrorCode    string             `json:"error_code,omitempty"`
	ErrorMessage string             `json:"error_message,omitempty"`
}
```

3) 文件末尾追加：
```go
// IPQualityResult is the Lattix-native representation of a xykt/IPQuality
// script report. It mirrors every field the upstream ip.sh JSON output emits;
// the script is executed with -p (privacy, no online report) -j (JSON) -f
// (full IP). Subfield JSON tags keep the upstream key names 1:1.
type IPQualityResult struct {
	SchemaVersion int               `json:"schema_version"`
	ScriptVersion string            `json:"script_version"`
	ScriptStale   bool              `json:"script_stale,omitempty"`
	Families      []IPQualityFamily `json:"families"`
}

type IPQualityFamily struct {
	Family ServerTestAddressFamily      `json:"family"`
	Head   IPQualityHead                `json:"Head"`
	Info   IPQualityInfo                `json:"Info"`
	Type   IPQualityType                `json:"Type"`
	Score  map[string]string            `json:"Score,omitempty"`
	Factor IPQualityFactor              `json:"Factor"`
	Media  map[string]IPQualityMediaStatus `json:"Media,omitempty"`
	Mail   IPQualityMail                `json:"Mail"`
	Raw    json.RawMessage              `json:"raw,omitempty"`
}

type IPQualityHead struct {
	IP      string `json:"IP"`
	Command string `json:"Command,omitempty"`
	GitHub  string `json:"GitHub,omitempty"`
	Time    string `json:"Time,omitempty"`
	Version string `json:"Version,omitempty"`
}

type IPQualityInfo struct {
	ASN              string             `json:"ASN,omitempty"`
	Organization     string             `json:"Organization,omitempty"`
	Latitude         string             `json:"Latitude,omitempty"`
	Longitude        string             `json:"Longitude,omitempty"`
	DMS              string             `json:"DMS,omitempty"`
	Map              string             `json:"Map,omitempty"`
	TimeZone         string             `json:"TimeZone,omitempty"`
	City             IPQualityCity      `json:"City"`
	Region           IPQualityRegion    `json:"Region"`
	Continent        IPQualityRegion    `json:"Continent"`
	RegisteredRegion IPQualityRegion    `json:"RegisteredRegion"`
	Type             string             `json:"Type,omitempty"`
}

type IPQualityCity struct {
	Name         string `json:"Name,omitempty"`
	PostalCode   string `json:"PostalCode,omitempty"`
	SubCode      string `json:"SubCode,omitempty"`
	Subdivisions string `json:"Subdivisions,omitempty"`
}

type IPQualityRegion struct {
	Code string `json:"Code,omitempty"`
	Name string `json:"Name,omitempty"`
}

type IPQualityType struct {
	Usage   map[string]string `json:"Usage,omitempty"`
	Company map[string]string `json:"Company,omitempty"`
}

type IPQualityFactor struct {
	CountryCode map[string]string `json:"CountryCode,omitempty"`
	Proxy       map[string]*bool  `json:"Proxy,omitempty"`
	Tor         map[string]*bool  `json:"Tor,omitempty"`
	VPN         map[string]*bool  `json:"VPN,omitempty"`
	Server      map[string]*bool  `json:"Server,omitempty"`
	Abuser      map[string]*bool  `json:"Abuser,omitempty"`
	Robot       map[string]*bool  `json:"Robot,omitempty"`
}

type IPQualityMediaStatus struct {
	Status string `json:"Status,omitempty"`
	Region string `json:"Region,omitempty"`
	Type   string `json:"Type,omitempty"`
}

type IPQualityMail struct {
	Port25       *bool                `json:"Port25,omitempty"`
	Providers    map[string]*bool     `json:"providers,omitempty"`
	DNSBlacklist IPQualityDNSBlacklist `json:"DNSBlacklist"`
}

type IPQualityDNSBlacklist struct {
	Total       int `json:"Total"`
	Clean       int `json:"Clean"`
	Marked      int `json:"Marked"`
	Blacklisted int `json:"Blacklisted"`
}
```

- [ ] **Step 2: 编译验证**

Run: `go build ./...`（在 `src/shared` 目录）
Expected: 无输出、退出码 0

- [ ] **Step 3: Commit**

```bash
git add src/shared/server_testing.go
git commit -m "feat(shared): typed IP quality report structures covering all upstream fields"
```

---

### Task 2: ipquality 包 —— JSON 解析器（parse.go + 测试数据）

**Files:**
- Create: `src/agent/internal/servertest/ipquality/parse.go`
- Create: `src/agent/internal/servertest/ipquality/parse_test.go`
- Create: `src/agent/internal/servertest/ipquality/testdata/single_ipv4.json`（上游 `res/output.json` 内容，Head.IP 改为完整 `36.235.123.45`）
- Create: `src/agent/internal/servertest/ipquality/testdata/dualstack.json`（两个 JSON 文档：v4 后 v6）
- Create: `src/agent/internal/servertest/ipquality/testdata/edges.json`（`"null"` 字符串、百分数分数、未知媒体服务、Port25 为 null）

**Interfaces:**
- Consumes: `shared.IPQuality*` 类型（Task 1）
- Produces: `ipquality.ParseScriptOutput(stdout string) ([]shared.IPQualityFamily, error)`；`ipquality.ErrNoFamily` 哨兵错误（stdout 无任何 JSON 文档时返回）

- [ ] **Step 1: 写解析器实现 `parse.go`**

```go
package ipquality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"lattix/shared"
)

// ErrNoFamily reports that the script produced no JSON document at all
// (for example a host without any public address).
var ErrNoFamily = errors.New("ipquality: no address family reported")

// scriptDocument mirrors one family document emitted by ip.sh -j.
type scriptDocument struct {
	Head struct {
		IP      string `json:"IP"`
		Command string `json:"Command"`
		GitHub  string `json:"GitHub"`
		Time    string `json:"Time"`
		Version string `json:"Version"`
	} `json:"Head"`
	Info struct {
		ASN              string                 `json:"ASN"`
		Organization     string                 `json:"Organization"`
		Latitude         string                 `json:"Latitude"`
		Longitude        string                 `json:"Longitude"`
		DMS              string                 `json:"DMS"`
		Map              string                 `json:"Map"`
		TimeZone         string                 `json:"TimeZone"`
		City             shared.IPQualityCity   `json:"City"`
		Region           shared.IPQualityRegion `json:"Region"`
		Continent        shared.IPQualityRegion `json:"Continent"`
		RegisteredRegion shared.IPQualityRegion `json:"RegisteredRegion"`
		Type             string                 `json:"Type"`
	} `json:"Info"`
	Type struct {
		Usage   map[string]string `json:"Usage"`
		Company map[string]string `json:"Company"`
	} `json:"Type"`
	Score map[string]string `json:"Score"`
	Factor struct {
		CountryCode map[string]string `json:"CountryCode"`
		Proxy       map[string]*bool  `json:"Proxy"`
		Tor         map[string]*bool  `json:"Tor"`
		VPN         map[string]*bool  `json:"VPN"`
		Server      map[string]*bool  `json:"Server"`
		Abuser      map[string]*bool  `json:"Abuser"`
		Robot       map[string]*bool  `json:"Robot"`
	} `json:"Factor"`
	Media map[string]shared.IPQualityMediaStatus `json:"Media"`
	Mail  json.RawMessage                        `json:"Mail"`
}

// ParseScriptOutput decodes the one or two JSON documents the dual-stack
// script prints to stdout and maps them to Lattix families. Family order in
// stdout is IPv4 first, IPv6 second; the family is also inferred from the
// reported address.
func ParseScriptOutput(stdout string) ([]shared.IPQualityFamily, error) {
	var families []shared.IPQualityFamily
	decoder := json.NewDecoder(strings.NewReader(stdout))
	start := int64(0)
	for {
		var document scriptDocument
		if err := decoder.Decode(&document); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode ipquality document %d: %w", len(families)+1, err)
		}
		end := decoder.InputOffset()
		family := mapDocument(document, stdout[start:end], len(families))
		families = append(families, family)
		start = end
	}
	if len(families) == 0 {
		return nil, ErrNoFamily
	}
	return families, nil
}

func mapDocument(document scriptDocument, raw string, index int) shared.IPQualityFamily {
	return shared.IPQualityFamily{
		Family: familyForIP(document.Head.IP, index),
		Head: shared.IPQualityHead{
			IP:      norm(document.Head.IP),
			Command: norm(document.Head.Command),
			GitHub:  norm(document.Head.GitHub),
			Time:    norm(document.Head.Time),
			Version: norm(document.Head.Version),
		},
		Info: shared.IPQualityInfo{
			ASN:          norm(document.Info.ASN),
			Organization: norm(document.Info.Organization),
			Latitude:     norm(document.Info.Latitude),
			Longitude:    norm(document.Info.Longitude),
			DMS:          norm(document.Info.DMS),
			Map:          norm(document.Info.Map),
			TimeZone:     norm(document.Info.TimeZone),
			City: shared.IPQualityCity{
				Name:         norm(document.Info.City.Name),
				PostalCode:   norm(document.Info.City.PostalCode),
				SubCode:      norm(document.Info.City.SubCode),
				Subdivisions: norm(document.Info.City.Subdivisions),
			},
			Region: shared.IPQualityRegion{
				Code: norm(document.Info.Region.Code),
				Name: norm(document.Info.Region.Name),
			},
			Continent: shared.IPQualityRegion{
				Code: norm(document.Info.Continent.Code),
				Name: norm(document.Info.Continent.Name),
			},
			RegisteredRegion: shared.IPQualityRegion{
				Code: norm(document.Info.RegisteredRegion.Code),
				Name: norm(document.Info.RegisteredRegion.Name),
			},
			Type: norm(document.Info.Type),
		},
		Type: shared.IPQualityType{
			Usage:   cleanStrings(document.Type.Usage),
			Company: cleanStrings(document.Type.Company),
		},
		Score: document.Score, // kept verbatim: "0", "0.47%", literal "null"
		Factor: shared.IPQualityFactor{
			CountryCode: cleanStrings(document.Factor.CountryCode),
			Proxy:       document.Factor.Proxy,
			Tor:         document.Factor.Tor,
			VPN:         document.Factor.VPN,
			Server:      document.Factor.Server,
			Abuser:      document.Factor.Abuser,
			Robot:       document.Factor.Robot,
		},
		Media: cleanMedia(document.Media),
		Mail:  mapMail(document.Mail),
		Raw:   json.RawMessage(bytes.TrimSpace([]byte(raw))),
	}
}

// familyForIP infers the address family from the reported address and falls
// back to the document order (IPv4 first) when the address is unrecognizable.
func familyForIP(ip string, index int) shared.ServerTestAddressFamily {
	if addr, err := netip.ParseAddr(strings.TrimSpace(ip)); err == nil && addr.Is6() && !addr.Is4In6() {
		return shared.ServerTestIPv6
	}
	if index == 1 {
		return shared.ServerTestIPv6
	}
	return shared.ServerTestIPv4
}

// norm maps the upstream literal string "null" to an empty value.
func norm(value string) string {
	if value == "null" {
		return ""
	}
	return value
}

func cleanStrings(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cleaned := make(map[string]string, len(values))
	for key, value := range values {
		cleaned[key] = norm(value)
	}
	return cleaned
}

func cleanMedia(media map[string]shared.IPQualityMediaStatus) map[string]shared.IPQualityMediaStatus {
	if len(media) == 0 {
		return nil
	}
	cleaned := make(map[string]shared.IPQualityMediaStatus, len(media))
	for name, status := range media {
		cleaned[name] = shared.IPQualityMediaStatus{
			Status: norm(status.Status),
			Region: norm(status.Region),
			Type:   norm(status.Type),
		}
	}
	return cleaned
}

// mapMail keeps Port25 and DNSBlacklist typed and collects every other
// provider key (Gmail, Outlook, ...) into Providers.
func mapMail(raw json.RawMessage) shared.IPQualityMail {
	var result shared.IPQualityMail
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return result
	}
	if value, ok := fields["Port25"]; ok {
		var port25 bool
		if err := json.Unmarshal(value, &port25); err == nil {
			result.Port25 = &port25
		}
	}
	if value, ok := fields["DNSBlacklist"]; ok {
		result.DNSBlacklist = decodeDNSBlacklist(value)
	}
	providers := make(map[string]*bool, len(fields))
	for name, value := range fields {
		if name == "Port25" || name == "DNSBlacklist" {
			continue
		}
		var flag bool
		if err := json.Unmarshal(value, &flag); err != nil {
			continue
		}
		flagCopy := flag
		providers[name] = &flagCopy
	}
	if len(providers) > 0 {
		result.Providers = providers
	}
	return result
}

func decodeDNSBlacklist(raw json.RawMessage) shared.IPQualityDNSBlacklist {
	var counts map[string]int
	_ = json.Unmarshal(raw, &counts)
	return shared.IPQualityDNSBlacklist{
		Total:       counts["Total"],
		Clean:       counts["Clean"],
		Marked:      counts["Marked"],
		Blacklisted: counts["Blacklisted"],
	}
}
```

- [ ] **Step 2: 写测试数据文件**

`testdata/single_ipv4.json`（取上游 `res/output.json`，Head.IP 改为 `36.235.123.45`，其余字段保持原样，含 `"null"` 字符串、百分数与 null 因子）：

```json
{
  "Head": { "IP": "36.235.123.45", "Command": "bash <(curl -sL https://Check.Place) -EI", "GitHub": "https://github.com/xykt/IPQuality", "Time": "2026-01-15 09:31:25 UTC", "Version": "v2026-01-15" },
  "Info": { "ASN": "3462", "Organization": "Data Communication Business Group", "Latitude": "24.0761", "Longitude": "120.5648", "DMS": "120°33′53″E, 24°4′34″N", "Map": "https://check.place/24.0761,120.5648,15,en", "TimeZone": "Asia/Taipei", "City": { "Name": "Chang-hua", "PostalCode": "null", "SubCode": "CHA", "Subdivisions": "Changhua" }, "Region": { "Code": "TW", "Name": "Taiwan" }, "Continent": { "Code": "AS", "Name": "Asia" }, "RegisteredRegion": { "Code": "TW", "Name": "Taiwan" }, "Type": "Geo-consistent" },
  "Type": { "Usage": { "IPinfo": "ISP", "ipregistry": "ISP", "ipapi": "ISP", "AbuseIPDB": "Line ISP", "IP2LOCATION": "Line ISP" }, "Company": { "IPinfo": "ISP", "ipregistry": "ISP", "ipapi": "ISP" } },
  "Score": { "IP2LOCATION": "0", "SCAMALYTICS": "0", "ipapi": "0.47%", "AbuseIPDB": "0", "IPQS": "null", "DBIP": "0" },
  "Factor": { "CountryCode": { "IP2LOCATION": "TW", "ipapi": "TW", "ipregistry": "TW", "IPQS": "TW", "SCAMALYTICS": "TW", "ipdata": "TW", "IPinfo": "TW", "IPWHOIS": "TW", "DBIP": "TW" }, "Proxy": { "IP2LOCATION": false, "ipapi": false, "ipregistry": false, "IPQS": false, "SCAMALYTICS": false, "ipdata": false, "IPinfo": false, "IPWHOIS": false, "DBIP": false }, "Tor": { "IP2LOCATION": false, "ipapi": false, "ipregistry": false, "IPQS": false, "SCAMALYTICS": false, "ipdata": false, "IPinfo": false, "IPWHOIS": false, "DBIP": null }, "VPN": { "IP2LOCATION": false, "ipapi": false, "ipregistry": false, "IPQS": false, "SCAMALYTICS": false, "ipdata": null, "IPinfo": false, "IPWHOIS": false, "DBIP": null }, "Server": { "IP2LOCATION": false, "ipapi": false, "ipregistry": false, "IPQS": null, "SCAMALYTICS": false, "ipdata": false, "IPinfo": false, "IPWHOIS": false, "DBIP": null }, "Abuser": { "IP2LOCATION": false, "ipapi": false, "ipregistry": false, "IPQS": false, "SCAMALYTICS": false, "ipdata": false, "IPinfo": null, "IPWHOIS": null, "DBIP": false }, "Robot": { "IP2LOCATION": false, "ipapi": false, "ipregistry": null, "IPQS": false, "SCAMALYTICS": false, "ipdata": null, "IPinfo": null, "IPWHOIS": null, "DBIP": false } },
  "Media": { "TikTok": { "Status": "Yes", "Region": "TW", "Type": "Native" }, "DisneyPlus": { "Status": "Yes", "Region": "TW", "Type": "Native" }, "Netflix": { "Status": "Yes", "Region": "TW", "Type": "Native" }, "Youtube": { "Status": "Yes", "Region": "TW", "Type": "Native" }, "AmazonPrimeVideo": { "Status": "Yes", "Region": "TW", "Type": "Native" }, "Spotify": { "Status": "Block", "Region": "", "Type": "" }, "ChatGPT": { "Status": "Yes", "Region": "TW", "Type": "Native" } },
  "Mail": { "Port25": false, "Gmail": false, "Outlook": false, "Yahoo": false, "Apple": false, "QQ": false, "MailRU": false, "AOL": false, "GMX": false, "MailCOM": false, "163": false, "Sohu": false, "Sina": false, "DNSBlacklist": { "Total": 439, "Clean": 411, "Marked": 28, "Blacklisted": 0 } }
}
```

`testdata/dualstack.json`（`\r` 前缀 + 换行分隔的两个文档；第二个文档是 IPv6 地址的缩写版结构）：

```json
{
  "Head": { "IP": "203.0.113.7", "Command": "bash <(curl -sL https://Check.Place) -jpf", "GitHub": "https://github.com/xykt/IPQuality", "Time": "2026-02-01 00:00:00 UTC", "Version": "v2026-02-01" },
  "Info": { "ASN": "64512", "Organization": "Example Org", "City": { "Name": "Shanghai", "PostalCode": "null", "SubCode": "SH", "Subdivisions": "Shanghai" }, "Region": { "Code": "CN", "Name": "China" }, "Continent": { "Code": "AS", "Name": "Asia" }, "RegisteredRegion": { "Code": "CN", "Name": "China" }, "Type": "Geo-consistent" },
  "Type": { "Usage": { "IPinfo": "ISP" }, "Company": { "IPinfo": "ISP" } },
  "Score": { "IP2LOCATION": "12", "SCAMALYTICS": "3", "ipapi": "2.5%", "AbuseIPDB": "0", "IPQS": "null", "DBIP": "1" },
  "Factor": { "CountryCode": { "IPinfo": "CN" }, "Proxy": { "IPinfo": false }, "Tor": { "IPinfo": false }, "VPN": { "IPinfo": false }, "Server": { "IPinfo": false }, "Abuser": { "IPinfo": null }, "Robot": { "IPinfo": null } },
  "Media": { "TikTok": { "Status": "No", "Region": "", "Type": "" }, "ChatGPT": { "Status": "Yes", "Region": "CN", "Type": "Native" } },
  "Mail": { "Port25": true, "Gmail": true, "Outlook": false, "DNSBlacklist": { "Total": 439, "Clean": 420, "Marked": 19, "Blacklisted": 0 } }
}
{
  "Head": { "IP": "240e:390:caf2:6e00:85af:0:d0:0", "Command": "bash <(curl -sL https://Check.Place) -jpf", "GitHub": "https://github.com/xykt/IPQuality", "Time": "2026-02-01 00:00:01 UTC", "Version": "v2026-02-01" },
  "Info": { "ASN": "4134", "Organization": "Chinanet", "City": { "Name": "Shanghai", "PostalCode": "null", "SubCode": "SH", "Subdivisions": "Shanghai" }, "Region": { "Code": "CN", "Name": "China" }, "Continent": { "Code": "AS", "Name": "Asia" }, "RegisteredRegion": { "Code": "CN", "Name": "China" }, "Type": "Geo-consistent" },
  "Type": { "Usage": { "IPinfo": "ISP" }, "Company": { "IPinfo": "ISP" } },
  "Score": { "IP2LOCATION": "9", "SCAMALYTICS": "1", "ipapi": "0.9%", "AbuseIPDB": "0", "IPQS": "null", "DBIP": "0" },
  "Factor": { "CountryCode": { "IPinfo": "CN" }, "Proxy": { "IPinfo": false }, "Tor": { "IPinfo": false }, "VPN": { "IPinfo": false }, "Server": { "IPinfo": false }, "Abuser": { "IPinfo": null }, "Robot": { "IPinfo": null } },
  "Media": { "TikTok": { "Status": "No", "Region": "", "Type": "" }, "ChatGPT": { "Status": "Yes", "Region": "CN", "Type": "Native" } },
  "Mail": { "Port25": false, "Gmail": false, "Outlook": true, "DNSBlacklist": { "Total": 0, "Clean": 0, "Marked": 0, "Blacklisted": 0 } }
}
```

`testdata/edges.json`（未知媒体服务、新邮件服务名、全部 `"null"` 字符串、Port25 为 JSON null）：

```json
{
  "Head": { "IP": "192.0.2.1", "Command": "null", "GitHub": "null", "Time": "null", "Version": "null" },
  "Info": { "ASN": "null", "Organization": "null", "Latitude": "null", "Longitude": "null", "DMS": "null", "Map": "null", "TimeZone": "null", "City": { "Name": "null", "PostalCode": "null", "SubCode": "null", "Subdivisions": "null" }, "Region": { "Code": "null", "Name": "null" }, "Continent": { "Code": "null", "Name": "null" }, "RegisteredRegion": { "Code": "null", "Name": "null" }, "Type": "null" },
  "Type": { "Usage": { "IPinfo": "null" }, "Company": { "IPinfo": "null" } },
  "Score": { "IPQS": "null", "ipapi": "0.47%" },
  "Factor": { "CountryCode": { "IPinfo": "TW" }, "Proxy": { "IPinfo": null }, "Tor": { "IPinfo": true }, "VPN": { "IPinfo": false }, "Server": { "IPinfo": null }, "Abuser": { "IPinfo": null }, "Robot": { "IPinfo": null } },
  "Media": { "FutureStream": { "Status": "null", "Region": "null", "Type": "null" } },
  "Mail": { "Port25": null, "NewMailService": true, "Gmail": null, "DNSBlacklist": { "Total": 0, "Clean": 0, "Marked": 0, "Blacklisted": 0 } }
}
```

- [ ] **Step 3: 写测试 `parse_test.go`**

```go
package ipquality

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lattix/shared"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(content)
}

func TestParseSingleIPv4(t *testing.T) {
	families, err := ParseScriptOutput(loadFixture(t, "single_ipv4.json"))
	if err != nil {
		t.Fatalf("ParseScriptOutput: %v", err)
	}
	if len(families) != 1 {
		t.Fatalf("families = %d, want 1", len(families))
	}
	family := families[0]
	if family.Family != shared.ServerTestIPv4 {
		t.Errorf("family = %s, want ipv4", family.Family)
	}
	if family.Head.IP != "36.235.123.45" {
		t.Errorf("head.IP = %q", family.Head.IP)
	}
	if family.Head.Version != "v2026-01-15" {
		t.Errorf("head.Version = %q", family.Head.Version)
	}
	if family.Info.ASN != "3462" || family.Info.Organization != "Data Communication Business Group" {
		t.Errorf("info = %+v", family.Info)
	}
	if family.Info.City.PostalCode != "" {
		t.Errorf("City.PostalCode = %q, want empty (null normalized)", family.Info.City.PostalCode)
	}
	if family.Info.Type != "Geo-consistent" {
		t.Errorf("info.Type = %q", family.Info.Type)
	}
	if family.Type.Usage["AbuseIPDB"] != "Line ISP" {
		t.Errorf("usage = %v", family.Type.Usage)
	}
	if family.Score["ipapi"] != "0.47%" || family.Score["IPQS"] != "null" {
		t.Errorf("score = %v (scores stay verbatim)", family.Score)
	}
	if family.Factor.Proxy["IP2LOCATION"] == nil || *family.Factor.Proxy["IP2LOCATION"] {
		t.Errorf("Factor.Proxy[IP2LOCATION] = %v, want false", family.Factor.Proxy["IP2LOCATION"])
	}
	if family.Factor.Tor["DBIP"] != nil {
		t.Errorf("Factor.Tor[DBIP] = %v, want nil", family.Factor.Tor["DBIP"])
	}
	if family.Factor.CountryCode["IPQS"] != "TW" {
		t.Errorf("Factor.CountryCode = %v", family.Factor.CountryCode)
	}
	if family.Media["Netflix"].Status != "Yes" || family.Media["Netflix"].Type != "Native" {
		t.Errorf("media = %v", family.Media)
	}
	if family.Mail.Port25 == nil || *family.Mail.Port25 {
		t.Errorf("Mail.Port25 = %v, want false", family.Mail.Port25)
	}
	if family.Mail.Providers["Gmail"] == nil || *family.Mail.Providers["Gmail"] {
		t.Errorf("Mail.Gmail = %v, want false", family.Mail.Providers["Gmail"])
	}
	if family.Mail.DNSBlacklist != (shared.IPQualityDNSBlacklist{Total: 439, Clean: 411, Marked: 28}) {
		t.Errorf("dnsbl = %+v", family.Mail.DNSBlacklist)
	}
	if len(family.Raw) == 0 {
		t.Error("raw copy is empty")
	}
}

func TestParseDualStack(t *testing.T) {
	families, err := ParseScriptOutput(loadFixture(t, "dualstack.json"))
	if err != nil {
		t.Fatalf("ParseScriptOutput: %v", err)
	}
	if len(families) != 2 {
		t.Fatalf("families = %d, want 2", len(families))
	}
	if families[0].Family != shared.ServerTestIPv4 || families[1].Family != shared.ServerTestIPv6 {
		t.Errorf("families = %s, %s; want ipv4, ipv6", families[0].Family, families[1].Family)
	}
	if families[1].Head.IP != "240e:390:caf2:6e00:85af:0:d0:0" {
		t.Errorf("family[1].Head.IP = %q", families[1].Head.IP)
	}
	if families[1].Mail.Port25 == nil || !*families[1].Mail.Port25 {
		t.Errorf("family[1] port25 = %v, want true", families[1].Mail.Port25)
	}
}

func TestParseEdges(t *testing.T) {
	families, err := ParseScriptOutput(loadFixture(t, "edges.json"))
	if err != nil {
		t.Fatalf("ParseScriptOutput: %v", err)
	}
	if len(families) != 1 {
		t.Fatalf("families = %d, want 1", len(families))
	}
	family := families[0]
	if family.Head.IP != "192.0.2.1" {
		t.Errorf("head.IP = %q", family.Head.IP)
	}
	if family.Head.Command != "" || family.Info.ASN != "" || family.Info.City.Name != "" {
		t.Errorf("null strings not normalized: %+v %+v", family.Head, family.Info)
	}
	if family.Type.Usage["IPinfo"] != "" {
		t.Errorf("usage null = %q", family.Type.Usage["IPinfo"])
	}
	if family.Score["IPQS"] != "null" {
		t.Errorf("score IPQS = %q, want verbatim \"null\"", family.Score["IPQS"])
	}
	if family.Factor.Proxy["IPinfo"] != nil || family.Factor.Tor["IPinfo"] == nil || !*family.Factor.Tor["IPinfo"] {
		t.Errorf("factors not decoded: %+v", family.Factor)
	}
	media, ok := family.Media["FutureStream"]
	if !ok || media.Status != "" {
		t.Errorf("unknown media service not preserved: %+v", family.Media)
	}
	if family.Mail.Port25 != nil {
		t.Errorf("Port25 = %v, want nil", family.Mail.Port25)
	}
	if family.Mail.Providers["NewMailService"] == nil || !*family.Mail.Providers["NewMailService"] {
		t.Errorf("new mail provider missing: %+v", family.Mail.Providers)
	}
	if family.Mail.Providers["Gmail"] != nil {
		t.Errorf("Gmail = %v, want nil", family.Mail.Providers["Gmail"])
	}
}

func TestParseNoDocument(t *testing.T) {
	if _, err := ParseScriptOutput(""); !errors.Is(err, ErrNoFamily) {
		t.Errorf("err = %v, want ErrNoFamily", err)
	}
	if _, err := ParseScriptOutput("not json at all"); err == nil || errors.Is(err, ErrNoFamily) {
		t.Errorf("err = %v, want a decode error", err)
	}
}
```

- [ ] **Step 4: 运行测试验证**

Run: `go test ./internal/servertest/ipquality/ -v`（在 `src/agent` 目录）
Expected: 3 个 Parse 测试 + 1 个 NoDocument 测试全部 PASS

- [ ] **Step 5: Commit**

```bash
git add src/agent/internal/servertest/ipquality/
git commit -m "feat(agent): parse xykt ipquality JSON into typed Lattix families"
```

---

### Task 3: ipquality 包 —— 脚本缓存与版本校验（script.go + 测试）

**Files:**
- Create: `src/agent/internal/servertest/ipquality/script.go`
- Create: `src/agent/internal/servertest/ipquality/script_test.go`

**Interfaces:**
- Consumes: `requester.FileRequester`（`GetText(ctx, url, maxBytes)`，来自 `lattix/shared/requester`）
- Produces: `ipquality.ScriptFetcher` 接口（`GetText(context.Context, string, int64) (string, error)`，`requester.ExternalFileRequester` 天然满足）；`ipquality.ExtractScriptVersion(string) (string, bool)`；`ipquality.EnsureScript(ctx, fetcher, cacheDir) (path, version string, stale bool, err error)`

- [ ] **Step 1: 写 `script.go`**

```go
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
	// ScriptURL is the upstream ip.sh source; the script is fetched from
	// GitHub raw and cached under the agent data directory.
	ScriptURL       = "https://raw.githubusercontent.com/xykt/IPQuality/main/ip.sh"
	maxScriptBytes  = 2 << 20
	cachedScriptName = "ip.sh"
)

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
```

- [ ] **Step 2: 写测试 `script_test.go`**

```go
package ipquality

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type stubFetcher struct {
	content string
	err     error
}

func (f stubFetcher) GetText(_ context.Context, _ string, _ int64) (string, error) {
	return f.content, f.err
}

func scriptContent(version string) string {
	return "#!/bin/bash\nscript_version=\"" + version + "\"\n# body\n"
}

func TestExtractScriptVersion(t *testing.T) {
	if version, ok := ExtractScriptVersion(scriptContent("v2026-03-29")); !ok || version != "v2026-03-29" {
		t.Errorf("version = %q, %v", version, ok)
	}
	if _, ok := ExtractScriptVersion("no version here"); ok {
		t.Error("expected no version")
	}
}

func TestEnsureScriptFreshCache(t *testing.T) {
	dir := t.TempDir()
	path, version, stale, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir)
	if err != nil {
		t.Fatalf("EnsureScript: %v", err)
	}
	if path != filepath.Join(dir, "ip.sh") || version != "v1" || stale {
		t.Errorf("path=%q version=%q stale=%v", path, version, stale)
	}
	content, _ := os.ReadFile(path)
	if string(content) != scriptContent("v1") {
		t.Errorf("cached content mismatch")
	}

	// Same version reuses the cache without a rewrite.
	info, _ := os.Stat(path)
	modTime := info.ModTime()
	_, version, stale, err = EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir)
	if err != nil || version != "v1" || stale {
		t.Fatalf("recheck: %v %q %v", err, version, stale)
	}
	info, _ = os.Stat(path)
	if !info.ModTime().Equal(modTime) {
		t.Error("cache was rewritten despite same version")
	}
}

func TestEnsureScriptUpdatesVersion(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	path, version, stale, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v2")}, dir)
	if err != nil {
		t.Fatalf("EnsureScript: %v", err)
	}
	if version != "v2" || stale {
		t.Errorf("version=%q stale=%v, want v2/false", version, stale)
	}
	content, _ := os.ReadFile(path)
	if string(content) != scriptContent("v2") {
		t.Error("cache not replaced")
	}
}

func TestEnsureScriptFallbackToCache(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := EnsureScript(context.Background(), stubFetcher{content: scriptContent("v1")}, dir); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	path, version, stale, err := EnsureScript(context.Background(), stubFetcher{err: os.ErrNotExist}, dir)
	if err != nil {
		t.Fatalf("EnsureScript: %v", err)
	}
	if version != "v1" || !stale {
		t.Errorf("version=%q stale=%v, want v1/true", version, stale)
	}
	content, _ := os.ReadFile(path)
	if string(content) != scriptContent("v1") {
		t.Error("cache content lost")
	}
}

func TestEnsureScriptNoCacheAndFetchFails(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := EnsureScript(context.Background(), stubFetcher{err: os.ErrNotExist}, dir); err == nil {
		t.Fatal("expected error when no cache and fetch fails")
	}
}
```

- [ ] **Step 3: 运行测试**

Run: `go test ./internal/servertest/ipquality/ -v`
Expected: script 测试 6 个 PASS

- [ ] **Step 4: Commit**

```bash
git add src/agent/internal/servertest/ipquality/
git commit -m "feat(agent): cached ip.sh with version-checked refresh and fallback"
```

---

### Task 4: ipquality 包 —— 依赖与执行（deps.go + run.go + 测试）

**Files:**
- Create: `src/agent/internal/servertest/ipquality/deps.go`
- Create: `src/agent/internal/servertest/ipquality/run.go`
- Create: `src/agent/internal/servertest/ipquality/run_test.go`
- Create: `src/agent/internal/servertest/ipquality/testdata/fake_ip.sh`

**Interfaces:**
- Consumes: `ipquality.EnsureScript`（Task 3）、`ipquality.ParseScriptOutput`（Task 2）、`shared.IPQualityResult`（Task 1）、`requester.ExternalFileRequester`
- Produces: `ipquality.Runner{DataDir string; Fetcher ScriptFetcher; Timeout time.Duration; Missing func() []string}`；`(*Runner).Run(ctx, progress func(string)) (shared.IPQualityResult, error)`

- [ ] **Step 1: 写 `deps.go`**

```go
package ipquality

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const installTimeout = 2 * time.Minute

var requiredCommands = []string{"jq", "curl", "bc", "nc", "dig", "ip"}

// MissingDependencies lists the script runtime commands absent from PATH.
func MissingDependencies() []string {
	var missing []string
	for _, command := range requiredCommands {
		if _, err := exec.LookPath(command); err != nil {
			missing = append(missing, command)
		}
	}
	return missing
}

// InstallDependencies runs the script's own installer (-y auto-install) for
// a short window and polls until every dependency appears. The script keeps
// running its v4 checks after installing; the process group is killed as soon
// as the dependencies are ready.
func InstallDependencies(ctx context.Context, scriptPath string, check func() []string) error {
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", scriptPath, "-y", "-p", "-4")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start dependency installer: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err == nil && len(check()) == 0 {
				return nil
			}
			return fmt.Errorf("dependency install: %w", err)
		case <-ticker.C:
			if len(check()) == 0 {
				_ = killProcessGroup(cmd.Process)
				<-done
				return nil
			}
		case <-ctx.Done():
			_ = killProcessGroup(cmd.Process)
			<-done
			return fmt.Errorf("dependency install timed out after %s", installTimeout)
		}
	}
}

func killProcessGroup(process *exec.Cmd) error {
	if process == nil || process.Process == nil {
		return nil
	}
	return syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
}
```

- [ ] **Step 2: 写 `run.go`**

```go
package ipquality

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lattix/shared"
	"lattix/shared/requester"
)

const (
	defaultRunTimeout = 15 * time.Minute
	maxOutputBytes    = 16 << 20
)

// Runner executes the xykt/IPQuality script for the Lattix agent.
type Runner struct {
	DataDir string
	// Fetcher defaults to requester.ExternalFileRequester with a 30s client.
	Fetcher ScriptFetcher
	// Timeout bounds one full script run; defaults to 15 minutes.
	Timeout time.Duration
	// Missing lists absent script dependencies; defaults to MissingDependencies.
	Missing func() []string
}

// Run prepares the script, ensures dependencies, executes it with
// "-p -j -f" and maps the JSON output into a Lattix report.
func (r *Runner) Run(ctx context.Context, progress func(string)) (shared.IPQualityResult, error) {
	if progress == nil {
		progress = func(string) {}
	}
	fetcher := r.Fetcher
	if fetcher == nil {
		fetcher = requester.ExternalFileRequester{Doer: &http.Client{Timeout: 30 * time.Second}}
	}
	missing := r.Missing
	if missing == nil {
		missing = MissingDependencies
	}

	progress("下载脚本")
	scriptPath, scriptVersion, stale, err := EnsureScript(ctx, fetcher, filepath.Join(r.DataDir, "scripts"))
	if err != nil {
		return shared.IPQualityResult{}, err
	}

	progress("检查依赖")
	if len(missing()) > 0 {
		progress("安装依赖")
		if err := InstallDependencies(ctx, scriptPath, missing); err != nil {
			return shared.IPQualityResult{}, err
		}
	}

	progress("运行检测")
	stdout, err := runScript(ctx, scriptPath, r.Timeout)
	if err != nil {
		return shared.IPQualityResult{}, err
	}

	progress("解析结果")
	families, err := ParseScriptOutput(stdout)
	if err != nil {
		return shared.IPQualityResult{}, err
	}
	return shared.IPQualityResult{
		SchemaVersion: shared.ServerTestSchemaVersion,
		ScriptVersion: scriptVersion,
		ScriptStale:   stale,
		Families:      families,
	}, nil
}

func runScript(ctx context.Context, scriptPath string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", scriptPath, "-p", "-j", "-f")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.max = maxOutputBytes
	stderr.max = maxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start ip.sh: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		_ = killProcessGroup(cmd)
		<-done
		return "", fmt.Errorf("ip.sh timed out after %s", timeout)
	case err := <-done:
		if stdout.exceeded {
			return "", fmt.Errorf("ip.sh output exceeds %d bytes", maxOutputBytes)
		}
		if err != nil {
			tail := strings.TrimSpace(stderr.buf.String())
			if len(tail) > 2048 {
				tail = tail[len(tail)-2048:]
			}
			if tail != "" {
				return "", fmt.Errorf("ip.sh failed: %w (stderr: %s)", err, tail)
			}
			return "", fmt.Errorf("ip.sh failed: %w", err)
		}
	}
	return stdout.buf.String(), nil
}

type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if !b.exceeded && b.buf.Len()+len(p) > b.max {
		b.exceeded = true
		return 0, errors.New("output limit exceeded")
	}
	return b.buf.Write(p)
}
```

- [ ] **Step 3: 写假脚本 `testdata/fake_ip.sh`**

```bash
#!/bin/bash
# Fake ip.sh for unit tests: prints one dual-stack report pair to stdout.
cat <<'EOF'
{
  "Head": { "IP": "203.0.113.9", "Command": "bash fake", "GitHub": "x", "Time": "2026-01-01 00:00:00 UTC", "Version": "v-fake" },
  "Info": { "ASN": "64512", "Organization": "Fake Org", "City": { "Name": "Test", "PostalCode": "null", "SubCode": "T", "Subdivisions": "T" }, "Region": { "Code": "CN", "Name": "China" }, "Continent": { "Code": "AS", "Name": "Asia" }, "RegisteredRegion": { "Code": "CN", "Name": "China" }, "Type": "Geo-consistent" },
  "Type": { "Usage": { "IPinfo": "ISP" }, "Company": { "IPinfo": "ISP" } },
  "Score": { "IP2LOCATION": "1", "SCAMALYTICS": "0", "ipapi": "0.1%", "AbuseIPDB": "0", "IPQS": "null", "DBIP": "0" },
  "Factor": { "CountryCode": { "IPinfo": "CN" }, "Proxy": { "IPinfo": false }, "Tor": { "IPinfo": false }, "VPN": { "IPinfo": false }, "Server": { "IPinfo": false }, "Abuser": { "IPinfo": null }, "Robot": { "IPinfo": null } },
  "Media": { "TikTok": { "Status": "Yes", "Region": "CN", "Type": "Native" } },
  "Mail": { "Port25": false, "Gmail": true, "DNSBlacklist": { "Total": 439, "Clean": 439, "Marked": 0, "Blacklisted": 0 } }
}
{
  "Head": { "IP": "240e:390::1", "Command": "bash fake", "GitHub": "x", "Time": "2026-01-01 00:00:01 UTC", "Version": "v-fake" },
  "Info": { "ASN": "4134", "Organization": "Fake6 Org", "City": { "Name": "Test", "PostalCode": "null", "SubCode": "T", "Subdivisions": "T" }, "Region": { "Code": "CN", "Name": "China" }, "Continent": { "Code": "AS", "Name": "Asia" }, "RegisteredRegion": { "Code": "CN", "Name": "China" }, "Type": "Geo-consistent" },
  "Type": { "Usage": { "IPinfo": "ISP" }, "Company": { "IPinfo": "ISP" } },
  "Score": { "IP2LOCATION": "0", "SCAMALYTICS": "0", "ipapi": "0.0%", "AbuseIPDB": "0", "IPQS": "null", "DBIP": "0" },
  "Factor": { "CountryCode": { "IPinfo": "CN" }, "Proxy": { "IPinfo": false }, "Tor": { "IPinfo": false }, "VPN": { "IPinfo": false }, "Server": { "IPinfo": false }, "Abuser": { "IPinfo": null }, "Robot": { "IPinfo": null } },
  "Media": { "TikTok": { "Status": "No", "Region": "", "Type": "" } },
  "Mail": { "Port25": true, "Gmail": false, "DNSBlacklist": { "Total": 0, "Clean": 0, "Marked": 0, "Blacklisted": 0 } }
}
EOF
exit 0
```

- [ ] **Step 4: 写测试 `run_test.go`**

```go
package ipquality

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lattix/shared"
)

type fileFetcher struct{ dir string }

func (f fileFetcher) GetText(_ context.Context, _ string, _ int64) (string, error) {
	content, err := os.ReadFile(filepath.Join(f.dir, "ip.sh"))
	return string(content), err
}

func fakeScriptContent() string {
	content, err := os.ReadFile(filepath.Join("testdata", "fake_ip.sh"))
	if err != nil {
		panic(err)
	}
	version := "v-fake"
	withVersion := "script_version=\"" + version + "\"\n" + string(content)
	return withVersion
}

func TestRunnerRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ip.sh"), []byte(fakeScriptContent()), 0o700); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	runner := Runner{
		DataDir: dir,
		Fetcher: fileFetcher{dir: dir},
		Timeout: time.Minute,
		Missing: func() []string { return nil },
	}
	var stages []string
	result, err := runner.Run(context.Background(), func(stage string) { stages = append(stages, stage) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ScriptVersion != "v-fake" || result.ScriptStale {
		t.Errorf("version=%q stale=%v", result.ScriptVersion, result.ScriptStale)
	}
	if len(result.Families) != 2 {
		t.Fatalf("families = %d, want 2", len(result.Families))
	}
	if result.Families[0].Family != shared.ServerTestIPv4 || result.Families[1].Family != shared.ServerTestIPv6 {
		t.Errorf("families = %s, %s", result.Families[0].Family, result.Families[1].Family)
	}
	if result.Families[0].Mail.Providers["Gmail"] == nil || !*result.Families[0].Mail.Providers["Gmail"] {
		t.Errorf("gmail = %v", result.Families[0].Mail.Providers["Gmail"])
	}
	if len(stages) < 3 || stages[0] != "下载脚本" {
		t.Errorf("stages = %v", stages)
	}
	// The fake script was cached into <dir>/scripts/ip.sh.
	if _, err := os.Stat(filepath.Join(dir, "scripts", "ip.sh")); err != nil {
		t.Errorf("cached script missing: %v", err)
	}
}

func TestRunnerRunNoPublicAddress(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/bash\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "ip.sh"), []byte("script_version=\"v-empty\"\n"+script), 0o700); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	runner := Runner{
		DataDir: dir,
		Fetcher: fileFetcher{dir: dir},
		Timeout: time.Minute,
		Missing: func() []string { return nil },
	}
	_, err := runner.Run(context.Background(), nil)
	if !strings.Contains(err.Error(), ErrNoFamily.Error()) {
		t.Fatalf("err = %v, want ErrNoFamily", err)
	}
}

func TestRunnerRunTimeout(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/bash\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "ip.sh"), []byte("script_version=\"v-slow\"\n"+script), 0o700); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	runner := Runner{
		DataDir: dir,
		Fetcher: fileFetcher{dir: dir},
		Timeout: 200 * time.Millisecond,
		Missing: func() []string { return nil },
	}
	_, err := runner.Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
}

func TestInstallDependenciesPollsAndKills(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "installer.sh")
	marker := filepath.Join(dir, "ready")
	content := "#!/bin/bash\n" +
		"echo installing\n" +
		"touch " + marker + "\n" +
		"sleep 30\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatalf("write installer: %v", err)
	}
	check := func() []string {
		if _, err := os.Stat(marker); err == nil {
			return nil
		}
		return []string{"jq"}
	}
	if err := InstallDependencies(context.Background(), script, check); err != nil {
		t.Fatalf("InstallDependencies: %v", err)
	}
	// The installer must have been killed before its sleep ended.
	if err := os.Stat(marker); err != nil {
		t.Errorf("marker missing: %v", err)
	}
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/servertest/ipquality/ -v`
Expected: run 测试 4 个 + 既有 parse/script 测试全部 PASS

- [ ] **Step 6: Commit**

```bash
git add src/agent/internal/servertest/ipquality/
git commit -m "feat(agent): run ip.sh with privacy JSON mode and enforce timeout"
```

---

### Task 5: servertest 集成 —— runner/worker 接线并删除旧实现

**Files:**
- Modify: `src/agent/internal/servertest/worker.go`（workerInput 加 `DataDir`；WorkerMain 传 dataDir；runWorkerProcess 填充）
- Modify: `src/agent/internal/servertest/runner.go`（`NewRunner` 签名、`Runner.DataDir`、phase 注册、删除 `runtimeSummary`、清理 import）
- Create: `src/agent/internal/servertest/ip_quality_script.go`（新的 `runIPQualityScript`）
- Delete: `src/agent/internal/servertest/ip_quality.go`
- Delete: `src/agent/internal/servertest/ip_providers.go`
- Delete: `src/agent/internal/servertest/dnsbl.go`
- Delete: `src/agent/internal/servertest/dnsbl_snapshot.txt`
- Delete: `src/agent/internal/servertest/asn.go`
- Delete: `src/agent/internal/servertest/ip_providers_test.go`
- Delete: `src/agent/internal/servertest/dnsbl_test.go`
- Delete: `src/agent/internal/servertest/asn_test.go`

**Interfaces:**
- Consumes: `ipquality.Runner`（Task 4）、`shared.ServerTestCategoryResult.IPQuality`（Task 1）
- Produces: `servertest.NewRunner(agentVersion, dataDir string)`、`Runner.DataDir`、`runIPQualityScript` phase 函数

- [ ] **Step 1: worker.go 三处修改**

1) `workerInput`（第 21-26 行）：
```go
type workerInput struct {
	AgentVersion  string                      `json:"agent_version"`
	DataDir       string                      `json:"data_dir"`
	SandboxState  string                      `json:"sandbox_state"`
	SandboxReason string                      `json:"sandbox_reason,omitempty"`
	Payload       shared.ServerTestRunPayload `json:"payload"`
}
```

2) `WorkerMain`（第 46 行）：
```go
	runner := NewRunner(input.AgentVersion, input.DataDir)
```

3) `runWorkerProcess`（第 118-123 行）：
```go
	input, err := json.Marshal(workerInput{
		AgentVersion: agentVersion, DataDir: dataDir,
		SandboxState: sandboxState, SandboxReason: sandboxReason, Payload: payload,
	})
```

- [ ] **Step 2: runner.go 修改**

1) `Runner` 结构（第 18-25 行）：
```go
type Runner struct {
	AgentVersion string
	DataDir      string
	Now          func() time.Time
}

func NewRunner(agentVersion, dataDir string) *Runner {
	return &Runner{AgentVersion: agentVersion, DataDir: dataDir, Now: time.Now}
}
```

2) phase 注册（第 99 行）：
```go
		{name: "ip_quality", categories: []shared.ServerTestCategory{shared.ServerTestIPQuality}, run: r.runIPQualityScript},
```

3) 删除 `runtimeSummary`（第 171-173 行）及其使用；清理 import（删除 `"fmt"`、`"runtime"`；保留 `context, os, sort, time`）。

- [ ] **Step 3: 新建 `ip_quality_script.go`**

```go
package servertest

import (
	"context"
	"errors"
	"time"

	"lattix/agent/internal/servertest/ipquality"
	"lattix/shared"
)

const ipQualityScriptTimeout = 15 * time.Minute

// runIPQualityScript executes the upstream xykt/IPQuality script in privacy
// JSON mode and maps the parsed report into the Lattix category result.
func (r *Runner) runIPQualityScript(parent context.Context, category shared.ServerTestCategory, _ []shared.ServerTestTarget, update func(int, int, string)) shared.ServerTestCategoryResult {
	ctx, cancel := context.WithTimeout(parent, ipQualityScriptTimeout)
	defer cancel()
	runner := ipquality.Runner{DataDir: r.DataDir, Timeout: ipQualityScriptTimeout}
	result, err := runner.Run(ctx, func(message string) { update(0, 1, message) })
	if err != nil {
		status := "failed"
		code := "ipquality_script_failed"
		if errors.Is(err, ipquality.ErrNoFamily) {
			status, code = "unavailable", "no_public_address"
		}
		update(1, 1, status)
		return shared.ServerTestCategoryResult{
			Category: category, Status: status, ErrorCode: code, ErrorMessage: err.Error(),
		}
	}
	update(1, 1, "完成")
	status := "available"
	if len(result.Families) == 0 {
		status = "unavailable"
	}
	return shared.ServerTestCategoryResult{
		Category: category, Status: status,
		Summary: map[string]any{
			"script_version": result.ScriptVersion,
			"script_stale":   result.ScriptStale,
			"families":       len(result.Families),
		},
		IPQuality: &result,
	}
}
```

- [ ] **Step 4: 删除旧文件**

Run: `git rm src/agent/internal/servertest/ip_quality.go src/agent/internal/servertest/ip_providers.go src/agent/internal/servertest/dnsbl.go src/agent/internal/servertest/dnsbl_snapshot.txt src/agent/internal/servertest/asn.go src/agent/internal/servertest/ip_providers_test.go src/agent/internal/servertest/dnsbl_test.go src/agent/internal/servertest/asn_test.go`

- [ ] **Step 5: 编译与测试**

Run: `go build ./... && go vet ./... && go test ./...`（在 `src/agent` 目录）
Expected: 全部通过；无未使用 import 错误

- [ ] **Step 6: Commit**

```bash
git add src/agent
git commit -m "feat(agent): run upstream ipquality script in worker; drop native Go implementation"
```

---

### Task 6: 前端 —— 类型与 IP 质量报告表重写

**Files:**
- Modify: `src/frontend/src/lib/types.ts`（`ServerTestCategoryResult` 加 `ip_quality?`；追加 `IPQuality*` 接口）
- Modify: `src/frontend/src/components/ServerTestPanel.tsx`（替换 `IPQualityReport`/`ProviderTable` 为按新结构渲染的报告区块；`ReportCategory` 不变）

**Interfaces:**
- Consumes: `ServerTestReport` 中的 `ServerTestCategoryResult.ip_quality`

- [ ] **Step 1: types.ts 追加类型**

`ServerTestCategoryResult`（第 241-248 行）加：
```ts
export interface ServerTestCategoryResult {
  category: ServerTestCategory
  status: string
  summary?: Record<string, unknown>
  items?: Array<Record<string, unknown>>
  ip_quality?: IPQualityResult
  error_code?: string
  error_message?: string
}
```

文件末尾（`IPQualityResult` 之前）追加：
```ts
export interface IPQualityResult {
  schema_version: number
  script_version: string
  script_stale?: boolean
  families: IPQualityFamily[]
}

export interface IPQualityFamily {
  family: 'ipv4' | 'ipv6' | 'dualstack'
  Head: IPQualityHead
  Info: IPQualityInfo
  Type: IPQualityType
  Score?: Record<string, string>
  Factor: IPQualityFactor
  Media?: Record<string, IPQualityMediaStatus>
  Mail: IPQualityMail
  raw?: string
}

export interface IPQualityHead {
  IP: string
  Command?: string
  GitHub?: string
  Time?: string
  Version?: string
}

export interface IPQualityInfo {
  ASN?: string
  Organization?: string
  Latitude?: string
  Longitude?: string
  DMS?: string
  Map?: string
  TimeZone?: string
  City?: IPQualityCity
  Region?: IPQualityRegion
  Continent?: IPQualityRegion
  RegisteredRegion?: IPQualityRegion
  Type?: string
}

export interface IPQualityCity {
  Name?: string
  PostalCode?: string
  SubCode?: string
  Subdivisions?: string
}

export interface IPQualityRegion {
  Code?: string
  Name?: string
}

export interface IPQualityType {
  Usage?: Record<string, string>
  Company?: Record<string, string>
}

export interface IPQualityFactor {
  CountryCode?: Record<string, string>
  Proxy?: Record<string, boolean | null>
  Tor?: Record<string, boolean | null>
  VPN?: Record<string, boolean | null>
  Server?: Record<string, boolean | null>
  Abuser?: Record<string, boolean | null>
  Robot?: Record<string, boolean | null>
}

export interface IPQualityMediaStatus {
  Status?: string
  Region?: string
  Type?: string
}

export interface IPQualityMail {
  Port25?: boolean | null
  Providers?: Record<string, boolean | null>
  DNSBlacklist: IPQualityDNSBlacklist
}

export interface IPQualityDNSBlacklist {
  Total: number
  Clean: number
  Marked: number
  Blacklisted: number
}
```

- [ ] **Step 2: ServerTestPanel.tsx 重写 IPQualityReport**

将 `ProviderTable`（第 175-212 行）整体删除；将 `IPQualityReport`（第 214-254 行）替换为以下实现（`asRecord`/`asRecords`/`text`/`statusBadge` 保留，`number` 保留）：

```tsx
const factorLabels: Record<string, string> = {
  CountryCode: '国家代码', Proxy: '代理', Tor: 'Tor', VPN: 'VPN', Server: '机房', Abuser: '滥用', Robot: '爬虫',
}
const factorKeys: Array<keyof IPQualityFactor> = ['Proxy', 'Tor', 'VPN', 'Server', 'Abuser', 'Robot']

function ipText(value: string | undefined): string {
  return value && value !== 'null' ? value : '-'
}

function IPQualityFamilySection({ family }: { family: IPQualityFamily }) {
  const info = family.Info ?? {}
  const scoreRows = Object.entries(family.Score ?? {})
  const factorRows = (() => {
    const names = new Set<string>()
    for (const key of factorKeys) {
      for (const name of Object.keys(family.Factor?.[key] ?? {})) names.add(name)
    }
    return Array.from(names)
  })()
  const mediaRows = Object.entries(family.Media ?? {})
  const mailRows = Object.entries(family.Mail?.Providers ?? {})
  const dnsbl = family.Mail?.DNSBlacklist
  return (
    <section className="space-y-3 border-t pt-4 first:border-0 first:pt-0">
      <div className="flex flex-wrap items-center gap-2">
        <h4 className="text-sm font-medium">{family.family.toUpperCase()}</h4>
        <span className="font-mono text-xs text-muted-foreground">{ipText(family.Head?.IP)}</span>
        {family.Head?.Version ? <Badge variant="outline">{family.Head.Version}</Badge> : null}
        {family.Head?.Time ? <span className="text-xs text-muted-foreground">{family.Head.Time}</span> : null}
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="border p-3">
          <div className="mb-2 text-xs font-medium">基础信息</div>
          <dl className="grid grid-cols-2 gap-x-3 gap-y-1.5 text-xs">
            <dt className="text-muted-foreground">ASN</dt><dd>{ipText(info.ASN)}{info.Organization ? ` · ${info.Organization}` : ''}</dd>
            <dt className="text-muted-foreground">城市</dt><dd>{ipText(info.City?.Name)}{info.City?.Subdivisions ? ` · ${info.City.Subdivisions}` : ''}</dd>
            <dt className="text-muted-foreground">地区</dt><dd>{ipText(info.Region?.Name)}{info.Region?.Code ? ` (${info.Region.Code})` : ''}</dd>
            <dt className="text-muted-foreground">注册地</dt><dd>{ipText(info.RegisteredRegion?.Name)}{info.RegisteredRegion?.Code ? ` (${info.RegisteredRegion.Code})` : ''}</dd>
            <dt className="text-muted-foreground">时区</dt><dd>{ipText(info.TimeZone)}</dd>
            <dt className="text-muted-foreground">IP 类型</dt><dd>{ipText(info.Type)}</dd>
          </dl>
        </div>
        <div className="border p-3">
          <div className="mb-2 text-xs font-medium">风险评分</div>
          <table className="w-full text-left text-xs">
            <tbody className="divide-y">
              {scoreRows.length === 0 ? <tr><td className="py-1.5 text-muted-foreground">无数据</td></tr> : scoreRows.map(([name, score]) => (
                <tr key={name}><td className="py-1.5 font-medium">{name}</td><td className="py-1.5 text-right tabular-nums">{score === 'null' ? '-' : score}</td></tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <div className="overflow-x-auto border">
        <table className="w-full min-w-[560px] text-left text-xs">
          <thead className="border-b text-muted-foreground">
            <tr><th className="px-2 py-2 font-medium">数据库</th>{factorKeys.map((key) => <th key={key} className="px-2 py-2 text-center font-medium">{factorLabels[key]}</th>)}<th className="px-2 py-2 text-center font-medium">{factorLabels.CountryCode}</th></tr>
          </thead>
          <tbody className="divide-y">
            {factorRows.length === 0 ? <tr><td colSpan={factorKeys.length + 2} className="px-2 py-2 text-muted-foreground">无数据</td></tr> : factorRows.map((name) => (
              <tr key={name}>
                <td className="px-2 py-2 font-medium">{name}</td>
                {factorKeys.map((key) => {
                  const value = family.Factor?.[key]?.[name]
                  return <td key={key} className="px-2 py-2 text-center">{value === undefined || value === null ? <span className="text-muted-foreground">-</span> : value ? <span className="text-destructive">是</span> : <span className="text-success">否</span>}</td>
                })}
                <td className="px-2 py-2 text-center">{ipText(family.Factor?.CountryCode?.[name])}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="border p-3">
          <div className="mb-2 text-xs font-medium">流媒体与 AI</div>
          {mediaRows.length === 0 ? <div className="text-xs text-muted-foreground">无数据</div> : <div className="flex flex-wrap gap-1.5">{mediaRows.map(([name, media]) => <Badge key={name} variant={statusBadge(media.Status ?? '')} title={`${media.Type ?? ''}${media.Region ? ` · ${media.Region}` : ''}`}>{name}{media.Region ? ` · ${media.Region}` : ''}</Badge>)}</div>}
        </div>
        <div className="border p-3">
          <div className="mb-2 text-xs font-medium">邮局检测</div>
          <div className="flex flex-wrap gap-1.5">
            <Badge variant={family.Mail?.Port25 ? 'secondary' : 'outline'}>{family.Mail?.Port25 == null ? '25 端口未知' : `25 端口${family.Mail.Port25 ? '开放' : '受限'}`}</Badge>
            {mailRows.map(([name, open]) => <Badge key={name} variant={open ? 'secondary' : 'outline'}>{name}{open === null ? '' : open ? '' : ''}</Badge>)}
          </div>
          {dnsbl ? (
            <div className="mt-2 text-xs text-muted-foreground">DNSBL {dnsbl.Total} 库 · 正常 {dnsbl.Clean} · 标记 {dnsbl.Marked} · 黑名单 {dnsbl.Blacklisted}</div>
          ) : null}
        </div>
      </div>
    </section>
  )
}

function IPQualityReport({ category }: { category: ServerTestCategoryResult }) {
  const result = category.ip_quality
  if (!result || result.families.length === 0) return null
  return (
    <div className="space-y-4">
      {result.script_stale ? <div className="flex items-start gap-2 bg-warning/10 px-3 py-2 text-xs text-warning"><AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" /><span>脚本版本可能过期（script_version={result.script_version}），本次复用缓存</span></div> : null}
      {result.families.map((family) => <IPQualityFamilySection key={family.family} family={family} />)}
    </div>
  )
}
```

注意：上面的邮局 Badge 渲染逻辑有冗余（`open ? '' : ''`），简化为：

```tsx
{mailRows.map(([name, open]) => <Badge key={name} variant={open ? 'secondary' : 'outline'}>{name}</Badge>)}
```

并把 import 块加上 `IPQualityFactor`、`IPQualityFamily` 等类型（`import type { ... } from '@/lib/types'` 中追加）。

- [ ] **Step 3: 类型检查与构建**

Run: `bun run build`（在 `src/frontend` 目录；若 bun 不可用则 `npx tsc -b`）
Expected: 类型检查通过、vite 构建成功

- [ ] **Step 4: Commit**

```bash
git add src/frontend
git commit -m "feat(panel): render script-based IP quality report tables"
```

---

### Task 7: 文档回写

**Files:**
- Modify: `docs/server-testing-design.md`

- [ ] **Step 1: 更新 §1 目标与非目标**

将第 12-14 行：

```markdown
测试动作参考 [TcpQuality](https://github.com/ibsgss/TcpQuality) 与
[NodeQuality](https://github.com/LloydAsp/NodeQuality) 的原理重新实现为项目内 Go 函数，
不下载或执行上游脚本。范围明确如下：
```

替换为：

```markdown
TCP/回程/大包/测速动作参考 [TcpQuality](https://github.com/ibsgss/TcpQuality) 与
[NodeQuality](https://github.com/LloydAsp/NodeQuality) 的原理实现为项目内 Go 函数。
IP 质量检测执行上游 [xykt/IPQuality](https://github.com/xykt/IPQuality) 脚本
（`bash ip.sh -p -j -f`），Agent 缓存脚本并按版本校验更新。范围明确如下：
```

并将第 17-22 行 bullet 中“不运行 25 端口邮件测试”“不上传报告到…Check.Place…”两条更新为：

```markdown
- IP 质量脚本以 `-p` 隐私模式运行，禁止生成在线报告链接、禁止上传报告；
- 脚本会执行 25 端口邮件连通性检测与 DNSBL 黑名单查询；
- 不调用 `tcpquality.ibsgss.uk` 等上游辅助代理；
```

- [ ] **Step 2: 重写 §4 IP 质量数据源**

将 §4（第 79-101 行）整节替换为：

```markdown
## 4. IP 质量检测（脚本化）

IP 质量检测执行上游 xykt/IPQuality 脚本 `ip.sh`，固定参数 `-p -j -f`：

- `-p` 隐私模式：不生成在线报告链接，不上传 `upload.check.place`；
- `-j` 输出 JSON 到 stdout（双栈时依次输出 IPv4、IPv6 两个文档）；
- `-f` 展示完整出口 IP。

脚本来源 `https://raw.githubusercontent.com/xykt/IPQuality/main/ip.sh`，由 Agent 缓存在
数据目录 `scripts/ip.sh`。每次测试前拉取最新脚本并与缓存比对 `script_version`，相同则复用
缓存，不同则原子替换；拉取失败回退缓存并在报告中标记 `script_stale`。

脚本依赖 `jq curl bc netcat dnsutils iproute2`。Agent 在测试前用 `exec.LookPath` 检测，
缺失时运行脚本自身的 `-y` 自动安装轮次（120 秒窗口，依赖就绪后立即终止进程组）。

执行超时 15 分钟，超时后终止整个进程组。脚本输出经流式 JSON 解码拆解为每个地址家族一份
文档，映射为 Lattix 强类型结构（`shared.IPQualityResult`），字段覆盖脚本输出的全部内容：
Head（IP/版本/时间）、Info（ASN/组织/城市/地区/注册地/时区/IP 类型）、Type（用途与公司
类型 per 数据库）、Score（各库评分，保留原始字符串）、Factor（国家代码与代理/Tor/VPN/
机房/滥用/爬虫因子 per 数据库）、Media（流媒体与 AI 解锁状态/地区/类型）、Mail（25 端口
与邮局连通性、DNSBL 汇总）。`"null"` 字符串规范化为空值；未知服务或新增字段不丢失，报告
同时携带规范化后的原始 JSON 副本。

单栈主机只输出一个家族文档，缺失家族在报告中不出现；无任何公网地址时分类状态为
`unavailable`。最终报告经 `server-test.result` 分片协议回传 Panel，由前端报告表渲染。
```

- [ ] **Step 3: 检查其它引用旧实现的段落**

Run: `rg -n "ipinfo.check.place|Team Cymru|cdn-cgi/trace|provider|粗粒度" docs/server-testing-design.md`

将第 185 行（§7 进度语义）中：
```markdown
国际与测速按目标完成数更新；IP 质量只能按 IPv4/IPv6 家族粗粒度更新。
```
替换为：
```markdown
国际与测速按目标完成数更新；IP 质量按脚本阶段（下载脚本、检查依赖、运行检测、解析结果）粗粒度更新。
```

其余残留如 `ipinfo.check.place`、`cdn-cgi/trace` 等旧数据源描述应已随 §4 重写消失；若仍有残留，逐处删除或改为脚本化描述。

- [ ] **Step 4: Commit**

```bash
git add docs/server-testing-design.md
git commit -m "docs: rewrite IP quality section for script-based execution"
```

---

### Task 8: 全量验证与收尾提交

- [ ] **Step 1: Go 全量构建与测试**

Run: `go build ./... && go vet ./... && go test ./...`（`src/shared`）
Run: `go build ./... && go vet ./... && go test ./...`（`src/agent`）
Expected: 全部通过

- [ ] **Step 2: 前端构建与 lint**

Run: `bun run build && bun run lint`（`src/frontend`）
Expected: 通过

- [ ] **Step 3: 检查未引用残留**

Run: `rg -n "runIPQuality\b|ip_providers|runDNSBL|runASNEnrichment|dnsbl_snapshot" src/`（仓库根目录）
Expected: 无匹配（除本计划文档外）

- [ ] **Step 4: 状态确认与提交**

Run: `git status --porcelain` 与 `git log --oneline -12`
Expected: 工作区无未提交改动；提交历史包含本计划的 7 个功能提交

```bash
git add -A && git commit -m "chore: finalize script-based IP quality redesign" --allow-empty
```

（若 Step 3 发现残留，先修复并单独提交后再执行本步。）
