package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestReferencePlaybackSessionSources(t *testing.T) {
	env := newPlaybackSessionEnv(t)
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
