package ipfsmirror

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ---- in-memory fake Repository -------------------------------------------

type fakeRepo struct {
	rows map[string]*sqlcgen.MediaIpfsPin
}

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[string]*sqlcgen.MediaIpfsPin{}} }

func (r *fakeRepo) UpsertIPFSPinIntent(ctx context.Context, arg sqlcgen.UpsertIPFSPinIntentParams) (sqlcgen.MediaIpfsPin, error) {
	row, ok := r.rows[arg.ObjectKey]
	if !ok {
		row = &sqlcgen.MediaIpfsPin{ObjectKey: arg.ObjectKey, State: "pending", NextAttemptAt: time.Now().UTC()}
		r.rows[arg.ObjectKey] = row
	}
	row.MediaClass = arg.MediaClass
	row.VideoID = arg.VideoID
	row.OwnerUserID = arg.OwnerUserID
	switch row.State {
	case "failed", "unpinned", "unpinning":
		row.State = "pending"
		row.Attempts = 0
		row.NextAttemptAt = time.Now().UTC()
		row.LastError = ""
	}
	return *row, nil
}

func (r *fakeRepo) BackfillIPFSPinIntent(ctx context.Context, arg sqlcgen.BackfillIPFSPinIntentParams) (int64, error) {
	// ON CONFLICT DO NOTHING: insert only when the object has no row yet; return 1
	// when a row was inserted, 0 when one already existed (any state) — the
	// idempotency signal the backfill tallies.
	if _, ok := r.rows[arg.ObjectKey]; ok {
		return 0, nil
	}
	r.rows[arg.ObjectKey] = &sqlcgen.MediaIpfsPin{
		ObjectKey:     arg.ObjectKey,
		MediaClass:    arg.MediaClass,
		VideoID:       arg.VideoID,
		OwnerUserID:   arg.OwnerUserID,
		State:         "pending",
		NextAttemptAt: time.Now().UTC(),
	}
	return 1, nil
}

func (r *fakeRepo) RepinIPFSObject(ctx context.Context, arg sqlcgen.RepinIPFSObjectParams) error {
	row, ok := r.rows[arg.ObjectKey]
	if !ok {
		row = &sqlcgen.MediaIpfsPin{ObjectKey: arg.ObjectKey}
		r.rows[arg.ObjectKey] = row
	}
	row.MediaClass = arg.MediaClass
	row.VideoID = arg.VideoID
	// Force actionable — even a live 'pinned' row — preserving cid/car_root so the
	// worker can swap the superseded root after the new add (the real query's
	// wholesale-replace semantics). next_attempt in the past ⇒ immediately claimable.
	row.State = "pending"
	row.Attempts = 0
	row.NextAttemptAt = time.Now().UTC().Add(-time.Second)
	row.LastError = ""
	return nil
}

func (r *fakeRepo) EnqueueIPFSUnpin(ctx context.Context, objectKey string) error {
	if row, ok := r.rows[objectKey]; ok && (row.State == "pinned" || row.State == "pending") {
		row.State = "unpinning"
		row.Attempts = 0
		row.NextAttemptAt = time.Now().UTC()
		row.LastError = ""
	}
	return nil
}

func (r *fakeRepo) ClaimDueIPFSPins(ctx context.Context, arg sqlcgen.ClaimDueIPFSPinsParams) ([]sqlcgen.ClaimDueIPFSPinsRow, error) {
	now := time.Now().UTC()
	var out []sqlcgen.ClaimDueIPFSPinsRow
	for _, row := range r.rows {
		if (row.State == "pending" || row.State == "unpinning") && !row.NextAttemptAt.After(now) {
			row.NextAttemptAt = now.Add(time.Duration(arg.LeaseSeconds) * time.Second)
			out = append(out, sqlcgen.ClaimDueIPFSPinsRow{
				ObjectKey: row.ObjectKey, MediaClass: row.MediaClass, Cid: row.Cid,
				CarRoot: row.CarRoot, State: row.State, Attempts: row.Attempts,
				VideoID: row.VideoID, OwnerUserID: row.OwnerUserID,
			})
			if len(out) >= int(arg.BatchSize) {
				break
			}
		}
	}
	return out, nil
}

// MarkIPFSPinned mirrors the state-guarded RETURNING query: the CID is ALWAYS
// recorded, but state advances to 'pinned' only from a pinnable state — a row a
// concurrent unpin flipped to 'unpinning' KEEPS 'unpinning' (the lost-update guard,
// P19 audit BLOCKER). Returns the resulting state (pgx.ErrNoRows when absent, as
// the real :one query does).
func (r *fakeRepo) MarkIPFSPinned(ctx context.Context, arg sqlcgen.MarkIPFSPinnedParams) (string, error) {
	row, ok := r.rows[arg.ObjectKey]
	if !ok {
		return "", pgx.ErrNoRows
	}
	row.Cid = arg.Cid
	row.CarRoot = arg.CarRoot
	row.ByteSize = arg.ByteSize
	row.LastError = ""
	if row.State == "pending" || row.State == "pinned" {
		row.State = "pinned"
	}
	return row.State, nil
}

// MarkIPFSPinFailed mirrors the state-guarded dead-letter: only fails a row still
// in the expected (claimed) state, so a concurrent privacy flip is not clobbered.
func (r *fakeRepo) MarkIPFSPinFailed(ctx context.Context, arg sqlcgen.MarkIPFSPinFailedParams) error {
	if row, ok := r.rows[arg.ObjectKey]; ok && row.State == arg.ExpectedState {
		row.State = "failed"
		row.Attempts++
		row.LastError = arg.LastError
	}
	return nil
}

