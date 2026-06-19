// Package auth implements account authentication for vidra-core: password
// hashing, JWT access tokens, and the registration/login application logic. It
// is HTTP-agnostic and testable without a server.
package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost is the work factor for password hashing. 12 is a sensible 2020s
// default: meaningfully slower than the library default (10) without making
// login latency unacceptable. It is a var (not const) only so tests can lower
// it for speed; production code never reassigns it.
var bcryptCost = 12

// HashPassword returns a bcrypt hash of the plaintext password. The cost and
// salt are embedded in the returned string, so it is self-describing for
// verification.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword reports whether plain matches the stored bcrypt hash. It returns
// a non-nil error when they do not match (or the hash is malformed); callers
// must treat any error as an authentication failure and must not distinguish
// "wrong password" from "user not found" to the client.
func CheckPassword(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}
