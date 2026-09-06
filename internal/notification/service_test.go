package notification

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	// replyRecipients answers CommentReplyRecipient: comment id -> the row the
	// real query would return. An absent key is pgx.ErrNoRows, exactly like the
	// :one query's "nobody to notify" answer. replyErr, when set, fails the
	// lookup — for proving a resolution failure is surfaced, not swallowed.
	replyRecipients map[uuid.UUID]sqlcgen.CommentReplyRecipientRow
	replyLookups    []uuid.UUID
	replyErr        error
	// ownerRecipients answers CommentVideoOwnerRecipient on exactly the same
	// contract as replyRecipients above: comment id -> the row the real query
	// would return, an absent key being its "nobody to notify" answer.
	ownerRecipients map[uuid.UUID]sqlcgen.CommentVideoOwnerRecipientRow
	ownerLookups    []uuid.UUID
	ownerErr        error
}

// CommentVideoOwnerRecipient mirrors the :one contract of the real query. Its
// selection rules (mute, block either way, tombstone, federated author,
// self-comment, inactive owner) live in SQL and are proved against a live
// database in store.TestCommentVideoOwnerRecipientOnRealPG — here we only prove
// the service reacts to each answer faithfully.
func (f *fakeRepo) CommentVideoOwnerRecipient(_ context.Context, commentID uuid.UUID) (sqlcgen.CommentVideoOwnerRecipientRow, error) {
	f.ownerLookups = append(f.ownerLookups, commentID)
	if f.ownerErr != nil {
		return sqlcgen.CommentVideoOwnerRecipientRow{}, f.ownerErr
	}
	row, ok := f.ownerRecipients[commentID]
	if !ok {
		return sqlcgen.CommentVideoOwnerRecipientRow{}, pgx.ErrNoRows
	}
	return row, nil
}

