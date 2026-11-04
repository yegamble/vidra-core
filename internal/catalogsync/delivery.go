// Package catalogsync coordinates immutable catalog observations. It grants no
// read readiness from transport receipts or volatile delivery progress.
package catalogsync

import (
	"context"
	"errors"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/searchevents"
)

var (
	ErrCompletionPending = errors.New("catalog completed manifest is not yet verified")
	ErrReceiptRejected   = errors.New("catalog frozen event was rejected")
)

type Source interface {
	// LoadCatalogPage must finish QueryRow/Scan and release all DB resources
	// before returning owned bytes. No source lock/transaction may cross HTTP.
	LoadCatalogPage(context.Context, catalog.Identity, int32) ([]byte, error)
	AcknowledgeCatalogWork(context.Context, catalog.SourceWork, catalog.FullProof) (bool, error)
}

type Receiver interface {
	CatalogStatus(context.Context) (catalog.Status, error)
	SendCatalogBody(context.Context, []byte) (searchevents.EventBatchResult, error)
}

// Deliver handles only normal-path evidence. Recovery/expiry refusals belong to
// the durable controller. Every attempt preflights and replays ALL frozen pages;
// no page receipt/cursor survives a receiver restore or establishes completion.
func Deliver(ctx context.Context, source Source, receiver Receiver, w catalog.SourceWork, observed catalog.Status) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	a, err := catalog.AssessReceiver(w.Evidence, observed)
	if err != nil {
		return false, err
	}
	if a.PendingCompleted {
		return source.AcknowledgeCatalogWork(ctx, w, *w.Evidence.Pending)
	}
	if !a.CanDeliver {
		return false, nil
	}
	p, limits := w.Evidence.Pending, observed.Limits.Effective()
	if w.PendingCanonicalBytes < 0 || w.PendingCanonicalBytes > limits.MaxSnapshotBytes ||
		(p.Manifest.DocumentCount == 0) != (w.PendingCanonicalBytes == 0) {
		return false, catalog.ErrQuotaExceeded
	}
	if _, err := catalog.ValidateFrozenEnd(w.PendingEndRequestBody, *p, limits); err != nil {
		return false, err
	}
	load := func(index int32) ([]byte, error) {
		body, err := source.LoadCatalogPage(ctx, p.Identity, index)
		if err != nil {
			return nil, err
		}
		if len(body) == 0 || int64(len(body)) > limits.MaxRequestBytes {
			return nil, catalog.ErrQuotaExceeded
		}
		return body, nil
	}
	// Validate every actual encoded request before the first POST. Keep only one
	// page in memory; immutable storage can later disappear but never be revised.
	for i := int32(0); i < p.Manifest.PageCount; i++ {
		if _, err := load(i); err != nil {
			return false, err
		}
	}
	confirm := func() (bool, error) {
		current, err := receiver.CatalogStatus(ctx)
		if err != nil {
			return false, err
		}
		a, err := catalog.AssessReceiver(w.Evidence, current)
		if err != nil {
			return false, err
		}
		if !a.PendingCompleted {
			return false, ErrCompletionPending
		}
		return source.AcknowledgeCatalogWork(ctx, w, *p)
	}
	for i := int32(0); i <= p.Manifest.PageCount; i++ {
		body := w.PendingEndRequestBody
		if i < p.Manifest.PageCount {
			body, err = load(i)
			if err != nil {
				return false, err
			}
		}
		receipt, sendErr := receiver.SendCatalogBody(ctx, body)
		if sendErr == nil && len(receipt.Failed) == 0 {
			continue
		}
		if sendErr == nil {
			sendErr = ErrReceiptRejected // Never persist raw upstream failure text.
		}
		// A response may be lost after commit, or another sender may complete it.
		// Only a fresh exact completed manifest can resolve the uncertainty.
		acked, verifyErr := confirm()
		if verifyErr == nil {
			return acked, nil
		}
		return false, errors.Join(sendErr, verifyErr)
	}
	return confirm()
}
