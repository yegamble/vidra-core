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
		NextOwnerID: a.NextOwnerID, Status: "pending", CreatedAt: now, UpdatedAt: now}
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
		row := sqlcgen.ListIncomingVideoOwnershipChangesRow{ID: r.ID, Status: r.Status, CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt, VideoID: r.VideoID, VideoTitle: v.Title}
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

func (f *ownershipFakeRepo) GetVideoOwnershipChange(_ context.Context, id uuid.UUID) (sqlcgen.VideoOwnershipChange, error) {
	for _, r := range f.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return sqlcgen.VideoOwnershipChange{}, pgx.ErrNoRows
}

// AcceptVideoOwnershipChange mirrors the single-statement accept: flip AND move
// only while pending + addressed to the caller, the initiator still owns the
// video, and the target channel is the recipient's.
func (f *ownershipFakeRepo) AcceptVideoOwnershipChange(_ context.Context, a sqlcgen.AcceptVideoOwnershipChangeParams) (sqlcgen.AcceptVideoOwnershipChangeRow, error) {
	for i, r := range f.rows {
		if r.ID != a.ID || r.NextOwnerID != a.NextOwnerID || r.Status != "pending" {
			continue
		}
		v, ok := f.v.videos[r.VideoID]
		target, tok := f.v.channelByID(a.ChannelID)
		if !ok || !tok || v.OwnerID != r.InitiatorID || target.OwnerID != r.NextOwnerID {
			break
		}
		from := v.ChannelID
		v.ChannelID, v.OwnerID = target.ID, target.OwnerID
		f.v.videos[r.VideoID] = v
		f.rows[i].Status = "accepted"
		return sqlcgen.AcceptVideoOwnershipChangeRow{ID: r.VideoID, FromChannelID: from}, nil
	}
	return sqlcgen.AcceptVideoOwnershipChangeRow{}, pgx.ErrNoRows
}

func (f *ownershipFakeRepo) RefuseVideoOwnershipChange(_ context.Context, a sqlcgen.RefuseVideoOwnershipChangeParams) (int64, error) {
	for i, r := range f.rows {
		if r.ID == a.ID && r.NextOwnerID == a.NextOwnerID && r.Status == "pending" {
			f.rows[i].Status = "refused"
			return 1, nil
		}
	}
	return 0, nil
}

func (f *ownershipFakeRepo) SumVideoStorageUsage(_ context.Context, videoID uuid.UUID) (int64, error) {
	var n int64
	for _, vf := range f.v.files[videoID] {
		n += vf.SizeBytes
	}
	return n, nil
}

func (f *videoFakeRepo) channelByID(id uuid.UUID) (sqlcgen.Channel, bool) {
	ch, err := f.GetChannelByID(context.Background(), id)
	return ch, err == nil
}
