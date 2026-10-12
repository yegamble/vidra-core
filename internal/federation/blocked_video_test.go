package federation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// T13: a moderator-blocked video must not be served or pushed over ActivityPub,
// and the block must retract it from followers.

func blockedRepo(channelID, videoID uuid.UUID, privacy string) fakeRepo {
	repo := newOutboxRepo(channelID, videoID, privacy, "published", []string{"https://a.example/inbox"})
	v := repo.videosByID[videoID]
	v.Blocked = true
	repo.videosByID[videoID] = v
	repo.tombstones = map[uuid.UUID]time.Time{}
	return repo
}

func TestVideoObjectOfBlockedVideoIsGoneOnceRetracted(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := blockedRepo(channelID, videoID, "public")
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	// Blocked but never retracted: indistinguishable from a private video.
	if _, err := svc.VideoObject(context.Background(), videoID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("blocked, no tombstone: err = %v, want ErrNotFound", err)
	}
	if err := svc.UnfederateBlockedVideo(context.Background(), videoID); err != nil {
		t.Fatalf("UnfederateBlockedVideo: %v", err)
	}
	if _, err := svc.VideoObject(context.Background(), videoID); !errors.Is(err, ErrGone) {
		t.Fatalf("blocked + retracted: err = %v, want ErrGone (410 Tombstone)", err)
	}
}

func TestUnfederateBlockedVideoSendsDelete(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := blockedRepo(channelID, videoID, "public")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.UnfederateBlockedVideo(context.Background(), videoID); err != nil {
		t.Fatalf("UnfederateBlockedVideo: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		var a struct {
			Type   string `json:"type"`
			Object string `json:"object"`
		}
		_ = json.Unmarshal(d.row.Payload, &a)
		if a.Type != "Delete" || a.Object != "https://videos.example/videos/"+videoID.String() {
			t.Errorf("activity = %+v, want Delete of the video URL", a)
		}
	}
}

func TestUnfederateBlockedVideoNeverFederatedIsNoop(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := blockedRepo(channelID, videoID, "private")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.UnfederateBlockedVideo(context.Background(), videoID); err != nil {
		t.Fatalf("UnfederateBlockedVideo: %v", err)
	}
	if len(repo.deliveries) != 0 || len(repo.tombstones) != 0 {
		t.Errorf("a never-federated video leaked a Delete/tombstone: %d / %d", len(repo.deliveries), len(repo.tombstones))
	}
}

func TestBlockedVideoIsNeverAnnouncedOrUpdated(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := blockedRepo(channelID, videoID, "public")
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.AnnounceVideo(context.Background(), videoID); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateVideo(context.Background(), videoID, true); err != nil {
		t.Fatal(err)
	}
	if len(repo.deliveries) != 0 {
		t.Errorf("a blocked video federated %d activities", len(repo.deliveries))
	}
}

func TestRefederateUnblockedVideoSendsCreate(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "public", "published", []string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.RefederateUnblockedVideo(context.Background(), videoID); err != nil {
		t.Fatal(err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		var a struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(d.row.Payload, &a)
		if a.Type != "Create" {
			t.Errorf("type = %q, want Create", a.Type)
		}
	}
}
