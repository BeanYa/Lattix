package shared

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	ServerTestSchemaVersion = 1

	TypeServerTestRun      = "server-test.run"
	TypeServerTestProgress = "server-test.progress"
	TypeServerTestResult   = "server-test.result"
)

type ServerTestCategory string

const (
	ServerTestIPQuality       ServerTestCategory = "ip_quality"
	ServerTestTCPIPv4         ServerTestCategory = "tcp_ipv4"
	ServerTestTCPIPv6         ServerTestCategory = "tcp_ipv6"
	ServerTestLargePacketIPv4 ServerTestCategory = "large_packet_ipv4"
	ServerTestCERNETIPv4      ServerTestCategory = "cernet_ipv4"
	ServerTestCERNET2IPv6     ServerTestCategory = "cernet2_ipv6"
	ServerTestReturnRouteIPv4 ServerTestCategory = "return_route_ipv4"
	ServerTestReturnRouteIPv6 ServerTestCategory = "return_route_ipv6"
	ServerTestInternational   ServerTestCategory = "international"
	ServerTestSpeed           ServerTestCategory = "speed"
)

var serverTestCategoryOrder = []ServerTestCategory{
	ServerTestIPQuality,
	ServerTestTCPIPv4,
	ServerTestTCPIPv6,
	ServerTestLargePacketIPv4,
	ServerTestCERNETIPv4,
	ServerTestCERNET2IPv6,
	ServerTestReturnRouteIPv4,
	ServerTestReturnRouteIPv6,
	ServerTestInternational,
	ServerTestSpeed,
}

func ServerTestCategories() []ServerTestCategory {
	return append([]ServerTestCategory(nil), serverTestCategoryOrder...)
}

func (c ServerTestCategory) Valid() bool {
	for _, candidate := range serverTestCategoryOrder {
		if c == candidate {
			return true
		}
	}
	return false
}

type ServerTestTaskStatus string

const (
	ServerTestQueued              ServerTestTaskStatus = "queued"
	ServerTestAccepted            ServerTestTaskStatus = "accepted"
	ServerTestRunning             ServerTestTaskStatus = "running"
	ServerTestSucceeded           ServerTestTaskStatus = "succeeded"
	ServerTestCompletedWithErrors ServerTestTaskStatus = "completed_with_errors"
	ServerTestFailed              ServerTestTaskStatus = "failed"
)

func (s ServerTestTaskStatus) Terminal() bool {
	return s == ServerTestSucceeded || s == ServerTestCompletedWithErrors || s == ServerTestFailed
}

func (s ServerTestTaskStatus) Valid() bool {
	switch s {
	case ServerTestQueued, ServerTestAccepted, ServerTestRunning,
		ServerTestSucceeded, ServerTestCompletedWithErrors, ServerTestFailed:
		return true
	default:
		return false
	}
}

type ServerTestAddressFamily string

const (
	ServerTestIPv4      ServerTestAddressFamily = "ipv4"
	ServerTestIPv6      ServerTestAddressFamily = "ipv6"
	ServerTestDualStack ServerTestAddressFamily = "dualstack"
)

type ServerTestTarget struct {
	ID            string                  `json:"id"`
	Category      ServerTestCategory      `json:"category"`
	Label         string                  `json:"label"`
	Carrier       string                  `json:"carrier,omitempty"`
	Province      string                  `json:"province,omitempty"`
	AddressFamily ServerTestAddressFamily `json:"address_family"`
	Host          string                  `json:"host"`
	Port          int                     `json:"port"`
	SNI           string                  `json:"sni,omitempty"`
	Path          string                  `json:"path,omitempty"`
	UploadPath    string                  `json:"upload_path,omitempty"`
	Backup        *ServerTestTarget       `json:"backup,omitempty"`
	Source        string                  `json:"source"`
}

type ServerTestCatalogSnapshot struct {
	Version string             `json:"version"`
	Hashes  map[string]string  `json:"hashes"`
	Targets []ServerTestTarget `json:"targets"`
}

type ServerTestRunPayload struct {
	SchemaVersion int                       `json:"schema_version"`
	TaskID        string                    `json:"task_id"`
	Generation    int64                     `json:"generation"`
	Categories    []ServerTestCategory      `json:"categories"`
	Catalog       ServerTestCatalogSnapshot `json:"catalog"`
}

