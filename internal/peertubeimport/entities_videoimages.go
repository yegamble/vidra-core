package peertubeimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// This file carries a video's two IMAGE assets: its poster (the source's
// `thumbnail` table) and its seek-preview storyboard (the source's `storyboard`
// table).
//
// ── why these are passes of their own ──
//
// Both used to be — or, for storyboards, would naturally have been — folded into
// importOneVideo. That is wrong for the same reason it is wrong for chapters and
// views (see entities_pervideo.go): importVideos short-circuits on a terminal
// KindVideo ledger row, and resyncOneVideo touches metadata and tags only, so
// anything inside that path reaches ONLY videos imported after it ships. The
// instance this fixes has 12–16k videos already imported; a fix that cannot
// reach them is not a fix. A pass of its own, with its own ledger kinds,
// backfills onto the catalogue that is already there.
//
// ── why the poster was broken, which is what this replaces ──
//
// The old code recorded a kind='thumbnail' video_files row pointing at
// thumbnails/<peertube-filename> (reference mode) or copied from that key
// (copy mode), on the assumption that a PeerTube thumbnail is an object in the
// source's object store.
//
// IT IS NOT. PeerTube's object_storage config has exactly five families —
// streaming_playlists, web_videos, user_exports, original_video_files and
// captions. Thumbnails, previews, storyboards and avatars are not among them and
// never leave the source host's local filesystem. has_thumbnail is an EXISTS over
// those rows, so every video reported a poster and every GET /thumbnail 404'd:
// on the instance that found this, 40 of 40 sampled. In copy mode against an S3
// source it was worse — the failed Open propagated and took the whole video
// import down with it.
//
// The one configuration that always worked is --source-local-root pointed at the
// source host's own storage tree, where thumbnails/ really is on disk. That is
// why the acquisition order below tries srcMedia FIRST and only then the HTTP
// route: it keeps the configuration that works and fixes the two that do not.

// videoImageAction is what a run decided to do with one video's poster or
// storyboard. It mirrors the actor-image outcomes exactly (see decideActorImage)
// because it is the same judgement about a different slot.
type videoImageAction int

const (
	// videoImageWrite — the slot is empty and filling it is what an import is for.
	videoImageWrite videoImageAction = iota
	// videoImageReplace — the slot holds something the import may replace: an
	// asset it can prove it wrote, or a row whose bytes are not actually there.
	videoImageReplace
	// videoImageUpToDate — the slot already holds this carry. No request, no
	// write, no cost.
	videoImageUpToDate
	// videoImageOperatorOwned — the slot holds an asset the import cannot claim
	// (a creator's uploaded poster, a Vidra-generated storyboard). Left exactly
	// as it is, and reported.
	videoImageOperatorOwned
	// videoImageCleared — the import filled this slot and it is empty again.
	// Somebody removed it, which is a decision and not a gap to refill nightly.
	videoImageCleared
)

// videoImageSlot is the DESTINATION's side of the decision for one video's
// poster or storyboard. Every field is evidence already on this instance.
type videoImageSlot struct {
	// present and current describe the video_files row in the slot right now,
	// current being the same fingerprint shape the import records for its writes.
	present bool
	current string
	// bytesPresent says the object that row points at is ACTUALLY IN THE STORE.
	// It is the field the whole of this fix turns on: a row whose object is not
	// there is a broken poster on every card in the catalogue, and nobody owns
	// bytes that do not exist.
	bytesPresent bool
	// nativeKey says the row sits at the key Vidra's own writers use
	// (media.VideoThumbnailKey / media.StoryboardKey*). See decideVideoImage for
	// why that is the one-time provenance bridge.
	nativeKey bool
	// carried is the fingerprint the import recorded the last time it wrote here.
	carried string
	// wroteBefore says the ledger holds a COMPLETED import write onto this slot,
	// and untouched that nothing has written the slot since that row landed.
	wroteBefore bool
	untouched   bool
	// carriedThisFile says the ledger already records carrying THIS source row.
	carriedThisFile bool
}

