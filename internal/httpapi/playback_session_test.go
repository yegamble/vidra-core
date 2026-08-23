package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

// postPlaybackSession opens a playback session, optionally carrying an account
// bearer token and/or a ?pt= playback token (the header-less path).
func postPlaybackSession(srv *Server, id, bearer, pt string) *httptest.ResponseRecorder {
	path := "/api/v1/videos/" + id + "/playback-session"
	if pt != "" {
		path += "?pt=" + pt
	}
	return doJSON(srv, http.MethodPost, path, bearer, "")
}

// playbackSessionEnv is one test's server plus the seams a case needs to build
// its fixture video.
type playbackSessionEnv struct {
	srv   *Server
	blobs storage.Backend
	tc    *transcodeFakeRepo
	owner string // the owner's account token; the channel handle is always "ada"
}

func newPlaybackSessionEnv(t *testing.T) playbackSessionEnv {
	t.Helper()
	srv, blobs, tc, _ := videoServerEnv(t, testConfig())
	return playbackSessionEnv{
		srv:   srv,
		blobs: blobs,
		tc:    tc,
		owner: createChannelFor(t, srv, "ada", "ada@example.test", "ada"),
	}
}

// TestPlaybackSessionAuthorizationAndShape is the contract for phase-4 item 1.
//
// Two things are being pinned at once, and they pull in opposite directions.
// AUTHORIZATION must be indistinguishable from the media routes' — the session
// calls videoVisibleForMedia rather than restating it, so a 404 stays a 404 and
// the deliberate 401 password_required keeps the unlock prompt working. And the
// TOKEN must stay conditional: a session that minted one for every viewer would
// mark every subsequent media request credentialed, which forces no-store and
// forbids every redirect, silently deleting CDN and presigned delivery for the
// whole instance. That failure has no error and no log line, so this table is
// the only thing that would catch it.
func TestPlaybackSessionAuthorizationAndShape(t *testing.T) {
	cases := []struct {
		name string
		// setup builds the fixture and returns the video id plus the credentials
		// the request carries.
		setup      func(t *testing.T, env playbackSessionEnv) (id, bearer, pt string)
		wantStatus int
		wantCode   string // stable error code expected in the body ("" = no error)
		wantToken  bool
		wantFormat string
		wantDASH   bool
	}{
		{
			// The case the CDN depends on: an ordinary public video gets a
			// session with NO credential in it.
			name: "public video, anonymous",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Clip","privacy":"public"}`)
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", ""
			},
			wantStatus: http.StatusOK,
			wantFormat: media.HLSFormatTS,
		},
		{
			// The unlock prompt's trigger. 401 with a stable code, not 404 —
			// this is the one deliberate exception to 404-for-invisible, and the
			// watch/embed page renders its password form from it.
			name: "password video, no credential",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPasswordVideo(t, env.srv, env.owner, "ada", "Locked", "hunter2secret")
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", ""
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "password_required",
		},
		{
			// The one tier that genuinely needs a credential gets one, and the
			// caller's existing token is renewed into a fresh session.
			name: "password video, valid ?pt=",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPasswordVideo(t, env.srv, env.owner, "ada", "Locked", "hunter2secret")
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", unlockToken(t, env.srv, id, "hunter2secret")
			},
			wantStatus: http.StatusOK,
			wantToken:  true,
			wantFormat: media.HLSFormatTS,
		},
		{
			// The owner of a password video has never been able to watch it in
			// Safari: native HLS cannot send an Authorization header and the
			// password gate has no cookie path. The session hands them the token
			// their own player needs.
			name: "password video, owner",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPasswordVideo(t, env.srv, env.owner, "ada", "Locked", "hunter2secret")
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, env.owner, ""
			},
			wantStatus: http.StatusOK,
			wantToken:  true,
			wantFormat: media.HLSFormatTS,
		},
		{
			name: "private video, anonymous",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Secret","privacy":"private"}`)
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", ""
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "unpublished video, anonymous",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createVideo(t, env.srv, env.owner, "ada", `{"title":"Draft","privacy":"public"}`)
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", ""
			},
			wantStatus: http.StatusNotFound,
		},
		{
			// Same video, owner's bearer: 200, and still no token — an
			// unpublished video is gated on account identity, which a media
			// element's request already carries in the session cookie.
			name: "unpublished video, owner",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createVideo(t, env.srv, env.owner, "ada", `{"title":"Draft","privacy":"public"}`)
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, env.owner, ""
			},
			wantStatus: http.StatusOK,
			wantFormat: media.HLSFormatTS,
		},
		{
			// Format discovery, the phase-3 inheritance: a CMAF tree also serves
			// DASH from the same segments, and until now nothing told a client
			// the MPD was there.
			name: "CMAF video advertises its DASH manifest",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"CMAF","privacy":"public"}`)
				seedReadyCMAF(t, env.tc, env.blobs, id)
				return id, "", ""
			},
			wantStatus: http.StatusOK,
			wantFormat: media.HLSFormatCMAF,
			wantDASH:   true,
		},
		{
			// An MPEG-TS tree has no MPD, so claiming one would send a DASH
			// player to a 404.
			name: "MPEG-TS video advertises no DASH manifest",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"TS","privacy":"public"}`)
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", ""
			},
			wantStatus: http.StatusOK,
			wantFormat: media.HLSFormatTS,
		},
		{
			// Authorized, but nothing transcoded yet. 200 with no manifest —
			// 404 would be indistinguishable from "no such video" and would
			// break the progressive /original path that still works here.
			name: "no ready tree yet",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				return createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Fresh","privacy":"public"}`), "", ""
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newPlaybackSessionEnv(t)
			id, bearer, pt := tc.setup(t, env)
			rec := postPlaybackSession(env.srv, id, bearer, pt)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode != "" {
				if !strings.Contains(rec.Body.String(), `"code":"`+tc.wantCode+`"`) {
					t.Fatalf("body = %s, want error code %q", rec.Body.String(), tc.wantCode)
				}
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
			if got.VideoID != id {
				t.Errorf("video_id = %q, want %q", got.VideoID, id)
			}
			if got.PackagingFormat != tc.wantFormat {
				t.Errorf("packaging_format = %q, want %q", got.PackagingFormat, tc.wantFormat)
			}
			// A format means a ready tree, which means a master playlist.
			if (got.HLSURL != "") != (tc.wantFormat != "") {
				t.Errorf("hls_url = %q, want present=%v", got.HLSURL, tc.wantFormat != "")
			}
			if tc.wantDASH {
				want := "/api/v1/videos/" + id + "/hls/cmaf/stream.mpd"
				if got.DASHURL != want {
					t.Errorf("dash_url = %q, want %q", got.DASHURL, want)
				}
			} else if got.DASHURL != "" {
				t.Errorf("dash_url = %q, want absent", got.DASHURL)
			}

			// The regression this endpoint exists to avoid. A token in a
			// response for a video that does not need one is not a harmless
			// extra field: every media request made with it is treated as
			// credentialed, and credentialed requests are never cached and never
			// redirected — so CDN and presigned delivery quietly stop happening
			// for everyone. Assert on the RAW body, so a rename of the Go field
			// cannot make this pass by accident.
			hasToken := strings.Contains(rec.Body.String(), `"playback_token"`)
			if hasToken != tc.wantToken {
				t.Fatalf("playback_token present = %v, want %v; body=%s", hasToken, tc.wantToken, rec.Body.String())
			}
			if tc.wantToken {
				if got.PlaybackToken == "" || got.ExpiresIn != 21600 {
					t.Errorf("token/expires_in = %q/%d, want a token and 21600", got.PlaybackToken, got.ExpiresIn)
				}
				// The minted token must actually open the media it was minted
				// for — a credential nothing accepts is worse than none.
				if rec := getHLS(env.srv, got.HLSURL+"&pt="+got.PlaybackToken, ""); rec.Code != http.StatusOK {
					t.Errorf("master with the session token = %d, want 200; body=%s", rec.Code, rec.Body.String())
				}
			} else if got.ExpiresIn != 0 {
				t.Errorf("expires_in = %d without a token, want absent", got.ExpiresIn)
			}
			// The manifest URLs never carry the credential themselves: they stay
			// plain origin-relative paths so they remain cacheable, and the
			// client appends ?pt= when it has one.
			if strings.Contains(got.HLSURL, "pt=") || strings.Contains(got.DASHURL, "pt=") {
				t.Errorf("manifest URLs must not embed a token: hls=%q dash=%q", got.HLSURL, got.DASHURL)
			}
		})
	}
}

