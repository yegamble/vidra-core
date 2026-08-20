// Package dbmigrate applies vidra-core's embedded schema migrations with the
// golang-migrate LIBRARY, replacing the migrate/migrate CLI container that used
// to bind-mount migrations/ from a git checkout.
//
// Ledger compatibility is the whole point: the postgres database driver below is
// the same one the CLI used, addressed by the same postgres:// DSN, writing the
// same DEFAULT ledger table — `schema_migrations`. An instance whose schema was
// last advanced by the CLI container is therefore picked up mid-chain by this
// code with no conversion step, and deploy/deploy.sh's post-migration ledger
// assertion keeps reading the table it always did.
package dbmigrate

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	// Registers the postgres:// (and postgresql://) database driver, so an
	// operator's DSN — sslmode, credentials, extra params and all — is honoured
	// verbatim, exactly as the CLI honoured it.
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/vidra/vidra-core/migrations"
)

// Status is the state of the schema_migrations ledger.
type Status struct {
	// Version is the highest applied migration, 0 when Applied is false.
	Version uint
	// Dirty reports a migration that failed halfway: golang-migrate marks the
	// ledger dirty before running each step and clears it on success, so a dirty
	// row means the schema is in an unknown state and needs a human.
	Dirty bool
	// Applied is false when no migration has ever run against this database (the
	// ledger table is absent or empty).
	Applied bool
}

// Up applies every pending migration and returns nil once the database is at the
// newest embedded version — including when there was nothing to do. Any failure
// (a broken statement, a dirty ledger from an earlier half-applied run, an
// unreachable database) is returned so the caller can exit non-zero without
// having touched anything else.
//
// logger may be nil; when set it receives golang-migrate's own per-step output,
// so a deploy log reads like the CLI's did.
func Up(dsn string, logger *slog.Logger) error {
	m, err := open(dsn, logger)
	if err != nil {
		return err
	}
	defer closeMigrate(m, logger)

	before, err := status(m)
	if err != nil {
		return err
	}

	switch err := m.Up(); {
	case err == nil:
	case errors.Is(err, migrate.ErrNoChange):
		logStatus(logger, "schema already up to date", before)
		return nil
	default:
		return fmt.Errorf("dbmigrate: apply migrations: %w", err)
	}

	after, err := status(m)
	if err != nil {
		return err
	}
	if after.Dirty {
		return fmt.Errorf("dbmigrate: schema_migrations is dirty at version %d after apply", after.Version)
	}
	if logger != nil {
		logger.Info("migrations applied", "from_version", before.Version, "to_version", after.Version)
	}
	return nil
}

// Force overwrites the schema_migrations ledger with version and clears the
// dirty flag, WITHOUT running any SQL — the golang-migrate CLI's `force`, which
// the recovery runbook needs after a migration dies halfway. It touches nothing
// but the ledger, so the caller is asserting that the schema really is at
// version; getting that wrong leaves the database permanently out of step with
// the code. The before/after states are returned so the caller can show the
// operator exactly what changed.
//
// version is an int (not uint) because golang-migrate spells "no migration has
// ever applied" as -1, which is the correct target when the very first migration
// failed.
func Force(dsn string, version int, logger *slog.Logger) (before, after Status, err error) {
	m, err := open(dsn, logger)
	if err != nil {
		return Status{}, Status{}, err
	}
	defer closeMigrate(m, logger)

	before, err = status(m)
	if err != nil {
		return Status{}, Status{}, err
	}
	if err := m.Force(version); err != nil {
		return before, Status{}, fmt.Errorf("dbmigrate: force version %d: %w", version, err)
	}
	after, err = status(m)
	if err != nil {
		return before, Status{}, err
	}
	if logger != nil {
		logger.Warn("schema_migrations forced", "from_version", before.Version, "from_dirty", before.Dirty, "to_version", after.Version)
	}
	return before, after, nil
}

// Version reports the ledger state without changing the schema. (The ledger
// table itself is created if missing — golang-migrate does that when it opens
// the database, whichever direction it is asked about — so on a never-migrated
// database this is a write, just not one that touches a single application
// table.)
func Version(dsn string) (Status, error) {
	m, err := open(dsn, nil)
	if err != nil {
		return Status{}, err
	}
	defer closeMigrate(m, nil)
	return status(m)
}

// open builds a migrator over the embedded files. The source name ("iofs") is a
// label for error messages only — the instance below is what is read.
func open(dsn string, logger *slog.Logger) (*migrate.Migrate, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("dbmigrate: empty database DSN")
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("dbmigrate: read embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return nil, fmt.Errorf("dbmigrate: connect to database: %w", err)
	}
	if logger != nil {
		m.Log = migrateLogger{logger: logger}
	}
	return m, nil
}

// status normalizes golang-migrate's "no version yet" sentinel into Status.
func status(m *migrate.Migrate) (Status, error) {
	version, dirty, err := m.Version()
	switch {
	case errors.Is(err, migrate.ErrNilVersion):
		return Status{}, nil
	case err != nil:
		return Status{}, fmt.Errorf("dbmigrate: read schema_migrations: %w", err)
	}
	return Status{Version: version, Dirty: dirty, Applied: true}, nil
}

// closeMigrate releases the source and the database connection. Close errors are
// reported but never fatal: by the time it runs the migration outcome is already
// decided, and masking a real apply error with a teardown error would be worse.
func closeMigrate(m *migrate.Migrate, logger *slog.Logger) {
	srcErr, dbErr := m.Close()
	if logger == nil {
		return
	}
	if srcErr != nil {
		logger.Warn("closing migration source failed", "error", srcErr)
	}
	if dbErr != nil {
		logger.Warn("closing migration database connection failed", "error", dbErr)
	}
}

func logStatus(logger *slog.Logger, msg string, s Status) {
	if logger == nil {
		return
	}
	if !s.Applied {
		logger.Info(msg, "version", "none")
		return
	}
	logger.Info(msg, "version", s.Version, "dirty", s.Dirty)
}

// migrateLogger adapts slog to golang-migrate's Logger interface. Verbose is
// false so the output stays at the CLI's default volume (one line per applied
// version) instead of the -verbose firehose.
type migrateLogger struct{ logger *slog.Logger }

func (l migrateLogger) Printf(format string, v ...any) {
	l.logger.Info(strings.TrimRight(fmt.Sprintf(format, v...), "\n"))
}

func (l migrateLogger) Verbose() bool { return false }
