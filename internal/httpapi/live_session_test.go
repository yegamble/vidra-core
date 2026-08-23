package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
)

// postLivePlaybackSession opens a live playback session, optionally carrying an
// account bearer token and/or a ?pt= playback token (the header-less path).
func postLivePlaybackSession(srv *Server, id, bearer, pt string) *httptest.ResponseRecorder {
	path := "/api/v1/live/" + id + "/playback-session"
	if pt != "" {
		path += "?pt=" + url.QueryEscape(pt)
	}
	return doJSON(srv, http.MethodPost, path, bearer, "")
}

// seedLiveStream creates a stream with the given privacy for channel "ada",
// writes the media server's output for it, and returns its id and stream key.
func seedLiveStream(t *testing.T, srv *Server, root, owner, privacy string) (string, string) {
	t.Helper()
	var created createLiveStreamResponse
	body := `{"title":"Show","privacy":"` + privacy + `"}`
	if err := json.Unmarshal(createLiveStream(srv, "ada", body, owner).Body.Bytes(), &created); err != nil {
		t.Fatalf("create live stream: %v", err)
	}
	id := created.LiveStream.ID
	if err := os.WriteFile(filepath.Join(root, id+".m3u8"),
		[]byte("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2.0,\n"+id+"-0.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, id+"-0.ts"), []byte("TSDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	return id, created.StreamKey
}

// goLive flips a seeded stream live through the ingest hook.
func goLive(t *testing.T, srv *Server, key string) {
	t.Helper()
	if r := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+key+`"}`, "s3cret"); r.Code != http.StatusOK {
		t.Fatalf("ingest start = %d", r.Code)
	}
}

// TestLivePlaybackSessionAuthorizationAndShape is the contract for phase-4
// item 7's session half.
//
// It pins the same two properties the VOD session table pins, for the same
// reasons. AUTHORIZATION must be indistinguishable from the live media routes' —
// the handler calls liveStreamForHLS rather than restating its four checks, so a
// private stream stays invisible and an offline one stays 404. And the TOKEN must
// stay CONDITIONAL: a session that minted one for every live viewer would mark
// every playlist and segment request credentialed, forcing no-store, and would do
// it silently.
func TestLivePlaybackSessionAuthorizationAndShape(t *testing.T) {
	cases := []struct {
		name string
		// setup builds the fixture and returns the stream id plus the credentials
		// the session request carries.
		setup      func(t *testing.T, srv *Server, root, owner, other string) (id, bearer, pt string)
		wantStatus int
		wantToken  bool
	}{
		{
			// The case delivery depends on: an ordinary public broadcast gets a
			// session with NO credential in it.
			name: "public stream, anonymous",
			setup: func(t *testing.T, srv *Server, root, owner, _ string) (string, string, string) {
				id, key := seedLiveStream(t, srv, root, owner, "public")
				goLive(t, srv, key)
				return id, "", ""
			},
			wantStatus: http.StatusOK,
		},
		{
			// Unlisted is reachable by anyone holding the id, so it needs no
			// credential either — handing one out would only turn delivery
			// origin-only.
			name: "unlisted stream, anonymous",
			setup: func(t *testing.T, srv *Server, root, owner, _ string) (string, string, string) {
				id, key := seedLiveStream(t, srv, root, owner, "unlisted")
				goLive(t, srv, key)
				return id, "", ""
			},
			wantStatus: http.StatusOK,
		},
		{
			// 404, not 401: live has no password tier, so there is no unlock
			// prompt to render and existence must not be leaked.
			name: "private stream, anonymous",
			setup: func(t *testing.T, srv *Server, root, owner, _ string) (string, string, string) {
				id, key := seedLiveStream(t, srv, root, owner, "private")
				goLive(t, srv, key)
				return id, "", ""
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "private stream, a different account",
			setup: func(t *testing.T, srv *Server, root, owner, other string) (string, string, string) {
				id, key := seedLiveStream(t, srv, root, owner, "private")
				goLive(t, srv, key)
				return id, other, ""
			},
			wantStatus: http.StatusNotFound,
		},
		{
			// The capability this endpoint exists for: the owner gets a credential
			// they can share, and that their own Safari can carry.
			name: "private stream, owner",
			setup: func(t *testing.T, srv *Server, root, owner, _ string) (string, string, string) {
				id, key := seedLiveStream(t, srv, root, owner, "private")
				goLive(t, srv, key)
				return id, owner, ""
			},
			wantStatus: http.StatusOK,
			wantToken:  true,
		},
		{
			// Renewal: a holder of a valid token gets a fresh one under a fresh
			// session. Renewal EXTENDS a grant — an absent or expired token fails
			// the gate and never reaches the mint.
			name: "private stream, valid ?pt=",
			setup: func(t *testing.T, srv *Server, root, owner, _ string) (string, string, string) {
				id, key := seedLiveStream(t, srv, root, owner, "private")
				goLive(t, srv, key)
				return id, "", liveSessionToken(t, srv, id, owner)
			},
			wantStatus: http.StatusOK,
			wantToken:  true,
		},
		{
			// Not live: 404 rather than a 200 advertising an hls_url that 404s.
			// Unlike a video with no transcode, there is no other way to play it.
			name: "public stream that is not live",
			setup: func(t *testing.T, srv *Server, root, owner, _ string) (string, string, string) {
				id, _ := seedLiveStream(t, srv, root, owner, "public")
				return id, "", ""
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, root := liveHLSServer(t)
			owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
			other := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
			id, bearer, pt := tc.setup(t, srv, root, owner, other)

			rec := postLivePlaybackSession(srv, id, bearer, pt)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}

			var got playbackSessionResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode session: %v; body=%s", err, rec.Body.String())
			}
			if _, err := uuid.Parse(got.SessionID); err != nil {
				t.Errorf("session_id = %q, want a UUID", got.SessionID)
			}
			if got.LiveStreamID != id {
				t.Errorf("live_stream_id = %q, want %q", got.LiveStreamID, id)
			}
			// A live session names a live stream. Echoing it as video_id would
			// send a client to video endpoints that 404 on this id.
			if got.VideoID != "" {
				t.Errorf("video_id = %q, want absent on a live session", got.VideoID)
			}
			if got.PackagingFormat != media.HLSFormatTS {
				t.Errorf("packaging_format = %q, want %q", got.PackagingFormat, media.HLSFormatTS)
			}
			if got.HLSURL != "/api/v1/live/"+id+"/hls/master.m3u8" {
				t.Errorf("hls_url = %q, want the live playlist path", got.HLSURL)
			}
			// No MPD and no ladder: the media server muxes one MPEG-TS bitrate.
			if got.DASHURL != "" || len(got.Renditions) != 0 {
				t.Errorf("dash_url/renditions = %q/%v, want both absent", got.DASHURL, got.Renditions)
			}
			// The manifest URL never carries the credential itself — the client
			// appends ?pt= when it has one, and the playlist rewrite carries it
			// from there.
			if strings.Contains(got.HLSURL, "pt=") {
				t.Errorf("hls_url must not embed a token: %q", got.HLSURL)
			}

			// The regression this endpoint must not introduce, asserted on the RAW
			// body so renaming the Go field cannot make it pass by accident: a
			// token for a stream that does not need one marks every subsequent
			// media request credentialed, which forces no-store on every playlist
			// and segment of a public broadcast.
			hasToken := strings.Contains(rec.Body.String(), `"playback_token"`)
			if hasToken != tc.wantToken {
				t.Fatalf("playback_token present = %v, want %v; body=%s", hasToken, tc.wantToken, rec.Body.String())
			}
			if tc.wantToken {
				if got.PlaybackToken == "" || got.ExpiresIn != 21600 {
					t.Errorf("token/expires_in = %q/%d, want a token and 21600", got.PlaybackToken, got.ExpiresIn)
				}
			} else if got.ExpiresIn != 0 {
				t.Errorf("expires_in = %d without a token, want absent", got.ExpiresIn)
			}
		})
	}
}

