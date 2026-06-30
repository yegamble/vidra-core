package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestBlockVideoModeration covers the moderator video block/unblock flow and the
// resulting visibility change on the public detail endpoint: a blocked video is
// hidden (404) from anonymous + regular viewers but still visible to a
// moderator/admin (so they can confirm before unblocking).
func TestBlockVideoModeration(t *testing.T) {
	srv := videoServer(t)
	// The first registered account ("ada") becomes admin.
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Before blocking, a public viewer sees the video.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+vid, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("detail before block = %d; body=%s", rec.Code, rec.Body.String())
	}

	// A regular user cannot block.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", `{"reason":"spam"}`, bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod block = %d, want 403", rec.Code)
	}

	// The admin blocks it; re-blocking is idempotent.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", `{"reason":"spam"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", `{"reason":"still spam"}`, admin); rec.Code != http.StatusNoContent {
		t.Errorf("re-block = %d, want 204", rec.Code)
	}

	// Anonymous + regular viewers now get 404; the admin (mod role) can still see it.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+vid, "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("anon detail after block = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+vid, "", bob); rec.Code != http.StatusNotFound {
		t.Errorf("user detail after block = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+vid, "", admin); rec.Code != http.StatusOK {
		t.Errorf("admin detail after block = %d, want 200 (mods can still view)", rec.Code)
	}

	// The admin unblocks it; it is visible to the public again. Unblocking again is idempotent.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/videos/"+vid+"/block", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+vid, "", ""); rec.Code != http.StatusOK {
		t.Errorf("anon detail after unblock = %d, want 200", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/videos/"+vid+"/block", "", admin); rec.Code != http.StatusNoContent {
		t.Errorf("idempotent unblock = %d, want 204", rec.Code)
	}
}

// TestBlockVideoNotFoundValidationAndAuth covers the unknown-video, over-length
// reason, and unauthenticated cases for the block endpoints.
func TestBlockVideoNotFoundValidationAndAuth(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	// Blocking an unknown video → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+uuid.New().String()+"/block", `{"reason":"x"}`, admin); rec.Code != http.StatusNotFound {
		t.Errorf("block unknown = %d, want 404", rec.Code)
	}

	// An over-length reason → 422 (the block request's own validation).
	tooLong := `{"reason":"` + strings.Repeat("a", maxReportReasonLen+1) + `"}`
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", tooLong, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("over-length reason = %d, want 422", rec.Code)
	}

	// Auth required on both routes.
	someID := uuid.New().String()
	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/admin/videos/" + someID + "/block", `{"reason":"x"}`},
		{http.MethodDelete, "/api/v1/admin/videos/" + someID + "/block", ""},
	}
	for _, tc := range cases {
		if rec := sendJSONAuth(srv, tc.method, tc.path, tc.body, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}
