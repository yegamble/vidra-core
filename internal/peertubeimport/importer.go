package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrConflictFail is returned when the 'fail' conflict policy hits a collision,
// aborting the whole run. It is a safe, operator-facing message.
var ErrConflictFail = errors.New("peertubeimport: aborting on conflict (policy=fail)")

// Importer performs one PeerTube→Vidra migration. It reads from a Source
// (read-only) and writes into the Vidra database + media store, recording every
// mapped row in the durable ledger for idempotency + resume. It is safe to
// re-run: already-imported entities are skipped.
type Importer struct {
	dest      *pgxpool.Pool
	q         *sqlcgen.Queries
	src       *Source
	srcMedia  storage.Backend // may be nil (metadata-only import)
	destMedia storage.Backend // may be nil (metadata-only import)
	mediaMode MediaMode
	policy    ConflictPolicy
	force     bool
	// sealKey seals an actor private-key PEM for at-rest storage (secretbox under
	// the KEK). Nil → store raw (dev only), matching the federation service.
	sealKey func(pem string) (string, error)
	// reloadSettings invalidates + reloads the running server's instance-settings
	// cache after the import writes one of those settings. Nil (the CLI, which
	// exits) → nothing to reload.
	reloadSettings func(context.Context) error
	logger         *slog.Logger

	// videosByID caches the source videos by numeric id, built once so
	// comments/playlist-elements/renditions (which reference the numeric id) can
	// resolve the ledger entry, which is keyed by the video UUID — and so the
	// per-video passes read the video list from the source once rather than once
	// each.
	videosByID map[int64]SourceVideo
	// videoVidraByID memoises the source-numeric-id → Vidra-video-id resolution
	// for one run (uuid.Nil = the video is not in the ledger). Both maps are
	// dropped at the start of every Run/Plan: a reused importer must never answer
	// from the source as it stood on a previous run.
	videoVidraByID map[int64]uuid.UUID
}

// Options customise an Importer.
type Options struct {
	Policy    ConflictPolicy
	Force     bool
	MediaMode MediaMode
	SrcMedia  storage.Backend
	DestMedia storage.Backend
	SealKey   func(pem string) (string, error)
	// ReloadSettings reloads the instance-settings overlay after the import
	// writes one (today: the instance category taxonomy). The server caches that
	// overlay in memory and only reloads it after its own writes, so without this
	// a carried taxonomy sits in the database without taking effect until the
	// next restart. Nil in a one-shot process.
	ReloadSettings func(context.Context) error
	Logger         *slog.Logger
}

// NewImporter builds an importer over an open Vidra pool and source.
func NewImporter(dest *pgxpool.Pool, src *Source, opts Options) *Importer {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	policy := opts.Policy
	if policy == "" {
		policy = PolicySkip
	}
	mediaMode := opts.MediaMode
	if mediaMode == "" {
		mediaMode = MediaModeCopy
	}
	return &Importer{
		dest:           dest,
		q:              sqlcgen.New(dest),
		src:            src,
		srcMedia:       opts.SrcMedia,
		destMedia:      opts.DestMedia,
		mediaMode:      mediaMode,
		policy:         policy,
		force:          opts.Force,
		sealKey:        opts.SealKey,
		reloadSettings: opts.ReloadSettings,
		logger:         logger,
	}
}

// Preflight verifies the source is reachable and its schema version is supported
// (or --force is set), pings the destination, and — for a local destination
// media store — checks there is plausibly enough free disk. It returns the
// detected source schema version. An unsupported version without force is a hard
// stop (VersionError); callers MUST NOT set force autonomously.
func (im *Importer) Preflight(ctx context.Context) (int, error) {
	if err := im.src.Ping(ctx); err != nil {
		return 0, fmt.Errorf("peertubeimport: source database unreachable: %w", err)
	}
	if err := im.dest.Ping(ctx); err != nil {
		return 0, fmt.Errorf("peertubeimport: destination database unreachable: %w", err)
	}
	version, err := im.src.DetectVersion(ctx)
	if err != nil {
		return 0, err
	}
	if !IsSupported(version) && !im.force {
		return version, VersionError(version)
	}
	// Storage reachability + free-disk (best effort; only meaningful for a local
	// destination, where a PathProvider exposes the root).
	if im.mediaMode == MediaModeCopy && im.destMedia != nil {
		if pp, ok := im.destMedia.(storage.PathProvider); ok {
			if root, perr := pp.Path("."); perr == nil {
				if free, derr := freeDiskBytes(root); derr == nil {
					if need, nerr := im.estimateBytes(ctx); nerr == nil && need > 0 && free < uint64(need) {
						return version, fmt.Errorf("peertubeimport: insufficient free disk at destination (%d bytes free, ~%d needed)", free, need)
					}
				}
			}
		}
	}
	return version, nil
}

