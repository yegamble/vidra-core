package comment

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type author struct{ username, displayName string }

type fakeRepo struct {
	comments map[uuid.UUID]sqlcgen.Comment
	authors  map[uuid.UUID]author // user_id -> author identity
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{comments: map[uuid.UUID]sqlcgen.Comment{}, authors: map[uuid.UUID]author{}}
}

func (f *fakeRepo) CreateComment(_ context.Context, a sqlcgen.CreateCommentParams) (sqlcgen.Comment, error) {
	c := sqlcgen.Comment{
		ID: uuid.New(), VideoID: a.VideoID, UserID: a.UserID, Body: a.Body,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.comments[c.ID] = c
	return c, nil
}

func (f *fakeRepo) ListCommentsByVideo(_ context.Context, a sqlcgen.ListCommentsByVideoParams) ([]sqlcgen.ListCommentsByVideoRow, error) {
	var rows []sqlcgen.ListCommentsByVideoRow
	for _, c := range f.comments {
		if c.VideoID == a.VideoID {
			au := f.authors[c.UserID]
			rows = append(rows, sqlcgen.ListCommentsByVideoRow{
				ID: c.ID, VideoID: c.VideoID, UserID: c.UserID, Body: c.Body,
				CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
				AuthorUsername: au.username, AuthorDisplayName: au.displayName,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func (f *fakeRepo) GetComment(_ context.Context, id uuid.UUID) (sqlcgen.Comment, error) {
	c, ok := f.comments[id]
	if !ok {
		return sqlcgen.Comment{}, errors.New("not found")
	}
	return c, nil
}

func (f *fakeRepo) DeleteComment(_ context.Context, id uuid.UUID) error {
	delete(f.comments, id)
	return nil
}

func TestCreateAndListByVideo(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	video, user := uuid.New(), uuid.New()
	repo.authors[user] = author{"ada", "Ada Makes"}

	if _, err := svc.Create(context.Background(), video, user, "first!"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	items, err := svc.ListByVideo(context.Background(), video, uuid.Nil, false, 20, 0)
	if err != nil {
		t.Fatalf("ListByVideo: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d comments, want 1", len(items))
	}
	if items[0].Comment.Body != "first!" || items[0].AuthorUsername != "ada" || items[0].AuthorDisplayName != "Ada Makes" {
		t.Errorf("unexpected comment view: %+v", items[0])
	}
}

func TestDeleteOnlyByAuthor(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	authorID := uuid.New()
	c, _ := svc.Create(context.Background(), uuid.New(), authorID, "x")

	if err := svc.Delete(context.Background(), c.ID, uuid.New()); err != ErrForbidden {
		t.Errorf("non-author delete = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(context.Background(), uuid.New(), authorID); err != ErrNotFound {
		t.Errorf("unknown delete = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(context.Background(), c.ID, authorID); err != nil {
		t.Errorf("author delete = %v, want nil", err)
	}
	if items, _ := svc.ListByVideo(context.Background(), c.VideoID, uuid.Nil, false, 20, 0); len(items) != 0 {
		t.Errorf("comment should be deleted, still %d", len(items))
	}
}
