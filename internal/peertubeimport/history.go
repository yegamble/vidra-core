package peertubeimport

import "context"

// History is paged independently of video imports so older migrations backfill.
// Only new pairs are written: even source-authoritative runs preserve edits and
// the ledger remembers cleared history after its native row has been deleted.
func (im *Importer) importWatchHistory(ctx context.Context, r *Report) error {
	has, err := im.src.tableExists(ctx, "userVideoHistory")
	if err != nil || !has {
		return err
	}
	onActor, err := im.src.actorLinksLiveOnActor(ctx)
	if err != nil {
		return err
	}
	actorJoin := `act.id = vc."actorId"`
	if onActor {
		actorJoin = `act."videoChannelId" = vc.id`
	}
	c := r.count(KindWatchHistory)
	var after int64
	for {
		var batch []byte
		var next int64
		var local, remote int
		// Cursor includes remote-only pages. Batching avoids one destination
		// round trip per entry on histories with hundreds of thousands of rows.
		err := im.src.pool.QueryRow(ctx, `SELECT
   COALESCE(jsonb_agg(p) FILTER (WHERE NOT remote), '[]'::jsonb),
   COALESCE(max(id),0), count(*) FILTER (WHERE NOT remote), count(*) FILTER (WHERE remote)
   FROM (SELECT h.id, h."userId"::text AS source_user_id, v.uuid::text AS source_video_id,
    GREATEST(h."currentTime",0) AS position_seconds, h."createdAt" AS created_at,
    h."updatedAt" AS updated_at, act."serverId" IS NOT NULL AS remote
    FROM "userVideoHistory" h JOIN video v ON v.id=h."videoId"
    JOIN "videoChannel" vc ON vc.id=v."channelId" JOIN actor act ON `+actorJoin+`
    WHERE h.id>$1 ORDER BY h.id LIMIT 1000) p`, after).Scan(&batch, &next, &local, &remote)
		if err != nil {
			return err
		}
		if next == 0 {
			return nil
		}
		after = next
		c.Unsupported += remote // Native history can only reference local videos.
		if r.DryRun {
			c.Planned += local
			continue
		}
		inserted, err := im.q.ImportWatchHistoryBatch(ctx, batch)
		if err != nil {
			return err
		}
		c.Imported += int(inserted)
		c.Skipped += local - int(inserted)
	}
}
