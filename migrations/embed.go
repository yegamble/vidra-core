// Package migrations embeds this directory's SQL files into the compiled binary
// so applying the schema needs no git checkout and no bind-mount: cmd/api's
// `migrate` subcommand hands this FS to golang-migrate's iofs source
// (internal/dbmigrate). Migrations therefore ship in lock-step with the binary
// that expects them — an image can no longer be paired with a stale (or newer)
// migrations/ tree from the host.
//
// The .sql files themselves do not move. sqlc.yaml still derives the schema from
// migrations/*.sql, and deploy/deploy.sh still computes the expected ledger
// version from the filenames; this file only ADDS an in-binary copy. A //go:embed
// directive can only reference files in its OWN directory, which is why the
// package lives here rather than beside the runner.
package migrations

import "embed"

// FS holds every migration file — both directions, so a future `down` path has
// what it needs — under the names golang-migrate's source parser requires:
// <version>_<title>.<up|down>.sql.
//
//go:embed *.sql
var FS embed.FS
