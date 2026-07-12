package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRegisterSeedsHistoryPreference proves new_user_history_enabled seeds the
// per-user watch-history preference at account creation — live per call, and
// defaulting to true when no provider is wired (the shipped behaviour).
func TestRegisterSeedsHistoryPreference(t *testing.T) {
	seed := true
	svc := NewService(newFakeRepo(), newTestIssuer(), time.Hour,
		WithNewUserHistoryEnabledFunc(func() bool { return seed }))

	u, _ := register(t, svc, "ada", "ada@example.test")
	if !u.HistoryEnabled {
		t.Fatalf("seed=true → HistoryEnabled = false, want true")
	}
	seed = false
	u2, _ := register(t, svc, "bob", "bob@example.test")
	if u2.HistoryEnabled {
		t.Fatalf("seed=false → HistoryEnabled = true, want false")
	}

	// No provider wired: history defaults on.
	u3, _ := register(t, newTestService(newFakeRepo()), "cal", "cal@example.test")
	if !u3.HistoryEnabled {
		t.Fatalf("no provider → HistoryEnabled = false, want true")
	}
}

// TestApproveRegistrationSeedsHistoryPreference proves the approval path seeds
// the same preference (the account is created at approval time).
func TestApproveRegistrationSeedsHistoryPreference(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, newTestIssuer(), time.Hour,
		WithNewUserHistoryEnabledFunc(func() bool { return false }))
	req, err := svc.RequestRegistration(context.Background(), RegisterInput{
		Username: "bob", Email: "bob@example.test", Password: "supersecret",
	}, "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	admin, _ := register(t, svc, "ada", "ada@example.test")
	u, err := svc.ApproveRegistration(context.Background(), admin.ID, req.ID)
	if err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	if u.HistoryEnabled {
		t.Fatalf("approved account HistoryEnabled = true, want false (seed off)")
	}
}

// TestEmailVerificationHold drives the registration verification gate end to
// end at the service level: a gated signup creates a held (pending) account
// with NO session, sends the verification message, login answers
// ErrEmailVerificationRequired until the token is consumed, and then works.
func TestEmailVerificationHold(t *testing.T) {
	repo := newFakeRepo()
	gate := true
	capture := NewCaptureMailer()
	svc := NewService(repo, newTestIssuer(), time.Hour,
		WithMailer(capture),
		WithEmailVerificationGateFunc(func() bool { return gate }))

	u, err := svc.RegisterPendingVerification(context.Background(), RegisterInput{
		Username: "bob", Email: "bob@example.test", Password: "supersecret",
	})
	if err != nil {
		t.Fatalf("RegisterPendingVerification: %v", err)
	}
	if !u.PendingEmailVerification {
		t.Fatalf("held account PendingEmailVerification = false, want true")
	}

	// Login is refused with the typed sentinel while pending + gate active.
	if _, err := svc.Login(context.Background(), LoginInput{Email: "bob@example.test", Password: "supersecret"}, "ua"); !errors.Is(err, ErrEmailVerificationRequired) {
		t.Fatalf("Login while pending = %v, want ErrEmailVerificationRequired", err)
	}

	// The verification message was sent; consuming the token releases the hold.
	raw, ok := capture.Latest(TokenKindEmailVerification, "bob@example.test")
	if !ok {
		t.Fatalf("no verification token captured")
	}
	if err := svc.VerifyEmail(context.Background(), raw); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	if _, err := svc.Login(context.Background(), LoginInput{Email: "bob@example.test", Password: "supersecret"}, "ua"); err != nil {
		t.Fatalf("Login after verification = %v, want nil", err)
	}
}

// TestEmailVerificationGrandfatherClause proves accounts created while the
// gate was OFF are never retroactively locked when it flips on, and that
// turning the gate off releases accounts still held.
func TestEmailVerificationGrandfatherClause(t *testing.T) {
	repo := newFakeRepo()
	gate := false
	svc := NewService(repo, newTestIssuer(), time.Hour,
		WithEmailVerificationGateFunc(func() bool { return gate }))

	// ada registers while the gate is off: pending=false, email unverified.
	register(t, svc, "ada", "ada@example.test")

	// The toggle flips on — ada must still be able to log in (grandfathered).
	gate = true
	if _, err := svc.Login(context.Background(), LoginInput{Email: "ada@example.test", Password: "supersecret"}, "ua"); err != nil {
		t.Fatalf("grandfathered Login = %v, want nil", err)
	}

	// bob registers through the gate and is held…
	if _, err := svc.RegisterPendingVerification(context.Background(), RegisterInput{
		Username: "bob", Email: "bob@example.test", Password: "supersecret",
	}); err != nil {
		t.Fatalf("RegisterPendingVerification: %v", err)
	}
	if _, err := svc.Login(context.Background(), LoginInput{Email: "bob@example.test", Password: "supersecret"}, "ua"); !errors.Is(err, ErrEmailVerificationRequired) {
		t.Fatalf("held Login = %v, want ErrEmailVerificationRequired", err)
	}
	// …until the admin turns the gate off, which releases held accounts.
	gate = false
	if _, err := svc.Login(context.Background(), LoginInput{Email: "bob@example.test", Password: "supersecret"}, "ua"); err != nil {
		t.Fatalf("released Login = %v, want nil", err)
	}
}

// TestApproveRegistrationComposesWithVerificationGate proves the documented
// order: approval first, then (gate active at approval time) the created
// account is held pending verification and the message is sent.
func TestApproveRegistrationComposesWithVerificationGate(t *testing.T) {
	repo := newFakeRepo()
	capture := NewCaptureMailer()
	svc := NewService(repo, newTestIssuer(), time.Hour,
		WithMailer(capture),
		WithEmailVerificationGateFunc(func() bool { return true }))

	admin, _ := register(t, svc, "ada", "ada@example.test") // pre-gate helper account
	req, err := svc.RequestRegistration(context.Background(), RegisterInput{
		Username: "bob", Email: "bob@example.test", Password: "supersecret",
	}, "please")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	u, err := svc.ApproveRegistration(context.Background(), admin.ID, req.ID)
	if err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	if !u.PendingEmailVerification {
		t.Fatalf("approved-under-gate account not pending verification")
	}
	if _, ok := capture.Latest(TokenKindEmailVerification, "bob@example.test"); !ok {
		t.Fatalf("no verification message sent on approval")
	}
	if _, err := svc.Login(context.Background(), LoginInput{Email: "bob@example.test", Password: "supersecret"}, "ua"); !errors.Is(err, ErrEmailVerificationRequired) {
		t.Fatalf("approved-but-unverified Login = %v, want ErrEmailVerificationRequired", err)
	}
}

// TestRegisterCreateUserParams pins the exact CreateUser parameters a plain
// register writes: never pending, history seeded.
func TestRegisterCreateUserParams(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	u, _ := register(t, svc, "ada", "ada@example.test")
	stored, err := repo.GetUserByEmail(context.Background(), "ada@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if stored.ID != u.ID || stored.PendingEmailVerification || !stored.HistoryEnabled {
		t.Fatalf("stored user = pending=%v history=%v, want pending=false history=true",
			stored.PendingEmailVerification, stored.HistoryEnabled)
	}
}
