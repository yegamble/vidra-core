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
// the user's role for coarse authorization.
type Claims struct {
	Role string `json:"role"`
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

// Issue returns a signed access token for the given user and role.
func (t *TokenIssuer) Issue(userID uuid.UUID, role string) (string, error) {
	now := t.now()
	claims := Claims{
		Role: role,
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
