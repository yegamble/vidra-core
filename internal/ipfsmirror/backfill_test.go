package ipfsmirror

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeCatalog returns whatever candidate rows the test seeds — including ones that
// FAIL the eligibility gate — so the backfill's own Eligible() re-check (the privacy
// fence at the catalog-backfill layer) is exercised directly, independent of the SQL
// WHERE clauses.
type fakeCatalog struct {
	videos    []VideoObjectRow
	images    []IdentityImageRow
	covers    []PlaylistCoverRow
	videosErr error
}

func (c *fakeCatalog) ListBackfillVideoObjects(ctx context.Context) ([]VideoObjectRow, error) {
	return c.videos, c.videosErr
}
func (c *fakeCatalog) ListBackfillIdentityImages(ctx context.Context) ([]IdentityImageRow, error) {
	return c.images, nil
}
func (c *fakeCatalog) ListBackfillPlaylistCovers(ctx context.Context) ([]PlaylistCoverRow, error) {
	return c.covers, nil
}

func backfillConfig(cat Catalog) Config {
	cfg := testConfig()
	cfg.Catalog = cat
	return cfg
}

// TestBackfillSeedsEligibleAndFencesNonPublic is the privacy-fence regression test
// at the backfill layer: the catalog deliberately returns non-public candidates
// (private video, draft video, unlisted-owner avatar, deactivated-owner banner,
// private playlist cover) alongside eligible ones. Backfill must seed ONLY the
// eligible public objects and NEVER a non-public one, with per-class counts.
func TestBackfillSeedsEligibleAndFencesNonPublic(t *testing.T) {
	vidPub := uuid.New()
	vidPriv := uuid.New()
	vidDraft := uuid.New()
	vidUnlistedOwner := uuid.New()
	ownerListed := uuid.New()
	ownerUnlisted := uuid.New()
	ownerInactive := uuid.New()

	cat := &fakeCatalog{
		videos: []VideoObjectRow{
			// eligible (public+published, owner listed) — one per video-derived class shape.
			{ObjectKey: "web-videos/" + vidPub.String() + ".mp4", Class: ClassVideoOriginal, VideoID: vidPub, Privacy: "public", State: "published"},
			{ObjectKey: "thumbnails/" + vidPub.String() + ".jpg", Class: ClassThumbnail, VideoID: vidPub, Privacy: "public", State: "published"},
			{ObjectKey: "streaming-playlists/" + vidPub.String() + "/", Class: ClassHLS, VideoID: vidPub, Privacy: "public", State: "published"},
			{ObjectKey: "captions/" + vidPub.String() + "/en.vtt", Class: ClassCaption, VideoID: vidPub, Privacy: "public", State: "published"},
			// NON-public: must be fenced out even though the catalog handed them over.
			{ObjectKey: "web-videos/" + vidPriv.String() + ".mp4", Class: ClassVideoOriginal, VideoID: vidPriv, Privacy: "private", State: "published"},
			{ObjectKey: "thumbnails/" + vidDraft.String() + ".jpg", Class: ClassThumbnail, VideoID: vidDraft, Privacy: "public", State: "draft"},
			// UNLISTED OWNER: a public+published video owned by an unlisted account —
			// treated as private for mirroring (spec §3/§7), must be fenced out.
			{ObjectKey: "web-videos/" + vidUnlistedOwner.String() + ".mp4", Class: ClassVideoOriginal, VideoID: vidUnlistedOwner, Privacy: "public", State: "published", OwnerUnlisted: true},
		},
		images: []IdentityImageRow{
			// eligible (active + listed).
			{ObjectKey: "avatars/users/a.png", Class: ClassUserAvatar, OwnerUserID: ownerListed, OwnerActive: true, OwnerUnlisted: false},
			{ObjectKey: "banners/channels/c.png", Class: ClassChannelBanner, OwnerUserID: ownerListed, OwnerActive: true, OwnerUnlisted: false},
			// NON-public: unlisted account, and deactivated account.
			{ObjectKey: "avatars/users/u.png", Class: ClassUserAvatar, OwnerUserID: ownerUnlisted, OwnerActive: true, OwnerUnlisted: true},
			{ObjectKey: "banners/users/x.png", Class: ClassUserBanner, OwnerUserID: ownerInactive, OwnerActive: false, OwnerUnlisted: false},
		},
		covers: []PlaylistCoverRow{
			{ObjectKey: "playlist-thumbnails/p1.jpg", Visibility: "public"},   // eligible
			{ObjectKey: "playlist-thumbnails/p2.jpg", Visibility: "private"},  // fenced
			{ObjectKey: "playlist-thumbnails/p3.jpg", Visibility: "unlisted"}, // fenced
		},
	}

	repo := newFakeRepo()
	svc := New(repo, &fakeLookups{}, newBlobs(t), nil, backfillConfig(cat))

	counts, err := svc.Backfill(context.Background())
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	if counts.Total != 7 {
		t.Errorf("Total = %d, want 7 (4 video + 2 image + 1 cover)", counts.Total)
	}
	wantByClass := map[string]int64{
		"video_original": 1, "thumbnail": 1, "hls": 1, "caption": 1,
		"user_avatar": 1, "channel_banner": 1, "playlist_cover": 1,
	}
	for cls, want := range wantByClass {
		if counts.ByClass[cls] != want {
			t.Errorf("ByClass[%s] = %d, want %d", cls, counts.ByClass[cls], want)
		}
	}
	if len(counts.ByClass) != len(wantByClass) {
		t.Errorf("ByClass has %d classes, want %d: %+v", len(counts.ByClass), len(wantByClass), counts.ByClass)
	}

	// The privacy fence: NOT ONE non-public object may have a ledger row.
	for _, key := range []string{
		"web-videos/" + vidPriv.String() + ".mp4",
		"thumbnails/" + vidDraft.String() + ".jpg",
		"web-videos/" + vidUnlistedOwner.String() + ".mp4",
		"avatars/users/u.png",
		"banners/users/x.png",
		"playlist-thumbnails/p2.jpg",
		"playlist-thumbnails/p3.jpg",
	} {
		if s := repo.state(key); s != "" {
			t.Errorf("non-public object %q was enqueued (state=%q) — privacy fence breached", key, s)
		}
	}

	// Eligible objects landed as pending pin intents with the right provenance.
	if s := repo.state("web-videos/" + vidPub.String() + ".mp4"); s != "pending" {
		t.Errorf("eligible original state = %q, want pending", s)
	}
	if row := repo.rows["web-videos/"+vidPub.String()+".mp4"]; !row.VideoID.Valid || uuid.UUID(row.VideoID.Bytes) != vidPub {
		t.Errorf("video object provenance not set to video_id")
	}
	if row := repo.rows["avatars/users/a.png"]; !row.OwnerUserID.Valid || uuid.UUID(row.OwnerUserID.Bytes) != ownerListed {
		t.Errorf("identity image provenance not set to owner_user_id")
	}
	if row := repo.rows["playlist-thumbnails/p1.jpg"]; row.VideoID.Valid || row.OwnerUserID.Valid {
		t.Errorf("playlist cover should carry no provenance")
	}
}

