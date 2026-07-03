package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAccountUnlistedFlag proves the §16 behavior end to end over HTTP:
// PATCH /auth/me toggles the flag (echoed on GET /auth/me), an unlisted
// owner's videos disappear from the public feed and search, while the direct
// channel video list and the video detail URL keep serving — and listing back
// restores discovery.
func TestAccountUnlistedFlag(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"findable","privacy":"public"}`)

	feedCount := func() int {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos", nil))
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return len(body.Videos)
	}
	searchCount := func() int {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/search?q=findable", nil))
		var body videoSearchResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return len(body.Videos)
	}

	if feedCount() != 1 || searchCount() != 1 {
		t.Fatalf("discovery before unlisting: feed=%d search=%d, want 1/1", feedCount(), searchCount())
	}

	// Toggle unlisted on; the response and GET /auth/me reflect it.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"unlisted":true}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch unlisted = %d; body=%s", rec.Code, rec.Body.String())
	}
	var me userView
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if !me.Unlisted {
		t.Fatalf("patch response unlisted = false, want true")
	}
	meRec := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", tok)
	_ = json.Unmarshal(meRec.Body.Bytes(), &me)
	if !me.Unlisted {
		t.Fatalf("GET /auth/me unlisted = false, want true")
	}

	// Discovery excludes the unlisted owner's video…
	if feedCount() != 0 || searchCount() != 0 {
		t.Fatalf("discovery while unlisted: feed=%d search=%d, want 0/0", feedCount(), searchCount())
	}
	// …but direct URLs still serve: the channel's public video list and detail.
	listRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/channels/ada/videos", nil))
	var list videoListResponse
	_ = json.Unmarshal(listRec.Body.Bytes(), &list)
	if listRec.Code != http.StatusOK || len(list.Videos) != 1 {
		t.Fatalf("direct channel list while unlisted = %d/%d videos, want 200/1", listRec.Code, len(list.Videos))
	}
	if rec := getVideo(srv, id, ""); rec.Code != http.StatusOK {
		t.Fatalf("direct video detail while unlisted = %d, want 200", rec.Code)
	}

	// Toggling back restores discovery. An unrelated profile edit must not
	// touch the flag (COALESCE keeps it).
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"unlisted":false}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("patch unlisted=false = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"display_name":"Ada"}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("unrelated patch = %d", rec.Code)
	}
	_ = json.Unmarshal(sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", tok).Body.Bytes(), &me)
	if me.Unlisted {
		t.Fatalf("unlisted flipped by unrelated patch")
	}
	if feedCount() != 1 || searchCount() != 1 {
		t.Fatalf("discovery after relisting: feed=%d search=%d, want 1/1", feedCount(), searchCount())
	}
}
