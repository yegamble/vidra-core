package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sign-in accepts an email OR a username. These tests pin the security
// invariants that make that safe, because usernames have never been
// charset-restricted: a username may look like an email and may literally
// equal ANOTHER account's email (the unique indexes are per-column only).
//
//  1. EMAIL WINS. An identifier that is some account's email always resolves
//     to that account, never to whoever picked it as a username.
//  2. EXACTLY ONE password compare per attempt. A matched account with a wrong
//     password is a flat 401 — never a second lookup against the other column,
//     which would let one string reach two accounts.
//  3. The is_active / verification-hold / MFA gates stay behind the password
//     compare, identical whichever column matched.

func TestLoginByUsername(t *testing.T) {
	svc := newTestService(newFakeRepo())
	register(t, svc, "ada", "ada@example.test")

	res, err := svc.Login(context.Background(), LoginInput{Identifier: "ada", Password: "supersecret"}, "test-agent")
	if err != nil {
		t.Fatalf("Login by username: %v", err)
	}
	if res.Tokens.AccessToken == "" || res.Tokens.RefreshToken == "" || res.User.Username != "ada" {
		t.Errorf("unexpected login result: %+v", res)
	}
}

func TestLoginByUsernameIsCaseInsensitive(t *testing.T) {
	svc := newTestService(newFakeRepo())
	register(t, svc, "Ada", "ada@example.test")

	res, err := svc.Login(context.Background(), LoginInput{Identifier: "  aDa  ", Password: "supersecret"}, "test-agent")
	if err != nil {
		t.Fatalf("Login by mixed-case username: %v", err)
	}
	if res.User.Username != "Ada" {
		t.Errorf("resolved username = %q, want Ada", res.User.Username)
	}
}

func TestLoginEmailFieldStillWorks(t *testing.T) {
	// Back-compat: callers that still populate LoginInput.Email (and never set
	// Identifier) behave exactly as before.
	svc := newTestService(newFakeRepo())
	register(t, svc, "ada", "ada@example.test")

	if _, err := svc.Login(context.Background(), LoginInput{Email: "ADA@example.test", Password: "supersecret"}, "a"); err != nil {
		t.Fatalf("Login by email field: %v", err)
	}
}

// TestLoginEmailWinsOverLookalikeUsername is the impersonation guard. mallory
// takes the literal string "ada@example.test" as her USERNAME; ada owns it as
// her EMAIL. That identifier must reach ada and only ada — otherwise anyone
// could park on a victim's address and harvest sign-in attempts.
func TestLoginEmailWinsOverLookalikeUsername(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	ada, _ := register(t, svc, "ada", "ada@example.test")
	mallory, _ := register(t, svc, "ada@example.test", "mallory@example.test")

	// ada's own password wins the shared identifier.
	res, err := svc.Login(context.Background(), LoginInput{Identifier: "ada@example.test", Password: "supersecret"}, "a")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.User.ID != ada.ID {
		t.Fatalf("identifier resolved to %s (%s), want ada %s", res.User.ID, res.User.Username, ada.ID)
	}

	// And mallory is NOT reachable through it even with her own password: the
	// email branch already claimed the identifier, so this is a wrong-password
	// attempt against ada.
	repo.reset()
	if _, err := svc.Login(context.Background(), LoginInput{Identifier: "ada@example.test", Password: "mallorysecret"}, "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("shadowed login err = %v, want ErrInvalidCredentials", err)
	}
	if repo.loginLookups != 1 {
		t.Errorf("account lookups = %d, want exactly 1 (no fallthrough to the username branch)", repo.loginLookups)
	}
	_ = mallory
}

