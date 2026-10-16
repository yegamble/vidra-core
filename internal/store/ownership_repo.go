package store

import (
	"context"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// OwnershipRepo is the video service's ownership-change repository: the sqlc
// queries, except that AcceptVideoOwnershipChange runs under the video's row
// lock.
type OwnershipRepo struct {
	*sqlcgen.Queries
	s *Store
}

// OwnershipRepository returns the repository to hand video.WithOwnershipRepository.
func (s *Store) OwnershipRepository() *OwnershipRepo {
	return &OwnershipRepo{Queries: s.Queries(), s: s}
}

// AcceptVideoOwnershipChange locks the video row first (LockOwnershipChangeVideo)
// and runs the accept as a SEPARATE statement in the same transaction. Opening
// an upload or replacement session and swapping in a replacement take the same
// row lock, so under READ COMMITTED the accept's own snapshot is taken only once
// any of them still in flight has committed: a session opened first is seen by
// the accept's "no media work in flight" check, and one opened after sees the
// video's new channel and is refused. A single statement could not give that
// guarantee — its snapshot would predate the lock.
func (r *OwnershipRepo) AcceptVideoOwnershipChange(ctx context.Context, arg sqlcgen.AcceptVideoOwnershipChangeParams) (sqlcgen.AcceptVideoOwnershipChangeRow, error) {
	tx, err := r.s.Pool.Begin(ctx)
	if err != nil {
		return sqlcgen.AcceptVideoOwnershipChangeRow{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.Queries.WithTx(tx)
	// pgx.ErrNoRows here means no such request (any more): the caller reads it
	// exactly like an accept that matched nothing.
	if _, err := q.LockOwnershipChangeVideo(ctx, arg.ID); err != nil {
		return sqlcgen.AcceptVideoOwnershipChangeRow{}, err
	}
	row, err := q.AcceptVideoOwnershipChange(ctx, arg)
	if err != nil {
		return sqlcgen.AcceptVideoOwnershipChangeRow{}, err
	}
	return row, tx.Commit(ctx)
}
