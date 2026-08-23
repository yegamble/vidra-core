package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/playback"
	"github.com/vidra/vidra-core/internal/qoe"
)

// recordingQoE captures what the handler decided, which is the interesting part:
// every identity- and source-shaped field on a stored event is derived by the
// server, so these assertions are about what the SERVER concluded rather than
// what the client sent.
type recordingQoE struct {
	events []qoe.Event
	health qoe.PlaybackHealth
	err    error
	gotIn  struct {
		start, end    time.Time
		limit, offset int
	}
}

func (r *recordingQoE) Record(_ context.Context, e qoe.Event) { r.events = append(r.events, e) }

func (r *recordingQoE) PlaybackHealth(_ context.Context, start, end time.Time, limit, offset int) (qoe.PlaybackHealth, error) {
	r.gotIn.start, r.gotIn.end, r.gotIn.limit, r.gotIn.offset = start, end, limit, offset
	return r.health, r.err
}

const qoeTestSecret = "test-secret-test-secret-test-secret-0"

func qoeServer(t *testing.T, rec *recordingQoE, opts ...Option) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.JWTSecret = qoeTestSecret
	classifier := qoe.NewClassifier(
		"https://cdn.example.com/media",
		"https://gateway.example.org",
		"https://s3.example.net",
		"https://vidra.example.com",
	)
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer(qoeTestSecret, "vidra", "vidra", 15*time.Minute)
	all := append([]Option{
		WithAuthService(auth.NewService(repo, issuer, 720*time.Hour), 15*time.Minute),
		WithQoEService(rec, qoe.NewDigester([]byte(qoeTestSecret)), classifier),
	}, opts...)
	return New(cfg, nil, nil, all...)
}

func qoeBody(events ...string) string {
	out := `{"events":[`
	for i, e := range events {
		if i > 0 {
			out += ","
		}
		out += e
	}
	return out + `]}`
}