// TestLoginWrongPasswordOnUsernameMatchDoesNotFallThrough proves invariant 2
// from the other direction: a username that matched must not be retried
// against the email column after the compare fails.
func TestLoginWrongPasswordOnUsernameMatchDoesNotFallThrough(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	register(t, svc, "ada", "ada@example.test")

	repo.reset()
	if _, err := svc.Login(context.Background(), LoginInput{Identifier: "ada", Password: "nope"}, "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
	if repo.loginLookups != 1 {
		t.Errorf("account lookups = %d, want exactly 1", repo.loginLookups)
	}
	if repo.emailLookups != 0 {
		t.Errorf("GetUserByEmail called %d times; Login must use only the login-identifier lookup", repo.emailLookups)
	}
}

// TestLoginUnknownIdentifierStillDummyCompares keeps the anti-enumeration
// dummy bcrypt compare on the total-miss path. Test hashes run at
// bcrypt.MinCost while the dummy hash literal is cost 12, so a real compare is
// hundreds of milliseconds and a skipped one is microseconds — the floor below
// is a structural assertion, not a benchmark.
func TestLoginUnknownIdentifierStillDummyCompares(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	register(t, svc, "ada", "ada@example.test")

	repo.reset()
	start := time.Now()
	if _, err := svc.Login(context.Background(), LoginInput{Identifier: "nobody", Password: "whatever"}, "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
	if elapsed := time.Since(start); elapsed < 2*time.Millisecond {
		t.Errorf("miss returned in %v — the dummy bcrypt compare was skipped, re-opening the timing oracle", elapsed)
	}
	if repo.loginLookups != 1 {
		t.Errorf("account lookups = %d, want exactly 1", repo.loginLookups)
	}
}

// TestLoginDisabledAccountViaUsername pins that the is_active check stays
// AFTER the password compare (the lookup itself must not filter on it), so a
// username reaches a disabled account exactly like its email does.
func TestLoginDisabledAccountViaUsername(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	user, _ := register(t, svc, "ada", "ada@example.test")
	if err := repo.DeactivateUser(context.Background(), user.ID); err != nil {
		t.Fatalf("DeactivateUser: %v", err)
	}

	if _, err := svc.Login(context.Background(), LoginInput{Identifier: "ada", Password: "supersecret"}, "a"); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("username login on disabled account = %v, want ErrAccountDisabled", err)
	}
	// Identical via the email column — the two identifiers are interchangeable.
	if _, err := svc.Login(context.Background(), LoginInput{Identifier: "ada@example.test", Password: "supersecret"}, "a"); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("email login on disabled account = %v, want ErrAccountDisabled", err)
	}
	// A wrong password on a disabled account is still ErrInvalidCredentials:
	// the compare comes first, so "disabled" never leaks pre-credential.
	if _, err := svc.Login(context.Background(), LoginInput{Identifier: "ada", Password: "nope"}, "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password on disabled account = %v, want ErrInvalidCredentials", err)
	}
}

// TestLoginVerificationHoldViaUsername: the W7 hold applies identically
// whichever column matched.
func TestLoginVerificationHoldViaUsername(t *testing.T) {
	repo := newFakeRepo()
	gate := true
	capture := NewCaptureMailer()
	svc := NewService(repo, newTestIssuer(), time.Hour,
		WithMailer(capture),
		WithEmailVerificationGateFunc(func() bool { return gate }))

	if _, err := svc.RegisterPendingVerification(context.Background(), RegisterInput{
		Username: "bob", Email: "bob@example.test", Password: "supersecret",
	}); err != nil {
		t.Fatalf("RegisterPendingVerification: %v", err)
	}

	if _, err := svc.Login(context.Background(), LoginInput{Identifier: "bob", Password: "supersecret"}, "ua"); !errors.Is(err, ErrEmailVerificationRequired) {
		t.Fatalf("held username login = %v, want ErrEmailVerificationRequired", err)
	}

	raw, ok := capture.Latest(TokenKindEmailVerification, "bob@example.test")
	if !ok {
		t.Fatalf("no verification token captured")
	}
	if err := svc.VerifyEmail(context.Background(), raw); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	if _, err := svc.Login(context.Background(), LoginInput{Identifier: "bob", Password: "supersecret"}, "ua"); err != nil {
		t.Fatalf("released username login = %v, want nil", err)
	}
}

// TestLoginMFAChallengeViaUsername: an MFA-enabled account withholds the
// session for a username sign-in exactly as for an email one.
func TestLoginMFAChallengeViaUsername(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newMFAService(t, nil)
	user, _ := register(t, svc, "ada", "ada@example.test")

	enr, err := svc.BeginTOTPEnrollment(ctx, user.ID)
	if err != nil {
		t.Fatalf("BeginTOTPEnrollment: %v", err)
	}
	if _, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, enr.Secret, time.Now())); err != nil {
		t.Fatalf("VerifyTOTPEnrollment: %v", err)
	}

	res, err := svc.Login(ctx, LoginInput{Identifier: "ada", Password: "supersecret"}, "ua")
	if err != nil {
		t.Fatalf("MFA username login: %v", err)
	}
	if !res.MFARequired || res.MFAToken == "" || res.Tokens.AccessToken != "" || res.Tokens.RefreshToken != "" {
		t.Fatalf("MFA username login = %+v; want mfa_token and no session tokens", res)
	}
}

// TestGetUserByLoginIdentifierFakeMirrorsPrecedence guards the fake itself: if
// it ever stopped preferring the email branch the security tests above would
// pass vacuously.
func TestGetUserByLoginIdentifierFakeMirrorsPrecedence(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	ada, _ := register(t, svc, "ada", "ada@example.test")
	register(t, svc, "ada@example.test", "mallory@example.test")

	got, err := repo.GetUserByLoginIdentifier(context.Background(), "ADA@EXAMPLE.TEST")
	if err != nil {
		t.Fatalf("GetUserByLoginIdentifier: %v", err)
	}
	if got.ID != ada.ID {
		t.Fatalf("resolved %s, want the email owner %s", got.ID, ada.ID)
	}
	var zero sqlcgen.User
	if _, err := repo.GetUserByLoginIdentifier(context.Background(), "nobody"); err == nil {
		t.Fatalf("miss returned %+v, want an error", zero)
	}
}
