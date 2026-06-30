// Package moderation implements abuse reports for vidra-core: a user reports a
// video or comment with a reason, and moderators/admins triage the queue
// (accept/reject with an internal note). It is HTTP-agnostic and testable
// without a server.
package moderation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Report target types and resolution statuses.
const (
	TargetVideo   = "video"
	TargetComment = "comment"

	StatusOpen     = "open"
	StatusAccepted = "accepted"
	StatusRejected = "rejected"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no report matches the lookup.
	ErrNotFound = errors.New("moderation: report not found")
	// ErrInvalidTarget means the reported target does not exist.
	ErrInvalidTarget = errors.New("moderation: invalid report target")
)

// Repository is the data access the moderation service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateVideoReport(ctx context.Context, arg sqlcgen.CreateVideoReportParams) (int64, error)
	CreateCommentReport(ctx context.Context, arg sqlcgen.CreateCommentReportParams) (int64, error)
	ListReports(ctx context.Context, arg sqlcgen.ListReportsParams) ([]sqlcgen.ListReportsRow, error)
	ResolveReport(ctx context.Context, arg sqlcgen.ResolveReportParams) (int64, error)
}

// Service holds the moderation application logic.
type Service struct {
	repo Repository
}

// NewService builds the moderation service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Item is a report with the reporter's username and target context resolved for
// the moderation queue. Empty strings mean the field is not applicable.
type Item struct {
	ID               uuid.UUID
	TargetType       string
	Reason           string
	Status           string
	ModeratorNote    string
	CreatedAt        time.Time
	ResolvedAt       *time.Time
	ReporterUsername string
	VideoID          string
	VideoTitle       string
	CommentID        string
	CommentBody      string
}

// ReportVideo records reporterID's report of a video (idempotent per
// reporter+video). The caller confirms the video is reportable first.
func (s *Service) ReportVideo(ctx context.Context, reporterID, videoID uuid.UUID, reason string) error {
	_, err := s.repo.CreateVideoReport(ctx, sqlcgen.CreateVideoReportParams{
		ReporterID: reporterID,
		VideoID:    pgUUID(videoID),
		Reason:     reason,
	})
	return err
}

// ReportComment records reporterID's report of a comment (idempotent per
// reporter+comment). An unknown comment → ErrInvalidTarget.
func (s *Service) ReportComment(ctx context.Context, reporterID, commentID uuid.UUID, reason string) error {
	_, err := s.repo.CreateCommentReport(ctx, sqlcgen.CreateCommentReportParams{
		ReporterID: reporterID,
		CommentID:  pgUUID(commentID),
		Reason:     reason,
	})
	if isForeignKeyViolation(err) {
		return ErrInvalidTarget
	}
	return err
}

// List returns the moderation queue, newest first. When openOnly is true, only
// unresolved reports are returned. The caller clamps limit/offset.
func (s *Service) List(ctx context.Context, openOnly bool, limit, offset int32) ([]Item, error) {
	rows, err := s.repo.ListReports(ctx, sqlcgen.ListReportsParams{
		OpenOnly:     openOnly,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, Item{
			ID:               r.ID,
			TargetType:       r.TargetType,
			Reason:           r.Reason,
			Status:           r.Status,
			ModeratorNote:    r.ModeratorNote,
			CreatedAt:        r.CreatedAt,
			ResolvedAt:       timePtr(r.ResolvedAt),
			ReporterUsername: r.ReporterUsername,
			VideoID:          uuidString(r.VideoID),
			VideoTitle:       deref(r.VideoTitle),
			CommentID:        uuidString(r.CommentID),
			CommentBody:      deref(r.CommentBody),
		})
	}
	return items, nil
}

// Resolve marks a report accepted/rejected with a moderator note. status must be
// StatusAccepted or StatusRejected (validated by the caller). An unknown id →
// ErrNotFound.
func (s *Service) Resolve(ctx context.Context, moderatorID, reportID uuid.UUID, status, note string) error {
	n, err := s.repo.ResolveReport(ctx, sqlcgen.ResolveReportParams{
		ID:            reportID,
		Status:        status,
		ModeratorNote: note,
		ResolvedBy:    pgUUID(moderatorID),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// pgUUID wraps a uuid.UUID as a non-null pgtype.UUID for a query parameter.
func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// uuidString renders a (possibly null) pgtype.UUID, "" when null.
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// timePtr returns a pointer to a (possibly null) timestamp, nil when null.
func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// deref returns the value of a (possibly nil) string pointer, "" when nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// isForeignKeyViolation reports whether err is a PostgreSQL foreign-key
// violation (SQLSTATE 23503) — e.g. reporting a comment that does not exist.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
