package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/retry"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"

	"github.com/vidra/vidra-core/internal/lease"
)

// Export queue tuning. Mirrors the transcode queue.
const (
	// exportMaxAttempts is how many times a job is tried before it is
	// dead-lettered (state 'failed').
	exportMaxAttempts = 5
	// exportBaseBackoff is the first retry delay; it doubles each attempt.
	exportBaseBackoff = time.Minute
	// exportMaxBackoff caps the exponential backoff.
	exportMaxBackoff = time.Hour
	// exportMaxLastErrorLen bounds the stored last_error string.
	exportMaxLastErrorLen = 500
	// ExportTTL is how long a finished archive stays downloadable before the
	// sweeper collects it. The static default; WithExportTTLFunc overlays it at
	// runtime (user_export_expiration_hours, config-parity W8).
	ExportTTL = 7 * 24 * time.Hour
)

// effectiveExportTTL resolves the current archive retention: the live overlay
// value when a provider is wired (0 = never expires), else the static ExportTTL.
// A negative provider value is treated as the static default (defensive; the
// settings validator never emits one).
func (s *Service) effectiveExportTTL() time.Duration {
	if s.exportTTLFn != nil {
		if d := s.exportTTLFn(); d >= 0 {
			return d
		}
	}
	return ExportTTL
}

// Export job states persisted on account_exports.
const (
	ExportPending = "pending"
	ExportRunning = "running"
	ExportDone    = "done"
	ExportFailed  = "failed"
)

// exportKey is the blob key an export archive is stored at. Deterministic and
// namespaced per user under the exports/ top-level kind dir (storage-layout.md).
func exportKey(userID, exportID uuid.UUID) string {
	return "exports/accounts/" + userID.String() + "/" + exportID.String() + ".json"
}

// RequestExport enqueues a new export job for the user. Single active export
// per user: while one is pending/running the request is refused with
// ErrExportActive. Finished/failed rows (and their archive blobs) are replaced
// by the new request.
func (s *Service) RequestExport(ctx context.Context, userID uuid.UUID) (sqlcgen.AccountExport, error) {
	// Replace any previous finished/failed export: the row AND its blob.
	oldKeys, err := s.repo.DeleteInactiveAccountExportsByUser(ctx, userID)
	if err != nil {
		return sqlcgen.AccountExport{}, fmt.Errorf("account: clear old exports: %w", err)
	}
	for _, key := range oldKeys {
		if key != "" {
			s.blobDelete(ctx, key)
		}
	}
	row, err := s.repo.CreateAccountExport(ctx, userID)
	if err != nil {
		// ON CONFLICT DO NOTHING returns no row while a pending/running export
		// exists (partial unique index) — the single-active-export refusal.
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlcgen.AccountExport{}, ErrExportActive
		}
		return sqlcgen.AccountExport{}, fmt.Errorf("account: create export: %w", err)
	}
	return row, nil
}

// LatestExport returns the user's most recent export row. ErrNotFound when the
// user never requested one (or the sweeper collected it).
func (s *Service) LatestExport(ctx context.Context, userID uuid.UUID) (sqlcgen.AccountExport, error) {
	row, err := s.repo.GetLatestAccountExportByUser(ctx, userID)
	if err != nil {
		return sqlcgen.AccountExport{}, ErrNotFound
	}
	return row, nil
}

// OpenExport opens the user's finished archive for download. ErrNotFound when
// no export exists, ErrExportNotReady while it is pending/running or after it
// failed, ErrExportExpired past its expiry (the sweeper may not have collected
// the row yet).
func (s *Service) OpenExport(ctx context.Context, userID uuid.UUID) (io.ReadCloser, sqlcgen.AccountExport, error) {
	row, err := s.LatestExport(ctx, userID)
	if err != nil {
		return nil, sqlcgen.AccountExport{}, err
	}
	if row.State != ExportDone || row.StorageKey == "" {
		return nil, sqlcgen.AccountExport{}, ErrExportNotReady
	}
	if row.ExpiresAt.Valid && !s.now().Before(row.ExpiresAt.Time) {
		return nil, sqlcgen.AccountExport{}, ErrExportExpired
	}
	if s.blobs == nil {
		return nil, sqlcgen.AccountExport{}, ErrExportNotReady
	}
	rc, err := s.blobs.Open(ctx, row.StorageKey)
	if err != nil {
		return nil, sqlcgen.AccountExport{}, fmt.Errorf("account: open export archive: %w", err)
	}
	return rc, row, nil
}

