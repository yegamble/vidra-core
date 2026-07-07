package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// stubLedgerRepo is a minimal ipfsmirror.Repository backed by an in-memory slice of
// ledger rows. It implements ONLY the two read queries the serving fields depend on
// (ListIPFSPinsByVideo → VideoPins detail object; ListPinnedVideoIDs → card badge);
// every other method is an inert stub. It exists so the handler-level contract
// regression below drives the REAL ipfsmirror.Service (not a hand-rolled double),
// exercising the actual state='pinned'-only read logic the P19 audit flagged.
type stubLedgerRepo struct {
	rows []sqlcgen.MediaIpfsPin
}

func (r *stubLedgerRepo) ListIPFSPinsByVideo(ctx context.Context, videoID pgtype.UUID) ([]sqlcgen.MediaIpfsPin, error) {
	var out []sqlcgen.MediaIpfsPin
	for _, row := range r.rows {
		if row.VideoID == videoID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *stubLedgerRepo) ListPinnedVideoIDs(ctx context.Context, videoIds []uuid.UUID) ([]pgtype.UUID, error) {
	want := map[uuid.UUID]struct{}{}
	for _, id := range videoIds {
		want[id] = struct{}{}
	}
	seen := map[uuid.UUID]struct{}{}
	var out []pgtype.UUID
	for _, row := range r.rows {
		if row.State != "pinned" || !row.VideoID.Valid {
			continue
		}
		id := uuid.UUID(row.VideoID.Bytes)
		if _, ok := want[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, row.VideoID)
	}
	return out, nil
}

// --- inert stubs (the serving read path never calls these) ---
func (r *stubLedgerRepo) UpsertIPFSPinIntent(ctx context.Context, arg sqlcgen.UpsertIPFSPinIntentParams) (sqlcgen.MediaIpfsPin, error) {
	return sqlcgen.MediaIpfsPin{}, nil
}
func (r *stubLedgerRepo) BackfillIPFSPinIntent(ctx context.Context, arg sqlcgen.BackfillIPFSPinIntentParams) (int64, error) {
	return 0, nil
}
func (r *stubLedgerRepo) RepinIPFSObject(ctx context.Context, arg sqlcgen.RepinIPFSObjectParams) error {
	return nil
}
func (r *stubLedgerRepo) EnqueueIPFSUnpin(ctx context.Context, objectKey string) error { return nil }
func (r *stubLedgerRepo) ClaimDueIPFSPins(ctx context.Context, arg sqlcgen.ClaimDueIPFSPinsParams) ([]sqlcgen.ClaimDueIPFSPinsRow, error) {
	return nil, nil
}
func (r *stubLedgerRepo) MarkIPFSPinned(ctx context.Context, arg sqlcgen.MarkIPFSPinnedParams) (string, error) {
	return "", nil
}
func (r *stubLedgerRepo) MarkIPFSPinFailed(ctx context.Context, arg sqlcgen.MarkIPFSPinFailedParams) error {
	return nil
}
func (r *stubLedgerRepo) MarkIPFSPinUnpinned(ctx context.Context, objectKey string) error { return nil }
func (r *stubLedgerRepo) RescheduleIPFSPin(ctx context.Context, arg sqlcgen.RescheduleIPFSPinParams) error {
	return nil
}
func (r *stubLedgerRepo) RearmFailedIPFSPins(ctx context.Context, batchSize int32) (int64, error) {
	return 0, nil
}
func (r *stubLedgerRepo) SweepIneligibleIPFSPins(ctx context.Context, batchSize int32) (int64, error) {
	return 0, nil
}
func (r *stubLedgerRepo) EnqueueIPFSUserReeval(ctx context.Context, userID uuid.UUID) error {
	return nil
}
func (r *stubLedgerRepo) ClaimDueIPFSUserReevals(ctx context.Context, arg sqlcgen.ClaimDueIPFSUserReevalsParams) ([]sqlcgen.ClaimDueIPFSUserReevalsRow, error) {
	return nil, nil
}
func (r *stubLedgerRepo) DeleteIPFSUserReeval(ctx context.Context, userID uuid.UUID) error {
	return nil
}
func (r *stubLedgerRepo) RescheduleIPFSUserReeval(ctx context.Context, arg sqlcgen.RescheduleIPFSUserReevalParams) error {
	return nil
}
func (r *stubLedgerRepo) CountIPFSPinsByStateClass(ctx context.Context) ([]sqlcgen.CountIPFSPinsByStateClassRow, error) {
	return nil, nil
}
func (r *stubLedgerRepo) CountIPFSPinsSharingCID(ctx context.Context, arg sqlcgen.CountIPFSPinsSharingCIDParams) (int64, error) {
	return 0, nil
}

// TestRacedRowNeverReportsPinned is the P19 audit contract-truthfulness regression
// at the HTTP boundary. A raced ledger row — the pin lost the lost-update race, so
// it is 'unpinning' with a valid CID recorded, not 'pinned' — must NEVER surface as
// pinned: no `ipfs` object on the detail endpoint and no `ipfs_pinned=true` on the
// feed card, even though the video is (still) public+published and the CID is valid.
// Driving the REAL ipfsmirror.Service proves the state='pinned'-only read logic
// holds end to end through the handlers. A control flip to 'pinned' proves the
// assertion actually bites (the badge does light up for a genuinely pinned row).
func TestRacedRowNeverReportsPinned(t *testing.T) {
	const validCID = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
	repo := &stubLedgerRepo{}
	real := ipfsmirror.New(repo, nil, nil, nil, ipfsmirror.Config{
		Enabled: true, GatewayURL: "https://ipfs.example.org",
	})

	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSGatewayURL = "https://ipfs.example.org"
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{WithIPFSMirrorService(real)})
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Raced","privacy":"public"}`)
	vid := uuid.MustParse(id)

	// The raced ledger state: pin lost the race → 'unpinning' with the CID recorded.
	repo.rows = []sqlcgen.MediaIpfsPin{{
		ObjectKey:  "web-videos/" + id + ".mp4",
		MediaClass: "video_original",
		State:      "unpinning",
		Cid:        validCID,
		VideoID:    pgtype.UUID{Bytes: vid, Valid: true},
	}}

	// Detail: no ipfs object, no ipfs_pinned.
	rec := getVideo(srv, id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if v.IPFS != nil {
		t.Errorf("raced (unpinning) video leaked an ipfs object %+v, want none", v.IPFS)
	}
	if v.IPFSPinned {
		t.Error("raced (unpinning) video reported ipfs_pinned=true on detail, want false")
	}

	// Feed card: no ipfs_pinned badge.
	if pinned := feedIPFSPinned(t, srv, id); pinned {
		t.Error("raced (unpinning) video card reported ipfs_pinned=true, want false")
	}

	// Control: flip the same row to 'pinned' — now the badge MUST light up, proving
	// the not-pinned assertion above is meaningful (not vacuously false).
	repo.rows[0].State = "pinned"
	if pinned := feedIPFSPinned(t, srv, id); !pinned {
		t.Error("genuinely pinned video card missing ipfs_pinned=true (control)")
	}
}

// feedIPFSPinned fetches the public feed and returns the ipfs_pinned flag of the
// card with the given id (false when the card or flag is absent).
func feedIPFSPinned(t *testing.T, srv *Server, id string) bool {
	t.Helper()
	rec := getPath(srv, "/api/v1/videos")
	if rec.Code != http.StatusOK {
		t.Fatalf("feed = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var feed videoFeedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("unmarshal feed: %v", err)
	}
	for _, card := range feed.Videos {
		if card.ID == id {
			return card.IPFSPinned
		}
	}
	t.Fatalf("video %s not found in feed", id)
	return false
}