// MarkIPFSPinUnpinned mirrors the state-guarded terminal-unpin: only completes when
// the row is STILL 'unpinning' (a concurrent private→public re-arm to 'pending' is
// preserved, not clobbered).
func (r *fakeRepo) MarkIPFSPinUnpinned(ctx context.Context, objectKey string) error {
	if row, ok := r.rows[objectKey]; ok && row.State == "unpinning" {
		row.State = "unpinned"
		row.LastError = ""
	}
	return nil
}

func (r *fakeRepo) RescheduleIPFSPin(ctx context.Context, arg sqlcgen.RescheduleIPFSPinParams) error {
	if row, ok := r.rows[arg.ObjectKey]; ok {
		row.Attempts++
		row.NextAttemptAt = arg.NextAttemptAt
		row.LastError = arg.LastError
	}
	return nil
}

func (r *fakeRepo) RearmFailedIPFSPins(ctx context.Context, batchSize int32) (int64, error) {
	var n int64
	for _, row := range r.rows {
		if row.State != "failed" {
			continue
		}
		if row.Cid == "" {
			row.State = "pending"
		} else {
			row.State = "unpinning"
		}
		row.Attempts = 0
		row.NextAttemptAt = time.Now().UTC()
		row.LastError = ""
		n++
		if n >= int64(batchSize) {
			break
		}
	}
	return n, nil
}

func (r *fakeRepo) CountIPFSPinsByStateClass(ctx context.Context) ([]sqlcgen.CountIPFSPinsByStateClassRow, error) {
	type key struct{ state, class string }
	counts := map[key]int64{}
	for _, row := range r.rows {
		counts[key{row.State, row.MediaClass}]++
	}
	var out []sqlcgen.CountIPFSPinsByStateClassRow
	for k, c := range counts {
		out = append(out, sqlcgen.CountIPFSPinsByStateClassRow{State: k.state, MediaClass: k.class, Count: c})
	}
	return out, nil
}

