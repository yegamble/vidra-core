package comment

import (
	"context"
	"errors"
	"sort"
	"strings"
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
		ParentID: a.ParentID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
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

func (f *fakeRepo) ListAdminComments(_ context.Context, a sqlcgen.ListAdminCommentsParams) ([]sqlcgen.ListAdminCommentsRow, error) {
	var rows []sqlcgen.ListAdminCommentsRow
	for _, c := range f.comments {
		if a.Query != nil && !strings.Contains(strings.ToLower(c.Body), strings.ToLower(*a.Query)) {
			continue
		}
		au := f.authors[c.UserID]
		rows = append(rows, sqlcgen.ListAdminCommentsRow{
			ID: c.ID, VideoID: c.VideoID, Body: c.Body, CreatedAt: c.CreatedAt,
			AuthorUsername: au.username, AuthorDisplayName: au.displayName,
		})
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

func (f *fakeRepo) UpdateComment(_ context.Context, a sqlcgen.UpdateCommentParams) (sqlcgen.Comment, error) {
	c, ok := f.comments[a.ID]
	if !ok {
		return sqlcgen.Comment{}, errors.New("not found")
	}
	c.Body = a.Body
	c.UpdatedAt = time.Now()
	f.comments[a.ID] = c
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

	if _, err := svc.Create(context.Background(), video, user, "first!", nil); err != nil {
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

func TestCreateReply(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	ctx := context.Background()
	video, other, user := uuid.New(), uuid.New(), uuid.New()
	repo.authors[user] = author{"ada", "Ada Makes"}

	parent, err := svc.Create(ctx, video, user, "top-level", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}

	// A reply to a comment on the same video is accepted and records parent_id.
	reply, err := svc.Create(ctx, video, user, "a reply", &parent.ID)
	if err != nil {
		t.Fatalf("Create reply: %v", err)
	}
	if !reply.ParentID.Valid || uuid.UUID(reply.ParentID.Bytes) != parent.ID {
		t.Errorf("reply parent_id = %+v, want %s", reply.ParentID, parent.ID)
	}

	// A reply whose parent is unknown is rejected.
	unknown := uuid.New()
	if _, err := svc.Create(ctx, video, user, "orphan", &unknown); err != ErrParentNotFound {
		t.Errorf("reply to unknown parent = %v, want ErrParentNotFound", err)
	}

	// A reply cannot point at a comment on a different video.
	otherParent, _ := svc.Create(ctx, other, user, "elsewhere", nil)
	if _, err := svc.Create(ctx, video, user, "cross-video", &otherParent.ID); err != ErrParentNotFound {
		t.Errorf("cross-video reply = %v, want ErrParentNotFound", err)
	}
}

func TestListForAdmin(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	user := uuid.New()
	repo.authors[user] = author{"ada", "Ada Makes"}
	ctx := context.Background()
	_, _ = svc.Create(ctx, uuid.New(), user, "hello world", nil)
	_, _ = svc.Create(ctx, uuid.New(), user, "spam spam", nil)

	all, err := svc.ListForAdmin(ctx, "", 20, 0)
	if err != nil {
		t.Fatalf("ListForAdmin: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all comments = %d, want 2", len(all))
	}
	if all[0].AuthorUsername != "ada" {
		t.Errorf("author = %q, want ada", all[0].AuthorUsername)
	}
	// The body filter narrows the result.
	filtered, _ := svc.ListForAdmin(ctx, "spam", 20, 0)
	if len(filtered) != 1 || filtered[0].Body != "spam spam" {
		t.Errorf("q=spam = %+v, want only [spam spam]", filtered)
	}
}

func TestDeleteOnlyByAuthor(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	authorID := uuid.New()
	c, _ := svc.Create(context.Background(), uuid.New(), authorID, "x", nil)

	if err := svc.Delete(context.Background(), c.ID, uuid.New(), false); err != ErrForbidden {
		t.Errorf("non-author delete = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(context.Background(), uuid.New(), authorID, false); err != ErrNotFound {
		t.Errorf("unknown delete = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(context.Background(), c.ID, authorID, false); err != nil {
		t.Errorf("author delete = %v, want nil", err)
	}
	if items, _ := svc.ListByVideo(context.Background(), c.VideoID, uuid.Nil, false, 20, 0); len(items) != 0 {
		t.Errorf("comment should be deleted, still %d", len(items))
	}
}

func TestModeratorCanDeleteAnyComment(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	c, _ := svc.Create(context.Background(), uuid.New(), uuid.New(), "x", nil)

	// A non-author moderator may delete it.
	if err := svc.Delete(context.Background(), c.ID, uuid.New(), true); err != nil {
		t.Errorf("moderator delete = %v, want nil", err)
	}
	// An unknown id is still ErrNotFound, even for a moderator.
	if err := svc.Delete(context.Background(), uuid.New(), uuid.New(), true); err != ErrNotFound {
		t.Errorf("moderator delete unknown = %v, want ErrNotFound", err)
	}
}

func TestEditComment(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	ctx := context.Background()
	author, other := uuid.New(), uuid.New()

	c, err := svc.Create(ctx, uuid.New(), author, "original", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The author edits their own comment.
	edited, err := svc.Edit(ctx, c.ID, author, "revised")
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.Body != "revised" {
		t.Errorf("body = %q, want revised", edited.Body)
	}
	if !edited.UpdatedAt.After(edited.CreatedAt) {
		t.Errorf("updated_at %v should be after created_at %v", edited.UpdatedAt, edited.CreatedAt)
	}

	// Another user cannot edit it (moderators delete, not edit — no isModerator path).
	if _, err := svc.Edit(ctx, c.ID, other, "hacked"); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-author edit = %v, want ErrForbidden", err)
	}
	// An unknown comment is ErrNotFound.
	if _, err := svc.Edit(ctx, uuid.New(), author, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown edit = %v, want ErrNotFound", err)
	}
}
