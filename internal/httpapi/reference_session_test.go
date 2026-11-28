package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func setReferenceDelivery(t *testing.T, srv *Server, value string) {
	t.Helper()
	if srv.settingssvc == nil {
		svc := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(srv.cfg))
		if err := svc.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		WithSettingsService(svc)(srv)
	}
	if err := srv.settingssvc.Apply(context.Background(), map[string]instancesettings.Update{instancesettings.KeyDeliveryIPFSEnabled: {Value: value}}, uuid.Nil); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceDeliverySwitch(t *testing.T) {
	env := newPlaybackSessionEnv(t)
	env.srv.cfg.IPFSEnabled = false
	env.srv.cfg.PublicBaseURL = "https://video.example"
	set := func(value string) { setReferenceDelivery(t, env.srv, value) }
	video := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Approved reference","privacy":"public"}`)
	id := uuid.MustParse(video)
	master := "streaming-playlists/" + video + "/r7/master.m3u8"
	env.tc.playlists[id] = sqlcgen.StreamingPlaylist{VideoID: id, MasterKey: master, State: "ready"}
	url := referencePath(id, "r7", "master.m3u8")
	const playlist = "#EXTM3U\n"
	calls := 0
	disableDuringRequest := false
	env.srv.referencePlayback = &referencePlayback{
		currentMaster: func(context.Context, uuid.UUID) (string, error) { return master, nil },
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			if disableDuringRequest {
				set("false")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(playlist))}, nil
		})},
	}
	check := func(method string, code int) {
		t.Helper()
		r := httptest.NewRecorder()
		env.srv.Handler().ServeHTTP(r, httptest.NewRequest(method, url, nil))
		if r.Code != code || r.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s returned %d, want %d with no-store", method, r.Code, code)
		}
		if strings.Contains(r.Body.String(), playlist) != (method == http.MethodGet && code == http.StatusOK) {
			t.Errorf("%s returned unexpected media bytes for status %d", method, r.Code)
		}
	}
	set("false")
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		check(method, http.StatusNotFound)
	}
	if session := playbackSession(t, env.srv, video); session.IPFSHLSURL != "" || session.AuthoritativeHLSURL == "" || calls != 0 {
		t.Error("disabled reference delivery reached the bridge or changed the session sources")
	}
	feature := featureNamed(t, infrastructureResponse{Features: env.srv.infraFeatures()}, "ipfs")
	if !feature.Configured || feature.Enabled || !strings.Contains(feature.Note, "delivery_ipfs_enabled") {
		t.Errorf("disabled wired reference feature = %+v", feature)
	}
	set("true")
	check(http.MethodGet, http.StatusOK)
	if session := playbackSession(t, env.srv, video); session.IPFSHLSURL != env.srv.webOrigin()+url {
		t.Error("enabling delivery did not restore the reference session")
	}
	feature = featureNamed(t, infrastructureResponse{Features: env.srv.infraFeatures()}, "ipfs")
	if !feature.Enabled || !feature.Configured || feature.Note != "" {
		t.Errorf("enabled wired reference feature = %+v", feature)
	}
	disableDuringRequest = true
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		set("true")
		check(method, http.StatusNotFound)
	}
	set("true")
	if session := playbackSession(t, env.srv, video); session.IPFSHLSURL != "" || session.AuthoritativeHLSURL == "" {
		t.Error("disabling delivery while the bridge was opening the file exposed the reference")
	}
}

func TestReferencePlaybackSessionSources(t *testing.T) {
	env := newPlaybackSessionEnv(t)
	setReferenceDelivery(t, env.srv, "true")
	video := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Approved source","privacy":"public"}`)
	id := uuid.MustParse(video)
	master := "streaming-playlists/" + video + "/r7/master.m3u8"
	env.tc.playlists[id] = sqlcgen.StreamingPlaylist{VideoID: id, MasterKey: master, State: "ready"}
	env.srv.cfg.PublicBaseURL = "https://video.example"
	eligible := master
	calls, status := 0, 200
	urlPath := "/api/v1/videos/" + video + "/ipfs-reference/r7/master.m3u8"
	env.srv.referencePlayback = &referencePlayback{
		currentMaster: func(context.Context, uuid.UUID) (string, error) { return eligible, nil },
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) > time.Second || r.Method != "HEAD" || r.URL.Path != urlPath {
				t.Fatal("invalid readiness probe")
			}
			return &http.Response{StatusCode: status, Body: http.NoBody}, nil
		})},
	}
	assert := func(want string, wantCalls int) {
		t.Helper()
		resp := playbackSession(t, env.srv, video)
		if resp.IPFSHLSURL != want || resp.AuthoritativeHLSURL == "" || calls != wantCalls {
			t.Fatalf("reference URL %q, authoritative %q, HEADs %d", resp.IPFSHLSURL, resp.AuthoritativeHLSURL, calls)
		}
	}
	assert("https://video.example"+urlPath, 1)
	status = 404
	assert("", 2)
	status = 503
	assert("", 3)
	eligible = "streaming-playlists/" + video + "/r8/master.m3u8"
	assert("", 3)
	// Operators can use both sources. An unavailable reference must leave an
	// independently healthy legacy mirror available to the same session.
	eligible = master
	fallback := "https://mirror.example/ipfs/legacy/master.m3u8"
	env.srv.cfg.IPFSEnabled = true
	env.srv.ipfsmirrorsvc = &sessionIPFSMirror{url: fallback}
	env.srv.ipfsHealth = stubIPFSHealth{public: ipfsmirror.Health{Probed: true, State: ipfsmirror.HealthOK}}
	assert(fallback, 4)

}
