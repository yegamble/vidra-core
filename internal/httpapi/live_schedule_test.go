package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func rfc(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }

// TestLiveScheduleValidation: a planned start must be in the future and within
// the one-year horizon, on create and on edit; unscheduled is always fine.
func TestLiveScheduleValidation(t *testing.T) {
	srv := videoServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	for name, at := range map[string]string{
		"past":        rfc(-time.Hour),
		"past by 1s":  rfc(-time.Second),
		"beyond 1 yr": rfc(400 * 24 * time.Hour),
		"far future":  "2999-01-01T00:00:00Z",
	} {
		rec := createLiveStream(srv, "ada", `{"title":"x","scheduled_start_at":"`+at+`"}`, ada)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "scheduled_start_at") {
			t.Errorf("create with %s start = %d (%s), want 422 on scheduled_start_at", name, rec.Code, rec.Body.String())
		}
	}
	if rec := createLiveStream(srv, "ada", `{"title":"x","scheduled_start_at":"not-a-date"}`, ada); rec.Code != http.StatusBadRequest {
		t.Errorf("garbage start = %d, want 400", rec.Code)
	}

	var created createLiveStreamResponse
	rec := createLiveStream(srv, "ada", `{"title":"ok","scheduled_start_at":"`+rfc(48*time.Hour)+`"}`, ada)
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid create = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.LiveStream.ScheduledStartAt == nil || created.LiveStream.State != "offline" {
		t.Fatalf("created = %+v, want a scheduled, still-offline stream", created.LiveStream)
	}
	id := created.LiveStream.ID

	// Edit: a past time is refused; the unchanged stored time is accepted even
	// if it were stale (echoed back by a full-replace body); omitting clears it.
	if r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+id, `{"title":"ok","scheduled_start_at":"`+rfc(-time.Minute)+`"}`, ada); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("edit to a past start = %d, want 422", r.Code)
	}
	echo := created.LiveStream.ScheduledStartAt.UTC().Format(time.RFC3339Nano)
	if r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+id, `{"title":"renamed","scheduled_start_at":"`+echo+`"}`, ada); r.Code != http.StatusOK {
		t.Errorf("edit echoing the stored start = %d, want 200; body=%s", r.Code, r.Body.String())
	}
	var cleared liveStreamView
	r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+id, `{"title":"renamed","scheduled_start_at":null}`, ada)
	_ = json.Unmarshal(r.Body.Bytes(), &cleared)
	if r.Code != http.StatusOK || cleared.ScheduledStartAt != nil || strings.Contains(r.Body.String(), "scheduled_start_at") {
		t.Errorf("clearing = %d %s, want 200 with the field absent", r.Code, r.Body.String())
	}
}

