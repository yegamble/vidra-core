package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A view of a remote video is a fire-and-forget ping like the local one: 204
// for anyone who can see the video (whether or not a View is federated), 404
// for an unknown one, and still 204 with federation off (nothing is sent).
func TestRecordRemoteVideoViewEndpoint(t *testing.T) {
	for _, fed := range []bool{true, false} {
		srv, tok, ratingPath := rvRatingServer(t, fed)
		path := strings.TrimSuffix(ratingPath, "/rating") + "/view"
		for _, token := range []string{"", tok} {
			if rec := sendJSONAuth(srv, http.MethodPost, path, "", token); rec.Code != http.StatusNoContent {
				t.Fatalf("federation=%v authed=%v: POST = %d, want 204; body=%s", fed, token != "", rec.Code, rec.Body.String())
			}
		}
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/remote-videos/"+uuid.NewString()+"/view", "", ""); rec.Code != http.StatusNotFound {
			t.Fatalf("federation=%v: unknown video = %d, want 404", fed, rec.Code)
		}
	}
}

// Each accepted ping can queue a delivery to another instance, so the route
// carries its own per-IP budget on top of the API-wide one.
func TestRecordRemoteVideoViewPerIPLimit(t *testing.T) {
	srv, _, ratingPath := rvRatingServer(t, true)
	path := strings.TrimSuffix(ratingPath, "/rating") + "/view"
	for i := 0; i < defaultRemoteViewRateLimit; i++ {
		if rec := sendJSONAuth(srv, http.MethodPost, path, "", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("ping %d = %d, want 204", i, rec.Code)
		}
	}
	if rec := sendJSONAuth(srv, http.MethodPost, path, "", ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("ping past the budget = %d, want 429", rec.Code)
	}
}