func (r *fakeRepo) CountIPFSPinsSharingCID(ctx context.Context, arg sqlcgen.CountIPFSPinsSharingCIDParams) (int64, error) {
	var n int64
	for _, row := range r.rows {
		if row.Cid == arg.Cid && row.Cid != "" && row.ObjectKey != arg.ObjectKey &&
			(row.State == "pinned" || row.State == "pending") {
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) ListIPFSPinsByVideo(ctx context.Context, videoID pgtype.UUID) ([]sqlcgen.MediaIpfsPin, error) {
	var out []sqlcgen.MediaIpfsPin
	for _, row := range r.rows {
		if row.VideoID == videoID {
			out = append(out, *row)
		}
	}
	return out, nil
}

func (r *fakeRepo) ListPinnedVideoIDs(ctx context.Context, videoIds []uuid.UUID) ([]pgtype.UUID, error) {
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

func (r *fakeRepo) state(key string) string {
	if row, ok := r.rows[key]; ok {
		return row.State
	}
	return ""
}

// ---- fake Lookups ---------------------------------------------------------

type fakeLookups struct {
	videoPrivacy, videoState string
	videoOK                  bool
	videoFiles               []VideoFileRef
	captionKeys              []string
	userActive, userUnlisted bool
	userOK                   bool
	channelOwner             uuid.UUID
	channelOK                bool
	playlistVis, playlistKey string
	playlistHasCover         bool
	ownerImages              []ImageRef
}

func (l *fakeLookups) VideoVisibility(ctx context.Context, videoID uuid.UUID) (string, string, uuid.UUID, bool, error) {
	return l.videoPrivacy, l.videoState, uuid.Nil, l.videoOK, nil
}
func (l *fakeLookups) VideoFiles(ctx context.Context, videoID uuid.UUID) ([]VideoFileRef, error) {
	return l.videoFiles, nil
}
func (l *fakeLookups) VideoCaptionKeys(ctx context.Context, videoID uuid.UUID) ([]string, error) {
	return l.captionKeys, nil
}
func (l *fakeLookups) UserFlags(ctx context.Context, userID uuid.UUID) (bool, bool, bool, error) {
	return l.userActive, l.userUnlisted, l.userOK, nil
}
func (l *fakeLookups) ChannelOwner(ctx context.Context, channelID uuid.UUID) (uuid.UUID, bool, error) {
	return l.channelOwner, l.channelOK, nil
}
func (l *fakeLookups) PlaylistCover(ctx context.Context, playlistID uuid.UUID) (string, string, bool, error) {
	return l.playlistVis, l.playlistKey, l.playlistHasCover, nil
}
func (l *fakeLookups) OwnerImageRefs(ctx context.Context, userID uuid.UUID) ([]ImageRef, error) {
	return l.ownerImages, nil
}

// ---- helpers --------------------------------------------------------------

func testConfig() Config {
	return Config{Enabled: true, GatewayURL: "https://gw.example.org", AddTimeout: time.Second,
		Concurrency: 2, MaxAttempts: 6, BaseBackoff: -1, ReconcileBatch: 100}
}

func newBlobs(t *testing.T) storage.Backend {
	t.Helper()
	b, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	return b
}

func putBlob(t *testing.T, b storage.Backend, key, data string) {
	t.Helper()
	if _, err := b.Put(context.Background(), key, strings.NewReader(data)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func seedPending(r *fakeRepo, key, class string) {
	r.rows[key] = &sqlcgen.MediaIpfsPin{ObjectKey: key, MediaClass: class, State: "pending", NextAttemptAt: time.Now().UTC().Add(-time.Second)}
}

// seedPinned inserts an already-pinned ledger row (state=pinned, valid CID) tied
// to a video, for the read-model (VideoPins / PinnedVideoIDs) tests.
func seedPinned(r *fakeRepo, key, class, cid string, videoID uuid.UUID) {
	r.rows[key] = &sqlcgen.MediaIpfsPin{
		ObjectKey: key, MediaClass: class, State: "pinned", Cid: cid,
		VideoID: pgtype.UUID{Bytes: videoID, Valid: videoID != uuid.Nil},
	}
}

// ---- tests ----------------------------------------------------------------

// TestWorkerPinsPendingRow: a due pending row is streamed from the authoritative
// store, add+pinned, and its CID/byte-size recorded.
func TestWorkerPinsPendingRow(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	key := "avatars/users/" + uuid.NewString() + ".png"
	putBlob(t, blobs, key, "avatar-bytes")
	seedPending(repo, key, string(ClassUserAvatar))

	svc := New(repo, &fakeLookups{}, blobs, client, testConfig())
	n, err := svc.DrainDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDue: %v", err)
	}
	if n != 1 {
		t.Fatalf("drained %d, want 1", n)
	}
	row := repo.rows[key]
	if row.State != "pinned" {
		t.Fatalf("state = %q, want pinned", row.State)
	}
	if row.Cid == "" {
		t.Error("cid was not recorded")
	}
	if row.ByteSize != int64(len("avatar-bytes")) {
		t.Errorf("byte_size = %d, want %d", row.ByteSize, len("avatar-bytes"))
	}
	if client.AddCount != 1 {
		t.Errorf("AddCount = %d, want 1", client.AddCount)
	}
}

// TestWorkerFailureSemantics: with the node down the pin does not succeed, the row
// stays actionable (rescheduled, not dead-lettered), and once the node recovers a
// later drain pins it — the upload never depended on the mirror.
func TestWorkerFailureSemantics(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	client.Down = true
	key := "thumbnails/" + uuid.NewString() + ".jpg"
	putBlob(t, blobs, key, "thumb")
	seedPending(repo, key, string(ClassThumbnail))

	svc := New(repo, &fakeLookups{}, blobs, client, testConfig())

	if n, _ := svc.DrainDue(context.Background(), 10); n != 0 {
		t.Fatalf("drain while down = %d, want 0", n)
	}
	if got := repo.state(key); got != "pending" {
		t.Fatalf("state after down = %q, want pending (still actionable)", got)
	}
	if repo.rows[key].Attempts == 0 {
		t.Error("attempts not incremented on transient failure")
	}

	client.Down = false // node recovers
	if n, _ := svc.DrainDue(context.Background(), 10); n != 1 {
		t.Fatalf("drain after recovery = %d, want 1", n)
	}
	if got := repo.state(key); got != "pinned" {
		t.Errorf("state after recovery = %q, want pinned", got)
	}
}

// TestWorkerDeadLetters: a permanently-failing pin (source object missing) is
// rescheduled up to the cap then dead-lettered to 'failed'.
func TestWorkerDeadLetters(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t) // key never stored ⇒ Open fails every time
	client := ipfs.NewFakeIPFSClient()
	key := "captions/" + uuid.NewString() + "/en.vtt"
	seedPending(repo, key, string(ClassCaption))

	cfg := testConfig()
	cfg.MaxAttempts = 3
	svc := New(repo, &fakeLookups{}, blobs, client, cfg)
	for i := 0; i < 3; i++ {
		if _, err := svc.DrainDue(context.Background(), 10); err != nil {
			t.Fatalf("drain %d: %v", i, err)
		}
	}
	if got := repo.state(key); got != "failed" {
		t.Errorf("state = %q, want failed (dead-lettered)", got)
	}
}

// TestReferenceCheckedUnpin: two ledger rows share one CID (identical bytes).
// Unpinning the first must NOT remove the node pin (still referenced); unpinning
// the second — now the last reference — removes it.
func TestReferenceCheckedUnpin(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	cid := ipfs.RawLeafCIDv1([]byte("shared-bytes"))
	keyA, keyB := "web-videos/a.mp4", "web-videos/b.mp4"
	repo.rows[keyA] = &sqlcgen.MediaIpfsPin{ObjectKey: keyA, MediaClass: "video_original", State: "pinned", Cid: cid}
	repo.rows[keyB] = &sqlcgen.MediaIpfsPin{ObjectKey: keyB, MediaClass: "video_original", State: "pinned", Cid: cid}

	svc := New(repo, &fakeLookups{}, blobs, client, testConfig())

	_ = repo.EnqueueIPFSUnpin(context.Background(), keyA)
	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("drain A: %v", err)
	}
	if repo.state(keyA) != "unpinned" {
		t.Fatalf("A state = %q, want unpinned", repo.state(keyA))
	}
	if client.UnpinCount != 0 {
		t.Errorf("node unpin called %d times while CID still referenced, want 0", client.UnpinCount)
	}

	_ = repo.EnqueueIPFSUnpin(context.Background(), keyB)
	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("drain B: %v", err)
	}
	if repo.state(keyB) != "unpinned" {
		t.Fatalf("B state = %q, want unpinned", repo.state(keyB))
	}
	if client.UnpinCount != 1 {
		t.Errorf("node unpin called %d times after last reference, want 1", client.UnpinCount)
	}
}

// TestReconcileReArms: a failed PIN (no cid) re-arms to pending; a failed UNPIN
// (has cid) re-arms to unpinning — never re-pinning a delete.
func TestReconcileReArms(t *testing.T) {
	repo := newFakeRepo()
	repo.rows["k-pin"] = &sqlcgen.MediaIpfsPin{ObjectKey: "k-pin", State: "failed", Cid: ""}
	repo.rows["k-unpin"] = &sqlcgen.MediaIpfsPin{ObjectKey: "k-unpin", State: "failed", Cid: ipfs.RawLeafCIDv1([]byte("x"))}

	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	n, err := svc.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if n != 2 {
		t.Fatalf("re-armed %d, want 2", n)
	}
	if repo.state("k-pin") != "pending" {
		t.Errorf("failed pin re-armed to %q, want pending", repo.state("k-pin"))
	}
	if repo.state("k-unpin") != "unpinning" {
		t.Errorf("failed unpin re-armed to %q, want unpinning", repo.state("k-unpin"))
	}
}

// TestStatusAggregates: Status folds ledger counts into overall + per-class
// buckets and reflects node reachability.
func TestStatusAggregates(t *testing.T) {
	repo := newFakeRepo()
	repo.rows["a"] = &sqlcgen.MediaIpfsPin{ObjectKey: "a", MediaClass: "thumbnail", State: "pinned"}
	repo.rows["b"] = &sqlcgen.MediaIpfsPin{ObjectKey: "b", MediaClass: "thumbnail", State: "pending"}
	repo.rows["c"] = &sqlcgen.MediaIpfsPin{ObjectKey: "c", MediaClass: "user_avatar", State: "failed"}

	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Enabled || !st.NodeReachable {
		t.Errorf("enabled=%v node_reachable=%v, want both true", st.Enabled, st.NodeReachable)
	}
	if st.Pins.Pinned != 1 || st.Pins.Pending != 1 || st.Pins.Failed != 1 {
		t.Errorf("pins = %+v, want pinned=1 pending=1 failed=1", st.Pins)
	}
	if len(st.ByClass) != 2 {
		t.Fatalf("by_class len = %d, want 2", len(st.ByClass))
	}
	// Sorted: thumbnail, user_avatar.
	if st.ByClass[0].MediaClass != "thumbnail" || st.ByClass[0].Pinned != 1 || st.ByClass[0].Pending != 1 {
		t.Errorf("by_class[0] = %+v, want thumbnail pinned=1 pending=1", st.ByClass[0])
	}
}

// TestStatusNodeDown: an unreachable node yields node_reachable=false, never an
// error (IPFS is non-authoritative).
func TestStatusNodeDown(t *testing.T) {
	client := ipfs.NewFakeIPFSClient()
	client.Down = true
	svc := New(newFakeRepo(), &fakeLookups{}, newBlobs(t), client, testConfig())
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.NodeReachable {
		t.Error("node_reachable = true with node down, want false")
	}
}

// TestEnqueueUserImageGate: the enqueue helper writes a pending row for a listed
// active owner, and writes NOTHING for an unlisted owner (the privacy fence at the
// producing edge).
func TestEnqueueUserImageGate(t *testing.T) {
	userID := uuid.New()
	key := "avatars/users/" + userID.String() + ".png"

	// Listed + active ⇒ enqueued.
	repo := newFakeRepo()
	svc := New(repo, &fakeLookups{userActive: true, userUnlisted: false, userOK: true}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	if err := svc.EnqueueUserImage(context.Background(), userID, ClassUserAvatar, key); err != nil {
		t.Fatalf("EnqueueUserImage: %v", err)
	}
	if repo.state(key) != "pending" {
		t.Errorf("listed user: state = %q, want pending", repo.state(key))
	}

	// Unlisted ⇒ no row.
	repo2 := newFakeRepo()
	svc2 := New(repo2, &fakeLookups{userActive: true, userUnlisted: true, userOK: true}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	if err := svc2.EnqueueUserImage(context.Background(), userID, ClassUserAvatar, key); err != nil {
		t.Fatalf("EnqueueUserImage (unlisted): %v", err)
	}
	if _, ok := repo2.rows[key]; ok {
		t.Error("unlisted user: a pin row was enqueued, want none (privacy fence breach)")
	}
}

// TestSyncVideoPublishThenPrivate: publishing a public video enqueues its
// original + VP9/WebM alternate + image derivatives (P19.3); flipping it private
// then unpins ALL of the video's ledger rows (the video-level privacy fence).
func TestSyncVideoPublishThenPrivate(t *testing.T) {
	repo := newFakeRepo()
	vid := uuid.New()
	lk := &fakeLookups{
		videoPrivacy: "public", videoState: "published", videoOK: true,
		videoFiles: []VideoFileRef{
			{Kind: "thumbnail", StorageKey: "thumbnails/v.jpg"},
			{Kind: "original", StorageKey: "web-videos/v.mp4"},
			{Kind: "webm", StorageKey: "streaming-playlists/v/vp9.webm"},
		},
		captionKeys: []string{"captions/v/en.vtt"},
	}
	svc := New(repo, lk, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())

	if err := svc.SyncVideo(context.Background(), vid); err != nil {
		t.Fatalf("SyncVideo publish: %v", err)
	}
	for _, key := range []string{"thumbnails/v.jpg", "web-videos/v.mp4", "streaming-playlists/v/vp9.webm", "captions/v/en.vtt"} {
		if repo.state(key) != "pending" {
			t.Errorf("%s state = %q, want pending", key, repo.state(key))
		}
	}
	// The original is now enqueued (P19.3) — with the correct media class.
	if got := repo.rows["web-videos/v.mp4"].MediaClass; got != string(ClassVideoOriginal) {
		t.Errorf("original media_class = %q, want %q", got, ClassVideoOriginal)
	}
	if got := repo.rows["streaming-playlists/v/vp9.webm"].MediaClass; got != string(ClassWebM) {
		t.Errorf("webm media_class = %q, want %q", got, ClassWebM)
	}

	// Flip to private ⇒ ALL the video's ledger rows unpinned (not just derivatives).
	lk.videoPrivacy = "private"
	if err := svc.SyncVideo(context.Background(), vid); err != nil {
		t.Fatalf("SyncVideo private: %v", err)
	}
	for _, key := range []string{"thumbnails/v.jpg", "web-videos/v.mp4", "streaming-playlists/v/vp9.webm", "captions/v/en.vtt"} {
		if repo.state(key) != "unpinning" {
			t.Errorf("%s state after private = %q, want unpinning", key, repo.state(key))
		}
	}
}

// TestVideoPins: the detail read model returns the pinned original CID + gateway
// base, surfaces only 'pinned' rows with a VALIDATED CID, and reports ok=false
// when nothing is pinned.
func TestVideoPins(t *testing.T) {
	repo := newFakeRepo()
	vid := uuid.New()
	origCID := ipfs.RawLeafCIDv1([]byte("original-bytes"))
	seedPinned(repo, "web-videos/v.mp4", string(ClassVideoOriginal), origCID, vid)
	// A pinned thumbnail (not surfaced in the ipfs object) and a still-pending
	// caption (not yet pinned) must not appear.
	seedPinned(repo, "thumbnails/v.jpg", string(ClassThumbnail), ipfs.RawLeafCIDv1([]byte("thumb")), vid)
	repo.rows["captions/v/en.vtt"] = &sqlcgen.MediaIpfsPin{
		ObjectKey: "captions/v/en.vtt", MediaClass: string(ClassCaption), State: "pending",
		VideoID: pgtype.UUID{Bytes: vid, Valid: true},
	}
	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())

	pins, ok, err := svc.VideoPins(context.Background(), vid)
	if err != nil {
		t.Fatalf("VideoPins: %v", err)
	}
	if !ok {
		t.Fatal("VideoPins ok = false, want true (original is pinned)")
	}
	if pins.OriginalCID != origCID {
		t.Errorf("OriginalCID = %q, want %q", pins.OriginalCID, origCID)
	}
	if pins.HLSCID != "" {
		t.Errorf("HLSCID = %q, want empty (no HLS in P19.3)", pins.HLSCID)
	}
	if pins.GatewayURL != "https://gw.example.org" {
		t.Errorf("GatewayURL = %q, want the configured gateway", pins.GatewayURL)
	}

	// A video with nothing pinned ⇒ ok=false.
	if _, ok, _ := svc.VideoPins(context.Background(), uuid.New()); ok {
		t.Error("VideoPins ok = true for a video with no pins, want false")
	}
}

// TestVideoPinsRejectsInvalidCID: a corrupt/unvalidated CID in the ledger is
// NEVER surfaced (defense in depth for the gateway-URL construction).
func TestVideoPinsRejectsInvalidCID(t *testing.T) {
	repo := newFakeRepo()
	vid := uuid.New()
	seedPinned(repo, "web-videos/v.mp4", string(ClassVideoOriginal), "Qm-not-a-cidv1-../etc", vid)
	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())

	if _, ok, err := svc.VideoPins(context.Background(), vid); err != nil || ok {
		t.Errorf("VideoPins with an invalid CID: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// TestPinnedVideoIDs: the feed badge batch resolves exactly the videos with a
// pinned row; a video whose media is only pending/unpinned is not included.
func TestPinnedVideoIDs(t *testing.T) {
	repo := newFakeRepo()
	pinnedVid, pendingVid, absentVid := uuid.New(), uuid.New(), uuid.New()
	seedPinned(repo, "web-videos/a.mp4", string(ClassVideoOriginal), ipfs.RawLeafCIDv1([]byte("a")), pinnedVid)
	repo.rows["web-videos/b.mp4"] = &sqlcgen.MediaIpfsPin{
		ObjectKey: "web-videos/b.mp4", MediaClass: string(ClassVideoOriginal), State: "pending",
		VideoID: pgtype.UUID{Bytes: pendingVid, Valid: true},
	}
	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())

	got, err := svc.PinnedVideoIDs(context.Background(), []uuid.UUID{pinnedVid, pendingVid, absentVid})
	if err != nil {
		t.Fatalf("PinnedVideoIDs: %v", err)
	}
	if !got[pinnedVid] {
		t.Error("pinned video not reported pinned")
	}
	if got[pendingVid] {
		t.Error("pending-only video reported pinned, want not")
	}
	if got[absentVid] {
		t.Error("video with no ledger row reported pinned, want not")
	}
}

// TestReadModelsDisabled: with the mirror disabled the read models are inert
// (no pins, no badges) — the additive fields simply never populate.
func TestReadModelsDisabled(t *testing.T) {
	repo := newFakeRepo()
	vid := uuid.New()
	seedPinned(repo, "web-videos/v.mp4", string(ClassVideoOriginal), ipfs.RawLeafCIDv1([]byte("x")), vid)
	cfg := testConfig()
	cfg.Enabled = false
	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), cfg)

	if _, ok, _ := svc.VideoPins(context.Background(), vid); ok {
		t.Error("disabled VideoPins ok = true, want false")
	}
	if got, _ := svc.PinnedVideoIDs(context.Background(), []uuid.UUID{vid}); len(got) != 0 {
		t.Errorf("disabled PinnedVideoIDs = %v, want empty", got)
	}
}

// TestDisabledNoOps: with the mirror disabled every producing hook and the worker
// are inert.
func TestDisabledNoOps(t *testing.T) {
	repo := newFakeRepo()
	cfg := testConfig()
	cfg.Enabled = false
	svc := New(repo, &fakeLookups{userActive: true, userOK: true}, newBlobs(t), ipfs.NewFakeIPFSClient(), cfg)

	_ = svc.EnqueueUserImage(context.Background(), uuid.New(), ClassUserAvatar, "avatars/users/x.png")
	if len(repo.rows) != 0 {
		t.Error("disabled mirror enqueued a row, want none")
	}
	if n, _ := svc.DrainDue(context.Background(), 10); n != 0 {
		t.Errorf("disabled DrainDue = %d, want 0", n)
	}
	if n, _ := svc.Reconcile(context.Background()); n != 0 {
		t.Errorf("disabled Reconcile = %d, want 0", n)
	}
}

// TestWorkerPinsDirectoryTree (P19.4): a due HLS directory row (trailing-slash
// key) is add+pinned as ONE wrapped tree — the fake node stores a single car_root
// CID whose content-addressed bytes carry the RELATIVE segment paths, the VP9/WebM
// alternate under the same prefix is EXCLUDED, and cid==car_root.
func TestWorkerPinsDirectoryTree(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	vid := uuid.New()
	prefix := "streaming-playlists/" + vid.String() + "/"
	putBlob(t, blobs, prefix+"master.m3u8", "#EXTM3U\n720p/playlist.m3u8\n")
	putBlob(t, blobs, prefix+"720p/playlist.m3u8", "#EXTM3U\nseg_00000.ts\n")
	putBlob(t, blobs, prefix+"720p/seg_00000.ts", "ts-bytes")
	putBlob(t, blobs, prefix+"vp9.webm", "webm-alt-bytes") // separate class — must be excluded
	seedPending(repo, prefix, string(ClassHLS))

	svc := New(repo, &fakeLookups{}, blobs, client, testConfig())
	n, err := svc.DrainDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDue: %v", err)
	}
	if n != 1 {
		t.Fatalf("drained %d, want 1", n)
	}
	row := repo.rows[prefix]
	if row.State != "pinned" {
		t.Fatalf("state = %q, want pinned", row.State)
	}
	if row.Cid == "" || row.CarRoot != row.Cid {
		t.Fatalf("cid=%q car_root=%q, want equal + non-empty (directory root)", row.Cid, row.CarRoot)
	}
	if ipfs.ValidateCID(row.Cid) != nil {
		t.Errorf("car_root %q is not a valid CIDv1", row.Cid)
	}
	if client.AddCount != 1 {
		t.Errorf("AddCount = %d, want 1 (single directory add)", client.AddCount)
	}
	// The wrapped tree carries the HLS files by RELATIVE path (so {gateway}/ipfs/
	// {car_root}/720p/seg_00000.ts resolves) and NOT the VP9/WebM alternate's bytes.
	data, ok := client.Content(row.Cid)
	if !ok {
		t.Fatal("car_root content not stored on the fake node")
	}
	if !strings.Contains(string(data), "720p/seg_00000.ts") {
		t.Error("relative segment path missing from the wrapped tree")
	}
	if strings.Contains(string(data), "master.m3u8") == false {
		t.Error("relative master path missing from the wrapped tree")
	}
	if strings.Contains(string(data), "webm-alt-bytes") {
		t.Error("vp9.webm was wrapped into the HLS car_root, want excluded (separate class)")
	}
}

