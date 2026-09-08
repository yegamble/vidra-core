package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// liveOnAir creates a channel + a live stream and puts it on air through the
// ingest hook, returning the owner's token, the stream id and its key.
func liveOnAir(t *testing.T, srv *Server, handle, username string) (token, id, key string) {
	t.Helper()
	token = createChannelFor(t, srv, handle, username+"@example.test", username)
	rec := createLiveStream(srv, handle, `{"title":"Broadcast"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create live = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created createLiveStreamResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if r := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+created.StreamKey+`"}`, "s3cret"); r.Code != http.StatusOK {
		t.Fatalf("ingest start = %d; body=%s", r.Code, r.Body.String())
	}
	return token, created.LiveStream.ID, created.StreamKey
}

func liveServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.LiveIngestSecret = "s3cret"
	return videoServerCfg(t, cfg)
}

func getLiveStream(t *testing.T, srv *Server, id, token string) liveStreamView {
	t.Helper()
	rec := getWithAuth(srv, "/api/v1/live/"+id, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get live = %d; body=%s", rec.Code, rec.Body.String())
	}
	var out liveStreamView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestTerminateLiveStreamAuthorizationMatrix: the wrong actors, in the order
// A26's own procedure names them. The FIRST registered user is the instance
// admin, so "ada" is staff and "bob" is an ordinary user.
func TestTerminateLiveStreamAuthorizationMatrix(t *testing.T) {
	srv := liveServer(t)
	_, id, _ := liveOnAir(t, srv, "ada", "ada")
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	body := `{"reason_code":"policy_violation"}`

	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/live/"+id+"/terminate", body, ""); r.Code != http.StatusUnauthorized {
		t.Errorf("anonymous terminate = %d, want 401", r.Code)
	}
	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/live/"+id+"/terminate", body, bob); r.Code != http.StatusForbidden {
		t.Errorf("ordinary user terminate = %d, want 403", r.Code)
	}
	// And nothing happened to the broadcast.
	if got := getLiveStream(t, srv, id, ""); got.State != "live" {
		t.Errorf("state = %q after two refused terminations, want live", got.State)
	}
}

// TestTerminateLiveStreamAsModerator is the happy path through HTTP: the
// broadcast ends, the creator can read the reason, and the response is honest
// about the publisher's socket on an instance with no control surface.
func TestTerminateLiveStreamAsModerator(t *testing.T) {
	srv := liveServer(t)
	admin, id, key := liveOnAir(t, srv, "ada", "ada")

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/live/"+id+"/terminate",
		`{"reason_code":"harassment","reason":"targeting a viewer in chat"}`, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("terminate = %d; body=%s", rec.Code, rec.Body.String())
	}
	var res terminateLiveResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.State != "ended" {
		t.Errorf("state = %q, want ended", res.State)
	}
	if !res.StreamKeyRotated {
		t.Error("the stream key was not rotated; the publisher reconnects and goes back on air")
	}
	if res.PublisherDisconnected {
		t.Error("this test server has no ingest control surface, so a claimed disconnect is a lie")
	}
	if res.Detail == "" {
		t.Error("a partial outcome was reported without saying what survived")
	}

	// The rotated key no longer authenticates a publish.
	if r := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+key+`"}`, "s3cret"); r.Code != http.StatusNotFound {
		t.Errorf("republish with the terminated key = %d, want 404 — rotation is what keeps the publisher off", r.Code)
	}

	// The creator is told why.
	got := getLiveStream(t, srv, id, admin)
	if got.Termination == nil {
		t.Fatal("the creator cannot see that their stream was terminated")
	}
	if !got.Termination.ByModerator || got.Termination.ReasonCode != "harassment" {
		t.Errorf("termination = %+v, want a moderator harassment termination", got.Termination)
	}
	if got.Termination.Reason != "targeting a viewer in chat" {
		t.Errorf("reason = %q, want the moderator's own words", got.Termination.Reason)
	}
}

// TestTerminationReasonIsNotPublic: the moderator's words are written for the
// creator, not for an audience. A takedown notice on a public page is a
// punishment nobody ruled on.
func TestTerminationReasonIsNotPublic(t *testing.T) {
	srv := liveServer(t)
	admin, id, _ := liveOnAir(t, srv, "ada", "ada")
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/live/"+id+"/terminate",
		`{"reason_code":"copyright","reason":"studio takedown 4471"}`, admin); r.Code != http.StatusOK {
		t.Fatalf("terminate = %d; body=%s", r.Code, r.Body.String())
	}

	for _, viewer := range []struct{ name, token string }{{"anonymous", ""}, {"a stranger", bob}} {
		rec := getWithAuth(srv, "/api/v1/live/"+id, viewer.token)
		var out liveStreamView
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Termination != nil {
			t.Errorf("%s can read the termination block: %+v", viewer.name, out.Termination)
		}
		if body := rec.Body.String(); strings.Contains(body, "studio takedown 4471") {
			t.Errorf("%s can read the moderator's free text in the response body", viewer.name)
		}
	}
}

