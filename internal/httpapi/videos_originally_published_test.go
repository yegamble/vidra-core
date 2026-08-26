package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// originally_published_at is the date a video was first published SOMEWHERE
// ELSE (migration 0119). These tests pin the two halves of its HTTP contract:
// PATCH accepts it, and the detail GET gives it back.

// TestOriginallyPublishedAtRoundTrips proves the write and the read agree. It
// PATCHes a date onto a published video deliberately — unlike publish_at, this
// field schedules nothing and describes the past, so being published must not
// refuse it.
func TestOriginallyPublishedAtRoundTrips(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Talk","privacy":"public"}`)

	// A video that was first published here carries no such date at all, and the
	// field is omitted rather than sent as null.
	var before videoView
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id, "", "").Body.Bytes(), &before)
	if before.OriginallyPublishedAt != nil {
		t.Fatalf("originally_published_at before any write = %v, want unset", before.OriginallyPublishedAt)
	}

	want := time.Date(2016, 4, 1, 12, 30, 0, 0, time.UTC)
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id,
		`{"originally_published_at":"`+want.Format(time.RFC3339)+`"}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch originally_published_at = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var patched videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.OriginallyPublishedAt == nil || !patched.OriginallyPublishedAt.Equal(want) {
		t.Fatalf("patch response originally_published_at = %v, want %v", patched.OriginallyPublishedAt, want)
	}

	var detail videoView
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id, "", "").Body.Bytes(), &detail)
	if detail.OriginallyPublishedAt == nil || !detail.OriginallyPublishedAt.Equal(want) {
		t.Fatalf("detail originally_published_at = %v, want %v", detail.OriginallyPublishedAt, want)
	}
	// It is its own field: setting it must not have invented a schedule.
	if detail.PublishAt != nil {
		t.Errorf("publish_at = %v, want unset (originally_published_at schedules nothing)", detail.PublishAt)
	}
}

// TestOriginallyPublishedAtIsAnUpdatableField proves the field satisfies the
// "at least one updatable field" guard on its own — without this the only way
// to set it would be to send a second, unrelated edit alongside it.
func TestOriginallyPublishedAtIsAnUpdatableField(t *testing.T) {
	if fes := (updateVideoRequest{}).Validate(); len(fes) == 0 {
		t.Fatal("an empty update body was accepted; the guard is not running")
	}
	when := time.Date(2016, 4, 1, 12, 30, 0, 0, time.UTC)
	if fes := (updateVideoRequest{OriginallyPublishedAt: &when}).Validate(); len(fes) != 0 {
		t.Fatalf("originally_published_at alone = %v, want accepted", fes)
	}
	// A past date is the NORMAL case here (publish_at rejects one), so nothing
	// may range-check it.
	past := time.Now().Add(-20 * 365 * 24 * time.Hour)
	if fes := (updateVideoRequest{OriginallyPublishedAt: &past}).Validate(); len(fes) != 0 {
		t.Fatalf("a date twenty years ago = %v, want accepted", fes)
	}
}
