package peertubeimport

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func (im *Importer) planPersonalMutes(ctx context.Context, r *Report) error {
	for _, kind := range []string{KindAccountMute, KindInstanceMute} {
		rows, err := im.src.personalMutes(ctx, kind == KindInstanceMute)
		if err != nil {
			return err
		}
		r.count(kind).Planned = len(rows)
	}
	return nil
}

// A separate ledger pass backfills already-imported accounts. Once carried, a
// mute belongs to its user: reruns never restore an unmute, even when the source
// is authoritative for entity metadata. Instance-owned blocklists stay separate.
func (im *Importer) importPersonalMutes(ctx context.Context, r *Report) error {
	for _, kind := range []string{KindAccountMute, KindInstanceMute} {
		rows, err := im.src.personalMutes(ctx, kind == KindInstanceMute)
		if err != nil {
			return err
		}
		c := r.count(kind)
		for _, m := range rows {
			sid := strconv.FormatInt(m.ID, 10)
			if err := im.importPersonalMute(ctx, kind, sid, m, c); err != nil {
				im.markFailed(ctx, kind, sid, "personal mute could not be stored")
				c.Failed++
				im.logger.WarnContext(ctx, "peertube import: personal mute failed", "entity_kind", kind)
			}
		}
	}
	return nil
}
func (im *Importer) importPersonalMute(ctx context.Context, kind, sid string, m sourcePersonalMute, c *Counts) error {
	if _, _, done, err := im.alreadyProcessed(ctx, kind, sid); err != nil {
		return err
	} else if done {
		c.Skipped++
		return nil
	}
	owner, ok, err := im.resolveParent(ctx, KindUser, strconv.FormatInt(m.OwnerUserID, 10))
	if err != nil {
		return err
	} else if !ok {
		return awaitParent(c)
	}
	var target uuid.UUID
	if kind == KindAccountMute {
		target, ok, err = im.resolveParent(ctx, KindUser, strconv.FormatInt(m.TargetUserID, 10))
		if err != nil {
			return err
		} else if !ok {
			return awaitParent(c)
		}
		if owner == target {
			if err := im.recordStandalone(ctx, kind, sid, uuid.Nil, "unsupported", "mute resolves to the same account"); err != nil {
				return err
			}
			c.Unsupported++
			return nil
		}
	} else if m.Domain, err = instancemod.NormalizeDomain(m.Domain); err != nil {
		return err
	}
	var inserted int64
	err = im.withTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		if kind == KindAccountMute {
			inserted, err = q.ImportAccountMute(ctx, sqlcgen.ImportAccountMuteParams{MuterID: owner, MutedID: target, CreatedAt: m.CreatedAt})
		} else {
			inserted, err = q.ImportInstanceMute(ctx, sqlcgen.ImportInstanceMuteParams{MuterID: owner, Domain: m.Domain, CreatedAt: m.CreatedAt})
		}
		if err != nil {
			return err
		}
		return recordLedger(ctx, q, kind, sid, uuid.Nil, "done", "")
	})
	if err == nil {
		if inserted > 0 {
			c.Imported++
		} else {
			c.Skipped++
		}
	}
	return err
}
