//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The podcast feed's batched flag read returns exactly the videos whose
// download_enabled is on, and tolerates an unknown id.
func TestListDownloadEnabledVideoIDs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	_, chID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()
	var on, off uuid.UUID
	for _, c := range []struct {
		dst *uuid.UUID
		dl  bool
	}{{&on, true}, {&off, false}} {
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, download_enabled) VALUES ($1, 't', $2) RETURNING id`,
			chID, c.dl).Scan(c.dst); err != nil {
			t.Fatalf("seed video: %v", err)
		}
	}
	got, err := st.Queries().ListDownloadEnabledVideoIDs(ctx, []uuid.UUID{on, off, uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != on {
		t.Errorf("got %v, want only %v", got, on)
	}
}