// TestQoEBeaconClassifiesTheOriginServerSide is the point of the whole ingest
// design: the client says which URL it fetched from, the SERVER says what that
// is. A client cannot name a delivery source, and therefore cannot expand the
// dimension by naming a new host.
func TestQoEBeaconClassifiesTheOriginServerSide(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want qoe.DeliverySource
	}{
		{"cdn", "https://cdn.example.com/media/videos/x/hls/seg.ts", qoe.SourceCDN},
		{"gateway", "https://gateway.example.org/ipfs/bafy/720p/seg.ts", qoe.SourceIPFSGateway},
		{"object store", "https://s3.example.net/bucket/k?X-Amz-Signature=x", qoe.SourcePresigned},
		{"own origin", "https://vidra.example.com/api/v1/videos/x/hls/seg.ts", qoe.SourceAPIProxy},
		{"relative", "/api/v1/videos/x/hls/seg.ts", qoe.SourceAPIProxy},
		{"unrecognised collapses to one bucket", "https://some-other-cdn.test/seg.ts", qoe.SourceOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingQoE{}
			srv := qoeServer(t, rec)
			body := qoeBody(`{"type":"playback.start","video_id":"` + uuid.New().String() +
				`","engine":"hls-js","packaging_format":"cmaf","ttff_ms":812,"source_url":"` + tc.url + `"}`)
			res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", body, "")
			if res.Code != http.StatusAccepted {
				t.Fatalf("POST = %d, want 202; body=%s", res.Code, res.Body.String())
			}
			if len(rec.events) != 1 {
				t.Fatalf("recorded %d events, want 1", len(rec.events))
			}
			if got := rec.events[0].DeliverySource; got != tc.want {
				t.Errorf("delivery_source = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestQoEBeaconRejectsUnknownVocabularyAllOrNothing: one bad event rejects the
// batch. A partial accept would let a client whose vocabulary has drifted keep
// sending forever while silently losing half its data.
func TestQoEBeaconRejectsUnknownVocabularyAllOrNothing(t *testing.T) {
	vid := uuid.New().String()
	good := `{"type":"playback.start","video_id":"` + vid + `","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800}`
	bad := map[string]string{
		"unknown type":     `{"type":"playback.vibes","video_id":"` + vid + `","engine":"hls-js","packaging_format":"cmaf"}`,
		"unknown engine":   `{"type":"playback.start","video_id":"` + vid + `","engine":"videojs","packaging_format":"cmaf","ttff_ms":800}`,
		"unknown format":   `{"type":"playback.start","video_id":"` + vid + `","engine":"hls-js","packaging_format":"hls","ttff_ms":800}`,
		"unknown error":    `{"type":"playback.error","video_id":"` + vid + `","engine":"hls-js","packaging_format":"cmaf","error_class":"kaboom"}`,
		"missing ttff":     `{"type":"playback.start","video_id":"` + vid + `","engine":"hls-js","packaging_format":"cmaf"}`,
		"bad video id":     `{"type":"playback.start","video_id":"not-a-uuid","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800}`,
		"native rendition": `{"type":"playback.start","video_id":"` + vid + `","engine":"native-hls","packaging_format":"cmaf","ttff_ms":800,"rendition_height":720}`,
	}
	for name, badEvent := range bad {
		t.Run(name, func(t *testing.T) {
			rec := &recordingQoE{}
			srv := qoeServer(t, rec)
			res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", qoeBody(good, badEvent), "")
			if res.Code == http.StatusAccepted {
				t.Fatalf("POST = 202 for a batch containing an invalid event; body=%s", res.Body.String())
			}
			if len(rec.events) != 0 {
				t.Errorf("recorded %d events from a rejected batch, want 0 (all-or-nothing)", len(rec.events))
			}
		})
	}
}

// TestQoEBeaconCapsTheBatch mirrors the search beacon's cap: a batch is a
// network optimisation, not a bulk-import channel.
func TestQoEBeaconCapsTheBatch(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec)
	vid := uuid.New().String()
	one := `{"type":"playback.start","video_id":"` + vid + `","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800}`
	events := make([]string, 21)
	for i := range events {
		events[i] = one
	}
	if res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", qoeBody(events...), ""); res.Code == http.StatusAccepted {
		t.Errorf("21 events accepted; want a rejection")
	}
	if len(rec.events) != 0 {
		t.Errorf("recorded %d events from an oversized batch", len(rec.events))
	}
	// An empty batch is a 202 and not an error: a client with nothing to report
	// should not have to special-case that.
	if res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", `{"events":[]}`, ""); res.Code != http.StatusAccepted {
		t.Errorf("empty batch = %d, want 202", res.Code)
	}
}

// TestQoEBeaconEnrichesIdentityServerSide: a beacon never says who it is, and a
// user_id in the body is not even read.
func TestQoEBeaconEnrichesIdentityServerSide(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec)
	vid := uuid.New().String()
	body := qoeBody(`{"type":"playback.start","video_id":"` + vid +
		`","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800,"user_id":"11111111-1111-1111-1111-111111111111","viewer_digest":"attacker-chosen"}`)
	if res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", body, ""); res.Code != http.StatusAccepted {
		t.Fatalf("POST = %d; body=%s", res.Code, res.Body.String())
	}
	if len(rec.events) != 1 {
		t.Fatalf("recorded %d events", len(rec.events))
	}
	got := rec.events[0]
	if got.ViewerDigest == "" || got.ViewerDigest == "attacker-chosen" {
		t.Errorf("viewer_digest = %q; it must be derived by the server", got.ViewerDigest)
	}
	// The digest is a keyed HMAC hex string, so it is fixed-width and carries no
	// address.
	if len(got.ViewerDigest) != 64 {
		t.Errorf("viewer_digest has length %d, want 64 hex chars", len(got.ViewerDigest))
	}
}

// TestQoESessionVerificationNeedsASignedToken is the honest answer to "can an
// admin trust these session ids": only when a token proves them. core#74 records
// no sessions, so a public video's id is client-asserted and the event says so.
func TestQoESessionVerificationNeedsASignedToken(t *testing.T) {
	videoID := uuid.New()
	sessionID := uuid.New()
	signer := playback.NewSigner([]byte(qoeTestSecret))
	token := signer.Sign(videoID, sessionID, playback.ScopePlayback, time.Hour)

	base := func(extra string) string {
		return qoeBody(`{"type":"playback.start","video_id":"` + videoID.String() +
			`","session_id":"` + sessionID.String() +
			`","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800` + extra + `}`)
	}
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"no token is client-asserted", base(""), false},
		{"matching token attests", base(`,"playback_token":"` + token + `"`), true},
		{"garbage token does not attest", base(`,"playback_token":"not.a.token"`), false},
		{"token for another session does not attest",
			base(`,"playback_token":"` + signer.Sign(videoID, uuid.New(), playback.ScopePlayback, time.Hour) + `"`), false},
		{"token for another video does not attest",
			base(`,"playback_token":"` + signer.Sign(uuid.New(), sessionID, playback.ScopePlayback, time.Hour) + `"`), false},
		{"a live-scoped token does not open a video session",
			base(`,"playback_token":"` + signer.Sign(videoID, sessionID, playback.ScopeLive, time.Hour) + `"`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingQoE{}
			srv := qoeServer(t, rec)
			res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", tc.body, "")
			if res.Code != http.StatusAccepted {
				t.Fatalf("POST = %d; body=%s", res.Code, res.Body.String())
			}
			if len(rec.events) != 1 {
				t.Fatalf("recorded %d events, want 1 — an unverifiable token must not cost the measurement", len(rec.events))
			}
			if got := rec.events[0].SessionVerified; got != tc.want {
				t.Errorf("session_verified = %v, want %v", got, tc.want)
			}
			if rec.events[0].SessionID != sessionID {
				t.Errorf("session_id = %v, want %v", rec.events[0].SessionID, sessionID)
			}
		})
	}
}

