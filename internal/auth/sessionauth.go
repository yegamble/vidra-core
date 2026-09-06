package auth

import (
	"context"

	"github.com/google/uuid"
)

// Principal is the authenticated caller the middleware builds from an access
// token: the account id the token names and the role that account holds RIGHT
// NOW. The role deliberately comes from the same row the revocation check
// reads, not from the JWT's copy of it — see AuthenticateAccessToken.
type Principal struct {
	UserID uuid.UUID
	Role   string
}

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
//
// The ROLE comes off that same row for the same reason. A JWT's role claim is a
// snapshot taken at sign-in, so trusting it left one piece of stale principal
// state behind after AUTH-05 closed the others: a demoted moderator kept every
// staff route until their access token expired, and a promoted one could not
// use them until it did. The row is already being read, so this costs nothing.
func (s *Service) AuthenticateAccessToken(ctx context.Context, claims *Claims) (Principal, error) {
	if claims == nil {
		return Principal{}, ErrSessionRevoked
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return Principal{}, ErrSessionRevoked
	}
	// Fail closed on a token that names no session: it cannot be checked
	// against a revocation, so it cannot be trusted. Only a previous binary
	// could have minted one (every access token has carried a session id since
	// AUTH-05 slice (c)), and its holder's refresh token is untouched, so the
	// client re-authenticates transparently on the next 401.
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return Principal{}, ErrSessionRevoked
	}
	sess, err := s.repo.GetActiveSessionForAccessToken(ctx, sessionID)
	if err != nil {
		return Principal{}, ErrSessionRevoked
	}
	// A session belongs to exactly one account; a token whose subject disagrees
	// with the session row is not something this instance minted coherently.
	if sess.UserID != userID {
		return Principal{}, ErrSessionRevoked
	}
	return Principal{UserID: userID, Role: sess.Role}, nil
}
