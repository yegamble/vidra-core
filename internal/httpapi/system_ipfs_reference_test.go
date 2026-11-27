package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/config"
)

func TestSystemStatusReportsReferencePinsWithoutEnablingTheMirror(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	t.Setenv("IPFS_REFERENCE_STATUS_FILE", path)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"version":1,"state":"ok","reason":"verified","pinned":1,"observed_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	server := ipfsServer(t, cfg)
	components, _ := server.systemComponents(t.Context())
	got := components["ipfs_reference"]
	if got.Status != "ok" || got.Detail["pinned"] != "1" || got.Detail["probed_at"] == "" || cfg.IPFSEnabled {
		t.Fatalf("unexpected reference pin verdict: %+v", got)
	}
	if ready, _ := server.componentHealth(t.Context()); ready["ipfs_reference"].Status != "" {
		t.Fatal("reference observations must not influence the public readiness or media path")
	}
	plain := ipfsServer(t, testConfig())
	if components, _ := plain.systemComponents(t.Context()); components["ipfs_reference"].Status != "" {
		t.Fatal("unconfigured reference probe appeared on a stock instance")
	}
}

func TestSystemReferencePinObservationFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	t.Setenv("IPFS_REFERENCE_STATUS_FILE", path)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	srv := ipfsServer(t, cfg)
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, state, reason string
		age                 time.Duration
		count               any
		want, pinned        string
	}{
		{"known empty", "ok", "verified", 0, 0, "ok", "0"},
		{"unknown count", "down", "node_unavailable", 0, nil, "down", ""},
		{"approval unreadable", "down", "approval_unavailable", 0, nil, "down", ""},
		{"unexpected pinned set", "degraded", "pin_set_mismatch", 0, 1, "degraded", "1"},
		{"unverified bytes", "degraded", "media_unverified", 0, nil, "degraded", ""},
		{"stale", "ok", "verified", 6 * time.Minute, 1, "degraded", ""},
		{"future", "ok", "verified", -time.Minute, 1, "degraded", ""},
		{"missing count", "ok", "verified", 0, nil, "down", ""},
		{"invalid count", "ok", "verified", 0, -1, "down", ""},
		{"wrong state", "ok", "pin_set_mismatch", 0, 2, "down", ""},
		{"unknown reason", "down", "private-value", 0, nil, "down", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(map[string]any{"version": 1, "observed_at": now.Add(-tc.age), "state": tc.state, "reason": tc.reason, "pinned": tc.count})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			all, healthy := srv.systemComponents(t.Context())
			got := all["ipfs_reference"]
			if got.Status != tc.want || got.Detail["pinned"] != tc.pinned || (got.Status != "ok" && (healthy || got.Error == "")) || strings.Contains(got.Error, "private-value") {
				t.Fatalf("unexpected status: %+v", got)
			}
		})
	}
	for _, data := range []string{"{", strings.Repeat(" ", 4097), `{"version":2}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		all, _ := srv.systemComponents(t.Context())
		if got := all["ipfs_reference"]; got.Status != "down" || got.Detail["pinned"] != "" {
			t.Fatalf("invalid file: %+v", got)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	all, _ := srv.systemComponents(t.Context())
	if got := all["ipfs_reference"]; got.Status != "down" || got.Detail["pinned"] != "" || strings.Contains(got.Error, path) {
		t.Fatalf("missing file: %+v", got)
	}
}
