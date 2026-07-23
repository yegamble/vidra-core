package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func getProgress(srv *Server, id, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/watch-progress", "", token)
}

func putProgress(srv *Server, id, body, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+id+"/watch-progress", body, token)
}

func listHistory(srv *Server, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodGet, "/api/v1/me/history", "", token)
}

func listHistoryProgress(srv *Server, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodGet, "/api/v1/me/history?progress=in_progress", "", token)
}

// TestWatchProgressAndHistoryRoundTrip drives the full feature: record progress
// on two videos, read one back, and confirm history lists them most-recently
// watched first carrying the saved resume position.
func TestWatchProgressAndHistoryRoundTrip(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	v1 := createPublishedVideo(t, srv, tok, "ada", `{"title":"first","privacy":"public"}`)
	v2 := createPublishedVideo(t, srv, tok, "ada", `{"title":"second","privacy":"public"}`)

	// Empty history to start.
	if got := historyTitles(t, listHistory(srv, tok)); len(got) != 0 {
		t.Fatalf("initial history = %v, want empty", got)
	}

	// Record progress on v1, then v2 (v2 last → newest-watched first).
	if rec := putProgress(srv, v1, `{"position_seconds":42}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("put progress v1 = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := putProgress(srv, v2, `{"position_seconds":7}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("put progress v2 = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Read v1's resume position back.
	rec := getProgress(srv, v1, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("get progress = %d; body=%s", rec.Code, rec.Body.String())
	}
	var wp watchProgressView
	_ = json.Unmarshal(rec.Body.Bytes(), &wp)
	if wp.VideoID != v1 || wp.PositionSeconds != 42 {
		t.Fatalf("get progress = %+v, want video=%s position=42", wp, v1)
	}

	// History lists both, newest-watched first, with positions.
	var body historyListResponse
	histRec := listHistory(srv, tok)
	if histRec.Code != http.StatusOK {
		t.Fatalf("list history = %d; body=%s", histRec.Code, histRec.Body.String())
	}
	_ = json.Unmarshal(histRec.Body.Bytes(), &body)
	if len(body.Videos) != 2 {
		t.Fatalf("history len = %d, want 2; body=%s", len(body.Videos), histRec.Body.String())
	}
	if body.Videos[0].Title != "second" || body.Videos[0].PositionSeconds != 7 {
		t.Errorf("history[0] = %q pos=%d, want second pos=7", body.Videos[0].Title, body.Videos[0].PositionSeconds)
	}
	if body.Videos[1].Title != "first" || body.Videos[1].PositionSeconds != 42 {
		t.Errorf("history[1] = %q pos=%d, want first pos=42", body.Videos[1].Title, body.Videos[1].PositionSeconds)
	}
	// Card data (channel link) rides along.
	if body.Videos[0].ChannelHandle == nil || *body.Videos[0].ChannelHandle != "ada" {
		t.Errorf("history card missing channel_handle: %+v", body.Videos[0])
	}

	// Updating progress on v1 re-bumps it to the top and overwrites the position.
	if rec := putProgress(srv, v1, `{"position_seconds":99}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("re-put progress v1 = %d", rec.Code)
	}
	body = historyListResponse{}
	_ = json.Unmarshal(listHistory(srv, tok).Body.Bytes(), &body)
	if len(body.Videos) != 2 || body.Videos[0].Title != "first" || body.Videos[0].PositionSeconds != 99 {
		t.Errorf("after re-watch, history[0] = %q pos=%d, want first pos=99", body.Videos[0].Title, body.Videos[0].PositionSeconds)
	}
}

// TestWatchHistoryInProgressFilter drives the ?progress=in_progress filter
// (home-featured-banner A3) end-to-end: a half-watched and an unprobed
// (NULL-duration) video stay in the "Continue watching" list, while an
// effectively-finished (>= 95%) one is excluded. The unfiltered list still
// returns everything.
func TestWatchHistoryInProgressFilter(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	halfway := createPublishedVideo(t, srv, tok, "ada", `{"title":"halfway","privacy":"public"}`)
	finished := createPublishedVideo(t, srv, tok, "ada", `{"title":"finished","privacy":"public"}`)
	nodur := createPublishedVideo(t, srv, tok, "ada", `{"title":"nodur","privacy":"public"}`)

	// Seed probed durations for the two videos that have a known length; nodur
	// stays unprobed (LEFT JOIN video_metadata -> NULL duration).
	d100 := int32(100)
	for _, id := range []string{halfway, finished} {
		if _, err := repo.UpsertVideoMetadata(context.Background(), sqlcgen.UpsertVideoMetadataParams{
			VideoID: uuid.MustParse(id), DurationSeconds: &d100,
		}); err != nil {
			t.Fatalf("UpsertVideoMetadata(%s): %v", id, err)
		}
	}

	if rec := putProgress(srv, halfway, `{"position_seconds":50}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("put halfway = %d", rec.Code)
	}
	if rec := putProgress(srv, finished, `{"position_seconds":96}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("put finished = %d", rec.Code)
	}
	if rec := putProgress(srv, nodur, `{"position_seconds":30}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("put nodur = %d", rec.Code)
	}

	// Unfiltered: all three present.
	if got := historyTitles(t, listHistory(srv, tok)); len(got) != 3 {
		t.Fatalf("unfiltered history = %v, want 3 entries", got)
	}

	// Filtered: finished (96%) is dropped; halfway + nodur remain.
	got := historyTitles(t, listHistoryProgress(srv, tok))
	present := map[string]bool{}
	for _, tt := range got {
		present[tt] = true
	}
	if len(got) != 2 || !present["halfway"] || !present["nodur"] || present["finished"] {
		t.Errorf("in_progress history = %v, want [halfway, nodur] (no finished)", got)
	}
}

// TestWatchHistoryDeleteAndClear covers removing a single entry and clearing all.
func TestWatchHistoryDeleteAndClear(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	v1 := createPublishedVideo(t, srv, tok, "ada", `{"title":"first","privacy":"public"}`)
	v2 := createPublishedVideo(t, srv, tok, "ada", `{"title":"second","privacy":"public"}`)
	_ = putProgress(srv, v1, `{"position_seconds":1}`, tok)
	_ = putProgress(srv, v2, `{"position_seconds":2}`, tok)

	// Delete one entry (idempotent).
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/history/"+v2, "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("delete entry = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/history/"+v2, "", tok); rec.Code != http.StatusNoContent {
		t.Errorf("delete entry again = %d, want 204 (idempotent)", rec.Code)
	}
	if got := historyTitles(t, listHistory(srv, tok)); len(got) != 1 || got[0] != "first" {
		t.Fatalf("after delete entry = %v, want [first]", got)
	}

	// Clear all (idempotent).
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/history", "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("clear history = %d", rec.Code)
	}
	if got := historyTitles(t, listHistory(srv, tok)); len(got) != 0 {
		t.Errorf("after clear = %v, want empty", got)
	}
}

func TestWatchProgressNonPublicVideoIs404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createVideo(t, srv, tok, "ada", `{"title":"draft","privacy":"private"}`)

	if rec := putProgress(srv, vid, `{"position_seconds":1}`, tok); rec.Code != http.StatusNotFound {
		t.Errorf("put progress on private video = %d, want 404", rec.Code)
	}
	if rec := getProgress(srv, vid, tok); rec.Code != http.StatusNotFound {
		t.Errorf("get progress on private video = %d, want 404", rec.Code)
	}
}

func TestWatchProgressValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"v","privacy":"public"}`)

	if rec := putProgress(srv, vid, `{"position_seconds":-5}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("negative position = %d, want 422", rec.Code)
	}
}

func TestWatchHistoryRequiresAuth(t *testing.T) {
	srv := videoServer(t)
	someID := "00000000-0000-0000-0000-000000000000"
	cases := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/me/history"},
		{http.MethodDelete, "/api/v1/me/history"},
		{http.MethodDelete, "/api/v1/me/history/" + someID},
		{http.MethodGet, "/api/v1/videos/" + someID + "/watch-progress"},
		{http.MethodPut, "/api/v1/videos/" + someID + "/watch-progress"},
	}
	for _, tc := range cases {
		if rec := sendJSONAuth(srv, tc.method, tc.path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// historyTitles extracts the titles from a history list response, failing on a
// non-200.
func historyTitles(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list history = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body historyListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	out := make([]string, 0, len(body.Videos))
	for _, v := range body.Videos {
		out = append(out, v.Title)
	}
	return out
}
