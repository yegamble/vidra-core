package comment

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type author struct{ username, displayName string }

// remoteRowMeta mirrors the remote-attribution columns the list SQL projects
// for a remote-authored comment (remote_author_name + origin domain).
type remoteRowMeta struct{ name, domain string }

type fakeRepo struct {
	comments   map[uuid.UUID]sqlcgen.Comment
	authors    map[uuid.UUID]author // user_id -> author identity
	remoteRows map[uuid.UUID]remoteRowMeta
	pinned     map[uuid.UUID]uuid.UUID // video_id -> pinned comment_id (0099)
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		comments: map[uuid.UUID]sqlcgen.Comment{},
		authors:  map[uuid.UUID]author{},
		pinned:   map[uuid.UUID]uuid.UUID{},
	}
}

func (f *fakeRepo) CreateComment(_ context.Context, a sqlcgen.CreateCommentParams) (sqlcgen.Comment, error) {
	c := sqlcgen.Comment{
		ID: uuid.New(), VideoID: a.VideoID, UserID: a.UserID, Body: a.Body,
		ParentID: a.ParentID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		HeldForReview: a.HeldForReview,
	}
	f.comments[c.ID] = c
	return c, nil
}

// ApproveComment mirrors the conditional UPDATE: only a still-held row is
// released; anything else is pgx.ErrNoRows (the "nothing to do" answer).
func (f *fakeRepo) ApproveComment(_ context.Context, id uuid.UUID) (sqlcgen.Comment, error) {
	c, ok := f.comments[id]
	if !ok || !c.HeldForReview {
		return sqlcgen.Comment{}, pgx.ErrNoRows
	}
	c.HeldForReview = false
	f.comments[id] = c
	return c, nil
}

