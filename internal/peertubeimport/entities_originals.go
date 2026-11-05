package peertubeimport

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var errOriginalTypeNotCarried = errors.New("peertubeimport: retained original file type is not carried")

// carriedOriginal is the video_files 'original' row a source web video becomes.
type carriedOriginal struct {
	key, contentType, name, sha string
	size                        int64
}

// carryOriginal is the one place a source web-video file becomes an original:
// copied (copy mode) or referenced (reference mode). Both the video pass and
// the backfill pass below call it, so they cannot disagree about what counts.
func (im *Importer) carryOriginal(ctx context.Context, v SourceVideo, files []SourceVideoFile) (carriedOriginal, bool, error) {
	var (
		o    carriedOriginal
		have bool
	)
	// Media keys are opaque, unique object paths (stored verbatim in
	// video_files.storage_key / captions.storage_key and served from there), so a
	// fresh random id is fine and avoids any dependence on the row's own id.
	mediaID := uuid.New()
	if len(files) > 0 && im.srcMedia != nil && im.destMedia != nil {
		f := files[0]
		if !allowedVideoExt[strings.ToLower(f.Extname)] {
			if f.KeptOriginal {
				return carriedOriginal{}, false, errOriginalTypeNotCarried
			}
			im.logger.WarnContext(ctx, "peertube import: skipping video file with disallowed extension", "source_uuid", v.UUID)
		} else {
			switch im.mediaMode {
			case MediaModeCopy:
				o.key = "web-videos/" + mediaID.String() + f.Extname
				size, sum, cerr := im.copyMedia(ctx, f.sourceKey(), o.key)
				if cerr != nil {
					return carriedOriginal{}, false, cerr
				}
				o.size = size
				o.sha = sum
				o.contentType = contentTypeForExt(f.Extname)
				o.name = f.Filename
				if f.OriginalName != "" {
					o.name = f.OriginalName
				}
				have = true
			case MediaModeReference:
				o.key = f.sourceKey()
				o.size = f.Size
				o.contentType = contentTypeForExt(f.Extname)
				o.name = f.Filename
				have = true
			}
		}
	}
	if len(files) > 0 && im.mediaMode == MediaModeReference && !have {
		f := files[0]
		if allowedVideoExt[strings.ToLower(f.Extname)] {
			o.key = f.sourceKey()
			o.size = f.Size
			o.contentType = contentTypeForExt(f.Extname)
			o.name = f.Filename
			have = true
		}
	}
	return o, have, nil
}

// importOriginalBackfill carries the web-video original onto videos an EARLIER
// run imported without one — a metadata-only run, or one whose source store was
// unreachable. importOneVideo only carries it on a video's first import, and the
// video ledger then says done, so without this pass that file is never copied.
//
// It only fills: a video that already has an original (carried a moment ago,
// uploaded here, or replaced by its creator) is recorded done and left alone,
// and that ledger row is what keeps a later deletion here from being undone.
func (im *Importer) importOriginalBackfill(ctx context.Context, r *Report) error {
	if !im.carriesMedia() {
		return nil
	}
	videos, err := im.src.Videos(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindVideoFile)
	for _, v := range videos {
		if _, _, done, err := im.alreadyProcessed(ctx, KindVideoFile, v.UUID); err != nil {
			return err
		} else if done {
			continue
		}
		id, ok, err := im.resolveParent(ctx, KindVideo, v.UUID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := im.backfillOneOriginal(ctx, v, id, c); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			im.markFailed(ctx, KindVideoFile, v.UUID, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: original backfill failed", "source_uuid", v.UUID, "error", err)
		}
		r.publishProgress()
	}
	return nil
}

func (im *Importer) backfillOneOriginal(ctx context.Context, v SourceVideo, videoID uuid.UUID, c *Counts) error {
	if _, err := im.q.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: videoID, Kind: "original"}); err == nil {
		return im.recordStandalone(ctx, KindVideoFile, v.UUID, videoID, "done", "an original was already here")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	files, err := im.src.VideoFiles(ctx, v.ID)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return im.recordStandalone(ctx, KindVideoFile, v.UUID, videoID, "done", "the source has no web-video file")
	}
	if !playableFile(files) {
		c.Unsupported++
		return im.recordStandalone(ctx, KindVideoFile, v.UUID, videoID, "unsupported", "web-video file type is not carried")
	}
	o, have, err := im.carryOriginal(ctx, v, files)
	if err != nil {
		return err
	}
	if !have {
		// No source store this run: nothing recorded, so a later run retries.
		return nil
	}
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if _, err := q.ImportInsertVideoFile(ctx, sqlcgen.ImportInsertVideoFileParams{
			VideoID: videoID, Kind: "original", StorageKey: o.key,
			ContentType: o.contentType, OriginalName: o.name, SizeBytes: o.size, Sha256: o.sha,
		}); err != nil {
			return err
		}
		return recordLedger(ctx, q, KindVideoFile, v.UUID, videoID, "done", "backfilled onto an earlier import")
	}); err != nil {
		return err
	}
	c.Imported++
	return nil
}
