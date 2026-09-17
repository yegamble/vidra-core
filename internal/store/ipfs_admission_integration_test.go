//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestIPFSAdmissionConcurrentBudgetAndFencedRelease(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.Pool.Exec(ctx, "TRUNCATE ipfs_control_operations, ipfs_control_config, ipfs_capacity")
	if err != nil {
		t.Fatal(err)
	}
	prefix := "admission-test/" + uuid.NewString() + "/"
	defer func() {
		_, _ = st.Pool.Exec(ctx, "DELETE FROM media_ipfs_pins WHERE object_key LIKE $1", prefix+"%")
		_, _ = st.Pool.Exec(ctx, "TRUNCATE ipfs_control_operations, ipfs_control_config, ipfs_capacity")
	}()
	q := st.Queries()
	c := ipfscontrol.Config{Provider: "internal", Enabled: true, AutoPinNew: true, BudgetBytes: 10 << 20, MinFreeBytes: 2 << 20, CopyBytesPerSecond: 1 << 20, Workers: 8}
	b, _ := json.Marshal(c)
	_, err = q.EnsureIPFSControlConfig(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.UpdateIPFSControlConfig(ctx, sqlcgen.UpdateIPFSControlConfigParams{ExpectedRevision: 1, Config: b, OperationID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.EnsureIPFSCapacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan sqlcgen.MediaIpfsPin, 8)
	for i := 0; i < 8; i++ {
		key := prefix + uuid.NewString()
		_, err = st.Pool.Exec(ctx, "INSERT INTO media_ipfs_pins(object_key,media_class) VALUES($1,'thumbnail')", key)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, e := q.AdmitIPFSPin(ctx, sqlcgen.AdmitIPFSPinParams{ObjectKey: key, ClaimToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}, ReservationBytes: 3 << 20, SourceGeneration: key, ConfigRevision: 2, RepoUsedBytes: 2 << 20, FilesystemFreeBytes: 20 << 20, ObservedAt: time.Now(), HostSequence: 1})
			if e == nil {
				results <- row
			} else if !errors.Is(e, pgx.ErrNoRows) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	close(results)
	var winners []sqlcgen.MediaIpfsPin
	for r := range results {
		winners = append(winners, r)
	}
	if len(winners) != 2 {
		t.Fatalf("budget admitted %d jobs; want 2", len(winners))
	}
	capacity, err := q.GetIPFSCapacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.ReservedBytes != 6<<20 || capacity.ActiveClaims != 2 {
		t.Fatalf("capacity %+v", capacity)
	}
	winner := winners[0]
	if n, e := q.ReleaseIPFSReservation(ctx, sqlcgen.ReleaseIPFSReservationParams{ObjectKey: winner.ObjectKey, ClaimToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}}); e != nil || n != 0 {
		t.Fatalf("stale token released capacity: %d %v", n, e)
	}
	if n, e := q.ReleaseIPFSReservation(ctx, sqlcgen.ReleaseIPFSReservationParams{ObjectKey: winner.ObjectKey, ClaimToken: winner.ClaimToken}); e != nil || n != 1 {
		t.Fatalf("owner release: %d %v", n, e)
	}
	capacity, err = q.GetIPFSCapacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.ReservedBytes != 3<<20 || capacity.ActiveClaims != 1 || capacity.MeasureAfter.IsZero() {
		t.Fatalf("released capacity %+v", capacity)
	}
	key := prefix + "policy-race"
	_, err = st.Pool.Exec(ctx, "INSERT INTO media_ipfs_pins(object_key,media_class) VALUES($1,'thumbnail')", key)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "UPDATE ipfs_control_config SET revision=3, config=jsonb_set(config,'{enabled}','false') WHERE singleton")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, e := q.AdmitIPFSPin(ctx, sqlcgen.AdmitIPFSPinParams{ObjectKey: key, ClaimToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}, ReservationBytes: 3 << 20, SourceGeneration: key, ConfigRevision: 2, RepoUsedBytes: 2 << 20, FilesystemFreeBytes: 20 << 20, ObservedAt: time.Now(), HostSequence: 1})
		finished <- e
	}()
	// Confirm the admission reached the held policy lock before committing the
	// newer disable revision. Without FOR SHARE it succeeds on its old snapshot.
	waited := false
	for i := 0; i < 100; i++ {
		var waiting bool
		if err = st.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE '-- name: AdmitIPFSPin%' AND wait_event_type='Lock')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			waited = true
			break
		}
		select {
		case e := <-finished:
			t.Fatalf("admission escaped locked policy: %v", e)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waited {
		t.Fatal("admission never reached policy lock")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale policy admitted: %v", err)
	}
	_, err = st.Pool.Exec(ctx, "UPDATE ipfs_control_config SET revision=4, config=jsonb_set(config,'{enabled}','true') WHERE singleton")
	if err != nil {
		t.Fatal(err)
	}
	base := sqlcgen.AdmitIPFSPinParams{ObjectKey: key, ClaimToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}, ReservationBytes: 3 << 20, SourceGeneration: key, ConfigRevision: 4, RepoUsedBytes: 2 << 20, FilesystemFreeBytes: 20 << 20, ObservedAt: time.Now(), HostSequence: 1}
	for _, name := range []string{"stale observation", "free floor", "budget"} {
		arg := base
		switch name {
		case "stale observation":
			arg.ObservedAt = time.Now().Add(-time.Minute)
		case "free floor":
			arg.FilesystemFreeBytes = 2 << 20
		case "budget":
			arg.RepoUsedBytes = 9 << 20
		}
		if _, err = q.AdmitIPFSPin(ctx, arg); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("%s admitted: %v", name, err)
		}
	}
}

