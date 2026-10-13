package blocklistsub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Errors the HTTP layer maps to statuses.
var (
	ErrInvalidURL    = errors.New("blocklistsub: invalid subscription URL")
	ErrInvalidFormat = errors.New("blocklistsub: unsupported format")
	ErrDuplicate     = errors.New("blocklistsub: already subscribed to that URL")
	ErrNotFound      = errors.New("blocklistsub: subscription not found")
)

const (
	// SyncInterval is how stale a subscription may get before the periodic job
	// refreshes it; DueTick is how often the job looks.
	SyncInterval = 6 * time.Hour
	DueTick      = 15 * time.Minute
	// maxRedeliver bounds how many removed domains one sync resumes deliveries
	// for; the rest simply stay cancelled, as they were while blocked.
	maxRedeliver = 200
	insertChunk  = 5000
)

// Repository is the data access the service needs; *sqlcgen.Queries satisfies it.
type Repository interface {
	CreateBlocklistSubscription(ctx context.Context, arg sqlcgen.CreateBlocklistSubscriptionParams) (sqlcgen.BlocklistSubscription, error)
	GetBlocklistSubscription(ctx context.Context, id uuid.UUID) (sqlcgen.BlocklistSubscription, error)
	ListBlocklistSubscriptions(ctx context.Context, arg sqlcgen.ListBlocklistSubscriptionsParams) ([]sqlcgen.ListBlocklistSubscriptionsRow, error)
	CountBlocklistSubscriptions(ctx context.Context) (int64, error)
	UpdateBlocklistSubscription(ctx context.Context, arg sqlcgen.UpdateBlocklistSubscriptionParams) (sqlcgen.BlocklistSubscription, error)
	DeleteBlocklistSubscription(ctx context.Context, id uuid.UUID) (int64, error)
	ListDueBlocklistSubscriptions(ctx context.Context, arg sqlcgen.ListDueBlocklistSubscriptionsParams) ([]sqlcgen.BlocklistSubscription, error)
	RecordBlocklistSyncResult(ctx context.Context, arg sqlcgen.RecordBlocklistSyncResultParams) error
	InsertSubscriptionBlocks(ctx context.Context, arg sqlcgen.InsertSubscriptionBlocksParams) ([]string, error)
	DeleteSubscriptionBlocksNotIn(ctx context.Context, arg sqlcgen.DeleteSubscriptionBlocksNotInParams) ([]sqlcgen.DeleteSubscriptionBlocksNotInRow, error)
	DeleteAllSubscriptionBlocksForSource(ctx context.Context, source string) ([]sqlcgen.DeleteAllSubscriptionBlocksForSourceRow, error)
	DeleteSubscriptionBlocksForDomain(ctx context.Context, domain string) ([]sqlcgen.DeleteSubscriptionBlocksForDomainRow, error)
	AddBlocklistAllow(ctx context.Context, arg sqlcgen.AddBlocklistAllowParams) (int64, error)
	RemoveBlocklistAllow(ctx context.Context, domain string) (int64, error)
	ListBlocklistAllow(ctx context.Context, arg sqlcgen.ListBlocklistAllowParams) ([]sqlcgen.BlocklistAllowlist, error)
	CountBlocklistAllow(ctx context.Context) (int64, error)
	ListAllBlocklistAllowDomains(ctx context.Context) ([]string, error)
}

// Auditor records the per-sync summary (satisfied by *audit.Service).
type Auditor interface {
	Record(ctx context.Context, ev audit.Event) error
}

// Service owns subscriptions, the allowlist and the sync.
type Service struct {
	repo        Repository
	fetch       Fetcher
	validate    func(string) error
	auditor     Auditor
	onUnblocked func(ctx context.Context, domain string, blockedAt time.Time)
	logger      *slog.Logger
	now         func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithAuditor records one audit row per sync.
func WithAuditor(a Auditor) Option { return func(s *Service) { s.auditor = a } }

// WithOnUnblocked is called for each block a sync removes (bounded), so the
// federation layer can resume deliveries the block cancelled.
func WithOnUnblocked(f func(ctx context.Context, domain string, blockedAt time.Time)) Option {
	return func(s *Service) { s.onUnblocked = f }
}

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.logger = l } }

