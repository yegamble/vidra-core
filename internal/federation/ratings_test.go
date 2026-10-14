package federation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The fake mirrors remote_video_ratings (0178): one row per (actor, video), an
// upsert replacing it in place, and the Undo delete matching either the
// (video, kind) pair or the rating's own activity id.
func (f fakeRepo) UpsertRemoteVideoRating(_ context.Context, arg sqlcgen.UpsertRemoteVideoRatingParams) error {
	if f.remoteRatings == nil {
		return errors.New("fake: remoteRatings not seeded")
	}
	f.remoteRatings[arg.RemoteActorUrl+"|"+arg.VideoID.String()] = &arg
	return nil
}

func (f fakeRepo) DeleteRemoteVideoRating(_ context.Context, arg sqlcgen.DeleteRemoteVideoRatingParams) error {
	for k, r := range f.remoteRatings {
		if r.RemoteActorUrl != arg.RemoteActorUrl {
			continue
		}
		if (r.VideoID == arg.VideoID && r.Rating == arg.Rating) || r.ActivityUrl == arg.ActivityUrl {
			delete(f.remoteRatings, k)
		}
	}
	return nil
}

const (
	rtVideoURL = contractBase + "/videos/watch/7a1d9e42-5b3c-4f8e-a6d0-9c8b7a6f5e4d"
	rtLikeID   = ctPTAccount + "/likes/77"
)

func ratingRepo() fakeRepo {
	r := newContractRepo()
	cacheContractActor(r, ctPTAccount, "Person", "marta", "peertube.example")
	return r
}

func rateActivity(id, typ, actor string, object any) []byte {
	b, _ := json.Marshal(map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       id, "type": typ, "actor": actor, "object": object,
	})
	return b
}

func ratingOf(r fakeRepo, actor string) string {
	if row, ok := r.remoteRatings[actor+"|"+ctVideoID.String()]; ok {
		return row.Rating
	}
	return ""
}

func TestInboundLikeDislikeUndoOnLocalVideo(t *testing.T) {
	ctx := context.Background()
	repo := ratingRepo()
	svc := NewService(repo, WithBaseURL(contractBase))
	send := func(body []byte) {
		t.Helper()
		if err := svc.HandleInbox(ctx, ctPTAccount, body); err != nil {
			t.Fatalf("HandleInbox: %v", err)
		}
	}

	send(rateActivity(rtLikeID, "Like", ctPTAccount, rtVideoURL))
	if got := ratingOf(repo, ctPTAccount); got != "like" {
		t.Fatalf("after Like: rating = %q, want like", got)
	}
	// A second Like (new activity id) is still ONE vote.
	send(rateActivity(ctPTAccount+"/likes/78", "Like", ctPTAccount, rtVideoURL))
	if len(repo.remoteRatings) != 1 {
		t.Fatalf("repeat Like stored %d rows, want 1", len(repo.remoteRatings))
	}
	// PeerTube's Dislike switches the same row.
	dislikeID := ctPTAccount + "/dislikes/79"
	send(rateActivity(dislikeID, "Dislike", ctPTAccount, rtVideoURL))
	if got := ratingOf(repo, ctPTAccount); got != "dislike" {
		t.Fatalf("after Dislike: rating = %q, want dislike", got)
	}
	// Undo{Like} no longer matches the vote: nothing changes.
	send(rateActivity(ctPTAccount+"/undo/1", "Undo", ctPTAccount,
		map[string]any{"id": rtLikeID, "type": "Like", "actor": ctPTAccount, "object": rtVideoURL}))
	if got := ratingOf(repo, ctPTAccount); got != "dislike" {
		t.Fatalf("stale Undo{Like} changed the vote: %q", got)
	}
	// Undo{Dislike} removes it.
	send(rateActivity(ctPTAccount+"/undo/2", "Undo", ctPTAccount,
		map[string]any{"id": dislikeID, "type": "Dislike", "actor": ctPTAccount, "object": rtVideoURL}))
	if len(repo.remoteRatings) != 0 {
		t.Fatalf("Undo{Dislike} left %d rows", len(repo.remoteRatings))
	}
}

func TestInboundUndoRatingByBareActivityID(t *testing.T) {
	ctx := context.Background()
	repo := ratingRepo()
	svc := NewService(repo, WithBaseURL(contractBase))
	if err := svc.HandleInbox(ctx, ctPTAccount, rateActivity(rtLikeID, "Like", ctPTAccount, rtVideoURL)); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleInbox(ctx, ctPTAccount, rateActivity(ctPTAccount+"/undo/3", "Undo", ctPTAccount, rtLikeID)); err != nil {
		t.Fatal(err)
	}
	if len(repo.remoteRatings) != 0 {
		t.Fatalf("Undo naming the Like by id left %d rows", len(repo.remoteRatings))
	}
}

