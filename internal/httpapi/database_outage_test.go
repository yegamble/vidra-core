package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// A34 stopped PostgreSQL under a live instance and measured two different
// answers to one outage: an AUTHENTICATED request got a typed 503
// `session_store_unavailable`, because requireAuth distinguishes a refused token
// from a store it could not ask, while an ANONYMOUS read of the same instance
// got a bare `500 internal_error / "an unexpected error occurred"` — no
// anonymous path classified the driver's error at all.
//
// The asymmetry is the finding. The anonymous visitor is the one who cannot
// read a status page, and 500 tells them the request was wrong when the request
// was fine.
//
// The dial-refused half — *pgconn.ConnectError, which is what a STOPPED
// postgres produces — is pinned in internal/pgconv against a real refused
// connection, because the type's cause field is unexported and cannot be
// constructed here.
func TestAnonymousReadDuringADatabaseOutageIsATyped503(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	_ = srv.Handler()

	for _, tc := range []struct {
		name string
		err  error
	}{
		{
			// The server went away mid-query (SQLSTATE class 08).
			name: "connection exception",
			err:  fmt.Errorf("query videos: %w", &pgconn.PgError{Code: "08006", Message: "connection failure"}),
		},
		{
			// The server is up and refusing to serve.
			name: "admin shutdown",
			err:  fmt.Errorf("query videos: %w", &pgconn.PgError{Code: "57P01", Message: "terminating connection due to administrator command"}),
		},
		{
			name: "too many connections",
			err:  fmt.Errorf("query videos: %w", &pgconn.PgError{Code: "53300", Message: "sorry, too many clients already"}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c := srv.echo.NewContext(httptest.NewRequest(http.MethodGet, "/api/v1/videos", nil), rec)
			srv.echo.HTTPErrorHandler(tc.err, c)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
			}
			if body.Error.Code != "database_unavailable" {
				t.Errorf("code = %q, want database_unavailable", body.Error.Code)
			}
			if strings.Contains(body.Error.Message, "unexpected error") {
				t.Errorf("the scrubber ate the message: %q", body.Error.Message)
			}
			// The client half must carry nothing about where the database is.
			for _, leak := range []string{"127.0.0.1", "5432", "connection failure", "too many clients"} {
				if strings.Contains(body.Error.Message, leak) {
					t.Errorf("the response body leaks %q: %s", leak, body.Error.Message)
				}
			}
		})
	}
}

// The other half of the predicate, and the more important one: a query that RAN
// and answered something the caller did not like is not an outage. Calling a
// statement timeout or a deadlock "unavailable" would turn a slow query into a
// dependency failure on the operator's status page and tell a client to retry a
// request that will fail the same way.
func TestQueriesThatANSWEREDAreNotOutages(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	_ = srv.Handler()

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"statement timeout", fmt.Errorf("%w", &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"})},
		{"deadlock", fmt.Errorf("%w", &pgconn.PgError{Code: "40P01", Message: "deadlock detected"})},
		{"serialization failure", fmt.Errorf("%w", &pgconn.PgError{Code: "40001", Message: "could not serialize access"})},
		{"check violation", fmt.Errorf("%w", &pgconn.PgError{Code: "23514", Message: "violates check constraint"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c := srv.echo.NewContext(httptest.NewRequest(http.MethodGet, "/api/v1/videos", nil), rec)
			srv.echo.HTTPErrorHandler(tc.err, c)

			if rec.Code == http.StatusServiceUnavailable {
				t.Fatalf("status = 503 for %s; a query that answered is not an outage", tc.name)
			}
		})
	}
}
