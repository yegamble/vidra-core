package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/video"
)

// errBodyOf decodes the error envelope so a test can assert on the identifier
// fields without string-matching JSON.
func errBodyOf(t *testing.T, rec *httptest.ResponseRecorder) ErrorBody {
	t.Helper()
	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rec.Body.String())
	}
	return resp.Error
}

// A password_required 401 must name the video, on BOTH ways of asking for it.
//
// This is what unblocks the /v/{code} watch page: the unlock endpoint is keyed
// on the uuid, and a caller who arrived by short code has no other way to learn
// it. Without this the unlock prompt has nowhere to post.
func TestPasswordRequired401NamesTheVideo(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPasswordVideo(t, srv, tok, "ada", "Locked", "correct-horse")
	code := shortCodeOf(t, srv, id, tok)

	t.Run("by uuid", func(t *testing.T) {
		rec := getVideo(srv, id, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("anon detail = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
		b := errBodyOf(t, rec)
		if b.Code != "password_required" {
			t.Fatalf("code = %q, want password_required", b.Code)
		}
		if b.VideoID != id {
			t.Errorf("video_id = %q, want %q", b.VideoID, id)
		}
		if b.ShortCode != code {
			t.Errorf("short_code = %q, want %q", b.ShortCode, code)
		}
	})

	// The case that actually blocks the frontend.
	t.Run("by short code", func(t *testing.T) {
		rec := getByCode(srv, code, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("resolve by code = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
		b := errBodyOf(t, rec)
		if b.VideoID != id {
			t.Fatalf("video_id = %q, want %q — a /v/{code} page cannot reach the unlock endpoint without it", b.VideoID, id)
		}
		if !video.ValidShortCode(b.ShortCode) {
			t.Errorf("short_code = %q, which is not a valid code", b.ShortCode)
		}
	})
}

// The end-to-end journey the change exists for: arrive by short code, unlock
// using only what the 401 handed back, then read the video.
func TestUnlockUsingOnlyWhatTheLocked401Returned(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPasswordVideo(t, srv, tok, "ada", "Locked", "correct-horse")
	code := shortCodeOf(t, srv, id, tok)

	// A client that knows ONLY the short code.
	rec := getByCode(srv, code, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("resolve = %d, want 401", rec.Code)
	}
	discovered := errBodyOf(t, rec).VideoID
	if discovered == "" {
		t.Fatal("401 returned no video_id; the journey cannot continue")
	}

	token := unlockToken(t, srv, discovered, "correct-horse")

	rec = getByCode(srv, code, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("resolve without the token = %d, want 401 still", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/resolve?code="+code+"&pt="+token, nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve with playback token = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// The identifiers are password_required-ONLY. ErrorBody is the envelope every
// endpoint uses, so a field that leaked onto other errors would be handing out
// ids for videos the caller was told do not exist.
func TestOtherErrorsCarryNoVideoIdentifiers(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	private := createVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	code := shortCodeOf(t, srv, private, tok)

	cases := []struct {
		name string
		path string
		want int
	}{
		{"private video is 404, not a named 401", "/api/v1/videos/" + private, http.StatusNotFound},
		{"private video by code is 404", "/api/v1/videos/resolve?code=" + code, http.StatusNotFound},
		{"unknown uuid", "/api/v1/videos/" + uuid.New().String(), http.StatusNotFound},
		{"malformed code", "/api/v1/videos/resolve?code=nope", http.StatusNotFound},
		{"no identifier at all", "/api/v1/videos/resolve", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			// Assert on RAW JSON: absent is the contract, and a struct decode
			// cannot tell absent from "".
			var raw map[string]map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
			}
			for _, f := range []string{"video_id", "short_code"} {
				if v, present := raw["error"][f]; present {
					t.Errorf("%s leaked %q onto a %d response", f, v, tc.want)
				}
			}
		})
	}
}