// decideVideoImage is the replace-versus-clobber judgement for a video's poster
// or storyboard: a pure function of what this instance holds, what the import
// remembers writing, and which side the operator said wins.
//
// ── the one-time provenance bridge ──
//
// The old importer wrote NO ledger row for a thumbnail (KindThumbnail was a
// report counter and nothing else), so for the 12–16k posters already out there
// the ledger has nothing to say about who wrote them. Two facts stand in for it,
// once:
//
//   - THE KEY SHAPE. Every poster Vidra itself writes is at
//     thumbnails/<video_id>.jpg — SetThumbnail, SetThumbnailFromFrame and
//     generateThumbnail all go through media.VideoThumbnailKey and there is no
//     fourth writer. The old import wrote thumbnails/<random-uuid>.jpg in copy
//     mode and thumbnails/<peertube-filename> in reference mode, so a row that is
//     NOT at the native key was written by the import.
//   - THE BYTES. Before replacing anything, the object is looked up. A row whose
//     object is genuinely there is working, whoever wrote it, and is left alone —
//     which is precisely what preserves the --source-local-root copy-mode
//     migration that was correct all along. A row whose object is absent is the
//     live failure, and replacing it takes nothing from anyone.
//
// From this change forward the ledger carries the provenance and the key-shape
// rule is only the bridge: the first run records a fingerprint for every slot it
// settles, and every later run takes the ordinary path above it.
func decideVideoImage(s videoImageSlot, mode importMode) videoImageAction {
	switch {
	case !s.present:
		if s.carried != "" || s.wroteBefore || s.carriedThisFile {
			if mode == modeSourceAuthoritative {
				return videoImageWrite
			}
			return videoImageCleared
		}
		return videoImageWrite

	case !s.bytesPresent:
		// The row claims an asset the store does not have. This is the failure
		// the pass exists for, and it is a gap in either mode: there is nothing
		// here for anyone to own.
		return videoImageReplace

	case s.carried == "" && !s.wroteBefore && !s.nativeKey:
		// The bridge, and the ONLY branch that reads the key shape: an
		// import-written row from before the ledger remembered these families,
		// whose object is present (checked above). It works, so it is adopted
		// rather than re-fetched — the run records a fingerprint for it and every
		// later run recognises it the ordinary way.
		return videoImageUpToDate

	case s.carried != "" && s.current == s.carried:
		// The slot still holds precisely what the import put there.
		if s.carriedThisFile {
			return videoImageUpToDate
		}
		return videoImageReplace // ...but the source now offers a different row

	case s.carried == "" && s.wroteBefore && s.untouched:
		// Carried by a release that recorded no fingerprint, and untouched since.
		return videoImageReplace

	case mode == modeSourceAuthoritative:
		return videoImageReplace

	default:
		return videoImageOperatorOwned
	}
}

// conflict returns the operator-facing note for a decision that leaves a slot
// alone. Divergence nobody is told about is the failure this guards against, so
// both leave-alone outcomes say so in the report.
func (a videoImageAction) conflict(what, sourceID string) (string, bool) {
	switch a {
	case videoImageOperatorOwned:
		return fmt.Sprintf("source %s %s: this video already has a %s the import did not write; it is left unchanged (the import only ever updates its own)", what, sourceID, what), true
	case videoImageCleared:
		return fmt.Sprintf("source %s %s: the %s the import wrote was removed on this instance; it is left unchanged", what, sourceID, what), true
	}
	return "", false
}

// videoImageFingerprint identifies the exact object the import put in a slot.
// The key alone would not do it — both keys here are derived from the Vidra
// video id, so a replacement lands on the same key — so it is key + stored size,
// both of which the write itself already produced.
func videoImageFingerprint(storageKey string, sizeBytes int64) string {
	if storageKey == "" {
		return ""
	}
	return storageKey + "|" + strconv.FormatInt(sizeBytes, 10)
}

// ── the passes ──

