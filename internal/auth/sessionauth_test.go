package auth

import (
	"context"
	"errors"
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
