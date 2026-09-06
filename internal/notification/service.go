// Package notification implements user notifications for vidra-core: a recipient
// is told when an actor does something relevant to them (follows their channel,
// comments on their video). It is HTTP-agnostic and testable without a server.
// Notification creation is a best-effort side effect of the follow/comment flows
// — a failure to record a notification must never fail the underlying action.
package notification

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Notification type discriminators.
const (
	TypeFollow  = "follow"
	TypeComment = "comment"
	// TypeCommentReply tells the author of a comment that someone replied to
	// it. It is deliberately distinct from TypeComment: TypeComment addresses
	// the VIDEO OWNER ("someone commented on your video") and says nothing to
	// the person actually being answered, who may not own the video at all.
	TypeCommentReply   = "comment_reply"
	TypeMessage        = "message"
	TypeReportResolved = "report_resolved"
	TypeVideoRejected  = "video_rejected"
	// TypeVideoBlocked tells a creator that a moderator blocked one of their
	// PUBLISHED videos. It is deliberately distinct from video_rejected, which
	// is the quarantine outcome for an upload that never published: a block
	// takes down live content, changes neither state nor privacy, and is
	// reversible. Before it existed a block was invisible to its owner — the
	// video 404'd for them too while their own dashboard still read "published".
	TypeVideoBlocked = "video_blocked"
	TypeCaptionReady = "caption_ready"
	// TypeNewVideo tells a follower that a channel they follow published a new
	// public video. Unlike every other type it is created by a set-based
	// fan-out (NotifyNewVideo), not one row at a time.
	TypeNewVideo = "new_video"
	// TypeNewReport tells an admin/moderator that a user filed an abuse report
	// — the moderation queue's push signal. Like new_video it is created by a
	// set-based fan-out (NotifyNewReport); only staff ever receive it.
	TypeNewReport = "new_report"
)

// KnownTypes lists every notification type, in stable order. Preferences may
// target exactly these; every type defaults to enabled.
func KnownTypes() []string {
	return []string{TypeCaptionReady, TypeComment, TypeCommentReply, TypeFollow, TypeMessage, TypeNewReport, TypeNewVideo, TypeReportResolved, TypeVideoBlocked, TypeVideoRejected}
}

// knownType reports whether t is a recognised notification type.
func knownType(t string) bool {
	switch t {
	case TypeFollow, TypeComment, TypeCommentReply, TypeMessage, TypeReportResolved, TypeVideoRejected, TypeVideoBlocked, TypeCaptionReady, TypeNewVideo, TypeNewReport:
		return true
	}
	return false
}

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no notification matches the lookup for this user.
	ErrNotFound = errors.New("notification: not found")
	// ErrUnknownType means a preference targets a type that does not exist.
	ErrUnknownType = errors.New("notification: unknown notification type")
)

// Repository is the data access the notification service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateNotification(ctx context.Context, arg sqlcgen.CreateNotificationParams) (sqlcgen.Notification, error)
	ListNotifications(ctx context.Context, arg sqlcgen.ListNotificationsParams) ([]sqlcgen.ListNotificationsRow, error)
	CountNotifications(ctx context.Context, arg sqlcgen.CountNotificationsParams) (int64, error)
	CountUnreadNotifications(ctx context.Context, userID uuid.UUID) (int64, error)
	MarkNotificationRead(ctx context.Context, arg sqlcgen.MarkNotificationReadParams) (int64, error)
	MarkAllNotificationsRead(ctx context.Context, userID uuid.UUID) error

	ListNotificationPrefs(ctx context.Context, userID uuid.UUID) ([]sqlcgen.ListNotificationPrefsRow, error)
	UpsertNotificationPref(ctx context.Context, arg sqlcgen.UpsertNotificationPrefParams) error
	IsNotificationTypeEnabled(ctx context.Context, arg sqlcgen.IsNotificationTypeEnabledParams) (bool, error)
	NotifyFollowersOfNewVideo(ctx context.Context, videoID uuid.UUID) (int64, error)
	NotifyStaffOfNewReport(ctx context.Context, reportID uuid.UUID) (int64, error)
	CommentReplyRecipient(ctx context.Context, commentID uuid.UUID) (sqlcgen.CommentReplyRecipientRow, error)
	CommentVideoOwnerRecipient(ctx context.Context, commentID uuid.UUID) (sqlcgen.CommentVideoOwnerRecipientRow, error)
	FollowNotificationRecipient(ctx context.Context, arg sqlcgen.FollowNotificationRecipientParams) (uuid.UUID, error)
}

