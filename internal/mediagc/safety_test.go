package mediagc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// seedOrphans stores n objects under a swept prefix and m referenced ones,
// returning a repo whose reference set covers exactly the referenced keys. The
// sweep therefore sees n orphans out of n+m scanned, which is the only input the
// circuit breaker looks at.
func seedOrphans(t *testing.T, blobs storage.Backend, orphans, referenced int) *fakeRepo {
	t.Helper()
	repo := &fakeRepo{}
	for i := 0; i < orphans; i++ {
		put(t, blobs, fmt.Sprintf("thumbnails/orphan-%04d.jpg", i))
	}
	for i := 0; i < referenced; i++ {
		k := fmt.Sprintf("thumbnails/live-%04d.jpg", i)
		put(t, blobs, k)
		repo.fileKeys = append(repo.fileKeys, k)
	}
	return repo
}

// The breaker is the guard against a sweep whose reference set came back wrong.
// Every wrong reference set — a half-restored database, a bucket that is not
// ours, a migration in flight — produces the same shape: nearly everything looks
// like an orphan. The two things this table pins down are that the ratio is
// compared correctly and that the absolute floor keeps small legitimate sweeps
// out of it entirely.
func TestBreakerTripsOnlyOnAnImplausibleOrphanShare(t *testing.T) {
	tests := []struct {
		name       string
		orphans    int
		referenced int
		maxPercent int
		wantTrip   bool
		wantPct    int
	}{
		{
			name: "a small sweep is never stopped, however lopsided",
			// 100% orphans, and it still deletes: below the floor the whole sweep
			// is recoverable from a backup, which is not true of the case the
			// breaker exists for.
			orphans: 20, referenced: 0, maxPercent: 25, wantTrip: false, wantPct: 100,
		},
		{
			name:    "exactly at the floor is still under it",
			orphans: breakerFloor, referenced: 0, maxPercent: 25, wantTrip: false, wantPct: 100,
		},
		{
			name:    "one over the floor, and over the ratio, trips",
			orphans: breakerFloor + 1, referenced: 0, maxPercent: 25, wantTrip: true, wantPct: 100,
		},
		{
			name: "over the floor but under the ratio deletes",
			// 101 of 1101 objects = 9%, comfortably under 25.
			orphans: 101, referenced: 1000, maxPercent: 25, wantTrip: false, wantPct: 9,
		},
		{
			name: "exactly at the ratio deletes — the breaker is a strict >",
			// 200 of 800 = 25%, which is the configured limit, not over it.
			orphans: 200, referenced: 600, maxPercent: 25, wantTrip: false, wantPct: 25,
		},
		{
			name: "a hair over the ratio trips",
			// 201 of 801 = 25.09%.
			orphans: 201, referenced: 600, maxPercent: 25, wantTrip: true, wantPct: 25,
		},
		{
			name: "100 turns the breaker off",
			// Everything is an orphan and it deletes anyway: that is what an
			// operator asked for by setting the limit to 100.
			orphans: 200, referenced: 0, maxPercent: 100, wantTrip: false, wantPct: 100,
		},
		{
			name: "0 stops any sweep over the floor",
			// 101 of 1101 = 9%, and 9 > 0, so it trips: the "report, never
			// delete" posture.
			orphans: 101, referenced: 1000, maxPercent: 0, wantTrip: true, wantPct: 9,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			blobs, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			repo := seedOrphans(t, blobs, tc.orphans, tc.referenced)
			svc := NewService(repo, blobs, WithMaxOrphanPercent(tc.maxPercent))

			res, err := svc.Sweep(ctx, false)
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}
			if len(res.Orphans) != tc.orphans {
				t.Fatalf("orphans = %d, want %d", len(res.Orphans), tc.orphans)
			}
			if res.OrphanPercent != tc.wantPct {
				t.Errorf("OrphanPercent = %d, want %d", res.OrphanPercent, tc.wantPct)
			}
			if res.BreakerTripped != tc.wantTrip {
				t.Fatalf("BreakerTripped = %v, want %v (%d orphans of %d scanned, limit %d%%)",
					res.BreakerTripped, tc.wantTrip, tc.orphans, res.Scanned, tc.maxPercent)
			}
			// The whole point is what happened to the objects, so assert that
			// rather than only the flag.
			survived := 0
			for _, k := range res.Orphans {
				if exists(t, blobs, k) {
					survived++
				}
			}
			if tc.wantTrip {
				if res.Deleted != 0 || survived != tc.orphans {
					t.Errorf("a tripped breaker deleted %d objects (%d of %d orphans survived)", res.Deleted, survived, tc.orphans)
				}
				if !res.DryRun || res.Mode != ModeDryRun {
					t.Errorf("a tripped breaker reported mode %q / DryRun=%v, want a dry run", res.Mode, res.DryRun)
				}
			} else {
				if res.Deleted != tc.orphans || survived != 0 {
					t.Errorf("deleted %d of %d orphans (%d survived)", res.Deleted, tc.orphans, survived)
				}
				if res.Mode != ModeDelete {
					t.Errorf("Mode = %q, want %q", res.Mode, ModeDelete)
				}
			}
		})
	}
}

