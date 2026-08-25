// Package audit persists and reads the durable, low-volume security-audit trail.
// Typed, bounded envelopes correlate to requests, traces and jobs without copying
// raw payloads or content. Routine/high-volume content activity is deliberately a
// separate stream; job execution details belong in job_events.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/trace"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Repository is the data access the audit service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	InsertAuditLog(ctx context.Context, arg sqlcgen.InsertAuditLogParams) error
	ListAuditLog(ctx context.Context, arg sqlcgen.ListAuditLogParams) ([]sqlcgen.ListAuditLogRow, error)
	CountAuditLog(ctx context.Context, action *string) (int64, error)
}

// Service persists and reads the audit trail.
type Service struct{ repo Repository }

// NewService builds the audit service.
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Record validates, bounds and appends a typed audit event. An empty actor id is
// stored as NULL; a user actor with a non-UUID id is rejected.
func (s *Service) Record(ctx context.Context, ev Event) error {
	ids := observability.CorrelationFromContext(ctx)
	if ev.RequestID == "" {
		ev.RequestID = ids.RequestID
	}
	if ev.CorrelationID == "" {
		ev.CorrelationID = ids.CorrelationID
	}
	if ev.TraceID == "" {
		if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
			ev.TraceID = sc.TraceID().String()
		}
	}
	n, err := normalizeEvent(ev)
	if err != nil {
		return err
	}
	return s.repo.InsertAuditLog(ctx, sqlcgen.InsertAuditLogParams{
		ID:            n.ID,
		SchemaVersion: n.SchemaVersion,
		Domain:        n.Domain,
		Action:        n.Action,
		Result:        n.Result,
		ActorID:       parseActor(n.Actor.ID),
		ActorKind:     n.Actor.Kind,
		ActorRole:     n.Actor.Role,
		Reason:        n.Reason,
		RequestID:     n.RequestID,
		CorrelationID: n.CorrelationID,
		TraceID:       n.TraceID,
		PipelineRunID: nullableUUID(n.PipelineRunID),
		JobID:         nullableUUID(n.JobID),
		ResourceType:  n.ResourceType,
		ResourceID:    n.ResourceID,
		Metadata:      n.metadataJSON,
		Changes:       n.changesJSON,
	})
}

// Entry is an audit row for the admin view. ActorUsername is "" when the actor is
// unknown, deleted, or unauthenticated.
type Entry struct {
	ID            uuid.UUID
	SchemaVersion int16
	Domain        string
	Action        string
	Result        string
	ActorID       string
	ActorKind     string
	ActorRole     string
	ActorUsername string
	Reason        string
	RequestID     string
	CorrelationID string
	TraceID       string
	PipelineRunID string
	JobID         string
	ResourceType  string
	ResourceID    string
	Metadata      map[string]string
	Changes       []Change
	OccurredAt    time.Time
}

// List returns audit entries newest first, optionally filtered by action (empty
// = all), with how many entries match the same filter. The caller clamps
// limit/offset.
func (s *Service) List(ctx context.Context, action string, limit, offset int32) ([]Entry, int64, error) {
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
		return nil, 0, err
	}
	total, err := s.repo.CountAuditLog(ctx, actionFilter)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		e := Entry{
			ID: r.ID, SchemaVersion: r.SchemaVersion, Domain: r.Domain,
			Action: r.Action, Result: r.Result, ActorKind: r.ActorKind,
			ActorRole: r.ActorRole, Reason: r.Reason, RequestID: r.RequestID,
			CorrelationID: r.CorrelationID, TraceID: r.TraceID,
			ResourceType: r.ResourceType, ResourceID: r.ResourceID,
			OccurredAt: r.OccurredAt,
		}
		if r.ActorID.Valid {
			e.ActorID = uuid.UUID(r.ActorID.Bytes).String()
		}
		if r.ActorUsername != nil {
			e.ActorUsername = *r.ActorUsername
		}
		if r.PipelineRunID.Valid {
			e.PipelineRunID = uuid.UUID(r.PipelineRunID.Bytes).String()
		}
		if r.JobID.Valid {
			e.JobID = uuid.UUID(r.JobID.Bytes).String()
		}
		if err := json.Unmarshal(r.Metadata, &e.Metadata); err != nil {
			return nil, 0, fmt.Errorf("audit: decode metadata for %s: %w", r.ID, err)
		}
		if err := json.Unmarshal(r.Changes, &e.Changes); err != nil {
			return nil, 0, fmt.Errorf("audit: decode changes for %s: %w", r.ID, err)
		}
		out = append(out, e)
	}
	return out, total, nil
}

// parseActor renders a string actor id as a nullable pgtype.UUID; "" or a
// non-UUID becomes NULL.
func parseActor(s string) pgtype.UUID {
	if id, err := uuid.Parse(s); err == nil {
		return pgtype.UUID{Bytes: id, Valid: true}
	}
	return pgtype.UUID{}
}

func nullableUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