// NewService builds the service. validate is the URL pre-flight (nil accepts
// any non-empty URL; the fetcher still guards every request).
func NewService(repo Repository, f Fetcher, validate func(string) error, opts ...Option) *Service {
	s := &Service{repo: repo, fetch: f, validate: validate, logger: slog.Default(), now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Create adds a subscription. It does not sync; the periodic job (or sync-now)
// does.
func (s *Service) Create(ctx context.Context, actor uuid.UUID, rawURL, label, format string, enabled bool) (sqlcgen.BlocklistSubscription, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || len(rawURL) > 2048 || (s.validate != nil && s.validate(rawURL) != nil) {
		return sqlcgen.BlocklistSubscription{}, ErrInvalidURL
	}
	if !ValidFormat(format) {
		return sqlcgen.BlocklistSubscription{}, ErrInvalidFormat
	}
	sub, err := s.repo.CreateBlocklistSubscription(ctx, sqlcgen.CreateBlocklistSubscriptionParams{
		Url: rawURL, Label: truncate(strings.TrimSpace(label), 200), Format: format,
		Enabled: enabled, CreatedBy: pgconv.UUID(actor),
	})
	if pgconv.IsUniqueViolation(err) {
		return sqlcgen.BlocklistSubscription{}, ErrDuplicate
	}
	return sub, err
}

func truncate(v string, n int) string {
	if r := []rune(v); len(r) > n {
		return string(r[:n])
	}
	return v
}

// List pages the subscriptions.
func (s *Service) List(ctx context.Context, limit, offset int32) ([]sqlcgen.ListBlocklistSubscriptionsRow, int64, error) {
	rows, err := s.repo.ListBlocklistSubscriptions(ctx, sqlcgen.ListBlocklistSubscriptionsParams{ResultLimit: limit, ResultOffset: offset})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountBlocklistSubscriptions(ctx)
	return rows, total, err
}

// Get returns one subscription.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (sqlcgen.BlocklistSubscription, error) {
	sub, err := s.repo.GetBlocklistSubscription(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sub, ErrNotFound
	}
	return sub, err
}

// Update changes the label/enabled flag. Disabling stops future syncs; the
// blocks already applied stay until the subscription is deleted.
func (s *Service) Update(ctx context.Context, id uuid.UUID, label string, enabled bool) (sqlcgen.BlocklistSubscription, error) {
	sub, err := s.repo.UpdateBlocklistSubscription(ctx, sqlcgen.UpdateBlocklistSubscriptionParams{
		ID: id, Label: truncate(strings.TrimSpace(label), 200), Enabled: enabled,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sub, ErrNotFound
	}
	return sub, err
}

func sourceOf(id uuid.UUID) string { return "subscription:" + id.String() }

// Delete removes the subscription and ONLY the blocks it created. The row goes
// first so an in-flight sync cannot re-add blocks for a subscription that no
// longer exists (InsertSubscriptionBlocks checks it still does).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) (removed int, err error) {
	n, err := s.repo.DeleteBlocklistSubscription(ctx, id)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrNotFound
	}
	rows, err := s.repo.DeleteAllSubscriptionBlocksForSource(ctx, sourceOf(id))
	if err != nil {
		return 0, err
	}
	for i, r := range rows {
		if i >= maxRedeliver {
			break
		}
		s.unblocked(ctx, r.Domain, r.CreatedAt)
	}
	return len(rows), nil
}

func (s *Service) unblocked(ctx context.Context, domain string, at time.Time) {
	if s.onUnblocked != nil {
		s.onUnblocked(ctx, domain, at)
	}
}

// SyncResult is what one sync did.
type SyncResult struct {
	Status          string // ok | not_modified | error
	Error           string
	Added, Removed  int
	Entries         int
	IgnoredSeverity int
	Invalid         int
}

// SyncDue syncs every enabled subscription not attempted within SyncInterval.
// It returns how many were attempted. One failing list never stops the others.
func (s *Service) SyncDue(ctx context.Context) (int, error) {
	subs, err := s.repo.ListDueBlocklistSubscriptions(ctx, sqlcgen.ListDueBlocklistSubscriptionsParams{
		Cutoff: s.now().Add(-SyncInterval), ResultLimit: 50,
	})
	if err != nil {
		return 0, err
	}
	for _, sub := range subs {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		s.sync(ctx, sub, uuid.Nil)
	}
	return len(subs), nil
}

// SyncNow syncs one subscription on an admin's request (any enabled state).
func (s *Service) SyncNow(ctx context.Context, id, actor uuid.UUID) (SyncResult, error) {
	sub, err := s.repo.GetBlocklistSubscription(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncResult{}, ErrNotFound
	}
	if err != nil {
		return SyncResult{}, err
	}
	return s.sync(ctx, sub, actor), nil
}

func (s *Service) sync(ctx context.Context, sub sqlcgen.BlocklistSubscription, actor uuid.UUID) SyncResult {
	res := s.run(ctx, sub)
	if err := s.repo.RecordBlocklistSyncResult(ctx, sqlcgen.RecordBlocklistSyncResultParams{
		ID: sub.ID, Status: res.Status, Error: res.Error, Etag: res.etag,
		Added: int32(res.Added), Removed: int32(res.Removed), Entries: int32(res.Entries),
	}); err != nil {
		s.logger.WarnContext(ctx, "recording blocklist sync result failed", "subscription", sub.ID, "error", err)
	}
	s.audit(ctx, sub.ID, actor, res)
	return res.SyncResult
}

type runResult struct {
	SyncResult
	etag string
}