// Service holds the notification application logic.
type Service struct {
	repo Repository
}

// NewService builds the notification service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Item is a notification with the actor's identity and context resolved for
// display. Empty strings mean the field is not applicable to this type.
type Item struct {
	ID                 uuid.UUID
	Type               string
	Read               bool
	CreatedAt          time.Time
	ActorUsername      string
	ActorDisplayName   string
	ChannelHandle      string
	ChannelDisplayName string
	VideoID            string
	VideoTitle         string
	CommentID          string
	ConversationID     string
	ReportID           string
	ReportStatus       string
	ReportTargetType   string
	// ModerationNote is the moderator's rejection note (migration 0130),
	// carried ONLY on video_rejected. The reject route has always collected it;
	// until 0130 nothing stored it, so the creator was told their upload was
	// refused and never why. It is empty when the moderator supplied no note.
	// A BLOCK's reason is deliberately not here — that prose is staff-only.
	ModerationNote string
}

// typeEnabled reports whether the recipient receives notifications of this
// type (no stored preference = enabled). Fail-open: preferences are a filter,
// and an unreadable filter must not silently disable notifications — if the
// lookup errors, the create proceeds (and surfaces any real storage failure).
func (s *Service) typeEnabled(ctx context.Context, recipientID uuid.UUID, typ string) bool {
	on, err := s.repo.IsNotificationTypeEnabled(ctx, sqlcgen.IsNotificationTypeEnabledParams{
		UserID: recipientID,
		Type:   typ,
	})
	if err != nil {
		return true
	}
	return on
}

// NotifyFollow records that actorID followed a channel, telling that channel's
// OWNER.
//
// Like NotifyComment and NotifyCommentReply, every selection rule lives in the
// SQL (see FollowNotificationRecipient): the channel must exist, the owner is
// never told about their own follow, the owner must be an active, non-deleted
// account, and a muted or blocked follower never reaches the owner's
// notification surface. That last rule is why this method resolves its own
// recipient instead of taking one: the caller cannot see the mute/block
// relationship, and for a long time neither did this path. A follow was the one
// action a muted or blocked account could still take to put its username in the
// muter's inbox — and a repeatable one, because the handler raises the
// notification whenever the follow row is genuinely new, so unfollow and follow
// again produced another.
//
// No resolved recipient is a silent no-op, not an error.
//
// Best-effort, like every other Notify* method: the caller treats a returned
// error as non-fatal.
func (s *Service) NotifyFollow(ctx context.Context, actorID, channelID uuid.UUID) error {
	recipient, err := s.repo.FollowNotificationRecipient(ctx, sqlcgen.FollowNotificationRecipientParams{
		ChannelID:  channelID,
		FollowerID: actorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !s.typeEnabled(ctx, recipient, TypeFollow) {
		return nil
	}
	_, err = s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:    recipient,
		Type:      TypeFollow,
		ActorID:   pgconv.UUID(actorID),
		ChannelID: pgconv.UUID(channelID),
	})
	return err
}

// NotifyComment records that actorID commented on a video, telling that video's
// OWNER, and returns the recipient it notified (uuid.Nil when nobody was).
//
// Like NotifyCommentReply, every selection rule lives in the SQL (see
// CommentVideoOwnerRecipient): the comment must be a live, locally authored
// comment; the owner is never told about their own comment; the owner must be an
// active, non-deleted account; and a muted or blocked commenter never reaches
// the owner's notification surface. That last rule is why this method resolves
// its own recipient instead of taking one: the caller cannot see the mute/block
// relationship, and for a long time neither did this path — an account the owner
// had muted still reached their inbox by commenting, carrying the comment id
// back to content the mute had hidden.
//
// No resolved recipient is a silent no-op, not an error.
//
// Best-effort, like every other Notify* method: the caller treats an error as
// non-fatal — a notification failure must never fail the comment.
func (s *Service) NotifyComment(ctx context.Context, actorID, commentID uuid.UUID) (uuid.UUID, error) {
	row, err := s.repo.CommentVideoOwnerRecipient(ctx, commentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	recipient := row.RecipientID
	if recipient == actorID || !s.typeEnabled(ctx, recipient, TypeComment) {
		return uuid.Nil, nil
	}
	if _, err := s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:    recipient,
		Type:      TypeComment,
		ActorID:   pgconv.UUID(actorID),
		VideoID:   pgconv.UUID(row.VideoID),
		CommentID: pgconv.UUID(commentID),
	}); err != nil {
		return uuid.Nil, err
	}
	return recipient, nil
}

