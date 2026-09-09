package pgconv

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// A STOPPED postgres produces *pgconn.ConnectError, whose cause field is
// unexported — so the only honest way to pin it is to make a connection that
// really does fail.
//
// It dials a UNIX SOCKET DIRECTORY that cannot exist rather than a closed TCP
// port. Both produce the same *pgconn.ConnectError, but only this one is
// unconditional: "port 1 is closed" is an assumption about the machine, and a
// test that has to skip itself when the assumption breaks is a test that can
// silently stop running. This touches no network at all and cannot be raced by
// anything on the host.
func TestAFailedConnectionIsUnavailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := pgconn.Connect(ctx,
		"postgres://vidra@/vidra?host=/nonexistent-vidra-socket-dir&sslmode=disable")
	if err == nil {
		t.Fatal("connecting through a socket directory that does not exist succeeded")
	}
	var ce *pgconn.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("a failed dial produced %T, not *pgconn.ConnectError: %v", err, err)
	}
	if !IsUnavailable(fmt.Errorf("query videos: %w", err)) {
		t.Errorf("a failed dial is not classified unavailable: %v", err)
	}
}

// The predicate's whole value is where it stops. A query that RAN and answered
// something the caller did not like is not an outage: calling a statement
// timeout "unavailable" would turn a slow query into a dependency failure on
// the status page and tell a client to retry a request that will fail the same
// way.
func TestIsUnavailableSeparatesTheStoreFromTheStatement(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		want  bool
	}{
		{"connection exception", "08000", true},
		{"connection does not exist", "08003", true},
		{"connection failure", "08006", true},
		{"admin shutdown", "57P01", true},
		{"crash shutdown", "57P02", true},
		{"cannot connect now", "57P03", true},
		{"too many connections", "53300", true},

		{"statement timeout", "57014", false},
		{"deadlock", "40P01", false},
		{"serialization failure", "40001", false},
		{"unique violation", "23505", false},
		{"check violation", "23514", false},
		{"undefined table", "42P01", false},
		{"insufficient privilege", "42501", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fmt.Errorf("query: %w", &pgconn.PgError{Code: tc.state, Message: tc.name})
			if got := IsUnavailable(err); got != tc.want {
				t.Errorf("IsUnavailable(%s) = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
	if IsUnavailable(nil) {
		t.Error("a nil error is not an outage")
	}
	if IsUnavailable(errors.New("something else entirely")) {
		t.Error("an unclassified error is not an outage")
	}
}