// CommentReplyRecipient mirrors the :one contract of the real query: a resolved
// row, or pgx.ErrNoRows when the statement's exclusions leave nobody. Its actual
// selection rules live in SQL and are proved against a live database in
// store.TestCommentReplyRecipientOnRealPG — here we only prove the service reacts
// to each answer faithfully.
func (f *fakeRepo) CommentReplyRecipient(_ context.Context, commentID uuid.UUID) (sqlcgen.CommentReplyRecipientRow, error) {
	f.replyLookups = append(f.replyLookups, commentID)
	if f.replyErr != nil {
		return sqlcgen.CommentReplyRecipientRow{}, f.replyErr
	}
	row, ok := f.replyRecipients[commentID]
	if !ok {
		return sqlcgen.CommentReplyRecipientRow{}, pgx.ErrNoRows
	}
	return row, nil
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
	ctx := context.Background()
	owner, fan := uuid.New(), uuid.New()
	ch, video, comment := uuid.New(), uuid.New(), uuid.New()
	// selfComment is the comment the OWNER wrote on their own video: the real
	// query excludes it (owner IS NOT DISTINCT FROM commenter), so the fake maps
	// only the fan's comment to a recipient.
	selfComment := uuid.New()
	svc := NewService(&fakeRepo{ownerRecipients: map[uuid.UUID]sqlcgen.CommentVideoOwnerRecipientRow{
		comment: {RecipientID: owner, VideoID: video},
	}})

	// A follow and a comment from someone else both notify the owner.
	if err := svc.NotifyFollow(ctx, owner, fan, ch); err != nil {
		t.Fatalf("NotifyFollow: %v", err)
	}
	if got, err := svc.NotifyComment(ctx, fan, comment); err != nil || got != owner {
		t.Fatalf("NotifyComment = (%s, %v), want (%s, nil)", got, err, owner)
	}
	// Self-actions never notify.
	if err := svc.NotifyFollow(ctx, owner, owner, ch); err != nil {
		t.Fatalf("self NotifyFollow: %v", err)
	}
	if got, err := svc.NotifyComment(ctx, owner, selfComment); err != nil || got != uuid.Nil {
		t.Fatalf("self NotifyComment = (%s, %v), want (nil uuid, nil)", got, err)
	}

	if n, _ := svc.UnreadCount(ctx, owner); n != 2 {
		t.Fatalf("unread = %d, want 2", n)
	}
	items, _, err := svc.List(ctx, owner, false, 20, 0)
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

	items, _, err := svc.List(ctx, recipient, false, 20, 0)
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

	items, _, err := svc.List(ctx, reporter, false, 20, 0)
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

	items, _, _ := svc.List(ctx, owner, false, 20, 0)
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
	if unread, _, _ := svc.List(ctx, owner, true, 20, 0); len(unread) != 0 {
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
	enabledComment := uuid.New()
	repo.ownerRecipients = map[uuid.UUID]sqlcgen.CommentVideoOwnerRecipientRow{
		enabledComment: {RecipientID: owner, VideoID: uuid.New()},
	}
	if got, err := svc.NotifyComment(ctx, fan, enabledComment); err != nil || got != owner {
		t.Fatalf("NotifyComment (enabled) = (%s, %v), want (%s, nil)", got, err, owner)
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

// TestVideoBlockedIsAKnownPreferenceType keeps the new moderation notification
// registered with the preference model. A type missing from KnownTypes cannot be
// turned off by the user it is delivered to — the exact failure the prefs
// surface exists to prevent — and this one is delivered on an unhappy event, so
// a creator who does not want it must be able to say so.
func TestVideoBlockedIsAKnownPreferenceType(t *testing.T) {
	if !knownType(TypeVideoBlocked) {
		t.Fatal("video_blocked is not a known notification type")
	}
	found := false
	for _, typ := range KnownTypes() {
		if typ == TypeVideoBlocked {
			found = true
		}
	}
	if !found {
		t.Fatalf("KnownTypes() = %v, missing %s", KnownTypes(), TypeVideoBlocked)
	}
	prefs, err := NewService(&fakeRepo{}).Prefs(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Prefs: %v", err)
	}
	if enabled, ok := prefs[TypeVideoBlocked]; !ok || !enabled {
		t.Fatalf("prefs[%s] = (%v, %v), want (true, true) by default", TypeVideoBlocked, enabled, ok)
	}
}

// TestNotifyVideoBlockedSkipsSelfAndDisabled proves the two suppressions every
// Notify* method shares, on the one type where getting them wrong is loudest: a
// moderator blocking their OWN video must not notify themselves, and a creator
// who turned video_blocked off must not be told.
func TestNotifyVideoBlockedSkipsSelfAndDisabled(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepo{}
	svc := NewService(repo)
	self, videoID := uuid.New(), uuid.New()
	if err := svc.NotifyVideoBlocked(ctx, self, self, videoID); err != nil {
		t.Fatalf("self-block notify: %v", err)
	}
	if len(repo.notifs) != 0 {
		t.Fatalf("a moderator blocking their own video notified themselves: %+v", repo.notifs)
	}

	owner := uuid.New()
	if err := svc.SetPrefs(ctx, owner, map[string]bool{TypeVideoBlocked: false}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	if err := svc.NotifyVideoBlocked(ctx, owner, uuid.New(), videoID); err != nil {
		t.Fatalf("notify with the type disabled: %v", err)
	}
	if len(repo.notifs) != 0 {
		t.Fatalf("video_blocked delivered to a recipient who turned it off: %+v", repo.notifs)
	}

	other := uuid.New()
	if err := svc.NotifyVideoBlocked(ctx, other, uuid.New(), videoID); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(repo.notifs) != 1 || repo.notifs[0].Type != TypeVideoBlocked {
		t.Fatalf("notifications = %+v, want one video_blocked", repo.notifs)
	}
	if repo.notifs[0].ActorID.Valid {
		t.Error("video_blocked stored the moderator as its actor")
	}
}

func (f *fakeRepo) CountNotifications(ctx context.Context, a sqlcgen.CountNotificationsParams) (int64, error) {
	rows, err := f.ListNotifications(ctx, sqlcgen.ListNotificationsParams{
		UserID: a.UserID, UnreadOnly: a.UnreadOnly, ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

// TestNotifyCommentReply is the reply notification's own contract: the person
// being ANSWERED hears about it. Before this existed, replying to someone's
// comment notified only the video's owner, so the author of the parent comment
// — the one actually addressed — got nothing at all.
func TestNotifyCommentReply(t *testing.T) {
	ctx := context.Background()
	parentAuthor, replier := uuid.New(), uuid.New()
	videoID, replyID := uuid.New(), uuid.New()
	repo := &fakeRepo{replyRecipients: map[uuid.UUID]sqlcgen.CommentReplyRecipientRow{
		replyID: {RecipientID: pgtype.UUID{Bytes: parentAuthor, Valid: true}, VideoID: videoID},
	}}
	svc := NewService(repo)

	got, err := svc.NotifyCommentReply(ctx, replier, replyID)
	if err != nil {
		t.Fatalf("NotifyCommentReply: %v", err)
	}
	if got != parentAuthor {
		t.Fatalf("recipient = %s, want the parent author %s", got, parentAuthor)
	}
	items, _, err := svc.List(ctx, parentAuthor, false, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("parent author has %d notifications, want 1", len(items))
	}
	it := items[0]
	if it.Type != TypeCommentReply {
		t.Errorf("type = %q, want %q — a reply must be distinguishable from a comment on your video", it.Type, TypeCommentReply)
	}
	if it.VideoID != videoID.String() {
		t.Errorf("video_id = %q, want %q (the watch page the reply lives on)", it.VideoID, videoID)
	}
	if it.CommentID != replyID.String() {
		t.Errorf("comment_id = %q, want the reply %q", it.CommentID, replyID)
	}
	// The actor is the replier: the copy has to be able to name who answered.
	if len(repo.notifs) != 1 || !repo.notifs[0].ActorID.Valid || uuid.UUID(repo.notifs[0].ActorID.Bytes) != replier {
		t.Fatalf("actor = %v, want the replier %s", repo.notifs[0].ActorID, replier)
	}
	// Unread and read-marking are the shared mechanics, not a per-type
	// reimplementation: the new row counts toward the badge and clears with it.
	if n, _ := svc.UnreadCount(ctx, parentAuthor); n != 1 {
		t.Fatalf("unread = %d, want 1", n)
	}
	if err := svc.MarkRead(ctx, parentAuthor, it.ID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if n, _ := svc.UnreadCount(ctx, parentAuthor); n != 0 {
		t.Fatalf("unread after MarkRead = %d, want 0", n)
	}
}

// TestNotifyCommentReplySkips covers every answer that must produce NO reply
// notification. The "no recipient" cases are the query's business (proved on
// real PG); what this pins down is that the service treats each as a silent
// no-op rather than an error or a stray row.
func TestNotifyCommentReplySkips(t *testing.T) {
	ctx := context.Background()
	replier := uuid.New()
	topLevel := uuid.New()

	t.Run("no resolved recipient is not an error", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService(repo)
		got, err := svc.NotifyCommentReply(ctx, replier, topLevel)
		if err != nil {
			t.Fatalf("NotifyCommentReply on a top-level comment: %v", err)
		}
		if got != uuid.Nil {
			t.Fatalf("recipient = %s, want uuid.Nil", got)
		}
		if len(repo.notifs) != 0 {
			t.Fatalf("wrote %d notifications, want 0", len(repo.notifs))
		}
	})

	t.Run("replying to yourself notifies nobody", func(t *testing.T) {
		replyID := uuid.New()
		repo := &fakeRepo{replyRecipients: map[uuid.UUID]sqlcgen.CommentReplyRecipientRow{
			replyID: {RecipientID: pgtype.UUID{Bytes: replier, Valid: true}, VideoID: uuid.New()},
		}}
		svc := NewService(repo)
		got, err := svc.NotifyCommentReply(ctx, replier, replyID)
		if err != nil {
			t.Fatalf("self reply: %v", err)
		}
		if got != uuid.Nil || len(repo.notifs) != 0 {
			t.Fatalf("self reply notified %s / wrote %d rows, want none", got, len(repo.notifs))
		}
	})

	t.Run("a disabled preference suppresses the reply notification", func(t *testing.T) {
		parentAuthor := uuid.New()
		replyID := uuid.New()
		repo := &fakeRepo{
			replyRecipients: map[uuid.UUID]sqlcgen.CommentReplyRecipientRow{
				replyID: {RecipientID: pgtype.UUID{Bytes: parentAuthor, Valid: true}, VideoID: uuid.New()},
			},
			prefs: map[string]bool{prefKey(parentAuthor, TypeCommentReply): false},
		}
		svc := NewService(repo)
		got, err := svc.NotifyCommentReply(ctx, replier, replyID)
		if err != nil {
			t.Fatalf("opted-out reply: %v", err)
		}
		if got != uuid.Nil || len(repo.notifs) != 0 {
			t.Fatalf("opted-out recipient got %s / %d rows, want none", got, len(repo.notifs))
		}
	})

	t.Run("a failed resolution is surfaced, not swallowed", func(t *testing.T) {
		repo := &fakeRepo{replyErr: context.DeadlineExceeded}
		svc := NewService(repo)
		if _, err := svc.NotifyCommentReply(ctx, replier, uuid.New()); err == nil {
			t.Fatal("NotifyCommentReply with a failing lookup returned nil error")
		}
	})
}

// TestCommentReplyIsAKnownPreferenceType keeps the new type registered with the
// preference model — a type missing from KnownTypes silently cannot be turned
// off, the exact failure the prefs surface exists to prevent.
func TestCommentReplyIsAKnownPreferenceType(t *testing.T) {
	if !knownType(TypeCommentReply) {
		t.Fatal("comment_reply is not a known notification type")
	}
	found := false
	for _, typ := range KnownTypes() {
		if typ == TypeCommentReply {
			found = true
		}
	}
	if !found {
		t.Fatalf("KnownTypes() = %v, missing %s", KnownTypes(), TypeCommentReply)
	}
	prefs, err := NewService(&fakeRepo{}).Prefs(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Prefs: %v", err)
	}
	if enabled, ok := prefs[TypeCommentReply]; !ok || !enabled {
		t.Fatalf("prefs[%s] = (%v, %v), want (true, true) by default", TypeCommentReply, enabled, ok)
	}
}

// TestNotifyCommentSkips covers every answer that must produce NO video-owner
// notification. The "no recipient" cases are the query's business (proved on
// real PG in store.TestCommentVideoOwnerRecipientOnRealPG); what this pins down
// is that the service treats each as a silent no-op rather than an error or a
// stray row — and, for the failing lookup, that it does NOT fall back to
// notifying anyone. A resolution failure that quietly notified the owner would
// restore exactly the leak this change closes.
func TestNotifyCommentSkips(t *testing.T) {
	ctx := context.Background()
	commenter := uuid.New()

	t.Run("no resolved recipient is not an error", func(t *testing.T) {
		repo := &fakeRepo{}
		svc := NewService(repo)
		got, err := svc.NotifyComment(ctx, commenter, uuid.New())
		if err != nil {
			t.Fatalf("NotifyComment with no recipient: %v", err)
		}
		if got != uuid.Nil || len(repo.notifs) != 0 {
			t.Fatalf("notified %s / wrote %d rows, want none", got, len(repo.notifs))
		}
	})

	t.Run("commenting on your own video notifies nobody", func(t *testing.T) {
		commentID := uuid.New()
		repo := &fakeRepo{ownerRecipients: map[uuid.UUID]sqlcgen.CommentVideoOwnerRecipientRow{
			commentID: {RecipientID: commenter, VideoID: uuid.New()},
		}}
		svc := NewService(repo)
		got, err := svc.NotifyComment(ctx, commenter, commentID)
		if err != nil {
			t.Fatalf("self comment: %v", err)
		}
		if got != uuid.Nil || len(repo.notifs) != 0 {
			t.Fatalf("self comment notified %s / wrote %d rows, want none", got, len(repo.notifs))
		}
	})

	t.Run("a disabled preference suppresses the owner notification", func(t *testing.T) {
		owner, commentID := uuid.New(), uuid.New()
		repo := &fakeRepo{
			ownerRecipients: map[uuid.UUID]sqlcgen.CommentVideoOwnerRecipientRow{
				commentID: {RecipientID: owner, VideoID: uuid.New()},
			},
			prefs: map[string]bool{prefKey(owner, TypeComment): false},
		}
		svc := NewService(repo)
		got, err := svc.NotifyComment(ctx, commenter, commentID)
		if err != nil {
			t.Fatalf("opted-out owner: %v", err)
		}
		if got != uuid.Nil || len(repo.notifs) != 0 {
			t.Fatalf("opted-out owner got %s / %d rows, want none", got, len(repo.notifs))
		}
	})

	t.Run("a failed resolution is surfaced, never notified past", func(t *testing.T) {
		repo := &fakeRepo{ownerErr: context.DeadlineExceeded}
		svc := NewService(repo)
		got, err := svc.NotifyComment(ctx, commenter, uuid.New())
		if err == nil {
			t.Fatal("NotifyComment with a failing lookup returned nil error")
		}
		if got != uuid.Nil || len(repo.notifs) != 0 {
			t.Fatalf("a failed lookup notified %s / wrote %d rows, want none", got, len(repo.notifs))
		}
	})
}
