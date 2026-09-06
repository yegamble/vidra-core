package auth

import (
	"context"

	"github.com/google/uuid"
)

// AuthenticateAccessToken turns a parsed access token into an authenticated
// principal, or ErrSessionRevoked. It is the seam that makes revocation
// EFFECTIVE: verifying the JWT alone proves only that this instance minted the
// token, so before this check a revoked session's — or a deactivated or
// hard-deleted account's — unexpired access token kept working on every route
// that did not itself load the user row, for the whole JWT_ACCESS_TTL.
//
// Cost is one indexed read per authenticated request: a primary-key lookup on
// sessions joined to users by primary key (GetActiveSessionForAccessToken).
// There is deliberately NO in-process cache. vidra runs multiple replicas
// (leader election, 2-replica soak), so a cache on one process could not be
// invalidated by a revocation served by another, and the whole point of this
// check is that revocation bites within seconds everywhere. The one query
// covers all four revocation sources at once — session revoked, session
// expired, account deactivated, account tombstoned — because it re-reads the
// account state instead of trusting the token's copy of it.
func (s *Service) AuthenticateAccessToken(ctx context.Context, claims *Claims) (uuid.UUID, error) {
	if claims == nil {
		return uuid.Nil, ErrSessionRevoked
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, ErrSessionRevoked
	}
	// Fail closed on a token that names no session: it cannot be checked
	// against a revocation, so it cannot be trusted. Only a previous binary
	// could have minted one (every access token has carried a session id since
	// AUTH-05 slice (c)), and its holder's refresh token is untouched, so the
	// client re-authenticates transparently on the next 401.
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return uuid.Nil, ErrSessionRevoked
	}
	sess, err := s.repo.GetActiveSessionForAccessToken(ctx, sessionID)
	if err != nil {
		return uuid.Nil, ErrSessionRevoked
	}
	// A session belongs to exactly one account; a token whose subject disagrees
	// with the session row is not something this instance minted coherently.
	if sess.UserID != userID {
		return uuid.Nil, ErrSessionRevoked
	}
	return userID, nil
}