// A dry run is never stopped by the breaker: reporting what WOULD be deleted
// from a store where almost everything looks like garbage is precisely the
// answer an operator in that situation needs.
func TestBreakerNeverBlocksADryRun(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := seedOrphans(t, blobs, breakerFloor+50, 0)
	res, err := NewService(repo, blobs, WithMaxOrphanPercent(25)).Sweep(ctx, true)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.BreakerTripped {
		t.Error("the breaker tripped on a dry run, which deletes nothing to begin with")
	}
	if len(res.Orphans) != breakerFloor+50 {
		t.Errorf("orphans = %d, want the full list %d", len(res.Orphans), breakerFloor+50)
	}
}

// Ownership gates deletion, and the states that gate it must force a dry run
// rather than an error — an operator staring at an unowned bucket wants the
// orphan list, because that list is how they tell whose media it is.
func TestOwnershipGatesDestructiveSweeps(t *testing.T) {
	tests := []struct {
		name        string
		ownership   BucketOwnership
		wantDeletes bool
	}{
		{"an owned store deletes", OwnershipOwned, true},
		{"local disk is exempt by design", OwnershipNotApplicable, true},
		{"an unowned bucket is reported, not swept", OwnershipUnowned, false},
		{"another install's bucket is never swept", OwnershipConflict, false},
		{"unresolved ownership forbids deletion", OwnershipUnknown, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			blobs, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			orphan := "web-videos/orphan.mp4"
			put(t, blobs, orphan)
			svc := NewService(&fakeRepo{}, blobs, WithBucketOwnership(tc.ownership))

			res, err := svc.Sweep(ctx, false)
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}
			if res.BucketOwnership != string(tc.ownership) {
				t.Errorf("BucketOwnership = %q, want %q", res.BucketOwnership, tc.ownership)
			}
			// The orphan list is reported in every state.
			if len(res.Orphans) != 1 || res.Orphans[0] != orphan {
				t.Fatalf("orphans = %v, want [%s] in every ownership state", res.Orphans, orphan)
			}
			if tc.wantDeletes {
				if res.ForcedDryRun || res.Mode != ModeDelete || res.Deleted != 1 || exists(t, blobs, orphan) {
					t.Errorf("a permitted sweep did not delete: %+v", res)
				}
				return
			}
			if !res.ForcedDryRun || !res.DryRun || res.Mode != ModeDryRun {
				t.Errorf("a forbidden sweep did not report a forced dry run: %+v", res)
			}
			if res.Deleted != 0 || !exists(t, blobs, orphan) {
				t.Errorf("a forbidden sweep deleted %q", orphan)
			}
		})
	}
}

// A dry run is allowed whatever the ownership state, and must not be mislabelled
// as forced — the caller asked for it.
func TestDryRunIsAllowedOnAnUnownedBucket(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put(t, blobs, "web-videos/orphan.mp4")
	res, err := NewService(&fakeRepo{}, blobs, WithBucketOwnership(OwnershipConflict)).Sweep(ctx, true)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.ForcedDryRun {
		t.Error("a requested dry run was reported as forced")
	}
	if len(res.Orphans) != 1 {
		t.Errorf("orphans = %v, want the full report", res.Orphans)
	}
}

