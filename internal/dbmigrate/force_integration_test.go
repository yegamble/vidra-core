//go:build integration

// Proof that Force is the recovery tool the deploy runbook says it is: against a
// REAL Postgres ledger left dirty by a half-applied migration, it clears the
// dirty flag and stamps the version the operator names — and `Up` goes from
// refusing to succeeding as a result. Everything runs in a throwaway database so
// the shared one is never touched. Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/dbmigrate/ -run TestForce
package dbmigrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// throwawayDSN creates a fresh database on the DATABASE_URL server and returns a
// DSN for it, dropping it when the test ends.
func throwawayDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	rnd := make([]byte, 6)
	if _, err := rand.Read(rnd); err != nil {
		t.Fatalf("rand: %v", err)
	}
	name := "vidra_forcetest_" + hex.EncodeToString(rnd)

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect maintenance db: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		// FORCE terminates any lingering connections (Postgres 13+).
		if _, err := admin.Exec(dropCtx, `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Logf("cleanup: drop temp db %s: %v", name, err)
		}
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func TestForceClearsADirtyLedger(t *testing.T) {
	dsn := throwawayDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := Up(dsn, nil); err != nil {
		t.Fatalf("initial migrate up: %v", err)
	}
	current, err := Version(dsn)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if !current.Applied || current.Dirty {
		t.Fatalf("after up, ledger = %+v, want an applied clean version", current)
	}

	// Simulate the half-applied migration the runbook is written for: the ledger
	// says "version N, dirty", exactly as golang-migrate leaves it when a step
	// dies mid-flight.
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect temp db: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(ctx, `UPDATE schema_migrations SET dirty = true`); err != nil {
		t.Fatalf("dirty the ledger: %v", err)
	}

	// A dirty ledger must block a normal deploy...
	if err := Up(dsn, nil); err == nil {
		t.Fatal("Up against a dirty ledger returned nil, want a refusal")
	}
	dirty, err := Version(dsn)
	if err != nil {
		t.Fatalf("read dirty version: %v", err)
	}
	if !dirty.Dirty {
		t.Fatalf("ledger = %+v, want dirty", dirty)
	}

	// ...and Force must be what unblocks it.
	before, after, err := Force(dsn, int(current.Version), nil)
	if err != nil {
		t.Fatalf("force to %d: %v", current.Version, err)
	}
	if !before.Dirty || before.Version != current.Version {
		t.Errorf("before = %+v, want the dirty ledger at version %d", before, current.Version)
	}
	if after.Dirty || after.Version != current.Version || !after.Applied {
		t.Errorf("after = %+v, want a clean version %d", after, current.Version)
	}
	if err := Up(dsn, nil); err != nil {
		t.Fatalf("migrate up after force: %v", err)
	}
}

// Forcing to -1 is the "the FIRST migration died" case: the ledger goes back to
// never-migrated, which Version reports as Applied=false.
func TestForceToEmptyLedger(t *testing.T) {
	dsn := throwawayDSN(t)

	if err := Up(dsn, nil); err != nil {
		t.Fatalf("initial migrate up: %v", err)
	}
	before, after, err := Force(dsn, -1, nil)
	if err != nil {
		t.Fatalf("force to -1: %v", err)
	}
	if !before.Applied {
		t.Errorf("before = %+v, want an applied version", before)
	}
	if after.Applied {
		t.Errorf("after = %+v, want an empty ledger", after)
	}
}