// NotifyCommentReply records that actorID replied to someone else's comment,
// and returns the recipient it notified (uuid.Nil when nobody was notified).
//
// It answers a DIFFERENT question from NotifyComment: NotifyComment tells the
// VIDEO OWNER that their video was commented on, which leaves the person
// actually being answered — who usually does not own the video — with nothing.
// This is the reply's own recipient.
//
// Every selection rule lives in the SQL (see CommentReplyRecipient): the
// comment must be a reply whose parent still exists and was authored by a
// local, active, non-deleted user; neither comment may be a tombstone; the
// replier is never notified of their own reply; and a muted or blocked replier
// never reaches the parent author's notification surface. No resolved
// recipient — the normal answer for a top-level comment — is a silent no-op,
// not an error.
//
// The returned recipient is what lets the caller keep the two comment
// notifications mutually exclusive when the parent's author IS the video
// owner: that user is answered once, not told twice about one reply.
//
// Best-effort, like every other Notify* method: the caller treats an error as
// non-fatal — a notification failure must never fail the comment.
func (s *Service) NotifyCommentReply(ctx context.Context, actorID, commentID uuid.UUID) (uuid.UUID, error) {
	row, err := s.repo.CommentReplyRecipient(ctx, commentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	if !row.RecipientID.Valid {
		return uuid.Nil, nil
	}
	recipient := uuid.UUID(row.RecipientID.Bytes)
	if recipient == actorID || !s.typeEnabled(ctx, recipient, TypeCommentReply) {
		return uuid.Nil, nil
	}
	if _, err := s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:    recipient,
		Type:      TypeCommentReply,
		ActorID:   pgconv.UUID(actorID),
		VideoID:   pgconv.UUID(row.VideoID),
		CommentID: pgconv.UUID(commentID),
	}); err != nil {
		return uuid.Nil, err
	}
	return recipient, nil
}

// NotifyMessage records that actorID sent recipientID a direct message in a
// conversation. Notifying yourself is a no-op, as is a recipient who disabled
// message notifications. Best-effort. The message body is deliberately NOT
// stored on the notification (no message plaintext leaks into the notification
// surface).
func (s *Service) NotifyMessage(ctx context.Context, recipientID, actorID, conversationID uuid.UUID) error {
	if recipientID == actorID || !s.typeEnabled(ctx, recipientID, TypeMessage) {
		return nil
	}
	_, err := s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:         recipientID,
		Type:           TypeMessage,
		ActorID:        pgconv.UUID(actorID),
		ConversationID: pgconv.UUID(conversationID),
	})
	return err
}

// NotifyNewVideo tells the followers of a video's channel that it just went
// live, and reports how many followers were notified. It is THE fan-out for the
// "new video from a channel you follow" notification and is wired to the publish
// transition (video.WithPublishHook), so it runs for direct publishes, scheduled
// publishes, releases of a publish-after-transcode hold, and moderator approvals
// alike.
//
// Every rule lives in the SQL (see NotifyFollowersOfNewVideo): the video must be
// published + public + unblocked, the follower's per-channel bell must be 'all',
// their global new_video preference must not be off, neither mute nor block may
// stand between them and the owner, and the owner never notifies themselves.
// Doing it in one statement — rather than a row-per-follower loop through
// CreateNotification like every other Notify* method — is what keeps a publish
// to a large follower list a single bounded round trip instead of an N+1 storm.
// A repeat call for the same video inserts nothing (the partial unique index in
// migration 0101), so hooks that fire more than once are safe.
//
// Best-effort, like the other Notify* methods: the caller treats an error as
// non-fatal — a notification failure must never fail or reverse a publish.
//
// Scale note: the insert is synchronous with the publish. A channel with an
// extreme follower count therefore pays that insert inside the publishing
// request; moving the fan-out to the durable job framework is the documented
// next step if that ever bites (it does not at self-hosted instance scale).
func (s *Service) NotifyNewVideo(ctx context.Context, videoID uuid.UUID) (int64, error) {
	return s.repo.NotifyFollowersOfNewVideo(ctx, videoID)
}

