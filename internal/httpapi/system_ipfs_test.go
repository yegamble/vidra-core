package httpapi

import (
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

// stubIPFSHealth stages one probe verdict, the way the five-minute probe loop
// would have left it.
type stubIPFSHealth struct {
	public  ipfsmirror.Health
	private ipfsmirror.Health
}

func (s stubIPFSHealth) GatewayHealth() ipfsmirror.Health { return s.public }
func (s stubIPFSHealth) PrivateHealth() ipfsmirror.Health { return s.private }

func okHealth() ipfsmirror.Health {
	return ipfsmirror.Health{State: ipfsmirror.HealthOK, Probed: true, Pinned: 3, Strays: 0}
}

func downHealth() ipfsmirror.Health {
	return ipfsmirror.Health{
		State: ipfsmirror.HealthDown, Probed: true, Pinned: 3, Strays: -1,
		Reason: "the IPFS gateway did not serve a CID this instance publishes",
	}
}

// THE GATE. Same server, same pinned object, two gateway verdicts: a 307 while
// the probe says the gateway serves, and the authoritative bytes while it does
// not. A31 measured only the first behaviour, unconditionally.
func TestThumbnailRedirectsOnlyWhileTheGatewayIsHealthy(t *testing.T) {
	mirror := &fakeIPFSMirror{assetURLs: map[string]string{}}
	health := &stubIPFSHealth{public: okHealth()}
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSGatewayURL = "https://ipfs.example.org"
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{
		WithIPFSMirrorService(mirror), WithIPFSHealth(health),
	})

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Poster","privacy":"public"}`)
	if rec := uploadThumbnail(srv, id, "poster.png", "image/png", "poster-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("thumbnail upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "video-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("publish upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	mirror.assetURLs[fakeAssetLookupKey("thumbnails/"+id+".jpg", "thumbnail")] = testPublicAssetURL

	rec := getThumbnail(srv, id, "")
	assertIPFSRedirect(t, rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))

	// The gateway dies. The next request must be served canonically rather than
	// handed a 307 that will fail — and be cached failing for max-age=300.
	health.public = downHealth()
	rec = getThumbnail(srv, id, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "poster-bytes" {
		t.Fatalf("with the gateway down: %d/%q, want 200 and the authoritative bytes", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("a redirect was still minted to %q", loc)
	}

	// And it comes back on its own when the probe does.
	health.public = okHealth()
	rec = getThumbnail(srv, id, "")
	assertIPFSRedirect(t, rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))
}

// The verdicts, in the vocabulary /admin/system speaks.
func TestIPFSComponentVerdicts(t *testing.T) {
	tests := []struct {
		name      string
		health    ipfsmirror.Health
		settingOn bool
		want      string
	}{
		{"serving", okHealth(), true, "ok"},
		{"gateway down", downHealth(), true, "down"},
		{
			"admin switched delivery off", okHealth(), false, "not_configured",
		},
		{
			"mirror off",
			ipfsmirror.Health{
				State: ipfsmirror.HealthNotConfigured, Strays: -1,
				Reason: "the public IPFS mirror is off (IPFS_ENABLED=false)",
			},
			true, "not_configured",
		},
		{
			"dead letters behind a healthy gateway",
			ipfsmirror.Health{State: ipfsmirror.HealthOK, Probed: true, DeadLettered: 4, Strays: -1},
			true, "degraded",
		},
		{
			"nothing is draining the queue",
			ipfsmirror.Health{State: ipfsmirror.HealthOK, Probed: true, Backlog: 9, OldestBacklogAgeSeconds: ipfsStallSeconds + 1, Strays: -1},
			true, "down",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ipfsComponent(tt.health, tt.settingOn)
			if got.Status != tt.want {
				t.Fatalf("status = %q, want %q (error=%q)", got.Status, tt.want, got.Error)
			}
			if got.Status != "ok" && got.Error == "" {
				t.Error("a non-ok component must carry the sentence an operator acts on")
			}
			// The facts behind the verdict ride on every arm, including the failing
			// ones — the federation-health pattern.
			if got.Detail["dead_lettered"] == "" || got.Detail["backlog"] == "" {
				t.Errorf("detail = %v, want the ledger facts on every arm", got.Detail)
			}
		})
	}
}

// -1 strays means "no comparison has completed", which must not be rendered as
// zero: reporting 0 would claim the node holds nothing unaccounted for, which is
// the claim A31 caught the old reconcile making by silence.
func TestIPFSDetailDistinguishesUnknownStraysFromNone(t *testing.T) {
	unknown := ipfsDetail(ipfsmirror.Health{State: ipfsmirror.HealthOK, Probed: true, Strays: -1})
	if _, present := unknown["unaccounted_node_pins"]; present {
		t.Error("an uncompleted comparison must be absent, not 0")
	}
	none := ipfsDetail(ipfsmirror.Health{State: ipfsmirror.HealthOK, Probed: true, Strays: 0})
	if none["unaccounted_node_pins"] != "0" {
		t.Errorf("a completed comparison with nothing found must say 0, got %q", none["unaccounted_node_pins"])
	}
	// No pin has ever landed: an absent last_pinned_at rather than an epoch that
	// looks like a bug.
	if _, present := none["last_pinned_at"]; present {
		t.Error("last_pinned_at must be absent when nothing has ever been pinned")
	}
}

// An instance with no mirror wired renders no component at all: "ok" for a
// feature the instance does not run is a claim.
func TestNoIPFSComponentWithoutAMirror(t *testing.T) {
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), nil)
	if _, ok := srv.ipfsStatus(); ok {
		t.Error("an install with no mirror rendered an ipfs component")
	}
	if _, ok := srv.privateIPFSStatus(); ok {
		t.Error("an install with no private tier rendered a private_ipfs component")
	}
}
