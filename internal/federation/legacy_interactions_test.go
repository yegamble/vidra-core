package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ratelimit"
)

func TestImportedLegacyVideoRatingsUndoAndViews(t *testing.T) {
	ctx := context.Background()
	repo := ratingRepo()
	legacy := uuid.New()
	repo.importedVideos = map[uuid.UUID]uuid.UUID{legacy: ctVideoID}
	legacyURL := contractBase + "/videos/watch/" + legacy.String()
	views := newFakeViews()
	svc := NewService(repo, WithBaseURL(contractBase), WithViewCounting(views.record, ratelimit.NewMemoryCounter()))
	for _, kind := range []string{"Like", "Dislike"} {
		if err := svc.HandleInbox(ctx, ctPTAccount, rateActivity(ctPTAccount+"/ratings/"+kind, kind, ctPTAccount, legacyURL)); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if got := ratingOf(repo, ctPTAccount); got != map[string]string{"Like": "like", "Dislike": "dislike"}[kind] {
			t.Fatalf("%s did not resolve to the imported video: %q", kind, got)
		}
	}
	undo := rateActivity(ctPTAccount+"/undo/legacy", "Undo", ctPTAccount,
		map[string]any{"type": "Dislike", "actor": ctPTAccount, "object": legacyURL})
	if err := svc.HandleInbox(ctx, ctPTAccount, undo); err != nil {
		t.Fatal(err)
	}
	if len(repo.remoteRatings) != 0 {
		t.Fatal("Undo by legacy video object left the imported video's vote behind")
	}
	if err := svc.HandleInbox(ctx, ctPTAccount, viewActivity(ctPTAccount+"/views/legacy", ctPTAccount, map[string]any{"object": legacyURL})); err != nil {
		t.Fatal(err)
	}
	if views.counts[ctVideoID] != 1 || len(views.counts) != 1 {
		t.Fatalf("legacy View counted against the wrong video: %v", views.counts)
	}
}

type failedLegacyLookupRepo struct {
	fakeRepo
	err error
}

func (r failedLegacyLookupRepo) GetVideoIDByLegacyUUID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, r.err
}

func TestLegacyInteractionLookupFailurePropagatesWithoutMutation(t *testing.T) {
	lookupErr := errors.New("temporary legacy lookup failure")
	legacyURL := contractBase + "/videos/watch/" + uuid.NewString()
	for _, kind := range []string{"Like", "Undo", "View"} {
		t.Run(kind, func(t *testing.T) {
			repo := ratingRepo()
			views := newFakeViews()
			svc := NewService(failedLegacyLookupRepo{fakeRepo: repo, err: lookupErr}, WithBaseURL(contractBase), WithViewCounting(views.record, ratelimit.NewMemoryCounter()))
			body := rateActivity(ctPTAccount+"/legacy/"+kind, kind, ctPTAccount, legacyURL)
			if kind == "Undo" {
				if err := NewService(repo, WithBaseURL(contractBase)).HandleInbox(context.Background(), ctPTAccount, rateActivity(rtLikeID, "Like", ctPTAccount, rtVideoURL)); err != nil {
					t.Fatal(err)
				}
				body = rateActivity(ctPTAccount+"/undo/legacy-error", kind, ctPTAccount,
					map[string]any{"id": rtLikeID, "type": "Like", "actor": ctPTAccount, "object": legacyURL})
			}
			if err := svc.HandleInbox(context.Background(), ctPTAccount, body); !errors.Is(err, lookupErr) {
				t.Fatalf("legacy lookup failure = %v, want retryable error %v", err, lookupErr)
			}
			if kind == "Undo" && ratingOf(repo, ctPTAccount) != "like" {
				t.Fatal("failed legacy lookup removed a vote")
			}
			if kind != "Undo" && len(repo.remoteRatings) != 0 || len(views.counts) != 0 {
				t.Fatal("failed legacy lookup changed ratings or views")
			}
		})
	}
}
