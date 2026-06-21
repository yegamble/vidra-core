package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func listSaved(srv *Server, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodGet, "/api/v1/me/saved", "", token)
}

func TestSaveListUnsave(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	v1 := createPublishedVideo(t, srv, tok, "ada", `{"title":"first","privacy":"public"}`)
	v2 := createPublishedVideo(t, srv, tok, "ada", `{"title":"second","privacy":"public"}`)

	titles := func(rec *httptest.ResponseRecorder) []string {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		out := make([]string, 0, len(body.Videos))
		for _, v := range body.Videos {
			out = append(out, v.Title)
		}
		return out
	}

	// Empty to start.
	if got := titles(listSaved(srv, tok)); len(got) != 0 {
		t.Fatalf("initial saved = %v, want empty", got)
	}

	// Saving requires auth.
	if anon := postTo(srv, "/api/v1/videos/"+v1+"/save", ""); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon save = %d, want 401", anon.Code)
	}

	// Save both (v2 last → newest-saved first).
	for _, id := range []string{v1, v2} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/save", "", tok); rec.Code != http.StatusNoContent {
			t.Fatalf("save %s = %d; body=%s", id, rec.Code, rec.Body.String())
		}
	}
	// Saving again is idempotent (still 204).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+v2+"/save", "", tok); rec.Code != http.StatusNoContent {
		t.Errorf("re-save = %d, want 204", rec.Code)
	}

	if got := titles(listSaved(srv, tok)); len(got) != 2 || got[0] != "second" {
		t.Fatalf("saved after save = %v, want [second first]", got)
	}

	// Unsave one.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+v2+"/save", "", tok); rec.Code != http.StatusNoContent {
		t.Errorf("unsave = %d, want 204", rec.Code)
	}
	if got := titles(listSaved(srv, tok)); len(got) != 1 || got[0] != "first" {
		t.Errorf("saved after unsave = %v, want [first]", got)
	}
}

func TestSaveNonPublicVideoIs404(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createVideo(t, srv, tok, "ada", `{"title":"draft","privacy":"private"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/save", "", tok); rec.Code != http.StatusNotFound {
		t.Errorf("save non-public video = %d, want 404", rec.Code)
	}
}

func TestSavedListRequiresAuth(t *testing.T) {
	srv := videoServer(t)
	if rec := listSaved(srv, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon saved list = %d, want 401", rec.Code)
	}
}