// NotifyNewReport tells every active admin and moderator that a user just
// filed an abuse report, and reports how many staff members were notified. It
// is the set-based fan-out behind the moderation queue's push signal, fired by
// the five report-creation handlers for genuinely new reports only (an
// idempotent repeat report never reaches it).
//
// Every rule lives in the SQL (see NotifyStaffOfNewReport): only active,
// non-deleted admins/moderators are told, a staff reporter is never told about
// their own filing, and a recipient who turned the new_report type off is
// skipped. A repeat call for the same report inserts nothing (the partial
// unique index in migration 0103).
//
// Best-effort, like the other Notify* methods: the caller treats an error as
// non-fatal — a notification failure must never fail the report itself.
func (s *Service) NotifyNewReport(ctx context.Context, reportID uuid.UUID) (int64, error) {
	return s.repo.NotifyStaffOfNewReport(ctx, reportID)
}

// NotifyReportResolved records that a moderator (actorID) resolved recipientID's
// abuse report. Resolving your own report is a no-op, as is a recipient who
// disabled report_resolved notifications. Best-effort. The moderator's identity
// is deliberately NOT stored on the notification (actor_id stays null) so it is
// never exposed to the reporter — the report's status and target type are
// resolved from the joined report row at read time.
func (s *Service) NotifyReportResolved(ctx context.Context, recipientID, actorID, reportID uuid.UUID) error {
	if recipientID == actorID || !s.typeEnabled(ctx, recipientID, TypeReportResolved) {
		return nil
	}
	_, err := s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:   recipientID,
		Type:     TypeReportResolved,
		ReportID: pgconv.UUID(reportID),
	})
	return err
}

// NotifyVideoRejected records that a moderator (actorID) rejected recipientID's
// quarantined upload (product-decisions.md §11). Rejecting your own video is a
// no-op, as is a recipient who disabled video_rejected notifications.
// Best-effort. The moderator's identity is deliberately NOT stored (actor_id
// stays null) so it is never exposed to the owner — the video's title is
// resolved from the joined video row at read time.
func (s *Service) NotifyVideoRejected(ctx context.Context, recipientID, actorID, videoID uuid.UUID) error {
	if recipientID == actorID || !s.typeEnabled(ctx, recipientID, TypeVideoRejected) {
		return nil
	}
	_, err := s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:  recipientID,
		Type:    TypeVideoRejected,
		VideoID: pgconv.UUID(videoID),
	})
	return err
}

// NotifyVideoBlocked records that a moderator (actorID) blocked recipientID's
// published video. Blocking your own video is a no-op, as is a recipient who
// disabled video_blocked notifications. Best-effort.
//
// Like NotifyVideoRejected it stores no actor, so the moderator is never exposed
// to the creator — and unlike the rejection it carries NO note: whether a
// creator may read the block reason is an open product ruling, and the safe
// default is the neutral fact. The video's title is resolved from the joined
// video row at read time.
func (s *Service) NotifyVideoBlocked(ctx context.Context, recipientID, actorID, videoID uuid.UUID) error {
	if recipientID == actorID || !s.typeEnabled(ctx, recipientID, TypeVideoBlocked) {
		return nil
	}
	_, err := s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:  recipientID,
		Type:    TypeVideoBlocked,
		VideoID: pgconv.UUID(videoID),
	})
	return err
}

