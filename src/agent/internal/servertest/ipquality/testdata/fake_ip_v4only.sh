#!/bin/bash
# Fake ip.sh for unit tests: prints only the IPv4 family document, then exits 1
# the way upstream ip.sh does on hosts without public IPv6 (e.g. a NAT-tier
# machine), where its trailing IPv6 gate `[[ ... ]] && check_IP ...` is false
# and the compound's status becomes the process exit code.
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
EOF
exit 1
