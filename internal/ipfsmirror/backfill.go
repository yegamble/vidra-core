package ipfsmirror

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrCatalogNotConfigured is returned by Backfill when the service was built
// without a Catalog (the one-shot admin reconcile is not wired on this build).
var ErrCatalogNotConfigured = errors.New("ipfsmirror: backfill catalog not configured")

// VideoObjectRow is one pre-existing, video-derived candidate object for the
// backfill scan, carrying the parent video's visibility facts — privacy, state,
// AND the owner's unlisted flag — so the eligibility fence re-checks it before
// anything is enqueued (an unlisted owner's public videos are never mirrored).
type VideoObjectRow struct {
	ObjectKey     string
	Class         MediaClass
	VideoID       uuid.UUID
	Privacy       string
	State         string
	OwnerUnlisted bool
}

// IdentityImageRow is one pre-existing user/channel avatar or banner candidate,
// carrying the owning account's flags for the eligibility fence. OwnerUserID is
// the account (a channel image's owner) — the provenance the ledger records.
type IdentityImageRow struct {
	ObjectKey     string
	Class         MediaClass
	OwnerUserID   uuid.UUID
	OwnerActive   bool
	OwnerUnlisted bool
}

// PlaylistCoverRow is one pre-existing public-playlist cover candidate. ObjectKey
// is the resolved storage key; Visibility is re-checked by the fence.
type PlaylistCoverRow struct {
	ObjectKey  string
	Visibility string
}

// Catalog is the read-only enumeration surface the one-shot backfill scans. It
// lists EVERY pre-existing object that could be mirrored, with the visibility facts
// the eligibility fence needs — it does NOT itself decide eligibility. *SQLLookups
// satisfies it in production; tests fake it. Deliberately separate from Lookups
// (per-entity) so the bulk-scan concern is isolated and only wired where the admin
// reconcile is served.
type Catalog interface {
	ListBackfillVideoObjects(ctx context.Context) ([]VideoObjectRow, error)
	ListBackfillIdentityImages(ctx context.Context) ([]IdentityImageRow, error)
	ListBackfillPlaylistCovers(ctx context.Context) ([]PlaylistCoverRow, error)
}

// BackfillCounts is the per-class tally of pin intents newly seeded by a one-shot
// catalog reconcile. Total is the sum of ByClass. A second run over an unchanged
// catalog yields Total=0 and an empty ByClass (every eligible object already has a
// ledger row) — the idempotency contract.
type BackfillCounts struct {
	Total   int64
	ByClass map[string]int64
}

// Backfill is the one-shot admin reconcile (P19.6): it scans the whole catalog and
// seeds a pin intent for EVERY eligible pre-existing object that has no ledger row
// yet — the objects that predate the mirror or were missed while the node was down.
// It is idempotent: an object that already has a ledger row (any state) is left
// untouched, so a second run enqueues zero (the periodic Reconcile re-arms failed
// rows separately).
//
// Every candidate is passed through the Eligible privacy fence BEFORE it is
// enqueued — the SQL enumeration's WHERE clauses are only an efficiency
// coarse-filter; this Go gate is the authoritative public-only guarantee, the same
// one the eligibility unit table exercises. Nothing private/unlisted/quarantined/
// non-public is ever seeded. No-op (empty counts) when disabled.
func (s *Service) Backfill(ctx context.Context) (BackfillCounts, error) {
	counts := BackfillCounts{ByClass: map[string]int64{}}
	if !s.enabled {
		return counts, nil
	}
	if s.catalog == nil {
		return counts, ErrCatalogNotConfigured
	}

	videos, err := s.catalog.ListBackfillVideoObjects(ctx)
	if err != nil {
		return counts, err
	}
	for _, r := range videos {
		if !Eligible(Subject{Class: r.Class, VideoPrivacy: r.Privacy, VideoState: r.State, OwnerUnlisted: r.OwnerUnlisted}) {
			continue
		}
		if err := s.backfillOne(ctx, r.ObjectKey, r.Class, r.VideoID, uuid.Nil, &counts); err != nil {
			return counts, err
		}
	}

	images, err := s.catalog.ListBackfillIdentityImages(ctx)
	if err != nil {
		return counts, err
	}
	for _, r := range images {
		if !Eligible(Subject{Class: r.Class, OwnerActive: r.OwnerActive, OwnerUnlisted: r.OwnerUnlisted}) {
			continue
		}
		if err := s.backfillOne(ctx, r.ObjectKey, r.Class, uuid.Nil, r.OwnerUserID, &counts); err != nil {
			return counts, err
		}
	}

	covers, err := s.catalog.ListBackfillPlaylistCovers(ctx)
	if err != nil {
		return counts, err
	}
	for _, r := range covers {
		if !Eligible(Subject{Class: ClassPlaylistCover, PlaylistVisibility: r.Visibility}) {
			continue
		}
		if err := s.backfillOne(ctx, r.ObjectKey, ClassPlaylistCover, uuid.Nil, uuid.Nil, &counts); err != nil {
			return counts, err
		}
	}

	if counts.Total > 0 {
		s.logger.Info("ipfs backfill seeded pin intents", "enqueued", counts.Total, "classes", len(counts.ByClass))
	}
	return counts, nil
}