// importVideoThumbnails carries each local video's poster.
//
// Like actor images it runs under --media-mode=reference, and for the same
// reason: reference mode works for video because Vidra can point at object keys
// the source already has in the shared bucket, and there is no such key for a
// thumbnail on ANY PeerTube configuration. The choice is between fetching it and
// a catalogue of broken images. --media-mode=none is respected — it says "import
// no media", and this is media.
func (im *Importer) importVideoThumbnails(ctx context.Context, r *Report) error {
	thumbs, present, err := im.src.VideoThumbnails(ctx)
	if err != nil {
		return err
	}
	if !present {
		r.Deferred = append(r.Deferred, "video thumbnails (this source has no thumbnail table)")
		return nil
	}
	targets, ok, err := im.resolveVideoImageTargets(ctx, KindThumbnail, "thumbnail", thumbnailRows(thumbs), r)
	if err != nil || !ok || len(targets) == 0 {
		return err
	}
	origin, err := im.sourceOrigin(ctx)
	if err != nil {
		return err
	}
	fetcher := newLazyStaticFetcher(origin, lazyStaticThumbnails)
	return im.runVideoImageTargets(ctx, r, targets, func(ctx context.Context, t videoImageTarget) error {
		return im.importOneThumbnail(ctx, fetcher, t, r)
	})
}

// importStoryboards carries each local video's seek-preview sprite sheet AND
// synthesises the WebVTT map to go with it — PeerTube stores the sheet plus the
// geometry columns and no VTT, while Vidra serves both.
func (im *Importer) importStoryboards(ctx context.Context, r *Report) error {
	boards, present, err := im.src.Storyboards(ctx)
	if err != nil {
		return err
	}
	if !present {
		r.Deferred = append(r.Deferred, "video storyboards (this source has no storyboard table; PeerTube grew them in 6.0)")
		return nil
	}
	// The source video list is read HERE, before any worker starts, because the
	// storyboard's tile count comes from the video's duration and the cache
	// behind it is not built for concurrent readers.
	videos, err := im.sourceVideosByID(ctx)
	if err != nil {
		return err
	}
	targets, ok, err := im.resolveVideoImageTargets(ctx, KindStoryboard, "storyboard", storyboardRows(boards, videos), r)
	if err != nil || !ok || len(targets) == 0 {
		return err
	}
	origin, err := im.sourceOrigin(ctx)
	if err != nil {
		return err
	}
	fetcher := newLazyStaticFetcher(origin, lazyStaticStoryboards)
	return im.runVideoImageTargets(ctx, r, targets, func(ctx context.Context, t videoImageTarget) error {
		return im.importOneStoryboard(ctx, fetcher, t, r)
	})
}

// videoImageRow is one source row of either family reduced to what the shared
// resolution needs: which video it belongs to and which file it names.
type videoImageRow struct {
	id       int64
	videoID  int64
	filename string
	// board and duration are set for the storyboard family: the source's
	// recorded grid geometry, and the length of the video it covers, which
	// together are the whole of the WebVTT map.
	board    SourceStoryboard
	duration int
}

func thumbnailRows(thumbs []SourceVideoThumbnail) []videoImageRow {
	out := make([]videoImageRow, 0, len(thumbs))
	for _, t := range thumbs {
		out = append(out, videoImageRow{id: t.ID, videoID: t.VideoID, filename: t.Filename})
	}
	return out
}

func storyboardRows(boards []SourceStoryboard, videos map[int64]SourceVideo) []videoImageRow {
	out := make([]videoImageRow, 0, len(boards))
	for _, b := range boards {
		out = append(out, videoImageRow{
			id: b.ID, videoID: b.VideoID, filename: b.Filename,
			board: b, duration: videos[b.VideoID].Duration,
		})
	}
	return out
}

// videoImageTarget is one source row resolved against this Vidra instance.
type videoImageTarget struct {
	row      videoImageRow
	kind     string // KindThumbnail / KindStoryboard
	what     string // the word used in notes: "thumbnail" / "storyboard"
	sourceID string
	videoID  uuid.UUID
	action   videoImageAction
}

// note renders the SAFE ledger note for a decision — never a filename, never a
// key, because a ledger note is read by whoever reads the ledger.
func (t videoImageTarget) note() string {
	switch t.action {
	case videoImageWrite:
		return "carried the source " + t.what
	case videoImageReplace:
		return "replaced the " + t.what + " on this instance with the source's"
	case videoImageUpToDate:
		return "the " + t.what + " on this instance is already this source " + t.what
	case videoImageOperatorOwned:
		return "left unchanged: the " + t.what + " on this instance was not written by the import"
	case videoImageCleared:
		return "left unchanged: the " + t.what + " the import wrote was removed on this instance"
	}
	return ""
}

