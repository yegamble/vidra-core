package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// A29-F2 / A29-F9: the object ids vidra mints must be dereferenceable as
// ActivityPub, and a DELETED one must say so.

const apJSONAccept = "application/activity+json"

// apObjectServer builds a federation-enabled server holding one public video,
// one local comment on it, and one deleted video's tombstone.
func apObjectServer(t *testing.T) (*Server, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	cfg := fedTestConfig()
	srv, repo := fedServerRepo(cfg)
	videoID := uuid.New()
	commentID := uuid.New()
	deletedID := uuid.New()
	repo.videosByID[videoID] = sqlcgen.GetVideoByIDRow{
		ID: videoID, ChannelID: repo.channelID,
		Title: "Dawn", Description: "A quiet opening.",
		Privacy: "public", State: "published",
		CreatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC),
	}
	repo.commentsBy[commentID] = sqlcgen.Comment{
		ID: commentID, VideoID: videoID,
		UserID:    pgtype.UUID{Bytes: repo.userID, Valid: true},
		Body:      "Beautiful grade.",
		CreatedAt: time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC),
	}
	repo.tombstones[deletedID] = fedFakeDeletedAt
	return srv, videoID, commentID, deletedID
}

func TestVideoObjectIsDereferenceableAsActivityPub(t *testing.T) {
	srv, videoID, _, _ := apObjectServer(t)

	rec := getAccept(t, srv, "/videos/"+videoID.String(), apJSONAccept)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var obj map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if obj["type"] != "Video" {
		t.Errorf("type = %v, want Video", obj["type"])
	}
	if want := "https://videos.example/videos/" + videoID.String(); obj["id"] != want {
		t.Errorf("id = %v, want %q", obj["id"], want)
	}
	if obj["@context"] != "https://www.w3.org/ns/activitystreams" && obj["@context"] == nil {
		t.Error("the document carries no @context, so a strict consumer cannot interpret it")
	}
	if ct := rec.Header().Get("Content-Type"); ct != activityJSONContentType {
		t.Errorf("content-type = %q, want %q", ct, activityJSONContentType)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != apObjectCacheControl {
		t.Errorf("cache-control = %q, want %q", cc, apObjectCacheControl)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag; a peer re-checking this object cannot avoid re-downloading it")
	}

	// The validator is honoured: an unchanged object costs a 304, not a body.
	rec2 := getAcceptIfNoneMatch(t, srv, "/videos/"+videoID.String(), apJSONAccept, etag)
	if rec2.Code != http.StatusNotModified {
		t.Errorf("conditional GET = %d, want 304", rec2.Code)
	}
}

// The AP profile media type must work too — it is what Mastodon sends.
func TestVideoObjectAcceptsTheLDProfileMediaType(t *testing.T) {
	srv, videoID, _, _ := apObjectServer(t)
	rec := getAccept(t, srv, "/videos/"+videoID.String(),
		`application/ld+json; profile="https://www.w3.org/ns/activitystreams"`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// A browser Accept is 406, not HTML and not JSON: core has no page to serve, and
// the operator's proxy is what keeps a browser from arriving here at all.
func TestVideoObjectRefusesNonActivityPubAccept(t *testing.T) {
	srv, videoID, _, _ := apObjectServer(t)
	rec := getAccept(t, srv, "/videos/"+videoID.String(), "text/html")
	if rec.Code != http.StatusNotAcceptable {
		t.Fatalf("status = %d, want 406; body=%s", rec.Code, rec.Body.String())
	}
}

// A29-F9: a retracted video's id answers 410 with a Tombstone, so a peer
// dereferencing the Delete it was just sent learns the retraction was real.
func TestDeletedVideoObjectIsATombstone(t *testing.T) {
	srv, _, _, deletedID := apObjectServer(t)
	rec := getAccept(t, srv, "/videos/"+deletedID.String(), apJSONAccept)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410; body=%s", rec.Code, rec.Body.String())
	}
	var obj map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if obj["type"] != "Tombstone" {
		t.Errorf("type = %v, want Tombstone", obj["type"])
	}
	if obj["formerType"] != "Video" {
		t.Errorf("formerType = %v, want Video", obj["formerType"])
	}
	if obj["deleted"] == nil {
		t.Error("no deleted timestamp on the Tombstone")
	}
	// A retraction must not leak what it retracts.
	for _, leaked := range []string{"name", "content", "attributedTo", "url", "icon", "duration"} {
		if _, present := obj[leaked]; present {
			t.Errorf("Tombstone carries %q: %v — a retraction must not describe what it retracts", leaked, obj[leaked])
		}
	}
	// And it must not be cached: a 410 is the one answer that can legitimately
	// turn back into a 200 after a restore.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Tombstone cache-control = %q, want no-store", cc)
	}
	// Nor carry a validator. An ETag invites If-None-Match, and a 304 on this
	// answer would tell the peer that what it already holds — the video — is
	// current. The rehearsal found one here and called it meaningless; it is
	// worse than meaningless.
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("Tombstone carries ETag %q: a 410 must not be revalidatable", etag)
	}
}

