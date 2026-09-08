package dbmigrate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/vidra/vidra-core/migrations"
)

// The embed directive is a glob, so a new migration is picked up automatically —
// but only if it is committed with a .sql extension in that exact directory. This
// guard fails the build for the one mistake that is otherwise invisible until a
// deploy: a file on disk that never made it into the binary.
func TestEmbeddedFilesMatchMigrationsDirectory(t *testing.T) {
	onDisk, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(onDisk) == 0 {
		t.Fatal("no migration files found on disk")
	}
	want := make([]string, 0, len(onDisk))
	for _, p := range onDisk {
		want = append(want, filepath.Base(p))
	}
	sort.Strings(want)

	var got []string
	err = fs.WalkDir(migrations.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got = append(got, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded FS: %v", err)
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("embedded %d files, migrations/ holds %d — run `go build` after adding migrations", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("embedded file %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Every embedded file must also be PARSEABLE by golang-migrate's source parser
// (<version>_<title>.<up|down>.sql) and form an unbroken version chain — a typo
// in a filename would otherwise surface as a silently skipped migration.
func TestEmbeddedSourceExposesEveryVersion(t *testing.T) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("open iofs source: %v", err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			t.Errorf("close source: %v", err)
		}
	}()

	var versions []uint
	v, err := src.First()
	if err != nil {
		t.Fatalf("first version: %v", err)
	}
	for {
		versions = append(versions, v)
		next, err := src.Next(v)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			t.Fatalf("next after %d: %v", v, err)
		}
		v = next
	}

	upFiles, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob up migrations: %v", err)
	}
	if len(versions) != len(upFiles) {
		t.Fatalf("source exposes %d versions, migrations/ holds %d *.up.sql files", len(versions), len(upFiles))
	}
	for i, got := range versions {
		if want := uint(i + 1); got != want {
			t.Fatalf("version %d in sequence is %d, want %d (gap or duplicate in migrations/)", i, got, want)
		}
		if _, _, err := src.ReadUp(got); err != nil {
			t.Fatalf("read up migration %d: %v", got, err)
		}
	}
	var _ source.Driver = src
}

// EmbeddedMax is what `migrate embedded-max` prints and what deploy/restore.sh
// compares a dump's ledger against, so it must be the real newest migration and
// not, say, the count of them.
func TestEmbeddedMaxIsTheNewestMigrationOnDisk(t *testing.T) {
	upFiles, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob up migrations: %v", err)
	}
	if len(upFiles) == 0 {
		t.Fatal("no up migrations found on disk")
	}
	var want uint
	for _, p := range upFiles {
		prefix, _, ok := strings.Cut(filepath.Base(p), "_")
		if !ok {
			t.Fatalf("migration %q has no version prefix", p)
		}
		n, err := strconv.ParseUint(prefix, 10, 64)
		if err != nil {
			t.Fatalf("migration %q has a non-numeric version prefix: %v", p, err)
		}
		if uint(n) > want {
			want = uint(n)
		}
	}

	got, err := EmbeddedMax()
	if err != nil {
		t.Fatalf("EmbeddedMax: %v", err)
	}
	if got != want {
		t.Fatalf("EmbeddedMax() = %d, want %d (the newest migrations/*.up.sql)", got, want)
	}
}

// The wording is a contract: deploy/rollback.sh's operator, the A38 rehearsal
// and vidra-search's TWIN all read this exact sentence to tell "the rollback
// target no-opped, as designed" apart from "the migrator failed". Changing it
// means changing the twin and the docs in the same commit.
func TestLedgerAheadMessageWording(t *testing.T) {
	const want = "schema version 135 is newer than this binary's newest migration 125; nothing to apply"
	if got := LedgerAheadMessage(135, 125); got != want {
		t.Fatalf("LedgerAheadMessage(135, 125) = %q, want %q", got, want)
	}
}