// TestOnTranscodeCompleteEnqueuesHLS (P19.4): the transcode-completion hook arms a
// media_class='hls' directory row (+ force-repins the VP9/WebM alternate) for an
// eligible video, and arms NOTHING for an ineligible (private) one — the privacy
// fence re-checked at completion. LIVE HLS never reaches this (only a finalized VOD
// job completes).
func TestOnTranscodeCompleteEnqueuesHLS(t *testing.T) {
	vid := uuid.New()
	hlsKey := "streaming-playlists/" + vid.String() + "/"
	webmKey := "streaming-playlists/" + vid.String() + "/vp9.webm"

	// Eligible ⇒ HLS directory row + webm force-repin.
	repo := newFakeRepo()
	lk := &fakeLookups{
		videoPrivacy: "public", videoState: "published", videoOK: true,
		videoFiles: []VideoFileRef{{Kind: "webm", StorageKey: webmKey}},
	}
	svc := New(repo, lk, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	if err := svc.OnTranscodeComplete(context.Background(), vid); err != nil {
		t.Fatalf("OnTranscodeComplete: %v", err)
	}
	if repo.state(hlsKey) != "pending" {
		t.Errorf("HLS row state = %q, want pending", repo.state(hlsKey))
	}
	if got := repo.rows[hlsKey].MediaClass; got != string(ClassHLS) {
		t.Errorf("HLS media_class = %q, want %q", got, ClassHLS)
	}
	if !repo.rows[hlsKey].VideoID.Valid {
		t.Error("HLS row has no video_id provenance (needed for cascade unpin)")
	}
	if repo.state(webmKey) != "pending" {
		t.Errorf("webm row state = %q, want pending (force-repinned at transcode completion)", repo.state(webmKey))
	}

	// Ineligible (private) ⇒ nothing armed here (privacy fence).
	repo2 := newFakeRepo()
	lk2 := &fakeLookups{
		videoPrivacy: "private", videoState: "published", videoOK: true,
		videoFiles: []VideoFileRef{{Kind: "webm", StorageKey: webmKey}},
	}
	svc2 := New(repo2, lk2, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	if err := svc2.OnTranscodeComplete(context.Background(), vid); err != nil {
		t.Fatalf("OnTranscodeComplete (private): %v", err)
	}
	if len(repo2.rows) != 0 {
		t.Errorf("private video armed %d rows at transcode completion, want 0 (privacy fence breach)", len(repo2.rows))
	}
}

// TestHLSPinLifecycle (P19.4) exercises the full HLS pin state machine end to end:
// first transcode → pinned car_root; re-transcode (wholesale-replaced content) →
// forced re-claim, new car_root, superseded root swap-unpinned; delete → the tree
// is unpinned (reference-checked) and the row goes terminal.
func TestHLSPinLifecycle(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	vid := uuid.New()
	prefix := "streaming-playlists/" + vid.String() + "/"
	lk := &fakeLookups{videoPrivacy: "public", videoState: "published", videoOK: true}
	svc := New(repo, lk, blobs, client, testConfig())
	ctx := context.Background()

	// --- first transcode ---
	putBlob(t, blobs, prefix+"master.m3u8", "v1-master")
	putBlob(t, blobs, prefix+"720p/playlist.m3u8", "v1-playlist")
	putBlob(t, blobs, prefix+"720p/seg_00000.ts", "v1-seg")
	if err := svc.OnTranscodeComplete(ctx, vid); err != nil {
		t.Fatalf("first OnTranscodeComplete: %v", err)
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if repo.state(prefix) != "pinned" {
		t.Fatalf("after first drain state = %q, want pinned", repo.state(prefix))
	}
	oldRoot := repo.rows[prefix].CarRoot
	if oldRoot == "" {
		t.Fatal("no car_root recorded on first pin")
	}
	if pinned, _ := client.IsPinned(ctx, oldRoot); !pinned {
		t.Fatal("old car_root not pinned on the node after first transcode")
	}

	// --- re-transcode: replace the tree content wholesale (new bytes ⇒ new root) ---
	putBlob(t, blobs, prefix+"master.m3u8", "v2-master-DIFFERENT")
	putBlob(t, blobs, prefix+"720p/seg_00000.ts", "v2-seg-DIFFERENT")
	if err := svc.OnTranscodeComplete(ctx, vid); err != nil {
		t.Fatalf("re-transcode OnTranscodeComplete: %v", err)
	}
	if repo.state(prefix) != "pending" {
		t.Fatalf("after re-transcode hook state = %q, want pending (forced re-claim)", repo.state(prefix))
	}
	if repo.rows[prefix].CarRoot != oldRoot {
		t.Error("force-repin cleared car_root before the worker ran; the old root must survive for the swap")
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("re-transcode drain: %v", err)
	}
	newRoot := repo.rows[prefix].CarRoot
	if newRoot == oldRoot {
		t.Fatal("car_root unchanged after re-transcode with different content")
	}
	if repo.state(prefix) != "pinned" {
		t.Errorf("after re-drain state = %q, want pinned", repo.state(prefix))
	}
	if pinned, _ := client.IsPinned(ctx, newRoot); !pinned {
		t.Error("new car_root not pinned after re-transcode")
	}
	if pinned, _ := client.IsPinned(ctx, oldRoot); pinned {
		t.Error("superseded car_root still pinned after re-transcode, want swap-unpinned")
	}

	// --- delete: unpin the HLS tree (reference-checked), row terminal ---
	if err := svc.UnpinVideo(ctx, vid); err != nil {
		t.Fatalf("UnpinVideo: %v", err)
	}
	if repo.state(prefix) != "unpinning" {
		t.Fatalf("after delete-enqueue state = %q, want unpinning", repo.state(prefix))
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("delete drain: %v", err)
	}
	if repo.state(prefix) != "unpinned" {
		t.Errorf("after delete drain state = %q, want unpinned (terminal)", repo.state(prefix))
	}
	if pinned, _ := client.IsPinned(ctx, newRoot); pinned {
		t.Error("car_root still pinned after delete, want unpinned")
	}
}

// TestHLSCIDInVideoPins (P19.4): once the HLS directory row is pinned, the detail
// read model surfaces its car_root as hls_cid (the P19.3 `ipfs` object is now
// truthful for HLS), alongside the configured gateway base.
func TestHLSCIDInVideoPins(t *testing.T) {
	repo := newFakeRepo()
	vid := uuid.New()
	hlsRoot := ipfs.DirCIDv1([]byte("hls-tree"))
	seedPinned(repo, "streaming-playlists/"+vid.String()+"/", string(ClassHLS), hlsRoot, vid)
	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())

	pins, ok, err := svc.VideoPins(context.Background(), vid)
	if err != nil || !ok {
		t.Fatalf("VideoPins ok=%v err=%v, want ok=true", ok, err)
	}
	if pins.HLSCID != hlsRoot {
		t.Errorf("HLSCID = %q, want %q", pins.HLSCID, hlsRoot)
	}
	if pins.GatewayURL != "https://gw.example.org" {
		t.Errorf("GatewayURL = %q, want the configured gateway", pins.GatewayURL)
	}
}

// TestPinLosesRaceToPrivacyFlip is the P19 audit BLOCKER regression. A public
// video's object is leased for pinning; MID-Add the owner flips it private, whose
// hook enqueues an unpin that flips the leased row 'pending'→'unpinning'. When Add
// returns, the state-guarded MarkIPFSPinned records the CID but KEEPS 'unpinning'
// (it never clobbers the newer visibility fact back to 'pinned'), and the worker
// then removes the just-pinned bytes inline. Final: row 'unpinned', node pin gone,
// the CID was recorded (so the unpin could target it), ipfs_pinned=false.
func TestPinLosesRaceToPrivacyFlip(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	vid := uuid.New()
	key := "web-videos/" + vid.String() + ".mp4"
	putBlob(t, blobs, key, "non-public-bytes")
	repo.rows[key] = &sqlcgen.MediaIpfsPin{
		ObjectKey: key, MediaClass: string(ClassVideoOriginal), State: "pending",
		VideoID: pgUUID(vid), NextAttemptAt: time.Now().UTC().Add(-time.Second),
	}
	// Mid-Add, the owner privatizes the video → the privacy hook unpins all of its
	// ledger rows (EnqueueIPFSUnpin flips this leased row to 'unpinning').
	fired := false
	client.AddHook = func() {
		if fired {
			return
		}
		fired = true
		_ = repo.EnqueueIPFSUnpin(context.Background(), key)
	}

	svc := New(repo, &fakeLookups{}, blobs, client, testConfig())
	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("DrainDue: %v", err)
	}

	row := repo.rows[key]
	if row.Cid == "" {
		t.Error("CID not recorded on the raced row; a later unpin could not target the node pin")
	}
	if row.State == "pinned" {
		t.Fatal("raced row is 'pinned' — non-public bytes permanently pinned to the public node (the BLOCKER)")
	}
	if row.State != "unpinned" {
		t.Errorf("state = %q, want unpinned (the worker removed the raced pin inline)", row.State)
	}
	if pinned, _ := client.IsPinned(context.Background(), row.Cid); pinned {
		t.Error("node pin still present for a now-private video — permanent public disclosure")
	}
	if client.UnpinCount != 1 {
		t.Errorf("node Unpin called %d times, want 1 (the raced bytes removed)", client.UnpinCount)
	}
	// Contract truthfulness: the read model must NOT report the raced video pinned.
	got, err := svc.PinnedVideoIDs(context.Background(), []uuid.UUID{vid})
	if err != nil {
		t.Fatalf("PinnedVideoIDs: %v", err)
	}
	if got[vid] {
		t.Error("raced (now-private) video reports ipfs_pinned=true — contract leak")
	}
}

// TestPinDoubleFlipConvergesToPinned: a public→private→public double-flip lands
// entirely DURING the Add. The NET visibility is public, so the row must converge
// to 'pinned' with the node pin intact — the guard must not strand it 'unpinning'
// (last writer = the latest visibility fact, not the stale mid-flight unpin).
func TestPinDoubleFlipConvergesToPinned(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	vid := uuid.New()
	key := "web-videos/" + vid.String() + ".mp4"
	putBlob(t, blobs, key, "public-bytes")
	repo.rows[key] = &sqlcgen.MediaIpfsPin{
		ObjectKey: key, MediaClass: string(ClassVideoOriginal), State: "pending",
		VideoID: pgUUID(vid), NextAttemptAt: time.Now().UTC().Add(-time.Second),
	}
	fired := false
	client.AddHook = func() {
		if fired {
			return
		}
		fired = true
		// private (→unpinning) then public again (→pending, re-armed) — both mid-Add.
		_ = repo.EnqueueIPFSUnpin(context.Background(), key)
		_, _ = repo.UpsertIPFSPinIntent(context.Background(), sqlcgen.UpsertIPFSPinIntentParams{
			ObjectKey: key, MediaClass: string(ClassVideoOriginal), VideoID: pgUUID(vid),
		})
	}

	svc := New(repo, &fakeLookups{}, blobs, client, testConfig())
	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("DrainDue: %v", err)
	}

	row := repo.rows[key]
	if row.State != "pinned" {
		t.Fatalf("state = %q, want pinned (net-public double-flip converges to pinned)", row.State)
	}
	if row.Cid == "" {
		t.Error("CID not recorded on the converged pin")
	}
	if pinned, _ := client.IsPinned(context.Background(), row.Cid); !pinned {
		t.Error("node pin missing after a net-public double-flip")
	}
	if client.UnpinCount != 0 {
		t.Errorf("node Unpin called %d times, want 0 (final state is public/pinned)", client.UnpinCount)
	}
	got, _ := svc.PinnedVideoIDs(context.Background(), []uuid.UUID{vid})
	if !got[vid] {
		t.Error("net-public video not reported pinned")
	}
}

