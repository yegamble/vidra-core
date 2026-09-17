package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

type managedHealthControl struct {
	fakeIPFSControl
	runtime    ipfscontrol.Runtime
	runtimeErr error
	reads      int
}

func (f *managedHealthControl) Runtime(context.Context) (ipfscontrol.Runtime, error) {
	f.reads++
	return f.runtime, f.runtimeErr
}

func TestManagedIPFSSystemHealthDistinguishesPauseFromMissingConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*managedHealthControl, *stubIPFSHealth)
		want   string
	}{
		{"paused healthy node without published content", nil, "paused"},
		{"awaiting gateway proof", func(f *managedHealthControl, _ *stubIPFSHealth) { f.doc.Config.Enabled = true }, "pending"},
		{"serving", func(f *managedHealthControl, h *stubIPFSHealth) { f.doc.Config.Enabled = true; h.public = okHealth() }, "ok"},
		{"paused node unavailable", func(f *managedHealthControl, _ *stubIPFSHealth) { f.runtime.Management.Available = false }, "down"},
		{"paused node stopped", func(f *managedHealthControl, _ *stubIPFSHealth) { f.runtime.Management.ObservedState = "stopped" }, "down"},
		{"stale node observation", func(f *managedHealthControl, _ *stubIPFSHealth) {
			old := time.Now().Add(-time.Minute)
			f.runtime.Management.ObservedAt = &old
		}, "down"},
		{"configuration pending", func(f *managedHealthControl, _ *stubIPFSHealth) { f.runtime.Management.AppliedConfigRevision = 1 }, "pending"},
		{"gateway failure remains visible during pause", func(_ *managedHealthControl, h *stubIPFSHealth) { h.public = downHealth() }, "down"},
		{"observation failure", func(f *managedHealthControl, _ *stubIPFSHealth) { f.runtimeErr = errors.New("private diagnostic") }, "down"},
		{"unadopted defaults", func(f *managedHealthControl, _ *stubIPFSHealth) { f.doc.PolicyActive = false }, "not_configured"},
		{"external provider retains gateway verdict", func(f *managedHealthControl, _ *stubIPFSHealth) { f.doc.Config.Provider = "external" }, "not_configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			f := &managedHealthControl{
				fakeIPFSControl: fakeIPFSControl{doc: ipfscontrol.Document{Revision: 2, PolicyActive: true, Config: ipfscontrol.Config{Provider: "internal"}}},
				runtime:         ipfscontrol.Runtime{ConfigRevision: 2, Management: ipfscontrol.Management{Available: true, ObservedState: "running", AppliedConfigRevision: 2, ObservedAt: &now}},
			}
			h := &stubIPFSHealth{public: ipfsmirror.Health{State: ipfsmirror.HealthNotConfigured, Reason: "no published content"}}
			if tc.change != nil {
				tc.change(f, h)
			}
			cfg := testConfig()
			cfg.IPFSEnabled = true
			srv := ipfsServer(t, cfg, WithIPFSControl(f), WithIPFSHealth(h))
			_, _ = srv.componentHealth(context.Background())
			if f.reads != 0 {
				t.Fatal("readiness performed a manager round trip")
			}
			components, _ := srv.systemComponents(context.Background())
			got := components["ipfs"]
			if got.Status != tc.want {
				t.Fatalf("status=%q want=%q: %s", got.Status, tc.want, got.Error)
			}
			if strings.Contains(got.Error, "private diagnostic") {
				t.Fatal("raw manager error leaked")
			}
			if tc.want == "paused" && (got.Detail["node_state"] != "running" || got.Detail["publication"] != "paused" || got.Detail["gateway_status"] != "not_configured") {
				t.Fatalf("missing separate node/publication/gateway facts: %v", got.Detail)
			}
			if f.writes != 0 {
				t.Fatal("health check changed publication policy")
			}
		})
	}
}
