package dispatch

import "testing"

func TestPreferredAgentAddress(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		nics   []string
		want   string
	}{
		{
			name:   "docker bridge falls back to public IPv4 NIC",
			remote: "192.168.32.1",
			nics:   []string{"10.0.0.2", "2602:fa4f:a30:264e:c176:474b:9f51:30ca", "156.246.95.109"},
			want:   "156.246.95.109",
		},
		{
			name:   "public socket peer wins",
			remote: "203.0.113.9",
			nics:   []string{"156.246.95.109"},
			want:   "203.0.113.9",
		},
		{
			name:   "public IPv6 is used when IPv4 is unavailable",
			remote: "172.18.0.1",
			nics:   []string{"fe80::1", "2602:fa4f:a30:264e:c176:474b:9f51:30ca"},
			want:   "2602:fa4f:a30:264e:c176:474b:9f51:30ca",
		},
		{
			name:   "private peer remains when no public candidate exists",
			remote: "192.168.32.1",
			nics:   []string{"10.0.0.2", "fe80::1", "100.64.0.8"},
			want:   "192.168.32.1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := preferredAgentAddress(tt.remote, tt.nics); got != tt.want {
				t.Fatalf("preferredAgentAddress(%q, %v) = %q, want %q", tt.remote, tt.nics, got, tt.want)
			}
		})
	}
}
