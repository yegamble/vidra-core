package peertubeimport

import (
	"context"
	"strconv"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// importCaptions carries the captions importOneVideo could not: the ones added
// on the source AFTER a video's first import. importOneVideo writes a video's
// captions in the video's own transaction and never runs again once the ledger
// row is terminal, so against a still-live source every later subtitle track was
// lost. This is a pass of its own, with its own ledger kind, for the same reason
// the per-video families are (entities_pervideo.go).
//
// It only ever FILLS, and the write is what enforces that (ImportFillCaption is
// ON CONFLICT DO NOTHING). A language the video already has a track for is
// recorded done and left alone: that row may be the one importOneVideo wrote a
// moment ago, or a catalogue migrated by an older release — which has the
// captions and no caption ledger — or a track the creator has since replaced.
// The ledger row is also what makes a DELETION here stick: once a source caption
// is recorded, a creator who removes it is never handed it back.
//
// Accepted residual: a caption deleted here BEFORE the first run of this pass
// has left no evidence, and is carried again once. After that the row above
// stands guard.
func (im *Importer) importCaptions(ctx context.Context, r *Report) error {
	if im.mediaMode == MediaModeNone || (im.mediaMode == MediaModeCopy && (im.srcMedia == nil || im.destMedia == nil)) {
		return nil
	}
	captions, err := im.src.AllCaptions(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindCaption)
	for _, capt := range captions {
		sid := strconv.FormatInt(capt.ID, 10)
		if _, _, done, err := im.alreadyProcessed(ctx, KindCaption, sid); err != nil {
			return err
		} else if done {
			c.Skipped++
			continue
		}
		if err := im.importOneCaption(ctx, capt, sid, c); err != nil {
			im.markFailed(ctx, KindCaption, sid, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: caption failed", "source_id", sid, "error", err)
		}
	}
	return nil
}

func (im *Importer) importOneCaption(ctx context.Context, capt SourceCaption, sid string, c *Counts) error {
	// Not in the ledger (a remote video's caption, or a video still waiting on its
	// channel): no row, so it is asked again next run.
	videoID, ok, err := im.resolveVideoByNumericID(ctx, capt.VideoID)
	if err != nil || !ok {
		return err
	}
	if !allowedCaptionExt[extOf(capt.Filename)] {
		c.Unsupported++
		return im.recordStandalone(ctx, KindCaption, sid, videoID, "unsupported", "caption file type is not carried")
	}
	present := "a caption in this language was already here"
	if exists, err := im.q.ImportCaptionExists(ctx, sqlcgen.ImportCaptionExistsParams{VideoID: videoID, Language: capt.Language}); err != nil {
		return err
	} else if exists {
		c.Skipped++
		return im.recordStandalone(ctx, KindCaption, sid, videoID, "done", present)
	}
	key := sourceCaptionKey(capt.Filename)
	if im.mediaMode == MediaModeCopy {
		// Keyed by the SOURCE row, so a retried copy overwrites itself — and never
		// captions/<video>/<lang>.vtt, which is where a caption uploaded here lives
		// (video.captionKey): a creator's upload racing this copy must not have its
		// object replaced underneath it.
		key = "captions/" + videoID.String() + "/import-" + sid + ".vtt"
		if _, _, err := im.copyMedia(ctx, sourceCaptionKey(capt.Filename), key); err != nil {
			return err
		}
	}
	var filled int64
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) (err error) {
		if filled, err = q.ImportFillCaption(ctx, sqlcgen.ImportFillCaptionParams{
			VideoID: videoID, Language: capt.Language, StorageKey: key,
		}); err != nil {
			return err
		}
		note := ""
		if filled == 0 {
			note = present
		}
		return recordLedger(ctx, q, KindCaption, sid, videoID, "done", note)
	}); err != nil {
		return err
	}
	if filled == 0 {
		c.Skipped++
		return nil
	}
	c.Imported++
	return nil
}