// resolveVideoImageTargets turns one family's source rows into the work this run
// will actually do, recording a ledger row for everything it decides NOT to
// fetch. ok=false means the family was deferred as a whole and the caller should
// stop; every decision that can be made from the database is made here, before a
// single byte moves.
func (im *Importer) resolveVideoImageTargets(
	ctx context.Context,
	kind, what string,
	rows []videoImageRow,
	r *Report,
) ([]videoImageTarget, bool, error) {
	if im.mediaMode == MediaModeNone || im.destMedia == nil {
		r.Deferred = append(r.Deferred, "video "+what+"s (media import is off)")
		return nil, false, nil
	}
	// The origin is only needed when the source's storage cannot answer, but it
	// is resolved up front because it is per-run and free after the first call.
	origin, err := im.sourceOrigin(ctx)
	if err != nil {
		return nil, false, err
	}
	if origin == "" && im.srcMedia == nil {
		// Not a failure of the run: a source with neither a mounted storage tree
		// nor a discoverable public origin cannot be asked for these files, and
		// every other family still imports.
		r.Deferred = append(r.Deferred, "video "+what+"s (no source media root, and the source's own actors carry no absolute URL so its public origin is unknown)")
		return nil, false, nil
	}

	mode := im.importMode()
	c := r.count(kind)
	var targets []videoImageTarget
	for _, row := range rows {
		sid := strconv.FormatInt(row.id, 10)
		// Only 'unsupported' short-circuits, and only because it is a fact about
		// the SOURCE FILE that no amount of re-asking will change. Every other
		// status describes a decision about the SLOT, and the slot is re-read
		// below: a re-run must notice that what it wrote is no longer there.
		if ruled, err := im.ledgerRuledOut(ctx, kind, sid); err != nil {
			return nil, false, err
		} else if ruled {
			c.Skipped++
			continue
		}
		if strings.TrimSpace(row.filename) == "" {
			if err := im.recordStandalone(ctx, kind, sid, uuid.Nil, "unsupported", "source row records no filename"); err != nil {
				return nil, false, err
			}
			c.Unsupported++
			continue
		}
		videoID, ok, err := im.resolveVideoByNumericID(ctx, row.videoID)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			// No terminal row: a video blocked by a conflict today may import
			// tomorrow, and its poster should follow it in. Same rule the other
			// per-video families keep.
			c.Skipped++
			continue
		}

		t := videoImageTarget{row: row, kind: kind, what: what, sourceID: sid, videoID: videoID}
		slot, err := im.videoImageSlotState(ctx, t)
		if err != nil {
			// A storage probe that errors is not a verdict, and guessing one would
			// either clobber a good asset or leave a broken one. The row fails and
			// the next run asks again.
			im.markVideoImageFailed(ctx, t, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: video image slot unreadable",
				"kind", kind, "source_id", sid, "error", err)
			continue
		}
		t.action = decideVideoImage(slot, mode)
		switch t.action {
		case videoImageWrite, videoImageReplace:
			targets = append(targets, t)
		default:
			// Up to date, operator-owned or cleared: none of the three moves a
			// byte. The ledger row still lands, carrying the fingerprint memory
			// forward, so a run that left a slot alone never costs the import the
			// ability to recognise its own earlier write. For the adopted bridge
			// row the memory it carries forward is what is in the slot NOW, which
			// is what retires the key-shape rule after one run.
			applied := slot.carried
			if t.action == videoImageUpToDate {
				applied = slot.current
			}
			if err := im.recordVideoImage(ctx, im.q, t, applied); err != nil {
				return nil, false, err
			}
			c.Skipped++
			if note, ok := t.action.conflict(what, sid); ok {
				r.addConflict(note)
			}
		}
	}
	return targets, true, nil
}