// TestPlaybackSessionMintsAFreshSessionEachTime proves the session id is
// per-session rather than per-video: it is the QoE correlation key (phase-4 item
// 4), so two playbacks of the same video must never share one.
func TestPlaybackSessionMintsAFreshSessionEachTime(t *testing.T) {
	env := newPlaybackSessionEnv(t)
	id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Clip","privacy":"public"}`)
	seedReadyHLS(t, env.tc, env.blobs, id)

	first, second := playbackSession(t, env.srv, id), playbackSession(t, env.srv, id)
	if first.SessionID == second.SessionID {
		t.Fatalf("two sessions for the same video share an id (%s)", first.SessionID)
	}
}

// TestPlaybackSessionOnUnknownVideoIs404 keeps the endpoint from becoming an
// existence oracle: an unparseable or unknown id answers exactly as an
// invisible one does.
func TestPlaybackSessionOnUnknownVideoIs404(t *testing.T) {
	env := newPlaybackSessionEnv(t)
	for _, id := range []string{"not-a-uuid", uuid.NewString()} {
		if rec := postPlaybackSession(env.srv, id, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("session for %q = %d, want 404", id, rec.Code)
		}
	}
}

// TestVideoDetailCarriesFormatDiscovery pins the same two fields on the video
// detail. A client reads that response first, and until now a CMAF video and an
// MPEG-TS video were indistinguishable on it — both serve HLS from hls_url — so
// the DASH manifest shipped in phase 3 was reachable only by hand.
func TestVideoDetailCarriesFormatDiscovery(t *testing.T) {
	env := newPlaybackSessionEnv(t)
	cmaf := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"CMAF","privacy":"public"}`)
	seedReadyCMAF(t, env.tc, env.blobs, cmaf)
	ts := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"TS","privacy":"public"}`)
	seedReadyHLS(t, env.tc, env.blobs, ts)
	pending := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Fresh","privacy":"public"}`)

	detail := func(id string) videoView {
		t.Helper()
		rec := getVideo(env.srv, id, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("detail %s = %d; body=%s", id, rec.Code, rec.Body.String())
		}
		var v videoView
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("decode detail: %v", err)
		}
		return v
	}

	if v := detail(cmaf); v.PackagingFormat != media.HLSFormatCMAF ||
		v.DASHURL == nil || *v.DASHURL != "/api/v1/videos/"+cmaf+"/hls/cmaf/stream.mpd" {
		t.Errorf("CMAF detail = (format %q, dash %v), want cmaf + the MPD path", v.PackagingFormat, v.DASHURL)
	}
	if v := detail(ts); v.PackagingFormat != media.HLSFormatTS || v.DASHURL != nil {
		t.Errorf("MPEG-TS detail = (format %q, dash %v), want hls-ts + no dash_url", v.PackagingFormat, v.DASHURL)
	}
	// Nothing transcoded: no hls_url, and therefore no format claim either.
	if v := detail(pending); v.PackagingFormat != "" || v.DASHURL != nil || v.HLSURL != nil {
		t.Errorf("pending detail = (format %q, dash %v, hls %v), want all absent", v.PackagingFormat, v.DASHURL, v.HLSURL)
	}
}

