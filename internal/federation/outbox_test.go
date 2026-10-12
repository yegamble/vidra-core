package federation

import (
	"context"
	"encoding/json"
	"errors"
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
				Type         string          `json:"type"`
				Name         string          `json:"name"`
				ID           string          `json:"id"`
				AttributedTo json.RawMessage `json:"attributedTo"`
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
		if create.Object.ID != "https://videos.example/videos/"+videoID.String() {
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
		{"internal", "internal", "published", []string{"https://a.example/inbox"}},
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
	if err := svc.UpdateVideo(context.Background(), videoID, true); err != nil {
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
	if err := svc.UpdateVideo(context.Background(), videoID, true); err != nil {
		t.Fatalf("UpdateVideo: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1 (a video that WAS federated retracts)", len(repo.deliveries))
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

// public → internal is a withdrawal from the fediverse exactly like public →
// private: the video stays watchable here, but only to this instance's own
// signed-in accounts, so every peer that holds a copy must be told to drop it.
func TestUpdateVideoPublicToInternalSendsDelete(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "internal", "published", []string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.UpdateVideo(context.Background(), videoID, true); err != nil {
		t.Fatalf("UpdateVideo: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1 (the federated copy is retracted)", len(repo.deliveries))
	}
	for _, d := range repo.deliveries {
		var a struct {
			Type   string `json:"type"`
			Object string `json:"object"`
		}
		_ = json.Unmarshal(d.row.Payload, &a)
		if a.Type != "Delete" || a.Object != "https://videos.example/videos/"+videoID.String() {
			t.Errorf("activity = %+v, want Delete of the video", a)
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
		if a.Type != "Delete" || a.Object != "https://videos.example/videos/"+videoID.String() {
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

// A video that was NEVER public+published has never been federated, so no peer
// holds a copy of it and there is nothing to retract. Fanning a Delete out
// anyway would tell every follower instance the uuid of a video they were never
// shown — the A29 rehearsal measured exactly that: a PRIVATE upload finishing
// its transcode, and a metadata edit on a private video, each broadcast a
// Delete naming the hidden video's id. INT-05's own procedure says a private or
// unlisted video produces ZERO activities.
func TestUpdateVideoNeverFederatedSendsNothing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		privacy string
		state   string
	}{
		{"private published", "private", "published"},
		{"unlisted published", "unlisted", "published"},
		{"internal published", "internal", "published"},
		{"public draft", "public", "draft"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channelID, videoID := uuid.New(), uuid.New()
			repo := newOutboxRepo(channelID, videoID, tc.privacy, tc.state, []string{"https://a.example/inbox"})
			svc := NewService(repo, WithBaseURL("https://videos.example"))
			if err := svc.UpdateVideo(context.Background(), videoID, false); err != nil {
				t.Fatalf("UpdateVideo: %v", err)
			}
			if len(repo.deliveries) != 0 {
				t.Errorf("deliveries = %d, want 0 — a never-federated video must not be retracted", len(repo.deliveries))
			}
		})
	}
}

// The retraction gate must not swallow a REAL unfederation: a video that was
// public+published and has just gone private still has to be retracted, and the
// wasFederated flag is what separates the two cases.
func TestUpdateVideoStillPublicIgnoresWasFederated(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "public", "published", []string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.UpdateVideo(context.Background(), videoID, false); err != nil {
		t.Fatalf("UpdateVideo: %v", err)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1 — a public+published video updates regardless of the flag", len(repo.deliveries))
	}
}

// A video moved between channels is announced to the NEW channel's followers by
// the metadata Update, but a peer following only the OLD channel would keep a
// stale copy that later deletes never reach. RetractMovedVideo sends the old
// followers a Delete — except inboxes the new channel's Update already reaches.
func TestRetractMovedVideoDeletesForOldOnlyFollowers(t *testing.T) {
	oldCh, newCh, videoID := uuid.New(), uuid.New(), uuid.New()
	mk := func(newAP bool) fakeRepo {
		repo := newOutboxRepo(newCh, videoID, "public", "published", []string{"https://both.example/inbox", "https://new.example/inbox"})
		repo.channelsByID[oldCh] = sqlcgen.Channel{ID: oldCh, Handle: "old"}
		if !newAP {
			repo.apDisabled = map[uuid.UUID]bool{newCh: true}
		}
		repo.followerInboxes[oldCh] = []string{"https://both.example/inbox", "https://old.example/inbox"}
		return repo
	}
	inboxesOf := func(repo fakeRepo) (map[string]bool, string) {
		got, typ := map[string]bool{}, ""
		for _, d := range repo.deliveries {
			got[d.row.InboxUrl] = true
			var a struct {
				Type  string `json:"type"`
				Actor string `json:"actor"`
			}
			_ = json.Unmarshal(d.row.Payload, &a)
			typ = a.Type
			if a.Actor != "https://videos.example/video-channels/old" {
				t.Errorf("Delete actor = %q, want the OLD channel", a.Actor)
			}
		}
		return got, typ
	}

	repo := mk(true)
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.RetractMovedVideo(context.Background(), videoID, oldCh); err != nil {
		t.Fatalf("RetractMovedVideo: %v", err)
	}
	got, typ := inboxesOf(repo)
	if typ != "Delete" || len(got) != 1 || !got["https://old.example/inbox"] {
		t.Errorf("AP-enabled target: inboxes=%v type=%q, want only old.example", got, typ)
	}

	// Target channel opted out of AP: its Update never goes out, so NO old
	// follower is covered and all of them get the Delete.
	repo = mk(false)
	svc = NewService(repo, WithBaseURL("https://videos.example"))
	if err := svc.RetractMovedVideo(context.Background(), videoID, oldCh); err != nil {
		t.Fatalf("RetractMovedVideo: %v", err)
	}
	if got, _ := inboxesOf(repo); len(got) != 2 || !got["https://both.example/inbox"] || !got["https://old.example/inbox"] {
		t.Errorf("AP-disabled target: inboxes=%v, want both old followers", got)
	}
}

// A peer dereferencing an internal video's object URL learns nothing: it is
// answered exactly like a private one (404), never served as a Video object.
func TestVideoObjectInternalIsNotFound(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "internal", "published", nil)
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if _, err := svc.VideoObject(context.Background(), videoID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("VideoObject(internal) err = %v, want ErrNotFound", err)
	}
}

// F4 (T18 security review): the redeliver arm's own predicate. A Create queued
// while the video was public must not go out once it is internal — the video is
// still watchable on this instance, which is exactly the state a denylist
// (`!= "private"`) would mistake for publishable.
func TestVideoStillPublishableRefusesInternal(t *testing.T) {
	channelID, videoID := uuid.New(), uuid.New()
	repo := newOutboxRepo(channelID, videoID, "public", "published", []string{"https://a.example/inbox"})
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	if ok, err := svc.videoStillPublishable(context.Background(), videoID); err != nil || !ok {
		t.Fatalf("control: public video videoStillPublishable = %v, %v; want true", ok, err)
	}
	v := repo.videosByID[videoID]
	v.Privacy = "internal"
	repo.videosByID[videoID] = v
	if ok, err := svc.videoStillPublishable(context.Background(), videoID); err != nil || ok {
		t.Fatalf("internal video videoStillPublishable = %v, %v; want false", ok, err)
	}
}
