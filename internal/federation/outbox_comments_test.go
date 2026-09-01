package federation

// Outbound federated-comment tests (remote-content §6): a local comment on a
// local public video fans out as Create/Update/Delete{Note} to the channel's
// remote follower inboxes, attributed to and signed as the commenter's
// ACCOUNT actor.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// newCommentOutboxRepo seeds a local user ("ada"), a public published video on
// channel "films" with the given follower inboxes, and one of ada's comments.
func newCommentOutboxRepo(inboxes []string) (fakeRepo, sqlcgen.Comment) {
	channelID, videoID, userID := uuid.New(), uuid.New(), uuid.New()
	c := sqlcgen.Comment{
		ID: uuid.New(), VideoID: videoID, UserID: pgUUIDv(userID), Body: "nice video!",
	}
	repo := fakeRepo{
		videosByID: map[uuid.UUID]sqlcgen.GetVideoByIDRow{
			videoID: {ID: videoID, ChannelID: channelID, Privacy: "public", State: "published"},
		},
		channelsByID:    map[uuid.UUID]sqlcgen.Channel{channelID: {ID: channelID, Handle: "films"}},
		followerInboxes: map[uuid.UUID][]string{channelID: inboxes},
		deliveries:      map[uuid.UUID]*fakeDelivery{},
		usersByID: map[uuid.UUID]sqlcgen.GetUserActorByIDRow{
			userID: {ID: userID, Username: "ada"},
		},
		acctKeys:     map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		commentsByID: map[uuid.UUID]sqlcgen.Comment{c.ID: c},
	}
	return repo, c
}

// noteActivity is the payload shape the fan-out tests decode.
type noteActivity struct {
	Type   string `json:"type"`
	Actor  string `json:"actor"`
	Object struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Content      string `json:"content"`
		InReplyTo    string `json:"inReplyTo"`
		AttributedTo string `json:"attributedTo"`
	} `json:"object"`
}

func TestAnnounceCommentFansOutSignedAsAccount(t *testing.T) {
	repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox", "https://b.example/inbox"})
	svc := NewService(repo, WithBaseURL(noteBase))

	if err := svc.AnnounceComment(context.Background(), c.ID); err != nil {
		t.Fatalf("AnnounceComment: %v", err)
	}
	if len(repo.deliveries) != 2 {
		t.Fatalf("deliveries = %d, want 2 (one per follower inbox)", len(repo.deliveries))
	}
	accountActor := noteBase + "/accounts/ada"
	for _, d := range repo.deliveries {
		// Signed as the commenter's ACCOUNT actor, not the channel.
		if !d.row.SigningUserID.Valid || d.row.SigningUsername != "ada" {
			t.Errorf("delivery signer = %+v, want account actor ada", d.row)
		}
		if d.row.SigningChannelID.Valid {
			t.Error("delivery must not be channel-signed")
		}
		var a noteActivity
		if err := json.Unmarshal(d.row.Payload, &a); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if a.Type != "Create" || a.Actor != accountActor {
			t.Errorf("activity = %+v", a)
		}
		if a.Object.Type != "Note" || a.Object.Content != "nice video!" {
			t.Errorf("object = %+v", a.Object)
		}
		if a.Object.ID != noteBase+"/comments/"+c.ID.String() {
			t.Errorf("note id = %q", a.Object.ID)
		}
		if a.Object.InReplyTo != noteBase+"/videos/"+c.VideoID.String() {
			t.Errorf("inReplyTo = %q, want the video object URL", a.Object.InReplyTo)
		}
		if a.Object.AttributedTo != accountActor {
			t.Errorf("attributedTo = %q", a.Object.AttributedTo)
		}
	}
	// The account keypair was minted up front for delivery-time signing.
	if len(repo.acctKeys) != 1 {
		t.Errorf("account keys minted = %d, want 1", len(repo.acctKeys))
	}
}

