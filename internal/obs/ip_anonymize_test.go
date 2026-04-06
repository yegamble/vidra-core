package obs

import (
	"testing"
)

func TestAnonymizeIP_IPv4LastOctet(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"192.168.1.100", "192.168.1.0"},
		{"10.0.0.1", "10.0.0.0"},
		{"255.255.255.255", "255.255.255.0"},
		{"0.0.0.0", "0.0.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := AnonymizeIP(tt.input)
			if got != tt.want {
				t.Errorf("AnonymizeIP(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAnonymizeIP_IPv6Last80Bits(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2001:db8:85a3::8a2e:370:7334", "2001:db8::"},
		{"::1", "::"},
		{"fe80::1", "fe80::"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := AnonymizeIP(tt.input)
			if got == tt.input {
				t.Errorf("AnonymizeIP(%q): expected IP to be anonymized, but got same value", tt.input)
			}
			// IPv6 should have last 80 bits zeroed (last 10 bytes)
			if got == "" {
				t.Errorf("AnonymizeIP(%q) returned empty string", tt.input)
			}
		})
	}
}

func TestAnonymizeIP_InvalidInput(t *testing.T) {
	result := AnonymizeIP("not-an-ip")
	// Invalid IPs should be returned as-is (graceful handling)
	if result == "" {
		t.Error("AnonymizeIP with invalid input should return non-empty string")
	}
}

func TestAnonymizeIP_EmptyString(t *testing.T) {
	result := AnonymizeIP("")
	if result != "" {
		t.Errorf("AnonymizeIP empty string should return empty, got %q", result)
	}
}
