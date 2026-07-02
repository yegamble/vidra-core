package federation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func newOutboxRepo(channelID, videoID uuid.UUID, privacy, state string, inboxes []string) fakeRepo {
	return fakeRepo{
		videosByID: map[uuid.UUID]sqlcgen.GetVideoByIDRow{
			videoID: {ID: videoID, ChannelID: channelID, Title: "My Clip", Description: "hello", Privacy: privacy, State: state},
		},
		channelsByID:    map[uuid.UUID]sqlcgen.Channel{channelID: {ID: channelID, Handle: "films"}},
		followerInboxes: map[uuid.UUID][]string{channelID: inboxes},
		deliveries:      map[uuid.UUID]*fakeDelivery{},
	}
}

func TestAnnounceVideoFansOutToFollowers(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "public", "published",
		[]string{"https://a.example/inbox", "https://b.example/inbox"})
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	if err := svc.AnnounceVideo(context.Background(), videoID); err != nil {
		t.Fatalf("AnnounceVideo: %v", err)
	}
	if len(repo.deliveries) != 2 {
		t.Fatalf("enqueued deliveries = %d, want 2 (one per follower inbox)", len(repo.deliveries))
	}
	// Inspect one payload: a Create wrapping the Video, from the channel actor.
	for _, d := range repo.deliveries {
		var create struct {
			Type   string `json:"type"`
			Actor  string `json:"actor"`
			Object struct {
				Type         string `json:"type"`
				Name         string `json:"name"`
				ID           string `json:"id"`
				AttributedTo string `json:"attributedTo"`
			} `json:"object"`
		}
		if err := json.Unmarshal(d.row.Payload, &create); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if create.Type != "Create" || create.Actor != "https://videos.example/video-channels/films" {
			t.Errorf("activity = %+v", create)
		}
		if create.Object.Type != "Video" || create.Object.Name != "My Clip" {
			t.Errorf("object = %+v", create.Object)
		}
		if create.Object.ID != "https://videos.example/videos/watch/"+videoID.String() {
			t.Errorf("object id = %q", create.Object.ID)
		}
		break
	}
}

func TestAnnounceVideoSkips(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	cases := []struct {
		name           string
		privacy, state string
		inboxes        []string
	}{
		{"private", "private", "published", []string{"https://a.example/inbox"}},
		{"not published", "public", "failed", []string{"https://a.example/inbox"}},
		{"no followers", "public", "published", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newOutboxRepo(channelID, videoID, tc.privacy, tc.state, tc.inboxes)
			svc := NewService(repo, WithBaseURL("https://videos.example"))
			if err := svc.AnnounceVideo(context.Background(), videoID); err != nil {
				t.Fatalf("AnnounceVideo: %v", err)
			}
			if len(repo.deliveries) != 0 {
				t.Errorf("deliveries = %d, want 0", len(repo.deliveries))
			}
		})
	}
}

func TestUpdateVideoSendsUpdate(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "public", "published", []string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.UpdateVideo(context.Background(), videoID); err != nil {
		t.Fatalf("UpdateVideo: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		var a struct {
			Type   string `json:"type"`
			Object struct {
				Type string `json:"type"`
			} `json:"object"`
		}
		_ = json.Unmarshal(d.row.Payload, &a)
		if a.Type != "Update" || a.Object.Type != "Video" {
			t.Errorf("activity = %+v, want Update{Video}", a)
		}
	}
}

func TestUpdateVideoGonePublicSendsDelete(t *testing.T) {
	// A video that is no longer public+published (e.g. went private) unfederates
	// via a Delete.
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "private", "published", []string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.UpdateVideo(context.Background(), videoID); err != nil {
		t.Fatalf("UpdateVideo: %v", err)
	}
	for _, d := range repo.deliveries {
		var a struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(d.row.Payload, &a)
		if a.Type != "Delete" {
			t.Errorf("private video should unfederate via Delete, got %q", a.Type)
		}
	}
}

func TestDeleteVideoSendsDelete(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := fakeRepo{
		channelsByID:    map[uuid.UUID]sqlcgen.Channel{channelID: {ID: channelID, Handle: "films"}},
		followerInboxes: map[uuid.UUID][]string{channelID: {"https://a.example/inbox"}},
		deliveries:      map[uuid.UUID]*fakeDelivery{},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.DeleteVideo(context.Background(), videoID, channelID, true); err != nil {
		t.Fatalf("DeleteVideo: %v", err)
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
		if a.Type != "Delete" || a.Object != "https://videos.example/videos/watch/"+videoID.String() {
			t.Errorf("activity = %+v, want Delete of the video URL", a)
		}
	}
}

func TestDeleteVideoNonPublicIsNoop(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := fakeRepo{
		channelsByID:    map[uuid.UUID]sqlcgen.Channel{channelID: {ID: channelID, Handle: "films"}},
		followerInboxes: map[uuid.UUID][]string{channelID: {"https://a.example/inbox"}},
		deliveries:      map[uuid.UUID]*fakeDelivery{},
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.DeleteVideo(context.Background(), videoID, channelID, false); err != nil {
		t.Fatalf("DeleteVideo: %v", err)
	}
	if len(repo.deliveries) != 0 {
		t.Errorf("deliveries = %d, want 0 (never-public video not federated)", len(repo.deliveries))
	}
}

func TestAnnounceVideoUnknownIsNoop(t *testing.T) {
	svc := NewService(fakeRepo{videosByID: map[uuid.UUID]sqlcgen.GetVideoByIDRow{}}, WithBaseURL("https://videos.example"))
	if err := svc.AnnounceVideo(context.Background(), uuid.New()); err != nil {
		t.Errorf("AnnounceVideo unknown video should be a no-op, got %v", err)
	}
}
