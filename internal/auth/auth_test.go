package auth

import (
    "testing"
    "time"
)

// TestHashAndVerify ensures password hashing and verification works as expected.
func TestHashAndVerify(t *testing.T) {
    pw := "secret-password"
    hash, err := HashPassword(pw)
    if err != nil {
        t.Fatalf("hashing failed: %v", err)
    }
    if err := VerifyPassword(hash, pw); err != nil {
        t.Fatalf("verification failed: %v", err)
    }
    if err := VerifyPassword(hash, "wrong"); err == nil {
        t.Fatalf("expected error for wrong password, got nil")
    }
}

// TestJWTGenerationAndValidation ensures tokens can be generated and
// validated. It checks that expired tokens are rejected.
func TestJWTGenerationAndValidation(t *testing.T) {
    secret := "testsecret"
    token, err := GenerateJWT(123, secret, time.Second)
    if err != nil {
        t.Fatalf("token generation: %v", err)
    }
    claims, err := ValidateJWT(token, secret)
    if err != nil {
        t.Fatalf("validate: %v", err)
    }
    if claims.UserID != 123 {
        t.Fatalf("unexpected user id: got %d", claims.UserID)
    }
    // Wait for expiry
    time.Sleep(2 * time.Second)
    if _, err := ValidateJWT(token, secret); err == nil {
        t.Fatalf("expected expired token to be invalid")
    }
}