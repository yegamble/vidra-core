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

// The AP-disabled skip guards every outbound video activity, not just Announce:
// an edit (UpdateVideo) and a deletion (DeleteVideo) must also emit nothing.
func TestUpdateVideoSkippedWhenActivityPubDisabled(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "public", "published",
		[]string{"https://a.example/inbox", "https://b.example/inbox"})
	repo.apDisabled = map[uuid.UUID]bool{channelID: true}

	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.UpdateVideo(context.Background(), videoID); err != nil {
		t.Fatalf("UpdateVideo: %v", err)
	}
	if len(repo.deliveries) != 0 {
		t.Fatalf("enqueued deliveries = %d, want 0 for AP-disabled channel", len(repo.deliveries))
	}
}

func TestDeleteVideoSkippedWhenActivityPubDisabled(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := fakeRepo{
		channelsByID:    map[uuid.UUID]sqlcgen.Channel{channelID: {ID: channelID, Handle: "films"}},
		followerInboxes: map[uuid.UUID][]string{channelID: {"https://a.example/inbox"}},
		deliveries:      map[uuid.UUID]*fakeDelivery{},
		apDisabled:      map[uuid.UUID]bool{channelID: true},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	// wasPublic=true so the never-federated short-circuit doesn't mask the flag.
	if err := svc.DeleteVideo(context.Background(), videoID, channelID, true); err != nil {
		t.Fatalf("DeleteVideo: %v", err)
	}
	if len(repo.deliveries) != 0 {
		t.Fatalf("enqueued deliveries = %d, want 0 for AP-disabled channel", len(repo.deliveries))
	}
}

// Comment federation honours the same per-channel opt-out: a create/update
// (federateComment) and a deletion (DeleteComment) on an AP-disabled channel
// emit nothing.
func TestFederateCommentSkippedWhenActivityPubDisabled(t *testing.T) {
	for _, activity := range []string{"Create", "Update"} {
		t.Run(activity, func(t *testing.T) {
			repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
			channelID := repo.videosByID[c.VideoID].ChannelID
			repo.apDisabled = map[uuid.UUID]bool{channelID: true}
			svc := NewService(repo, WithBaseURL(noteBase))

			var err error
			if activity == "Create" {
				err = svc.AnnounceComment(context.Background(), c.ID)
			} else {
				err = svc.UpdateComment(context.Background(), c.ID)
			}
			if err != nil {
				t.Fatalf("%sComment: %v", activity, err)
			}
			if len(repo.deliveries) != 0 {
				t.Fatalf("enqueued deliveries = %d, want 0 for AP-disabled channel", len(repo.deliveries))
			}
		})
	}
}

func TestDeleteCommentSkippedWhenActivityPubDisabled(t *testing.T) {
	repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
	channelID := repo.videosByID[c.VideoID].ChannelID
	repo.apDisabled = map[uuid.UUID]bool{channelID: true}
	authorID := uuid.UUID(c.UserID.Bytes)
	delete(repo.commentsByID, c.ID) // already gone at hook time

	svc := NewService(repo, WithBaseURL(noteBase))
	if err := svc.DeleteComment(context.Background(), c.ID, c.VideoID, authorID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if len(repo.deliveries) != 0 {
		t.Fatalf("enqueued deliveries = %d, want 0 for AP-disabled channel", len(repo.deliveries))
	}
}

// Control: the same comment fixtures WITH ActivityPub enabled (the default) do
// fan out, proving the skips above are the flag's effect and not the fixture.
func TestCommentFederationFansOutWhenActivityPubEnabled(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
		svc := NewService(repo, WithBaseURL(noteBase))
		if err := svc.AnnounceComment(context.Background(), c.ID); err != nil {
			t.Fatalf("AnnounceComment: %v", err)
		}
		if len(repo.deliveries) != 1 {
			t.Fatalf("enqueued deliveries = %d, want 1 for AP-enabled channel", len(repo.deliveries))
		}
	})
	t.Run("delete", func(t *testing.T) {
		repo, c := newCommentOutboxRepo([]string{"https://a.example/inbox"})
		authorID := uuid.UUID(c.UserID.Bytes)
		delete(repo.commentsByID, c.ID)
		svc := NewService(repo, WithBaseURL(noteBase))
		if err := svc.DeleteComment(context.Background(), c.ID, c.VideoID, authorID); err != nil {
			t.Fatalf("DeleteComment: %v", err)
		}
		if len(repo.deliveries) != 1 {
			t.Fatalf("enqueued deliveries = %d, want 1 for AP-enabled channel", len(repo.deliveries))
		}
	})
}