// runVideoImageTargets drives one family's fetch+write at bounded concurrency: a
// few connections to one live production host, not a thundering herd. A per-row
// failure is recorded and the run continues — nobody's migration should stop
// because one poster 404s.
func (im *Importer) runVideoImageTargets(
	ctx context.Context,
	r *Report,
	targets []videoImageTarget,
	do func(context.Context, videoImageTarget) error,
) error {
	im.logger.InfoContext(ctx, "peertube import: carrying video images",
		"kind", targets[0].kind, "images", len(targets), "concurrency", lazyStaticConcurrency)
	var (
		wg   sync.WaitGroup
		work = make(chan videoImageTarget)
	)
	for i := 0; i < lazyStaticConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				err := do(ctx, t)
				if err == nil {
					continue
				}
				im.markVideoImageFailed(ctx, t, safeErr(err))
				im.videoImageCount(r, t.kind, func(c *Counts) { c.Failed++ })
				im.logger.WarnContext(ctx, "peertube import: video image failed",
					"kind", t.kind, "source_id", t.sourceID, "error", err)
			}
		}()
	}
	for _, t := range targets {
		select {
		case work <- t:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(work)
	wg.Wait()
	return nil
}

// videoImageCount mutates a report counter under the pass's own lock, because
// the workers above share the report.
func (im *Importer) videoImageCount(r *Report, kind string, fn func(*Counts)) {
	im.videoImageMu.Lock()
	fn(r.count(kind))
	im.videoImageMu.Unlock()
}

// importOneThumbnail acquires one poster and stores it at Vidra's own key, so
// an imported poster is indistinguishable from a generated one.
func (im *Importer) importOneThumbnail(ctx context.Context, fetcher *lazyStaticFetcher, t videoImageTarget, r *Report) error {
	body, ext, err := im.acquireVideoImage(ctx, fetcher, t, r,
		sourceThumbnailKey(t.row.filename), sourcePreviewKey(t.row.filename))
	if err != nil || body == nil {
		return err
	}
	// The key is always .jpg even for a PNG or WebP poster: that is Vidra's own
	// convention (media.VideoThumbnailKey), and the real format travels on
	// content_type exactly as it does for an uploaded poster.
	key := media.VideoThumbnailKey(t.videoID)
	size, sum, err := storage.PutSizedHashed(ctx, im.destMedia, key, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		// Delete-then-insert, not an upsert: ImportInsertVideoFile has no ON
		// CONFLICT, so without the delete a re-run accumulates duplicate rows.
		// It mirrors the native replace-in-place in video.Service.SetThumbnail.
		if err := q.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{
			VideoID: t.videoID, Kind: "thumbnail",
		}); err != nil {
			return err
		}
		if _, err := q.ImportInsertVideoFile(ctx, sqlcgen.ImportInsertVideoFileParams{
			VideoID:      t.videoID,
			Kind:         "thumbnail",
			StorageKey:   key,
			ContentType:  imageContentTypeForExt(ext),
			OriginalName: "thumbnail" + ext,
			SizeBytes:    size,
			Sha256:       sum,
		}); err != nil {
			return err
		}
		return im.recordVideoImage(ctx, q, t, videoImageFingerprint(key, size))
	}); err != nil {
		return err
	}
	im.videoImageCount(r, t.kind, func(c *Counts) { c.Imported++ })
	return nil
}

