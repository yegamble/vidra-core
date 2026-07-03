package comment

// Federation-hook + remote-attribution behavior (remote-content §6): the
// create/edit/delete seams fire for LOCAL comments only, and list projections
// flag remote-authored rows with their origin identity.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedRemoteComment plants a remote-authored comment row directly in the fake
// repo (the write path for those lives in internal/federation).
func seedRemoteComment(repo *fakeRepo, videoID uuid.UUID) sqlcgen.Comment {
	actor := "https://tube.example/video-channels/movies"
	name := "movies"
	obj := "https://tube.example/comments/9001"
	c := sqlcgen.Comment{
		ID: uuid.New(), VideoID: videoID, Body: "remote hello",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		RemoteActorUrl: &actor, RemoteAuthorName: &name, RemoteObjectUrl: &obj,
	}
	repo.comments[c.ID] = c
	return c
}

func TestHooksFireForLocalComments(t *testing.T) {
	repo := newFakeRepo()
	var created, updated []uuid.UUID
	type deleted struct{ commentID, videoID, authorID uuid.UUID }
	var deletes []deleted
	svc := NewService(repo,
		WithCreateHook(func(_ context.Context, id uuid.UUID) { created = append(created, id) }),
		WithUpdateHook(func(_ context.Context, id uuid.UUID) { updated = append(updated, id) }),
		WithDeleteHook(func(_ context.Context, cid, vid, aid uuid.UUID) {
			deletes = append(deletes, deleted{cid, vid, aid})
		}),
	)
	ctx := context.Background()
	videoID, author := uuid.New(), uuid.New()

	c, err := svc.Create(ctx, videoID, author, "hi", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(created) != 1 || created[0] != c.ID {
		t.Errorf("create hook fired %v, want [%s]", created, c.ID)
	}
	if _, err := svc.Edit(ctx, c.ID, author, "hi again"); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if len(updated) != 1 || updated[0] != c.ID {
		t.Errorf("update hook fired %v, want [%s]", updated, c.ID)
	}
	if err := svc.Delete(ctx, c.ID, author, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deletes) != 1 || deletes[0] != (deleted{c.ID, videoID, author}) {
		t.Errorf("delete hook fired %+v, want [{%s %s %s}]", deletes, c.ID, videoID, author)
	}

	// A failed edit (wrong author) must not fire the update hook.
	c2, _ := svc.Create(ctx, videoID, author, "second", nil)
	if _, err := svc.Edit(ctx, c2.ID, uuid.New(), "hacked"); err != ErrForbidden {
		t.Fatalf("foreign edit = %v, want ErrForbidden", err)
	}
	if len(updated) != 1 {
		t.Errorf("update hook fired on a forbidden edit")
	}
}

func TestDeleteHookSkippedForRemoteComments(t *testing.T) {
	repo := newFakeRepo()
	fired := 0
	svc := NewService(repo, WithDeleteHook(func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) { fired++ }))
	rc := seedRemoteComment(repo, uuid.New())

	// A moderator deletes the remote comment: local-only, no federated Delete.
	if err := svc.Delete(context.Background(), rc.ID, uuid.New(), true); err != nil {
		t.Fatalf("moderator delete: %v", err)
	}
	if fired != 0 {
		t.Errorf("delete hook fired %d times for a remote comment, want 0", fired)
	}
	// A non-moderator can never delete it (no local author to match).
	rc2 := seedRemoteComment(repo, uuid.New())
	if err := svc.Delete(context.Background(), rc2.ID, uuid.New(), false); err != ErrForbidden {
		t.Errorf("non-moderator delete of remote comment = %v, want ErrForbidden", err)
	}
}

func TestRemoteCommentsAreNeverEditable(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	rc := seedRemoteComment(repo, uuid.New())
	if _, err := svc.Edit(context.Background(), rc.ID, uuid.New(), "hijack"); err != ErrForbidden {
		t.Errorf("edit of remote comment = %v, want ErrForbidden", err)
	}
}

func TestListByVideoFlagsRemoteRows(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	videoID, user := uuid.New(), uuid.New()
	repo.authors[user] = author{"ada", "Ada Makes"}
	if _, err := svc.Create(context.Background(), videoID, user, "local", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rc := seedRemoteComment(repo, videoID)

	// The fake list mirrors the SQL projection for remote rows.
	repo.remoteRows = map[uuid.UUID]remoteRowMeta{rc.ID: {name: "movies", domain: "tube.example"}}

	items, err := svc.ListByVideo(context.Background(), videoID, uuid.Nil, false, 20, 0)
	if err != nil {
		t.Fatalf("ListByVideo: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d comments, want 2", len(items))
	}
	var local, remote *WithAuthor
	for i := range items {
		if items[i].Remote {
			remote = &items[i]
		} else {
			local = &items[i]
		}
	}
	if local == nil || remote == nil {
		t.Fatalf("want one local + one remote row, got %+v", items)
	}
	if local.AuthorUsername != "ada" || local.AuthorDomain != "" {
		t.Errorf("local row = %+v", *local)
	}
	if remote.AuthorUsername != "movies" || remote.AuthorDomain != "tube.example" {
		t.Errorf("remote row = %+v", *remote)
	}
	if remote.Comment.UserID.Valid {
		t.Error("remote comment must have no local author id")
	}
}