func (s *Service) run(ctx context.Context, sub sqlcgen.BlocklistSubscription) runResult {
	fail := func(msg string) runResult { return runResult{SyncResult: SyncResult{Status: "error", Error: msg}} }
	got, err := s.fetch.Fetch(ctx, sub.Url, sub.Etag)
	if err != nil {
		var se StatusError
		switch {
		case errors.As(err, &se):
			return fail(se.Error())
		case errors.Is(err, ErrBlockedTarget), errors.Is(err, ErrTooLarge), errors.Is(err, ErrTimeout):
			return fail(err.Error())
		default:
			return fail("fetch failed")
		}
	}
	if got.NotModified {
		return runResult{SyncResult: SyncResult{Status: "not_modified"}}
	}
	parsed, err := Parse(sub.Format, got.Body)
	if err != nil {
		return fail("list is not valid " + sub.Format)
	}
	// A list that yields nothing usable must never be read as "remove
	// everything": a truncated download, an HTML error page served with 200 or a
	// wrong format would otherwise lift every block the subscription owns.
	if len(parsed.Block) == 0 {
		return fail("list contained no usable domain entries; nothing changed")
	}
	allow, err := s.repo.ListAllBlocklistAllowDomains(ctx)
	if err != nil {
		return fail("internal error")
	}
	exempt := make(map[string]bool, len(allow))
	for _, d := range allow {
		exempt[d] = true
	}
	desired := make([]string, 0, len(parsed.Block))
	for _, d := range parsed.Block {
		if !exempt[d] {
			desired = append(desired, d)
		}
	}
	out := runResult{SyncResult: SyncResult{
		Status: "ok", Entries: len(parsed.Block), IgnoredSeverity: parsed.IgnoredSeverity, Invalid: parsed.Invalid,
	}, etag: got.ETag}
	reason := "Blocklist subscription"
	if l := strings.TrimSpace(sub.Label); l != "" {
		reason += ": " + truncate(l, 100)
	}
	for i := 0; i < len(desired); i += insertChunk {
		end := min(i+insertChunk, len(desired))
		added, err := s.repo.InsertSubscriptionBlocks(ctx, sqlcgen.InsertSubscriptionBlocksParams{
			Reason: reason, SubscriptionID: sub.ID, Domains: desired[i:end],
		})
		if err != nil {
			return fail("internal error")
		}
		out.Added += len(added)
	}
	// Removal runs AFTER the inserts, and only on success of all of them: a
	// half-applied list leaves extra blocks, never missing ones.
	removed, err := s.repo.DeleteSubscriptionBlocksNotIn(ctx, sqlcgen.DeleteSubscriptionBlocksNotInParams{
		Source: sourceOf(sub.ID), Keep: desired,
	})
	if err != nil {
		return fail("internal error")
	}
	out.Removed = len(removed)
	for i, r := range removed {
		if i >= maxRedeliver {
			break
		}
		s.unblocked(ctx, r.Domain, r.CreatedAt)
	}
	return out
}

func (s *Service) audit(ctx context.Context, id, actor uuid.UUID, r runResult) {
	if s.auditor == nil {
		return
	}
	ev := audit.Event{
		Action:       observability.ActionBlocklistSync,
		Result:       "success",
		ResourceType: "blocklist_subscription",
		ResourceID:   id.String(),
		Metadata: []audit.MetadataField{
			{Key: "outcome", Value: r.Status},
			{Key: "added", Value: fmt.Sprint(r.Added)},
			{Key: "removed", Value: fmt.Sprint(r.Removed)},
			{Key: "count", Value: fmt.Sprint(r.Entries)},
		},
	}
	if r.Status == "error" {
		ev.Result = "failure"
		ev.Reason = r.Error
	}
	if actor != uuid.Nil {
		ev.Actor = audit.ActorSnapshot{ID: actor.String(), Kind: "user"}
	} else {
		ev.Actor = audit.ActorSnapshot{Kind: "system"}
	}
	if err := s.auditor.Record(ctx, ev); err != nil {
		s.logger.WarnContext(ctx, "blocklist sync audit failed", "error", err)
	}
}

// AddAllow exempts a domain from every subscription and drops any
// subscription-owned block on it now. A manual block is untouched.
func (s *Service) AddAllow(ctx context.Context, actor uuid.UUID, domain string) error {
	d, err := instancemod.NormalizeDomain(domain)
	if err != nil {
		return err
	}
	if _, err := s.repo.AddBlocklistAllow(ctx, sqlcgen.AddBlocklistAllowParams{Domain: d, AddedBy: pgconv.UUID(actor)}); err != nil {
		return err
	}
	rows, err := s.repo.DeleteSubscriptionBlocksForDomain(ctx, d)
	if err != nil {
		return err
	}
	for _, r := range rows {
		s.unblocked(ctx, r.Domain, r.CreatedAt)
	}
	return nil
}

// RemoveAllow lifts an exemption (idempotent); the next sync re-applies lists.
func (s *Service) RemoveAllow(ctx context.Context, domain string) error {
	d, err := instancemod.NormalizeDomain(domain)
	if err != nil {
		return err
	}
	_, err = s.repo.RemoveBlocklistAllow(ctx, d)
	return err
}

// ListAllow pages the allowlist.
func (s *Service) ListAllow(ctx context.Context, limit, offset int32) ([]sqlcgen.BlocklistAllowlist, int64, error) {
	rows, err := s.repo.ListBlocklistAllow(ctx, sqlcgen.ListBlocklistAllowParams{ResultLimit: limit, ResultOffset: offset})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountBlocklistAllow(ctx)
	return rows, total, err
}