// TestBackfillIdempotent: a second run over an unchanged catalog seeds zero (every
// eligible object already has a ledger row).
func TestBackfillIdempotent(t *testing.T) {
	vid := uuid.New()
	cat := &fakeCatalog{
		videos: []VideoObjectRow{
			{ObjectKey: "web-videos/" + vid.String() + ".mp4", Class: ClassVideoOriginal, VideoID: vid, Privacy: "public", State: "published"},
		},
		images: []IdentityImageRow{
			{ObjectKey: "avatars/users/a.png", Class: ClassUserAvatar, OwnerUserID: uuid.New(), OwnerActive: true},
		},
	}
	repo := newFakeRepo()
	svc := New(repo, &fakeLookups{}, newBlobs(t), nil, backfillConfig(cat))

	first, err := svc.Backfill(context.Background())
	if err != nil {
		t.Fatalf("first Backfill: %v", err)
	}
	if first.Total != 2 {
		t.Fatalf("first Total = %d, want 2", first.Total)
	}
	second, err := svc.Backfill(context.Background())
	if err != nil {
		t.Fatalf("second Backfill: %v", err)
	}
	if second.Total != 0 || len(second.ByClass) != 0 {
		t.Errorf("second run = {Total:%d ByClass:%+v}, want zero (idempotent)", second.Total, second.ByClass)
	}
}

