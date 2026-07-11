package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ratelimit"
)

// createPasswordVideo publishes a video, adds `password`, flips its privacy to
// "password", and returns its id.
func createPasswordVideo(t *testing.T, srv *Server, token, handle, title, password string) string {
	t.Helper()
	id := createPublishedVideo(t, srv, token, handle, `{"title":"`+title+`","privacy":"private"}`)
	if rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/passwords", `{"password":"`+password+`"}`, token); rec.Code != http.StatusCreated {
		t.Fatalf("add password = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, token, `{"privacy":"password"}`); rec.Code != http.StatusOK {
		t.Fatalf("set privacy=password = %d; body=%s", rec.Code, rec.Body.String())
	}
	return id
}

// unlockToken unlocks a password video and returns the playback token.
func unlockToken(t *testing.T, srv *Server, id, password string) string {
	t.Helper()
	rec := postTo(srv, "/api/v1/videos/"+id+"/unlock", `{"password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unlock = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp unlockResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode unlock: %v; body=%s", err, rec.Body.String())
	}
	if resp.PlaybackToken == "" || resp.ExpiresIn != 21600 {
		t.Fatalf("unlock response = %+v, want a token + expires_in 21600", resp)
	}
	return resp.PlaybackToken
}

// TestVideoPasswordCRUD proves add/list/replace/delete, that no plaintext or hash
// is ever echoed, and owner gating.
func TestVideoPasswordCRUD(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"private"}`)

	// Add — 201 with id + created_at only; NEVER the plaintext or a hash field.
	rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/passwords", `{"password":"hunter2secret"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "hunter2secret") || strings.Contains(body, "hash") || strings.Contains(body, "password_hash") {
		t.Fatalf("add response leaks the password/hash: %s", body)
	}
	var added videoPasswordView
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	if added.ID == "" || added.CreatedAt.IsZero() {
		t.Fatalf("add response = %+v, want id + created_at", added)
	}

	// List — one entry, no secret.
	rec = doJSON(srv, http.MethodGet, "/api/v1/videos/"+id+"/passwords", tok, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), added.ID) {
		t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Replace-all — two entries.
	rec = putJSONAuth(srv, "/api/v1/videos/"+id+"/passwords", `{"passwords":["first-secret","second-secret"]}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace = %d; body=%s", rec.Code, rec.Body.String())
	}
	var listed videoPasswordsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &listed)
	if len(listed.Passwords) != 2 {
		t.Fatalf("replace response = %d passwords, want 2", len(listed.Passwords))
	}

	// Delete one — 204 (still one left, non-password video so no last-guard).
	rec = doJSON(srv, http.MethodDelete, "/api/v1/videos/"+id+"/passwords/"+listed.Passwords[0].ID, tok, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Delete an unknown password id — 404.
	rec = doJSON(srv, http.MethodDelete, "/api/v1/videos/"+id+"/passwords/"+uuid.New().String(), tok, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown = %d, want 404", rec.Code)
	}

	// A non-owner sees 404 on every management endpoint (existence not leaked).
	other := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := doJSON(srv, http.MethodGet, "/api/v1/videos/"+id+"/passwords", other, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner list = %d, want 404", rec.Code)
	}
	if rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/passwords", `{"password":"another-one"}`, other); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner add = %d, want 404", rec.Code)
	}
	// Unauthenticated → 401.
	if rec := doJSON(srv, http.MethodGet, "/api/v1/videos/"+id+"/passwords", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon list = %d, want 401", rec.Code)
	}
}

// TestVideoPasswordValidation proves the 6–100 char rule and the 1–20 count rule.
func TestVideoPasswordValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"private"}`)

	for _, bad := range []string{`{"password":"12345"}`, `{"password":"` + strings.Repeat("x", 101) + `"}`} {
		if rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/passwords", bad, tok); rec.Code != http.StatusBadRequest {
			t.Fatalf("add %q = %d, want 400", bad, rec.Code)
		}
	}
	// Replace-all with zero entries and with >20 entries are both 400.
	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/passwords", `{"passwords":[]}`, tok); rec.Code != http.StatusBadRequest {
		t.Fatalf("replace empty = %d, want 400", rec.Code)
	}
	many := `{"passwords":[` + strings.TrimSuffix(strings.Repeat(`"password123",`, 21), ",") + `]}`
	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/passwords", many, tok); rec.Code != http.StatusBadRequest {
		t.Fatalf("replace 21 = %d, want 400", rec.Code)
	}
}

