package video

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// RetainedRecoveryDeclined is the terminal eligibility refusal stored on a
// recovery job. The operator retry command recognizes only this outcome.
const RetainedRecoveryDeclined = "the retained upload is no longer eligible for recovery"

var ErrRecoveryIneligible = errors.New("video: retained source is no longer eligible")

type retainedQueries interface {
	GetCurrentRetainedScanRecovery(context.Context, uuid.UUID) (sqlcgen.CurrentRetainedScanRecovery, error)
	SetRetainedScanVideoState(context.Context, sqlcgen.SetRetainedScanVideoStateParams) (sqlcgen.SetRetainedScanVideoStateRow, error)
	DetachInfectedRetainedFile(context.Context, uuid.UUID) error
	IsRetainedScanAccepted(context.Context, sqlcgen.IsRetainedScanAcceptedParams) (bool, error)
}

type retainedSource struct {
	Repository
	queries    retainedQueries
	invalidate func()
	job        uuid.UUID
}

// Every outcome, including publication and quarantine, must still match the
// creator, visibility, moderation state and attached source at the state write.
func (r *retainedSource) SetVideoState(ctx context.Context, p sqlcgen.SetVideoStateParams) (sqlcgen.Video, error) {
	// This guarded write replaces SetVideoState on the cached repository.
	defer r.invalidate()
	row, err := r.queries.SetRetainedScanVideoState(ctx, sqlcgen.SetRetainedScanVideoStateParams{
		VideoID: p.ID, JobID: r.job, NewState: p.State,
	})
	return sqlcgen.Video(row), err
}

type verifiedScanner interface {
	ScanVerified(context.Context, string, int64, string) (bool, error)
}

type retainedScanner struct {
	verifiedScanner
	source sqlcgen.CurrentRetainedScanRecovery
}

func (s retainedScanner) Scan(ctx context.Context, key string) (bool, error) {
	if key != s.source.StorageKey {
		return false, ErrRecoveryIneligible
	}
	return s.ScanVerified(ctx, key, s.source.SizeBytes, s.source.Sha256)
}

// ProcessRetained runs normal ingestion on an already attached original only
// after the operator has enrolled the exact failed upload. A regular finalize
// has no such enrollment and reaches Process through AttachOriginal instead.
func (s *Service) ProcessRetained(ctx context.Context, jobID, videoID uuid.UUID) error {
	q := s.retained
	scanner, verifies := s.scanner.(verifiedScanner)
	if q == nil || !verifies || s.scanMode != ScanModeFailClosed || s.prober == nil {
		return ErrRecoveryIneligible
	}
	source, err := q.GetCurrentRetainedScanRecovery(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		// A previous worker can commit publication just before losing its lease.
		// That successful attempt must not be reported as a new failed scan.
		accepted, readErr := q.IsRetainedScanAccepted(ctx, sqlcgen.IsRetainedScanAcceptedParams{JobID: jobID, VideoID: videoID})
		if errors.Is(readErr, pgx.ErrNoRows) {
			return ErrRecoveryIneligible
		}
		if readErr != nil {
			return readErr
		}
		if accepted {
			return nil
		}
	}
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && source.VideoID != videoID) {
		return ErrRecoveryIneligible
	}
	if err != nil {
		return err
	}
	guard := &retainedSource{Repository: s.repo, queries: q, invalidate: s.InvalidateAnonymous, job: jobID}
	operation := *s
	operation.repo = guard
	operation.scanner = retainedScanner{verifiedScanner: scanner, source: source}
	result, err := operation.process(ctx, videoID, source.StorageKey, guard)
	if err == nil && result.State == "failed" {
		return ErrRecoveryIneligible
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRecoveryIneligible
	}
	return err
}
