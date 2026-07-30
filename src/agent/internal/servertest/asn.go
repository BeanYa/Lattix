package servertest

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const asnEnrichmentTimeout = 35 * time.Second

type txtLookup func(context.Context, string) ([]string, error)

func runASNEnrichment(parent context.Context, address netip.Addr) map[string]any {
	return runASNEnrichmentWithLookup(parent, address, net.DefaultResolver.LookupTXT)
}

func runASNEnrichmentWithLookup(parent context.Context, address netip.Addr, lookup txtLookup) map[string]any {
	ctx, cancel := context.WithTimeout(parent, asnEnrichmentTimeout)
	defer cancel()
	originRecords, err := lookup(ctx, cymruOriginQuery(address))
	if err != nil {
		return map[string]any{"status": "unavailable", "error_code": "asn_lookup_failed", "error_message": safeDNSError(err)}
	}
	origin, ok := parseCymruOrigin(originRecords)
	if !ok {
		return map[string]any{"status": "unavailable", "error_code": "asn_response_unrecognized", "error_message": "Team Cymru returned no parseable origin record"}
	}
	result := map[string]any{
		"status": "available", "asn": origin.asn, "prefix": origin.prefix,
		"country_code": origin.countryCode, "registry": origin.registry, "allocated_at": origin.allocatedAt,
	}
	nameRecords, nameErr := lookup(ctx, fmt.Sprintf("AS%d.asn.cymru.com", origin.asn))
	if nameErr != nil {
		result["name_status"] = "unavailable"
		result["name_error"] = safeDNSError(nameErr)
		return result
	}
	if name, ok := parseCymruName(nameRecords); ok {
		result["name"] = name
		result["name_status"] = "available"
	} else {
		result["name_status"] = "unavailable"
		result["name_error"] = "Team Cymru returned no parseable AS name record"
	}
	return result
}

func cymruOriginQuery(address netip.Addr) string {
	address = address.Unmap()
	if address.Is4() {
		octets := address.As4()
		return strings.Join([]string{
			strconv.Itoa(int(octets[3])), strconv.Itoa(int(octets[2])),
			strconv.Itoa(int(octets[1])), strconv.Itoa(int(octets[0])), "origin.asn.cymru.com",
		}, ".")
	}
	hex := strings.ReplaceAll(address.StringExpanded(), ":", "")
	reversed := make([]byte, 0, len(hex)*2)
	for index := len(hex) - 1; index >= 0; index-- {
		reversed = append(reversed, hex[index], '.')
	}
	return string(reversed) + "origin6.asn.cymru.com"
}

type cymruOrigin struct {
	asn         int64
	prefix      string
	countryCode string
	registry    string
	allocatedAt string
}

func parseCymruOrigin(records []string) (cymruOrigin, bool) {
	for _, record := range records {
		fields := splitCymruRecord(record)
		if len(fields) < 5 || strings.EqualFold(fields[0], "AS") {
			continue
		}
		asnField := strings.Fields(fields[0])
		if len(asnField) == 0 {
			continue
		}
		asn, err := strconv.ParseInt(asnField[0], 10, 64)
		if err != nil || asn <= 0 {
			continue
		}
		return cymruOrigin{
			asn: asn, prefix: fields[1], countryCode: normalizedCountry(fields[2]),
			registry: strings.ToLower(fields[3]), allocatedAt: fields[4],
		}, true
	}
	return cymruOrigin{}, false
}

func parseCymruName(records []string) (string, bool) {
	for _, record := range records {
		fields := splitCymruRecord(record)
		if len(fields) < 5 || strings.EqualFold(fields[0], "AS") {
			continue
		}
		name := strings.TrimSpace(strings.Join(fields[4:], " | "))
		if name != "" {
			return name, true
		}
	}
	return "", false
}

func splitCymruRecord(record string) []string {
	record = strings.Trim(strings.TrimSpace(record), `"`)
	rawFields := strings.Split(record, "|")
	fields := make([]string, 0, len(rawFields))
	for _, field := range rawFields {
		fields = append(fields, strings.TrimSpace(field))
	}
	return fields
}
