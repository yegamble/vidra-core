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
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrExceeded means storing the incoming bytes would push the user past their
// effective storage quota. The HTTP layer maps it to 422 quota_exceeded.
var ErrExceeded = errors.New("quota: storage quota exceeded")

// ErrDailyExceeded means storing the incoming bytes would push the user past
// the instance's rolling-24h daily upload quota (config-parity W7,
// default_user_daily_quota_bytes). The HTTP layer maps it to 422
// daily_quota_exceeded.
var ErrDailyExceeded = errors.New("quota: daily upload quota exceeded")

// dailyWindow is the rolling daily-quota window. ROLLING trailing 24h — not a
// calendar day — so the cap cannot be doubled by uploading just before and
// after midnight, and behaviour is timezone-independent (documented deviation
// detail; PeerTube's daily quota is also a rolling counter).
const dailyWindow = 24 * time.Hour

// usageRetention is how long recorded upload-usage events are kept: double the
// window, pruned opportunistically at record time (no dedicated sweeper).
const usageRetention = 2 * dailyWindow

// Repository is the data access the quota service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	SumUserStorageUsage(ctx context.Context, ownerID uuid.UUID) (int64, error)
	// Rolling daily-upload ledger (upload_usage_events).
	RecordUploadUsageEvent(ctx context.Context, arg sqlcgen.RecordUploadUsageEventParams) error
	SumUploadUsageSince(ctx context.Context, arg sqlcgen.SumUploadUsageSinceParams) (int64, error)
	PruneUploadUsageEvents(ctx context.Context, arg sqlcgen.PruneUploadUsageEventsParams) (int64, error)
}

// Service resolves effective quotas and current usage.
type Service struct {
	repo Repository
	// defaultBytes is the instance-wide default quota (0 = unlimited), from
	// INSTANCE_DEFAULT_QUOTA_BYTES.
	defaultBytes int64
	// defaultBytesFn, when set, supersedes defaultBytes so the instance default
	// quota can be resolved live from the instance-settings overlay. Boot wires it
	// to settingssvc.Int(default_user_quota_bytes); nil in the plain constructor.
	defaultBytesFn func() int64
	// dailyBytesFn resolves the instance's rolling-24h daily upload quota
	// (default_user_daily_quota_bytes; 0/nil = unlimited). There is
	// deliberately NO per-user daily override in v1 — the key is the single
	// instance-wide control (documented deviation from PeerTube's per-user
	// videoQuotaDaily).
	dailyBytesFn func() int64
	// now is the injectable clock the rolling window is computed against.
	now func() time.Time
}

// Option customises the quota service.
type Option func(*Service)

// WithDefaultBytesFunc makes the instance-default quota dynamic: f is consulted
// per Status so an admin can retune INSTANCE_DEFAULT_QUOTA_BYTES's overlay at
// runtime. When set it supersedes the constructor's defaultBytes.
func WithDefaultBytesFunc(f func() int64) Option {
	return func(s *Service) { s.defaultBytesFn = f }
}

// WithDailyBytesFunc wires the live default_user_daily_quota_bytes setting
// (0 = unlimited). Nil is ignored (no daily quota).
func WithDailyBytesFunc(f func() int64) Option {
	return func(s *Service) {
		if f != nil {
			s.dailyBytesFn = f
		}
	}
}

// WithClock overrides the rolling-window clock (tests). Nil is ignored.
func WithClock(f func() time.Time) Option {
	return func(s *Service) {
		if f != nil {
			s.now = f
		}
	}
}

