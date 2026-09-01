package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

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
	// ackVersion is the per-run operator acknowledgement of an unverified source
	// schema: the version the launching administrator was shown and accepted. 0
	// (the zero value, and the only value the server can produce on its own) means
	// no acknowledgement was made.
	ackVersion int
	// sourceAuthoritative says the SOURCE wins where the two sides diverge, rather
	// than the import only filling gaps. See Options.SourceAuthoritative.
	sourceAuthoritative bool
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
	// tagsByVideo / elementsByPlaylist are the bulk source reads the RESYNC needs
	// and the insert path does not: per-entity reads of these two families would
	// cost one round trip per video and per playlist on a run whose whole point is
	// that an unchanged entity costs nothing. Dropped between runs with the rest.
	tagsByVideo        map[int64][]string
	elementsByPlaylist map[int64][]SourcePlaylistElement
	// resync is the destination's side of a source-authoritative run: what this
	// instance currently holds for every row the import owns, read in bulk before
	// the passes start. Nil in the default gap-filling mode, and every resync
	// branch keys off that — see resync.go.
	resync *resyncState

	// The source instance's public HTTP origin, derived once from its own actors'
	// canonical URLs (see sourceOrigin). Unlike the video maps this is NOT reset
	// between runs: an instance's own origin is a property of the source, not of
	// the data in it, and it changing mid-migration would break far more than
	// avatars.
	originOnce sync.Once
	origin     string
	originErr  error

	// videoImageMu guards the report while the thumbnail/storyboard passes fan
	// out across workers. Their per-row outcomes are the only counters written
	// from more than one goroutine.
	videoImageMu sync.Mutex
}

// Options customise an Importer.
type Options struct {
	Policy ConflictPolicy
	// Force is the CLI's --force: a HUMAN operator's blanket override of the
	// version gate, including for a source whose version cannot be read at all.
	// Nothing but cmd/peertube-import may set it, and no agent may pass it.
	Force bool
	// AcknowledgedSchemaVersion is the API's narrower equivalent: the exact schema
	// version an administrator was shown and explicitly accepted on THIS launch
	// request. It opens the version gate only for that number (AcknowledgesVersion)
	// and widens nothing else. The server never sets it; it arrives from the
	// request or it is zero.
	AcknowledgedSchemaVersion int
	MediaMode                 MediaMode
	SrcMedia                  storage.Backend
	DestMedia                 storage.Backend
	SealKey                   func(pem string) (string, error)
	// SourceAuthoritative says the SOURCE is the truth where the two sides
	// diverge, instead of the import only filling gaps on this instance.
	//
	// The default (false) is gap-filling, and it is the right default: an import
	// that overwrites is an import that can quietly undo somebody's work. It
	// exists because the migration workflow this tool is actually used for is a
	// REPEATED sync — the operator runs it against a still-live PeerTube on a
	// schedule up to cutover, editing the new instance in between — and for that
	// operator "the source wins" is the correct answer, not a hazard.
	//
	// It is ORTHOGONAL to Policy. A ConflictPolicy resolves a NATURAL-KEY
	// collision (username, handle, email, slug) at insert time and never reaches
	// an ON CONFLICT, the ledger gate, or the taxonomy decision; this says whether
	// a re-run may UPDATE the rows the import already owns. Neither can be
	// expressed in terms of the other.
	//
	// What it reaches, and what it deliberately does not, is in resync.go —
	// including the four hard exclusions (no re-INSERT, no actor-key rotation, no
	// view-count assignment, no rendition overwrite).
	SourceAuthoritative bool
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
		dest:                dest,
		q:                   sqlcgen.New(dest),
		src:                 src,
		srcMedia:            opts.SrcMedia,
		destMedia:           opts.DestMedia,
		mediaMode:           mediaMode,
		policy:              policy,
		force:               opts.Force,
		ackVersion:          opts.AcknowledgedSchemaVersion,
		sourceAuthoritative: opts.SourceAuthoritative,
		sealKey:             opts.SealKey,
		reloadSettings:      opts.ReloadSettings,
		logger:              logger,
	}
}