func TestIPFSEvictionKeepsLegacySharedAndRecentPins(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q := st.Queries()
	prefix := "eviction-test/" + uuid.NewString() + "/"
	defer func() { _, _ = st.Pool.Exec(ctx, "DELETE FROM media_ipfs_pins WHERE object_key LIKE $1", prefix+"%") }()
	for _, name := range []string{"legacy", "shared1", "shared2", "recent", "cold"} {
		reason, cid := "new", prefix+name
		if name == "legacy" {
			reason = "legacy"
		}
		if name == "shared1" || name == "shared2" {
			cid = prefix + "shared"
		}
		_, err = st.Pool.Exec(ctx, "INSERT INTO media_ipfs_pins(object_key,media_class,cid,state,policy_reason,created_at,updated_at,demand_at) VALUES($1,'thumbnail',$2,'pinned',$3,now()-interval '2 hours',now()-interval '2 hours',CASE WHEN $4 THEN now() ELSE NULL END)", prefix+name, cid, reason, name == "recent")
		if err != nil {
			t.Fatal(err)
		}
	}
	key, err := q.EvictColdIPFSPin(ctx)
	if err != nil || key != prefix+"cold" {
		t.Fatalf("evicted %q %v", key, err)
	}
	if _, err = q.EvictColdIPFSPin(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("protected pin evicted: %v", err)
	}
	if err = q.MarkIPFSPinUnpinned(ctx, key); err != nil {
		t.Fatal(err)
	}
	row, err := q.RouteIPFSPinIntent(ctx, sqlcgen.RouteIPFSPinIntentParams{ObjectKey: key, MediaClass: "thumbnail", TargetNetwork: "public"})
	if err != nil || row.State != "unpinned" {
		t.Fatalf("ordinary reconciliation resurrected eviction %+v %v", row, err)
	}
	if err = q.TagIPFSPolicyIntent(ctx, sqlcgen.TagIPFSPolicyIntentParams{ObjectKey: key, Reason: "demand"}); err != nil {
		t.Fatal(err)
	}
	row, err = q.GetIPFSPinByObjectKey(ctx, key)
	if err != nil || row.State != "pending" || row.CapacityReason != "" {
		t.Fatalf("explicit demand did not rearm %+v %v", row, err)
	}
}