// TestVideoPasswordLastDelete409 proves deleting the last password of a
// privacy=password video is a 409 (the video would otherwise be unlockable by no one).
func TestVideoPasswordLastDelete409(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPasswordVideo(t, srv, tok, "ada", "Locked", "the-only-password")

	var listed videoPasswordsResponse
	_ = json.Unmarshal(doJSON(srv, http.MethodGet, "/api/v1/videos/"+id+"/passwords", tok, "").Body.Bytes(), &listed)
	if len(listed.Passwords) != 1 {
		t.Fatalf("want exactly one password, got %d", len(listed.Passwords))
	}
	rec := doJSON(srv, http.MethodDelete, "/api/v1/videos/"+id+"/passwords/"+listed.Passwords[0].ID, tok, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete last = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// TestSetPrivacyPasswordRequiresPassword proves you cannot make a video
// privacy=password without at least one password.
func TestSetPrivacyPasswordRequiresPassword(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Create directly as password → 400.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"v","privacy":"password"}`, tok); rec.Code != http.StatusBadRequest {
		t.Fatalf("create password = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"private"}`)
	// Switch to password with no passwords → 400.
	if rec := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, `{"privacy":"password"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("patch->password (no passwords) = %d, want 400", rec.Code)
	}
	// Add a password, then the switch succeeds.
	if rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/passwords", `{"password":"unlock-me-now"}`, tok); rec.Code != http.StatusCreated {
		t.Fatalf("add password = %d", rec.Code)
	}
	if rec := doJSON(srv, http.MethodPatch, "/api/v1/videos/"+id, tok, `{"privacy":"password"}`); rec.Code != http.StatusOK {
		t.Fatalf("patch->password (with password) = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPasswordDetailGateAndUnlock proves the 401 password_required gate on the
// detail, the unlock flow, and that the minted token works via Bearer AND ?pt=.
func TestPasswordDetailGateAndUnlock(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPasswordVideo(t, srv, tok, "ada", "Locked", "correct-horse")

	// Anonymous detail → 401 password_required (NOT 404).
	rec := getVideo(srv, id, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon detail = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"password_required"`) {
		t.Fatalf("anon detail body = %s, want password_required code", rec.Body.String())
	}
	// The owner sees it without any token.
	if rec := getVideo(srv, id, tok); rec.Code != http.StatusOK {
		t.Fatalf("owner detail = %d, want 200", rec.Code)
	}
	// A plain unknown id is still 404 (not 401) — the gate is password-specific.
	if rec := getVideo(srv, uuid.New().String(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id detail = %d, want 404", rec.Code)
	}

	// Wrong password → 401; unknown video → 404; a non-password video → 404.
	if rec := postTo(srv, "/api/v1/videos/"+id+"/unlock", `{"password":"nope"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password unlock = %d, want 401", rec.Code)
	}
	if rec := postTo(srv, "/api/v1/videos/"+uuid.New().String()+"/unlock", `{"password":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown video unlock = %d, want 404", rec.Code)
	}
	publicID := createPublishedVideo(t, srv, tok, "ada", `{"title":"Pub","privacy":"public"}`)
	if rec := postTo(srv, "/api/v1/videos/"+publicID+"/unlock", `{"password":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("non-password video unlock = %d, want 404", rec.Code)
	}

	// Correct unlock → token; detail works via Bearer and via ?pt=.
	token := unlockToken(t, srv, id, "correct-horse")
	if rec := getVideo(srv, id, token); rec.Code != http.StatusOK {
		t.Fatalf("detail with Bearer token = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getHLS(srv, "/api/v1/videos/"+id+"?pt="+token, ""); rec.Code != http.StatusOK {
		t.Fatalf("detail with ?pt= = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// The unlock response never logs/echoes the token beyond playback_token.
	if strings.Contains(getVideo(srv, id, token).Body.String(), token) {
		t.Fatalf("detail body must not echo the playback token")
	}
}

// TestUnlockTokenVideoScoped proves a token minted for video A is rejected on
// video B (the core token-scoping security property, end to end).
func TestUnlockTokenVideoScoped(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	a := createPasswordVideo(t, srv, tok, "ada", "AAA", "password-a")
	b := createPasswordVideo(t, srv, tok, "ada", "BBB", "password-b")

	tokenA := unlockToken(t, srv, a, "password-a")
	// Token A unlocks A but not B.
	if rec := getVideo(srv, a, tokenA); rec.Code != http.StatusOK {
		t.Fatalf("token A on A = %d, want 200", rec.Code)
	}
	if rec := getVideo(srv, b, tokenA); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token A on B = %d, want 401", rec.Code)
	}
	if rec := getHLS(srv, "/api/v1/videos/"+b+"?pt="+tokenA, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token A on B via ?pt= = %d, want 401", rec.Code)
	}
}

// TestPasswordVideoExcludedFromListings proves password videos never appear on
// public discovery surfaces (like unlisted), while a public one does.
func TestPasswordVideoExcludedFromListings(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pwID := createPasswordVideo(t, srv, tok, "ada", "SecretOnly", "hidden-password")
	pubID := createPublishedVideo(t, srv, tok, "ada", `{"title":"OpenToAll","privacy":"public"}`)

	feed := getHLS(srv, "/api/v1/videos", "").Body.String()
	if strings.Contains(feed, pwID) || strings.Contains(feed, "SecretOnly") {
		t.Fatalf("feed must exclude the password video:\n%s", feed)
	}
	if !strings.Contains(feed, pubID) {
		t.Fatalf("feed should include the public video")
	}
	search := getHLS(srv, "/api/v1/videos/search?q=Secret", "").Body.String()
	if strings.Contains(search, pwID) {
		t.Fatalf("search must exclude the password video:\n%s", search)
	}
	// Anonymous channel list excludes it; the owner still sees it.
	anonCh := getHLS(srv, "/api/v1/channels/ada/videos", "").Body.String()
	if strings.Contains(anonCh, pwID) {
		t.Fatalf("anon channel list must exclude the password video")
	}
	ownerCh := getHLS(srv, "/api/v1/channels/ada/videos", tok).Body.String()
	if !strings.Contains(ownerCh, pwID) {
		t.Fatalf("owner channel list should include the password video")
	}
}

// TestPasswordVideoMediaGatedAndPtRewrite proves media endpoints are gated for a
// password video, that ?pt= unlocks them, and that HLS playlists served with ?pt=
// have their relative URIs rewritten to carry the token (the Safari native-HLS path).
func TestPasswordVideoMediaGatedAndPtRewrite(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPasswordVideo(t, srv, tok, "ada", "Locked", "media-password")
	seedReadyHLS(t, tcRepo, blobs, id)

	// Without a token: original + HLS master are gated (401 password_required).
	if rec := getHLS(srv, "/api/v1/videos/"+id+"/original", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("original without token = %d, want 401", rec.Code)
	}
	if rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("master without token = %d, want 401", rec.Code)
	}

	token := unlockToken(t, srv, id, "media-password")

	// original serves with ?pt=.
	if rec := getHLS(srv, "/api/v1/videos/"+id+"/original?pt="+token, ""); rec.Code != http.StatusOK {
		t.Fatalf("original with ?pt= = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Master playlist served with ?pt= rewrites the variant URI to carry the token.
	master := getHLS(srv, "/api/v1/videos/"+id+"/hls/master.m3u8?pt="+token, "")
	if master.Code != http.StatusOK {
		t.Fatalf("master with ?pt= = %d, want 200", master.Code)
	}
	if !strings.Contains(master.Body.String(), "240p/playlist.m3u8?pt="+token) {
		t.Fatalf("master should rewrite the variant URI with ?pt=:\n%s", master.Body.String())
	}
	// Variant playlist served with ?pt= rewrites the segment URI.
	variant := getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/playlist.m3u8?pt="+token, "")
	if variant.Code != http.StatusOK {
		t.Fatalf("variant with ?pt= = %d, want 200", variant.Code)
	}
	if !strings.Contains(variant.Body.String(), "seg_00000.ts?pt="+token) {
		t.Fatalf("variant should rewrite the segment URI with ?pt=:\n%s", variant.Body.String())
	}
	// A tag line is never rewritten.
	if strings.Contains(variant.Body.String(), "#EXTINF:2.0,?pt=") || strings.Contains(master.Body.String(), "#EXT-X-STREAM-INF") && strings.Contains(master.Body.String(), "INF?pt=") {
		t.Fatalf("tag lines must not be rewritten:\n%s", variant.Body.String())
	}
	// The segment itself serves with ?pt=.
	if rec := getHLS(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts?pt="+token, ""); rec.Code != http.StatusOK {
		t.Fatalf("segment with ?pt= = %d, want 200", rec.Code)
	}
}

// TestUnlockRateLimited proves the unlock endpoint is throttled under the same
// strict auth-budget limiter as login.
func TestUnlockRateLimited(t *testing.T) {
	fc := &fakeCounter{}
	limiter := ratelimit.NewLimiter(fc, 2, time.Minute)
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), []Option{WithAuthRateLimiter(limiter)})
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPasswordVideo(t, srv, tok, "ada", "Locked", "rate-limited-pw")
	// Reset the counter so the auth budget consumed by registration during setup
	// does not shift the deterministic 2-then-deny sequence below.
	fc.counts = nil

	// First two unlock attempts are within budget (they 401 on the wrong password,
	// not 429).
	for i := 1; i <= 2; i++ {
		if rec := postTo(srv, "/api/v1/videos/"+id+"/unlock", `{"password":"wrong"}`); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("unlock #%d throttled too early", i)
		}
	}
	if rec := postTo(srv, "/api/v1/videos/"+id+"/unlock", `{"password":"wrong"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("unlock #3 = %d, want 429", rec.Code)
	}
}

// TestUnlockRateLimitedByDefault proves the unlock endpoint is throttled even
// when NO auth limiter is configured (W1 audit): a built-in in-memory default
// (defaultUnlockRateLimit / minute) engages so the password-guessing surface is
// never left unthrottled on a deployment running without Redis.
func TestUnlockRateLimitedByDefault(t *testing.T) {
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), nil) // no auth limiter
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPasswordVideo(t, srv, tok, "ada", "Locked", "default-limited-pw")

	// The first defaultUnlockRateLimit attempts are within budget (they 401 on the
	// wrong password, not 429); the next one is denied by the built-in default.
	for i := 1; i <= defaultUnlockRateLimit; i++ {
		if rec := postTo(srv, "/api/v1/videos/"+id+"/unlock", `{"password":"wrong"}`); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("unlock #%d throttled too early (built-in default too tight)", i)
		}
	}
	if rec := postTo(srv, "/api/v1/videos/"+id+"/unlock", `{"password":"wrong"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("unlock #%d = %d, want 429 from the built-in default", defaultUnlockRateLimit+1, rec.Code)
	}
}

// TestPlaybackTokenNeverLogged proves the entire query is omitted from the request
// log. Redacting only known token names is insufficient for future secret params.
func TestPlaybackTokenNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), []Option{WithLogger(logger)})
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPasswordVideo(t, srv, tok, "ada", "Locked", "log-safe-password")
	token := unlockToken(t, srv, id, "log-safe-password")

	buf.Reset()
	if rec := getHLS(srv, "/api/v1/videos/"+id+"?pt="+token, ""); rec.Code != http.StatusOK {
		t.Fatalf("detail with ?pt= = %d, want 200", rec.Code)
	}
	logged := buf.String()
	if strings.Contains(logged, token) {
		t.Fatalf("the playback token must never appear in logs:\n%s", logged)
	}
	if strings.Contains(logged, "pt=") || !strings.Contains(logged, `"path":"/api/v1/videos/:id"`) {
		t.Fatalf("request log should contain only the route template, never query parameters:\n%s", logged)
	}
}

// TestEmbedPrivacyGetPutValidation exercises the embed-privacy contract: defaults,
// a whitelist round-trip, the validation matrix, owner gating, and that a password
// video's policy is readable pre-unlock (no 401 on this endpoint).
func TestEmbedPrivacyGetPutValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	// Default is enabled with no allowed_domains.
	rec := getHLS(srv, "/api/v1/videos/"+id+"/embed-privacy", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"enabled"`) {
		t.Fatalf("default embed-privacy = %d; body=%s", rec.Code, rec.Body.String())
	}

	// PUT whitelist with domains → 200, and GET reads it back.
	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/embed-privacy", `{"status":"whitelist","allowed_domains":["Example.com","blog.example.org"]}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("put whitelist = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec = getHLS(srv, "/api/v1/videos/"+id+"/embed-privacy", "")
	if !strings.Contains(rec.Body.String(), `"status":"whitelist"`) || !strings.Contains(rec.Body.String(), "example.com") {
		t.Fatalf("read-back whitelist = %s", rec.Body.String())
	}

	// Validation matrix → 400.
	for _, bad := range []string{
		`{"status":"nope"}`,
		`{"status":"whitelist","allowed_domains":[]}`,
		`{"status":"whitelist","allowed_domains":["https://example.com"]}`,
		`{"status":"enabled","allowed_domains":["example.com"]}`,
	} {
		if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/embed-privacy", bad, tok); rec.Code != http.StatusBadRequest {
			t.Fatalf("put %s = %d, want 400", bad, rec.Code)
		}
	}

	// Owner-only PUT: a non-owner is 404.
	other := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := putJSONAuth(srv, "/api/v1/videos/"+id+"/embed-privacy", `{"status":"disabled"}`, other); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner put = %d, want 404", rec.Code)
	}
	// Unauthenticated PUT → 401.
	if rec := doJSON(srv, http.MethodPut, "/api/v1/videos/"+id+"/embed-privacy", "", `{"status":"disabled"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon put = %d, want 401", rec.Code)
	}

	// A password video's embed policy is readable pre-unlock (this endpoint is NOT
	// password-gated, unlike the detail).
	pwID := createPasswordVideo(t, srv, tok, "ada", "Locked", "embed-read-pw")
	if rec := getHLS(srv, "/api/v1/videos/"+pwID+"/embed-privacy", ""); rec.Code != http.StatusOK {
		t.Fatalf("anon embed-privacy on password video = %d, want 200 (not gated)", rec.Code)
	}
}
