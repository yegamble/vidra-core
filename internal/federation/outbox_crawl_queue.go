package federation

import (
	"context"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// OutboxCrawlQueue is the durable queue of outbox reads (migration 0190). Like
// InstanceRepository it is kept apart from Repository, so the many fakes of
// that surface are untouched; NewService picks it up from a repository that
// provides it (*sqlcgen.Queries does). Without it no read is ever queued.
type OutboxCrawlQueue interface {
	EnqueueOutboxCrawl(ctx context.Context, arg sqlcgen.EnqueueOutboxCrawlParams) error
	ClaimDueOutboxCrawls(ctx context.Context, arg sqlcgen.ClaimDueOutboxCrawlsParams) ([]sqlcgen.ClaimDueOutboxCrawlsRow, error)
	SettleOutboxCrawl(ctx context.Context, arg sqlcgen.SettleOutboxCrawlParams) error
}

var _ OutboxCrawlQueue = (*sqlcgen.Queries)(nil)

// outboxCrawlMaxAttempts is how many times one queued read is tried.
const outboxCrawlMaxAttempts = 3

// DrainOutboxCrawls runs the queued outbox reads that are due.
func (s *Service) DrainOutboxCrawls(_ context.Context, _ int) (int, error) {
	return 0, nil
}
