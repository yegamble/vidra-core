//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestIPFSReturnedCopySurvivesLostClaimAndFencesAdmission(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reset := func() {
		_, e := st.Pool.Exec(ctx, "TRUNCATE ipfs_copy_cleanup, ipfs_capacity, ipfs_control_operations, ipfs_control_config")
		if e != nil {
			t.Fatal(e)
		}
	}
	reset()
	defer reset()
	q := st.Queries()
	cfg := ipfscontrol.Config{Provider: "internal", Enabled: true, AutoPinNew: true, BudgetBytes: 10 << 20, MinFreeBytes: 0, CopyBytesPerSecond: 1 << 20, Workers: 1}
	b, _ := json.Marshal(cfg)
	if _, err = q.EnsureIPFSControlConfig(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err = q.UpdateIPFSControlConfig(ctx, sqlcgen.UpdateIPFSControlConfigParams{ExpectedRevision: 1, Config: b, OperationID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.EnsureIPFSCapacity(ctx); err != nil {
		t.Fatal(err)
	}
	key := "cleanup-test/" + uuid.NewString()
	defer func() { _, _ = st.Pool.Exec(ctx, "DELETE FROM media_ipfs_pins WHERE object_key=$1", key) }()
	if _, err = st.Pool.Exec(ctx, "INSERT INTO media_ipfs_pins(object_key,media_class) VALUES($1,'thumbnail')", key); err != nil {
		t.Fatal(err)
	}
	returned := sqlcgen.RecordIPFSReturnedCopyParams{ClaimToken: uuid.New(), ObjectKey: key, Cid: "returned-root"}
	for range 2 {
		if err = q.RecordIPFSReturnedCopy(ctx, returned); err != nil {
			t.Fatal(err)
		}
	}
	c, err := q.GetIPFSCapacity(ctx)
	if err != nil || c.CleanupPending != 1 {
		t.Fatalf("duplicate record inflated queue: %+v %v", c, err)
	}
	admission := sqlcgen.AdmitIPFSPinParams{ObjectKey: key, ClaimToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}, ReservationBytes: 1 << 20, SourceGeneration: key, ConfigRevision: 2, RepoUsedBytes: 0, FilesystemFreeBytes: 20 << 20, ObservedAt: time.Now(), HostSequence: 1}
	if _, err = q.AdmitIPFSPin(ctx, admission); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cleanup did not fence admission: %v", err)
	}
	cfg.Provider = "external"
	external, _ := json.Marshal(cfg)
	if _, err = q.UpdateIPFSControlConfig(ctx, sqlcgen.UpdateIPFSControlConfigParams{ExpectedRevision: 2, Config: external, OperationID: uuid.New()}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("ownership changed with cleanup pending: %v", err)
	}
	token := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err = q.BeginIPFSCopyCleanup(ctx, sqlcgen.BeginIPFSCopyCleanupParams{MaintenanceToken: token, HostSequence: 1, ConfigRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.BeginIPFSCopyCleanup(ctx, sqlcgen.BeginIPFSCopyCleanupParams{MaintenanceToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}, HostSequence: 1, ConfigRevision: 2}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("concurrent maintenance: %v", err)
	}
	if n, e := q.FinishIPFSCopyCleanup(ctx, sqlcgen.FinishIPFSCopyCleanupParams{ClaimToken: returned.ClaimToken, Cid: returned.Cid, MaintenanceToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}}); e != nil || n != 0 {
		t.Fatalf("stale maintenance deleted work: %d %v", n, e)
	}
	if n, e := q.FinishIPFSCopyCleanup(ctx, sqlcgen.FinishIPFSCopyCleanupParams{ClaimToken: returned.ClaimToken, Cid: returned.Cid, MaintenanceToken: token}); e != nil || n != 1 {
		t.Fatalf("finish: %d %v", n, e)
	}
	c, err = q.GetIPFSCapacity(ctx)
	if err != nil || c.CleanupPending != 0 || c.MaintenanceToken.Valid {
		t.Fatalf("cleanup not released: %+v %v", c, err)
	}
	if _, err = q.AdmitIPFSPin(ctx, admission); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("old pre-GC measurement admitted: %v", err)
	}
	admission.ObservedAt = time.Now()
	if _, err = q.AdmitIPFSPin(ctx, admission); err != nil {
		t.Fatalf("fresh measurement blocked: %v", err)
	}
}
