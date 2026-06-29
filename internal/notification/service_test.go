package notification

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory notification.Repository.
type fakeRepo struct {
	notifs []sqlcgen.Notification
}

func (f *fakeRepo) CreateNotification(_ context.Context, a sqlcgen.CreateNotificationParams) (sqlcgen.Notification, error) {
	n := sqlcgen.Notification{
		ID: uuid.New(), UserID: a.UserID, Type: a.Type,
		ActorID: a.ActorID, ChannelID: a.ChannelID, VideoID: a.VideoID, CommentID: a.CommentID,
		CreatedAt: time.Now(),
	}
	f.notifs = append(f.notifs, n)
	return n, nil
}

func (f *fakeRepo) ListNotifications(_ context.Context, a sqlcgen.ListNotificationsParams) ([]sqlcgen.ListNotificationsRow, error) {
	var rows []sqlcgen.ListNotificationsRow
	for i := len(f.notifs) - 1; i >= 0; i-- { // newest first
		n := f.notifs[i]
		if n.UserID != a.UserID {
			continue
		}
		if a.UnreadOnly && n.ReadAt.Valid {
			continue
		}
		rows = append(rows, sqlcgen.ListNotificationsRow{
			ID: n.ID, Type: n.Type, ActorID: n.ActorID, ChannelID: n.ChannelID,
			VideoID: n.VideoID, CommentID: n.CommentID, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt,
		})
	}
	return rows, nil
}

func (f *fakeRepo) CountUnreadNotifications(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for _, x := range f.notifs {
		if x.UserID == userID && !x.ReadAt.Valid {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) MarkNotificationRead(_ context.Context, a sqlcgen.MarkNotificationReadParams) (int64, error) {
	for i := range f.notifs {
		if f.notifs[i].ID == a.ID && f.notifs[i].UserID == a.UserID {
			f.notifs[i].ReadAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeRepo) MarkAllNotificationsRead(_ context.Context, userID uuid.UUID) error {
	for i := range f.notifs {
		if f.notifs[i].UserID == userID {
			f.notifs[i].ReadAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func TestNotifyAndList(t *testing.T) {
	svc := NewService(&fakeRepo{})
	ctx := context.Background()
	owner, fan := uuid.New(), uuid.New()
	ch, video, comment := uuid.New(), uuid.New(), uuid.New()

	// A follow and a comment from someone else both notify the owner.
	if err := svc.NotifyFollow(ctx, owner, fan, ch); err != nil {
		t.Fatalf("NotifyFollow: %v", err)
	}
	if err := svc.NotifyComment(ctx, owner, fan, video, comment); err != nil {
		t.Fatalf("NotifyComment: %v", err)
	}
	// Self-actions never notify.
	if err := svc.NotifyFollow(ctx, owner, owner, ch); err != nil {
		t.Fatalf("self NotifyFollow: %v", err)
	}
	if err := svc.NotifyComment(ctx, owner, owner, video, comment); err != nil {
		t.Fatalf("self NotifyComment: %v", err)
	}

	if n, _ := svc.UnreadCount(ctx, owner); n != 2 {
		t.Fatalf("unread = %d, want 2", n)
	}
	items, err := svc.List(ctx, owner, false, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("list len = %d, want 2 (self-notifications skipped)", len(items))
	}
	// Newest first: the comment was created after the follow.
	if items[0].Type != TypeComment || items[1].Type != TypeFollow {
		t.Errorf("order = [%s, %s], want [comment, follow]", items[0].Type, items[1].Type)
	}
}

func TestMarkReadAndAll(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	owner, fan := uuid.New(), uuid.New()
	_ = svc.NotifyFollow(ctx, owner, fan, uuid.New())
	_ = svc.NotifyFollow(ctx, owner, fan, uuid.New())

	items, _ := svc.List(ctx, owner, false, 20, 0)
	first := items[0].ID

	// Mark one read → unread drops to 1; marking again is idempotent.
	if err := svc.MarkRead(ctx, owner, first); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if err := svc.MarkRead(ctx, owner, first); err != nil {
		t.Fatalf("MarkRead idempotent: %v", err)
	}
	if n, _ := svc.UnreadCount(ctx, owner); n != 1 {
		t.Fatalf("unread after mark-one = %d, want 1", n)
	}

	// Unknown id (or another user's) → ErrNotFound.
	if err := svc.MarkRead(ctx, owner, uuid.New()); err != ErrNotFound {
		t.Errorf("mark unknown = %v, want ErrNotFound", err)
	}
	if err := svc.MarkRead(ctx, uuid.New(), first); err != ErrNotFound {
		t.Errorf("mark another user's = %v, want ErrNotFound", err)
	}

	// Mark all read → unread is 0; unread-only list is empty.
	if err := svc.MarkAllRead(ctx, owner); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if n, _ := svc.UnreadCount(ctx, owner); n != 0 {
		t.Fatalf("unread after mark-all = %d, want 0", n)
	}
	if unread, _ := svc.List(ctx, owner, true, 20, 0); len(unread) != 0 {
		t.Errorf("unread-only list = %d, want 0", len(unread))
	}
}
