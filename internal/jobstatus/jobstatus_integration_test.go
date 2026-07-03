//go:build integration

// Integration smoke for the job-status aggregation: proves every per-queue
// stat/failure query is valid against the LIVE schema (column names, states,
// casts) and that Overview/Depths assemble without error. Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/jobstatus/
package jobstatus

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store"
)

func TestJobStatusOverviewIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	svc := NewService(st.Queries())
	ov, err := svc.Overview(ctx)
	if err != nil {
		t.Fatalf("Overview against live schema: %v", err)
	}
	if len(ov.Queues) != 6 {
		t.Fatalf("want 6 queues, got %d", len(ov.Queues))
	}
	// Every queue reports non-negative counts; on an empty DB oldest age is 0.
	for _, q := range ov.Queues {
		if q.Pending < 0 || q.Running < 0 || q.Done < 0 || q.Failed < 0 || q.OldestPendingAgeSeconds < 0 {
			t.Errorf("negative count in %+v", q)
		}
	}
	depths, err := svc.Depths(ctx)
	if err != nil {
		t.Fatalf("Depths: %v", err)
	}
	if len(depths) != 24 {
		t.Errorf("want 24 depth samples, got %d", len(depths))
	}
}
