package rating

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory rating.Repository keyed by (user, video).
type fakeRepo struct {
	ratings map[[2]uuid.UUID]string
}

func newFakeRepo() *fakeRepo { return &fakeRepo{ratings: map[[2]uuid.UUID]string{}} }

func (f *fakeRepo) UpsertVideoRating(_ context.Context, a sqlcgen.UpsertVideoRatingParams) error {
	f.ratings[[2]uuid.UUID{a.UserID, a.VideoID}] = a.Rating
	return nil
}

func (f *fakeRepo) DeleteVideoRating(_ context.Context, a sqlcgen.DeleteVideoRatingParams) error {
	delete(f.ratings, [2]uuid.UUID{a.UserID, a.VideoID})
	return nil
}

func (f *fakeRepo) GetVideoRating(_ context.Context, a sqlcgen.GetVideoRatingParams) (string, error) {
	r, ok := f.ratings[[2]uuid.UUID{a.UserID, a.VideoID}]
	if !ok {
		return "", errors.New("no row")
	}
	return r, nil
}

func (f *fakeRepo) CountVideoRatings(_ context.Context, videoID uuid.UUID) (sqlcgen.CountVideoRatingsRow, error) {
	var row sqlcgen.CountVideoRatingsRow
	for k, v := range f.ratings {
		if k[1] != videoID {
			continue
		}
		switch v {
		case Like:
			row.Likes++
		case Dislike:
			row.Dislikes++
		}
	}
	return row, nil
}

func TestSetChangeAndClear(t *testing.T) {
	svc := NewService(newFakeRepo())
	video, ada, bob := uuid.New(), uuid.New(), uuid.New()

	// Ada likes.
	sum, err := svc.Set(context.Background(), video, ada, Like)
	if err != nil {
		t.Fatalf("Set like: %v", err)
	}
	if sum.Likes != 1 || sum.Dislikes != 0 || sum.Mine != Like {
		t.Fatalf("after like: %+v", sum)
	}

	// Bob dislikes.
	if _, err := svc.Set(context.Background(), video, bob, Dislike); err != nil {
		t.Fatalf("Set dislike: %v", err)
	}

	// Ada changes her mind to dislike (replaces, not adds).
	sum, _ = svc.Set(context.Background(), video, ada, Dislike)
	if sum.Likes != 0 || sum.Dislikes != 2 || sum.Mine != Dislike {
		t.Fatalf("after change: %+v", sum)
	}

	// Ada clears her rating.
	sum, _ = svc.Clear(context.Background(), video, ada)
	if sum.Likes != 0 || sum.Dislikes != 1 || sum.Mine != "" {
		t.Fatalf("after clear: %+v", sum)
	}
}

func TestSetRejectsInvalid(t *testing.T) {
	svc := NewService(newFakeRepo())
	if _, err := svc.Set(context.Background(), uuid.New(), uuid.New(), "love"); !errors.Is(err, ErrInvalidRating) {
		t.Errorf("Set(invalid) = %v, want ErrInvalidRating", err)
	}
}

func TestGetAnonymousHidesMine(t *testing.T) {
	svc := NewService(newFakeRepo())
	video, ada := uuid.New(), uuid.New()
	_, _ = svc.Set(context.Background(), video, ada, Like)

	// Anonymous viewer sees the count but not a personal rating.
	sum, err := svc.Get(context.Background(), video, uuid.UUID{}, false)
	if err != nil {
		t.Fatalf("Get anon: %v", err)
	}
	if sum.Likes != 1 || sum.Mine != "" {
		t.Errorf("anon summary: %+v", sum)
	}
}