// Preflight verifies the source is reachable and its schema version is supported
// (or a human has signed off on it), pings the destination, proves the
// destination media store will accept a write, and — for a local destination
// media store — checks there is plausibly enough free disk. It returns the
// detected source schema version EVEN when it refuses on that version, so the
// caller can tell the operator which version it saw.
//
// An unsupported version is a hard stop (*UnverifiedSchemaError) unless the
// operator overrode it: --force from a human on the CLI, or a per-run
// acknowledgement naming this exact version from an administrator through the
// API. Callers MUST NOT set either autonomously.
//
// Everything here is a question whose answer would otherwise arrive minutes into
// a run, in the middle of an operation that is half done. That is the standard
// each check is held to.
func (im *Importer) Preflight(ctx context.Context) (int, error) {
	if err := im.src.Ping(ctx); err != nil {
		return 0, fmt.Errorf("peertubeimport: source database unreachable: %w%s", err, sourceDialAdvice(err))
	}
	if err := im.dest.Ping(ctx); err != nil {
		return 0, fmt.Errorf("peertubeimport: destination database unreachable: %w", err)
	}
	// Before the version probe, because it is the cheaper question and the more
	// expensive answer: an unsupported version stops a run that has done nothing,
	// while a destination nobody can write to stops one that has already created
	// accounts, channels and videos.
	if err := im.checkDestinationWritable(ctx); err != nil {
		return 0, err
	}
	version, err := im.src.DetectVersion(ctx)
	if err != nil {
		return 0, err
	}
	if !IsSupported(version) && !im.force && !AcknowledgesVersion(im.ackVersion, version) {
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

// checkDestinationWritable proves the destination media store will accept an
// object from these credentials, by storing a tiny scratch object at a key no
// data lives under and removing it again (storage.ProbeWrite).
//
// It exists because a real migration ran for three minutes and then failed 1,321
// avatar uploads with `s3: put "avatars/users/…": not entitled`: the destination
// Backblaze B2 key had `readFiles` and not `writeFiles`. Nothing in this
// codebase noticed. Every other thing that touches the destination store before
// the first write is a read — the bucket head, the ownership marker, the
// lifecycle configuration — and a read-only credential passes all of them, so
// the first write in the run IS the check, and by then the operator is watching
// a wall of per-image warnings and deciding whether to let it finish.
//
// It runs for --dry-run too, and that is the point of a rehearsal: the run that
// is supposed to tell you what will happen must be the one that tells you the
// destination will refuse it. The probe object is removed again, so a dry run
// still leaves no trace — no ledger rows, no entities, no media.
//
// --media-mode=none skips it: that mode imports metadata and writes no objects
// at all, so there is nothing to prove. REFERENCE mode does NOT skip it, because
// reference is only reference for video: actor images are fetched over HTTP and
// stored, whatever the mode says.
//
// A failed CLEANUP never stops the run. A store that took the write has answered
// the question that was asked, and refusing to migrate into a store that can
// hold the migration because a scratch object could not be tidied away would be
// the wrong end of the trade — it is logged, with the key, so the operator can
// remove it.
func (im *Importer) checkDestinationWritable(ctx context.Context) error {
	if im.destMedia == nil || im.mediaMode == MediaModeNone {
		return nil
	}
	res, err := storage.ProbeWrite(ctx, im.destMedia)
	if err != nil {
		// The message is persisted on the run row and shown to an admin, so it
		// carries the store's IDENTITY (endpoint and bucket, never a key or a
		// secret) and what to grant, rather than a wrapped stack of SDK errors.
		return fmt.Errorf("peertubeimport: the destination media store %s refused a test write, so every object this run would store fails the same way: %w — grant this credential write access to the bucket (Backblaze B2: the `writeFiles` capability, which a `readFiles`-only key does not imply; AWS/MinIO/Spaces: `s3:PutObject`), then re-run",
			storage.Describe(im.destMedia), err)
	}
	if res.Leaked() && im.logger != nil {
		im.logger.WarnContext(ctx, "peertube import: destination write probe could not clean up after itself",
			"key", res.Key, "store", storage.Describe(im.destMedia), "error", res.CleanupErr)
	}
	return nil
}

// sourceDialAdvice names the most likely cause of a failed connection to the
// source database, as a clause to append to the driver's own message. It returns
// "" when nothing in the error is recognisable — a wrong guess sends an operator
// to the wrong machine, which is worse than no guess.
//
// It exists because pgx's message is accurate and unreadable at the moment it is
// needed. The two failures a real migration actually hit were `dial unix
// /tmp/.s.PGSQL.15432` — which is what a DSN with no usable host in it does,
// silently, rather than reporting a bad host — and a source PostgreSQL listening
// on 127.0.0.1 only, which from another machine is an ordinary connection
// refused. Neither says what to change.
//
// The advice never echoes the DSN: this string is persisted on the import run
// row and shown to admins.
func sourceDialAdvice(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "dial unix") || strings.Contains(msg, ".s.pgsql."):
		return " — that is a LOCAL unix socket, not the source host: PostgreSQL drivers fall back to one when the connection string carries no usable host, so a typo'd or missing host silently becomes a path on THIS machine. --source-dsn should be postgres://user:password@host:port/database naming the source; if you are tunnelling, name the local end of the tunnel (127.0.0.1:<local port>)"
	case strings.Contains(msg, "connection refused"):
		return " — the address answered and nothing is listening on that port. A PeerTube database is bound to 127.0.0.1 by default, so it is unreachable from anywhere else until listen_addresses and pg_hba.conf say otherwise; an SSH tunnel (ssh -L 15432:127.0.0.1:5432 <source host>, then --source-dsn against 127.0.0.1:15432) is the safer way and needs no change on the source"
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "lookup "):
		return " — the host name in --source-dsn does not resolve from here. Check the spelling, or use the source's IP address"
	case strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return " — the packets are being dropped rather than refused, which is a firewall or a cloud security group between here and the source rather than PostgreSQL itself. Open the port from this host, or tunnel over SSH (ssh -L 15432:127.0.0.1:5432 <source host>)"
	case strings.Contains(msg, "authentication failed") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "pg_hba"):
		return " — the source is REACHABLE and it rejected the login, so this is the credentials, the database name, or the source's pg_hba.conf rules rather than the network. A read-only role on the PeerTube database is all this tool needs"
	default:
		return ""
	}
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
	im.resetRunCaches() // the source moves between runs; never plan off a stale list
	r := NewReport(true, im.policy, im.sourceAuthoritative)
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
		// A dry run is where "how many of the accounts I am about to import are
		// suspended on the source?" is worth answering, so the plan counts them
		// too rather than leaving the kind at zero until the real run.
		if u.Blocked {
			r.count(KindUserSuspension).Planned++
		}
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

	if err := im.planActorImages(ctx, r); err != nil {
		return nil, err
	}

	videos, err := im.src.Videos(ctx)
	if err != nil {
		return nil, err
	}
	r.count(KindVideo).Planned = len(videos)
	for _, v := range videos {
		if v.NSFW {
			r.count(KindVideoSensitive).Planned++
		}
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

	// Posters and storyboards are counted from their own bulk source reads, not
	// from the per-video loop above: both are one row per video on the source and
	// both are carried by passes of their own. See entities_videoimages.go.
	if err := im.planVideoThumbnails(ctx, r); err != nil {
		return nil, err
	}
	if err := im.planStoryboards(ctx, r); err != nil {
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
	im.resetRunCaches()
	r := NewReport(false, im.policy, im.sourceAuthoritative)
	r.SourceVersion = version
	r.Deferred = deferredFamilies()

	// The destination snapshot is taken ONCE, here, before any pass runs: nine
	// bulk statements that answer "what does this instance currently hold for the
	// rows the import owns?". Every resync branch then compares in memory, so an
	// unchanged entity costs one map lookup and no query at all — which is what
	// keeps a no-op re-run at the ~21 seconds the cutover budget assumes.
	if im.sourceAuthoritative {
		st, err := im.loadResyncState(ctx)
		if err != nil {
			return r, err
		}
		im.resync = st
	}

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
		// Avatars/banners run after both, as a pass of their own for the same
		// reason the per-video families do — so a re-run backfills faces onto
		// accounts an earlier release already imported. See entities_actorimages.go.
		{"actor images", im.importActorImages},
		{"videos", im.importVideos},
		// Posters and storyboards run after videos, as passes of their own, for
		// exactly the reason the per-video families do — and because the posters
		// the old in-video path wrote point at objects PeerTube never stored, so
		// the catalogue that is already migrated is the one that needs them. See
		// entities_videoimages.go.
		{"thumbnails", im.importVideoThumbnails},
		{"storyboards", im.importStoryboards},
		// Per-video data runs AFTER videos and as passes of its own, so a re-run
		// backfills it onto videos an earlier release already imported. See
		// entities_pervideo.go.
		{"view counts", im.importViewCounts},
		{"original publication dates", im.importVideoOriginalDates},
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

// resetRunCaches drops everything an importer memoised about a PREVIOUS run.
// The scheduled-import workflow reuses one importer, and every one of these
// answers a question whose answer moves between runs.
func (im *Importer) resetRunCaches() {
	im.videosByID, im.videoVidraByID = nil, nil
	im.tagsByVideo, im.elementsByPlaylist = nil, nil
	im.resync = nil
}

// sourceTags returns every local video's tags, read from the source once per run.
func (im *Importer) sourceTags(ctx context.Context) (map[int64][]string, error) {
	if im.tagsByVideo != nil {
		return im.tagsByVideo, nil
	}
	tags, err := im.src.AllTags(ctx)
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = map[int64][]string{}
	}
	im.tagsByVideo = tags
	return tags, nil
}

// sourcePlaylistElements returns one playlist's slots from the bulk read.
func (im *Importer) sourcePlaylistElements(ctx context.Context, playlistID int64) ([]SourcePlaylistElement, error) {
	if im.elementsByPlaylist == nil {
		els, err := im.src.AllPlaylistElements(ctx)
		if err != nil {
			return nil, err
		}
		if els == nil {
			els = map[int64][]SourcePlaylistElement{}
		}
		im.elementsByPlaylist = els
	}
	return im.elementsByPlaylist[playlistID], nil
}

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

// optSchemaVersion wraps an acknowledged schema version for storage. Anything
// that is not a positive version is NULL: "no acknowledgement" and "acknowledged
// version zero" must not be two spellings of the same row, and the column's CHECK
// refuses the latter anyway.
func optSchemaVersion(v int) *int32 {
	if v <= 0 {
		return nil
	}
	v32 := int32(v)
	return &v32
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
