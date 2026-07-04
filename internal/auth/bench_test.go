package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// BenchmarkTokenParse measures the hot access-token verification path: every
// authenticated request runs TokenIssuer.Parse (HS256 signature + issuer +
// audience + expiry checks). Regression here taxes the whole authenticated API.
func BenchmarkTokenParse(b *testing.B) {
	iss := NewTokenIssuer("bench-secret-bench-secret-bench-secret-0", "vidra", "vidra", 15*time.Minute)
	tok, err := iss.Issue(uuid.New(), "user")
	if err != nil {
		b.Fatalf("issue: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := iss.Parse(tok); err != nil {
			b.Fatalf("parse: %v", err)
		}
	}
}

// BenchmarkTokenIssue measures the token minting path (login/refresh).
func BenchmarkTokenIssue(b *testing.B) {
	iss := NewTokenIssuer("bench-secret-bench-secret-bench-secret-0", "vidra", "vidra", 15*time.Minute)
	uid := uuid.New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := iss.Issue(uid, "user"); err != nil {
			b.Fatalf("issue: %v", err)
		}
	}
}
