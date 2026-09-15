package federation

// Outbound tests for locally-authored comments ON remote videos (migration 0147,
// the home-instance-hosts-and-federates ruling): the comment is minted as a Note
// attributed to the author's account actor, inReplyTo the remote video's origin
// object id, delivered to the ORIGIN inbox through the durable queue, and its
// delivery_state reflects the queue result.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const (
	authoredOriginActor    = "https://peer.example/video-channels/films"
	authoredOriginVideoURL = "https://peer.example/videos/origin-1"
)

// newAuthoredRepo seeds a remote video (origin channel actor + inbox), a local
// user "ada", and one comment ada authored on that remote video.
func newAuthoredRepo(originInbox string) (fakeRepo, sqlcgen.AuthoredRemoteComment, uuid.UUID) {
	rvID, userID, commentID := uuid.New(), uuid.New(), uuid.New()
	c := sqlcgen.AuthoredRemoteComment{
		ID: commentID, RemoteVideoID: rvID, UserID: userID, Body: "great video",
		ObjectUrl:     noteBase + "/remote-comments/" + commentID.String(),
		InReplyTo:     authoredOriginVideoURL,
		DeliveryState: "pending",
		CreatedAt:     time.Now(),
	}
	repo := fakeRepo{
		remoteVideos: map[string]*fakeRemoteVideo{
			authoredOriginVideoURL: {id: rvID, params: sqlcgen.UpsertRemoteVideoParams{
				ObjectUrl: authoredOriginVideoURL, RemoteActorUrl: authoredOriginActor,
			}},
		},
		remoteActors: map[string]sqlcgen.RemoteActor{
			authoredOriginActor: {ActorUrl: authoredOriginActor, InboxUrl: originInbox, Domain: "peer.example"},
		},
		usersByID:              map[uuid.UUID]sqlcgen.GetUserActorByIDRow{userID: {ID: userID, Username: "ada"}},
		acctKeys:               map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		authoredRemoteComments: map[uuid.UUID]sqlcgen.AuthoredRemoteComment{commentID: c},
		deliveries:             map[uuid.UUID]*fakeDelivery{},
		blockedDomains:         map[string]bool{},
	}
	return repo, c, userID
}

func TestAnnounceAuthoredRemoteCommentEnqueuesCreateToOrigin(t *testing.T) {
	repo, c, _ := newAuthoredRepo("https://peer.example/inbox")
	svc := NewService(repo, WithBaseURL(noteBase))

	if err := svc.AnnounceAuthoredRemoteComment(context.Background(), c.ID); err != nil {
		t.Fatalf("AnnounceAuthoredRemoteComment: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1 (to the origin inbox)", len(repo.deliveries))
	}
	accountActor := noteBase + "/accounts/ada"
	for _, d := range repo.deliveries {
		if d.row.InboxUrl != "https://peer.example/inbox" {
			t.Errorf("delivered to %q, want the origin inbox", d.row.InboxUrl)
		}
		// Signed as the author's ACCOUNT actor.
		if !d.row.SigningUserID.Valid || d.row.SigningUsername != "ada" || d.row.SigningChannelID.Valid {
			t.Errorf("signer = %+v, want account actor ada", d.row)
		}
		// Carries the authored-comment link so the drain can reflect the result.
		if !d.row.AuthoredRemoteCommentID.Valid || uuid.UUID(d.row.AuthoredRemoteCommentID.Bytes) != c.ID {
			t.Errorf("authored_remote_comment_id = %+v, want %v", d.row.AuthoredRemoteCommentID, c.ID)
		}
		var a noteActivity
		if err := json.Unmarshal(d.row.Payload, &a); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if a.Type != "Create" || a.Actor != accountActor {
			t.Errorf("activity = %+v", a)
		}
		if a.Object.Type != "Note" || a.Object.Content != "great video" {
			t.Errorf("object = %+v", a.Object)
		}
		if a.Object.ID != c.ObjectUrl {
			t.Errorf("note id = %q, want the minted local object url %q", a.Object.ID, c.ObjectUrl)
		}
		if a.Object.InReplyTo != authoredOriginVideoURL {
			t.Errorf("inReplyTo = %q, want the origin video url", a.Object.InReplyTo)
		}
		if a.Object.AttributedTo != accountActor {
			t.Errorf("attributedTo = %q", a.Object.AttributedTo)
		}
	}
	if len(repo.acctKeys) != 1 {
		t.Errorf("account keys minted = %d, want 1", len(repo.acctKeys))
	}
}

