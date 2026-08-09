package notification

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory notification.Repository. prefs maps "userID\x00type"
// → enabled (absent = default enabled), mirroring the real table. prefsErr,
// when set, fails the pref lookups — for proving the fail-open behaviour.
type fakeRepo struct {
	notifs   []sqlcgen.Notification
	prefs    map[string]bool
	prefsErr error
	// fanOut records the videos handed to the new-video fan-out and what the
	// (SQL-side) statement reported back. The fan-out's real rules live in the
	// query, so they are proved against a live schema in the integration test —
	// here we only prove the service passes through faithfully.
	fanOut    []uuid.UUID
	fanOutN   int64
	fanOutErr error
	// reportFanOut records the reports handed to the new-report staff fan-out
	// (same pass-through contract as fanOut above; rules proved in
	// store.TestNewReportStaffFanOutOnRealPG).
	reportFanOut []uuid.UUID
}

func (f *fakeRepo) NotifyFollowersOfNewVideo(_ context.Context, videoID uuid.UUID) (int64, error) {
	f.fanOut = append(f.fanOut, videoID)
	if f.fanOutErr != nil {
		return 0, f.fanOutErr
	}
	return f.fanOutN, nil
}

func (f *fakeRepo) NotifyStaffOfNewReport(_ context.Context, reportID uuid.UUID) (int64, error) {
	f.reportFanOut = append(f.reportFanOut, reportID)
	if f.fanOutErr != nil {
		return 0, f.fanOutErr
	}
	return f.fanOutN, nil
}

func prefKey(userID uuid.UUID, typ string) string { return userID.String() + "\x00" + typ }

func (f *fakeRepo) ListNotificationPrefs(_ context.Context, userID uuid.UUID) ([]sqlcgen.ListNotificationPrefsRow, error) {
	if f.prefsErr != nil {
		return nil, f.prefsErr
	}
	var rows []sqlcgen.ListNotificationPrefsRow
	for _, typ := range KnownTypes() { // stable order, like ORDER BY type
		if enabled, ok := f.prefs[prefKey(userID, typ)]; ok {
			rows = append(rows, sqlcgen.ListNotificationPrefsRow{Type: typ, Enabled: enabled})
		}
	}
	return rows, nil
}

func (f *fakeRepo) UpsertNotificationPref(_ context.Context, a sqlcgen.UpsertNotificationPrefParams) error {
	if f.prefsErr != nil {
		return f.prefsErr
	}
	if f.prefs == nil {
		f.prefs = map[string]bool{}
	}
	f.prefs[prefKey(a.UserID, a.Type)] = a.Enabled
	return nil
}

func (f *fakeRepo) IsNotificationTypeEnabled(_ context.Context, a sqlcgen.IsNotificationTypeEnabledParams) (bool, error) {
	if f.prefsErr != nil {
		return false, f.prefsErr
	}
	if enabled, ok := f.prefs[prefKey(a.UserID, a.Type)]; ok {
		return enabled, nil
	}
	return true, nil
}

