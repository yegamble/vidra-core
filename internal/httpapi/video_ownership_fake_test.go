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
	// inFlight mirrors VideoMediaWorkInFlight over the harness's own upload
	// and transcode services (replace sessions, live transcode jobs); the full
	// predicate is proven against PostgreSQL in internal/store.
	inFlight func(uuid.UUID) bool
}

// ownershipServer is videoServerFull with ownership changes wired.
func ownershipServer(t *testing.T, opts ...video.Option) (*Server, *videoFakeRepo, *ownershipFakeRepo) {
	t.Helper()
	srv, repo, own, _ := ownershipServerTC(t, opts...)
	return srv, repo, own
}

// ownershipServerTC is ownershipServer plus the transcode fake, for seeding
// live transcode jobs.
func ownershipServerTC(t *testing.T, opts ...video.Option) (*Server, *videoFakeRepo, *ownershipFakeRepo, *transcodeFakeRepo) {
	t.Helper()
	own := &ownershipFakeRepo{}
	srv, _, tc, _, repo := videoServerFull(t, testConfig(), append(opts, video.WithOwnershipRepository(own))...)
	own.v = repo
	own.inFlight = func(id uuid.UUID) bool {
		ctx := context.Background()
		return (srv.uploadsvc != nil && srv.uploadsvc.HasActiveReplaceSession(ctx, id)) ||
			(srv.transcodesvc != nil && srv.transcodesvc.HasLiveJob(ctx, id))
	}
	return srv, repo, own, tc
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
		if status == "cancelled" ||
			(status == "pending" && (v.OwnerID != r.InitiatorID || !r.CreatedAt.After(a.ExpiredBefore))) {
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
		if !ok || !tok || v.OwnerID != r.InitiatorID || target.OwnerID != r.NextOwnerID ||
			!r.CreatedAt.After(a.ExpiredBefore) || f.inFlight(r.VideoID) {
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

func (f *ownershipFakeRepo) VideoMediaWorkInFlight(_ context.Context, videoID uuid.UUID) (bool, error) {
	return f.inFlight(videoID), nil
}

// SwapVideoOriginalIfOwner mirrors the guarded single-statement swap: only while
// the video still belongs to OwnerID are its originals replaced by the new row.
func (f *videoFakeRepo) SwapVideoOriginalIfOwner(ctx context.Context, a sqlcgen.SwapVideoOriginalIfOwnerParams) (sqlcgen.VideoFile, error) {
	if v, ok := f.videos[a.VideoID]; !ok || v.OwnerID != a.OwnerID {
		return sqlcgen.VideoFile{}, pgx.ErrNoRows
	}
	_ = f.DeleteVideoFilesByVideoAndKind(ctx, sqlcgen.DeleteVideoFilesByVideoAndKindParams{VideoID: a.VideoID, Kind: "original"})
	return f.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{VideoID: a.VideoID, Kind: "original", StorageKey: a.StorageKey,
		ContentType: a.ContentType, OriginalName: a.OriginalName, SizeBytes: a.SizeBytes, Sha256: a.Sha256})
}
