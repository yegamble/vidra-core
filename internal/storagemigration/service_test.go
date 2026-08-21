package storagemigration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

func localAt(t *testing.T, name string) *storage.Local {
	t.Helper()
	b, err := storage.NewLocal(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return b
}

func sha256Of(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func put(t *testing.T, b storage.Backend, key, body string) {
	t.Helper()
	if _, err := b.Put(context.Background(), key, strings.NewReader(body)); err != nil {
		t.Fatalf("Put %q: %v", key, err)
	}
}

func read(t *testing.T, b storage.Backend, key string) string {
	t.Helper()
	rc, err := b.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open %q: %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return string(data)
}

// newTestService wires a service over two local stores and a fake repository,
// with a zero grace period so the delete-source phase is reachable in a test.
func newTestService(t *testing.T) (*Service, *fakeRepo, *storage.Local, *storage.Local) {
	t.Helper()
	src, dst := localAt(t, "source"), localAt(t, "target")
	repo := newFakeRepo()
	return NewService(repo, src, dst, Config{}), repo, src, dst
}

// drain runs sweep+copy passes until the campaign stops changing, which is what
// the two workers do on their tickers.
func drain(t *testing.T, svc *Service) {
	t.Helper()
	for i := 0; i < 20; i++ {
		if err := svc.SweepOnce(context.Background()); err != nil {
			t.Fatalf("SweepOnce: %v", err)
		}
		if _, err := svc.CopyOnce(context.Background(), 8); err != nil {
			t.Fatalf("CopyOnce: %v", err)
		}
	}
}

// TestMigrationCopiesVerifiesAndSyncs is the happy path end to end over local
// stores: every object arrives byte-identical under the SAME key, and the
// campaign only claims to be synced once that is true.
func TestMigrationCopiesVerifiesAndSyncs(t *testing.T) {
	ctx := context.Background()
	svc, repo, src, dst := newTestService(t)

	bodies := map[string]string{
		"web-videos/a.mp4":                     "the original bytes",
		"thumbnails/a.jpg":                     "poster",
		"streaming-playlists/a/r0/master.m3u8": "#EXTM3U",
		"streaming-playlists/a/r0/0.ts":        "segment zero",
		storage.OwnerMarkerKey:                 "instance-identity",
	}
	for key, body := range bodies {
		put(t, src, key, body)
	}
	// One object with a recorded hash, so the cross-check path runs on the happy
	// path too rather than only in its failure test.
	repo.fileHashes["web-videos/a.mp4"] = sha256Of(bodies["web-videos/a.mp4"])

	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if camp.State != StateEnumerating {
		t.Errorf("new campaign state = %q, want %q", camp.State, StateEnumerating)
	}

	drain(t, svc)

	got, counts, err := svc.Get(ctx, camp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateSynced {
		t.Fatalf("state = %q (last_error %q), want %q", got.State, got.LastError, StateSynced)
	}
	if got.ObjectsTotal != int64(len(bodies)) || got.ObjectsDone != int64(len(bodies)) || got.ObjectsFailed != 0 {
		t.Errorf("counters = total %d done %d failed %d, want %d/%d/0",
			got.ObjectsTotal, got.ObjectsDone, got.ObjectsFailed, len(bodies), len(bodies))
	}
	if counts[ObjectVerified] != int64(len(bodies)) {
		t.Errorf("verified objects = %d, want %d", counts[ObjectVerified], len(bodies))
	}
	for key, body := range bodies {
		if got := read(t, dst, key); got != body {
			t.Errorf("target %q = %q, want %q", key, got, body)
		}
		if o := repo.object(key); o.Sha256 != sha256Of(body) || o.ByteSize != int64(len(body)) {
			t.Errorf("%q recorded sha256=%q size=%d, want %q/%d", key, o.Sha256, o.ByteSize, sha256Of(body), len(body))
		}
	}
	// Keys are unchanged by the move: the IPFS pin ledger keys on them.
	if ok, _ := src.Exists(ctx, "web-videos/a.mp4"); !ok {
		t.Error("the source copy was removed before cutover; nothing may be deleted until the grace period after the env swap")
	}
}

// TestDeltaPassPicksUpAnObjectWrittenAfterEnumeration is what makes "synced"
// mean anything on a live instance: uploads keep landing in the source while the
// copy runs.
func TestDeltaPassPicksUpAnObjectWrittenAfterEnumeration(t *testing.T) {
	ctx := context.Background()
	svc, repo, src, dst := newTestService(t)
	put(t, src, "web-videos/first.mp4", "one")

	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(t, svc)
	if got, _, _ := svc.Get(ctx, camp.ID); got.State != StateSynced {
		t.Fatalf("state before the new upload = %q, want synced", got.State)
	}

	// A viewer uploads while the campaign sits in 'synced'.
	put(t, src, "web-videos/second.mp4", "two")
	drain(t, svc)

	if got := read(t, dst, "web-videos/second.mp4"); got != "two" {
		t.Errorf("late object in target = %q, want %q", got, "two")
	}
	if s := repo.state("web-videos/second.mp4"); s != ObjectVerified {
		t.Errorf("late object state = %q, want %q", s, ObjectVerified)
	}
	if got, _, _ := svc.Get(ctx, camp.ID); got.State != StateSynced || got.ObjectsTotal != 2 {
		t.Errorf("after the delta pass: state %q total %d, want synced/2", got.State, got.ObjectsTotal)
	}
}

// TestTamperedTargetFailsVerification is the whole point of re-reading the
// object out of the target. The copy-time digest is taken from the bytes read
// off the SOURCE, so a target that stored something else would otherwise be
// recorded as verified — and its source copy deleted a week later.
func TestTamperedTargetFailsVerification(t *testing.T) {
	ctx := context.Background()
	src, dst := localAt(t, "source"), localAt(t, "target")
	repo := newFakeRepo()
	svc := NewService(repo, src, &corruptingBackend{Backend: dst}, Config{})

	put(t, src, "web-videos/a.mp4", "the original bytes")
	if _, err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(t, svc)

	o := repo.object("web-videos/a.mp4")
	if o.State != ObjectFailed {
		t.Fatalf("state = %q, want %q — a corrupted copy must never be recorded as verified", o.State, ObjectFailed)
	}
	if o.LastError != failVerification {
		t.Errorf("last_error = %q, want %q", o.LastError, failVerification)
	}
	if strings.Contains(o.LastError, "web-videos") {
		t.Error("last_error carries an object key; this column is projected into the admin jobs overview")
	}
}

// TestSourceHashMismatchIsTerminal: a faithful copy of a source object that no
// longer matches its video_files row is a corrupt SOURCE. Retrying reproduces
// the same wrong bytes, so it dead-letters in front of an operator instead.
func TestSourceHashMismatchIsTerminal(t *testing.T) {
	ctx := context.Background()
	svc, repo, src, _ := newTestService(t)
	put(t, src, "web-videos/a.mp4", "what is actually stored")
	repo.fileHashes["web-videos/a.mp4"] = sha256Of("what the database says is stored")

	if _, err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(t, svc)

	o := repo.object("web-videos/a.mp4")
	if o.State != ObjectFailed || o.LastError != failRecordedHash {
		t.Errorf("state/last_error = %q/%q, want %q/%q", o.State, o.LastError, ObjectFailed, failRecordedHash)
	}
	// A dead-lettered object is a reported fact, not unfinished work: the
	// campaign still reaches synced so an operator can decide about it.
	if camp, _, _ := svc.Get(ctx, mustActive(t, repo)); camp.State != StateSynced || camp.ObjectsFailed != 1 {
		t.Errorf("campaign = %q failed=%d, want synced/1", camp.State, camp.ObjectsFailed)
	}
}

// TestMissingSourceObjectIsTerminal: the store listed a key and then did not
// have it. Re-reading will not produce it.
func TestMissingSourceObjectIsTerminal(t *testing.T) {
	ctx := context.Background()
	svc, repo, src, _ := newTestService(t)
	put(t, src, "web-videos/a.mp4", "here for now")
	if _, err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Enumerate, then delete underneath the campaign.
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if err := src.Delete(ctx, "web-videos/a.mp4"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CopyOnce(ctx, 4); err != nil {
		t.Fatalf("CopyOnce: %v", err)
	}

	o := repo.object("web-videos/a.mp4")
	if o.State != ObjectFailed || o.LastError != failSourceMissing {
		t.Errorf("state/last_error = %q/%q, want %q/%q", o.State, o.LastError, ObjectFailed, failSourceMissing)
	}
	if o.Attempts != 1 {
		t.Errorf("attempts = %d, want 1: a missing object must not burn the whole retry budget first", o.Attempts)
	}
}

// TestTransientFailureRetriesThenDeadLetters: a store that is merely unreachable
// must not lose objects on the first tick, and must not retry forever either.
func TestTransientFailureRetriesThenDeadLetters(t *testing.T) {
	ctx := context.Background()
	src := localAt(t, "source")
	repo := newFakeRepo()
	flaky := &failingBackend{}
	svc := NewService(repo, src, flaky, Config{})
	put(t, src, "web-videos/a.mp4", "bytes")

	if _, err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	for attempt := 1; attempt < maxAttempts; attempt++ {
		if _, err := svc.CopyOnce(ctx, 4); err != nil {
			t.Fatalf("CopyOnce: %v", err)
		}
		o := repo.object("web-videos/a.mp4")
		if o.State != ObjectPending || o.LastError != failTransientCopy {
			t.Fatalf("attempt %d: state/last_error = %q/%q, want pending/%q", attempt, o.State, o.LastError, failTransientCopy)
		}
		if !o.NextAttemptAt.After(time.Now()) {
			t.Fatalf("attempt %d: next_attempt_at is not in the future; the retry has no backoff", attempt)
		}
		// The backoff would otherwise keep the row out of reach of the next pass.
		repo.mu.Lock()
		repo.objects["web-videos/a.mp4"].NextAttemptAt = time.Now().Add(-time.Second)
		repo.mu.Unlock()
	}
	if _, err := svc.CopyOnce(ctx, 4); err != nil {
		t.Fatalf("CopyOnce: %v", err)
	}
	if o := repo.object("web-videos/a.mp4"); o.State != ObjectFailed || o.LastError != failAttemptsSpent {
		t.Errorf("after %d attempts: state/last_error = %q/%q, want failed/%q", maxAttempts, o.State, o.LastError, failAttemptsSpent)
	}
}

// TestCutoverGraceAndSourceDeletion walks the load-bearing half of the design:
// the operator swaps BOTH environment sets, the api notices by comparing store
// identities, the grace clock runs, and only then are the old copies deleted.
func TestCutoverGraceAndSourceDeletion(t *testing.T) {
	ctx := context.Background()
	src, dst := localAt(t, "source"), localAt(t, "target")
	repo := newFakeRepo()
	forward := NewService(repo, src, dst, Config{})

	put(t, src, "web-videos/a.mp4", "bytes a")
	put(t, src, "thumbnails/a.jpg", "bytes b")
	camp, err := forward.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(t, forward)
	if got, _, _ := forward.Get(ctx, camp.ID); got.State != StateSynced {
		t.Fatalf("state = %q, want synced", got.State)
	}

	// THE ENV SWAP: STORAGE_* now names the new store and
	// STORAGE_MIGRATION_TARGET_* names the old one. Same two backends, other way
	// round — which is exactly what the process sees after the restart.
	swapped := NewService(repo, dst, src, Config{Grace: time.Hour})
	if err := swapped.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce after swap: %v", err)
	}
	got, _, _ := swapped.Get(ctx, camp.ID)
	if got.State != StateCutover {
		t.Fatalf("state after the env swap = %q, want %q", got.State, StateCutover)
	}
	if got.ObservedCutoverAt == nil {
		t.Fatal("observed_cutover_at was not recorded; the grace period has no clock")
	}

	// Inside the grace window nothing is deleted — that window IS the undo.
	if err := swapped.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce in grace: %v", err)
	}
	if got, _, _ := swapped.Get(ctx, camp.ID); got.State != StateCutover {
		t.Errorf("state during grace = %q, want it to stay at cutover", got.State)
	}
	if ok, _ := src.Exists(ctx, "web-videos/a.mp4"); !ok {
		t.Fatal("a source object was deleted during the grace period")
	}

	// Grace elapsed.
	elapsed := NewService(repo, dst, src, Config{Grace: 0})
	if err := elapsed.SweepOnce(ctx); err != nil { // cutover -> deleting_source
		t.Fatalf("SweepOnce: %v", err)
	}
	if got, _, _ := elapsed.Get(ctx, camp.ID); got.State != StateDeletingSource {
		t.Fatalf("state after grace = %q, want %q", got.State, StateDeletingSource)
	}
	if err := elapsed.SweepOnce(ctx); err != nil { // delete the batch, then finish
		t.Fatalf("SweepOnce: %v", err)
	}
	if err := elapsed.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	final, counts, err := elapsed.Get(ctx, camp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != StateDone {
		t.Errorf("final state = %q (last_error %q), want %q", final.State, final.LastError, StateDone)
	}
	if counts[ObjectSourceDeleted] != 2 {
		t.Errorf("source_deleted objects = %d, want 2", counts[ObjectSourceDeleted])
	}
	for _, key := range []string{"web-videos/a.mp4", "thumbnails/a.jpg"} {
		if ok, _ := src.Exists(ctx, key); ok {
			t.Errorf("source still holds %q after the campaign completed", key)
		}
		if ok, _ := dst.Exists(ctx, key); !ok {
			t.Errorf("target lost %q", key)
		}
	}
}

// completeCampaign drives one campaign the whole way — start, copy, the
// operator's environment swap, zero grace, source deletion — and returns the
// finished campaign together with everything the service logged on the way.
func completeCampaign(t *testing.T, repo *fakeRepo, src, dst *storage.Local) (Campaign, string) {
	t.Helper()
	ctx := context.Background()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	forward := NewService(repo, src, dst, Config{Logger: logger})
	camp, err := forward.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(t, forward)
	if got, _, _ := forward.Get(ctx, camp.ID); got.State != StateSynced {
		t.Fatalf("state before the swap = %q (last_error %q), want synced", got.State, got.LastError)
	}

	// THE ENV SWAP, with no grace window so the delete phase is reachable in a
	// test. synced -> cutover -> deleting_source -> done is three sweeps.
	swapped := NewService(repo, dst, src, Config{Grace: 0, Logger: logger})
	for i := 0; i < 5; i++ {
		if err := swapped.SweepOnce(ctx); err != nil {
			t.Fatalf("SweepOnce after the swap: %v", err)
		}
	}
	got, _, err := swapped.Get(ctx, camp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateDone {
		t.Fatalf("state = %q (last_error %q), want %q", got.State, got.LastError, StateDone)
	}
	return got, logs.String()
}

// TestCompletedMigrationClaimsTheDestinationStore closes the hole a real move
// exposed: the ownership marker is otherwise only written by boot-time claiming
// (which claims a store this boot CREATED or found EMPTY) and by the admin adopt
// endpoint, and a migration's destination is neither by the time anyone boots on
// it. Nothing was left to tell media GC that the store now being served belongs
// to this install, so it stayed silently non-destructive.
func TestCompletedMigrationClaimsTheDestinationStore(t *testing.T) {
	ctx := context.Background()
	src, dst := localAt(t, "source"), localAt(t, "target")
	repo := newFakeRepo()
	put(t, src, "web-videos/a.mp4", "bytes a")
	put(t, src, "thumbnails/a.jpg", "bytes b")

	_, logs := completeCampaign(t, repo, src, dst)

	marker, found, err := storage.ReadOwnerMarker(ctx, dst)
	if err != nil || !found {
		t.Fatalf("ReadOwnerMarker on the destination = (%q, %v, %v); a completed migration must leave the new store owned", marker, found, err)
	}
	if marker != repo.identity.String() {
		t.Errorf("destination marker = %q, want this install's identity %q", marker, repo.identity)
	}
	if !strings.Contains(logs, "claimed the destination store") {
		t.Errorf("the claim was not logged; an operator has to be able to see it happened:\n%s", logs)
	}
	// And it went to the store being SERVED, not the one just emptied. Writing it
	// through the migration-target handle is the same bug inverted.
	if _, found, _ := storage.ReadOwnerMarker(ctx, src); found {
		t.Error("the marker was written to the decommissioned source; at 'done' the PRIMARY handle is the destination")
	}
}

// TestCompletedMigrationNeverOverwritesAForeignOwnershipMarker is the
// shared-store case the whole ownership rail exists for. Taking the store over
// silently would re-arm destructive GC across another install's media.
func TestCompletedMigrationNeverOverwritesAForeignOwnershipMarker(t *testing.T) {
	ctx := context.Background()
	src, dst := localAt(t, "source"), localAt(t, "target")
	repo := newFakeRepo()
	put(t, src, "web-videos/a.mp4", "bytes a")

	const theirs = "11111111-1111-4111-8111-111111111111"
	if err := storage.WriteOwnerMarker(ctx, dst, theirs); err != nil {
		t.Fatalf("WriteOwnerMarker: %v", err)
	}

	_, logs := completeCampaign(t, repo, src, dst)

	marker, _, err := storage.ReadOwnerMarker(ctx, dst)
	if err != nil {
		t.Fatalf("ReadOwnerMarker: %v", err)
	}
	if marker != theirs {
		t.Fatalf("destination marker = %q, want it left at %q", marker, theirs)
	}
	if !strings.Contains(logs, "ANOTHER Vidra install's ownership marker") {
		t.Errorf("the conflict was not logged loudly:\n%s", logs)
	}
	if strings.Contains(logs, theirs) {
		t.Error("the other install's identity was logged as a plain value an operator could mistake for their own")
	}
}

// TestCompletedMigrationWithoutAnInstanceIdentityStillFinishes: an install whose
// migrations have not been run has no identity to stamp. That costs the marker —
// and the operator an adopt — but it must never cost the campaign, whose bytes
// are already moved and whose source is already gone.
func TestCompletedMigrationWithoutAnInstanceIdentityStillFinishes(t *testing.T) {
	ctx := context.Background()
	src, dst := localAt(t, "source"), localAt(t, "target")
	repo := newFakeRepo()
	repo.identityErr = errors.New("no instance identity row")
	put(t, src, "web-videos/a.mp4", "bytes a")

	final, logs := completeCampaign(t, repo, src, dst)

	if final.State != StateDone {
		t.Errorf("state = %q, want %q", final.State, StateDone)
	}
	if _, found, _ := storage.ReadOwnerMarker(ctx, dst); found {
		t.Error("a marker was written with no identity to write")
	}
	if !strings.Contains(logs, "could not read this instance's identity") {
		t.Errorf("the skipped claim was not explained:\n%s", logs)
	}
}

// TestCopyRefusesWhenTheStoresAreTheOtherWayRound is the guard that keeps a
// half-finished env swap from copying the store being decommissioned back over
// the live one. It is the single most destructive thing this package could do.
func TestCopyRefusesWhenTheStoresAreTheOtherWayRound(t *testing.T) {
	ctx := context.Background()
	src, dst := localAt(t, "source"), localAt(t, "target")
	repo := newFakeRepo()
	forward := NewService(repo, src, dst, Config{})
	put(t, src, "web-videos/a.mp4", "source bytes")
	if _, err := forward.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := forward.SweepOnce(ctx); err != nil { // enumerate -> copying
		t.Fatalf("SweepOnce: %v", err)
	}
	// The new store already has a NEWER version of this object (the instance has
	// been serving from it). Copying backwards would destroy it.
	put(t, dst, "web-videos/a.mp4", "newer bytes written after cutover")

	swapped := NewService(repo, dst, src, Config{Grace: time.Hour})
	n, err := swapped.CopyOnce(ctx, 4)
	if err != nil {
		t.Fatalf("CopyOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("claimed %d objects with the stores reversed; want 0", n)
	}
	if got := read(t, dst, "web-videos/a.mp4"); got != "newer bytes written after cutover" {
		t.Errorf("the live store was overwritten with the old store's copy: %q", got)
	}
}

// TestSweepRefusesUnknownStores: neither reading of the topology holds, so
// acting would mean guessing which handle is the source — and one of the two
// guesses deletes the library.
func TestSweepRefusesUnknownStores(t *testing.T) {
	ctx := context.Background()
	svc, repo, src, _ := newTestService(t)
	put(t, src, "web-videos/a.mp4", "bytes")
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	stranger := NewService(repo, localAt(t, "somewhere-else"), localAt(t, "elsewhere"), Config{})
	if err := stranger.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	got, _, _ := stranger.Get(ctx, camp.ID)
	if got.State != StateEnumerating {
		t.Errorf("state = %q; a process holding unknown stores must not advance the campaign", got.State)
	}
	if got.LastError == "" {
		t.Error("no diagnostic was recorded; a stalled campaign has to say why")
	}
	if n, _ := stranger.CopyOnce(ctx, 4); n != 0 {
		t.Errorf("claimed %d objects with unknown stores; want 0", n)
	}
}

// TestStartGuards covers the refusals an operator can actually hit.
func TestStartGuards(t *testing.T) {
	ctx := context.Background()

	t.Run("no target configured", func(t *testing.T) {
		svc := NewService(newFakeRepo(), localAt(t, "source"), nil, Config{})
		if svc.Enabled() {
			t.Error("Enabled() is true with no target")
		}
		if _, err := svc.Start(ctx); !errors.Is(err, ErrNoTarget) {
			t.Errorf("Start err = %v, want ErrNoTarget", err)
		}
		// The passes are inert, not erroring: an instance without a target still
		// runs its workers' tickers harmlessly.
		if err := svc.SweepOnce(ctx); err != nil {
			t.Errorf("SweepOnce with no target = %v, want nil", err)
		}
		if n, err := svc.CopyOnce(ctx, 4); n != 0 || err != nil {
			t.Errorf("CopyOnce with no target = %d, %v", n, err)
		}
	})

	t.Run("target is the source", func(t *testing.T) {
		same := localAt(t, "one")
		svc := NewService(newFakeRepo(), same, same, Config{})
		if _, err := svc.Start(ctx); err == nil {
			t.Error("Start accepted a target that is the same store as the source")
		}
	})

	t.Run("already active", func(t *testing.T) {
		svc, _, _, _ := newTestService(t)
		if _, err := svc.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := svc.Start(ctx); !errors.Is(err, ErrAlreadyActive) {
			t.Errorf("second Start err = %v, want ErrAlreadyActive", err)
		}
	})

	t.Run("source cannot enumerate itself", func(t *testing.T) {
		svc := NewService(newFakeRepo(), unlistableBackend{}, localAt(t, "target"), Config{})
		if _, err := svc.Start(ctx); !errors.Is(err, ErrListingUnsupported) {
			t.Errorf("Start err = %v, want ErrListingUnsupported", err)
		}
	})
}

// TestCancelStopsClaimingAndLeavesCopiesInPlace. Cancelling is not an undo of
// the bytes: deleting the copies would be a destructive action taken on the way
// OUT of a destructive operation.
func TestCancelStopsClaimingAndLeavesCopiesInPlace(t *testing.T) {
	ctx := context.Background()
	svc, repo, src, dst := newTestService(t)
	put(t, src, "web-videos/a.mp4", "one")
	put(t, src, "web-videos/b.mp4", "two")
	camp, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.SweepOnce(ctx); err != nil { // enumerate -> copying
		t.Fatalf("SweepOnce: %v", err)
	}
	if _, err := svc.CopyOnce(ctx, 1); err != nil { // copy exactly one
		t.Fatalf("CopyOnce: %v", err)
	}

	if _, err := svc.Cancel(ctx, camp.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if n, _ := svc.CopyOnce(ctx, 4); n != 0 {
		t.Errorf("claimed %d objects after cancel; want 0", n)
	}
	if ok, _ := dst.Exists(ctx, "web-videos/a.mp4"); !ok {
		t.Error("the already-copied object was removed from the target by a cancel")
	}
	if got, _, _ := svc.Get(ctx, camp.ID); got.State != StateCancelled {
		t.Errorf("state = %q, want %q", got.State, StateCancelled)
	}
	if s := repo.state("web-videos/b.mp4"); s != ObjectPending {
		t.Errorf("uncopied object state = %q, want it left at %q", s, ObjectPending)
	}
	// A cancelled campaign never blocks the next one.
	if _, err := svc.Start(ctx); err != nil {
		t.Errorf("Start after cancel: %v", err)
	}
}

// mustActive returns the one live campaign's id, for assertions that do not
// carry it around.
func mustActive(t *testing.T, repo *fakeRepo) uuid.UUID {
	t.Helper()
	row, err := repo.GetActiveStorageMigration(context.Background())
	if err != nil {
		t.Fatalf("GetActiveStorageMigration: %v", err)
	}
	return row.ID
}

// corruptingBackend stores something OTHER than what it was given, which is what
// a silently lossy object store looks like from the outside.
type corruptingBackend struct{ storage.Backend }

// Describe forwards the wrapped store's identity: the wrapper is a fault
// injector, not a different store.
func (c *corruptingBackend) Describe() string { return storage.Describe(c.Backend) }

func (c *corruptingBackend) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	if len(data) > 0 {
		data[0] ^= 0xFF
	}
	return c.Backend.Put(ctx, key, strings.NewReader(string(data)))
}

// failingBackend refuses every write with a transient-looking error.
type failingBackend struct{}

var errFlaky = errors.New("storage: test backend is temporarily unavailable")

func (failingBackend) Put(context.Context, string, io.Reader) (int64, error) { return 0, errFlaky }
func (failingBackend) Open(context.Context, string) (io.ReadCloser, error)   { return nil, errFlaky }
func (failingBackend) Delete(context.Context, string) error                  { return errFlaky }
func (failingBackend) Exists(context.Context, string) (bool, error)          { return false, errFlaky }
func (failingBackend) Describe() string                                      { return "test:flaky" }

// unlistableBackend can store objects but cannot enumerate itself, so its whole
// contents can never be established and nothing may be migrated off it.
type unlistableBackend struct{}

func (unlistableBackend) Put(context.Context, string, io.Reader) (int64, error) { return 0, nil }
func (unlistableBackend) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}
func (unlistableBackend) Delete(context.Context, string) error         { return nil }
func (unlistableBackend) Exists(context.Context, string) (bool, error) { return false, nil }
func (unlistableBackend) Describe() string                             { return "test:unlistable" }
