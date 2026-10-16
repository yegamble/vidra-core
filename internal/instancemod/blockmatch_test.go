package instancemod

import (
	"context"
	"testing"
)

func TestBlockMatchesEquivalentForms(t *testing.T) {
	lookup := func(list ...string) func(context.Context, string) (bool, error) {
		return func(_ context.Context, d string) (bool, error) {
			for _, e := range list {
				if e == d {
					return true, nil
				}
			}
			return false, nil
		}
	}
	for _, c := range []struct{ entry, probe string }{
		{"bücher.example", "xn--bcher-kva.example"},
		{"xn--bcher-kva.example", "bücher.example"},
		{"bücher.example", "BÜCHER.example."},
		{"plain.example", "Plain.Example"},
	} {
		if ok, _ := BlockMatches(context.Background(), lookup(c.entry), c.probe); !ok {
			t.Errorf("entry %q must block probe %q", c.entry, c.probe)
		}
	}
	if ok, _ := BlockMatches(context.Background(), lookup("bücher.example"), "other.example"); ok {
		t.Error("unrelated host blocked")
	}
}

func TestBlockMatchesPortProbesStoredPortEntries(t *testing.T) {
	exact := func(list ...string) func(context.Context, string) (bool, error) {
		return func(_ context.Context, d string) (bool, error) {
			for _, e := range list {
				if e == d {
					return true, nil
				}
			}
			return false, nil
		}
	}
	for _, c := range []struct{ entry, host, port string }{
		{"blocked.example:8443", "Blocked.Example.", "8443"},
		{"blocked.example:443", "blocked.example", EffectivePort("https", "")},
		{"plain.example:80", "plain.example", EffectivePort("HTTP", "")},
		{"xn--bcher-kva.example:8443", "bücher.example", "8443"},
	} {
		if ok, _ := BlockMatchesPort(context.Background(), exact(c.entry), c.host, c.port); !ok {
			t.Errorf("entry %q must block %s:%s", c.entry, c.host, c.port)
		}
	}
	if ok, _ := BlockMatchesPort(context.Background(), exact("blocked.example:443"), "blocked.example", ""); ok {
		t.Error("no port: a host:port entry must not match by exact lookup")
	}
	if got := EffectivePort("ftp", ""); got != "" {
		t.Errorf("ftp default port = %q, want empty", got)
	}
	if got := EffectivePort("https", "8443"); got != "8443" {
		t.Errorf("explicit port lost: %q", got)
	}
}
