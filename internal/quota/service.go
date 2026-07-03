// Package quota implements per-user storage quotas for vidra-core: resolving
// the quota that applies to an account (per-user override, else the instance
// default) and computing the account's current usage by aggregating its stored
// video files (originals, renditions, thumbnails) across the videos owned via
// its channels. It is HTTP-agnostic and testable without a server; the HTTP
// layer enforces the quota on upload/import and serves GET /me/quota from it.
package quota

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrExceeded means storing the incoming bytes would push the user past their
// effective storage quota. The HTTP layer maps it to 422 quota_exceeded.
var ErrExceeded = errors.New("quota: storage quota exceeded")

// Repository is the data access the quota service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	SumUserStorageUsage(ctx context.Context, ownerID uuid.UUID) (int64, error)
}

// Service resolves effective quotas and current usage.
type Service struct {
	repo Repository
	// defaultBytes is the instance-wide default quota (0 = unlimited), from
	// INSTANCE_DEFAULT_QUOTA_BYTES.
	defaultBytes int64
}

// NewService builds the quota service. defaultBytes is the instance default
// quota in bytes (0 = unlimited).
func NewService(repo Repository, defaultBytes int64) *Service {
	return &Service{repo: repo, defaultBytes: defaultBytes}
}

// Effective resolves the storage quota that applies to an account: the
// per-user override when set (non-NULL), else the instance default. A resolved
// value of 0 (at either level) means unlimited, reported as nil so callers
// need no sentinel. Pure — the seam unit tests exercise directly.
func Effective(override *int64, instanceDefault int64) *int64 {
	v := instanceDefault
	if override != nil {
		v = *override
	}
	if v <= 0 {
		return nil // unlimited
	}
	return &v
}

// Status is an account's storage picture: bytes currently stored and the
// effective cap (nil = unlimited).
type Status struct {
	UsedBytes  int64
	QuotaBytes *int64
}

// Status returns the user's current usage and effective quota.
func (s *Service) Status(ctx context.Context, userID uuid.UUID) (Status, error) {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return Status{}, err
	}
	used, err := s.repo.SumUserStorageUsage(ctx, userID)
	if err != nil {
		return Status{}, err
	}
	return Status{UsedBytes: used, QuotaBytes: Effective(u.StorageQuotaBytes, s.defaultBytes)}, nil
}

// Remaining reports how many more bytes the user may store before hitting
// their effective quota (clamped at 0) and whether they are limited at all.
// limited=false means unlimited — callers must not enforce anything.
func (s *Service) Remaining(ctx context.Context, userID uuid.UUID) (remaining int64, limited bool, err error) {
	st, err := s.Status(ctx, userID)
	if err != nil {
		return 0, false, err
	}
	if st.QuotaBytes == nil {
		return 0, false, nil
	}
	rem := *st.QuotaBytes - st.UsedBytes
	if rem < 0 {
		rem = 0
	}
	return rem, true, nil
}

// CheckFits returns ErrExceeded when storing incoming additional bytes would
// exceed the user's effective quota; nil when it fits or the user is unlimited.
func (s *Service) CheckFits(ctx context.Context, userID uuid.UUID, incoming int64) error {
	remaining, limited, err := s.Remaining(ctx, userID)
	if err != nil {
		return err
	}
	if limited && incoming > remaining {
		return ErrExceeded
	}
	return nil
}