// estimateBytes sums the highest-resolution web file size across all source
// videos — the rough disk the copied originals will need.
func (im *Importer) estimateBytes(ctx context.Context) (int64, error) {
	videos, err := im.src.Videos(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, v := range videos {
		files, err := im.src.VideoFiles(ctx, v.ID)
		if err != nil {
			return 0, err
		}
		if len(files) > 0 {
			total += files[0].Size // highest resolution first
		}
	}
	return total, nil
}

// Plan performs a --dry-run: it counts every entity the import would touch,
// flags username/handle collisions under the current policy, and writes NOTHING
// (no ledger rows, no entities, no media). The returned Report is the mapping
// plan the operator reviews before running for real.
func (im *Importer) Plan(ctx context.Context, version int) (*Report, error) {
	im.videosByID, im.videoVidraByID = nil, nil // the source moves between runs; never plan off a stale list
	r := NewReport(true, im.policy)
	r.SourceVersion = version
	r.Deferred = deferredFamilies()

	if err := im.planCategoryTaxonomy(ctx, r); err != nil {
		return nil, err
	}

	users, err := im.src.Users(ctx)
	if err != nil {
		return nil, err
	}
	r.count(KindUser).Planned = len(users)
	for _, u := range users {
		if note, collides, err := im.userConflict(ctx, u); err != nil {
			return nil, err
		} else if collides {
			r.addConflict(note)
		}
	}

	channels, err := im.src.Channels(ctx)
	if err != nil {
		return nil, err
	}
	r.count(KindChannel).Planned = len(channels)
	for _, c := range channels {
		if exists, err := im.handleExists(ctx, c.Handle); err != nil {
			return nil, err
		} else if exists {
			r.addConflict(fmt.Sprintf("channel handle %q already exists (policy=%s)", c.Handle, im.policy))
		}
	}

	videos, err := im.src.Videos(ctx)
	if err != nil {
		return nil, err
	}
	r.count(KindVideo).Planned = len(videos)
	for _, v := range videos {
		files, err := im.src.VideoFiles(ctx, v.ID)
		if err != nil {
			return nil, err
		}
		if len(files) > 0 {
			r.count(KindVideoFile).Planned++
		}
		if _, ok, err := im.src.HLSPlaylist(ctx, v.ID); err != nil {
			return nil, err
		} else if ok {
			r.count(KindHLSPlaylist).Planned++
		}
		if thumb, err := im.src.ThumbnailFilename(ctx, v.ID); err != nil {
			return nil, err
		} else if thumb != "" {
			r.count(KindThumbnail).Planned++
		}
		caps, err := im.src.Captions(ctx, v.ID)
		if err != nil {
			return nil, err
		}
		r.count(KindCaption).Planned += len(caps)
		tags, err := im.src.Tags(ctx, v.ID)
		if err != nil {
			return nil, err
		}
		r.count(KindTag).Planned += len(tags)
	}

	if err := im.planPerVideo(ctx, r, videos); err != nil {
		return nil, err
	}

	comments, err := im.src.Comments(ctx)
	if err != nil {
		return nil, err
	}
	r.count(KindComment).Planned = len(comments)

	playlists, err := im.src.Playlists(ctx)
	if err != nil {
		return nil, err
	}
	r.count(KindPlaylist).Planned = len(playlists)
	for _, p := range playlists {
		els, err := im.src.PlaylistElements(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		r.count(KindPlaylistItem).Planned += len(els)
	}

	follows, err := im.src.Follows(ctx)
	if err != nil {
		return nil, err
	}
	r.count(KindFollow).Planned = len(follows)

	return r, nil
}

// deferredFamilies lists entity families intentionally NOT migrated in this
// version, surfaced in every report + the docs so operators reconcile them.
func deferredFamilies() []string {
	return []string{
		"HLS copying in copy mode (reference mode reuses existing PeerTube HLS objects; copy mode regenerates via Vidra transcoding)",
		"moderation state (video blacklist, account/server blocklists, abuse reports)",
		"user notification settings and watch history",
		"live sessions, plugins, themes, runners, redundancy config",
		"account + channel avatars and banners (actorImage)",
		"original-file provenance records (videoSource)",
		"per-day view history: the source records one lifetime total per video and no daily breakdown, so the total is carried and video_view_days is left empty rather than inventing buckets",
	}
}

// Run performs the import. It processes entity families parent-first, each row
// idempotent via the ledger, calling progress (when non-nil) after each family
// so a caller can persist a snapshot. On the 'fail' policy a collision aborts
// with ErrConflictFail. The returned Report is the final tally.
func (im *Importer) Run(ctx context.Context, version int, progress func(*Report)) (*Report, error) {
	// Drop the cached source video list: an importer reused across runs (and the
	// scheduled-import workflow is exactly that) would otherwise resolve children
	// against the video set as it stood the FIRST time, so every video added to
	// the source since would silently lose its comments, chapters and ratings.
	im.videosByID, im.videoVidraByID = nil, nil
	r := NewReport(false, im.policy)
	r.SourceVersion = version
	r.Deferred = deferredFamilies()

	steps := []struct {
		name string
		fn   func(context.Context, *Report) error
	}{
		// The instance taxonomy goes first: it has no parents to wait for, and a
		// video's category id only means something once the list that defines it
		// is in place. See entities_taxonomy.go.
		{"category taxonomy", im.importCategoryTaxonomy},
		{"users", im.importUsers},
		{"channels", im.importChannels},
		{"videos", im.importVideos},
		// Per-video data runs AFTER videos and as passes of its own, so a re-run
		// backfills it onto videos an earlier release already imported. See
		// entities_pervideo.go.
		{"view counts", im.importViewCounts},
		{"chapters", im.importChapters},
		{"ratings", im.importRatings},
		{"renditions", im.importRenditions},
		{"comments", im.importComments},
		{"playlists", im.importPlaylists},
		{"follows", im.importFollows},
	}
	for _, step := range steps {
		if err := step.fn(ctx, r); err != nil {
			return r, err
		}
		if progress != nil {
			progress(r)
		}
	}
	return r, nil
}

// ── shared helpers ──

// withTx runs fn inside a destination transaction, committing on success. The
// entity insert(s) and the ledger upsert share one transaction so a crash cannot
// leave a mapped-but-missing (or missing-but-mapped) row.
func (im *Importer) withTx(ctx context.Context, fn func(q *sqlcgen.Queries) error) error {
	tx, err := im.dest.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(im.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// resolveParent returns the mapped Vidra id of an already-imported parent
// entity (status done/skipped with a usable vidra_id). ok=false means the parent
// was not imported (e.g. skipped-without-target, failed, or absent), so the child
// must be skipped too.
func (im *Importer) resolveParent(ctx context.Context, kind, sourceID string) (uuid.UUID, bool, error) {
	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{EntityKind: kind, SourceID: sourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if (row.Status == "done" || row.Status == "skipped") && row.VidraID.Valid {
		return uuid.UUID(row.VidraID.Bytes), true, nil
	}
	return uuid.Nil, false, nil
}

// alreadyProcessed reports whether a source entity has a terminal ledger row
// (done/skipped/unsupported) — used to make re-runs a no-op.
func (im *Importer) alreadyProcessed(ctx context.Context, kind, sourceID string) (uuid.UUID, string, bool, error) {
	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{EntityKind: kind, SourceID: sourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", false, nil
	}
	if err != nil {
		return uuid.Nil, "", false, err
	}
	terminal := row.Status == "done" || row.Status == "skipped" || row.Status == "unsupported"
	var id uuid.UUID
	if row.VidraID.Valid {
		id = uuid.UUID(row.VidraID.Bytes)
	}
	return id, row.Status, terminal, nil
}

// recordLedger upserts a ledger row (used inside a tx alongside the entity insert).
func recordLedger(ctx context.Context, q *sqlcgen.Queries, kind, sourceID string, vidraID uuid.UUID, status, note string) error {
	_, err := q.UpsertImportLedgerEntry(ctx, sqlcgen.UpsertImportLedgerEntryParams{
		EntityKind: kind,
		SourceID:   sourceID,
		VidraID:    optUUID(vidraID),
		Status:     status,
		Note:       note,
	})
	return err
}

// copyMedia streams a source object into the destination store, returning the
// byte size and a hex sha-256 checksum. It is idempotent (Put overwrites), so a
// re-run after a crash safely re-copies. size is bounded by maxSourceFileBytes.
func (im *Importer) copyMedia(ctx context.Context, srcKey, destKey string) (int64, string, error) {
	if im.srcMedia == nil || im.destMedia == nil {
		return 0, "", fmt.Errorf("peertubeimport: media copy requested without source+destination storage")
	}
	rc, err := im.srcMedia.Open(ctx, srcKey)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = rc.Close() }()
	// The LimitReader below is a CAP, not a length: maxSourceFileBytes+1 exists
	// so an absurd source object trips the guard after it, and passing it as the
	// upload size would tell the destination to expect 16 GiB of a 4 KB
	// thumbnail. So the length is taken from the SOURCE READER before it is
	// wrapped — the wrapper hides its concrete type, and without this every
	// import copied with an unknown length and paid for a multipart upload per
	// object. Only a size that is provably the whole object is passed: an object
	// bigger than the cap still goes up unsized, so the check below still sees
	// cap+1 bytes and rejects it.
	size := storage.SizeUnknown
	if n := storage.SizeOf(rc); n >= 0 && n <= maxSourceFileBytes {
		size = n
	}
	limited := io.LimitReader(rc, maxSourceFileBytes+1)
	n, sum, err := storage.PutSizedHashed(ctx, im.destMedia, destKey, limited, size)
	if err != nil {
		return 0, "", err
	}
	if n > maxSourceFileBytes {
		_ = im.destMedia.Delete(ctx, destKey)
		return 0, "", fmt.Errorf("peertubeimport: source object %s exceeds the size cap", destKey)
	}
	return n, sum, nil
}

// optUUID wraps a uuid as a nullable pgtype.UUID (Nil → NULL).
func optUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

// sealPrivateKey seals an actor private-key PEM for at-rest storage. With no
// sealer configured (dev) it stores the raw PEM, matching the federation
// service's behaviour. The PEM is NEVER logged.
func (im *Importer) sealPrivateKey(pem string) (string, error) {
	if pem == "" {
		return "", nil
	}
	if im.sealKey == nil {
		return pem, nil
	}
	return im.sealKey(pem)
}
