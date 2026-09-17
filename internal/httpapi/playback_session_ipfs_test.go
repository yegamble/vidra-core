package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

type sessionIPFSMirror struct {
	fakeIPFSMirror
	url       string
	err       error
	calls     int
	masterKey string
	videoID   uuid.UUID
}

func (m *sessionIPFSMirror) PublicPlaybackHLS(ctx context.Context, id uuid.UUID, masterKey string) (string, bool, error) {
	m.calls++
	m.masterKey, m.videoID = masterKey, id
	if _, bounded := ctx.Deadline(); !bounded {
		panic("IPFS lookup must be bounded")
	}
	return m.url, m.url != "", m.err
}

func TestPlaybackSessionIPFSSourceSelection(t *testing.T) {
	for _, tc := range []struct {
		name, privacy                            string
		enabled, healthy, ready, pinned, failure bool
		wantIPFS                                 bool
	}{
		{"current public tree", "public", true, true, true, true, false, true},
		{"gateway down", "public", true, false, true, true, false, false},
		{"delivery disabled", "public", false, true, true, true, false, false},
		{"no completed pin", "public", true, true, true, false, false, false},
		{"ledger unavailable", "public", true, true, true, true, true, false},
		{"no ready tree", "public", true, true, false, true, false, false},
		{"private owner", "private", true, true, true, true, false, false},
		{"unlisted owner", "unlisted", true, true, true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newPlaybackSessionEnv(t)
			env.srv.cfg.IPFSEnabled = tc.enabled
			mirror := &sessionIPFSMirror{}
			if tc.pinned {
				mirror.url = "https://ipfs.example.test/ipfs/bafyfixture/v1-master.m3u8"
			}
			if tc.failure {
				mirror.err = errors.New("unavailable")
			}
			env.srv.ipfsmirrorsvc = mirror
			health := ipfsmirror.Health{Probed: true, State: ipfsmirror.HealthDown}
			if tc.healthy {
				health.State = ipfsmirror.HealthOK
			}
			env.srv.ipfsHealth = stubIPFSHealth{public: health}
			id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Clip","privacy":"`+tc.privacy+`"}`)
			if tc.ready {
				seedReadyPeerTubeHLS(t, env.tc, env.blobs, id)
			}
			rec := postPlaybackSession(env.srv, id, env.owner, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("session=%d %s", rec.Code, rec.Body.String())
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if (got["ipfs_hls_url"] != nil) != tc.wantIPFS {
				t.Fatalf("IPFS present=%v want=%v: %s", got["ipfs_hls_url"] != nil, tc.wantIPFS, rec.Body.String())
			}
			if tc.ready && (got["authoritative_hls_url"] == nil || string(got["authoritative_hls_url"]) != string(got["hls_url"])) {
				t.Fatalf("missing authoritative fallback: %s", rec.Body.String())
			}
			if !tc.ready && got["authoritative_hls_url"] != nil {
				t.Fatal("unready tree advertised")
			}
			if tc.wantIPFS {
				if mirror.videoID != uuid.MustParse(id) || mirror.masterKey != env.tc.playlists[uuid.MustParse(id)].MasterKey {
					t.Fatal("generation or imported master filename lost")
				}
				if string(got["ipfs_hls_url"]) != `"`+mirror.url+`"` {
					t.Fatal("wrong gateway URL")
				}
			}
			if (!tc.enabled || !tc.healthy || !tc.ready || tc.privacy != "public") && mirror.calls != 0 {
				t.Fatal("ineligible session consulted IPFS")
			}
		})
	}
}

func TestPlaybackSessionIPFSRequiresFreshGateAndNoDRM(t *testing.T) {
	env := newDRMEnv(t)
	env.srv.cfg.IPFSEnabled = true
	mirror := &sessionIPFSMirror{url: "https://ipfs.example.test/ipfs/bafyfixture/master.m3u8"}
	env.srv.ipfsmirrorsvc = mirror
	id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Clip","privacy":"public"}`)
	seedReadyHLS(t, env.tc, env.blobs, id)
	if got := playbackSession(t, env.srv, id); got.IPFSHLSURL != "" {
		t.Fatal("unobserved health advertised")
	}
	env.srv.ipfsHealth = stubIPFSHealth{public: okHealth()}
	settings := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(env.srv.cfg))
	if err := settings.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	env.srv.settingssvc = settings
	set := func(value string) {
		t.Helper()
		if err := settings.Apply(context.Background(), map[string]instancesettings.Update{instancesettings.KeyDeliveryIPFSEnabled: {Value: value}}, uuid.Nil); err != nil {
			t.Fatal(err)
		}
	}
	set("false")
	if got := playbackSession(t, env.srv, id); got.IPFSHLSURL != "" {
		t.Fatal("delivery kill switch ignored")
	}
	set("true")
	if got := playbackSession(t, env.srv, id); got.IPFSHLSURL == "" {
		t.Fatal("delivery did not resume")
	}
	env.protect(t, id)
	if got := playbackSession(t, env.srv, id); got.IPFSHLSURL != "" || got.DRM == nil {
		t.Fatal("protected tree advertised via public IPFS")
	}
}