// An id that was never a video and a private video answer identically, so the
// endpoint is not an enumeration oracle.
func TestUnknownAndNonPublicVideoObjectsAreIndistinguishable(t *testing.T) {
	cfg := fedTestConfig()
	srv, repo := fedServerRepo(cfg)
	privateID := uuid.New()
	repo.videosByID[privateID] = sqlcgen.GetVideoByIDRow{
		ID: privateID, ChannelID: repo.channelID,
		Title: "Secret", Privacy: "private", State: "published",
	}
	unlistedID := uuid.New()
	repo.videosByID[unlistedID] = sqlcgen.GetVideoByIDRow{
		ID: unlistedID, ChannelID: repo.channelID,
		Title: "Quiet", Privacy: "unlisted", State: "published",
	}
	draftID := uuid.New()
	repo.videosByID[draftID] = sqlcgen.GetVideoByIDRow{
		ID: draftID, ChannelID: repo.channelID,
		Title: "Draft", Privacy: "public", State: "uploaded",
	}
	for name, id := range map[string]uuid.UUID{
		"never existed": uuid.New(),
		"private":       privateID,
		"unlisted":      unlistedID,
		"unpublished":   draftID,
	} {
		t.Run(name, func(t *testing.T) {
			rec := getAccept(t, srv, "/videos/"+id.String(), apJSONAccept)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A29-F2, the Note half: a reply threads against /comments/{uuid}, so that id
// has to answer as well.
func TestNoteObjectIsDereferenceableAsActivityPub(t *testing.T) {
	srv, videoID, commentID, _ := apObjectServer(t)
	rec := getAccept(t, srv, "/comments/"+commentID.String(), apJSONAccept)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var obj map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if obj["type"] != "Note" {
		t.Errorf("type = %v, want Note", obj["type"])
	}
	if want := "https://videos.example/comments/" + commentID.String(); obj["id"] != want {
		t.Errorf("id = %v, want %q", obj["id"], want)
	}
	if want := "https://videos.example/videos/" + videoID.String(); obj["inReplyTo"] != want {
		t.Errorf("inReplyTo = %v, want %q", obj["inReplyTo"], want)
	}
	if want := "https://videos.example/accounts/ada"; obj["attributedTo"] != want {
		t.Errorf("attributedTo = %v, want %q", obj["attributedTo"], want)
	}
}

// The object routes exist only when federation is on, like every other AP route.
func TestObjectIdsAreAbsentWithoutFederation(t *testing.T) {
	cfg := testConfig()
	cfg.PublicBaseURL = "https://videos.example"
	srv := New(cfg, nil, nil)
	rec := getAccept(t, srv, "/videos/"+uuid.New().String(), apJSONAccept)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 with federation off", rec.Code)
	}
}

func getAcceptIfNoneMatch(t *testing.T, srv *Server, path, accept, etag string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Accept", accept)
	req.Header.Set("If-None-Match", etag)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// A29-F4: an activity from a blocked instance is still answered 202 — telling a
// blocked peer that it is blocked is a probe target — but the refusal now leaves
// an audit row, so the admin who blocked the domain can see the block working.
func TestBlockedInstanceInboxRefusalIsAudited(t *testing.T) {
	var buf bytes.Buffer
	key, pubPEM := signerKeyPEM(t)
	cfg := fedTestConfig()
	repo := newFedRepoFor(cfg)
	repo.remoteActors[bobActor] = sqlcgen.RemoteActor{ActorUrl: bobActor, PublicKeyPem: pubPEM}
	repo.blockedDomains["remote.example"] = true
	srv := New(cfg, nil, nil,
		WithFederationService(federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))),
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
	)

	body := []byte(`{"id":"https://remote.example/act/9","type":"Follow","actor":"` + bobActor +
		`","object":"https://videos.example/video-channels/films"}`)
	req := httptest.NewRequest(http.MethodPost, "https://videos.example/inbox", bytes.NewReader(body))
	if err := (httpsig.Signer{KeyID: bobActor + "#main-key", Priv: key}).Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 — a blocked instance must not learn it is blocked", rec.Code)
	}
	if len(repo.remoteFollows) != 0 {
		t.Error("the activity from a blocked instance was dispatched")
	}
	ev := findAudit(auditEvents(t, &buf), observability.ActionFederationInboxRejected, observability.ResultSuccess)
	if ev == nil {
		t.Fatal("no federation.inbox.rejected audit row; the block is invisible to the admin who set it")
	}
	if reason, _ := ev["reason"].(string); reason != "domain=remote.example" {
		t.Errorf("reason = %q, want domain=remote.example", reason)
	}
	// The refused activity's body, id and actor URL must never reach the trail.
	for _, forbidden := range []string{"https://remote.example/act/9", bobActor} {
		for k, v := range ev {
			if str, ok := v.(string); ok && strings.Contains(str, forbidden) {
				t.Errorf("audit field %q leaked the refused activity: %q", k, str)
			}
		}
	}
}