// The default for an object-store backend has to be "cannot delete". A caller
// that forgets to resolve ownership must lose the deletion, not the guard —
// this is the one default in the package whose failure mode is irreversible.
func TestObjectStoreBackendDefaultsToUnknownOwnership(t *testing.T) {
	s3, err := storage.NewS3(storage.S3Config{
		Endpoint:  "s3.example.invalid",
		Bucket:    "b",
		AccessKey: "k",
		SecretKey: "s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := NewService(nil, s3).Ownership(); got != OwnershipUnknown {
		t.Errorf("object-store default ownership = %q, want %q", got, OwnershipUnknown)
	}
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := NewService(nil, local).Ownership(); got != OwnershipNotApplicable {
		t.Errorf("local default ownership = %q, want %q", got, OwnershipNotApplicable)
	}
}

// Adoption writes the marker and re-enables deletion in the same process. The
// local backend refuses, because a storage root has no ownership question to
// answer and stamping a file into somebody's media directory would be a surprise.
func TestAdoptBucket(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	orphan := "web-videos/orphan.mp4"
	put(t, blobs, orphan)

	// Local: refused, and the state is untouched.
	local := NewService(&fakeRepo{}, blobs, WithInstanceIdentity("11111111-1111-4111-8111-111111111111"))
	if err := local.AdoptBucket(ctx); err != ErrAdoptNotApplicable {
		t.Fatalf("AdoptBucket on local = %v, want ErrAdoptNotApplicable", err)
	}

	// An unowned store: adopting flips it and stamps the identity.
	const identity = "22222222-2222-4222-8222-222222222222"
	svc := NewService(&fakeRepo{}, blobs,
		WithBucketOwnership(OwnershipUnowned), WithInstanceIdentity(identity))
	if err := svc.AdoptBucket(ctx); err != nil {
		t.Fatalf("AdoptBucket: %v", err)
	}
	if got := svc.Ownership(); got != OwnershipOwned {
		t.Fatalf("ownership after adoption = %q, want %q", got, OwnershipOwned)
	}
	marker, found, err := storage.ReadOwnerMarker(ctx, blobs)
	if err != nil || !found {
		t.Fatalf("ReadOwnerMarker after adoption: found=%v err=%v", found, err)
	}
	if marker != identity {
		t.Errorf("marker = %q, want the instance identity %q", marker, identity)
	}
	// And the next destructive sweep actually deletes.
	res, err := svc.Sweep(ctx, false)
	if err != nil {
		t.Fatalf("sweep after adoption: %v", err)
	}
	if res.Deleted != 1 || exists(t, blobs, orphan) {
		t.Errorf("the sweep after adoption deleted %d objects: %+v", res.Deleted, res)
	}
}

// Without an identity there is nothing to stamp, and a marker holding an empty
// string would be worse than none at all: the next boot would read it, fail to
// match, and call it a conflict.
func TestAdoptBucketNeedsAnIdentity(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(&fakeRepo{}, blobs, WithBucketOwnership(OwnershipUnowned))
	if err := svc.AdoptBucket(context.Background()); err != ErrNoInstanceIdentity {
		t.Fatalf("AdoptBucket without an identity = %v, want ErrNoInstanceIdentity", err)
	}
	if _, found, _ := storage.ReadOwnerMarker(context.Background(), blobs); found {
		t.Error("a marker was written despite there being no identity")
	}
}

// The audit line is the only record of a sweep an operator ever reads back, so
// it has to carry the reason nothing was deleted — and no object keys.
func TestSummaryCarriesTheReasonAndNoKeys(t *testing.T) {
	res := Result{
		Mode: ModeDryRun, Scanned: 900, Orphans: []string{"web-videos/secret-name.mp4"},
		OrphanPercent: 60, BreakerTripped: true, BucketOwnership: string(OwnershipUnowned), ForcedDryRun: true,
	}
	got := res.Summary()
	for _, want := range []string{"mode=dry-run", "scanned=900", "orphans=1", "orphan_pct=60", "deleted=0", "breaker=true", "ownership=unowned", "forced_dry_run=true"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary()=%q, want it to carry %q", got, want)
		}
	}
	if strings.Contains(got, "secret-name") {
		t.Errorf("Summary() leaked an object key: %q", got)
	}
}

