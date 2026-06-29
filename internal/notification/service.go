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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Notification type discriminators.
const (
	TypeFollow  = "follow"
	TypeComment = "comment"
)

// ErrNotFound means no notification matches the lookup for this user.
var ErrNotFound = errors.New("notification: not found")

// Repository is the data access the notification service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateNotification(ctx context.Context, arg sqlcgen.CreateNotificationParams) (sqlcgen.Notification, error)
	ListNotifications(ctx context.Context, arg sqlcgen.ListNotificationsParams) ([]sqlcgen.ListNotificationsRow, error)
	CountUnreadNotifications(ctx context.Context, userID uuid.UUID) (int64, error)
	MarkNotificationRead(ctx context.Context, arg sqlcgen.MarkNotificationReadParams) (int64, error)
	MarkAllNotificationsRead(ctx context.Context, userID uuid.UUID) error
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
}

// NotifyFollow records that actorID followed recipientID's channel. Notifying
// yourself (recipient == actor) is a no-op. Best-effort: the caller treats a
// returned error as non-fatal.
func (s *Service) NotifyFollow(ctx context.Context, recipientID, actorID, channelID uuid.UUID) error {
	if recipientID == actorID {
		return nil
	}
	_, err := s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:    recipientID,
		Type:      TypeFollow,
		ActorID:   pgUUID(actorID),
		ChannelID: pgUUID(channelID),
	})
	return err
}

// NotifyComment records that actorID commented on recipientID's video. Notifying
// yourself is a no-op. Best-effort.
func (s *Service) NotifyComment(ctx context.Context, recipientID, actorID, videoID, commentID uuid.UUID) error {
	if recipientID == actorID {
		return nil
	}
	_, err := s.repo.CreateNotification(ctx, sqlcgen.CreateNotificationParams{
		UserID:    recipientID,
		Type:      TypeComment,
		ActorID:   pgUUID(actorID),
		VideoID:   pgUUID(videoID),
		CommentID: pgUUID(commentID),
	})
	return err
}

// List returns the user's notifications, newest first. When unreadOnly is true,
// only unread notifications are returned. The caller clamps limit/offset.
func (s *Service) List(ctx context.Context, userID uuid.UUID, unreadOnly bool, limit, offset int32) ([]Item, error) {
	rows, err := s.repo.ListNotifications(ctx, sqlcgen.ListNotificationsParams{
		UserID:       userID,
		UnreadOnly:   unreadOnly,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, Item{
			ID:                 r.ID,
			Type:               r.Type,
			Read:               r.ReadAt.Valid,
			CreatedAt:          r.CreatedAt,
			ActorUsername:      deref(r.ActorUsername),
			ActorDisplayName:   deref(r.ActorDisplayName),
			ChannelHandle:      deref(r.ChannelHandle),
			ChannelDisplayName: deref(r.ChannelDisplayName),
			VideoID:            uuidString(r.VideoID),
			VideoTitle:         deref(r.VideoTitle),
			CommentID:          uuidString(r.CommentID),
		})
	}
	return items, nil
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

// pgUUID wraps a uuid.UUID as a non-null pgtype.UUID for a query parameter.
func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// uuidString renders a (possibly null) pgtype.UUID, returning "" when null.
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// deref returns the value of a (possibly nil) string pointer, "" when nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