// TestPinDirectoryLosesRaceToPrivacyFlip: the same lost-update race on the HLS
// directory-add path (pinDirectory). A mid-AddDirectory privatize must not leave
// the wrapped tree pinned — final 'unpinned', car_root gone from the node.
func TestPinDirectoryLosesRaceToPrivacyFlip(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	vid := uuid.New()
	prefix := "streaming-playlists/" + vid.String() + "/"
	putBlob(t, blobs, prefix+"master.m3u8", "#EXTM3U\n720p/playlist.m3u8\n")
	putBlob(t, blobs, prefix+"720p/seg_00000.ts", "ts-bytes")
	repo.rows[prefix] = &sqlcgen.MediaIpfsPin{
		ObjectKey: prefix, MediaClass: string(ClassHLS), State: "pending",
		VideoID: pgUUID(vid), NextAttemptAt: time.Now().UTC().Add(-time.Second),
	}
	fired := false
	client.AddHook = func() {
		if fired {
			return
		}
		fired = true
		_ = repo.EnqueueIPFSUnpin(context.Background(), prefix)
	}

	svc := New(repo, &fakeLookups{}, blobs, client, testConfig())
	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("DrainDue: %v", err)
	}

	row := repo.rows[prefix]
	if row.State == "pinned" {
		t.Fatal("raced HLS tree is 'pinned' — a now-private video's tree is publicly pinned")
	}
	if row.State != "unpinned" {
		t.Errorf("state = %q, want unpinned", row.State)
	}
	if row.CarRoot == "" {
		t.Error("car_root not recorded on the raced HLS row")
	}
	if pinned, _ := client.IsPinned(context.Background(), row.CarRoot); pinned {
		t.Error("HLS car_root still pinned on the node after the raced unpin")
	}
}

