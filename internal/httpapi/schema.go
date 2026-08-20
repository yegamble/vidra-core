package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/dbmigrate"
	"github.com/vidra/vidra-core/internal/version"
)

// undefinedTableCode is postgres' 42P01. On this path it is not an error but an
// answer: the ledger table is absent because no migration has ever run.
const undefinedTableCode = "42P01"

// schemaLedgerReader reads the golang-migrate ledger. *pgxpool.Pool satisfies
// it, and the pool the server already holds is what cmd/api passes: the
// alternative, dbmigrate.Version, opens a fresh connection per call, takes no
// context, and CREATES the ledger table when it is missing — a write, from a
// read path, on every probe.
type schemaLedgerReader interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// schemaLedgerStatus is the migration ledger's position. Version is a NUMBER
// because the comparison is numeric: `vidra update` refuses an image whose
// embedded migrations are older than the database it is pointed at. Applied is
// false when no migration has ever run — a fresh install and a database at
// version 0 are the same number and must not be the same answer.
type schemaLedgerStatus struct {
	Version int64 `json:"version"`
	Dirty   bool  `json:"dirty"`
	Applied bool  `json:"applied"`
	// Error carries a ledger read that failed for any reason OTHER than "not
	// migrated yet". The response is still 200: a 5xx here would hide which of
	// the two surfaces — the process or its database — is the one that is broken.
	Error string `json:"error,omitempty"`
}

// schemaResponse is returned by GET /schemaz.
type schemaResponse struct {
	Software versionResponse    `json:"software"`
	Schema   schemaLedgerStatus `json:"schema"`
}

// handleSchema reports the running build and the position of the migration
// ledger, and nothing else: build information and one integer version, never
// secrets or configuration. It always answers 200 — a database it cannot read
// is reported inside the document, because the caller asking "what version is
// this instance" has to be able to tell "the api is not there" from "the api is
// there and its database is not".
//
// It is an OPERATIONS surface, root-mounted beside /healthz and /version and
// unthrottled like them, and the edge Caddyfile does not route it: its consumers
// are host-local tooling — `vidra doctor` and the future `vidra update` — dialing
// 127.0.0.1:HTTP_PORT. It is nonetheless declared in api/openapi.yaml, because
// TestOpenAPIContract enumerates every unconditionally registered route and
// /version sets the precedent.
func (s *Server) handleSchema(c echo.Context) error {
	return c.JSON(http.StatusOK, schemaResponse{
		Software: versionResponse{
			Name:      "vidra",
			Version:   version.Version,
			Commit:    version.Commit,
			BuildDate: version.Date,
			Go:        version.GoVersion(),
		},
		Schema: s.schemaLedgerPosition(c.Request().Context()),
	})
}

// schemaLedgerPosition reads the single ledger row. golang-migrate keeps at most
// one, rewriting it on every step, so there is nothing to order or limit.
func (s *Server) schemaLedgerPosition(ctx context.Context) schemaLedgerStatus {
	if s.schemaLedger == nil {
		return schemaLedgerStatus{Error: "no database handle is wired"}
	}
	var (
		ledgerVersion int64
		dirty         bool
	)
	err := s.schemaLedger.
		QueryRow(ctx, "SELECT version, dirty FROM "+dbmigrate.Table).
		Scan(&ledgerVersion, &dirty)
	switch {
	case err == nil:
		return schemaLedgerStatus{Version: ledgerVersion, Dirty: dirty, Applied: true}
	case errors.Is(err, pgx.ErrNoRows), isUndefinedTable(err):
		// The table is absent (nothing has ever migrated this database) or empty
		// (a force to -1 deletes the row). Both are "fresh", not "failed".
		return schemaLedgerStatus{}
	default:
		return schemaLedgerStatus{Error: err.Error()}
	}
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == undefinedTableCode
}