// TestQoEBeaconRespectsTheKillSwitch: turning collection off answers 202 and
// records nothing, so a client cannot tell (and cannot behave differently) when
// an operator flips it mid-incident.
func TestQoEBeaconRespectsTheKillSwitch(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec, WithSettingsService(newQoESettings(t, false)))
	body := qoeBody(`{"type":"playback.start","video_id":"` + uuid.New().String() +
		`","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800}`)
	res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", body, "")
	if res.Code != http.StatusAccepted {
		t.Fatalf("POST with collection off = %d, want 202", res.Code)
	}
	if len(rec.events) != 0 {
		t.Errorf("recorded %d events with collection off", len(rec.events))
	}
}

// TestQoEBeaconDefaultsToCollecting: an install that never visits the settings
// page still measures, which is the whole reason the default is on.
func TestQoEBeaconDefaultsToCollecting(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec, WithSettingsService(newQoESettings(t, true)))
	body := qoeBody(`{"type":"playback.start","video_id":"` + uuid.New().String() +
		`","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800}`)
	if res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", body, ""); res.Code != http.StatusAccepted {
		t.Fatalf("POST = %d, want 202", res.Code)
	}
	if len(rec.events) != 1 {
		t.Errorf("recorded %d events with the default setting, want 1", len(rec.events))
	}
}

// TestQoEBeaconRejectsAnOversizedSourceURL fences the one unbounded string a
// client controls, before it is parsed.
func TestQoEBeaconRejectsAnOversizedSourceURL(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec)
	long := "https://cdn.example.com/media/"
	for len(long) <= maxSourceURLLength {
		long += "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	body := qoeBody(`{"type":"playback.start","video_id":"` + uuid.New().String() +
		`","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800,"source_url":"` + long + `"}`)
	if res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", body, ""); res.Code == http.StatusAccepted {
		t.Error("an oversized source_url was accepted")
	}
	if len(rec.events) != 0 {
		t.Errorf("recorded %d events", len(rec.events))
	}
}

// TestQoEBeaconRejectionDoesNotEchoInput: the response reaches a client and is
// logged by intermediaries, so it must never quote a signed URL back.
func TestQoEBeaconRejectionDoesNotEchoInput(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec)
	body := qoeBody(`{"type":"playback.error","video_id":"` + uuid.New().String() +
		`","engine":"hls-js","packaging_format":"cmaf","error_class":"https://s3.example.net/k?X-Amz-Signature=deadbeef"}`)
	res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", body, "")
	if res.Code == http.StatusAccepted {
		t.Fatal("an unknown error class was accepted")
	}
	if b := res.Body.String(); containsAny(b, "X-Amz-Signature", "s3.example.net", "deadbeef") {
		t.Errorf("rejection echoed client input: %s", b)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// TestQoERoutesAbsentWithoutTheService: an install that wires no telemetry has no
// beacon route at all, rather than one that silently discards.
func TestQoERoutesAbsentWithoutTheService(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", `{"events":[]}`, "")
	if res.Code != http.StatusNotFound {
		t.Errorf("POST with no QoE service = %d, want 404", res.Code)
	}
}

// newQoESettings builds a real settings service with the collection toggle in
// the requested position. Real rather than faked: the default-on decision lives
// in the settings registry, so a fake would let the two disagree.
func newQoESettings(t *testing.T, on bool) *instancesettings.Service {
	t.Helper()
	svc := instancesettings.NewService(newInstanceSettingsFakeRepo(), instancesettings.Defaults{})
	if on {
		// The default is already on; asserting it here is the point of the
		// TestQoEBeaconDefaultsToCollecting case.
		if !svc.Bool(instancesettings.KeyQoECollectionEnabled) {
			t.Fatal("qoe_collection_enabled defaults to false; the registry and this test disagree")
		}
		return svc
	}
	if err := svc.Apply(context.Background(), map[string]instancesettings.Update{
		instancesettings.KeyQoECollectionEnabled: {Value: "false"},
	}, uuid.Nil); err != nil {
		t.Fatalf("turn qoe collection off: %v", err)
	}
	return svc
}
