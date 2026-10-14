package federation

import "testing"

func TestCapDomainIsRegistrableDomain(t *testing.T) {
	for in, want := range map[string]string{
		"https://sub7.evil.example/accounts/x": "evil.example",
		"https://Peer.Tube.co.uk:8443/a":       "tube.co.uk",
		"https://evil.example./a":              "evil.example",
		"https://10.0.0.5:9000/a":              "10.0.0.5",
		"https://localhost/a":                  "localhost",
	} {
		if got := capDomain(in); got != want {
			t.Errorf("capDomain(%q) = %q, want %q", in, got, want)
		}
	}
}