// TestReadModelsExcludeRacedRow: the read models the API trusts (VideoPins for the
// detail `ipfs` object, PinnedVideoIDs for the card badge) count ONLY 'pinned'
// rows, so a raced row still in the intermediate 'unpinning' state (a valid CID
// recorded, but the object is being removed) is NEVER surfaced as pinned — the
// secondary contract-leak flagged in the P19 audit.
func TestReadModelsExcludeRacedRow(t *testing.T) {
	repo := newFakeRepo()
	vid := uuid.New()
	cid := ipfs.RawLeafCIDv1([]byte("raced-bytes"))
	repo.rows["web-videos/x.mp4"] = &sqlcgen.MediaIpfsPin{
		ObjectKey: "web-videos/x.mp4", MediaClass: string(ClassVideoOriginal),
		State: "unpinning", Cid: cid, VideoID: pgUUID(vid),
	}
	svc := New(repo, &fakeLookups{}, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())

	if _, ok, err := svc.VideoPins(context.Background(), vid); err != nil || ok {
		t.Errorf("VideoPins on a raced 'unpinning' row: ok=%v err=%v, want ok=false", ok, err)
	}
	got, err := svc.PinnedVideoIDs(context.Background(), []uuid.UUID{vid})
	if err != nil {
		t.Fatalf("PinnedVideoIDs: %v", err)
	}
	if got[vid] {
		t.Error("raced 'unpinning' video reported ipfs_pinned=true, want false")
	}
}

// compile-time: sqlcgen.Queries satisfies Repository (production wiring).
var _ Repository = (*sqlcgen.Queries)(nil)
