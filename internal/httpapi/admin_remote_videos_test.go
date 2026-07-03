package httpapi

// Remote-video moderation handler tests (remote-content §8): local reports of
// federated remote videos and the admin per-video hide mirroring video_blocks.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/remotevideo"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// remoteModServer builds a server with auth + moderation + the remote-video
// read side, seeded with one ingested remote video.
func remoteModServer(t *testing.T) (*Server, uuid.UUID) {
	t.Helper()
	authRepo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(authRepo, issuer, 720*time.Hour)

	id := uuid.New()
	rvRepo := remoteVideoFakeRepo{id: {
		ID:             id,
		ObjectUrl:      "https://remote.example/videos/watch/x",
		RemoteActorUrl: "https://remote.example/video-channels/movies",
		Domain:         "remote.example",
		Title:          "Remote clip",
		WatchUrl:       "https://remote.example/w/x",
	}}
	modRepo := &moderationFakeRepo{auth: authRepo, remoteVideos: rvRepo}
	srv := New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithModerationService(moderation.NewService(modRepo)),
		WithRemoteVideoService(remotevideo.NewService(rvRepo, nil)),
	)
	return srv, id
}

func TestReportRemoteVideo(t *testing.T) {
	srv, id := remoteModServer(t)
	// First registered account is admin; second is a regular reporter.
	_ = registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Reporting requires auth.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/remote-videos/"+id.String()+"/report", `{"reason":"spam"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon report = %d, want 401", rec.Code)
	}
	// An unknown remote video is 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/remote-videos/"+uuid.NewString()+"/report", `{"reason":"spam"}`, bob); rec.Code != http.StatusNotFound {
		t.Errorf("unknown target report = %d, want 404", rec.Code)
	}
	// A blank reason is 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/remote-videos/"+id.String()+"/report", `{"reason":"  "}`, bob); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank reason = %d, want 422", rec.Code)
	}
	// Filing (and idempotent re-filing) succeeds.
	for i := 0; i < 2; i++ {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/remote-videos/"+id.String()+"/report", `{"reason":"illegal upload"}`, bob); rec.Code != http.StatusNoContent {
			t.Fatalf("report (round %d) = %d; body=%s", i, rec.Code, rec.Body.String())
		}
	}
}

func TestBlockRemoteVideoModeration(t *testing.T) {
	srv, id := remoteModServer(t)
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	blockPath := "/api/v1/admin/remote-videos/" + id.String() + "/block"

	// Before blocking, the remote-watch detail serves it.
	if rec := getWithAuth(srv, "/api/v1/remote-videos/"+id.String(), ""); rec.Code != http.StatusOK {
		t.Fatalf("detail before block = %d; body=%s", rec.Code, rec.Body.String())
	}
	// A regular user cannot block, list, or unblock.
	if rec := sendJSONAuth(srv, http.MethodPost, blockPath, `{"reason":"spam"}`, bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod block = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/remote-videos/blocked", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod list = %d, want 403", rec.Code)
	}
	// An unknown remote video is 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/remote-videos/"+uuid.NewString()+"/block", `{"reason":"x"}`, admin); rec.Code != http.StatusNotFound {
		t.Errorf("block unknown = %d, want 404", rec.Code)
	}
	// The admin blocks it; re-blocking is idempotent.
	if rec := sendJSONAuth(srv, http.MethodPost, blockPath, `{"reason":"illegal"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, blockPath, `{"reason":"still illegal"}`, admin); rec.Code != http.StatusNoContent {
		t.Errorf("re-block = %d, want 204", rec.Code)
	}

	// The block-list carries the row with its origin identity.
	rec := getWithAuth(srv, "/api/v1/admin/remote-videos/blocked", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("blocked list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var list blockedRemoteVideoListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Videos) != 1 {
		t.Fatalf("blocked rows = %d, want 1", len(list.Videos))
	}
	row := list.Videos[0]
	if row.RemoteVideoID != id.String() || row.Title != "Remote clip" || row.Domain != "remote.example" {
		t.Errorf("blocked row = %+v", row)
	}
	if row.Reason != "still illegal" || row.BlockedBy != "ada" {
		t.Errorf("blocked row audit fields = %+v", row)
	}

	// Unblocking empties the list; unblocking again is idempotent.
	if rec := sendJSONAuth(srv, http.MethodDelete, blockPath, "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, blockPath, "", admin); rec.Code != http.StatusNoContent {
		t.Errorf("idempotent unblock = %d, want 204", rec.Code)
	}
	rec = getWithAuth(srv, "/api/v1/admin/remote-videos/blocked", admin)
	list = blockedRemoteVideoListResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Videos) != 0 {
		t.Errorf("blocked rows after unblock = %d, want 0", len(list.Videos))
	}
}

// TestRemoteCommentViewShape locks the §6 comment projection: a remote-authored
// comment carries remote=true + the origin domain and a null author_id.
func TestRemoteCommentViewShape(t *testing.T) {
	actor := "https://remote.example/accounts/bob"
	name := "bob"
	c := sqlcgen.Comment{
		ID: uuid.New(), VideoID: uuid.New(), Body: "hello from afar",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		RemoteActorUrl: &actor, RemoteAuthorName: &name,
	}
	v := newCommentView(c, "bob", "bob")
	v.AuthorDomain = "remote.example"
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	if decoded["remote"] != true {
		t.Errorf("remote = %v, want true", decoded["remote"])
	}
	if decoded["author_id"] != nil {
		t.Errorf("author_id = %v, want null", decoded["author_id"])
	}
	if decoded["author_domain"] != "remote.example" {
		t.Errorf("author_domain = %v", decoded["author_domain"])
	}
}
