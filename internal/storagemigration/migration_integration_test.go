//go:build integration

// Integration tests for storage migration require BOTH a live PostgreSQL with
// migrations applied (DATABASE_URL) and a live S3-compatible object store
// (S3_TEST_ENDPOINT). They self-skip when either is absent. Run with:
//
//	docker compose --profile core --profile storage up -d postgres migrate minio
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	S3_TEST_ENDPOINT=localhost:9000 \
//	go test -count=1 -tags=integration -race ./internal/storagemigration/...
//
// This is the proof the whole wave rests on: a REAL local→object-store move,
// verified by reading every object back out of the destination, with the
// safety rails (tamper detection, the media-GC interlock, dual-read) exercised
// against the same live pair of stores rather than against fakes.
package storagemigration

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

// newTargetBucket builds an S3 backend on a bucket of its own. A migration
// ENUMERATES THE WHOLE STORE, so a bucket shared with another test would make
// the object set nondeterministic — the isolation is part of what is being
// tested, not test hygiene.
func newTargetBucket(t *testing.T, name string) *storage.S3 {
	t.Helper()
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT not set; skipping object-store integration test")
	}
	useSSL := false
	if v := os.Getenv("S3_TEST_USE_SSL"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			t.Fatalf("parse S3_TEST_USE_SSL: %v", err)
		}
		useSSL = b
	}
	b, err := storage.NewS3(storage.S3Config{
		Endpoint:       endpoint,
		Bucket:         name,
		AccessKey:      envOr("S3_TEST_ACCESS_KEY", "vidra"),
		SecretKey:      envOr("S3_TEST_SECRET_KEY", "vidra-dev-secret"),
		UseSSL:         useSSL,
		ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := b.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket %q: %v", name, err)
	}
	return b
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// campaignFixture is one live database + one live object store, with every row
// this test created removed afterwards. The campaign cleanup matters beyond
// tidiness: the single-active partial unique index means a leaked live campaign
// would fail every later run.
type campaignFixture struct {
	st     *store.Store
	q      *sqlcgen.Queries
	source *storage.Local
	target *storage.S3
	suffix int64
}

func newCampaignFixture(t *testing.T, bucketSuffix string) *campaignFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	suffix := time.Now().UnixNano()
	f := &campaignFixture{
		st:     st,
		q:      st.Queries(),
		source: localAt(t, "source"),
		target: newTargetBucket(t, fmt.Sprintf("vidra-mig-%s-%d", bucketSuffix, suffix%1_000_000)),
		suffix: suffix,
	}
	// A campaign left behind by an earlier run (or an earlier subtest) would be
	// picked up as "the active campaign" and make this test read someone else's
	// state. Start from a clean ledger and leave one behind.
	f.clearCampaigns(t)
	t.Cleanup(func() { f.clearCampaigns(t) })
	return f
}

func (f *campaignFixture) clearCampaigns(t *testing.T) {
	t.Helper()
	if _, err := f.st.Pool.Exec(context.Background(), `DELETE FROM storage_migrations`); err != nil {
		t.Fatalf("clear campaigns: %v", err)
	}
}

// seedVideoFile creates the row chain a stored original really has, so the
// migration's cross-check against video_files.sha256 runs against a real row
// rather than a stub.
func (f *campaignFixture) seedVideoFile(t *testing.T, ctx context.Context, key, sha string, size int64) {
	t.Helper()
	u, err := f.q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     fmt.Sprintf("miggy-%d", f.suffix),
		Email:        fmt.Sprintf("miggy-%d@example.test", f.suffix),
		PasswordHash: "$2a$12$fakefakefakefakefakefakefakefakefakefakefakefakefakefe",
		Role:         "user",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID)
	})
	ch, err := f.q.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		OwnerID: u.ID, Handle: fmt.Sprintf("miggy-ch-%d", f.suffix), DisplayName: "Miggy"})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	v, err := f.q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: ch.ID, Title: "moveme", Privacy: "private", CommentsPolicy: "enabled"})
	if err != nil {
		t.Fatalf("create video: %v", err)
	}
	if _, err := f.q.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID: v.ID, Kind: "original", StorageKey: key, SizeBytes: size, Sha256: sha,
	}); err != nil {
		t.Fatalf("create video file: %v", err)
	}
}

// runToSynced ticks the two workers' units the way the workers do, until the
// campaign settles.
func runToSynced(t *testing.T, ctx context.Context, svc *Service) {
	t.Helper()
	for i := 0; i < 30; i++ {
		if err := svc.SweepOnce(ctx); err != nil {
			t.Fatalf("SweepOnce: %v", err)
		}
		if _, err := svc.CopyOnce(ctx, 4); err != nil {
			t.Fatalf("CopyOnce: %v", err)
		}
	}
}