// importOneStoryboard acquires one sprite sheet and writes it together with the
// WebVTT map derived from the source's geometry columns.
func (im *Importer) importOneStoryboard(ctx context.Context, fetcher *lazyStaticFetcher, t videoImageTarget, r *Report) error {
	// The plan is built BEFORE the fetch, because a source row whose geometry
	// cannot describe a sheet is unsupported no matter what the bytes are, and
	// there is no reason to ask a live instance for them.
	plan, ok := storyboardPlan(t.row)
	if !ok {
		if err := im.recordStandalone(ctx, t.kind, t.sourceID, uuid.Nil, "unsupported",
			"source storyboard records no usable grid, or its video no duration"); err != nil {
			return err
		}
		im.videoImageCount(r, t.kind, func(c *Counts) { c.Unsupported++ })
		return nil
	}
	body, ext, err := im.acquireVideoImage(ctx, fetcher, t, r, sourceStoryboardKey(t.row.filename))
	if err != nil || body == nil {
		return err
	}
	vtt := []byte(media.RenderStoryboardVTT(plan))

	jpgKey := media.StoryboardKeyJPG(t.videoID)
	vttKey := media.StoryboardKeyVTT(t.videoID)
	spriteSize, spriteSum, err := storage.PutSizedHashed(ctx, im.destMedia, jpgKey, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	vttSize, vttSum, err := storage.PutSizedHashed(ctx, im.destMedia, vttKey, bytes.NewReader(vtt), int64(len(vtt)))
	if err != nil {
		return err
	}
	// Both rows land in ONE transaction. A sprite with no VTT is a
	// has_storyboard:true that renders nothing, which is the same class of lie
	// this whole change is undoing.
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		for _, kind := range []string{"storyboard", "storyboard_vtt"} {
			if err := q.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{
				VideoID: t.videoID, Kind: kind,
			}); err != nil {
				return err
			}
		}
		if _, err := q.ImportInsertVideoFile(ctx, sqlcgen.ImportInsertVideoFileParams{
			VideoID:      t.videoID,
			Kind:         "storyboard",
			StorageKey:   jpgKey,
			ContentType:  imageContentTypeForExt(ext),
			OriginalName: "storyboard.jpg",
			SizeBytes:    spriteSize,
			Sha256:       spriteSum,
		}); err != nil {
			return err
		}
		if _, err := q.ImportInsertVideoFile(ctx, sqlcgen.ImportInsertVideoFileParams{
			VideoID:      t.videoID,
			Kind:         "storyboard_vtt",
			StorageKey:   vttKey,
			ContentType:  "text/vtt",
			OriginalName: "storyboard.vtt",
			SizeBytes:    vttSize,
			Sha256:       vttSum,
		}); err != nil {
			return err
		}
		return im.recordVideoImage(ctx, q, t, videoImageFingerprint(jpgKey, spriteSize))
	}); err != nil {
		return err
	}
	im.videoImageCount(r, t.kind, func(c *Counts) { c.Imported++ })
	return nil
}

// storyboardPlan reconstructs the layout of a source sprite sheet. The real tile
// count comes from the VIDEO's duration and the row's spriteDuration, never from
// the grid: a generator sizes the grid to fit and ffmpeg pads the unused
// trailing cells with black, so cues derived from cols*rows put black frames at
// the end of every scrub bar. See media.PlanFromSprites.
func storyboardPlan(row videoImageRow) (media.StoryboardPlan, bool) {
	b := row.board
	return media.PlanFromSprites(b.TotalWidth, b.TotalHeight, b.SpriteWidth, b.SpriteHeight,
		b.SpriteDuration, row.duration)
}

// acquireVideoImage gets one asset's bytes, preferring the source's own storage
// and falling back to its public HTTP route.
//
//  1. srcMedia, at the PeerTube filesystem key. This is the --source-local-root
//     path, and it is the ONE configuration in which these families were ever
//     correctly readable from storage. Several keys may be tried: an 8.1 source
//     whose admin skipped the manual file-moving script still has its
//     ex-previews under previews/ while the database calls them thumbnails.
//  2. <origin>/lazy-static/…, which is how every other configuration reaches
//     them at all.
//
// A nil body with a nil error means the row was ruled out and already recorded.
func (im *Importer) acquireVideoImage(
	ctx context.Context,
	fetcher *lazyStaticFetcher,
	t videoImageTarget,
	r *Report,
	storageKeys ...string,
) ([]byte, string, error) {
	if im.srcMedia != nil {
		for _, key := range storageKeys {
			body, ext, err := readSourceImage(ctx, im.srcMedia, key)
			if err == nil {
				return body, ext, nil
			}
			// A source object that is present but not an image Vidra stores (or is
			// too big) is a fact about the source, and the HTTP route would serve
			// exactly the same bytes. Rule it out rather than asking twice.
			if errors.Is(err, errNotAnImage) || errors.Is(err, errImageTooLarge) {
				return nil, "", im.ruleOutVideoImage(ctx, t, r, err)
			}
		}
	}
	if fetcher.origin == "" {
		return nil, "", fmt.Errorf("peertubeimport: %s %s is not in the source media root and the source's public origin is unknown", t.what, t.sourceID)
	}
	body, ext, err := fetcher.fetch(ctx, t.row.filename)
	if err != nil {
		if errors.Is(err, errNotAnImage) || errors.Is(err, errImageTooLarge) {
			return nil, "", im.ruleOutVideoImage(ctx, t, r, err)
		}
		return nil, "", err
	}
	return body, ext, nil
}

