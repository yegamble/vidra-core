package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/dbmigrate"
)

// The `migrate` subcommand lives on the api binary — and therefore inside the
// PUBLISHED api image — so applying the schema needs neither a second image nor a
// git checkout of migrations/ (they are embedded; see migrations/embed.go). Both
// forms below read DATABASE_URL, so the migrator and the server it ships with
// can never be pointed at different databases:
//
//	docker compose run --rm migrate                  # compose supplies: api migrate up
//	docker compose run --rm migrate migrate version  # ledger check, applies no migration
//
// (`compose run` REPLACES the service command, hence the repeated word in the
// second form.)
const migrateUsage = "migrate <up|version|force <version> " + forceConfirmFlag + ">"

// forceConfirmFlag guards `migrate force`. Forcing rewrites the ledger without
// running any SQL, so an operator who reaches for it after a half-applied
// migration is ASSERTING what the schema actually looks like; a bare
// `migrate force 42` typed in a hurry would silently strand the database. The
// flag exists to make that assertion explicit and unpasteable-by-accident.
const forceConfirmFlag = "--yes-i-know"

// runMigrate executes the subcommand and returns an error on any failure; main
// turns that into a non-zero exit, which is what gates deploy/deploy.sh.
func runMigrate(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: api " + migrateUsage)
	}
	if args[0] == "force" {
		return runMigrateForce(args[1:])
	}
	if len(args) != 1 {
		return errors.New("usage: api " + migrateUsage)
	}
	dsn := migrateDatabaseURL()

	switch args[0] {
	case "up":
		return dbmigrate.Up(dsn, slog.Default())
	case "version":
		st, err := dbmigrate.Version(dsn)
		if err != nil {
			return err
		}
		fmt.Println(formatStatus(st))
		if st.Dirty {
			// Reported on stdout above for the operator AND as a failure, so a
			// script gating on the exit code cannot miss a dirty ledger.
			return fmt.Errorf("schema_migrations is dirty at version %d: a migration failed halfway and the schema state is unknown", st.Version)
		}
		return nil
	default:
		return fmt.Errorf("unknown migrate subcommand %q (usage: api %s)", args[0], migrateUsage)
	}
}

// runMigrateForce is the dirty-ledger recovery path from the deploy runbook: it
// stamps schema_migrations at the version the operator says the schema is really
// at and clears the dirty flag, running NO migration SQL. The confirmation flag
// is checked before the database is touched, so a refusal is exactly that —
// nothing happened.
func runMigrateForce(args []string) error {
	versionArg := ""
	confirmed := false
	for _, arg := range args {
		switch {
		case arg == forceConfirmFlag:
			confirmed = true
		case strings.HasPrefix(arg, "--"):
			return fmt.Errorf("unknown flag %q (usage: api %s)", arg, migrateUsage)
		case versionArg != "":
			return errors.New("usage: api " + migrateUsage)
		default:
			versionArg = arg
		}
	}
	if versionArg == "" {
		return errors.New("usage: api " + migrateUsage)
	}
	// -1 is golang-migrate's "no migration has ever applied", the right target
	// when it was the FIRST migration that died.
	version, err := strconv.Atoi(versionArg)
	if err != nil || version < -1 {
		return fmt.Errorf("migrate force: %q is not a migration version (a number, or -1 for an empty ledger)", versionArg)
	}
	if !confirmed {
		return fmt.Errorf("refusing to force schema_migrations to version %d: this rewrites the ledger WITHOUT running any migration SQL, so the schema must already match. Re-run with %s if that is true", version, forceConfirmFlag)
	}

	before, after, err := dbmigrate.Force(migrateDatabaseURL(), version, slog.Default())
	if err != nil {
		// before is still worth printing: it is the state the operator is
		// recovering from, and a failed force leaves it untouched.
		fmt.Printf("before: %s\n", formatStatus(before))
		return err
	}
	fmt.Printf("before: %s\nafter:  %s\n", formatStatus(before), formatStatus(after))
	return nil
}

// formatStatus renders a ledger state in the one shape the runbook greps for.
func formatStatus(st dbmigrate.Status) string {
	if !st.Applied {
		return "version=none dirty=false"
	}
	return fmt.Sprintf("version=%d dirty=%t", st.Version, st.Dirty)
}

// migrateDatabaseURL resolves the destination the SAME way the server does — the
// DATABASE_URL variable, falling back to the same development default
// (config.Load). The server's full config validation is deliberately NOT run
// here: a schema-only one-shot must not fail because an unrelated runtime
// variable (JWT_SECRET, storage credentials) is absent from the migrator's
// environment.
func migrateDatabaseURL() string {
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_URL")); dsn != "" {
		return dsn
	}
	return config.DefaultDatabaseURL
}
