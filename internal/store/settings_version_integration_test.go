//go:build integration

// Integration test: cross-REPLICA cache invalidation against a REAL PostgreSQL
// with migrations applied.
//
// The property under test is the one that makes running more than one api
// process CORRECT rather than merely safe: AN ADMIN CHANGE MADE ON ONE REPLICA
// MUST REACH THE OTHERS. Instance settings, instance documents and branding
// images are each loaded once at boot into an in-memory map and refreshed only
// by the process that served the write, so before migration 0121 a fleet of N
// replicas obeyed a settings change on exactly one of them — silently, with the
// admin's own read landing on the changed replica 1 time in N.
//
// A fake repository cannot prove this: the guarantee comes from two service
// instances sharing one row in one database, which is the thing being asserted.
//
// Run via `make test-integration`:
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -race ./internal/store/...
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/instancedocs"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/settingsversion"
)

// TestSettingsVersionPropagatesAcrossInstances stands up TWO independent
// service sets over ONE database — the two-api-replica topology — writes
// through A, ticks B's poller, and asserts B now serves the new value.
func TestSettingsVersionPropagatesAcrossInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	// This test writes real rows into the shared integration database; clean up
	// what it touched so the sibling suites see the schema they expect.
	defer func() {
		bg := context.Background()
		_, _ = st.Pool.Exec(bg, `DELETE FROM instance_settings WHERE key = $1`, instancesettings.KeyInstanceName)
		_, _ = st.Pool.Exec(bg, `DELETE FROM instance_documents WHERE name = $1`, instancedocs.NameCustomCSS)
	}()

	defaults := instancesettings.Defaults{InstanceName: "Boot Default", UploadsEnabled: true}
	bump := settingsversion.BumpFunc(q)

	// Replica A: the one that will serve the admin's PATCH.
	settingsA := instancesettings.NewService(q, defaults, instancesettings.WithVersionBump(bump))
	docsA := instancedocs.NewService(q, instancedocs.WithVersionBump(bump))
	if err := settingsA.Load(ctx); err != nil {
		t.Fatalf("A settings load: %v", err)
	}
	if err := docsA.Load(ctx); err != nil {
		t.Fatalf("A docs load: %v", err)
	}

	// Replica B: same database, its own caches, its own poller. It never sees
	// the write except through the counter.
	settingsB := instancesettings.NewService(q, defaults, instancesettings.WithVersionBump(bump))
	docsB := instancedocs.NewService(q, instancedocs.WithVersionBump(bump))
	imagesB := profileimage.NewService(nil, nil, profileimage.WithInstanceImages(q))
	if err := settingsB.Load(ctx); err != nil {
		t.Fatalf("B settings load: %v", err)
	}
	if err := docsB.Load(ctx); err != nil {
		t.Fatalf("B docs load: %v", err)
	}
	if err := imagesB.LoadInstanceImages(ctx); err != nil {
		t.Fatalf("B branding load: %v", err)
	}
	pollerB := settingsversion.New(q, settingsversion.DefaultInterval,
		settingsversion.Cache{Name: "instance settings", Reload: settingsB.Load},
		settingsversion.Cache{Name: "instance documents", Reload: docsB.Load},
		settingsversion.Cache{Name: "instance branding", Reload: imagesB.LoadInstanceImages},
	)
	if err := pollerB.Prime(ctx); err != nil {
		t.Fatalf("B prime: %v", err)
	}
	primed := pollerB.Known()

	// A poll before any write must be a no-op — the poller reloads on CHANGE,
	// not on every tick.
	if changed, err := pollerB.Tick(ctx); err != nil || changed {
		t.Fatalf("quiescent tick: changed=%v err=%v, want false/nil", changed, err)
	}

	// The admin edits the instance name on replica A.
	if err := settingsA.Apply(ctx, map[string]instancesettings.Update{
		instancesettings.KeyInstanceName: {Value: "Renamed On Replica A"},
	}, uuid.Nil); err != nil {
		t.Fatalf("A apply: %v", err)
	}
	if got := settingsA.String(instancesettings.KeyInstanceName); got != "Renamed On Replica A" {
		t.Fatalf("A instance_name = %q, want the new value", got)
	}

	// THE BUG, asserted: without a poll, B is still serving its boot value.
	if got := settingsB.String(instancesettings.KeyInstanceName); got != "Boot Default" {
		t.Fatalf("B instance_name before polling = %q, want the stale boot value "+
			"(the test can no longer distinguish propagation from a shared cache)", got)
	}

	// THE FIX, asserted: one tick and B agrees.
	changed, err := pollerB.Tick(ctx)
	if err != nil {
		t.Fatalf("B tick: %v", err)
	}
	if !changed {
		t.Fatal("B's poll saw no change after A's write")
	}
	if got := pollerB.Known(); got <= primed {
		t.Fatalf("B known version = %d, want > %d", got, primed)
	}
	if got := settingsB.String(instancesettings.KeyInstanceName); got != "Renamed On Replica A" {
		t.Fatalf("B instance_name after polling = %q, want Renamed On Replica A", got)
	}

	// The counter guards all three stores with one number: a DOCUMENT write on
	// A also has to reach B.
	if _, err := docsA.Set(ctx, instancedocs.NameCustomCSS, "body{--replica:a}", uuid.Nil); err != nil {
		t.Fatalf("A docs set: %v", err)
	}
	if _, ok := docsB.Get(instancedocs.NameCustomCSS); ok {
		t.Fatal("B has the custom CSS before polling (test cannot distinguish propagation)")
	}
	if changed, err := pollerB.Tick(ctx); err != nil || !changed {
		t.Fatalf("B tick after a document write: changed=%v err=%v, want true/nil", changed, err)
	}
	doc, ok := docsB.Get(instancedocs.NameCustomCSS)
	if !ok || doc.Body != "body{--replica:a}" {
		t.Fatalf("B custom CSS after polling = %+v ok=%v, want A's body", doc, ok)
	}

	// And it settles: a further tick with nothing written reloads nothing.
	if changed, err := pollerB.Tick(ctx); err != nil || changed {
		t.Fatalf("settled tick: changed=%v err=%v, want false/nil", changed, err)
	}
}

// TestBumpSettingsVersionIsMonotonicAndConcurrencySafe: the counter is only
// ever compared for inequality, so what it must guarantee is that N concurrent
// admin writes leave it strictly ahead of where it started, never lost to a
// read-modify-write race (the statement is a single atomic UPDATE for exactly
// this reason).
func TestBumpSettingsVersionIsMonotonicAndConcurrencySafe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	before, err := q.GetSettingsVersion(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	const writers = 8
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func() {
			_, err := q.BumpSettingsVersion(ctx)
			errs <- err
		}()
	}
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent bump: %v", err)
		}
	}

	after, err := q.GetSettingsVersion(ctx)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if after != before+writers {
		t.Fatalf("version = %d after %d concurrent bumps from %d, want %d "+
			"(a lost update means a replica can miss an admin change)", after, writers, before, before+writers)
	}
}
