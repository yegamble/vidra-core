package federation

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Per-channel ActivityPub distribution flag (migration 0096): a channel with
// activitypub_enabled=false does not federate — inbound Follows are dropped and
// outbound Create/Announce is skipped.

func TestHandleInboxFollowIgnoredWhenActivityPubDisabled(t *testing.T) {
	repo := newInboxRepo()
	repo.remoteActors = map[string]sqlcgen.RemoteActor{
		remoteBob: {ActorUrl: remoteBob, InboxUrl: "https://remote.example/accounts/bob/inbox"},
	}
	repo.deliveries = map[uuid.UUID]*fakeDelivery{}
	// Turn ActivityPub OFF for the films channel.
	repo.apDisabled = map[uuid.UUID]bool{repo.channels["films"].ID: true}

	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.HandleInbox(context.Background(), remoteBob, []byte(filmsFollow)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	// No follow recorded and no Accept/Reject delivery enqueued: an AP-disabled
	// channel emits nothing outbound.
	if len(repo.remoteFollows) != 0 {
		t.Errorf("remote follow recorded for AP-disabled channel: %+v", repo.remoteFollows)
	}
	if len(repo.deliveries) != 0 {
		t.Errorf("delivery enqueued for AP-disabled channel: %+v", repo.deliveries)
	}
}

func TestAnnounceVideoSkippedWhenActivityPubDisabled(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "public", "published",
		[]string{"https://a.example/inbox", "https://b.example/inbox"})
	repo.apDisabled = map[uuid.UUID]bool{channelID: true}

	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.AnnounceVideo(context.Background(), videoID); err != nil {
		t.Fatalf("AnnounceVideo: %v", err)
	}
	if len(repo.deliveries) != 0 {
		t.Fatalf("enqueued deliveries = %d, want 0 for AP-disabled channel", len(repo.deliveries))
	}
}

// Control: the same fixture WITH ActivityPub enabled (the default) does fan out,
// proving the skip above is the flag's effect and not the fixture.
func TestAnnounceVideoFansOutWhenActivityPubEnabled(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "public", "published",
		[]string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.AnnounceVideo(context.Background(), videoID); err != nil {
		t.Fatalf("AnnounceVideo: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("enqueued deliveries = %d, want 1 for AP-enabled channel", len(repo.deliveries))
	}
}
