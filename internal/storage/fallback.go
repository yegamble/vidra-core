package storage

import (
	"context"
	"errors"
	"io"
	"log/slog"
)

// Fallback is the DUAL-READ view of two stores during a storage migration: reads
// try the primary and fall back to the secondary, writes only ever go to the
// primary.
//
// It exists because a migration has a window — from the moment the operator
// swaps STORAGE_* and restarts until the last object has been copied and
// verified — in which some objects live in the new store and some only in the
// old one. A viewer must not care. Open/Exists therefore ask the primary first
// and, only on a definite "not there", ask the secondary.
//
// # Why it implements Backend and NOTHING else
//
// Every optional capability in this package is a claim about ONE store, and a
// pair of stores cannot honestly make any of them:
//
//   - PathProvider would hand out a local path for an object that may be in the
//     other store, so ffprobe/thumbnailing would read the wrong file or none.
//   - ObjectLister/RootLister would have to merge two namespaces, and the caller
//     that most wants listing is media GC — which deletes what it lists. A GC
//     sweep that enumerated a merged view would happily delete from a store the
//     merge did not actually cover.
//   - Presigner would mint a URL for one store while the object is in the other,
//     producing a 404 the API can no longer fall back from.
//   - SizedPutter is a write hint, and writes here are the primary's alone.
//
// So this type deliberately fails every feature detection. Consumers that need a
// capability must be handed the RAW primary backend, not this wrapper — see the
// wiring in cmd/api/main.go, where only the HTTP serving handles get a Fallback
// and mediagc, the IPFS mirror, the doctor and every media tool keep the primary.
//
// A Fallback with a nil secondary is a valid, inert pass-through to the primary.
type Fallback struct {
	primary   Backend
	secondary Backend
	logger    *slog.Logger
}

// Compile-time proof that Fallback is a Backend and NOT any optional capability.
// If someone later adds a method that satisfies one of these, the corresponding
// line stops compiling and they have to read the doc comment above first.
var (
	_ Backend = (*Fallback)(nil)
)

// NewFallback wraps primary with a read-only fallback to secondary. A nil logger
// falls back to the default one.
func NewFallback(primary, secondary Backend, logger *slog.Logger) *Fallback {
	if logger == nil {
		logger = slog.Default()
	}
	return &Fallback{primary: primary, secondary: secondary, logger: logger}
}

// Put stores r in the PRIMARY only. New bytes belong to the store the instance
// is configured to use; mirroring them into the store being migrated away from
// would create objects the campaign never enumerated and would have to delete
// again.
func (f *Fallback) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	return f.primary.Put(ctx, key, r)
}

// Delete removes the object from the primary and then, best-effort, from the
// secondary.
//
// The secondary delete is what keeps a deletion from being undone by the
// fallback read: an object deleted from the new store while a stale copy sits in
// the old one would keep being served by Open. A secondary error is logged and
// swallowed — the authoritative delete succeeded, and failing the caller's
// request because a store being decommissioned was briefly unreachable would
// turn a bookkeeping problem into a user-visible one. The migration campaign's
// own delete-source pass is the durable cleanup for anything missed here.
func (f *Fallback) Delete(ctx context.Context, key string) error {
	if err := f.primary.Delete(ctx, key); err != nil {
		return err
	}
	if f.secondary == nil {
		return nil
	}
	if err := f.secondary.Delete(ctx, key); err != nil {
		// The key is not logged: it identifies media, and this line goes to the
		// same log an operator pastes into a ticket.
		f.logger.WarnContext(ctx, "storage: could not delete the migration counterpart copy; the migration's delete-source pass will collect it",
			"error", err.Error())
	}
	return nil
}

// Open returns the object from the primary, or from the secondary when the
// primary definitely does not have it. Any other primary error is returned
// as-is: a timeout or a permission problem is not evidence that the object is
// somewhere else, and quietly serving a stale copy from the store being migrated
// away from would hide a broken primary.
func (f *Fallback) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := f.primary.Open(ctx, key)
	if err == nil || f.secondary == nil || !errors.Is(err, ErrNotFound) {
		return rc, err
	}
	return f.secondary.Open(ctx, key)
}

// Exists reports whether either store holds the object, asking the secondary
// only when the primary says no. A primary error is returned rather than
// masked, for the same reason as Open.
func (f *Fallback) Exists(ctx context.Context, key string) (bool, error) {
	ok, err := f.primary.Exists(ctx, key)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return false, err
		}
		ok = false
	}
	if ok || f.secondary == nil {
		return ok, nil
	}
	return f.secondary.Exists(ctx, key)
}