// ruleOutVideoImage records a TERMINAL verdict about a source file: it cannot be
// an image Vidra stores, or it cannot fit. Recording either one 'unsupported'
// stops the next run asking the same question of a live production instance
// again — the discipline the oversize-avatar incident bought.
func (im *Importer) ruleOutVideoImage(ctx context.Context, t videoImageTarget, r *Report, cause error) error {
	reason := "source did not serve a JPEG, PNG or WebP"
	if errors.Is(cause, errImageTooLarge) {
		reason = fmt.Sprintf("source image is larger than the %d-byte cap this import accepts", int64(maxLazyStaticBytes))
	}
	if err := im.recordStandalone(ctx, t.kind, t.sourceID, uuid.Nil, "unsupported", reason); err != nil {
		return err
	}
	im.videoImageCount(r, t.kind, func(c *Counts) { c.Unsupported++ })
	return nil
}

// readSourceImage reads one object out of the source's media root under exactly
// the gates the HTTP path applies: the same size cap, the same sniff, and the
// same rule that the extension comes from what the bytes ARE.
func readSourceImage(ctx context.Context, b storage.Backend, key string) ([]byte, string, error) {
	rc, err := b.Open(ctx, key)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, maxLazyStaticBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxLazyStaticBytes {
		return nil, "", fmt.Errorf("%w of %d bytes", errImageTooLarge, int64(maxLazyStaticBytes))
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("%w: empty object", errNotAnImage)
	}
	ext := imageExtForSniffedType(sniffContentType(body))
	if ext == "" {
		return nil, "", fmt.Errorf("%w: sniffed %q", errNotAnImage, sniffContentType(body))
	}
	return body, ext, nil
}

// ── ledger + slot state ──

// ledgerRuledOut reports whether the ledger already records this source FILE as
// one that cannot be carried.
func (im *Importer) ledgerRuledOut(ctx context.Context, kind, sourceID string) (bool, error) {
	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{EntityKind: kind, SourceID: sourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.Status == "unsupported", nil
}

// videoImageSlotState gathers this instance's side of the decision for one slot:
// what row is in it, whether its bytes are really there, and what the import
// remembers putting there.
func (im *Importer) videoImageSlotState(ctx context.Context, t videoImageTarget) (videoImageSlot, error) {
	fileKind, nativeKey := "thumbnail", media.VideoThumbnailKey(t.videoID)
	if t.kind == KindStoryboard {
		fileKind, nativeKey = "storyboard", media.StoryboardKeyJPG(t.videoID)
	}
	var s videoImageSlot
	cur, err := im.q.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: t.videoID, Kind: fileKind})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, err
	}
	if err == nil {
		s.present = true
		s.current = videoImageFingerprint(cur.StorageKey, cur.SizeBytes)
		s.nativeKey = cur.StorageKey == nativeKey
	}

	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{
		EntityKind: t.kind, SourceID: t.sourceID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, err
	}
	s.carriedThisFile = err == nil && row.Status == "done"

	last, lerr := im.q.GetImportLedgerLastWriteForTarget(ctx, sqlcgen.GetImportLedgerLastWriteForTargetParams{
		EntityKind: t.kind, VidraID: optUUID(t.videoID),
	})
	if lerr != nil && !errors.Is(lerr, pgx.ErrNoRows) {
		return s, lerr
	}
	if lerr == nil {
		s.carried, s.wroteBefore = last.AppliedValue, true
		// video_files rows are DELETED and re-inserted on every replacement, so
		// created_at is the write time and the comparison is the same one actor
		// images make: an asset NEWER than the ledger row was put there by
		// somebody else.
		s.untouched = s.present && !cur.CreatedAt.After(last.UpdatedAt)
	}

	// The object probe is the expensive half of this decision — one HEAD per
	// video against a remote store, ~15k of them on a real catalogue — so it is
	// only paid where it can change the answer. A slot the ledger already
	// accounts for was written by a run that verified its own write, and is
	// settled from the database alone.
	if !s.present || s.carried != "" || s.wroteBefore {
		s.bytesPresent = s.present
		return s, nil
	}
	ok, err := im.destMedia.Exists(ctx, cur.StorageKey)
	if err != nil {
		return s, err
	}
	s.bytesPresent = ok
	return s, nil
}

