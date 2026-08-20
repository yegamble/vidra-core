package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/version"
)

// fakeLedger is a schemaLedgerReader that answers with one canned row or one
// canned error — the four shapes the ledger can be in from the handler's side.
type fakeLedger struct {
	version int64
	dirty   bool
	err     error
	queries []string
}

func (f *fakeLedger) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	f.queries = append(f.queries, sql)
	return fakeRow{ledger: f}
}

type fakeRow struct{ ledger *fakeLedger }

func (r fakeRow) Scan(dest ...any) error {
	if r.ledger.err != nil {
		return r.ledger.err
	}
	*(dest[0].(*int64)) = r.ledger.version
	*(dest[1].(*bool)) = r.ledger.dirty
	return nil
}

func getSchemaz(t *testing.T, opts ...Option) schemaResponse {
	t.Helper()
	srv := New(testConfig(), nil, nil, opts...)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/schemaz", nil))

	// Always 200: a ledger this endpoint cannot read is reported in the body, not
	// as a status code that hides which surface is broken.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body schemaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

func TestSchemazAppliedAndClean(t *testing.T) {
	ledger := &fakeLedger{version: 104}
	body := getSchemaz(t, WithSchemaLedger(ledger))

	if body.Software.Name != "vidra" || body.Software.Version != version.Version || body.Software.Go == "" {
		t.Errorf("software = %+v, want vidra with a version and a go toolchain", body.Software)
	}
	if body.Schema.Version != 104 || body.Schema.Dirty || !body.Schema.Applied {
		t.Errorf("schema = %+v, want {Version:104 Dirty:false Applied:true}", body.Schema)
	}
	if body.Schema.Error != "" {
		t.Errorf("error = %q, want empty on a clean read", body.Schema.Error)
	}
	// The ledger name comes from the package that owns it, never a local literal.
	if len(ledger.queries) != 1 || !strings.Contains(ledger.queries[0], "schema_migrations") {
		t.Errorf("queries = %q, want one SELECT against schema_migrations", ledger.queries)
	}
}

// The version number is what `vidra update` compares, so it has to arrive as a
// JSON number rather than a string.
func TestSchemazVersionIsANumber(t *testing.T) {
	srv := New(testConfig(), nil, nil, WithSchemaLedger(&fakeLedger{version: 104}))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/schemaz", nil))

	var raw struct {
		Schema struct {
			Version json.Number `json:"version"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, err := raw.Schema.Version.Int64(); err != nil {
		t.Errorf("schema.version = %q, want a JSON number: %v", raw.Schema.Version, err)
	}
}

func TestSchemazDirty(t *testing.T) {
	body := getSchemaz(t, WithSchemaLedger(&fakeLedger{version: 103, dirty: true}))

	if !body.Schema.Dirty || !body.Schema.Applied || body.Schema.Version != 103 {
		t.Errorf("schema = %+v, want {Version:103 Dirty:true Applied:true}", body.Schema)
	}
}

// A database no migration has ever run against: the ledger table does not exist
// (42P01) or holds no row. Both are a fresh install, not a failure — and a fresh
// install has to be distinguishable from an ancient one, which is what applied
// is for.
func TestSchemazLedgerAbsent(t *testing.T) {
	for name, err := range map[string]error{
		"table absent": &pgconn.PgError{Code: undefinedTableCode, Message: `relation "schema_migrations" does not exist`},
		"no rows":      pgx.ErrNoRows,
	} {
		t.Run(name, func(t *testing.T) {
			body := getSchemaz(t, WithSchemaLedger(&fakeLedger{err: err}))

			if body.Schema.Applied || body.Schema.Version != 0 || body.Schema.Dirty {
				t.Errorf("schema = %+v, want the zero ledger with applied=false", body.Schema)
			}
			if body.Schema.Error != "" {
				t.Errorf("error = %q; never migrated is an answer, not a failure", body.Schema.Error)
			}
		})
	}
}

// Any other read failure is reported inside a 200 document: the caller sees the
// degraded state instead of a status code that could equally mean "no api here".
func TestSchemazDatabaseError(t *testing.T) {
	body := getSchemaz(t, WithSchemaLedger(&fakeLedger{err: errors.New("connection refused")}))

	if body.Schema.Error == "" {
		t.Fatalf("schema = %+v, want the read failure reported in error", body.Schema)
	}
	if body.Schema.Applied {
		t.Errorf("applied = true on a failed read; an unreadable ledger is not an applied one")
	}
	// The build half still answers — that is the point of not 5xx-ing.
	if body.Software.Name != "vidra" {
		t.Errorf("software = %+v, want the build metadata regardless of the database", body.Software)
	}
}

func TestSchemazWithoutLedgerWired(t *testing.T) {
	body := getSchemaz(t)

	if body.Schema.Applied {
		t.Error("applied = true with no ledger wired; an unread ledger must never read as a fresh install")
	}
	if body.Schema.Error == "" {
		t.Error("error is empty with no ledger wired; the caller must be told the ledger was not read")
	}
}
