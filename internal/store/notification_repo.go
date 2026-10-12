package store

import (
	"context"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// NotificationRepo is the notification service's repository: the sqlc queries,
// except that NotifyCommentMentions is serialised per comment.
type NotificationRepo struct {
	*sqlcgen.Queries
	s *Store
}

// NotificationRepository returns the repository to hand notification.NewService.
func (s *Store) NotificationRepository() *NotificationRepo {
	return &NotificationRepo{Queries: s.Queries(), s: s}
}

// NotifyCommentMentions runs the capped fan-out under a per-comment advisory
// lock. The "at most 10 per comment" cap counts existing rows from the
// statement's own READ COMMITTED snapshot, so two concurrent fan-outs (e.g.
// back-to-back edits) would each see none of the other's rows and together
// exceed it. Taking the lock in its own statement first means the insert, a
// SEPARATE statement, gets a fresh snapshot that includes whatever the previous
// holder committed.
func (r *NotificationRepo) NotifyCommentMentions(ctx context.Context, arg sqlcgen.NotifyCommentMentionsParams) (int64, error) {
	tx, err := r.s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, arg.CommentID.String()); err != nil {
		return 0, err
	}
	n, err := r.Queries.WithTx(tx).NotifyCommentMentions(ctx, arg)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}
