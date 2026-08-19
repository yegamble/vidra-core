package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
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
//	docker compose run --rm migrate migrate version  # ledger check, changes nothing
//
// (`compose run` REPLACES the service command, hence the repeated word in the
// second form.)
const migrateUsage = "migrate <up|version>"

// runMigrate executes the subcommand and returns an error on any failure; main
// turns that into a non-zero exit, which is what gates deploy/deploy.sh.
func runMigrate(args []string) error {
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
		if !st.Applied {
			fmt.Println("version=none dirty=false")
			return nil
		}
		fmt.Printf("version=%d dirty=%t\n", st.Version, st.Dirty)
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