// NotifyCaptionReady records that an auto-generated caption track finished for
// recipientID's video (fix_plan P13). Unlike the other notifications this has no
// actor — it is a system event addressed to the video's owner — so there is no
// self-notify skip; it is suppressed only when the recipient disabled
// caption_ready notifications. Best-effort: the caption is stored regardless of
// whether the notification records. The video's title is resolved from the
// joined video row at read time (no caption text is stored on the notification).
func (s *Service) NotifyCaptionReady(ctx context.Context, recipientID, videoID uuid.UUID) error {
	if !s.typeEnabled(ctx, recipientID, TypeCaptionReady) {
		return nil
	}
	_, err := s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:  recipientID,
		Type:    TypeCaptionReady,
		VideoID: pgconv.UUID(videoID),
	})
	return err
}

// List returns the user's notifications, newest first. When unreadOnly is true,
// only unread notifications are returned. The caller clamps limit/offset.
func (s *Service) List(ctx context.Context, userID uuid.UUID, unreadOnly bool, limit, offset int32) ([]Item, int64, error) {
	rows, err := s.repo.ListNotifications(ctx, sqlcgen.ListNotificationsParams{
		UserID:       userID,
		UnreadOnly:   unreadOnly,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	// The LIST total, honouring ?unread — distinct from the unread badge, which
	// says nothing about how many read notifications sit behind the page.
	total, err := s.repo.CountNotifications(ctx, sqlcgen.CountNotificationsParams{
		UserID: userID, UnreadOnly: unreadOnly,
	})
	if err != nil {
		return nil, 0, err
	}
	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, Item{
			ID:                 r.ID,
			Type:               r.Type,
			Read:               r.ReadAt.Valid,
			CreatedAt:          r.CreatedAt,
			ActorUsername:      pgconv.Deref(r.ActorUsername),
			ActorDisplayName:   pgconv.Deref(r.ActorDisplayName),
			ChannelHandle:      pgconv.Deref(r.ChannelHandle),
			ChannelDisplayName: pgconv.Deref(r.ChannelDisplayName),
			VideoID:            uuidString(r.VideoID),
			VideoTitle:         pgconv.Deref(r.VideoTitle),
			CommentID:          uuidString(r.CommentID),
			ConversationID:     uuidString(r.ConversationID),
			ReportID:           uuidString(r.ReportID),
			ReportStatus:       pgconv.Deref(r.ReportStatus),
			ReportTargetType:   pgconv.Deref(r.ReportTargetType),
			ModerationNote:     r.ModerationNote,
		})
	}
	return items, total, nil
}

// UnreadCount returns how many unread notifications the user has.
func (s *Service) UnreadCount(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.repo.CountUnreadNotifications(ctx, userID)
}

// MarkRead marks one of the user's notifications read (idempotent). An unknown
// id, or one belonging to another user, returns ErrNotFound.
func (s *Service) MarkRead(ctx context.Context, userID, notifID uuid.UUID) error {
	n, err := s.repo.MarkNotificationRead(ctx, sqlcgen.MarkNotificationReadParams{ID: notifID, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllRead marks all of the user's unread notifications read (idempotent).
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	return s.repo.MarkAllNotificationsRead(ctx, userID)
}

// Prefs returns the user's per-type notification preferences: every known type,
// defaulting to enabled, overlaid with any stored rows. Stored rows for types
// that are no longer known are ignored.
func (s *Service) Prefs(ctx context.Context, userID uuid.UUID) (map[string]bool, error) {
	prefs := make(map[string]bool, len(KnownTypes()))
	for _, typ := range KnownTypes() {
		prefs[typ] = true
	}
	rows, err := s.repo.ListNotificationPrefs(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if knownType(r.Type) {
			prefs[r.Type] = r.Enabled
		}
	}
	return prefs, nil
}

// SetPrefs applies a partial preference update: only the types present in
// changes are touched. Any unknown type rejects the whole update with
// ErrUnknownType (nothing is written). Upserts run in KnownTypes order so the
// write pattern is deterministic.
func (s *Service) SetPrefs(ctx context.Context, userID uuid.UUID, changes map[string]bool) error {
	for typ := range changes {
		if !knownType(typ) {
			return ErrUnknownType
		}
	}
	for _, typ := range KnownTypes() {
		enabled, ok := changes[typ]
		if !ok {
			continue
		}
		if err := s.repo.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{
			UserID:  userID,
			Type:    typ,
			Enabled: enabled,
		}); err != nil {
			return err
		}
	}
	return nil
}

// uuidString renders a (possibly null) pgtype.UUID, returning "" when null.
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