// TestLocalToObjectStoreEndToEnd is the wave's headline proof: a real local
// media root is moved into a real object store, object by object, and every
// copy is verified by reading it back OUT of the object store and re-hashing it
// there.
func TestLocalToObjectStoreEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	f := newCampaignFixture(t, "e2e")

	// A realistic spread: an original with a video_files row and a recorded
	// hash, a poster, and an HLS tree (which has no per-object database row at
	// all — the migration is the only thing that ever verifies those).
	original := fmt.Sprintf("web-videos/%s.mp4", uuid.New())
	bodies := map[string]string{
		original:                                 "pretend this is an mp4 original",
		"thumbnails/poster.jpg":                  "poster bytes",
		"streaming-playlists/vid/r0/master.m3u8": "#EXTM3U\n#EXT-X-VERSION:3\n",
		"streaming-playlists/vid/r0/0.ts":        "segment zero bytes",
		"streaming-playlists/vid/r0/1.ts":        "segment one bytes",
		storage.OwnerMarkerKey:                   uuid.New().String(),
	}
	for key, body := range bodies {
		put(t, f.source, key, body)
	}
	f.seedVideoFile(t, ctx, original, sha256Of(bodies[original]), int64(len(bodies[original])))

	svc := NewService(f.q, f.source, f.target, Config{})
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The trigger projects the campaign into the operator-facing job run the
	// moment it exists — no HTTP surface of its own, and no worker had to run.
	assertJobRun(t, ctx, f, camp.ID, "running", StateEnumerating)

	runToSynced(t, ctx, svc)

	got, counts, err := svc.Get(ctx, camp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateSynced {
		t.Fatalf("state = %q (last_error %q), want %q", got.State, got.LastError, StateSynced)
	}
	if counts[ObjectVerified] != int64(len(bodies)) || counts[ObjectFailed] != 0 {
		t.Fatalf("object states = %v, want %d verified / 0 failed", counts, len(bodies))
	}
	if got.ObjectsTotal != int64(len(bodies)) || got.ObjectsDone != int64(len(bodies)) {
		t.Errorf("counters = %d/%d, want %d/%d", got.ObjectsDone, got.ObjectsTotal, len(bodies), len(bodies))
	}

	// The assertion that matters: the bytes are IN THE OBJECT STORE, under the
	// SAME keys, and hash to what the ledger recorded.
	for key, body := range bodies {
		if got := read(t, f.target, key); got != body {
			t.Errorf("object store %q = %q, want %q", key, got, body)
		}
		var state, sum string
		var size int64
		if err := f.st.Pool.QueryRow(ctx,
			`SELECT state, sha256, byte_size FROM storage_migration_objects WHERE object_key = $1`, key,
		).Scan(&state, &sum, &size); err != nil {
			t.Fatalf("read ledger row for %q: %v", key, err)
		}
		if state != ObjectVerified || sum != sha256Of(body) || size != int64(len(body)) {
			t.Errorf("%q ledger row: state=%q sha256=%q size=%d, want verified/%s/%d",
				key, state, sum, size, sha256Of(body), len(body))
		}
	}
	// Nothing is deleted from the source before cutover + grace.
	for key := range bodies {
		if ok, _ := f.source.Exists(ctx, key); !ok {
			t.Errorf("source lost %q before cutover", key)
		}
	}

	t.Run("the delta pass picks up an object written after enumeration", func(t *testing.T) {
		late := "thumbnails/uploaded-during-the-move.jpg"
		put(t, f.source, late, "arrived late")
		runToSynced(t, ctx, svc)

		if got := read(t, f.target, late); got != "arrived late" {
			t.Errorf("late object = %q", got)
		}
		var state string
		if err := f.st.Pool.QueryRow(ctx,
			`SELECT state FROM storage_migration_objects WHERE object_key = $1`, late).Scan(&state); err != nil {
			t.Fatalf("read late ledger row: %v", err)
		}
		if state != ObjectVerified {
			t.Errorf("late object state = %q, want %q", state, ObjectVerified)
		}
		if got, _, _ := svc.Get(ctx, camp.ID); got.State != StateSynced || got.ObjectsTotal != int64(len(bodies))+1 {
			t.Errorf("after the delta pass: state %q total %d", got.State, got.ObjectsTotal)
		}
	})

	t.Run("dual-read serves an object that is only in the other store", func(t *testing.T) {
		// The migration window in miniature: an object present ONLY in the store
		// the instance is not serving from must still reach a viewer.
		onlyInTarget := "thumbnails/only-in-the-new-store.jpg"
		put(t, f.target, onlyInTarget, "new store only")
		fallback := storage.NewFallback(f.source, f.target, nil)

		if got := read(t, fallback, onlyInTarget); got != "new store only" {
			t.Errorf("fallback read = %q, want %q", got, "new store only")
		}
		if ok, err := fallback.Exists(ctx, onlyInTarget); err != nil || !ok {
			t.Errorf("fallback Exists = %v, %v; want true, nil", ok, err)
		}
		// And the primary still wins when it has the object.
		if got := read(t, fallback, original); got != bodies[original] {
			t.Errorf("fallback primary read = %q", got)
		}
	})

	t.Run("media GC is forced to dry-run while the campaign is live", func(t *testing.T) {
		// Real interlock, real query: mediagc asks the same HasActiveStorageMigration
		// cmd/api wires, against the campaign this test just started.
		gc := mediagc.NewService(f.q, f.source,
			mediagc.WithBucketOwnership(mediagc.OwnershipOwned),
			mediagc.WithActiveMigrationCheck(f.q.HasActiveStorageMigration))
		res, err := gc.Sweep(ctx, false)
		if err != nil {
			t.Fatalf("gc sweep: %v", err)
		}
		if !res.ForcedDryRun || res.ForcedDryRunReason != mediagc.ReasonMigrationActive {
			t.Fatalf("gc result = forced %v reason %q, want a forced dry run for %q",
				res.ForcedDryRun, res.ForcedDryRunReason, mediagc.ReasonMigrationActive)
		}
		if res.Deleted != 0 {
			t.Errorf("gc deleted %d objects during a live migration", res.Deleted)
		}
		// The orphan list is still the full report, and the objects survive.
		for key := range bodies {
			if ok, _ := f.source.Exists(ctx, key); !ok {
				t.Errorf("gc deleted %q during a live migration", key)
			}
		}

		// Ending the campaign releases the interlock, and only then.
		if _, err := svc.Cancel(ctx, camp.ID); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		assertJobRun(t, ctx, f, camp.ID, "cancelled", StateCancelled)
		res, err = gc.Sweep(ctx, false)
		if err != nil {
			t.Fatalf("gc sweep after cancel: %v", err)
		}
		if res.ForcedDryRun {
			t.Errorf("gc is still forced to dry-run after the campaign was cancelled (reason %q)", res.ForcedDryRunReason)
		}
	})
}