func TestInboundRatingRefusals(t *testing.T) {
	other := "https://peertube.example/accounts/olga"
	cases := []struct {
		name    string
		signer  string
		body    []byte
		seed    func(r fakeRepo)
		wantErr error
	}{
		{
			name:    "actor is not the signer",
			signer:  other,
			body:    rateActivity(rtLikeID, "Like", ctPTAccount, rtVideoURL),
			seed:    func(r fakeRepo) { cacheContractActor(r, other, "Person", "olga", "peertube.example") },
			wantErr: ErrActorMismatch,
		},
		{
			name:    "Undo of another actor's Like",
			signer:  other,
			body:    rateActivity(other+"/undo/1", "Undo", other, map[string]any{"id": rtLikeID, "type": "Like", "actor": ctPTAccount, "object": rtVideoURL}),
			seed:    func(r fakeRepo) { cacheContractActor(r, other, "Person", "olga", "peertube.example") },
			wantErr: ErrActorMismatch,
		},
		{
			name:    "activity id on another host",
			signer:  ctPTAccount,
			body:    rateActivity("https://elsewhere.example/likes/1", "Like", ctPTAccount, rtVideoURL),
			wantErr: ErrBadResource,
		},
		{
			name:    "blocked instance",
			signer:  ctPTAccount,
			body:    rateActivity(rtLikeID, "Like", ctPTAccount, rtVideoURL),
			seed:    func(r fakeRepo) { r.blockedDomains["peertube.example"] = true },
			wantErr: ErrSenderBlocked,
		},
		{
			name:    "actor blocked instance-wide",
			signer:  ctPTAccount,
			body:    rateActivity(rtLikeID, "Like", ctPTAccount, rtVideoURL),
			seed:    func(r fakeRepo) { r.adminActorBlocks[ctPTAccount] = "spam" },
			wantErr: ErrSenderBlocked,
		},
		{
			name:   "video owner blocks the actor",
			signer: ctPTAccount,
			body:   rateActivity(rtLikeID, "Like", ctPTAccount, rtVideoURL),
			seed:   func(r fakeRepo) { r.remoteBlocks[ctUserID.String()+"|"+ctPTAccount] = true },
		},
		{
			name:   "private video",
			signer: ctPTAccount,
			body:   rateActivity(rtLikeID, "Like", ctPTAccount, rtVideoURL),
			seed: func(r fakeRepo) {
				v := r.videosByID[ctVideoID]
				v.Privacy = "private"
				r.videosByID[ctVideoID] = v
			},
		},
		{
			name:   "moderator-blocked video",
			signer: ctPTAccount,
			body:   rateActivity(rtLikeID, "Like", ctPTAccount, rtVideoURL),
			seed: func(r fakeRepo) {
				v := r.videosByID[ctVideoID]
				v.Blocked = true
				r.videosByID[ctVideoID] = v
			},
		},
		{
			name:   "unknown local video",
			signer: ctPTAccount,
			body:   rateActivity(rtLikeID, "Like", ctPTAccount, contractBase+"/videos/watch/"+uuid.NewString()),
		},
		{
			name:   "not one of our videos",
			signer: ctPTAccount,
			body:   rateActivity(rtLikeID, "Like", ctPTAccount, ctPTVideo),
		},
		{
			name:   "a non-Person actor",
			signer: ctPTChannel,
			body:   rateActivity(ctPTChannel+"/likes/1", "Like", ctPTChannel, rtVideoURL),
			seed:   func(r fakeRepo) { cacheContractActor(r, ctPTChannel, "Group", "kayak_diaries", "peertube.example") },
		},
		{
			name:   "an uncached actor",
			signer: other,
			body:   rateActivity(other+"/likes/1", "Like", other, rtVideoURL),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := ratingRepo()
			repo.adminActorBlocks = map[string]string{}
			repo.remoteBlocks = map[string]bool{}
			// Every refusal case starts from a vote that must survive, so an
			// Undo refusal is proven as well as a Like refusal.
			repo.remoteRatings[ctPTAccount+"|"+ctVideoID.String()] = &sqlcgen.UpsertRemoteVideoRatingParams{
				VideoID: ctVideoID, RemoteActorUrl: ctPTAccount, Rating: "dislike", ActivityUrl: ctPTAccount + "/dislikes/1",
			}
			if tc.seed != nil {
				tc.seed(repo)
			}
			svc := NewService(repo, WithBaseURL(contractBase))
			err := svc.HandleInbox(context.Background(), tc.signer, tc.body)
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			} else if tc.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want accept-and-ignore", err)
			}
			if len(repo.remoteRatings) != 1 || ratingOf(repo, ctPTAccount) != "dislike" {
				t.Fatalf("refused activity changed the ratings: %+v", repo.remoteRatings)
			}
		})
	}
}
