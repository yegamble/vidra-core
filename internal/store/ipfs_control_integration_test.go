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
	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestIPFSControlConfigAtomicCASAndOperations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.Pool.Exec(ctx, "TRUNCATE ipfs_copy_cleanup, ipfs_control_operations, ipfs_control_config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), "TRUNCATE ipfs_copy_cleanup, ipfs_control_operations, ipfs_control_config")
	}()
	q := st.Queries()
	c := ipfscontrol.Config{Provider: "internal", BudgetBytes: 20 << 30, MinFreeBytes: 20 << 30, CopyBytesPerSecond: 2 << 20, Workers: 1}
	b, _ := json.Marshal(c)
	first, err := q.EnsureIPFSControlConfig(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.PolicyActive {
		t.Fatalf("read activated policy: %+v", first)
	}
	c.Enabled = true
	b, _ = json.Marshal(c)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := q.UpdateIPFSControlConfig(ctx, sqlcgen.UpdateIPFSControlConfigParams{ExpectedRevision: 1, Config: b, OperationID: uuid.New()})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("CAS winners=%d", wins)
	}
	current, err := q.GetIPFSControlConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != 2 || !current.PolicyActive {
		t.Fatalf("partial CAS: %+v", current)
	}
	unchanged, err := q.UpdateIPFSControlConfig(ctx, sqlcgen.UpdateIPFSControlConfigParams{ExpectedRevision: 2, Config: b, OperationID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != 2 || unchanged.OperationID != uuid.Nil {
		t.Fatalf("unchanged config enqueued: %+v", unchanged)
	}
	id := uuid.New()
	arg := sqlcgen.RequestIPFSControlOperationParams{ID: id, ExpectedRevision: 2, Action: "restart"}
	op, err := q.RequestIPFSControlOperation(ctx, arg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := q.RequestIPFSControlOperation(ctx, arg)
	if err != nil || again.Sequence != op.Sequence {
		t.Fatalf("nonidempotent operation: %+v %v", again, err)
	}
	arg.Action = "apply"
	if _, err := q.RequestIPFSControlOperation(ctx, arg); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("reused ID accepted: %v", err)
	}
	c.Provider = "external"
	b, _ = json.Marshal(c)
	if _, err := q.UpdateIPFSControlConfig(ctx, sqlcgen.UpdateIPFSControlConfigParams{ExpectedRevision: 2, Config: b, OperationID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	arg.ID = uuid.New()
	arg.ExpectedRevision = 3
	if _, err := q.RequestIPFSControlOperation(ctx, arg); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("external lifecycle accepted: %v", err)
	}
}