// NewService builds the quota service. defaultBytes is the instance default
// quota in bytes (0 = unlimited); WithDefaultBytesFunc can override it live.
func NewService(repo Repository, defaultBytes int64, opts ...Option) *Service {
	s := &Service{repo: repo, defaultBytes: defaultBytes, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// effectiveDefaultBytes resolves the current instance-default quota: the live
// overlay value when a provider is wired, else the static constructor value.
func (s *Service) effectiveDefaultBytes() int64 {
	if s.defaultBytesFn != nil {
		return s.defaultBytesFn()
	}
	return s.defaultBytes
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
	return Status{UsedBytes: used, QuotaBytes: Effective(u.StorageQuotaBytes, s.effectiveDefaultBytes())}, nil
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

// --- rolling-24h daily upload quota (config-parity W7) ---
//
// ACCOUNTING DESIGN (documented choice): a dedicated append-only ledger
// (upload_usage_events) written when an original file is stored, summed over
// the trailing 24h — NOT a sum over video_files rows. Summing live files
// would let a user bypass the daily cap by deleting and re-uploading (the
// delete would "refund" the window), and upload sessions are not retained
// after completion, so they are not queryable either. The ledger is pruned
// opportunistically at record time (usageRetention), so no sweeper is needed.
// Enforcement is RACE-TOLERANT by design: two concurrent uploads may both
// pass the check and overshoot the cap by at most one file — acceptable per
// the wave spec, and identical to the total-quota gate's posture.

// dailyQuotaBytes resolves the effective instance daily quota (nil =
// unlimited/not configured).
func (s *Service) dailyQuotaBytes() *int64 {
	if s.dailyBytesFn == nil {
		return nil
	}
	v := s.dailyBytesFn()
	if v <= 0 {
		return nil
	}
	return &v
}

// DailyStatus is an account's rolling-24h upload picture: bytes stored in the
// trailing window and the effective daily cap (nil = unlimited).
type DailyStatus struct {
	UsedBytes  int64
	QuotaBytes *int64
}

// Daily returns the user's rolling-24h upload usage and the effective daily
// quota. When no daily quota is configured the ledger is not queried
// (UsedBytes 0, QuotaBytes nil) — the hot upload path pays nothing until an
// admin turns the knob.
func (s *Service) Daily(ctx context.Context, userID uuid.UUID) (DailyStatus, error) {
	quota := s.dailyQuotaBytes()
	if quota == nil {
		return DailyStatus{}, nil
	}
	used, err := s.repo.SumUploadUsageSince(ctx, sqlcgen.SumUploadUsageSinceParams{
		UserID:    userID,
		CreatedAt: s.now().Add(-dailyWindow),
	})
	if err != nil {
		return DailyStatus{}, err
	}
	return DailyStatus{UsedBytes: used, QuotaBytes: quota}, nil
}

// CheckDailyFits returns ErrDailyExceeded when storing incoming additional
// bytes would exceed the rolling-24h daily quota; nil when it fits or no
// daily quota is configured.
func (s *Service) CheckDailyFits(ctx context.Context, userID uuid.UUID, incoming int64) error {
	st, err := s.Daily(ctx, userID)
	if err != nil {
		return err
	}
	if st.QuotaBytes == nil {
		return nil
	}
	if incoming > *st.QuotaBytes-st.UsedBytes {
		return ErrDailyExceeded
	}
	return nil
}

// RecordUpload appends a daily-usage ledger event for bytes the user just
// stored and opportunistically prunes that user's events older than the
// retention horizon. Events are recorded even while no daily quota is
// configured, so flipping the setting on is immediately meaningful.
func (s *Service) RecordUpload(ctx context.Context, userID uuid.UUID, bytes int64) error {
	if bytes <= 0 {
		return nil
	}
	if err := s.repo.RecordUploadUsageEvent(ctx, sqlcgen.RecordUploadUsageEventParams{
		UserID: userID,
		Bytes:  bytes,
	}); err != nil {
		return err
	}
	// Best-effort prune: a failure never surfaces (the next record retries).
	_, _ = s.repo.PruneUploadUsageEvents(ctx, sqlcgen.PruneUploadUsageEventsParams{
		UserID:    userID,
		CreatedAt: s.now().Add(-usageRetention),
	})
	return nil
}