// TestTerminateRequiresAKnownReasonCode: the two 422s, and neither ends the
// broadcast.
func TestTerminateRequiresAKnownReasonCode(t *testing.T) {
	srv := liveServer(t)
	admin, id, _ := liveOnAir(t, srv, "ada", "ada")

	for _, body := range []string{`{}`, `{"reason_code":""}`, `{"reason_code":"vibes"}`} {
		rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/live/"+id+"/terminate", body, admin)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("terminate %s = %d, want 422; body=%s", body, rec.Code, rec.Body.String())
		}
	}
	if got := getLiveStream(t, srv, id, admin); got.State != "live" {
		t.Errorf("state = %q after three rejected bodies, want live", got.State)
	}
}

// TestTerminateAnOfflineStreamIs409: the stream is real and already off, which
// is the outcome the caller wanted. A 404 would send a moderator hunting for a
// stream sitting right in front of them.
func TestTerminateAnOfflineStreamIs409(t *testing.T) {
	srv := liveServer(t)
	admin, id, _ := liveOnAir(t, srv, "ada", "ada")
	body := `{"reason_code":"spam"}`
	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/live/"+id+"/terminate", body, admin); r.Code != http.StatusOK {
		t.Fatalf("first terminate = %d", r.Code)
	}
	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/live/"+id+"/terminate", body, admin); r.Code != http.StatusConflict {
		t.Errorf("second terminate = %d, want 409", r.Code)
	}
}

// TestOwnerEndsOwnStream: the creator's control, which did not exist. A stranger
// gets 404 (not 403), matching every other owner-scoped live route, and the
// creator's own end is never rendered as a takedown.
func TestOwnerEndsOwnStream(t *testing.T) {
	srv := liveServer(t)
	ada, id, _ := liveOnAir(t, srv, "ada", "ada")
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/live/"+id+"/end", "", ""); r.Code != http.StatusUnauthorized {
		t.Errorf("anonymous end = %d, want 401", r.Code)
	}
	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/live/"+id+"/end", "", bob); r.Code != http.StatusNotFound {
		t.Errorf("stranger end = %d, want 404 (the owner-scoped convention, not 403)", r.Code)
	}
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/live/"+id+"/end", "", ada)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner end = %d; body=%s", rec.Code, rec.Body.String())
	}
	var res terminateLiveResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.State != "ended" || !res.StreamKeyRotated {
		t.Errorf("owner end result = %+v, want ended + rotated", res)
	}
	got := getLiveStream(t, srv, id, ada)
	if got.Termination == nil || got.Termination.ByModerator {
		t.Errorf("the creator's own end reads as %+v; it must not look like a moderation action", got.Termination)
	}
	if got.Termination.ReasonCode != "" || got.Termination.Reason != "" {
		t.Error("the creator's own end stored a reason")
	}
}

// TestLiveViewerCountAbsentWithoutRedis is the "absent, not zero" contract at the
// wire: the test server has no viewer counter, so the field must not appear at
// all rather than telling a creator mid-broadcast that nobody is watching.
func TestLiveViewerCountAbsentWithoutRedis(t *testing.T) {
	srv := liveServer(t)
	_, id, _ := liveOnAir(t, srv, "ada", "ada")
	rec := getWithAuth(srv, "/api/v1/live/"+id, "")
	if strings.Contains(rec.Body.String(), "viewer_count") {
		t.Errorf("viewer_count is present on an instance that cannot measure it: %s", rec.Body.String())
	}
	var out liveStreamView
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.ViewerCount != nil {
		t.Errorf("viewer_count = %d, want absent", *out.ViewerCount)
	}
}

// TestLiveIngestComponentNotConfiguredWithoutRTMP: an install that does no live
// is not a faulty one. This is the component's floor; the "down" verdict needs a
// prober and is unit-tested in system_live_test.go.
func TestLiveIngestComponentNotConfiguredWithoutRTMP(t *testing.T) {
	srv := liveServer(t)
	rec := getWithAuth(srv, "/readyz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz = %d; body=%s", rec.Code, rec.Body.String())
	}
	var out readinessResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	c, ok := out.Components["live_ingest"]
	if !ok {
		t.Fatalf("readyz has no live_ingest component: %s", rec.Body.String())
	}
	if c.Status != "not_configured" {
		t.Errorf("live_ingest = %q on an install with no LIVE_RTMP_URL, want not_configured", c.Status)
	}
	if out.Status != "ok" {
		t.Errorf("readiness = %q; an install that does no live is not degraded", out.Status)
	}
}