func TestAnnounceCommentReplyThreadsToParentURL(t *testing.T) {
	repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL(noteBase))

	// A reply to a LOCAL parent uses our comment URL.
	parent := sqlcgen.Comment{ID: uuid.New(), VideoID: c.VideoID, UserID: c.UserID, Body: "parent"}
	repo.commentsByID[parent.ID] = parent
	reply := c
	reply.ID = uuid.New()
	reply.ParentID = pgUUIDv(parent.ID)
	repo.commentsByID[reply.ID] = reply
	if err := svc.AnnounceComment(context.Background(), reply.ID); err != nil {
		t.Fatalf("AnnounceComment (local parent): %v", err)
	}

	// A reply to a REMOTE parent uses the parent's origin object URL.
	remoteObj := "https://remote.example/comments/77"
	actor := "https://remote.example/accounts/bob"
	remoteParent := sqlcgen.Comment{
		ID: uuid.New(), VideoID: c.VideoID, Body: "remote parent",
		RemoteActorUrl: &actor, RemoteObjectUrl: &remoteObj,
	}
	repo.commentsByID[remoteParent.ID] = remoteParent
	reply2 := c
	reply2.ID = uuid.New()
	reply2.ParentID = pgUUIDv(remoteParent.ID)
	repo.commentsByID[reply2.ID] = reply2
	if err := svc.AnnounceComment(context.Background(), reply2.ID); err != nil {
		t.Fatalf("AnnounceComment (remote parent): %v", err)
	}

	wants := map[string]bool{
		noteBase + "/comments/" + parent.ID.String(): false,
		remoteObj: false,
	}
	for _, d := range repo.deliveries {
		var a noteActivity
		_ = json.Unmarshal(d.row.Payload, &a)
		if _, ok := wants[a.Object.InReplyTo]; ok {
			wants[a.Object.InReplyTo] = true
		}
	}
	for target, seen := range wants {
		if !seen {
			t.Errorf("no Note threaded to %q", target)
		}
	}
}

func TestAnnounceCommentSkips(t *testing.T) {
	t.Run("remote-authored comments never fan out", func(t *testing.T) {
		repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
		actor := "https://remote.example/accounts/bob"
		rc := c
		rc.ID = uuid.New()
		rc.UserID = pgtype.UUID{}
		rc.RemoteActorUrl = &actor
		repo.commentsByID[rc.ID] = rc
		svc := NewService(repo, WithBaseURL(noteBase))
		if err := svc.AnnounceComment(context.Background(), rc.ID); err != nil {
			t.Fatalf("AnnounceComment: %v", err)
		}
		if len(repo.deliveries) != 0 {
			t.Errorf("deliveries = %d, want 0", len(repo.deliveries))
		}
	})
	t.Run("non-public video", func(t *testing.T) {
		repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
		v := repo.videosByID[c.VideoID]
		v.Privacy = "private"
		repo.videosByID[c.VideoID] = v
		svc := NewService(repo, WithBaseURL(noteBase))
		if err := svc.AnnounceComment(context.Background(), c.ID); err != nil {
			t.Fatalf("AnnounceComment: %v", err)
		}
		if len(repo.deliveries) != 0 {
			t.Errorf("deliveries = %d, want 0", len(repo.deliveries))
		}
	})
	t.Run("no remote followers", func(t *testing.T) {
		repo, c := newCommentOutboxRepo(nil)
		svc := NewService(repo, WithBaseURL(noteBase))
		if err := svc.AnnounceComment(context.Background(), c.ID); err != nil {
			t.Fatalf("AnnounceComment: %v", err)
		}
		if len(repo.deliveries) != 0 {
			t.Errorf("deliveries = %d, want 0", len(repo.deliveries))
		}
		if len(repo.acctKeys) != 0 {
			t.Error("minted an account key with nobody to deliver to")
		}
	})
	t.Run("unknown comment is a no-op", func(t *testing.T) {
		repo, _ := newCommentOutboxRepo([]string{"https://a.example/inbox"})
		svc := NewService(repo, WithBaseURL(noteBase))
		if err := svc.AnnounceComment(context.Background(), uuid.New()); err != nil {
			t.Fatalf("AnnounceComment: %v", err)
		}
		if len(repo.deliveries) != 0 {
			t.Errorf("deliveries = %d, want 0", len(repo.deliveries))
		}
	})
}

func TestUpdateCommentSendsUpdateNote(t *testing.T) {
	repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL(noteBase))
	if err := svc.UpdateComment(context.Background(), c.ID); err != nil {
		t.Fatalf("UpdateComment: %v", err)
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

func TestDeleteCommentSendsDelete(t *testing.T) {
	repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL(noteBase))
	authorID := uuid.UUID(c.UserID.Bytes)

	// The comment row is already gone at hook time.
	delete(repo.commentsByID, c.ID)
	if err := svc.DeleteComment(context.Background(), c.ID, c.VideoID, authorID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
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
		if a.Type != "Delete" || a.Object != noteBase+"/comments/"+c.ID.String() {
			t.Errorf("activity = %+v, want Delete of the note URL", a)
		}
		if a.Actor != noteBase+"/accounts/ada" {
			t.Errorf("actor = %q, want the account actor", a.Actor)
		}
		if !d.row.SigningUserID.Valid || d.row.SigningUsername != "ada" {
			t.Errorf("delivery signer = %+v, want account actor ada", d.row)
		}
	}
}
