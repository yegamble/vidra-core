package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func newInboxRepo() fakeRepo {
	films := sqlcgen.Channel{ID: uuid.New(), Handle: "films", DisplayName: "Films"}
	return fakeRepo{
		channels:      map[string]sqlcgen.Channel{"films": films},
		channelsByID:  map[uuid.UUID]sqlcgen.Channel{films.ID: films},
		processed:     map[string]bool{},
		remoteFollows: map[string]*fakeRemoteFollow{},
		followBacks:   map[string]*fakeFollowBack{},
		rcFollows:     map[uuid.UUID]*sqlcgen.RemoteChannelFollow{},
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

func TestHandleInboxEnqueuesAccept(t *testing.T) {
	repo := newInboxRepo()
	repo.remoteActors = map[string]sqlcgen.RemoteActor{
		remoteBob: {ActorUrl: remoteBob, InboxUrl: "https://remote.example/accounts/bob/inbox"},
	}
	repo.deliveries = map[uuid.UUID]*fakeDelivery{}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(filmsFollow)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Errorf("enqueued deliveries = %d, want 1 (Accept)", len(repo.deliveries))
	}
}

func TestHandleInboxUndoFollowRemoves(t *testing.T) {
	repo := newInboxRepo()
	// Seed an existing remote follow (as if a prior Follow was recorded).
	cid := repo.channels["films"].ID
	repo.remoteFollows[cid.String()+"|"+remoteBob] = &fakeRemoteFollow{ID: uuid.New(), ChannelID: cid, RemoteActorUrl: remoteBob, State: "accepted"}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	undo := `{"id":"https://remote.example/act/undo/1","type":"Undo","actor":"` + remoteBob +
		`","object":{"id":"` + followID + `","type":"Follow","actor":"` + remoteBob + `","object":"` + filmsActor + `"}}`
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(undo)); err != nil {
		t.Fatalf("HandleInbox Undo: %v", err)
	}
	if _, ok := repo.remoteFollows[cid.String()+"|"+remoteBob]; ok {
		t.Error("remote follow was not removed by Undo{Follow}")
	}
}

func TestHandleInboxUndoNonFollowIgnored(t *testing.T) {
	repo := newInboxRepo()
	cid := repo.channels["films"].ID
	repo.remoteFollows[cid.String()+"|"+remoteBob] = &fakeRemoteFollow{ID: uuid.New(), ChannelID: cid, RemoteActorUrl: remoteBob, State: "accepted"}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	// Undo of a Like → accepted and ignored; the follow stays.
	undo := `{"id":"https://remote.example/act/undo/2","type":"Undo","actor":"` + remoteBob +
		`","object":{"type":"Like","object":"x"}}`
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(undo)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if _, ok := repo.remoteFollows[cid.String()+"|"+remoteBob]; !ok {
		t.Error("Undo{Like} should not remove a follow")
	}
}

func TestHandleInboxUndoActorMismatch(t *testing.T) {
	svc := NewService(newInboxRepo(), WithBaseURL("https://videos.example"))
	// Signer differs from the Undo's actor.
	undo := `{"id":"https://remote.example/act/undo/3","type":"Undo","actor":"` + remoteBob +
		`","object":{"type":"Follow","actor":"` + remoteBob + `","object":"` + filmsActor + `"}}`
	if err := svc.HandleInbox(context.Background(), "https://remote.example/accounts/eve", []byte(undo)); !errors.Is(err, ErrActorMismatch) {
		t.Errorf("err = %v, want ErrActorMismatch", err)
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