func (p ServerTestRunPayload) Validate() error {
	if p.SchemaVersion != ServerTestSchemaVersion {
		return fmt.Errorf("unsupported server test schema version %d", p.SchemaVersion)
	}
	if !ValidMessageID(p.TaskID) {
		return errors.New("invalid task_id")
	}
	if p.Generation < 1 {
		return errors.New("generation must be positive")
	}
	if len(p.Categories) == 0 || len(p.Categories) > len(serverTestCategoryOrder) {
		return errors.New("invalid category count")
	}
	seen := make(map[ServerTestCategory]struct{}, len(p.Categories))
	for _, category := range p.Categories {
		if !category.Valid() {
			return fmt.Errorf("invalid category %q", category)
		}
		if _, exists := seen[category]; exists {
			return fmt.Errorf("duplicate category %q", category)
		}
		seen[category] = struct{}{}
	}
	if strings.TrimSpace(p.Catalog.Version) == "" {
		return errors.New("catalog version is required")
	}
	for name, value := range p.Catalog.Hashes {
		decoded, err := hex.DecodeString(value)
		if strings.TrimSpace(name) == "" || err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("invalid catalog hash %q", name)
		}
	}
	return nil
}

type ServerTestProgressPayload struct {
	SchemaVersion int                          `json:"schema_version"`
	TaskID        string                       `json:"task_id"`
	Generation    int64                        `json:"generation"`
	Sequence      uint64                       `json:"sequence"`
	Status        ServerTestTaskStatus         `json:"status"`
	Phase         string                       `json:"phase"`
	Completed     int                          `json:"completed"`
	Total         int                          `json:"total"`
	Message       string                       `json:"message,omitempty"`
	Categories    []ServerTestCategoryProgress `json:"categories"`
}

type ServerTestCategoryProgress struct {
	Category  ServerTestCategory `json:"category"`
	Status    string             `json:"status"`
	Completed int                `json:"completed"`
	Total     int                `json:"total"`
	Message   string             `json:"message,omitempty"`
}

type ServerTestResultManifest struct {
	SchemaVersion    int                  `json:"schema_version"`
	TaskID           string               `json:"task_id"`
	Generation       int64                `json:"generation"`
	Status           ServerTestTaskStatus `json:"status"`
	AgentVersion     string               `json:"agent_version"`
	CatalogVersion   string               `json:"catalog_version"`
	UncompressedSize int                  `json:"uncompressed_size"`
	CompressedSize   int                  `json:"compressed_size"`
	SHA256           string               `json:"sha256"`
	ChunkCount       int                  `json:"chunk_count"`
	ErrorCode        string               `json:"error_code,omitempty"`
	ErrorMessage     string               `json:"error_message,omitempty"`
}

func (m ServerTestResultManifest) Validate() error {
	if m.SchemaVersion != ServerTestSchemaVersion || !ValidMessageID(m.TaskID) || m.Generation < 1 {
		return errors.New("invalid result identity")
	}
	if !m.Status.Terminal() {
		return errors.New("result status is not terminal")
	}
	if m.UncompressedSize < 0 || m.UncompressedSize > 8<<20 {
		return errors.New("uncompressed result exceeds limit")
	}
	if m.CompressedSize < 0 || m.ChunkCount < 1 || m.ChunkCount > 64 {
		return errors.New("invalid compressed result dimensions")
	}
	decoded, err := hex.DecodeString(m.SHA256)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid result sha256")
	}
	return nil
}

type ServerTestResultChunkPayload struct {
	Manifest   *ServerTestResultManifest `json:"manifest,omitempty"`
	TaskID     string                    `json:"task_id"`
	Generation int64                     `json:"generation"`
	Index      int                       `json:"index"`
	Data       []byte                    `json:"data"`
}

type ServerTestResultACK struct {
	Status string `json:"status"` // accepted|complete|superseded
}

type ServerTestReport struct {
	SchemaVersion  int                        `json:"schema_version"`
	TaskID         string                     `json:"task_id"`
	Generation     int64                      `json:"generation"`
	Status         ServerTestTaskStatus       `json:"status"`
	StartedAt      string                     `json:"started_at"`
	CompletedAt    string                     `json:"completed_at"`
	AgentVersion   string                     `json:"agent_version"`
	CatalogVersion string                     `json:"catalog_version"`
	Environment    ServerTestEnvironment      `json:"environment"`
	Categories     []ServerTestCategoryResult `json:"categories"`
	ErrorCode      string                     `json:"error_code,omitempty"`
	ErrorMessage   string                     `json:"error_message,omitempty"`
}

type ServerTestEnvironment struct {
	ProbeMethod    string `json:"probe_method"`
	Degraded       bool   `json:"degraded"`
	DegradedReason string `json:"degraded_reason,omitempty"`
	Sandbox        string `json:"sandbox"`
	SandboxReason  string `json:"sandbox_reason,omitempty"`
	Privileges     string `json:"privileges"`
	IPv6Available  *bool  `json:"ipv6_available,omitempty"`
}

type ServerTestCategoryResult struct {
	Category     ServerTestCategory `json:"category"`
	Status       string             `json:"status"`
	Summary      map[string]any     `json:"summary,omitempty"`
	Items        []map[string]any   `json:"items,omitempty"`
	IPQuality    *IPQualityResult   `json:"ip_quality,omitempty"`
	ErrorCode    string             `json:"error_code,omitempty"`
	ErrorMessage string             `json:"error_message,omitempty"`
}

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
