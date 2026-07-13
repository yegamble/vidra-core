//go:build integration

// This is the P19 "migrations apply cleanly to an EXISTING database fixture"
// gate — the complement to the empty-DB migration check. It proves the full
// migration chain is safe against a POPULATED database, not just a blank one: a
// later migration that adds a NOT NULL column without a default, drops a column
// rows depend on, or otherwise breaks existing data would fail here.
//
// Strategy: create a throwaway database, apply migrations up to an EARLY version
// (0010), insert a couple of fixture rows into that early schema, then apply ALL
// remaining migrations and assert (a) they succeed and (b) the fixture rows
// survive intact. Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/store/ -run TestMigrationsAgainstExistingFixture
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// snapshotVersion is the early schema the fixture is inserted against.
const snapshotVersion = 10

var upFilePattern = regexp.MustCompile(`^(\d{4})_.*\.up\.sql$`)

type upMigration struct {
	version int
	path    string
}

// loadUpMigrations returns every *.up.sql under migrations/, ordered by version.
func loadUpMigrations(t *testing.T) []upMigration {
	t.Helper()
	dir := filepath.Join("..", "..", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var ups []upMigration
	for _, e := range entries {
		m := upFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("bad migration version %q: %v", e.Name(), err)
		}
		ups = append(ups, upMigration{version: v, path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].version < ups[j].version })
	if len(ups) == 0 {
		t.Fatal("no migrations found")
	}
	return ups
}

// applyMigration executes one migration file. pgx uses the simple query protocol
// for an argument-less Exec, so multi-statement migration files run as a unit.
func applyMigration(ctx context.Context, t *testing.T, conn *pgx.Conn, m upMigration) {
	t.Helper()
	sqlBytes, err := os.ReadFile(m.path)
	if err != nil {
		t.Fatalf("read %s: %v", m.path, err)
	}
	if _, err := conn.Exec(ctx, string(sqlBytes)); err != nil {
		t.Fatalf("apply migration %04d (%s): %v", m.version, filepath.Base(m.path), err)
	}
}

func TestMigrationsAgainstExistingFixture(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// A throwaway database so we never touch the shared one.
	rnd := make([]byte, 6)
	if _, err := rand.Read(rnd); err != nil {
		t.Fatalf("rand: %v", err)
	}
	tempDB := "vidra_migtest_" + hex.EncodeToString(rnd)

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect maintenance db: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{tempDB}.Sanitize()); err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	defer func() {
		// FORCE terminates any lingering connections (Postgres 13+).
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		if _, err := admin.Exec(dropCtx, `DROP DATABASE IF EXISTS `+pgx.Identifier{tempDB}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Logf("cleanup: drop temp db %s: %v", tempDB, err)
		}
	}()

	// DSN pointing at the throwaway database.
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + tempDB
	tempConn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("connect temp db: %v", err)
	}
	// Ensure the temp connection is closed before the deferred DROP runs.
	closed := false
	closeTemp := func() {
		if !closed {
			_ = tempConn.Close(ctx)
			closed = true
		}
	}
	defer closeTemp()

	ups := loadUpMigrations(t)

	// 1) Apply the EARLY snapshot (through 0010).
	for _, m := range ups {
		if m.version > snapshotVersion {
			continue
		}
		applyMigration(ctx, t, tempConn, m)
	}

	// 2) Seed a couple of fixture rows into the early schema (users + a session
	//    FK'd to it — both are core tables present since 0002).
	var userID uuid.UUID
	if err := tempConn.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, $3) RETURNING id`,
		"migfixture", "migfixture@example.test", "not-a-real-hash",
	).Scan(&userID); err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	var sessionID uuid.UUID
	if err := tempConn.QueryRow(ctx,
		`INSERT INTO sessions (user_id, refresh_hash, expires_at) VALUES ($1, $2, now() + interval '1 day') RETURNING id`,
		userID, "fixture-refresh-hash",
	).Scan(&sessionID); err != nil {
		t.Fatalf("insert fixture session: %v", err)
	}

	// 3) Apply ALL remaining migrations against the populated database.
	for _, m := range ups {
		if m.version <= snapshotVersion {
			continue
		}
		// Migration 0095 deliberately adds a nullable preference override. Seed
		// the pre-migration player-settings row so the test proves existing users
		// inherit the admin default rather than being frozen to false.
		if m.version == 95 {
			if _, err := tempConn.Exec(ctx,
				`INSERT INTO user_player_settings (user_id) VALUES ($1)`, userID,
			); err != nil {
				t.Fatalf("seed pre-0095 player settings: %v", err)
			}
		}
		applyMigration(ctx, t, tempConn, m)
		if m.version == 95 {
			assertInherited := func(stage string) {
				t.Helper()
				var override *bool
				if err := tempConn.QueryRow(ctx,
					`SELECT video_card_previews_enabled FROM user_player_settings WHERE user_id = $1`, userID,
				).Scan(&override); err != nil {
					t.Fatalf("%s: read preview override: %v", stage, err)
				}
				if override != nil {
					t.Fatalf("%s: existing user's preview override = %v, want NULL/inherit", stage, *override)
				}
			}
			assertInherited("0095 up")

			// Exercise the matching down migration immediately, before any later
			// migration could depend on this column, then restore 0095 so the
			// remainder of the chain sees the expected latest schema.
			downPath := filepath.Join(filepath.Dir(m.path), "0095_video_card_previews.down.sql")
			downSQL, err := os.ReadFile(downPath)
			if err != nil {
				t.Fatalf("read %s: %v", downPath, err)
			}
			if _, err := tempConn.Exec(ctx, string(downSQL)); err != nil {
				t.Fatalf("apply migration 0095 down: %v", err)
			}
			var columnExists bool
			if err := tempConn.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.columns
					WHERE table_schema = 'public'
					  AND table_name = 'user_player_settings'
					  AND column_name = 'video_card_previews_enabled'
				)`).Scan(&columnExists); err != nil {
				t.Fatalf("inspect 0095 down schema: %v", err)
			}
			if columnExists {
				t.Fatal("0095 down left video_card_previews_enabled in place")
			}
			applyMigration(ctx, t, tempConn, m)
			assertInherited("0095 re-up")
		}
	}

	// 4) The fixture rows must have survived the full migration chain unchanged.
	var gotUsername, gotEmail string
	if err := tempConn.QueryRow(ctx,
		`SELECT username, email FROM users WHERE id = $1`, userID,
	).Scan(&gotUsername, &gotEmail); err != nil {
		t.Fatalf("fixture user did not survive migrations: %v", err)
	}
	if gotUsername != "migfixture" || gotEmail != "migfixture@example.test" {
		t.Errorf("fixture user changed: username=%q email=%q", gotUsername, gotEmail)
	}
	var gotUserID uuid.UUID
	if err := tempConn.QueryRow(ctx,
		`SELECT user_id FROM sessions WHERE id = $1`, sessionID,
	).Scan(&gotUserID); err != nil {
		t.Fatalf("fixture session did not survive migrations: %v", err)
	}
	if gotUserID != userID {
		t.Errorf("fixture session FK changed: got %s, want %s", gotUserID, userID)
	}

	// Sanity: the latest schema really did advance well beyond the snapshot.
	if latest := ups[len(ups)-1].version; latest <= snapshotVersion {
		t.Fatalf("expected migrations beyond %04d, latest is %04d", snapshotVersion, latest)
	}
}
