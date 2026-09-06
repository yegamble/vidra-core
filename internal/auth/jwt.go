package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ErrInvalidToken is returned when a token fails signature, expiry, issuer, or
// audience validation.
var ErrInvalidToken = errors.New("auth: invalid token")

// Claims is the vidra-core access-token payload: standard registered claims plus
// the user's role for coarse authorization and, for access tokens, the id of the
// session the token was minted from.
type Claims struct {
	Role string `json:"role"`
	// SessionID binds an ACCESS token to its sessions row so revocation can
	// reach it. Without it the JWT is self-authenticating for its whole TTL:
	// revoking a session (sign out everywhere, a password change, deactivation,
	// the §1 hard delete) killed only the REFRESH token, and the already-issued
	// access token kept writing for up to JWT_ACCESS_TTL — proven in the A12
	// deletion evidence, where a hard-deleted account created a channel, a
	// playlist and a fresh archive of itself after its own tombstone.
	//
	// It is omitempty because the single-purpose mfa_token carries no session
	// (it is minted BEFORE one exists). The auth middleware refuses an access
	// token without it: a token that names no session cannot be checked against
	// a revocation, so it fails closed.
	SessionID string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

// TokenIssuer mints and verifies HS256 access tokens. The signing secret never
// leaves this type.
type TokenIssuer struct {
	secret   []byte
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time // injectable clock for tests
}

// NewTokenIssuer builds a TokenIssuer. ttl is the access-token lifetime.
func NewTokenIssuer(secret, issuer, audience string, ttl time.Duration) *TokenIssuer {
	return &TokenIssuer{
		secret:   []byte(secret),
		issuer:   issuer,
		audience: audience,
		ttl:      ttl,
		now:      time.Now,
	}
}

// TTL reports the configured access-token lifetime.
func (t *TokenIssuer) TTL() time.Duration { return t.ttl }

// Issue returns a signed token for the given user and role with NO session
// binding. It is for single-purpose tokens that exist before a session does —
// today only the mfa_token, which lives on its own audience and can never be
// presented as an access token. Access tokens use IssueForSession.
func (t *TokenIssuer) Issue(userID uuid.UUID, role string) (string, error) {
	return t.IssueForSession(userID, role, "")
}

// IssueForSession returns a signed access token bound to sessionID. The binding
// is what makes revocation effective: the auth middleware resolves the session
// on every authenticated request, so revoking the row (or disabling/deleting the
// account) invalidates this token immediately rather than at its expiry.
func (t *TokenIssuer) IssueForSession(userID uuid.UUID, role, sessionID string) (string, error) {
	now := t.now()
	claims := Claims{
		Role:      role,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    t.issuer,
			Audience:  jwt.ClaimStrings{t.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(t.secret)
}

// Parse validates a token's signature and registered claims and returns the
// claims. It pins the algorithm to HS256 (defeating alg-confusion attacks) and
// enforces issuer and audience.
func (t *TokenIssuer) Parse(token string) (*Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(token, &claims, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("%w: unexpected signing method %q", ErrInvalidToken, tok.Header["alg"])
		}
		return t.secret, nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(t.issuer),
		jwt.WithAudience(t.audience),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return &claims, nil
}