// backfillOne seeds one pin intent (insert-if-missing) and tallies it only when a
// row was actually inserted (execrows == 1), so ByClass reflects genuinely new work.
func (s *Service) backfillOne(ctx context.Context, objectKey string, class MediaClass, videoID, ownerID uuid.UUID, counts *BackfillCounts) error {
	n, err := s.repo.BackfillIPFSPinIntent(ctx, sqlcgen.BackfillIPFSPinIntentParams{
		ObjectKey:   objectKey,
		MediaClass:  string(class),
		VideoID:     pgUUID(videoID),
		OwnerUserID: pgUUID(ownerID),
	})
	if err != nil {
		return err
	}
	if n > 0 {
		counts.ByClass[string(class)] += n
		counts.Total += n
	}
	return nil
}

// ---- SQL-backed Catalog (production) --------------------------------------
//
// SQLLookups (lookups.go) satisfies Catalog too — the same *sqlcgen.Queries handle
// backs both — so cmd/api passes one NewSQLLookups value for both roles.

var _ Catalog = (*SQLLookups)(nil)

func (l *SQLLookups) ListBackfillVideoObjects(ctx context.Context) ([]VideoObjectRow, error) {
	rows, err := l.q.ListBackfillVideoObjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]VideoObjectRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, VideoObjectRow{
			ObjectKey:     r.ObjectKey,
			Class:         MediaClass(r.MediaClass),
			VideoID:       r.VideoID,
			Privacy:       r.Privacy,
			State:         r.State,
			OwnerUnlisted: r.OwnerUnlisted,
		})
	}
	return out, nil
}

func (l *SQLLookups) ListBackfillIdentityImages(ctx context.Context) ([]IdentityImageRow, error) {
	rows, err := l.q.ListBackfillIdentityImages(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]IdentityImageRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, IdentityImageRow{
			ObjectKey:     r.ObjectKey,
			Class:         MediaClass(r.MediaClass),
			OwnerUserID:   r.OwnerUserID,
			OwnerActive:   r.OwnerActive,
			OwnerUnlisted: r.OwnerUnlisted,
		})
	}
	return out, nil
}

func (l *SQLLookups) ListBackfillPlaylistCovers(ctx context.Context) ([]PlaylistCoverRow, error) {
	rows, err := l.q.ListBackfillPlaylistCovers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PlaylistCoverRow, 0, len(rows))
	for _, r := range rows {
		if r.ThumbnailExt == nil || *r.ThumbnailExt == "" {
			continue
		}
		out = append(out, PlaylistCoverRow{
			ObjectKey:  media.PlaylistThumbnailKey(r.ID, *r.ThumbnailExt),
			Visibility: r.Visibility,
		})
	}
	return out, nil
}