// DrainExports claims up to limit due export jobs and runs each: the user's
// archive is assembled, written to blob storage, and the job marked done with
// the configured expiry (default 7 days; 0 = never expires) — or rescheduled
// with exponential backoff / dead-lettered
// after exportMaxAttempts. Returns the number completed. Only the claim-query
// error is returned — per-job failures are persisted in the queue. Intended to
// be called on a ticker by a single worker.
func (s *Service) DrainExports(ctx context.Context, limit int) (int, error) {
	rows, err := s.repo.ClaimDueAccountExports(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	done := 0
	for _, row := range rows {
		stopLease := lease.Keep(ctx, lease.DefaultInterval, "account_export", func(c context.Context) error {
			return s.repo.RenewAccountExportLease(c, row.ID)
		})
		err := s.runExport(ctx, row)
		stopLease()
		if err != nil {
			s.recordExportFailure(ctx, row, err)
			continue
		}
		done++
	}
	return done, nil
}

// runExport generates and stores one archive, then completes the job.
func (s *Service) runExport(ctx context.Context, row sqlcgen.ClaimDueAccountExportsRow) error {
	if s.blobs == nil {
		return errors.New("account: no storage backend configured")
	}
	user, err := s.repo.GetUserByID(ctx, row.UserID)
	if err != nil {
		return fmt.Errorf("account: export user lookup: %w", err)
	}
	archive, err := s.buildArchive(ctx, user)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(archive, "", "  ")
	if err != nil {
		return fmt.Errorf("account: marshal archive: %w", err)
	}
	key := exportKey(row.UserID, row.ID)
	if _, err := s.blobs.Put(ctx, key, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("account: store archive: %w", err)
	}
	// Retention is resolved at completion time (runtime overlay); a TTL of 0
	// stamps no expiry — the archive stays downloadable until replaced.
	var expiresAt pgtype.Timestamptz
	if ttl := s.effectiveExportTTL(); ttl > 0 {
		expiresAt = pgconv.Time(s.now().UTC().Add(ttl))
	}
	return s.repo.CompleteAccountExport(ctx, sqlcgen.CompleteAccountExportParams{
		ID:         row.ID,
		StorageKey: key,
		ExpiresAt:  expiresAt,
	})
}

// recordExportFailure reschedules with backoff, or dead-letters after the cap.
func (s *Service) recordExportFailure(ctx context.Context, row sqlcgen.ClaimDueAccountExportsRow, cause error) {
	attempts := int(row.Attempts) + 1
	msg := cause.Error()
	if len(msg) > exportMaxLastErrorLen {
		msg = msg[:exportMaxLastErrorLen]
	}
	if attempts >= exportMaxAttempts {
		_ = s.repo.FailAccountExport(ctx, sqlcgen.FailAccountExportParams{ID: row.ID, LastError: msg})
		return
	}
	_ = s.repo.RescheduleAccountExport(ctx, sqlcgen.RescheduleAccountExportParams{
		ID:            row.ID,
		NextAttemptAt: s.now().UTC().Add(exportBackoff(attempts)),
		LastError:     msg,
	})
}

// exportBackoff is exportBaseBackoff * 2^(attempts-1), capped.
func exportBackoff(attempts int) time.Duration {
	return retry.Backoff(attempts, exportBaseBackoff, exportMaxBackoff)
}

// SweepExpiredExports deletes up to limit finished archives past their stamped
// expiry (rows with no expiry are never collected): the blob first
// (best-effort), then the row. Returns the number of
// rows removed. Intended to run on the same worker ticker as DrainExports.
func (s *Service) SweepExpiredExports(ctx context.Context, limit int) (int, error) {
	rows, err := s.repo.ListExpiredAccountExports(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, row := range rows {
		if row.StorageKey != "" {
			s.blobDelete(ctx, row.StorageKey)
		}
		if err := s.repo.DeleteAccountExport(ctx, row.ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
