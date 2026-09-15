package authoredremotecomment

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository.
type fakeRepo struct {
	rows map[uuid.UUID]sqlcgen.AuthoredRemoteComment
}

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[uuid.UUID]sqlcgen.AuthoredRemoteComment{}} }

func (f *fakeRepo) CreateAuthoredRemoteComment(_ context.Context, a sqlcgen.CreateAuthoredRemoteCommentParams) (sqlcgen.AuthoredRemoteComment, error) {
	c := sqlcgen.AuthoredRemoteComment{
		ID: a.ID, RemoteVideoID: a.RemoteVideoID, UserID: a.UserID,
		Body: a.Body, ObjectUrl: a.ObjectUrl, InReplyTo: a.InReplyTo,
		DeliveryState: "pending",
	}
	f.rows[a.ID] = c
	return c, nil
}

func (f *fakeRepo) GetAuthoredRemoteComment(_ context.Context, id uuid.UUID) (sqlcgen.AuthoredRemoteComment, error) {
	if c, ok := f.rows[id]; ok {
		return c, nil
	}
	return sqlcgen.AuthoredRemoteComment{}, pgx.ErrNoRows
}

func (f *fakeRepo) ListAuthoredRemoteCommentsByVideo(_ context.Context, a sqlcgen.ListAuthoredRemoteCommentsByVideoParams) ([]sqlcgen.ListAuthoredRemoteCommentsByVideoRow, error) {
	var out []sqlcgen.ListAuthoredRemoteCommentsByVideoRow
	for _, c := range f.rows {
		if c.RemoteVideoID == a.RemoteVideoID {
			out = append(out, sqlcgen.ListAuthoredRemoteCommentsByVideoRow{
				ID: c.ID, RemoteVideoID: c.RemoteVideoID, UserID: c.UserID, Body: c.Body,
				ObjectUrl: c.ObjectUrl, InReplyTo: c.InReplyTo, DeliveryState: c.DeliveryState,
				Edited: c.Edited, AuthorUsername: "alice", AuthorDisplayName: "Alice",
			})
		}
	}
	return out, nil
}

func (f *fakeRepo) CountAuthoredRemoteCommentsByVideo(_ context.Context, a sqlcgen.CountAuthoredRemoteCommentsByVideoParams) (int64, error) {
	var n int64
	for _, c := range f.rows {
		if c.RemoteVideoID == a.RemoteVideoID {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) UpdateAuthoredRemoteCommentBody(_ context.Context, a sqlcgen.UpdateAuthoredRemoteCommentBodyParams) (sqlcgen.AuthoredRemoteComment, error) {
	c, ok := f.rows[a.ID]
	if !ok {
		return sqlcgen.AuthoredRemoteComment{}, pgx.ErrNoRows
	}
	c.Body = a.Body
	c.Edited = true
	c.DeliveryState = "pending"
	c.LastError = ""
	f.rows[a.ID] = c
	return c, nil
}

func (f *fakeRepo) DeleteAuthoredRemoteComment(_ context.Context, id uuid.UUID) (int64, error) {
	if _, ok := f.rows[id]; !ok {
		return 0, nil
	}
	delete(f.rows, id)
	return 1, nil
}

const testBaseURL = "https://home.example"

func TestCreateMintsObjectURLAndFiresHook(t *testing.T) {
	repo := newFakeRepo()
	var created uuid.UUID
	svc := NewService(repo,
		WithBaseURL(testBaseURL),
		WithCreateHook(func(_ context.Context, id uuid.UUID) { created = id }),
	)
	rvID, userID := uuid.New(), uuid.New()
	c, err := svc.Create(context.Background(), rvID, userID, "hi there", "https://peer.example/videos/abc")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if want := testBaseURL + "/remote-comments/" + c.ID.String(); c.ObjectUrl != want {
		t.Errorf("object_url = %q, want %q", c.ObjectUrl, want)
	}
	if c.InReplyTo != "https://peer.example/videos/abc" {
		t.Errorf("in_reply_to = %q", c.InReplyTo)
	}
	if c.DeliveryState != "pending" {
		t.Errorf("delivery_state = %q, want pending", c.DeliveryState)
	}
	if created != c.ID {
		t.Errorf("create hook not fired with the new id (got %v)", created)
	}
}

func TestEditOnlyByAuthor(t *testing.T) {
	repo := newFakeRepo()
	var updated uuid.UUID
	svc := NewService(repo, WithBaseURL(testBaseURL),
		WithUpdateHook(func(_ context.Context, id uuid.UUID) { updated = id }))
	author := uuid.New()
	c, _ := svc.Create(context.Background(), uuid.New(), author, "original", "https://peer.example/videos/abc")

	// A different user cannot edit.
	if _, err := svc.Edit(context.Background(), c.ID, uuid.New(), "hijack"); err != ErrForbidden {
		t.Fatalf("Edit by non-author = %v, want ErrForbidden", err)
	}
	// The author can, and it fires the update hook + marks edited.
	got, err := svc.Edit(context.Background(), c.ID, author, "revised")
	if err != nil {
		t.Fatalf("Edit by author: %v", err)
	}
	if got.Body != "revised" || !got.Edited {
		t.Errorf("edit result = %+v", got)
	}
	if updated != c.ID {
		t.Errorf("update hook not fired")
	}
	// Unknown id → ErrNotFound.
	if _, err := svc.Edit(context.Background(), uuid.New(), author, "x"); err != ErrNotFound {
		t.Errorf("Edit unknown = %v, want ErrNotFound", err)
	}
}

func TestDeleteAuthorOrModerator(t *testing.T) {
	repo := newFakeRepo()
	var deletedObjectURL string
	svc := NewService(repo, WithBaseURL(testBaseURL),
		WithDeleteHook(func(_ context.Context, _, _, _ uuid.UUID, objectURL string) { deletedObjectURL = objectURL }))
	author := uuid.New()
	c, _ := svc.Create(context.Background(), uuid.New(), author, "delete me", "https://peer.example/videos/abc")

	// A stranger cannot delete.
	if err := svc.Delete(context.Background(), c.ID, uuid.New(), false); err != ErrForbidden {
		t.Fatalf("Delete by stranger = %v, want ErrForbidden", err)
	}
	// A moderator can delete anyone's (home instance owns moderation).
	if err := svc.Delete(context.Background(), c.ID, uuid.New(), true); err != nil {
		t.Fatalf("Delete by moderator: %v", err)
	}
	if deletedObjectURL != c.ObjectUrl {
		t.Errorf("delete hook object url = %q, want %q", deletedObjectURL, c.ObjectUrl)
	}
	if _, err := repo.GetAuthoredRemoteComment(context.Background(), c.ID); err == nil {
		t.Errorf("comment still present after delete")
	}
}

func TestListByRemoteVideo(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, WithBaseURL(testBaseURL))
	rvID := uuid.New()
	for i := 0; i < 3; i++ {
		if _, err := svc.Create(context.Background(), rvID, uuid.New(), "c"+strings.Repeat("x", i), "https://peer.example/videos/abc"); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := svc.ListByRemoteVideo(context.Background(), rvID, uuid.New(), true, 100, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Errorf("total=%d rows=%d, want 3/3", total, len(rows))
	}
}
