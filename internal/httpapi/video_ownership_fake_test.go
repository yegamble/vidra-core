package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// ownershipFakeRepo mirrors video_ownership_changes (0165) over the video
// harness's fakes, applying the predicates the SQL applies. v is late-bound:
// the video fake only exists once videoServerFull has returned.
type ownershipFakeRepo struct {
	v    *videoFakeRepo
	rows []sqlcgen.VideoOwnershipChange
}

// ownershipServer is videoServerFull with ownership changes wired.
func ownershipServer(t *testing.T, opts ...video.Option) (*Server, *videoFakeRepo, *ownershipFakeRepo) {
	t.Helper()
	own := &ownershipFakeRepo{}
	srv, _, _, _, repo := videoServerFull(t, testConfig(), append(opts, video.WithOwnershipRepository(own))...)
	own.v = repo
	return srv, repo, own
}

func (f *ownershipFakeRepo) ResolveOwnershipRecipient(_ context.Context, a sqlcgen.ResolveOwnershipRecipientParams) (sqlcgen.ResolveOwnershipRecipientRow, error) {
	for _, u := range f.v.users.users {
		if !strings.EqualFold(u.Username, a.Username) || !u.IsActive || u.DeletedAt.Valid {
			continue
		}
		for _, b := range f.v.userBlocks.blocks {
			if (b.blocker == u.ID && b.blocked == a.InitiatorID) || (b.blocker == a.InitiatorID && b.blocked == u.ID) {
				return sqlcgen.ResolveOwnershipRecipientRow{}, pgx.ErrNoRows
			}
		}
		row := sqlcgen.ResolveOwnershipRecipientRow{ID: u.ID}
		for _, m := range f.v.mutes.mutes {
			row.MutedInitiator = row.MutedInitiator || (m.muter == u.ID && m.muted == a.InitiatorID)
		}
		return row, nil
	}
	return sqlcgen.ResolveOwnershipRecipientRow{}, pgx.ErrNoRows
}

func (f *ownershipFakeRepo) CreateVideoOwnershipChange(_ context.Context, a sqlcgen.CreateVideoOwnershipChangeParams) (sqlcgen.VideoOwnershipChange, error) {
	for _, r := range f.rows {
		if r.VideoID == a.VideoID && r.Status == "pending" {
			return sqlcgen.VideoOwnershipChange{}, pgx.ErrNoRows // ON CONFLICT DO NOTHING
		}
	}
	now := time.Now()
	r := sqlcgen.VideoOwnershipChange{ID: uuid.New(), VideoID: a.VideoID, InitiatorID: a.InitiatorID,
		NextOwnerID: a.NextOwnerID, VideoTitle: a.VideoTitle, Status: "pending", CreatedAt: now, UpdatedAt: now}
	f.rows = append(f.rows, r)
	return r, nil
}

func (f *ownershipFakeRepo) ListIncomingVideoOwnershipChanges(_ context.Context, a sqlcgen.ListIncomingVideoOwnershipChangesParams) ([]sqlcgen.ListIncomingVideoOwnershipChangesRow, error) {
	var out []sqlcgen.ListIncomingVideoOwnershipChangesRow
	for i := len(f.rows) - 1; i >= 0; i-- { // newest first
		r := f.rows[i]
		v, ok := f.v.videos[r.VideoID]
		if r.NextOwnerID != a.NextOwnerID || !ok {
			continue
		}
		// The SQL: the title is the give-time snapshot, and a pending request
		// the giver can no longer honour reads 'expired'.
		status := r.Status
		if status == "pending" && (v.OwnerID != r.InitiatorID || !r.CreatedAt.After(a.ExpiredBefore)) {
			status = "expired"
		}
		row := sqlcgen.ListIncomingVideoOwnershipChangesRow{ID: r.ID, Status: status, CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt, VideoID: r.VideoID, VideoTitle: r.VideoTitle}
		for _, u := range f.v.users.users {
			if u.ID == r.InitiatorID {
				row.InitiatorUsername, row.InitiatorDisplayName = u.Username, u.DisplayName
			}
		}
		out = append(out, row)
	}
	if int(a.ResultOffset) >= len(out) {
		return nil, nil
	}
	out = out[a.ResultOffset:]
	if len(out) > int(a.ResultLimit) {
		out = out[:a.ResultLimit]
	}
	return out, nil
}

func (f *ownershipFakeRepo) CountIncomingVideoOwnershipChanges(_ context.Context, nextOwnerID uuid.UUID) (int64, error) {
	var n int64
	for _, r := range f.rows {
		if r.NextOwnerID == nextOwnerID {
			n++
		}
	}
	return n, nil
}

func (f *ownershipFakeRepo) ExpireStaleVideoOwnershipChanges(_ context.Context, a sqlcgen.ExpireStaleVideoOwnershipChangesParams) (int64, error) {
	var n int64
	for i, r := range f.rows {
		v, ok := f.v.videos[r.VideoID]
		if r.VideoID == a.VideoID && r.Status == "pending" && ok &&
			(v.OwnerID != r.InitiatorID || !r.CreatedAt.After(a.ExpiredBefore)) {
			f.rows[i].Status, f.rows[i].UpdatedAt = "expired", time.Now()
			n++
		}
	}
	return n, nil
}

func (f *ownershipFakeRepo) HasPendingVideoOwnershipChange(_ context.Context, videoID uuid.UUID) (bool, error) {
	for _, r := range f.rows {
		if r.VideoID == videoID && r.Status == "pending" {
			return true, nil
		}
	}
	return false, nil
}

func (f *ownershipFakeRepo) HasRecentOwnershipRefusal(_ context.Context, a sqlcgen.HasRecentOwnershipRefusalParams) (bool, error) {
	for _, r := range f.rows {
		if r.InitiatorID == a.InitiatorID && r.NextOwnerID == a.NextOwnerID &&
			(r.Status == "refused" || r.Status == "cancelled") && r.UpdatedAt.After(a.RefusedAfter) {
			return true, nil
		}
	}
	return false, nil
}

func (f *ownershipFakeRepo) CancelVideoOwnershipChange(_ context.Context, a sqlcgen.CancelVideoOwnershipChangeParams) (int64, error) {
	for i, r := range f.rows {
		if r.VideoID == a.VideoID && r.InitiatorID == a.InitiatorID && r.Status == "pending" {
			f.rows[i].Status, f.rows[i].UpdatedAt = "cancelled", time.Now()
			return 1, nil
		}
	}
	return 0, nil
}
