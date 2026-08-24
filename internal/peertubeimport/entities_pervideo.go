package peertubeimport

import (
	"context"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// This file carries the per-video data that hangs off a video rather than being
// part of its insert: view totals, chapters, ratings and HLS ladder rungs.
//
// They are SEPARATE PASSES, not extra work inside importOneVideo, and that is
// deliberate. importOneVideo never runs again for a video with a terminal ledger
// row, so anything folded into it can only ever reach videos imported after the
// code shipped. An instance whose 14,766 videos were imported by an earlier
// release would get none of this. A pass of its own, keyed by its own ledger
// rows, BACKFILLS onto videos that are already there — which is also what makes
// the tool usable the way the operator actually uses it: repeatedly, on a
// schedule, against a source that keeps moving until cutover.
//
// A parent video that is not (or not yet) imported is passed over WITHOUT a
// terminal ledger row here. The older families record "skipped: video not
// imported" and never look again, which is right for a comment — it is its own
// entity, and the decision not to import it is final. It is wrong for data
// derived from a video: if the operator resolves the conflict that blocked the
// video and re-runs, its chapters and views should follow it in.

// ── view counts ──

// importViewCounts carries each source video's lifetime view total onto Vidra's
// counter.
//
// THE MAPPING DECISION, stated once: the source has ONE number per video and no
// daily breakdown behind it. Vidra stores both a lifetime total
// (video_view_counts) and a per-UTC-day rollup (video_view_days) that feeds the
// creator statistics chart. The total is carried. The day rollup is NOT written
// at all — not one backfilled bucket, not a spread across the video's lifetime.
// Either would invent a shape of data the source never had: a single bucket
// claims 3.17 million views happened on one calendar day, and a spread claims a
// per-day history nobody measured. Migration 0046 already established what an
// absent day row means ("days before this migration simply have no rows and
// render as zero"), so an imported video's chart reads as no daily data before
// the import and real daily data after it — which is exactly the truth.
//
// The write is a DELTA, never an assignment: see ImportApplyVideoViewDelta and
// migration 0112 for why, and TestImportViewCountsAreDeltaNotDouble for the
// proof that a second run adds nothing.
func (im *Importer) importViewCounts(ctx context.Context, r *Report) error {
	videos, err := im.src.Videos(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindViewCount)
	for _, v := range videos {
		if v.Views <= 0 {
			continue // nothing to carry; not a skip, there is no data here
		}
		if err := im.importOneViewCount(ctx, v, c); err != nil {
			im.markFailed(ctx, KindViewCount, v.UUID, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: view count failed", "source_uuid", v.UUID, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOneViewCount(ctx context.Context, v SourceVideo, c *Counts) error {
	videoID, ok, err := im.resolveParent(ctx, KindVideo, v.UUID)
	if err != nil {
		return err
	}
	if !ok {
		c.Skipped++
		return nil
	}
	applied, _, err := im.ledgerCounter(ctx, KindViewCount, v.UUID)
	if err != nil {
		return err
	}
	delta := v.Views - applied
	if delta == 0 {
		// The source total has not moved since the last run. Writing anything at
		// all here — even the same number — is what would make a scheduled import
		// drift, so the run does nothing and reports it as a skip.
		c.Skipped++
		return nil
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ImportApplyVideoViewDelta(ctx, sqlcgen.ImportApplyVideoViewDeltaParams{
			VideoID: videoID,
			Delta:   delta,
		}); err != nil {
			return err
		}
		return q.UpsertImportLedgerCounter(ctx, sqlcgen.UpsertImportLedgerCounterParams{
			EntityKind:  KindViewCount,
			SourceID:    v.UUID,
			VidraID:     optUUID(videoID),
			Status:      "done",
			Note:        "",
			SourceValue: v.Views,
		})
	}); err != nil {
		return err
	}
	c.Imported++
	return nil
}

// ledgerCounter reads how much of a counter has already been applied for a
// source entity. found=false (and 0) when there is no ledger row yet, which is
// the same starting point as "applied nothing".
func (im *Importer) ledgerCounter(ctx context.Context, kind, sourceID string) (int64, bool, error) {
	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{EntityKind: kind, SourceID: sourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return row.SourceValue, true, nil
}

// ── chapters ──

// importChapters carries the seek-bar chapter marks. videos.has_chapters needs
// no write: the detail response derives it with an EXISTS over video_chapters,
// so a video's flag flips the moment its first chapter row lands.
func (im *Importer) importChapters(ctx context.Context, r *Report) error {
	chapters, present, err := im.src.Chapters(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindChapter)
	if !present {
		return nil
	}
	for _, ch := range chapters {
		sid := strconv.FormatInt(ch.ID, 10)
		if _, _, done, err := im.alreadyProcessed(ctx, KindChapter, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOneChapter(ctx, ch, sid, c); err != nil {
			im.markFailed(ctx, KindChapter, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: chapter failed", "source_id", sid, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOneChapter(ctx context.Context, ch SourceChapter, sid string, c *Counts) error {
	videoID, ok, err := im.resolveVideoByNumericID(ctx, ch.VideoID)
	if err != nil {
		return err
	}
	if !ok {
		c.Skipped++
		return nil
	}
	title := normalizeChapterTitle(ch.Title)
	if title == "" || ch.Timecode < 0 {
		// video_chapters CHECKs a non-empty title and a non-negative start. A source
		// row that cannot satisfy them is recorded as unsupported so it is visible
		// in the ledger rather than silently absent.
		if err := im.recordStandalone(ctx, KindChapter, sid, uuid.Nil, "unsupported", "chapter has no usable title or start"); err != nil {
			return err
		}
		c.Unsupported++
		return nil
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ImportInsertVideoChapter(ctx, sqlcgen.ImportInsertVideoChapterParams{
			VideoID:      videoID,
			StartSeconds: int32(ch.Timecode),
			Title:        title,
		}); err != nil {
			return err
		}
		return recordLedger(ctx, q, KindChapter, sid, videoID, "done", "")
	}); err != nil {
		return err
	}
	c.Imported++
	return nil
}

// ── ratings ──

// importRatings carries likes and dislikes. Only ratings cast by a LOCAL account
// on a LOCAL video are carried: video_ratings is keyed by a Vidra user, and a
// remote account has none.
func (im *Importer) importRatings(ctx context.Context, r *Report) error {
	ratings, present, err := im.src.Ratings(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindRating)
	if !present {
		return nil
	}
	for _, rate := range ratings {
		sid := strconv.FormatInt(rate.ID, 10)
		if _, _, done, err := im.alreadyProcessed(ctx, KindRating, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOneRating(ctx, rate, sid, c); err != nil {
			im.markFailed(ctx, KindRating, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: rating failed", "source_id", sid, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOneRating(ctx context.Context, rate SourceRating, sid string, c *Counts) error {
	value, ok := mapRating(rate.Type)
	if !ok {
		if err := im.recordStandalone(ctx, KindRating, sid, uuid.Nil, "unsupported", "rating is neither like nor dislike"); err != nil {
			return err
		}
		c.Unsupported++
		return nil
	}
	videoID, ok, err := im.resolveVideoByNumericID(ctx, rate.VideoID)
	if err != nil {
		return err
	}
	if !ok {
		c.Skipped++
		return nil
	}
	userID, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(rate.RaterUser, 10))
	if err != nil {
		return err
	}
	if !ok {
		c.Skipped++
		return nil
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ImportInsertVideoRating(ctx, sqlcgen.ImportInsertVideoRatingParams{
			VideoID:   videoID,
			UserID:    userID,
			Rating:    value,
			CreatedAt: rate.CreatedAt,
		}); err != nil {
			return err
		}
		return recordLedger(ctx, q, KindRating, sid, videoID, "done", "")
	}); err != nil {
		return err
	}
	c.Imported++
	return nil
}

// ── HLS ladder rungs ──

// importRenditions records one video_renditions row per rung of an imported HLS
// tree. Without them an imported video plays a full quality ladder — hls.js
// reads the levels out of the master playlist itself — while the API reports
// `renditions: []` and the quality menu renders empty.
//
// Rungs are only carried for a video that HAS a ready HLS tree recorded in
// Vidra. In copy mode the media is re-transcoded by Vidra, and that transcode
// writes its own rendition rows against its own key prefixes; advertising the
// source's ladder for a tree Vidra is about to replace would be a claim about
// files that are not there.
func (im *Importer) importRenditions(ctx context.Context, r *Report) error {
	rungs, err := im.src.Renditions(ctx)
	if err != nil {
		return err
	}
	videos, err := im.sourceVideosByID(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindRendition)
	// One readiness lookup per VIDEO, not per rung.
	ready := map[uuid.UUID]bool{}
	for _, rung := range rungs {
		sid := strconv.FormatInt(rung.FileID, 10)
		if _, _, done, err := im.alreadyProcessed(ctx, KindRendition, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOneRendition(ctx, rung, sid, videos, ready, c); err != nil {
			im.markFailed(ctx, KindRendition, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: rendition failed", "source_id", sid, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOneRendition(
	ctx context.Context,
	rung SourceRendition,
	sid string,
	videos map[int64]SourceVideo,
	ready map[uuid.UUID]bool,
	c *Counts,
) error {
	if rung.Resolution <= 0 && rung.Height <= 0 {
		// PeerTube's resolution 0 is the audio-only rung. It is not a rung of the
		// quality ladder and video_renditions CHECKs height > 0.
		if err := im.recordStandalone(ctx, KindRendition, sid, uuid.Nil, "unsupported", "audio-only rendition has no height"); err != nil {
			return err
		}
		c.Unsupported++
		return nil
	}
	videoID, ok, err := im.resolveVideoByNumericID(ctx, rung.VideoID)
	if err != nil {
		return err
	}
	if !ok {
		c.Skipped++
		return nil
	}
	isReady, seen := ready[videoID]
	if !seen {
		isReady, err = im.q.ImportVideoHasReadyPlaylist(ctx, videoID)
		if err != nil {
			return err
		}
		ready[videoID] = isReady
	}
	if !isReady {
		c.Skipped++
		return nil
	}

	height := renditionHeight(rung.Resolution, rung.Height)
	width := renditionWidth(height, rung.Width, videos[rung.VideoID].AspectRatio)
	if height <= 0 || width <= 0 {
		if err := im.recordStandalone(ctx, KindRendition, sid, uuid.Nil, "unsupported", "rendition has no usable dimensions"); err != nil {
			return err
		}
		c.Unsupported++
		return nil
	}
	size := rung.Size
	if size < 0 {
		size = 0
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ImportInsertVideoRendition(ctx, sqlcgen.ImportInsertVideoRenditionParams{
			VideoID:   videoID,
			Height:    int32(height),
			Width:     int32(width),
			KeyPrefix: sourceHLSDir(rung.VideoUUID),
			SizeBytes: size,
		}); err != nil {
			return err
		}
		return recordLedger(ctx, q, KindRendition, sid, videoID, "done", "")
	}); err != nil {
		return err
	}
	c.Imported++
	return nil
}

// planPerVideo adds the four per-video families to a dry-run plan. The counts are
// ROWS THE IMPORT WOULD TOUCH, and for view counts that means videos carrying a
// total — one per video, never a number of views.
func (im *Importer) planPerVideo(ctx context.Context, r *Report, videos []SourceVideo) error {
	for _, v := range videos {
		if v.Views > 0 {
			r.count(KindViewCount).Planned++
		}
	}
	chapters, present, err := im.src.Chapters(ctx)
	if err != nil {
		return err
	}
	if present {
		r.count(KindChapter).Planned = len(chapters)
	} else {
		r.Deferred = append(r.Deferred, "chapters (this source has no videoChapter table)")
	}
	ratings, present, err := im.src.Ratings(ctx)
	if err != nil {
		return err
	}
	if present {
		r.count(KindRating).Planned = len(ratings)
	} else {
		r.Deferred = append(r.Deferred, "ratings (this source has no accountVideoRate table)")
	}
	rungs, err := im.src.Renditions(ctx)
	if err != nil {
		return err
	}
	for _, rung := range rungs {
		if rung.Resolution > 0 || rung.Height > 0 {
			r.count(KindRendition).Planned++
		}
	}
	return nil
}
