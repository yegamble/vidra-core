// Package mediahash backfills the content hash of stored media that predates
// the hash-on-Put write paths (phase-2 storage, work item 2).
//
// Every video_files row written since migration 0106 carries the SHA-256 its
// upload stream produced. Rows written before it carry the empty (not-computed)
// state, and so do rows whose write path genuinely could not produce a digest —
// a PeerTube import in reference mode, which never copies the bytes. This
// package reads those objects back, once, and records what it finds, so the
// integrity-verified storage migration that follows has something to compare
// against for the whole library rather than only for what was uploaded after the
// upgrade.
//
// Deliberately out of scope: HLS segments and playlists. They have no
// video_files rows — a ladder rung is one video_renditions row covering a whole
// directory of segments — so there is no per-object place to record a digest and
// nothing that would read one back. Storage migration verifies that tree object
// by object while it copies it. This backfill therefore scans video_files only,
// and a store whose HLS objects are corrupt will still look fully hashed here.
package mediahash

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// SentinelMissing is recorded in video_files.sha256 when the backfill went to
// read an object and the store said it is not there. It is deliberately not a
// hex digest, so it can never be mistaken for one, and it is deliberately not
// left empty: a dangling reference that stayed empty would be re-read on every
// tick forever and would keep the backfill from ever reporting completion.
//
// The row is a known-bad reference from that point on. A later phase-2 wave's
// verify-blobs check consumes this value; nothing else may treat it as a hash.
const SentinelMissing = "missing"

// Repository is the data access the backfill needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	ListUnhashedVideoFiles(ctx context.Context, limit int32) ([]sqlcgen.ListUnhashedVideoFilesRow, error)
	SetVideoFileSHA256(ctx context.Context, arg sqlcgen.SetVideoFileSHA256Params) error
}

// Service hashes not-yet-hashed video_files rows against a storage backend.
type Service struct {
	repo   Repository
	blobs  storage.Backend
	logger *slog.Logger
}

// NewService builds the backfill service. A nil logger falls back to the
// default one.
func NewService(repo Repository, blobs storage.Backend, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, blobs: blobs, logger: logger}
}

// Result is one pass's outcome. Scanned == 0 means the backfill has drained:
// every video_files row now carries either a digest or the missing sentinel.
type Result struct {
	// Scanned is how many unhashed rows the pass picked up.
	Scanned int
	// Hashed is how many of them now carry a digest.
	Hashed int
	// Missing is how many had no object behind them and now carry the sentinel.
	Missing int
	// Failed is how many could not be read for some other reason. They keep the
	// empty state and are retried on the next pass.
	Failed int
}

// BackfillOnce hashes up to batch not-yet-hashed rows, oldest first, and
// returns what it did. It is the unit the worker ticks.
//
// A per-row read failure that is not "object not found" — a timeout, a
// transient object-store 5xx, a permission problem — leaves the row in the
// empty state and is counted in Failed rather than aborting the pass: the next
// tick picks it up again, and one unreadable object must not stall the rest of
// the library. Only a database error, which makes the whole pass meaningless,
// is returned.
//
// Callers must run this on ONE instance at a time (the worker is leader-gated).
// It claims nothing, so two concurrent passes would read the same rows and hash
// the same objects twice; the writes would still be correct, just wasted.
func (s *Service) BackfillOnce(ctx context.Context, batch int32) (Result, error) {
	var res Result
	if s.blobs == nil {
		return res, errors.New("mediahash: no storage backend configured")
	}
	rows, err := s.repo.ListUnhashedVideoFiles(ctx, batch)
	if err != nil {
		return res, err
	}
	res.Scanned = len(rows)
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		sum, err := s.hashObject(ctx, row.StorageKey)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			// A row pointing at an object the store does not have. Recording the
			// sentinel is what turns that from an error retried forever into a
			// fact a later consistency check can act on.
			s.logger.WarnContext(ctx, "media hash backfill: object missing for stored file",
				"video_file_id", row.ID.String(), "storage_key", row.StorageKey)
			if uerr := s.repo.SetVideoFileSHA256(ctx, sqlcgen.SetVideoFileSHA256Params{
				ID: row.ID, Sha256: SentinelMissing,
			}); uerr != nil {
				return res, uerr
			}
			res.Missing++
		case err != nil:
			s.logger.WarnContext(ctx, "media hash backfill: could not read stored file",
				"video_file_id", row.ID.String(), "error", err.Error())
			res.Failed++
		default:
			if uerr := s.repo.SetVideoFileSHA256(ctx, sqlcgen.SetVideoFileSHA256Params{
				ID: row.ID, Sha256: sum,
			}); uerr != nil {
				return res, uerr
			}
			res.Hashed++
		}
	}
	return res, nil
}

// hashObject streams the object at key through SHA-256 and returns the
// lowercase hex digest. It never buffers the object: these are whole video
// originals, and the point of hashing on Put in the first place is that nothing
// has to hold one in memory.
func (s *Service) hashObject(ctx context.Context, key string) (string, error) {
	rc, err := s.blobs.Open(ctx, key)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
