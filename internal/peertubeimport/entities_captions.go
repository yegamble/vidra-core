package peertubeimport

import (
	"context"
	"strconv"
	"sync"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// importCaptions carries copy-mode captions independently of video creation,
// and fills tracks added after an earlier import in either media mode. Missing
// tracks remain failed in their own ledger without blocking a video's HLS.
//
// It only ever FILLS, and the write is what enforces that (ImportFillCaption is
// ON CONFLICT DO NOTHING). A language the video already has a track for is
// recorded done and left alone: that row may be the one importOneVideo wrote a
// moment ago, or a catalogue migrated by an older release — which has the
// captions and no caption ledger — or a track the creator has since replaced.
// The ledger row is also what makes a DELETION here stick: once a source caption
// is recorded, a creator who removes it is never handed it back.
//
// Reference-mode importOneVideo records tracks in the video's transaction, so
// that guarantee does not depend on this pass having run in reference mode.
//
// Accepted residual: a track an OLDER release carried has no ledger row until
// this pass first records it. One deleted here before that — or whose first
// recording failed and is being retried — has left no evidence, and is carried
// once more.
func (im *Importer) importCaptions(ctx context.Context, r *Report) error {
	if im.mediaMode == MediaModeNone || (im.mediaMode == MediaModeCopy && (im.srcMedia == nil || im.destMedia == nil)) {
		return nil
	}
	captions, err := im.src.AllCaptions(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindCaption)
	type target struct {
		caption SourceCaption
		sid     string
		videoID uuid.UUID
	}
	var targets []target
	for _, capt := range captions {
		sid := strconv.FormatInt(capt.ID, 10)
		if _, _, done, err := im.alreadyProcessed(ctx, KindCaption, sid); err != nil {
			return err
		} else if done {
			if _, justCarried := im.captionsThisRun[sid]; !justCarried {
				c.Skipped++
			}
			continue
		}
		// Resolve parents before fan-out: the per-run parent/video caches are
		// deliberately sequential. A failed/deleted parent stays retryable.
		videoID, ok, err := im.resolveVideoByNumericID(ctx, capt.VideoID)
		if err != nil {
			return err
		}
		if ok {
			targets = append(targets, target{capt, sid, videoID})
		}
	}
	workers := 1 // Preserve reference-mode ordering; it makes no blob transfers.
	if im.mediaMode == MediaModeCopy {
		workers = 4
	}
	work := make(chan target)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				counts := &Counts{}
				if err := im.importOneCaption(ctx, t.caption, t.sid, t.videoID, counts); err != nil {
					im.markFailed(ctx, KindCaption, t.sid, safeErr(err))
					counts.Failed++
					im.logger.WarnContext(ctx, "peertube import: caption failed", "source_id", t.sid, "error", err)
				}
				mu.Lock()
				c.Imported += counts.Imported
				c.Skipped += counts.Skipped
				c.Failed += counts.Failed
				c.Unsupported += counts.Unsupported
				r.publishProgress()
				mu.Unlock()
			}
		}()
	}
enqueue:
	for _, t := range targets {
		select {
		case work <- t:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(work)
	wg.Wait()
	return ctx.Err()
}

func (im *Importer) importOneCaption(ctx context.Context, capt SourceCaption, sid string, videoID uuid.UUID, c *Counts) error {
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
