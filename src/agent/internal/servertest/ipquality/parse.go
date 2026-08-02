// Package ipquality runs the upstream xykt/IPQuality script for Lattix agent
// server tests and maps its JSON output into Lattix-native report structures.
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
	if value, ok := fields["Port25"]; ok && !isNullJSON(value) {
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
		if name == "Port25" || name == "DNSBlacklist" || isNullJSON(value) {
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

func isNullJSON(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
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