// TestBackfillSkipsExistingLedgerRow: an object that already has a ledger row (in
// ANY state, here a live 'pinned' one) is left untouched and not counted — the
// backfill only seeds missing rows; the periodic Reconcile owns re-arming.
func TestBackfillSkipsExistingLedgerRow(t *testing.T) {
	vid := uuid.New()
	key := "web-videos/" + vid.String() + ".mp4"
	repo := newFakeRepo()
	// Pre-seed a live pinned row (as if already mirrored before the backfill runs).
	repo.rows[key] = &sqlcgen.MediaIpfsPin{
		ObjectKey: key, MediaClass: string(ClassVideoOriginal), State: "pinned",
		Cid: "bafkre", VideoID: pgUUID(vid),
	}
	cat := &fakeCatalog{videos: []VideoObjectRow{
		{ObjectKey: key, Class: ClassVideoOriginal, VideoID: vid, Privacy: "public", State: "published"},
	}}
	svc := New(repo, &fakeLookups{}, newBlobs(t), nil, backfillConfig(cat))

	counts, err := svc.Backfill(context.Background())
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if counts.Total != 0 {
		t.Errorf("Total = %d, want 0 (existing row not re-seeded)", counts.Total)
	}
	if s := repo.state(key); s != "pinned" {
		t.Errorf("existing row state = %q, want unchanged 'pinned'", s)
	}
}

// TestBackfillDisabled: a disabled mirror is an inert no-op — empty counts, no
// error, nothing enqueued (even a configured catalog is never scanned).
func TestBackfillDisabled(t *testing.T) {
	cat := &fakeCatalog{videos: []VideoObjectRow{
		{ObjectKey: "web-videos/x.mp4", Class: ClassVideoOriginal, VideoID: uuid.New(), Privacy: "public", State: "published"},
	}}
	cfg := backfillConfig(cat)
	cfg.Enabled = false
	repo := newFakeRepo()
	svc := New(repo, &fakeLookups{}, newBlobs(t), nil, cfg)

	counts, err := svc.Backfill(context.Background())
	if err != nil {
		t.Fatalf("Backfill (disabled): %v", err)
	}
	if counts.Total != 0 {
		t.Errorf("Total = %d, want 0 when disabled", counts.Total)
	}
	if len(repo.rows) != 0 {
		t.Errorf("rows seeded while disabled: %d", len(repo.rows))
	}
}

// TestBackfillNoCatalog: an enabled service built without a Catalog reports a clean
// ErrCatalogNotConfigured rather than panicking.
func TestBackfillNoCatalog(t *testing.T) {
	svc := New(newFakeRepo(), &fakeLookups{}, newBlobs(t), nil, testConfig()) // no Catalog
	if _, err := svc.Backfill(context.Background()); !errors.Is(err, ErrCatalogNotConfigured) {
		t.Errorf("err = %v, want ErrCatalogNotConfigured", err)
	}
}

// TestBackfillPropagatesCatalogError: an enumeration error aborts the scan.
func TestBackfillPropagatesCatalogError(t *testing.T) {
	sentinel := errors.New("db down")
	cat := &fakeCatalog{videosErr: sentinel}
	svc := New(newFakeRepo(), &fakeLookups{}, newBlobs(t), nil, backfillConfig(cat))
	if _, err := svc.Backfill(context.Background()); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel", err)
	}
}
