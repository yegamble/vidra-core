package peertubeimport

import (
	"context"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"path"
)

// A separate pass repairs old imports too. A ready playlist is the durable
// checkpoint and is never replaced (including a ladder generated on Vidra).
func (im *Importer) importHLSCopies(ctx context.Context, r *Report) error {
	if im.mediaMode != MediaModeCopy || im.srcMedia == nil || im.destMedia == nil {
		return nil
	}
	videos, err := im.src.Videos(ctx)
	if err != nil {
		return err
	}
	c := r.count(KindHLSPlaylist)
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
		prefix := sourceHLSDir(v.UUID)
		if err = im.copyHLSTree(ctx, prefix, path.Base(hls.PlaylistFilename)); err != nil {
			c.Failed++
			im.markFailed(ctx, KindHLSPlaylist, v.UUID, "HLS copy incomplete; rerun required")
			continue
		}
		if err = im.withTx(ctx, func(q *sqlcgen.Queries) error {
			if _, err := q.UpsertStreamingPlaylist(ctx, sqlcgen.UpsertStreamingPlaylistParams{VideoID: id, MasterKey: prefix + "/" + path.Base(hls.PlaylistFilename), State: "ready"}); err != nil {
				return err
			}
			return recordLedger(ctx, q, KindHLSPlaylist, v.UUID, id, "done", "")
		}); err != nil {
			return err
		}
		c.Imported++
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
