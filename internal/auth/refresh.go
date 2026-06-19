package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// refreshTokenBytes is the entropy of a raw refresh token (256 bits).
const refreshTokenBytes = 32

// generateRefreshToken returns a new high-entropy opaque refresh token and its
// storage hash. The raw token goes to the client exactly once; only the hash is
// persisted. Because the token is already high-entropy random, a fast hash
// (SHA-256) is the correct choice — bcrypt is for low-entropy passwords.
func generateRefreshToken() (raw, hash string, err error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashRefreshToken(raw), nil
}

// hashRefreshToken returns the hex SHA-256 of a raw refresh token, used as the
// lookup key in the sessions table.
func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
