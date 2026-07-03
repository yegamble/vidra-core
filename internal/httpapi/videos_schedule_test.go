package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestScheduledPublishFlow drives the whole §17 flow over HTTP: create with a
// future publish_at, upload → the video parks in 'scheduled' (off the public
// feed, badge-able in the studio list), then the sweeper (PublishDue) comes due
// and publishes it onto the feed.
func TestScheduledPublishFlow(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"premiere","privacy":"public","publish_at":"`+future+`"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.PublishAt == nil {
		t.Fatalf("create response missing publish_at: %s", rec.Body.String())
	}

	// Upload finalizes into 'scheduled', not 'published'.
	up := uploadVideoFile(srv, created.ID, "clip.mp4", "video/mp4", "tiny", tok)
	if up.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", up.Code, up.Body.String())
	}
	var uploaded uploadVideoFileResponse
	_ = json.Unmarshal(up.Body.Bytes(), &uploaded)
	if uploaded.Video.State != "scheduled" {
		t.Fatalf("state after upload = %q, want scheduled", uploaded.Video.State)
	}

	feedTitles := func() []string {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos", nil))
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		out := make([]string, 0, len(body.Videos))
		for _, v := range body.Videos {
			out = append(out, v.Title)
		}
		return out
	}
	if got := feedTitles(); len(got) != 0 {
		t.Fatalf("public feed before due = %v, want empty (scheduled is not published)", got)
	}

	// Owner detail + studio list expose publish_at and the scheduled state.
	var detail videoView
	_ = json.Unmarshal(getVideo(srv, created.ID, tok).Body.Bytes(), &detail)
	if detail.State != "scheduled" || detail.PublishAt == nil {
		t.Fatalf("detail = state %q publish_at %v, want scheduled + set", detail.State, detail.PublishAt)
	}
	listRec := sendJSONAuth(srv, http.MethodGet, "/api/v1/channels/ada/videos", "", tok)
	var list videoListResponse
	_ = json.Unmarshal(listRec.Body.Bytes(), &list)
	if len(list.Videos) != 1 || list.Videos[0].PublishAt == nil || list.Videos[0].State != "scheduled" {
		t.Fatalf("studio list = %+v, want one scheduled video with publish_at", list.Videos)
	}

	// Not due yet: the sweeper is a no-op.
	if n, err := srv.videosvc.PublishDue(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("PublishDue before due = (%d, %v), want (0, nil)", n, err)
	}

	// Rewind the schedule so it is due, then sweep.
	id := uuid.MustParse(created.ID)
	row := repo.videos[id]
	row.PublishAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}
	repo.videos[id] = row
	if n, err := srv.videosvc.PublishDue(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("PublishDue when due = (%d, %v), want (1, nil)", n, err)
	}

	_ = json.Unmarshal(getVideo(srv, created.ID, "").Body.Bytes(), &detail)
	if detail.State != "published" {
		t.Fatalf("state after sweep = %q, want published", detail.State)
	}
	if got := feedTitles(); len(got) != 1 || got[0] != "premiere" {
		t.Fatalf("public feed after sweep = %v, want [premiere]", got)
	}
}

// TestScheduledPublishValidation proves the §17 422s: a past publish_at on
// create or update, and setting publish_at once the video is published.
func TestScheduledPublishValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"x","publish_at":"`+past+`"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("past publish_at create = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}

	// A draft accepts a future schedule via PATCH.
	id := createVideo(t, srv, tok, "ada", `{"title":"draft"}`)
	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id,
		`{"publish_at":"`+future+`"}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("future publish_at patch = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id,
		`{"publish_at":"`+past+`"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("past publish_at patch = %d, want 422", rec.Code)
	}

	// Once published, scheduling is refused.
	pub := createPublishedVideo(t, srv, tok, "ada", `{"title":"live","privacy":"public"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+pub,
		`{"publish_at":"`+future+`"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publish_at on published = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}