func (f *fakeRepo) ListCommentsByVideo(_ context.Context, a sqlcgen.ListCommentsByVideoParams) ([]sqlcgen.ListCommentsByVideoRow, error) {
	var rows []sqlcgen.ListCommentsByVideoRow
	for _, c := range f.comments {
		if c.VideoID == a.VideoID {
			au := f.authors[uuid.UUID(c.UserID.Bytes)]
			meta := f.remoteRows[c.ID]
			rows = append(rows, sqlcgen.ListCommentsByVideoRow{
				ID: c.ID, VideoID: c.VideoID, UserID: c.UserID, Body: c.Body,
				ParentID: c.ParentID, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
				AuthorUsername: au.username, AuthorDisplayName: au.displayName,
				RemoteActorUrl: c.RemoteActorUrl, RemoteAuthorName: meta.name, AuthorDomain: meta.domain,
				Hearted: c.Hearted, Pinned: f.pinned[c.VideoID] == c.ID,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Pinned != rows[j].Pinned {
			return rows[i].Pinned
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	return rows, nil
}

func (f *fakeRepo) GetCommentWithMeta(_ context.Context, id uuid.UUID) (sqlcgen.GetCommentWithMetaRow, error) {
	c, ok := f.comments[id]
	if !ok {
		return sqlcgen.GetCommentWithMetaRow{}, errors.New("not found")
	}
	au := f.authors[uuid.UUID(c.UserID.Bytes)]
	meta := f.remoteRows[c.ID]
	return sqlcgen.GetCommentWithMetaRow{
		ID: c.ID, VideoID: c.VideoID, UserID: c.UserID, Body: c.Body,
		ParentID: c.ParentID, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		DeletedAt: c.DeletedAt, Hearted: c.Hearted, HeldForReview: c.HeldForReview,
		Pinned:         f.pinned[c.VideoID] == c.ID,
		AuthorUsername: au.username, AuthorDisplayName: au.displayName,
		RemoteActorUrl: c.RemoteActorUrl, RemoteAuthorName: meta.name, AuthorDomain: meta.domain,
	}, nil
}

func (f *fakeRepo) SetCommentHearted(_ context.Context, a sqlcgen.SetCommentHeartedParams) (sqlcgen.Comment, error) {
	c, ok := f.comments[a.ID]
	if !ok {
		return sqlcgen.Comment{}, errors.New("not found")
	}
	c.Hearted = a.Hearted
	f.comments[a.ID] = c
	return c, nil
}

func (f *fakeRepo) SetVideoPinnedComment(_ context.Context, a sqlcgen.SetVideoPinnedCommentParams) error {
	if a.PinnedCommentID.Valid {
		f.pinned[a.VideoID] = uuid.UUID(a.PinnedCommentID.Bytes)
	} else {
		delete(f.pinned, a.VideoID)
	}
	return nil
}

func (f *fakeRepo) ListAdminComments(_ context.Context, a sqlcgen.ListAdminCommentsParams) ([]sqlcgen.ListAdminCommentsRow, error) {
	var rows []sqlcgen.ListAdminCommentsRow
	for _, c := range f.comments {
		if a.Query != nil && !strings.Contains(strings.ToLower(c.Body), strings.ToLower(*a.Query)) {
			continue
		}
		au := f.authors[uuid.UUID(c.UserID.Bytes)]
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

func (f *fakeRepo) GetCommentAuthorIDByUsername(_ context.Context, username string) (uuid.UUID, error) {
	for id, a := range f.authors {
		if strings.EqualFold(a.username, username) {
			return id, nil
		}
	}
	return uuid.Nil, errors.New("not found")
}

func (f *fakeRepo) DeleteCommentsByAuthor(_ context.Context, a sqlcgen.DeleteCommentsByAuthorParams) ([]sqlcgen.DeleteCommentsByAuthorRow, error) {
	var rows []sqlcgen.DeleteCommentsByAuthorRow
	for id, c := range f.comments {
		byUser := a.UserID.Valid && c.UserID == a.UserID
		byActor := a.RemoteActorUrl != nil && c.RemoteActorUrl != nil && *c.RemoteActorUrl == *a.RemoteActorUrl
		if byUser || byActor {
			rows = append(rows, sqlcgen.DeleteCommentsByAuthorRow{ID: id, VideoID: c.VideoID, UserID: c.UserID})
		}
	}
	for _, r := range rows {
		delete(f.comments, r.ID)
	}
	return rows, nil
}

func TestCreateAndListByVideo(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	video, user := uuid.New(), uuid.New()
	repo.authors[user] = author{"ada", "Ada Makes"}

	if _, err := svc.Create(context.Background(), video, user, "first!", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	items, _, err := svc.ListByVideo(context.Background(), video, uuid.Nil, false, false, 20, 0)
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

	all, _, err := svc.ListForAdmin(ctx, "", 20, 0)
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
	filtered, _, _ := svc.ListForAdmin(ctx, "spam", 20, 0)
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
	if items, _, _ := svc.ListByVideo(context.Background(), c.VideoID, uuid.Nil, false, false, 20, 0); len(items) != 0 {
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

// TestPinSwapUnpinAndList covers the pin lifecycle: pinning replaces any prior
// pin, the list returns the pinned comment first, unpin clears it, and unpinning
// a comment that is NOT the current pin never clobbers a different pin (0099).
func TestPinSwapUnpinAndList(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	ctx := context.Background()
	video, owner := uuid.New(), uuid.New()
	repo.authors[owner] = author{"ada", "Ada"}
	a, _ := svc.Create(ctx, video, owner, "A", nil)
	b, _ := svc.Create(ctx, video, owner, "B", nil)

	if wa, err := svc.Pin(ctx, a.ID); err != nil || !wa.Pinned {
		t.Fatalf("pin A = %+v, err %v", wa, err)
	}
	// Pinning B replaces A atomically.
	if wa, err := svc.Pin(ctx, b.ID); err != nil || !wa.Pinned {
		t.Fatalf("pin B = %+v, err %v", wa, err)
	}
	if pinned, _ := svc.IsPinned(ctx, a.ID); pinned {
		t.Error("pinning B should have replaced A, but A is still pinned")
	}
	if pinned, _ := svc.IsPinned(ctx, b.ID); !pinned {
		t.Error("B should be the current pin")
	}

	// The list returns B first, flagged pinned.
	items, _, _ := svc.ListByVideo(ctx, video, uuid.Nil, false, false, 20, 0)
	if len(items) != 2 || items[0].Comment.ID != b.ID || !items[0].Pinned {
		t.Fatalf("list[0] = %+v, want B pinned-first", items[0])
	}

	// Unpin B.
	if wa, err := svc.Unpin(ctx, b.ID); err != nil || wa.Pinned {
		t.Fatalf("unpin B = %+v, err %v", wa, err)
	}
	if pinned, _ := svc.IsPinned(ctx, b.ID); pinned {
		t.Error("B should be unpinned")
	}

	// Re-pin A, then unpin B (which is not the current pin): A's pin survives.
	if _, err := svc.Pin(ctx, a.ID); err != nil {
		t.Fatalf("re-pin A: %v", err)
	}
	if _, err := svc.Unpin(ctx, b.ID); err != nil {
		t.Fatalf("unpin non-pinned B: %v", err)
	}
	if pinned, _ := svc.IsPinned(ctx, a.ID); !pinned {
		t.Error("unpinning a non-pinned comment must not clear A's pin")
	}
}

// TestPinRejectsReplyAndTombstone: only a top-level, non-tombstoned comment can
// be pinned; a tombstoned comment can't be hearted either (0099).
func TestPinRejectsReplyAndTombstone(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	ctx := context.Background()
	video, owner := uuid.New(), uuid.New()
	repo.authors[owner] = author{"ada", "Ada"}
	top, _ := svc.Create(ctx, video, owner, "top", nil)
	reply, _ := svc.Create(ctx, video, owner, "reply", &top.ID)

	if _, err := svc.Pin(ctx, reply.ID); !errors.Is(err, ErrNotTopLevel) {
		t.Errorf("pin reply = %v, want ErrNotTopLevel", err)
	}

	// Tombstone the top-level comment.
	tomb := repo.comments[top.ID]
	tomb.DeletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	repo.comments[top.ID] = tomb
	if _, err := svc.Pin(ctx, top.ID); !errors.Is(err, ErrTombstoned) {
		t.Errorf("pin tombstoned = %v, want ErrTombstoned", err)
	}
	if _, err := svc.Heart(ctx, top.ID); !errors.Is(err, ErrTombstoned) {
		t.Errorf("heart tombstoned = %v, want ErrTombstoned", err)
	}

	// Unknown comment.
	if _, err := svc.Pin(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("pin unknown = %v, want ErrNotFound", err)
	}
	if _, err := svc.Heart(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("heart unknown = %v, want ErrNotFound", err)
	}
}

// TestHeartRoundTripLocalAndRemote: the heart flag round-trips on both local and
// remote-authored comments (heart is local metadata) and shows in the list.
func TestHeartRoundTripLocalAndRemote(t *testing.T) {
	repo := newFakeRepo()
	repo.remoteRows = map[uuid.UUID]remoteRowMeta{}
	svc := NewService(repo)
	ctx := context.Background()
	video, owner := uuid.New(), uuid.New()
	repo.authors[owner] = author{"ada", "Ada"}

	local, _ := svc.Create(ctx, video, owner, "local", nil)
	if wa, err := svc.Heart(ctx, local.ID); err != nil || !wa.Comment.Hearted {
		t.Fatalf("heart local = %+v, err %v", wa, err)
	}
	if wa, err := svc.Unheart(ctx, local.ID); err != nil || wa.Comment.Hearted {
		t.Fatalf("unheart local = %+v, err %v", wa, err)
	}

	// A remote-authored comment can still be hearted (local metadata).
	actor := "https://remote.example/users/bob"
	remote := sqlcgen.Comment{
		ID: uuid.New(), VideoID: video, Body: "hi from afar",
		CreatedAt: time.Now(), UpdatedAt: time.Now(), RemoteActorUrl: &actor,
	}
	repo.comments[remote.ID] = remote
	repo.remoteRows[remote.ID] = remoteRowMeta{name: "bob@remote.example", domain: "remote.example"}
	wa, err := svc.Heart(ctx, remote.ID)
	if err != nil || !wa.Comment.Hearted || !wa.Remote || wa.AuthorDomain != "remote.example" {
		t.Fatalf("heart remote = %+v, err %v", wa, err)
	}

	items, _, _ := svc.ListByVideo(ctx, video, uuid.Nil, false, false, 20, 0)
	for _, it := range items {
		if it.Comment.ID == remote.ID && !it.Comment.Hearted {
			t.Error("remote heart not reflected in the list")
		}
	}
}

// TestPinHeartDoNotFederate: none of pin/unpin/heart/unheart fire the federation
// Create/Update/Delete{Note} hooks — they are local metadata (0099).
func TestPinHeartDoNotFederate(t *testing.T) {
	repo := newFakeRepo()
	var creates, updates, deletes int
	svc := NewService(repo,
		WithCreateHook(func(context.Context, uuid.UUID) { creates++ }),
		WithUpdateHook(func(context.Context, uuid.UUID) { updates++ }),
		WithDeleteHook(func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) { deletes++ }),
	)
	ctx := context.Background()
	video, owner := uuid.New(), uuid.New()
	repo.authors[owner] = author{"ada", "Ada"}
	c, err := svc.Create(ctx, video, owner, "top", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Create fired the create hook once; the metadata actions must fire nothing.
	if creates != 1 {
		t.Fatalf("create hook = %d, want 1 (from Create)", creates)
	}
	creates = 0

	if _, err := svc.Pin(ctx, c.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if _, err := svc.Heart(ctx, c.ID); err != nil {
		t.Fatalf("Heart: %v", err)
	}
	if _, err := svc.Unheart(ctx, c.ID); err != nil {
		t.Fatalf("Unheart: %v", err)
	}
	if _, err := svc.Unpin(ctx, c.ID); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if creates != 0 || updates != 0 || deletes != 0 {
		t.Errorf("pin/heart federated: create=%d update=%d delete=%d, want 0/0/0", creates, updates, deletes)
	}
}

func (f *fakeRepo) CountCommentsByVideo(ctx context.Context, a sqlcgen.CountCommentsByVideoParams) (int64, error) {
	rows, err := f.ListCommentsByVideo(ctx, sqlcgen.ListCommentsByVideoParams{
		VideoID: a.VideoID, ViewerID: a.ViewerID, ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountAdminComments(ctx context.Context, query *string) (int64, error) {
	rows, err := f.ListAdminComments(ctx, sqlcgen.ListAdminCommentsParams{Query: query, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func TestRemoveByAccount(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	type del struct{ cid, vid, aid uuid.UUID }
	var hooks []del
	svc := NewService(repo, WithDeleteHook(func(_ context.Context, c, v, a uuid.UUID) { hooks = append(hooks, del{c, v, a}) }))
	video, spammer, other := uuid.New(), uuid.New(), uuid.New()
	repo.authors[spammer] = author{"Spam-Bot", ""}
	repo.authors[other] = author{"ada", ""}
	for _, u := range []uuid.UUID{spammer, spammer, other} {
		if _, err := svc.Create(ctx, video, u, "x", nil); err != nil {
			t.Fatal(err)
		}
	}
	remote := seedRemoteComment(repo, video)

	// A local username is case-insensitive, covers all its comments, and fires
	// the federation hook once per LOCAL comment.
	n, id, err := svc.RemoveByAccount(ctx, "@spam-bot")
	if err != nil || n != 2 || id != spammer {
		t.Fatalf("RemoveByAccount = %d, %s, %v; want 2, %s, nil", n, id, err, spammer)
	}
	if len(hooks) != 2 || hooks[0].aid != spammer || hooks[0].vid != video {
		t.Errorf("hooks = %+v, want 2 for the spammer", hooks)
	}
	if len(repo.comments) != 2 {
		t.Errorf("remaining = %d, want 2 (ada's + the remote one)", len(repo.comments))
	}

	// A remote actor URL removes its federated comments locally, no hook.
	hooks = nil
	n, id, err = svc.RemoveByAccount(ctx, *remote.RemoteActorUrl)
	if err != nil || n != 1 || id != uuid.Nil || len(hooks) != 0 {
		t.Fatalf("remote = %d, %s, %v, hooks=%d; want 1, nil-uuid, nil, 0", n, id, err, len(hooks))
	}

	// Unknown local account is ErrNotFound; blank too.
	for _, acct := range []string{"nobody", "  "} {
		if _, _, err := svc.RemoveByAccount(ctx, acct); !errors.Is(err, ErrNotFound) {
			t.Errorf("RemoveByAccount(%q) err = %v, want ErrNotFound", acct, err)
		}
	}
}

// A client disconnect or proxy timeout cancels the request context after the
// DELETE has committed; the federation fan-out must still reach every comment
// or the Notes stay alive on remote instances.
func TestRemoveByAccountFansOutOnCancelledRequestContext(t *testing.T) {
	repo := newFakeRepo()
	var ctxErrs []error
	svc := NewService(repo, WithDeleteHook(func(ctx context.Context, _, _, _ uuid.UUID) {
		ctxErrs = append(ctxErrs, ctx.Err())
	}))
	video, spammer := uuid.New(), uuid.New()
	repo.authors[spammer] = author{"spam", ""}
	for i := 0; i < 3; i++ {
		if _, err := svc.Create(context.Background(), video, spammer, "x", nil); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, _, err := svc.RemoveByAccount(ctx, "spam")
	if err != nil || n != 3 {
		t.Fatalf("RemoveByAccount = %d, %v; want 3, nil", n, err)
	}
	if len(ctxErrs) != 3 {
		t.Fatalf("hooks ran %d times, want 3", len(ctxErrs))
	}
	for _, e := range ctxErrs {
		if e != nil {
			t.Errorf("hook ctx was cancelled (%v); fan-out must not inherit request cancellation", e)
		}
	}
}
