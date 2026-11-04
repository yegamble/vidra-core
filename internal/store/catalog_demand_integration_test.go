//go:build integration

package store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/catalog"
)

func TestCatalogCaptureDemandDistinguishesStaticHintsAndOutstandingLegacy(t *testing.T) {
	ctx, s := legacyCatalogFixture(t)
	check := func(want catalog.CaptureDemand) {
		t.Helper()
		got, err := s.CatalogCaptureDemand(ctx)
		if err != nil || got != want {
			t.Fatalf("capture demand %+v want%+v: %v", got, want, err)
		}
		var released bool
		if err := s.Pool.QueryRow(ctx, `SELECT true`).Scan(&released); err != nil || !released {
			t.Fatalf("poll retained resources: %v/%v", released, err)
		}
	}
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	check(catalog.CaptureDemand{}) // No bootstrap is needed to retain a hint.
	hint := uuid.New()
	exec(`INSERT INTO search_catalog_dirty(id,scope) VALUES ($1,'local')`, hint)
	check(catalog.CaptureDemand{Dirty: true})
	exec(`DELETE FROM search_catalog_dirty WHERE id=$1`, hint)
	for _, kind := range []string{"video.stats", "search.config_updated", "user.history_deleted", "future.unknown"} {
		exec(`INSERT INTO search_outbox(event_type,payload,state) VALUES ($1,'{}','dead')`, kind)
	}
	check(catalog.CaptureDemand{})
	for _, kind := range []string{"video.upsert", "video.suppress", "channel.upsert", "channel.delete", "user.suppress", "reconcile.begin", "reconcile.page", "reconcile.end"} {
		for _, state := range []string{"pending", "dead"} {
			var id int64
			if err := s.Pool.QueryRow(ctx, `INSERT INTO search_outbox(event_type,payload,state,next_attempt_at)
				VALUES ($1,'{}',$2,now()+interval '1 day') RETURNING id`, kind, state).Scan(&id); err != nil {
				t.Fatal(err)
			}
			check(catalog.CaptureDemand{LegacyOutstanding: true})
			exec(`UPDATE search_outbox SET state='delivered' WHERE id=$1`, id)
			check(catalog.CaptureDemand{})
		}
	}
}