// recordVideoImage upserts a slot's ledger row, preserving the fingerprint
// memory it is given.
//
// 'up to date' is 'done' and not 'skipped', which is not cosmetic: the status is
// how the NEXT run knows this file is the one already in the slot, and
// downgrading it would make every later run re-fetch an asset it already has.
func (im *Importer) recordVideoImage(ctx context.Context, q *sqlcgen.Queries, t videoImageTarget, applied string) error {
	status := "skipped"
	switch t.action {
	case videoImageWrite, videoImageReplace, videoImageUpToDate:
		status = "done"
	}
	return q.UpsertImportLedgerApplied(ctx, sqlcgen.UpsertImportLedgerAppliedParams{
		EntityKind:   t.kind,
		SourceID:     t.sourceID,
		VidraID:      optUUID(t.videoID),
		Status:       status,
		Note:         t.note(),
		AppliedValue: applied,
	})
}

// markVideoImageFailed records a per-row failure WITHOUT downgrading a row that
// already records a completed write — the same rule markActorImageFailed keeps,
// and for the same reason: a completed row IS the slot's ownership memory, and
// stamping 'failed' over it would leave the next run reading a creator's poster
// where there is none and refusing to touch it forever.
func (im *Importer) markVideoImageFailed(ctx context.Context, t videoImageTarget, note string) {
	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{
		EntityKind: t.kind, SourceID: t.sourceID,
	})
	if err == nil && row.Status == "done" {
		return
	}
	im.markFailed(ctx, t.kind, t.sourceID, note)
}

// ── the dry run ──

// planVideoThumbnails / planStoryboards add the two families to a --dry-run
// plan: one entry per source row, which is already one row per video. Like
// planActorImages they deliberately stop there and do not run the ownership
// decision — a plan is taken before the videos exist, so every slot would
// resolve to "no video yet" and the plan would report a migration that carries
// no posters at all.
func (im *Importer) planVideoThumbnails(ctx context.Context, r *Report) error {
	thumbs, present, err := im.src.VideoThumbnails(ctx)
	if err != nil {
		return err
	}
	if !present {
		r.Deferred = append(r.Deferred, "video thumbnails (this source has no thumbnail table)")
		return nil
	}
	r.count(KindThumbnail).Planned = len(thumbs)
	return im.planVideoImageReachable(ctx, r, "thumbnail")
}

func (im *Importer) planStoryboards(ctx context.Context, r *Report) error {
	boards, present, err := im.src.Storyboards(ctx)
	if err != nil {
		return err
	}
	if !present {
		r.Deferred = append(r.Deferred, "video storyboards (this source has no storyboard table; PeerTube grew them in 6.0)")
		return nil
	}
	r.count(KindStoryboard).Planned = len(boards)
	return im.planVideoImageReachable(ctx, r, "storyboard")
}

// planVideoImageReachable adds the same deferral notes the pass itself would, so
// a rehearsal says up front that a family will not be carried.
func (im *Importer) planVideoImageReachable(ctx context.Context, r *Report, what string) error {
	if im.mediaMode == MediaModeNone {
		r.Deferred = append(r.Deferred, "video "+what+"s (media import is off)")
		return nil
	}
	origin, err := im.sourceOrigin(ctx)
	if err != nil {
		return err
	}
	if origin == "" && im.srcMedia == nil {
		r.Deferred = append(r.Deferred, "video "+what+"s (no source media root, and the source's own actors carry no absolute URL so its public origin is unknown)")
	}
	return nil
}
