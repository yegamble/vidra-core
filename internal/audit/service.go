// Package audit persists and reads the durable security-audit trail (auth,
// moderation, admin, and registration actions). Rows are append-only and never
// contain secrets/PII — only a stable action id, result, the actor's id, a safe
// reason, and the request id. It mirrors observability.AuditEvent, which remains
// the slog side of the same events.
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Repository is the data access the audit service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	InsertAuditLog(ctx context.Context, arg sqlcgen.InsertAuditLogParams) error
	ListAuditLog(ctx context.Context, arg sqlcgen.ListAuditLogParams) ([]sqlcgen.ListAuditLogRow, error)
}

// Service persists and reads the audit trail.
type Service struct{ repo Repository }

// NewService builds the audit service.
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Event is a security-audit record to persist. ActorID is the actor's user id as
// a string ("" when unauthenticated); a non-UUID/empty value is stored as NULL.
type Event struct {
	Action    string
	Result    string
	ActorID   string
	Reason    string
	RequestID string
}

// Record appends an audit event.
func (s *Service) Record(ctx context.Context, ev Event) error {
	return s.repo.InsertAuditLog(ctx, sqlcgen.InsertAuditLogParams{
		Action:    ev.Action,
		Result:    ev.Result,
		ActorID:   parseActor(ev.ActorID),
		Reason:    ev.Reason,
		RequestID: ev.RequestID,
	})
}

// Entry is an audit row for the admin view. ActorUsername is "" when the actor is
// unknown, deleted, or unauthenticated.
type Entry struct {
	ID            uuid.UUID
	Action        string
	Result        string
	ActorID       string
	ActorUsername string
	Reason        string
	RequestID     string
	OccurredAt    time.Time
}

// List returns audit entries newest first, optionally filtered by action (empty
// = all). The caller clamps limit/offset.
func (s *Service) List(ctx context.Context, action string, limit, offset int32) ([]Entry, error) {
	var actionFilter *string
	if action != "" {
		actionFilter = &action
	}
	rows, err := s.repo.ListAuditLog(ctx, sqlcgen.ListAuditLogParams{
		Action:       actionFilter,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		e := Entry{
			ID: r.ID, Action: r.Action, Result: r.Result, Reason: r.Reason,
			RequestID: r.RequestID, OccurredAt: r.OccurredAt,
		}
		if r.ActorID.Valid {
			e.ActorID = uuid.UUID(r.ActorID.Bytes).String()
		}
		if r.ActorUsername != nil {
			e.ActorUsername = *r.ActorUsername
		}
		out = append(out, e)
	}
	return out, nil
}

// parseActor renders a string actor id as a nullable pgtype.UUID; "" or a
// non-UUID becomes NULL.
func parseActor(s string) pgtype.UUID {
	if id, err := uuid.Parse(s); err == nil {
		return pgtype.UUID{Bytes: id, Valid: true}
	}
	return pgtype.UUID{}
}
