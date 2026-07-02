package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func newInboxRepo() fakeRepo {
	return fakeRepo{
		channels: map[string]sqlcgen.Channel{
			"films": {ID: uuid.New(), Handle: "films", DisplayName: "Films"},
		},
		processed:     map[string]bool{},
		remoteFollows: map[string]sqlcgen.InsertRemoteFollowParams{},
	}
}

const (
	remoteBob   = "https://remote.example/accounts/bob"
	followID    = "https://remote.example/activities/follow/1"
	filmsActor  = "https://videos.example/video-channels/films"
	filmsFollow = `{"id":"` + followID + `","type":"Follow","actor":"` + remoteBob + `","object":"` + filmsActor + `"}`
)

func TestHandleInboxFollowRecordsRemoteFollow(t *testing.T) {
	repo := newInboxRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(filmsFollow)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	rf, ok := repo.remoteFollows[repo.channels["films"].ID.String()+"|"+remoteBob]
	if !ok {
		t.Fatalf("remote follow not recorded: %+v", repo.remoteFollows)
	}
	if rf.FollowActivityUrl != followID {
		t.Errorf("follow activity url = %q, want %q", rf.FollowActivityUrl, followID)
	}
	if !repo.processed[followID] {
		t.Error("activity not marked processed")
	}
}

func TestHandleInboxRejectsActorMismatch(t *testing.T) {
	svc := NewService(newInboxRepo(), WithBaseURL("https://videos.example"))
	// Signer differs from the Follow's actor.
	err := svc.HandleInbox(context.Background(), "https://remote.example/accounts/eve", []byte(filmsFollow))
	if !errors.Is(err, ErrActorMismatch) {
		t.Errorf("err = %v, want ErrActorMismatch", err)
	}
}

func TestHandleInboxDedup(t *testing.T) {
	repo := newInboxRepo()
	repo.processed[followID] = true // already handled
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(filmsFollow)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.remoteFollows) != 0 {
		t.Error("a duplicate activity was processed again")
	}
}

func TestHandleInboxIgnoresUnknownTypeAndForeignObject(t *testing.T) {
	repo := newInboxRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	// Non-Follow type → accepted and ignored.
	like := `{"id":"https://remote.example/act/like/1","type":"Like","actor":"` + remoteBob + `","object":"x"}`
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(like)); err != nil {
		t.Fatalf("Like: %v", err)
	}
	// Follow whose object is not one of our channels → ignored (no error, no record).
	foreign := `{"id":"https://remote.example/act/f/2","type":"Follow","actor":"` + remoteBob + `","object":"https://other.example/video-channels/x"}`
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(foreign)); err != nil {
		t.Fatalf("foreign Follow: %v", err)
	}
	if len(repo.remoteFollows) != 0 {
		t.Errorf("unexpected follow recorded: %+v", repo.remoteFollows)
	}
}

func TestHandleInboxRejectsMalformed(t *testing.T) {
	svc := NewService(newInboxRepo(), WithBaseURL("https://videos.example"))
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(`not json`)); !errors.Is(err, ErrBadResource) {
		t.Errorf("err = %v, want ErrBadResource", err)
	}
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(`{"type":"Follow"}`)); !errors.Is(err, ErrBadResource) {
		t.Errorf("missing id: err = %v, want ErrBadResource", err)
	}
}
