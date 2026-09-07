package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestAuthenticateAccessTokenRefusesATokenWithNoSession pins the fail-closed
// rule at the seam that enforces it: a token that names no session cannot be
// checked against a revocation, so it authenticates nothing. Only a binary from
// before AUTH-05 slice (c) could have minted one, and its holder's refresh token
// is untouched, so a client re-authenticates transparently on the next 401.
//
// It is asserted here rather than through the HTTP middleware because it is a
// property of the seam, and proving it there would mean forging a signed JWT —
// i.e. a second copy of the signing secret in another package's tests.
func TestAuthenticateAccessTokenRefusesATokenWithNoSession(t *testing.T) {
	svc := newTestService(newFakeRepo())
	id := uuid.New()

	claims := &Claims{}
	claims.Subject = id.String()
	if _, err := svc.AuthenticateAccessToken(context.Background(), claims); !errors.Is(err, ErrSessionRevoked) {
		t.Errorf("a token with no session id authenticated: err = %v, want ErrSessionRevoked", err)
	}

	// The same for a nil claims set and an unparseable subject — every failure
	// collapses to the one error the HTTP layer answers 401 for.
	if _, err := svc.AuthenticateAccessToken(context.Background(), nil); !errors.Is(err, ErrSessionRevoked) {
		t.Errorf("nil claims: err = %v, want ErrSessionRevoked", err)
	}
	bad := &Claims{SessionID: uuid.NewString()}
	bad.Subject = "not-a-uuid"
	if _, err := svc.AuthenticateAccessToken(context.Background(), bad); !errors.Is(err, ErrSessionRevoked) {
		t.Errorf("unparseable subject: err = %v, want ErrSessionRevoked", err)
	}

	// And a well-formed token whose session simply is not there.
	missing := &Claims{SessionID: uuid.NewString()}
	missing.Subject = id.String()
	if _, err := svc.AuthenticateAccessToken(context.Background(), missing); !errors.Is(err, ErrSessionRevoked) {
		t.Errorf("unknown session: err = %v, want ErrSessionRevoked", err)
	}
}

// TestAuthenticateAccessTokenSeparatesAnOutageFromARevocation is the whole
// point of ErrSessionLookupUnavailable. Before it, EVERY error out of
// GetActiveSessionForAccessToken — including "the database is unreachable" —
// collapsed to ErrSessionRevoked, so a Postgres outage made every authenticated
// route answer 401 "invalid or expired token". Two things follow from that and
// both are bad: the admin status page whose JOB is to report the outage becomes
// unreadable, and every signed-in client is told its session is invalid, which
// is how a blip turns into a fleet-wide sign-out.
//
// The revocation semantics are unchanged and are asserted here beside it: a
// session that is genuinely absent still answers ErrSessionRevoked, because
// "no row" is the ONE answer that means the token no longer authorizes.
func TestAuthenticateAccessTokenSeparatesAnOutageFromARevocation(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	claims := &Claims{SessionID: uuid.NewString()}
	claims.Subject = uuid.New().String()

	// No row: still a revocation.
	if _, err := svc.AuthenticateAccessToken(context.Background(), claims); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("absent session: err = %v, want ErrSessionRevoked", err)
	}

	// The store itself is down. Not a revocation — nothing was learned about
	// this session at all.
	repo.sessionLookupErr = errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")
	_, err := svc.AuthenticateAccessToken(context.Background(), claims)
	if !errors.Is(err, ErrSessionLookupUnavailable) {
		t.Fatalf("session store down: err = %v, want ErrSessionLookupUnavailable", err)
	}
	if errors.Is(err, ErrSessionRevoked) {
		t.Error("an outage still reports as a revocation, so the caller cannot tell them apart")
	}
	// The driver's message must not travel with the error: it reaches an
	// unauthenticated caller through the HTTP layer, and a DSN in a connection
	// error is exactly the kind of thing that must not.
	if strings.Contains(err.Error(), "127.0.0.1:5432") {
		t.Errorf("the underlying connection error leaked into the returned error: %v", err)
	}
}
