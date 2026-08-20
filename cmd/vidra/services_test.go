package main

import (
	"strings"
	"testing"
)

// The name map is the CLI's vocabulary, and it is shared by logs, restart and
// status on purpose. Every entry here is a name an operator might reasonably
// type; the compose names are accepted too, because pasting what `docker compose
// ps` printed has to work.
func TestComposeServiceNames(t *testing.T) {
	for _, tc := range []struct{ typed, want string }{
		{"api", "api"},
		{"backend", "api"},
		{"frontend", "frontend"},
		{"web", "frontend"},
		{"ui", "frontend"},
		{"search", "search"},
		{"caddy", "caddy"},
		{"proxy", "caddy"},
		{"edge", "caddy"},
		{"postgres", "postgres"},
		{"db", "postgres"},
		{"database", "postgres"},
		{"redis", "redis"},
		{"cache", "redis"},
		{"clamav", "clamav"},
		{"scan", "clamav"},
		{"antivirus", "clamav"},
		{"whisper", "whisper"},
		{"captions", "whisper"},
		{"rtmp", "rtmp"},
		{"live", "rtmp"},
		{"otel", "otel-collector"},
		{"tracing", "otel-collector"},
		{"otel-collector", "otel-collector"},
		{"jaeger", "jaeger"},
		{"ipfs", "ipfs"},
		// The one-shots are known names, so restart can say what they are
		// instead of "no such service".
		{"migrate", "migrate"},
		{"search-migrate", "search-migrate"},
		{"prep-volumes", "prep-volumes"},
		// Typed the way a human types it.
		{"  API  ", "api"},
		{"Postgres", "postgres"},
	} {
		got, ok := composeService(tc.typed)
		if !ok || got != tc.want {
			t.Errorf("composeService(%q) = %q, %v; want %q, true", tc.typed, got, ok, tc.want)
		}
	}

	for _, unknown := range []string{"", "nginx", "backedn", "postgresql", "api-server"} {
		if got, ok := composeService(unknown); ok {
			t.Errorf("composeService(%q) = %q, true; want it unknown", unknown, got)
		}
	}
}

// The list an unknown name is answered with has to be worth printing: sorted,
// and containing the names an operator was most likely reaching for.
func TestServiceNamesIsASortedList(t *testing.T) {
	names := serviceNames()
	if len(names) != len(serviceAliases) {
		t.Fatalf("serviceNames() has %d entries, the map has %d", len(names), len(serviceAliases))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("serviceNames() is not sorted at %d: %q before %q", i, names[i-1], names[i])
		}
	}
	joined := strings.Join(names, ", ")
	for _, want := range []string{"api", "frontend", "postgres", "caddy"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the list does not offer %q: %s", want, joined)
		}
	}
}

func TestEnabledInProfiles(t *testing.T) {
	stock := []string{"core", "frontend"}
	for _, tc := range []struct {
		service  string
		profiles []string
		want     bool
	}{
		{"api", stock, true},
		{"frontend", stock, true},
		{"postgres", stock, true},
		{"frontend", []string{"core"}, false},
		{"rtmp", stock, false},
		{"rtmp", []string{"core", "frontend", "media"}, true},
		// Two profiles, either of which enables it.
		{"ipfs", []string{"core", "ipfs"}, true},
		{"ipfs", []string{"core", "full"}, true},
		{"ipfs", stock, false},
		// caddy carries no profile: the TLS edge comes up with every production
		// deploy, so it is enabled even in a deployment that lists nothing.
		{"caddy", nil, true},
		{"caddy", stock, true},
	} {
		if got := enabledIn(tc.service, tc.profiles); got != tc.want {
			t.Errorf("enabledIn(%q, %v) = %v, want %v", tc.service, tc.profiles, got, tc.want)
		}
	}
}