// The marker must never be collectable by the sweep that depends on it.
func TestOwnerMarkerIsOutsideEverySweptPrefix(t *testing.T) {
	for _, prefix := range sweptPrefixes {
		if strings.HasPrefix(storage.OwnerMarkerKey, prefix) {
			t.Errorf("the ownership marker %q lives under swept prefix %q — the sweep could delete its own guard", storage.OwnerMarkerKey, prefix)
		}
	}
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteOwnerMarker(ctx, blobs, "33333333-3333-4333-8333-333333333333"); err != nil {
		t.Fatal(err)
	}
	res, err := NewService(&fakeRepo{}, blobs, WithBucketOwnership(OwnershipOwned)).Sweep(ctx, false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(res.Orphans) != 0 {
		t.Errorf("the sweep saw %v; the marker is not media and must not be enumerated", res.Orphans)
	}
	if _, found, _ := storage.ReadOwnerMarker(ctx, blobs); !found {
		t.Error("the sweep deleted the ownership marker")
	}
}

// TestActiveMigrationForcesADryRun is the storage-migration interlock. During a
// move the two stores are deliberately out of step — objects are being written
// into a store this instance is not serving from, and after cutover the OLD
// store is full of objects no database row will ever reference again by design —
// so a sweep in that window would look at a healthy store and see garbage.
func TestActiveMigrationForcesADryRun(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		active     bool
		checkErr   error
		wantForced bool
	}{
		{name: "no migration running deletes as asked", active: false, wantForced: false},
		{name: "a live campaign forces a dry run", active: true, wantForced: true},
		{
			// Fail safe: the rail exists for the case where the answer matters
			// most, and that is exactly the case where a database it cannot reach
			// is least reassuring.
			name:   "an unanswerable check is treated as a live campaign",
			active: false, checkErr: errors.New("database unreachable"), wantForced: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blobs, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			repo := seedOrphans(t, blobs, 3, 1)
			svc := NewService(repo, blobs,
				WithBucketOwnership(OwnershipOwned),
				WithActiveMigrationCheck(func(context.Context) (bool, error) {
					return tc.active, tc.checkErr
				}))

			res, err := svc.Sweep(ctx, false)
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}
			if res.ForcedDryRun != tc.wantForced {
				t.Fatalf("ForcedDryRun = %v, want %v", res.ForcedDryRun, tc.wantForced)
			}
			if tc.wantForced {
				if res.ForcedDryRunReason != ReasonMigrationActive {
					t.Errorf("ForcedDryRunReason = %q, want %q", res.ForcedDryRunReason, ReasonMigrationActive)
				}
				if res.Deleted != 0 || res.Mode != ModeDryRun {
					t.Errorf("deleted %d objects in mode %q during a migration", res.Deleted, res.Mode)
				}
				// The orphan list is still the full report: "what would it have
				// deleted?" is the operator's next question either way.
				if len(res.Orphans) != 3 {
					t.Errorf("orphans = %d, want 3", len(res.Orphans))
				}
				for _, k := range res.Orphans {
					if !exists(t, blobs, k) {
						t.Errorf("%q was deleted while a migration was in flight", k)
					}
				}
			} else {
				if res.ForcedDryRunReason != "" {
					t.Errorf("ForcedDryRunReason = %q, want empty", res.ForcedDryRunReason)
				}
				if res.Deleted != 3 || res.Mode != ModeDelete {
					t.Errorf("deleted %d objects in mode %q, want 3/delete", res.Deleted, res.Mode)
				}
			}
		})
	}
}

// TestOwnershipReasonWinsOverTheMigrationCheck: ownership is the cheaper and more
// alarming answer, and it is reported rather than being masked by whichever rail
// happens to run second.
func TestOwnershipReasonWinsOverTheMigrationCheck(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := seedOrphans(t, blobs, 2, 0)
	migrationChecked := false
	svc := NewService(repo, blobs,
		WithBucketOwnership(OwnershipConflict),
		WithActiveMigrationCheck(func(context.Context) (bool, error) {
			migrationChecked = true
			return true, nil
		}))
	res, err := svc.Sweep(ctx, false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.ForcedDryRunReason != ReasonBucketOwnership {
		t.Errorf("ForcedDryRunReason = %q, want %q", res.ForcedDryRunReason, ReasonBucketOwnership)
	}
	if migrationChecked {
		t.Error("the migration check ran even though ownership had already forced a dry run; it costs a database round trip")
	}
	if !strings.Contains(res.Summary(), "forced_reason="+ReasonBucketOwnership) {
		t.Errorf("Summary() = %q, want it to carry the forced reason", res.Summary())
	}
}
