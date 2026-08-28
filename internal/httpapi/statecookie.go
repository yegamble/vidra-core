package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// Signed per-attempt state cookies.
//
// Both browser login flows that leave the instance and come back — OAuth/OIDC
// and ATProto — have to park an attempt's secrets somewhere across the round
// trip: the CSRF state, the nonce, the PKCE verifier, and (for ATProto) the
// ephemeral DPoP private key. They park them in an httpOnly cookie sealed with
// HMAC-SHA256 over the instance's JWT secret — the same trust domain as the
// sessions those flows go on to mint.
//
// This is security-critical and was written out twice, verbatim. One copy is
// enough: a fix to the compare, the encoding, or the cookie attributes has to
// land in one place or it is not a fix.
//
// What the seal does NOT do is encrypt. The payload is signed, not hidden — it
// is base64 of plain JSON, readable by anyone holding the cookie. That is safe
// only because the cookie is httpOnly (out of reach of scripts), path-scoped to
// its own flow, and single-use inside a ten-minute window; the DPoP key riding
// in the ATProto payload is worthless after the exchange it authorises.

// statePayload is a sealed attempt payload: a CSRF state token and a seal time,
// both of which openState checks before handing the payload back.
type statePayload interface {
	// stateToken is the attempt's CSRF token. Empty means the payload is
	// structurally unusable, however well-signed it is.
	stateToken() string
	// issuedAt is when the payload was sealed (unix seconds).
	issuedAt() int64
}

// sealState encodes p as JSON and appends a detached HMAC-SHA256 tag, both
// base64url: "<body>.<sig>". The key is the instance's JWT secret.
func sealState[T statePayload](secret string, p T) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// openState verifies and decodes a sealed cookie value, rejecting — with no
// detail, deliberately — a malformed envelope, a bad signature, an unparseable
// payload, a payload with no state token, and one sealed longer than ttl ago.
//
// The signature check is hmac.Equal (constant time). A byte-wise compare here
// would leak the correct tag one prefix at a time to a caller willing to retry.
func openState[T statePayload](secret, sealed string, ttl time.Duration) (T, bool) {
	var zero T
	bodyB64, sigB64, ok := strings.Cut(sealed, ".")
	if !ok {
		return zero, false
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyB64)
	if err != nil {
		return zero, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return zero, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return zero, false
	}
	var p T
	if err := json.Unmarshal(body, &p); err != nil {
		return zero, false
	}
	if p.stateToken() == "" || time.Since(time.Unix(p.issuedAt(), 0)) > ttl {
		return zero, false
	}
	return p, true
}

// writeStateCookie sets a flow cookie with the attributes every one of them
// needs: httpOnly (invisible to scripts), SameSite=Lax (so it still rides the
// top-level GET back from the provider, but not a cross-site POST), and Secure
// whenever the instance is served over https.
func (s *Server) writeStateCookie(c echo.Context, name, path, value string, maxAge time.Duration) {
	c.SetCookie(&http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure(),
	})
}

// clearStateCookie expires a flow cookie (Max-Age=-1). The attributes have to
// match the ones it was written with or the browser keeps the old cookie.
func (s *Server) clearStateCookie(c echo.Context, name, path string) {
	c.SetCookie(&http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure(),
	})
}