// TestDASHManifestIsReachableAtTheAdvertisedURL closes the loop: the URL the
// session hands out is the URL that serves the manifest, unversioned, with the
// DASH content type.
func TestDASHManifestIsReachableAtTheAdvertisedURL(t *testing.T) {
	env := newPlaybackSessionEnv(t)
	id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"CMAF","privacy":"public"}`)
	seedReadyCMAF(t, env.tc, env.blobs, id)

	got := playbackSession(t, env.srv, id)
	rec := getHLS(env.srv, got.DASHURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("advertised dash_url %q = %d; body=%s", got.DASHURL, rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, contentTypeMPD) {
		t.Errorf("dash_url Content-Type = %q, want %q", ct, contentTypeMPD)
	}
	// Unversioned on purpose: a DASH player expands SegmentTemplate itself and
	// fetches the segments without a query string, so a ?v= would fence only the
	// manifest. Advertising an immutable-looking URL over revalidated segments is
	// the cache-coherency trap phase 4's ground truth calls out.
	if strings.Contains(got.DASHURL, hlsVersionParam+"=") {
		t.Errorf("dash_url = %q, want no version parameter", got.DASHURL)
	}
}

// playbackSession opens a session anonymously and decodes it.
func playbackSession(t *testing.T, srv *Server, id string) playbackSessionResponse {
	t.Helper()
	rec := postPlaybackSession(srv, id, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("playback session = %d; body=%s", rec.Code, rec.Body.String())
	}
	var got playbackSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode session: %v; body=%s", err, rec.Body.String())
	}
	return got
}