// TestLiveScheduleOwnerOnlyAndVisibility: only a channel manager edits the
// schedule (a stranger gets 404), the planned start rides the same visibility as
// the stream (private hidden), and the Upcoming list is public+offline only.
func TestLiveScheduleOwnerOnlyAndVisibility(t *testing.T) {
	srv := videoServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	at := rfc(24 * time.Hour)

	mk := func(title, privacy string) liveStreamView {
		var out createLiveStreamResponse
		rec := createLiveStream(srv, "ada", `{"title":"`+title+`","privacy":"`+privacy+`","scheduled_start_at":"`+at+`"}`, ada)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d; body=%s", title, rec.Code, rec.Body.String())
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.LiveStream
	}
	pub, unl, priv := mk("Pub", "public"), mk("Unl", "unlisted"), mk("Priv", "private")

	// A stranger cannot create a schedule on, or edit the schedule of, a stream.
	if r := createLiveStream(srv, "ada", `{"title":"x","scheduled_start_at":"`+at+`"}`, bob); r.Code != http.StatusForbidden {
		t.Errorf("stranger create = %d, want 403", r.Code)
	}
	if r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+pub.ID, `{"title":"Pub","scheduled_start_at":null}`, bob); r.Code != http.StatusNotFound {
		t.Errorf("stranger clear = %d, want 404", r.Code)
	}
	if g := getWithAuth(srv, "/api/v1/live/"+pub.ID, ""); !strings.Contains(g.Body.String(), `"scheduled_start_at"`) {
		t.Errorf("anonymous read of a public stream lacks the planned start: %s", g.Body.String())
	}
	if g := getWithAuth(srv, "/api/v1/live/"+priv.ID, bob); g.Code != http.StatusNotFound {
		t.Errorf("stranger read of a private stream = %d, want 404 (no schedule leak)", g.Code)
	}
	if g := getWithAuth(srv, "/api/v1/live/"+priv.ID, ada); !strings.Contains(g.Body.String(), `"scheduled_start_at"`) {
		t.Errorf("owner read of a private stream lacks the planned start")
	}

	// Upcoming lists the public one only, for anonymous and authenticated alike.
	for _, tok := range []string{"", bob} {
		rec := getWithAuth(srv, "/api/v1/live/upcoming", tok)
		var body liveUpcomingListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusOK || len(body.LiveStreams) != 1 || body.LiveStreams[0].ID != pub.ID || body.Total != 1 {
			t.Fatalf("upcoming = %d %s, want exactly the public stream", rec.Code, rec.Body.String())
		}
	}
	_ = unl
	// Channel filter.
	if rec := getWithAuth(srv, "/api/v1/live/upcoming?channel=nobody", ""); strings.Contains(rec.Body.String(), pub.ID) {
		t.Errorf("channel filter leaked another channel's stream: %s", rec.Body.String())
	}
	if rec := getWithAuth(srv, "/api/v1/live/upcoming?channel=ada", ""); !strings.Contains(rec.Body.String(), pub.ID) {
		t.Errorf("channel filter dropped the channel's own stream: %s", rec.Body.String())
	}
}

// TestLiveScheduleNeverAutoStartsAndGoLiveConsumesIt: a planned start in the
// (stale) past changes nothing about state, and the real go-live clears it so a
// reused permanent stream stops announcing a start that already happened.
func TestLiveScheduleNeverAutoStartsAndGoLiveConsumesIt(t *testing.T) {
	cfg := testConfig()
	cfg.LiveIngestSecret = "s3cret"
	srv := videoServerCfg(t, cfg)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	var out createLiveStreamResponse
	_ = json.Unmarshal(createLiveStream(srv, "ada", `{"title":"Show","scheduled_start_at":"`+rfc(time.Hour)+`"}`, ada).Body.Bytes(), &out)
	id := out.LiveStream.ID
	if v := readLive(t, srv, id, ada); v.State != "offline" || v.ScheduledStartAt == nil {
		t.Fatalf("before go-live = %+v, want offline + scheduled", v)
	}
	if r := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+out.StreamKey+`"}`, "s3cret"); r.Code != http.StatusOK {
		t.Fatalf("ingest start = %d", r.Code)
	}
	if v := readLive(t, srv, id, ada); v.State != "live" || v.ScheduledStartAt != nil {
		t.Fatalf("after go-live = %+v, want live with the schedule consumed", v)
	}
	// Cannot announce a stream that is already on air.
	if r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+id, `{"title":"Show","scheduled_start_at":"`+rfc(time.Hour)+`"}`, ada); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("scheduling a live stream = %d, want 422", r.Code)
	}
}

func readLive(t *testing.T, srv *Server, id, tok string) liveStreamView {
	t.Helper()
	var v liveStreamView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live/"+id, tok).Body.Bytes(), &v)
	return v
}

// TestLiveUpcomingFollowsTheLiveToggle: with live switched off the public list
// is empty (200), never a rail of announcements nobody can keep.
func TestLiveUpcomingFollowsTheLiveToggle(t *testing.T) {
	cfg := testConfig()
	cfg.LiveEnabled = false
	srv := videoServerCfg(t, cfg)
	rec := getWithAuth(srv, "/api/v1/live/upcoming", "")
	var body liveUpcomingListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || len(body.LiveStreams) != 0 || body.Total != 0 {
		t.Fatalf("upcoming with live off = %d %s, want an empty 200", rec.Code, rec.Body.String())
	}
}
