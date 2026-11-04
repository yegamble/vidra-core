//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCatalogCaptureWaitsBeforeRepeatableReadSnapshotAndDiscardsLostLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	observer, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	worker, err := New(ctx, dsn(t), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	var pid int32
	if err := worker.Pool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	lock, err := observer.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_lock($1)`, catalogAuthorityKey); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = lock.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, catalogAuthorityKey) }()
	id := uuid.New()
	defer func() {
		_, _ = observer.Pool.Exec(context.Background(), `DELETE FROM search_catalog_dirty WHERE id=$1`, id)
	}()
	done := make(chan error, 1)
	go func() {
		done <- worker.WithCatalogCapture(ctx, func(tx pgx.Tx) error {
			var visible bool
			var isolation string
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM search_catalog_dirty WHERE id=$1), current_setting('transaction_isolation')`, id).Scan(&visible, &isolation); err != nil {
				return err
			}
			if !visible || isolation != "repeatable read" {
				return errors.New("capture snapshot began before authority lock")
			}
			return nil
		})
	}()
	// Observe a real lock waiter, rather than assuming goroutine scheduling.
	for {
		var waiting bool
		if err := observer.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("capture did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if _, err := lock.Exec(ctx, `INSERT INTO search_catalog_dirty(id,scope) VALUES ($1,'local')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_unlock($1)`, catalogAuthorityKey); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("capture aborted")
	if err := worker.WithCatalogControl(ctx, func(tx pgx.Tx) error {
		var isolation string
		if err := tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil || isolation != "read committed" {
			return errors.New("control needs read committed")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM search_catalog_dirty WHERE id=$1`, id); err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("lost callback error: %v", err)
	}
	var retained bool
	if err := observer.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM search_catalog_dirty WHERE id=$1)`, id).Scan(&retained); err != nil || !retained {
		t.Fatalf("aborted authority write committed: %v/%v", retained, err)
	}
	// Fault injection: losing the session lock must not return this backend to
	// the pool or permit the caller to treat its committed result as safe.
	if err := worker.WithCatalogCapture(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT pg_advisory_unlock($1)`, catalogAuthorityKey)
		return err
	}); err == nil {
		t.Fatal("lost session lock reported success")
	}
	var nextPID int32
	if err := worker.Pool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&nextPID); err != nil || nextPID == pid {
		t.Fatalf("uncertain backend reused: %d/%d %v", pid, nextPID, err)
	}
}
