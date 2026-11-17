package federation

import (
	"context"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The queue between an accepted Follow and the read of the followed actor's
// outbox (CrawlOutbox). The read is network work against a remote server, so
// it does not happen in the inbox request that carried the Accept: the Accept
// queues it, durably, and the federation worker runs it with retry.

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

const (
	// outboxCrawlCooldown is how long an actor's outbox is left alone after a
	// read: a second follower of the same channel, or a follow, unfollow,
	// follow, queues nothing inside it. New uploads arrive by delivery.
	outboxCrawlCooldown = time.Hour
	// outboxCrawlLease is how long a claimed read is another worker's to
	// leave alone, and outboxCrawlTimeout bounds one read well inside it.
	outboxCrawlLease   = 10 * time.Minute
	outboxCrawlTimeout = 2 * time.Minute
	// outboxCrawlMaxAttempts is how many times one queued read is tried.
	outboxCrawlMaxAttempts = 3
	// maxCrawlErrorLen bounds the reason kept with a failed read.
	maxCrawlErrorLen = 500
)

// queueOutboxCrawl queues a read of the outbox of an actor that has just
// accepted a Follow from here. Best-effort: the follow is accepted whatever
// happens to the queue, so a failure is logged and not returned.
func (s *Service) queueOutboxCrawl(ctx context.Context, actorURL string) {
	if s.crawls == nil {
		return
	}
	err := s.crawls.EnqueueOutboxCrawl(ctx, sqlcgen.EnqueueOutboxCrawlParams{
		ActorUrl: actorURL, CooldownSeconds: int32(outboxCrawlCooldown / time.Second),
	})
	if err != nil && s.logger != nil {
		s.logger.Warn("federation: could not queue the outbox read for a newly followed actor", "actor", actorURL, "error", err)
	}
}

// DrainOutboxCrawls claims up to limit due reads and runs each. It reports how
// many are over — read, or given up on — which is what lets a worker's drain
// terminate: a read that failed and waits for its retry is not counted.
func (s *Service) DrainOutboxCrawls(ctx context.Context, limit int) (int, error) {
	if s.crawls == nil {
		return 0, nil
	}
	rows, err := s.crawls.ClaimDueOutboxCrawls(ctx, sqlcgen.ClaimDueOutboxCrawlsParams{
		BatchSize: int32(limit), LeaseSeconds: int32(outboxCrawlLease / time.Second),
	})
	if err != nil {
		return 0, err
	}
	over := 0
	for _, row := range rows {
		readCtx, cancel := context.WithTimeout(ctx, outboxCrawlTimeout)
		stored, err := s.CrawlOutbox(readCtx, row.ActorUrl)
		cancel()
		settle := sqlcgen.SettleOutboxCrawlParams{ActorUrl: row.ActorUrl, State: "done", NextAttemptAt: time.Now()}
		if err != nil {
			settle.State, settle.LastError = "failed", truncate(err.Error(), maxCrawlErrorLen)
			if attempt := int(row.Attempts) + 1; attempt < outboxCrawlMaxAttempts {
				settle.State, settle.NextAttemptAt = "pending", time.Now().Add(deliveryBackoff(attempt))
			}
		}
		if err := s.crawls.SettleOutboxCrawl(ctx, settle); err != nil {
			return over, err
		}
		if settle.State != "pending" {
			over++
		}
		if s.logger != nil {
			s.logger.Info("federation: read a followed actor's outbox", "actor", row.ActorUrl, "stored", stored, "state", settle.State, "error", settle.LastError)
		}
	}
	return over, nil
}
