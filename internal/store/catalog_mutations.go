package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var errCatalogMutation = errors.New("catalog mutation input is invalid")

// AcknowledgeCatalogWork requires completion independently verified by the caller,
// never an event receipt. The entire retained pending proof must match.
func (s *Store) AcknowledgeCatalogWork(ctx context.Context, w catalog.SourceWork, completed catalog.FullProof) (bool, error) {
	if !validCatalogMutationWork(w) || w.Evidence.Pending == nil || *w.Evidence.Pending != completed {
		return false, errCatalogMutation
	}
	return s.catalogMutation(ctx, func(q *sqlcgen.Queries) (int64, error) {
		return q.AcknowledgeCatalogFull(ctx, sqlcgen.AcknowledgeCatalogFullParams{
			Epoch: completed.Identity.Epoch, Generation: int64(completed.Identity.Generation),
			PageCount: completed.Manifest.PageCount, DocumentCount: completed.Manifest.DocumentCount,
			MembershipSha256: completed.Manifest.MembershipSHA256, PagesSha256: completed.Manifest.PagesSHA256,
			ExpectedForceFull: w.ForceFull, ExpectedForceFullAfterGeneration: w.ForceFullAfterGeneration,
			ExpectedErrorID: catalogUUID(w.LastErrorID),
		})
	})
}

func (s *Store) RecordCatalogDeliveryFailure(ctx context.Context, w catalog.SourceWork, code string) (bool, error) {
	if !validCatalogMutationWork(w) || w.Evidence.Pending == nil || !catalogFailureCode(code) {
		return false, errCatalogMutation
	}
	p := w.Evidence.Pending
	return s.catalogMutation(ctx, func(q *sqlcgen.Queries) (int64, error) {
		return q.RecordCatalogDeliveryError(ctx, sqlcgen.RecordCatalogDeliveryErrorParams{
			ErrorCode: code, Epoch: p.Identity.Epoch, Generation: int64(p.Identity.Generation),
			PageCount: p.Manifest.PageCount, DocumentCount: p.Manifest.DocumentCount,
			MembershipSha256: p.Manifest.MembershipSHA256, PagesSha256: p.Manifest.PagesSHA256,
			ExpectedForceFull: w.ForceFull, ExpectedForceFullAfterGeneration: w.ForceFullAfterGeneration,
			ExpectedErrorID: catalogUUID(w.LastErrorID),
		})
	})
}

func (s *Store) RecordCatalogCaptureFailure(ctx context.Context, w catalog.SourceWork, code string) (bool, error) {
	if !validCatalogMutationWork(w) || w.Evidence.Pending != nil || !catalogFailureCode(code) {
		return false, errCatalogMutation
	}
	return s.catalogMutation(ctx, func(q *sqlcgen.Queries) (int64, error) {
		return q.RecordCatalogCaptureError(ctx, sqlcgen.RecordCatalogCaptureErrorParams{
			ErrorCode: code, Epoch: w.Evidence.Epoch, ExpectedAllocatedGeneration: w.Evidence.AllocatedGeneration,
			ExpectedForceFull: w.ForceFull, ExpectedForceFullAfterGeneration: w.ForceFullAfterGeneration,
			ExpectedErrorID: catalogUUID(w.LastErrorID),
		})
	})
}

// Success is returned only after COMMIT/unlock. An uncertain error returns false
// even if committed: callers reload durable state rather than infer rollback.
func (s *Store) catalogMutation(ctx context.Context, change func(*sqlcgen.Queries) (int64, error)) (bool, error) {
	var changed int64
	err := s.WithCatalogControl(ctx, func(tx pgx.Tx) error {
		var err error
		changed, err = change(sqlcgen.New(tx))
		return err
	})
	if err != nil {
		return false, err
	}
	return changed == 1, nil
}

func validCatalogMutationWork(w catalog.SourceWork) bool {
	e := w.Evidence
	if e.Epoch == uuid.Nil || e.AllocatedGeneration < 0 || w.ForceFullAfterGeneration < 0 || w.ForceFullAfterGeneration > e.AllocatedGeneration ||
		(w.LastErrorID == nil) != (w.LastErrorCode == "") || w.LastErrorID != nil && (*w.LastErrorID == uuid.Nil || !catalogFailureCode(w.LastErrorCode)) {
		return false
	}
	for _, p := range []*catalog.FullProof{e.Verified, e.Pending} {
		if p != nil && (p.Identity.Validate() != nil || p.Identity.Epoch != e.Epoch || int64(p.Identity.Generation) > e.AllocatedGeneration ||
			p.Manifest.Validate() != nil || p.Manifest.PageCount > catalog.MaxPages || p.Manifest.DocumentCount > catalog.MaxDocuments) {
			return false
		}
	}
	return e.Pending == nil || int64(e.Pending.Identity.Generation) == e.AllocatedGeneration &&
		(e.Verified == nil || e.Pending.Identity.Generation > e.Verified.Identity.Generation)
}

func catalogFailureCode(code string) bool {
	switch code {
	case "quota_exceeded", "scalar_limit_exceeded", "capture_failed", "delivery_failed", "verification_failed", "epoch_conflict":
		return true
	}
	return false
}