func (f *fakeRepo) CreateNotification(_ context.Context, a sqlcgen.CreateNotificationParams) (sqlcgen.Notification, error) {
	n := sqlcgen.Notification{
		ID: uuid.New(), UserID: a.UserID, Type: a.Type,
		ActorID: a.ActorID, ChannelID: a.ChannelID, VideoID: a.VideoID, CommentID: a.CommentID,
		ConversationID: a.ConversationID, ReportID: a.ReportID, CreatedAt: time.Now(),
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
			VideoID: n.VideoID, CommentID: n.CommentID, ConversationID: n.ConversationID,
			ReportID: n.ReportID, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt,
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

func TestNotifyMessage(t *testing.T) {
	svc := NewService(&fakeRepo{})
	ctx := context.Background()
	recipient, sender, conv := uuid.New(), uuid.New(), uuid.New()

	if err := svc.NotifyMessage(ctx, recipient, sender, conv); err != nil {
		t.Fatalf("NotifyMessage: %v", err)
	}
	// Messaging yourself never notifies.
	if err := svc.NotifyMessage(ctx, recipient, recipient, conv); err != nil {
		t.Fatalf("self NotifyMessage: %v", err)
	}

	items, err := svc.List(ctx, recipient, false, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("list len = %d, want 1 (self-notification skipped)", len(items))
	}
	if items[0].Type != TypeMessage || items[0].ConversationID != conv.String() {
		t.Errorf("item = {type:%s conversation:%s}, want {message %s}", items[0].Type, items[0].ConversationID, conv)
	}
}

func TestNotifyReportResolved(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	reporter, moderator, report := uuid.New(), uuid.New(), uuid.New()

	if err := svc.NotifyReportResolved(ctx, reporter, moderator, report); err != nil {
		t.Fatalf("NotifyReportResolved: %v", err)
	}
	// Resolving your own report never notifies.
	if err := svc.NotifyReportResolved(ctx, reporter, reporter, report); err != nil {
		t.Fatalf("self NotifyReportResolved: %v", err)
	}

	items, err := svc.List(ctx, reporter, false, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("list len = %d, want 1 (self-notification skipped)", len(items))
	}
	if items[0].Type != TypeReportResolved || items[0].ReportID != report.String() {
		t.Errorf("item = {type:%s report:%s}, want {report_resolved %s}", items[0].Type, items[0].ReportID, report)
	}
	// The moderator's identity must never be recorded on the notification row.
	if repo.notifs[0].ActorID.Valid {
		t.Errorf("actor_id stored on report_resolved notification = %v, want null (moderator identity must not leak)", repo.notifs[0].ActorID)
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

func TestPrefsDefaultAllEnabled(t *testing.T) {
	svc := NewService(&fakeRepo{})
	prefs, err := svc.Prefs(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Prefs: %v", err)
	}
	if len(prefs) != len(KnownTypes()) {
		t.Fatalf("prefs len = %d, want %d (every known type)", len(prefs), len(KnownTypes()))
	}
	for _, typ := range KnownTypes() {
		if enabled, ok := prefs[typ]; !ok || !enabled {
			t.Errorf("prefs[%s] = %v/%v, want true (default enabled)", typ, enabled, ok)
		}
	}
}

func TestSetPrefsPartialUpdateAndUnknownType(t *testing.T) {
	svc := NewService(&fakeRepo{})
	ctx := context.Background()
	user := uuid.New()

	// Disable one type; the others stay at the default.
	if err := svc.SetPrefs(ctx, user, map[string]bool{TypeComment: false}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	prefs, _ := svc.Prefs(ctx, user)
	if prefs[TypeComment] {
		t.Error("comment still enabled after disabling it")
	}
	for _, typ := range []string{TypeFollow, TypeMessage, TypeReportResolved} {
		if !prefs[typ] {
			t.Errorf("prefs[%s] flipped by an unrelated update", typ)
		}
	}

	// Re-enable → back to true.
	if err := svc.SetPrefs(ctx, user, map[string]bool{TypeComment: true}); err != nil {
		t.Fatalf("SetPrefs re-enable: %v", err)
	}
	if prefs, _ := svc.Prefs(ctx, user); !prefs[TypeComment] {
		t.Error("comment not re-enabled")
	}

	// Unknown type rejects the whole update; nothing written.
	err := svc.SetPrefs(ctx, user, map[string]bool{TypeFollow: false, "premium_offers": false})
	if err != ErrUnknownType {
		t.Fatalf("SetPrefs unknown type = %v, want ErrUnknownType", err)
	}
	if prefs, _ := svc.Prefs(ctx, user); !prefs[TypeFollow] {
		t.Error("follow was written although the update contained an unknown type")
	}
}

func TestDisabledTypeSuppressesCreation(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	owner, fan, conv, report := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	if err := svc.SetPrefs(ctx, owner, map[string]bool{
		TypeFollow: false, TypeMessage: false, TypeReportResolved: false,
	}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}

	// Disabled types create nothing (and return nil — a suppressed notification
	// is not an error).
	if err := svc.NotifyFollow(ctx, owner, fan, uuid.New()); err != nil {
		t.Fatalf("NotifyFollow (disabled): %v", err)
	}
	if err := svc.NotifyMessage(ctx, owner, fan, conv); err != nil {
		t.Fatalf("NotifyMessage (disabled): %v", err)
	}
	if err := svc.NotifyReportResolved(ctx, owner, fan, report); err != nil {
		t.Fatalf("NotifyReportResolved (disabled): %v", err)
	}
	if len(repo.notifs) != 0 {
		t.Fatalf("notifications created = %d, want 0 (all disabled)", len(repo.notifs))
	}

	// A type left enabled still notifies.
	if err := svc.NotifyComment(ctx, owner, fan, uuid.New(), uuid.New()); err != nil {
		t.Fatalf("NotifyComment (enabled): %v", err)
	}
	if len(repo.notifs) != 1 || repo.notifs[0].Type != TypeComment {
		t.Fatalf("notifs = %+v, want exactly one comment notification", repo.notifs)
	}

	// Re-enabling restores delivery.
	if err := svc.SetPrefs(ctx, owner, map[string]bool{TypeFollow: true}); err != nil {
		t.Fatalf("SetPrefs re-enable: %v", err)
	}
	if err := svc.NotifyFollow(ctx, owner, fan, uuid.New()); err != nil {
		t.Fatalf("NotifyFollow (re-enabled): %v", err)
	}
	if len(repo.notifs) != 2 {
		t.Fatalf("notifs after re-enable = %d, want 2", len(repo.notifs))
	}
}

func TestPrefLookupFailureFailsOpen(t *testing.T) {
	repo := &fakeRepo{prefsErr: context.DeadlineExceeded}
	svc := NewService(repo)
	ctx := context.Background()
	owner, fan := uuid.New(), uuid.New()

	// The pref filter is unreadable → the notification is still created.
	if err := svc.NotifyFollow(ctx, owner, fan, uuid.New()); err != nil {
		t.Fatalf("NotifyFollow with failing pref lookup: %v", err)
	}
	if len(repo.notifs) != 1 {
		t.Fatalf("notifs = %d, want 1 (fail-open)", len(repo.notifs))
	}
}

// TestNotifyNewVideoPassesThrough proves the service-side contract of the
// new-video fan-out: the video id reaches the statement unchanged, the number of
// notified followers is returned to the caller (cmd/api logs it), and a failure
// surfaces as an error rather than being swallowed — the publish hook is what
// decides to treat it as best-effort. The fan-out's actual selection rules are
// SQL and are proved against a real database in TestNewVideoFanOutOnRealPG.
func TestNotifyNewVideoPassesThrough(t *testing.T) {
	repo := &fakeRepo{fanOutN: 3}
	svc := NewService(repo)
	ctx := context.Background()
	videoID := uuid.New()

	notified, err := svc.NotifyNewVideo(ctx, videoID)
	if err != nil {
		t.Fatalf("NotifyNewVideo: %v", err)
	}
	if notified != 3 {
		t.Fatalf("notified = %d, want 3", notified)
	}
	if len(repo.fanOut) != 1 || repo.fanOut[0] != videoID {
		t.Fatalf("fan-out calls = %v, want [%s]", repo.fanOut, videoID)
	}

	repo.fanOutErr = context.DeadlineExceeded
	if _, err := svc.NotifyNewVideo(ctx, videoID); err == nil {
		t.Fatal("NotifyNewVideo with a failing statement returned nil error")
	}
}

// TestNewVideoIsAKnownPreferenceType keeps the notification type registered with
// the preference model: a type missing from KnownTypes silently cannot be turned
// off by a user, which is exactly the failure mode the prefs surface exists to
// prevent.
func TestNewVideoIsAKnownPreferenceType(t *testing.T) {
	if !knownType(TypeNewVideo) {
		t.Fatal("new_video is not a known notification type")
	}
	found := false
	for _, typ := range KnownTypes() {
		if typ == TypeNewVideo {
			found = true
		}
	}
	if !found {
		t.Fatalf("KnownTypes() = %v, missing %s", KnownTypes(), TypeNewVideo)
	}
	prefs, err := NewService(&fakeRepo{}).Prefs(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Prefs: %v", err)
	}
	if enabled, ok := prefs[TypeNewVideo]; !ok || !enabled {
		t.Fatalf("prefs[%s] = (%v, %v), want (true, true) by default", TypeNewVideo, enabled, ok)
	}
}