// TestTamperedObjectStoreCopyFailsVerification proves the end-to-end check is
// real against a real object store: the digest taken while streaming out of the
// source is NOT what the row records — the object is read back out of the
// destination and hashed there, so a store that quietly kept different bytes is
// caught before its source copy is ever deleted.
func TestTamperedObjectStoreCopyFailsVerification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	f := newCampaignFixture(t, "tamper")

	key := "web-videos/tampered.mp4"
	put(t, f.source, key, "the bytes that were sent")

	// A destination that stores something other than what it was given, which is
	// what a silently lossy object store looks like from the outside.
	svc := NewService(f.q, f.source, &corruptingBackend{Backend: f.target}, Config{})
	if _, err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	runToSynced(t, ctx, svc)

	var state, lastError string
	if err := f.st.Pool.QueryRow(ctx,
		`SELECT state, last_error FROM storage_migration_objects WHERE object_key = $1`, key,
	).Scan(&state, &lastError); err != nil {
		t.Fatalf("read ledger row: %v", err)
	}
	if state != ObjectFailed {
		t.Fatalf("state = %q, want %q — a corrupted copy must never be recorded as verified", state, ObjectFailed)
	}
	if lastError != failVerification {
		t.Errorf("last_error = %q, want %q", lastError, failVerification)
	}
	// And what is actually in the bucket is indeed not what was sent, which is
	// the condition the check detected rather than an artefact of the test.
	if got := read(t, f.target, key); got == "the bytes that were sent" {
		t.Error("the corrupting wrapper did not corrupt anything; the test proves nothing")
	}
}

// assertJobRun checks the trigger's projection: the admin jobs browser reads
// job_runs generically, so a campaign appears there with no Go code of its own.
func assertJobRun(t *testing.T, ctx context.Context, f *campaignFixture, id uuid.UUID, wantState, wantStage string) {
	t.Helper()
	var state, stage, queue, runType string
	var progress *int16
	if err := f.st.Pool.QueryRow(ctx,
		`SELECT state, stage, queue, type, progress_percent FROM job_runs WHERE queue = 'storage_migrations' AND source_id = $1`,
		id.String(),
	).Scan(&state, &stage, &queue, &runType, &progress); err != nil {
		t.Fatalf("read job_runs projection: %v", err)
	}
	if state != wantState || stage != wantStage {
		t.Errorf("job run state/stage = %q/%q, want %q/%q", state, stage, wantState, wantStage)
	}
	if runType != "storage_migration" {
		t.Errorf("job run type = %q", runType)
	}
	if progress == nil || *progress < 0 || *progress > 100 {
		t.Errorf("job run progress_percent = %v, want an honest 0-100", progress)
	}
	var events int
	if err := f.st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM job_events e JOIN job_runs r ON r.id = e.job_id
		 WHERE r.queue = 'storage_migrations' AND r.source_id = $1`, id.String()).Scan(&events); err != nil {
		t.Fatalf("count job_events: %v", err)
	}
	if events == 0 {
		t.Error("no job_events were recorded for the campaign; the executions timeline would be empty")
	}
}
