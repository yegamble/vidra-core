//go:build integration

// The rollback floor (A38, 2026-09-07), proved against a REAL Postgres ledger:
// a CLEAN ledger ahead of this binary's newest embedded migration is the state
// `deploy/rollback.sh` puts the previous release's migrator in, and it must be a
// logged no-op with exit 0 — not the "no migration found for version N" failure
// that used to take every service depending on the one-shot down with it.
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/dbmigrate/ -run TestUpWhenTheLedgerIsAhead
package dbmigrate

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// aheadOfEmbedded stamps the ledger past the newest embedded migration, the way
// a newer release's migrator leaves it, and returns that version.
func aheadOfEmbedded(t *testing.T, dsn string) uint {
	t.Helper()
	max, err := EmbeddedMax()
	if err != nil {
		t.Fatalf("EmbeddedMax: %v", err)
	}
	ahead := max + 10
	if _, _, err := Force(dsn, int(ahead), nil); err != nil {
		t.Fatalf("force ledger to %d: %v", ahead, err)
	}
	return ahead
}

func TestUpWhenTheLedgerIsAheadAndCleanIsALoggedNoOp(t *testing.T) {
	dsn := throwawayDSN(t)
	if err := Up(dsn, nil); err != nil {
		t.Fatalf("initial migrate up: %v", err)
	}
	max, err := EmbeddedMax()
	if err != nil {
		t.Fatalf("EmbeddedMax: %v", err)
	}
	ahead := aheadOfEmbedded(t, dsn)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if err := Up(dsn, logger); err != nil {
		t.Fatalf("Up against a ledger at %d with embedded max %d: %v (want a no-op)", ahead, max, err)
	}

	// One line, and it says which two numbers disagree — an operator reading a
	// rollback log has to be able to tell this apart from a silent success.
	out := buf.String()
	if want := LedgerAheadMessage(ahead, max); !strings.Contains(out, want) {
		t.Errorf("log = %q, want it to contain %q", out, want)
	}
	fieldOf := func(k string, v uint) string { return k + "=" + strconv.FormatUint(uint64(v), 10) }
	for _, field := range []string{fieldOf("ledger_version", ahead), fieldOf("embedded_max", max), "dirty=false"} {
		if !strings.Contains(out, field) {
			t.Errorf("log = %q, want the structured field %q", out, field)
		}
	}

	// Nothing was applied and nothing was rewritten.
	after, err := Version(dsn)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if after.Version != ahead || after.Dirty || !after.Applied {
		t.Fatalf("ledger = %+v, want a clean version %d — the no-op must not touch it", after, ahead)
	}
}

// A ledger ahead but DIRTY still fails: dirty means the schema state is unknown,
// which no compatibility policy makes safe to serve.
func TestUpWhenTheLedgerIsAheadAndDirtyStillFails(t *testing.T) {
	dsn := throwawayDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := Up(dsn, nil); err != nil {
		t.Fatalf("initial migrate up: %v", err)
	}
	ahead := aheadOfEmbedded(t, dsn)

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect temp db: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(ctx, `UPDATE `+Table+` SET dirty = true`); err != nil {
		t.Fatalf("dirty the ledger: %v", err)
	}

	if err := Up(dsn, nil); err == nil {
		t.Fatalf("Up against a DIRTY ledger at %d returned nil, want a refusal", ahead)
	}
}

// A ledger BELOW the embedded max still migrates — the no-op branch must not
// swallow a real upgrade.
func TestUpBelowTheEmbeddedMaxStillApplies(t *testing.T) {
	dsn := throwawayDSN(t)
	if err := Up(dsn, nil); err != nil {
		t.Fatalf("initial migrate up: %v", err)
	}
	max, err := EmbeddedMax()
	if err != nil {
		t.Fatalf("EmbeddedMax: %v", err)
	}
	current, err := Version(dsn)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if current.Version != max {
		t.Fatalf("after up the ledger is %d, want the embedded max %d", current.Version, max)
	}
	// Re-running at exactly the max is ErrNoChange, not the ahead branch.
	if err := Up(dsn, nil); err != nil {
		t.Fatalf("Up at exactly the embedded max: %v", err)
	}
}