func TestUpdateAuthoredRemoteCommentSendsUpdate(t *testing.T) {
	repo, c, _ := newAuthoredRepo("https://peer.example/inbox")
	svc := NewService(repo, WithBaseURL(noteBase))
	if err := svc.UpdateAuthoredRemoteComment(context.Background(), c.ID); err != nil {
		t.Fatalf("UpdateAuthoredRemoteComment: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		var a noteActivity
		_ = json.Unmarshal(d.row.Payload, &a)
		if a.Type != "Update" || a.Object.Type != "Note" {
			t.Errorf("activity = %+v, want Update{Note}", a)
		}
	}
}

func TestDeleteAuthoredRemoteCommentSendsDelete(t *testing.T) {
	repo, c, userID := newAuthoredRepo("https://peer.example/inbox")
	svc := NewService(repo, WithBaseURL(noteBase))
	// The row is gone by hook time (hard delete).
	delete(repo.authoredRemoteComments, c.ID)
	if err := svc.DeleteAuthoredRemoteComment(context.Background(), c.ID, c.RemoteVideoID, userID, c.ObjectUrl); err != nil {
		t.Fatalf("DeleteAuthoredRemoteComment: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		var a struct {
			Type   string `json:"type"`
			Actor  string `json:"actor"`
			Object string `json:"object"`
		}
		_ = json.Unmarshal(d.row.Payload, &a)
		if a.Type != "Delete" || a.Object != c.ObjectUrl {
			t.Errorf("activity = %+v, want Delete of the local note url", a)
		}
	}
}

func TestAnnounceAuthoredRemoteCommentBlockedOriginMarksFailed(t *testing.T) {
	// The origin video is gone / its instance unresolvable: the comment stays
	// hosted here but is stamped 'failed' so the author sees the federation leg
	// never landed.
	repo, c, _ := newAuthoredRepo("https://peer.example/inbox")
	delete(repo.remoteVideos, authoredOriginVideoURL) // origin no longer resolvable
	svc := NewService(repo, WithBaseURL(noteBase))
	if err := svc.AnnounceAuthoredRemoteComment(context.Background(), c.ID); err != nil {
		t.Fatalf("AnnounceAuthoredRemoteComment: %v", err)
	}
	if len(repo.deliveries) != 0 {
		t.Errorf("deliveries = %d, want 0 (nowhere to deliver)", len(repo.deliveries))
	}
	if got := repo.authoredRemoteComments[c.ID].DeliveryState; got != "failed" {
		t.Errorf("delivery_state = %q, want failed", got)
	}
}

func TestDrainReflectsDeliveredOntoAuthoredComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	repo, c, _ := newAuthoredRepo(srv.URL)
	svc := NewService(repo, WithBaseURL(noteBase), WithAllowPrivateFetch(true))
	if err := svc.AnnounceAuthoredRemoteComment(context.Background(), c.ID); err != nil {
		t.Fatalf("announce: %v", err)
	}
	n, err := svc.DrainDeliveries(context.Background(), 10)
	if err != nil || n != 1 {
		t.Fatalf("DrainDeliveries = (%d, %v), want (1, nil)", n, err)
	}
	if got := repo.authoredRemoteComments[c.ID].DeliveryState; got != "delivered" {
		t.Errorf("delivery_state = %q, want delivered", got)
	}
}

func TestDrainReflectsBlockCancelOntoAuthoredComment(t *testing.T) {
	repo, c, _ := newAuthoredRepo("https://peer.example/inbox")
	svc := NewService(repo, WithBaseURL(noteBase))
	if err := svc.AnnounceAuthoredRemoteComment(context.Background(), c.ID); err != nil {
		t.Fatalf("announce: %v", err)
	}
	// The origin instance is blocked before the drain runs: the delivery is
	// cancelled (never sent), and the author's badge must say 'failed'.
	repo.blockedDomains["peer.example"] = true
	if _, err := svc.DrainDeliveries(context.Background(), 10); err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	got := repo.authoredRemoteComments[c.ID]
	if got.DeliveryState != "failed" {
		t.Errorf("delivery_state = %q, want failed", got.DeliveryState)
	}
	if got.LastError != DeliveryCancelledByPolicy {
		t.Errorf("last_error = %q, want the cancel marker", got.LastError)
	}
}
