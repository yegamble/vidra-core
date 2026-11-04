package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Distinct from the receiver's 0x7669647261636174, including shared-DB installs.
const catalogAuthorityKey int64 = 0x7669647261737263

// WithCatalogCapture acquires authority BEFORE opening its repeatable-read
// snapshot. Acquiring the lock by SELECT inside RR can freeze a pre-wait view.
// The callback must not commit/rollback the transaction or perform network I/O.
func (s *Store) WithCatalogCapture(ctx context.Context, fn func(pgx.Tx) error) error {
	return s.withCatalogAuthority(ctx, pgx.RepeatableRead, fn)
}

// WithCatalogControl serializes brief bootstrap/control/ACK transactions with
// capture. No connection, transaction or session lock may span an HTTP request.
func (s *Store) WithCatalogControl(ctx context.Context, fn func(pgx.Tx) error) error {
	return s.withCatalogAuthority(ctx, pgx.ReadCommitted, fn)
}

func (s *Store) withCatalogAuthority(ctx context.Context, isolation pgx.TxIsoLevel, fn func(pgx.Tx) error) (err error) {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	discarded := false
	discard := func() {
		discarded = true
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Hijack().Close(closeCtx)
	}
	defer func() {
		if !discarded {
			conn.Release()
		}
	}()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, catalogAuthorityKey); err != nil {
		// A lost response can leave the lock acquired. Never reuse this backend.
		discard()
		return err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		unlockErr := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock($1)`, catalogAuthorityKey).Scan(&unlocked)
		if unlockErr != nil || !unlocked {
			discard()
			if unlockErr == nil {
				unlockErr = errors.New("session lock no longer held")
			}
			err = errors.Join(err, fmt.Errorf("catalog authority unlock uncertain: %w", unlockErr))
		}
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
	if err != nil {
		return err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
