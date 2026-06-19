package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestIssuer() *TokenIssuer {
	return NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", time.Hour)
}

func TestIssueAndParseRoundTrip(t *testing.T) {
	iss := newTestIssuer()
	id := uuid.New()

	tok, err := iss.Issue(id, "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.Subject != id.String() {
		t.Errorf("sub = %q, want %q", claims.Subject, id.String())
	}
	if claims.Role != "admin" {
		t.Errorf("role = %q, want admin", claims.Role)
	}
}

func TestParseRejectsTamperedToken(t *testing.T) {
	iss := newTestIssuer()
	tok, _ := iss.Issue(uuid.New(), "user")
	if _, err := iss.Parse(tok + "x"); err == nil {
		t.Error("Parse accepted a tampered token")
	}
}

func TestParseRejectsWrongSecret(t *testing.T) {
	tok, _ := newTestIssuer().Issue(uuid.New(), "user")
	other := NewTokenIssuer("a-totally-different-secret-aaaaaaaaaa", "vidra", "vidra", time.Hour)
	if _, err := other.Parse(tok); err == nil {
		t.Error("Parse accepted a token signed with a different secret")
	}
}

func TestParseRejectsExpiredToken(t *testing.T) {
	iss := newTestIssuer()
	iss.now = func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) }
	tok, _ := iss.Issue(uuid.New(), "user")

	// Verify "now" well after expiry.
	iss.now = func() time.Time { return time.Date(2020, 1, 1, 2, 0, 0, 0, time.UTC) }
	if _, err := iss.Parse(tok); err == nil {
		t.Error("Parse accepted an expired token")
	}
}

func TestParseRejectsWrongAudience(t *testing.T) {
	tok, _ := newTestIssuer().Issue(uuid.New(), "user")
	other := NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "other-audience", time.Hour)
	if _, err := other.Parse(tok); err == nil {
		t.Error("Parse accepted a token with the wrong audience")
	}
}
