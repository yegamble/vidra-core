// Package instancemod implements instance-level moderation for vidra-core
// (.ralph/specs/federation-remote-content.md §8): per-user mutes of whole
// remote instances (content from the domain hidden from that user's surfaces)
// and the admin instance blocklist (inbound activities dropped, existing
// content hidden everywhere, outbound deliveries cancelled). This package owns
// the domain model + management; the filtering/drop effects are applied by the
// surfaces that read these tables (federation inbox/drain, remote-video reads).
// It is HTTP-agnostic and testable without a server.
package instancemod

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrInvalidDomain means the submitted instance domain is not a bare, valid
// hostname (optionally with a port).
var ErrInvalidDomain = errors.New("instancemod: invalid instance domain")

// maxDomainLen is the DNS hostname length cap.
const maxDomainLen = 253

// maxReasonLen bounds the stored admin block reason.
const maxReasonLen = 2000

// Repository is the data access the service needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	MuteInstance(ctx context.Context, arg sqlcgen.MuteInstanceParams) (int64, error)
	UnmuteInstance(ctx context.Context, arg sqlcgen.UnmuteInstanceParams) (int64, error)
	ListMutedInstances(ctx context.Context, arg sqlcgen.ListMutedInstancesParams) ([]sqlcgen.ListMutedInstancesRow, error)
	CountMutedInstances(ctx context.Context, muterID uuid.UUID) (int64, error)
	BlockInstance(ctx context.Context, arg sqlcgen.BlockInstanceParams) (int64, error)
	UnblockInstance(ctx context.Context, domain string) (int64, error)
	ListBlockedInstances(ctx context.Context, arg sqlcgen.ListBlockedInstancesParams) ([]sqlcgen.BlockedInstance, error)
	CountBlockedInstances(ctx context.Context) (int64, error)
}

// Service holds the instance-moderation application logic.
type Service struct {
	repo Repository
}

// NewService builds the instance-moderation service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// MutedInstance is one instance a user has muted.
type MutedInstance struct {
	Domain  string
	MutedAt time.Time
}

// BlockedInstance is one admin-blocked instance.
type BlockedInstance struct {
	Domain    string
	Reason    string
	BlockedBy *uuid.UUID
	BlockedAt time.Time
}

// NormalizeDomain lowercases and validates a bare instance domain (host, or
// host:port for dev origins). Schemes, paths, userinfo, spaces, and over-long
// values → ErrInvalidDomain.
func NormalizeDomain(raw string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(raw))
	if d == "" || len(d) > maxDomainLen ||
		strings.ContainsAny(d, "/\\?#@ \t") || strings.Contains(d, "://") {
		return "", ErrInvalidDomain
	}
	u, err := url.Parse("https://" + d)
	if err != nil || u.Host != d || u.Hostname() == "" {
		return "", ErrInvalidDomain
	}
	return d, nil
}

// MuteInstance records that muterID mutes the given instance domain.
// Idempotent. An invalid domain → ErrInvalidDomain.
func (s *Service) MuteInstance(ctx context.Context, muterID uuid.UUID, domain string) error {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return err
	}
	_, err = s.repo.MuteInstance(ctx, sqlcgen.MuteInstanceParams{MuterID: muterID, Domain: d})
	return err
}

// UnmuteInstance lifts muterID's mute of the given domain (idempotent).
func (s *Service) UnmuteInstance(ctx context.Context, muterID uuid.UUID, domain string) error {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return err
	}
	_, err = s.repo.UnmuteInstance(ctx, sqlcgen.UnmuteInstanceParams{MuterID: muterID, Domain: d})
	return err
}

// ListMutedInstances returns the instances muterID has muted, newest first.
// The caller clamps limit/offset.
func (s *Service) ListMutedInstances(ctx context.Context, muterID uuid.UUID, limit, offset int32) ([]MutedInstance, int64, error) {
	rows, err := s.repo.ListMutedInstances(ctx, sqlcgen.ListMutedInstancesParams{
		MuterID:      muterID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountMutedInstances(ctx, muterID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]MutedInstance, 0, len(rows))
	for _, r := range rows {
		out = append(out, MutedInstance{Domain: r.Domain, MutedAt: r.CreatedAt})
	}
	return out, total, nil
}

// BlockInstance adds a domain to the admin blocklist (idempotent; re-blocking
// refreshes the reason/admin). The reason is bounded, never rejected on length.
func (s *Service) BlockInstance(ctx context.Context, blockedBy uuid.UUID, domain, reason string) error {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return err
	}
	if len(reason) > maxReasonLen {
		reason = reason[:maxReasonLen]
	}
	_, err = s.repo.BlockInstance(ctx, sqlcgen.BlockInstanceParams{
		Domain:    d,
		Reason:    reason,
		BlockedBy: pgtype.UUID{Bytes: blockedBy, Valid: true},
	})
	return err
}

// UnblockInstance removes a domain from the blocklist (idempotent).
func (s *Service) UnblockInstance(ctx context.Context, domain string) error {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return err
	}
	_, err = s.repo.UnblockInstance(ctx, d)
	return err
}

// ListBlockedInstances returns the admin blocklist, newest block first. The
// caller clamps limit/offset.
func (s *Service) ListBlockedInstances(ctx context.Context, limit, offset int32) ([]BlockedInstance, int64, error) {
	rows, err := s.repo.ListBlockedInstances(ctx, sqlcgen.ListBlockedInstancesParams{
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountBlockedInstances(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]BlockedInstance, 0, len(rows))
	for _, r := range rows {
		bi := BlockedInstance{Domain: r.Domain, Reason: r.Reason, BlockedAt: r.CreatedAt}
		if r.BlockedBy.Valid {
			id := uuid.UUID(r.BlockedBy.Bytes)
			bi.BlockedBy = &id
		}
		out = append(out, bi)
	}
	return out, total, nil
}