// TestLiveSessionTokenCarriesThroughTheWholePlaylist is the load-bearing test of
// this change, and the one that decided its design.
//
// A relative URI in an m3u8 resolves against the playlist's URL, and RFC 3986
// §5.2.2 DISCARDS the base's query string when the reference has a path — so
// "?pt=" on master.m3u8 never reaches "<id>-0.ts" by itself, in any client.
// Safari's native HLS player, the only client that cannot set an Authorization
// header and the entire reason ?pt= exists, has no hook to put it back. The
// origin must therefore write the token INTO the playlist, exactly as the VOD
// route has always done.
//
// So this walks the chain a header-less player walks: session → master → the URI
// the master actually printed → segment bytes. If the rewrite regresses, the
// master still returns 200 and only the segments 404, which is a stall no
// authorization test would catch.
func TestLiveSessionTokenCarriesThroughTheWholePlaylist(t *testing.T) {
	srv, root := liveHLSServer(t)
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id, key := seedLiveStream(t, srv, root, owner, "private")
	goLive(t, srv, key)

	var session playbackSessionResponse
	rec := postLivePlaybackSession(srv, id, owner, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("live session = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	// Everything below is ANONYMOUS. The token is the only credential in play.
	master := getWithAuth(srv, session.HLSURL+"?pt="+url.QueryEscape(session.PlaybackToken), "")
	if master.Code != http.StatusOK {
		t.Fatalf("master with the session token = %d; body=%s", master.Code, master.Body.String())
	}
	segmentURI := firstPlaylistURI(t, master.Body.String())
	if !strings.Contains(segmentURI, "pt=") {
		t.Fatalf("segment URI %q carries no token: a native-HLS player would 404 every segment", segmentURI)
	}
	seg := getWithAuth(srv, "/api/v1/live/"+id+"/hls/"+segmentURI, "")
	if seg.Code != http.StatusOK || seg.Body.String() != "TSDATA" {
		t.Fatalf("segment at the rewritten URI = %d body=%q, want 200 TSDATA", seg.Code, seg.Body.String())
	}

	// Without a token the playlist is passed through untouched — the rewrite is
	// carrying a credential, not decorating URIs. A public stream's playlist must
	// stay byte-for-byte what the media server wrote.
	plain := getWithAuth(srv, session.HLSURL, owner)
	if uri := firstPlaylistURI(t, plain.Body.String()); uri != id+"-0.ts" {
		t.Errorf("uncredentialed playlist URI = %q, want the media server's own %q", uri, id+"-0.ts")
	}
	onDisk, err := os.ReadFile(filepath.Join(root, id+".m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Body.String() != string(onDisk) {
		t.Errorf("uncredentialed playlist was modified:\n got %q\nwant %q", plain.Body.String(), string(onDisk))
	}
}

// TestLiveAndVideoSessionsAreOneShape is the anti-fork test.
//
// The value of a session model is that a player learns ONE response and uses it
// for everything it plays; two nearly-identical shapes would be worse than none,
// because the difference would only surface at the client. So both endpoints
// answer with the same Go type, and the only key that may differ between them is
// which subject they name.
func TestLiveAndVideoSessionsAreOneShape(t *testing.T) {
	cfg := testConfig()
	cfg.LiveHLSRoot = t.TempDir()
	cfg.LiveIngestSecret = "s3cret"
	srv, blobs, tc, _ := videoServerEnv(t, cfg)
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	videoID := createPublishedVideo(t, srv, owner, "ada", `{"title":"Clip","privacy":"public"}`)
	seedReadyHLS(t, tc, blobs, videoID)
	streamID, key := seedLiveStream(t, srv, cfg.LiveHLSRoot, owner, "public")
	goLive(t, srv, key)

	vod := jsonKeys(t, postPlaybackSession(srv, videoID, "", "").Body.Bytes())
	live := jsonKeys(t, postLivePlaybackSession(srv, streamID, "", "").Body.Bytes())

	// The live session may never introduce a key of its own: every key it emits
	// is a key the video session emits too, apart from which subject it names.
	// That is the direction that catches a fork.
	//
	// The reverse is deliberately NOT asserted. The shared fields are omitempty,
	// and live truthfully omits some of them — there is no rendition ladder and
	// no MPD behind a single-bitrate MPEG-TS broadcast, and claiming either would
	// be the fork this test guards against, pointed the other way.
	for key := range live {
		if !vod[key] && key != "live_stream_id" {
			t.Errorf("live session carries %q, which is not part of the session shape", key)
		}
	}
	if !vod["video_id"] || vod["live_stream_id"] {
		t.Errorf("a video session must name a video and only a video: %v", vod)
	}
	if !live["live_stream_id"] || live["video_id"] {
		t.Errorf("a live session must name a live stream and only a live stream: %v", live)
	}
}

// jsonKeys decodes an object's top-level key set.
func jsonKeys(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("decode session: %v; body=%s", err, body)
	}
	keys := make(map[string]bool, len(obj))
	for k := range obj {
		keys[k] = true
	}
	return keys
}

// TestLivePlaybackSessionUnavailable keeps the endpoint from becoming an
// existence oracle and from promising playback the deployment cannot serve.
func TestLivePlaybackSessionUnavailable(t *testing.T) {
	t.Run("unknown or unparseable id is 404", func(t *testing.T) {
		srv, _ := liveHLSServer(t)
		for _, id := range []string{"not-a-uuid", uuid.NewString()} {
			if rec := postLivePlaybackSession(srv, id, "", ""); rec.Code != http.StatusNotFound {
				t.Errorf("session for %q = %d, want 404", id, rec.Code)
			}
		}
	})
	t.Run("no LIVE_HLS_ROOT is 404", func(t *testing.T) {
		// A live stream with nowhere to read segments from cannot be played, so a
		// session for it would hand out an hls_url that 404s.
		cfg := testConfig()
		cfg.LiveIngestSecret = "s3cret"
		srv := videoServerCfg(t, cfg)
		owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
		var created createLiveStreamResponse
		_ = json.Unmarshal(createLiveStream(srv, "ada", `{"title":"Show"}`, owner).Body.Bytes(), &created)
		goLive(t, srv, created.StreamKey)

		if rec := postLivePlaybackSession(srv, created.LiveStream.ID, owner, ""); rec.Code != http.StatusNotFound {
			t.Errorf("session without LIVE_HLS_ROOT = %d, want 404", rec.Code)
		}
	})
}

// liveSessionToken opens a session as the owner and returns the token it minted.
func liveSessionToken(t *testing.T, srv *Server, id, owner string) string {
	t.Helper()
	rec := postLivePlaybackSession(srv, id, owner, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("live session = %d; body=%s", rec.Code, rec.Body.String())
	}
	var got playbackSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if got.PlaybackToken == "" {
		t.Fatalf("session for a private stream carried no token; body=%s", rec.Body.String())
	}
	return got.PlaybackToken
}

// firstPlaylistURI returns the first non-comment, non-blank line of an m3u8 —
// the URI a player would follow next.
func firstPlaylistURI(t *testing.T, playlist string) string {
	t.Helper()
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	t.Fatalf("playlist has no URI line: %q", playlist)
	return ""
}
