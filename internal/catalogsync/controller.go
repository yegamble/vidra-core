package catalogsync

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/catalog"
)

var ErrBootstrapPending = errors.New("catalog initial intent requires verified epoch handshake")

type ControllerSource interface {
	Source
	LoadCatalogWork(context.Context) (catalog.SourceWork, error)
	ConvertLegacyCatalog(context.Context, int32) (int64, error)
	CatalogCaptureDemand(context.Context) (catalog.CaptureDemand, error)
	CaptureCatalog(context.Context, uuid.UUID, catalog.Limits) (catalog.Identity, error)
	RecordCatalogCaptureFailure(context.Context, catalog.SourceWork, string) (bool, error)
	RecordCatalogDeliveryFailure(context.Context, catalog.SourceWork, string) (bool, error)
}

// Cycle advances normal durable work once, never read readiness. Bootstrap and
// restore refusals remain explicit until their controller paths are composed.
// Every source call must release its connection before receiver HTTP starts.
func Cycle(ctx context.Context, source ControllerSource, receiver Receiver, now time.Time, repairInterval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	w, err := source.LoadCatalogWork(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // No durable operator bootstrap intent, so v2 stays dormant.
	}
	if err != nil {
		return &cycleFailure{code: "verification_failed", cause: err}
	}
	if w.LastErrorCode == "epoch_conflict" {
		return catalog.ErrEpochConflict
	}
	converted, err := source.ConvertLegacyCatalog(ctx, 1000)
	if err != nil {
		return failCycle(ctx, source, w, "verification_failed", err)
	}
	demand, err := source.CatalogCaptureDemand(ctx)
	if err != nil {
		return failCycle(ctx, source, w, "verification_failed", err)
	}
	if converted != 0 || demand.LegacyOutstanding {
		return nil // Conversion leaves a durable hint; coalesce before capture.
	}
	if w.Evidence.AllocatedGeneration == 0 {
		return ErrBootstrapPending
	}
	observed, err := receiver.CatalogStatus(ctx)
	if err != nil {
		return failCycle(ctx, source, w, "verification_failed", err)
	}
	if _, err := catalog.AssessReceiver(w.Evidence, observed); err != nil {
		return failCycle(ctx, source, w, "verification_failed", err)
	}
	if w.Evidence.Pending != nil {
		if _, err := Deliver(ctx, source, receiver, w, observed); err != nil {
			return failCycle(ctx, source, w, "delivery_failed", err)
		}
		return nil
	}
	if repairInterval <= 0 {
		repairInterval = 24 * time.Hour
	}
	if demand.Dirty || w.ForceFull || w.LastErrorCode != "" || w.VerifiedAt == nil || now.Sub(*w.VerifiedAt) >= repairInterval {
		if _, err := source.CaptureCatalog(ctx, w.Evidence.Epoch, observed.Limits.Effective()); err != nil {
			return failCycle(ctx, source, w, "capture_failed", err)
		}
	}
	return nil // No-work is never an error-clear or readiness mutation.
}

type cycleFailure struct {
	code  string
	cause error
}

func (e *cycleFailure) Error() string { return "catalog cycle: " + e.code }
func (e *cycleFailure) Unwrap() error { return e.cause }

func failCycle(ctx context.Context, source ControllerSource, w catalog.SourceWork, code string, cause error) error {
	switch {
	case errors.Is(cause, catalog.ErrEpochConflict), errors.Is(cause, catalog.ErrReceiverEpochMismatch),
		errors.Is(cause, catalog.ErrProducerBehindReceiver), errors.Is(cause, catalog.ErrReceiverProofMismatch):
		code = "epoch_conflict"
	case errors.Is(cause, catalog.ErrQuotaExceeded):
		code = "quota_exceeded"
	case errors.Is(cause, catalog.ErrScalarLimit):
		code = "scalar_limit_exceeded"
	}
	// Even a timed-out GET/capture needs a bounded attempt to retain its durable
	// read barrier. Exact Work/error-token CAS protects newer successful work.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var err error
	if w.Evidence.Pending == nil {
		_, err = source.RecordCatalogCaptureFailure(persist, w, code)
	} else {
		_, err = source.RecordCatalogDeliveryFailure(persist, w, code)
	}
	return &cycleFailure{code: code, cause: errors.Join(cause, err)}
}
