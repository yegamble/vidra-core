package httpapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// The owner's ruling: the discovery opt-out covers PLAYBACK TELEMETRY too.
//
// A13 already decided that a user with all three discovery controls off is
// collected about exactly as an anonymous visitor is — no account-derived value
// in any behavioural search event. The QoE beacon was written before that ruling
// and never consulted it: it stamped a keyed, day-scoped viewer_digest on every
// signed-in viewer's rows regardless. These tests hold the beacon to the SAME
// predicate (searchConsent — there is one definition of "opted out" in this
// codebase and this is it), and to the three things that must NOT change with
// it: the session still lands, so the anonymous rollups still count the
// playback; a default viewer is still attributed; and an anonymous viewer's
// IP-derived digest is untouched.

// qoeBeaconStart posts one well-formed playback.start and returns the single
// event the recorder saw.
func qoeBeaconStart(t *testing.T, srv *Server, rec *recordingQoE, token, session string) qoeRecordedEvent {
	t.Helper()
	before := len(rec.events)
	body := qoeBody(`{"type":"playback.start","video_id":"` + uuid.NewString() +
		`","session_id":"` + session +
		`","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800}`)
	res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", body, token)
	if res.Code != http.StatusAccepted {
		t.Fatalf("POST /qoe/events = %d, want 202; body=%s", res.Code, res.Body.String())
	}
	if len(rec.events) != before+1 {
		t.Fatalf("recorded %d events, want exactly one more than %d", len(rec.events), before)
	}
	e := rec.events[len(rec.events)-1]
	return qoeRecordedEvent{digest: e.ViewerDigest, session: e.SessionID.String(), source: string(e.DeliverySource)}
}

// qoeRecordedEvent is the slice of a stored event these tests are about.
type qoeRecordedEvent struct {
	digest  string
	session string
	source  string
}

// TestQoEOptedOutViewerIsNotAttributed is the ruling itself.
func TestQoEOptedOutViewerIsNotAttributed(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	session := uuid.NewString()
	attributed := qoeBeaconStart(t, srv, rec, tok, session)
	if len(attributed.digest) != 64 {
		t.Fatalf("a default viewer's viewer_digest = %q, want the 64-hex keyed digest — the opt-out must not change the default", attributed.digest)
	}

	optOutAll(t, srv, tok)
	got := qoeBeaconStart(t, srv, rec, tok, session)
	if got.digest != "" {
		t.Errorf("viewer_digest = %q on an opted-out viewer's row, want empty — the discovery opt-out covers playback telemetry", got.digest)
	}
	// The row itself must still land, with its session, or the anonymous
	// histograms silently stop counting the playbacks of everyone who opted out
	// and every percentile an admin reads becomes a sample of the consenting
	// half of the audience.
	if got.session != session {
		t.Errorf("session_id = %q, want %q — an unattributed row still correlates within its own session", got.session, session)
	}
	if got.source != attributed.source {
		t.Errorf("delivery_source = %q, want %q — the classification is not identity-dependent", got.source, attributed.source)
	}
}

// TestQoEOptOutIsServerSide: the decision is taken from the viewer's stored
// prefs at ingest. A client cannot opt IN by sending anything, and cannot opt
// OUT by withholding anything — the body has never carried identity and still
// does not.
func TestQoEOptOutIsServerSide(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec)
	tok, id := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	optOutAll(t, srv, tok)

	body := qoeBody(`{"type":"playback.start","video_id":"` + uuid.NewString() +
		`","engine":"hls-js","packaging_format":"cmaf","ttff_ms":800,` +
		`"user_id":"` + id + `","viewer_digest":"attacker-chosen"}`)
	if res := sendJSONAuth(srv, http.MethodPost, "/api/v1/qoe/events", body, tok); res.Code != http.StatusAccepted {
		t.Fatalf("POST = %d; body=%s", res.Code, res.Body.String())
	}
	if got := rec.events[len(rec.events)-1].ViewerDigest; got != "" {
		t.Errorf("viewer_digest = %q; a body cannot buy back attribution the viewer switched off", got)
	}
}

// TestQoEOptOutIsForwardOnly mirrors TestOptOutIsForwardOnly for the beacon:
// re-enabling a control resumes attribution from that moment. It is the same
// per-request decision, so nothing already written changes and nothing has to be
// backfilled.
func TestQoEOptOutIsForwardOnly(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	optOutAll(t, srv, tok)
	if got := qoeBeaconStart(t, srv, rec, tok, uuid.NewString()); got.digest != "" {
		t.Fatalf("viewer_digest = %q while opted out", got.digest)
	}

	setPrefs(t, srv, tok, `{"search_history_enabled":true}`)
	got := qoeBeaconStart(t, srv, rec, tok, uuid.NewString())
	if len(got.digest) != 64 {
		t.Errorf("viewer_digest = %q after re-enabling one control, want the keyed digest back", got.digest)
	}
}

// TestQoEAnonymousViewerIsUnchanged: the ruling is about SIGNED-IN viewers'
// stored prefs. An anonymous viewer has none, and keeps the IP-derived digest
// that lets an admin ask "was that rebuffer spike one viewer or a thousand?".
func TestQoEAnonymousViewerIsUnchanged(t *testing.T) {
	rec := &recordingQoE{}
	srv := qoeServer(t, rec)
	if got := qoeBeaconStart(t, srv, rec, "", uuid.NewString()); len(got.digest) != 64 {
		t.Errorf("anonymous viewer_digest = %q, want the 64-hex keyed digest", got.digest)
	}
}
