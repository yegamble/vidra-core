package peertubeimport

import (
	"context"
	"fmt"
	"path"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type hlsCopyTarget struct {
	sourceID, prefix, master string
	videoID                  uuid.UUID
}

type hlsCopyOutcome int

const (
	hlsCopied hlsCopyOutcome = iota
	hlsCopyFailed
	hlsCopySkipped
)

// Workers never touch the parent caches. Zero retains four trees for callers
// without an override; the upper bound also caps streaming fallback buffers.
func runHLSCopyWorkers(ctx context.Context, targets []hlsCopyTarget, copyOne func(context.Context, hlsCopyTarget) (hlsCopyOutcome, error)) (Counts, error) {
	return runHLSCopyWorkersWithProgress(ctx, targets, copyOne, nil, 0)
}

// Progress is serialized with aggregation, after the target ledger commits.
func runHLSCopyWorkersWithProgress(ctx context.Context, targets []hlsCopyTarget, copyOne func(context.Context, hlsCopyTarget) (hlsCopyOutcome, error), progress func(Counts), concurrency int) (Counts, error) {
	if concurrency <= 0 {
		concurrency = 4
	}
	concurrency = min(concurrency, 32)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	var counts Counts
	var firstErr error
	next := 0
	for range min(concurrency, len(targets)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if ctx.Err() != nil || next == len(targets) {
					mu.Unlock()
					return
				}
				target := targets[next]
				next++
				mu.Unlock()
				outcome, err := copyOne(ctx, target)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
						cancel()
					}
				} else {
					switch outcome {
					case hlsCopied:
						counts.Imported++
					case hlsCopyFailed:
						counts.Failed++
					case hlsCopySkipped:
						counts.Skipped++
					}
					if progress != nil {
						progress(counts)
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return counts, firstErr
	}
	return counts, ctx.Err()
}

// A separate pass repairs old imports too. A ready playlist is the durable
// checkpoint and is never replaced (including a ladder generated on Vidra).
func (im *Importer) importHLSCopies(ctx context.Context, r *Report) error {
	if im.mediaMode == MediaModeReference {
		return im.importLateHLSReferences(ctx, r)
	}
	if im.mediaMode != MediaModeCopy || im.srcMedia == nil || im.destMedia == nil {
		return nil
	}
	videos, err := im.src.Videos(ctx)
	if err != nil {
		return err
	}
	// Parent resolution mutates per-run maps. Resolve the entire work list
	// before workers start; only immutable targets cross the worker boundary.
	var targets []hlsCopyTarget
	for _, v := range videos {
		id, ok, err := im.resolveParent(ctx, KindVideo, v.UUID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ready, err := im.q.ImportVideoHasReadyPlaylist(ctx, id)
		if err != nil {
			return err
		}
		if ready {
			continue
		}
		hls, ok, err := im.src.HLSPlaylist(ctx, v.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		targets = append(targets, hlsCopyTarget{sourceID: v.UUID, videoID: id, prefix: sourceHLSDir(v.UUID), master: path.Base(hls.PlaylistFilename)})
	}
	im.copyMediaDisabled.Store(false)
	c := r.count(KindHLSPlaylist)
	initial := *c
	_, err = runHLSCopyWorkersWithProgress(ctx, targets, im.copyHLSTarget, func(counts Counts) {
		c.Imported = initial.Imported + counts.Imported
		c.Failed = initial.Failed + counts.Failed
		c.Skipped = initial.Skipped + counts.Skipped
		r.publishProgress()
	}, im.copyConcurrency)
	return err
}

func (im *Importer) copyHLSTarget(ctx context.Context, target hlsCopyTarget) (hlsCopyOutcome, error) {
	if err := im.copyHLSTree(ctx, target.prefix, target.master); err != nil {
		if ctx.Err() != nil {
			return hlsCopyFailed, ctx.Err()
		}
		return hlsCopyFailed, recordLedger(ctx, im.q, KindHLSPlaylist, target.sourceID, uuid.Nil, "failed", "HLS copy incomplete; rerun required")
	}
	var published int64
	err := im.withTx(ctx, func(q *sqlcgen.Queries) (err error) {
		published, err = q.ImportPublishCopiedHLSPlaylist(ctx, sqlcgen.ImportPublishCopiedHLSPlaylistParams{VideoID: target.videoID, MasterKey: target.prefix + "/" + target.master})
		if err != nil || published == 0 {
			return err
		}
		return recordLedger(ctx, q, KindHLSPlaylist, target.sourceID, target.videoID, "done", "")
	})
	if err == nil && published == 0 {
		return hlsCopySkipped, nil
	}
	return hlsCopied, err
}

// importLateHLSReferences is the reference-mode half of the same repair. A video
// read while the source was still transcoding it lands with no playlist, and its
// ledger row is terminal from then on — importOneVideo never sees it again — so
// the playlist the source finished an hour later had no way in. Against a live
// source that is every video uploaded shortly before a scheduled run, and under
// --source-authoritative its state still followed the source to 'published' with
// nothing to play.
//
// It only ever FILLS: a video with any streaming-playlist row, ready or not, is
// Vidra's own pipeline's business. The destination is asked once and the source
// once, so a re-run over a healthy catalogue stays a no-op re-run.
func (im *Importer) importLateHLSReferences(ctx context.Context, r *Report) error {
	lacking, err := im.q.ImportListVideosWithoutPlaylist(ctx)
	if err != nil || len(lacking) == 0 {
		return err
	}
	vidraByUUID := make(map[string]sqlcgen.ImportListVideosWithoutPlaylistRow, len(lacking))
	for _, row := range lacking {
		vidraByUUID[row.SourceID] = row
	}
	videos, err := im.sourceVideosByID(ctx)
	if err != nil {
		return err
	}
	var ids []int64
	for id, v := range videos {
		if _, ok := vidraByUUID[v.UUID]; ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	playlists, err := im.src.HLSPlaylists(ctx, ids)
	if err != nil {
		return err
	}
	c := r.count(KindHLSPlaylist)
	stillDraft := 0
	for _, id := range ids {
		hls, ok := playlists[id]
		if !ok {
			continue // still nothing on the source; asked again next run
		}
		v := videos[id]
		row := vidraByUUID[v.UUID]
		var filled int64
		if err := im.withTx(ctx, func(q *sqlcgen.Queries) (err error) {
			if filled, err = q.ImportFillStreamingPlaylist(ctx, sqlcgen.ImportFillStreamingPlaylistParams{
				VideoID: row.VideoID, MasterKey: sourceHLSKey(v.UUID, hls.PlaylistFilename),
			}); err != nil || filled == 0 {
				return err
			}
			return recordLedger(ctx, q, KindHLSPlaylist, v.UUID, row.VideoID, "done", "")
		}); err != nil {
			return err
		}
		if filled == 0 {
			continue
		}
		c.Imported++
		if row.State == "draft" && mapVideoState(v.State) == "published" {
			stillDraft++
		}
	}
	// State is metadata, and the default run never rewrites what it wrote — so a
	// video first read mid-transcode is playable now and still a draft. Say so,
	// with the way out, rather than leave the operator to find it.
	if stillDraft > 0 {
		r.addConflict(fmt.Sprintf("%d video(s) received their HLS playlist on this run but are still drafts here: "+
			"the source had not published them when they were first imported. Run with source_authoritative to follow the source's state", stillDraft))
	}
	return nil
}

// Count the destination after backfill, including empties left by older runs.
func (im *Importer) countMissingMedia(ctx context.Context, r *Report) error {
	if !im.carriesMedia() {
		return nil
	}
	videos, err := im.src.Videos(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindVideoNoMedia)
	c.Imported = 0
	for _, v := range videos {
		id, ok, err := im.resolveParent(ctx, KindVideo, v.UUID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ready, err := im.q.ImportVideoHasReadyPlaylist(ctx, id)
		if err != nil {
			return err
		}
		if ready {
			continue
		}
		files, err := im.q.ListVideoFiles(ctx, id)
		if err != nil {
			return err
		}
		found := false
		for _, f := range files {
			if f.Kind == "original" {
				found = true
			}
		}
		if !found {
			c.Imported++
		}
	}
	return nil
}
