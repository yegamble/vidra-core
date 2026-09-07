package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func regInput(name, email string) RegisterInput {
	return RegisterInput{Username: name, Email: email, Password: "supersecret"}
}

func TestRequestRegistrationCreatesPending(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(newFakeRepo())

	req, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), "please let me in")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if req.Status != RegistrationPending || req.Username != "bob" || req.Note != "please let me in" {
		t.Errorf("request = %+v, want pending bob with note", req)
	}

	// A second pending request for the same email/username → conflict.
	if _, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), ""); err != ErrConflict {
		t.Errorf("duplicate pending err = %v, want ErrConflict", err)
	}

	// It shows in the pending queue.
	list, _, err := svc.ListRegistrationRequests(ctx, "pending", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != req.ID {
		t.Fatalf("pending queue = %+v, want the one request", list)
	}
}

func TestRequestRegistrationConflictsWithExistingUser(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := newTestService(repo)

	// Register an account the normal way, then a request for the same email fails.
	if _, _, err := svc.Register(ctx, regInput("ada", "ada@example.test"), "ua"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := svc.RequestRegistration(ctx, regInput("ada2", "ada@example.test"), ""); err != ErrConflict {
		t.Errorf("request for existing email err = %v, want ErrConflict", err)
	}
}

func TestApproveRegistrationCreatesAccount(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(newFakeRepo())
	admin := uuid.New()

	req, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}

	user, err := svc.ApproveRegistration(ctx, admin, req.ID)
	if err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	if user.Email != "bob@example.test" || user.Role != "user" {
		t.Errorf("approved user = %+v, want bob/user", user)
	}
	// The approved account can now log in.
	if _, err := svc.Login(ctx, LoginInput{Email: "bob@example.test", Password: "supersecret"}, "ua"); err != nil {
		t.Errorf("login after approval: %v", err)
	}
	// Re-approving the same (now resolved) request → not found.
	if _, err := svc.ApproveRegistration(ctx, admin, req.ID); err != ErrRegistrationRequestNotFound {
		t.Errorf("re-approve err = %v, want ErrRegistrationRequestNotFound", err)
	}
	// It is no longer pending.
	if list, _, _ := svc.ListRegistrationRequests(ctx, "pending", 20, 0); len(list) != 0 {
		t.Errorf("pending after approve = %d, want 0", len(list))
	}
}

func TestApproveUnknownRegistration(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if _, err := svc.ApproveRegistration(context.Background(), uuid.New(), uuid.New()); err != ErrRegistrationRequestNotFound {
		t.Errorf("approve unknown err = %v, want ErrRegistrationRequestNotFound", err)
	}
}

func TestRejectRegistration(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(newFakeRepo())
	admin := uuid.New()

	req, err := svc.RequestRegistration(ctx, regInput("carol", "carol@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if err := svc.RejectRegistration(ctx, admin, req.ID, "not now"); err != nil {
		t.Fatalf("RejectRegistration: %v", err)
	}
	// A rejected request cannot be approved.
	if _, err := svc.ApproveRegistration(ctx, admin, req.ID); err != ErrRegistrationRequestNotFound {
		t.Errorf("approve after reject err = %v, want ErrRegistrationRequestNotFound", err)
	}
	// Rejecting again (not pending) → not found.
	if err := svc.RejectRegistration(ctx, admin, req.ID, ""); err != ErrRegistrationRequestNotFound {
		t.Errorf("re-reject err = %v, want ErrRegistrationRequestNotFound", err)
	}
	// No account was created, so login fails.
	if _, err := svc.Login(ctx, LoginInput{Email: "carol@example.test", Password: "supersecret"}, "ua"); err == nil {
		t.Error("login should fail for a rejected registration")
	}
}

// failingMailer wraps a CaptureMailer and fails only the two signup-decision
// notices, so a test can prove the DECISION still succeeds when the sink is
// down — best-effort means the mail may fail, never the review.
type failingMailer struct {
	*CaptureMailer
	err error
}

func (m failingMailer) SendRegistrationApproved(context.Context, string, string, string, bool) error {
	return m.err
}

func (m failingMailer) SendRegistrationRejected(context.Context, string, string, string) error {
	return m.err
}

func (m failingMailer) SendOwnershipTransferred(context.Context, string, string, string, string, bool) error {
	return m.err
}

// TestApproveRegistrationNotifiesTheApplicant closes A16 slice 1 finding (2):
// "approval and rejection notify the applicant of nothing — they discover the
// outcome by trying to sign in".
func TestApproveRegistrationNotifiesTheApplicant(t *testing.T) {
	ctx := context.Background()
	mailer := NewCaptureMailer()
	svc := NewService(newFakeRepo(), newTestIssuer(), time.Hour,
		WithMailer(mailer), WithPublicBaseURL("https://videos.example/"))
	admin := uuid.New()

	req, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if got := mailer.RegistrationDecisions(); len(got) != 0 {
		t.Fatalf("applying already notified: %+v", got)
	}
	if _, err := svc.ApproveRegistration(ctx, admin, req.ID); err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	got := mailer.RegistrationDecisions()
	if len(got) != 1 {
		t.Fatalf("decision notices = %+v, want exactly one", got)
	}
	if got[0].Decision != "approved" || got[0].Email != "bob@example.test" || got[0].Username != "bob" {
		t.Errorf("approval notice = %+v, want approved bob@example.test", got[0])
	}
	// The trailing slash on the configured base must not survive into the link.
	if got[0].SignInURL != "https://videos.example/login" {
		t.Errorf("sign-in URL = %q, want https://videos.example/login", got[0].SignInURL)
	}
	if got[0].VerifyRequired {
		t.Error("verify_required set while the verification gate is off")
	}
	// Approving twice is 404 and must not mail a second time.
	if _, err := svc.ApproveRegistration(ctx, admin, req.ID); err != ErrRegistrationRequestNotFound {
		t.Fatalf("second approve = %v, want ErrRegistrationRequestNotFound", err)
	}
	if got := mailer.RegistrationDecisions(); len(got) != 1 {
		t.Errorf("notices after a repeat approve = %+v, want still one", got)
	}
}

// TestApproveRegistrationSaysWhenVerificationStillHolds: promising "sign in
// here" while the account is held for email verification would be a lie the
// applicant discovers at the login form.
func TestApproveRegistrationSaysWhenVerificationStillHolds(t *testing.T) {
	ctx := context.Background()
	mailer := NewCaptureMailer()
	svc := NewService(newFakeRepo(), newTestIssuer(), time.Hour,
		WithMailer(mailer),
		WithEmailVerificationGateFunc(func() bool { return true }))
	req, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if _, err := svc.ApproveRegistration(ctx, uuid.New(), req.ID); err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	got := mailer.RegistrationDecisions()
	if len(got) != 1 || !got[0].VerifyRequired {
		t.Fatalf("approval notice = %+v, want verify_required", got)
	}
	// With no public base URL configured the message carries no link at all.
	if got[0].SignInURL != "" {
		t.Errorf("sign-in URL = %q, want empty with no public base URL", got[0].SignInURL)
	}
}

// TestRejectRegistrationNotifiesTheApplicantWithTheNote proves the reviewer's
// note reaches the one person it was written for. The note is prose, which is
// why it travels by mail: the audit envelope's metadata allowlist would reject
// it outright.
func TestRejectRegistrationNotifiesTheApplicantWithTheNote(t *testing.T) {
	ctx := context.Background()
	mailer := NewCaptureMailer()
	svc := NewService(newFakeRepo(), newTestIssuer(), time.Hour, WithMailer(mailer))
	admin := uuid.New()

	req, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if err := svc.RejectRegistration(ctx, admin, req.ID, "  we only accept applicants from the co-op  "); err != nil {
		t.Fatalf("RejectRegistration: %v", err)
	}
	got := mailer.RegistrationDecisions()
	if len(got) != 1 {
		t.Fatalf("decision notices = %+v, want exactly one", got)
	}
	if got[0].Decision != "rejected" || got[0].Email != "bob@example.test" || got[0].Username != "bob" {
		t.Errorf("rejection notice = %+v, want rejected bob@example.test", got[0])
	}
	if got[0].Note != "we only accept applicants from the co-op" {
		t.Errorf("note = %q, want the trimmed reviewer note", got[0].Note)
	}

	// Rejecting twice is 404 and mails nothing more.
	if err := svc.RejectRegistration(ctx, admin, req.ID, "again"); err != ErrRegistrationRequestNotFound {
		t.Fatalf("second reject = %v, want ErrRegistrationRequestNotFound", err)
	}
	if got := mailer.RegistrationDecisions(); len(got) != 1 {
		t.Errorf("notices after a repeat reject = %+v, want still one", got)
	}
}

// TestRejectRegistrationWithoutANoteSendsOne: a rejection with no note still
// tells the applicant the outcome — silence is the defect being fixed.
func TestRejectRegistrationWithoutANoteSendsOne(t *testing.T) {
	ctx := context.Background()
	mailer := NewCaptureMailer()
	svc := NewService(newFakeRepo(), newTestIssuer(), time.Hour, WithMailer(mailer))
	req, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if err := svc.RejectRegistration(ctx, uuid.New(), req.ID, ""); err != nil {
		t.Fatalf("RejectRegistration: %v", err)
	}
	got := mailer.RegistrationDecisions()
	if len(got) != 1 || got[0].Note != "" {
		t.Fatalf("notice = %+v, want one rejection with an empty note", got)
	}
}

// TestRegistrationDecisionsSurviveADeadMailer: the mail is best-effort, the
// decision is not. A sink that is down must not strand a request in the queue.
func TestRegistrationDecisionsSurviveADeadMailer(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	mailer := failingMailer{CaptureMailer: NewCaptureMailer(), err: errors.New("relay refused")}
	svc := NewService(repo, newTestIssuer(), time.Hour, WithMailer(mailer))
	admin := uuid.New()

	approved, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if _, err := svc.ApproveRegistration(ctx, admin, approved.ID); err != nil {
		t.Fatalf("approve with a dead mailer = %v, want the account created anyway", err)
	}
	rejected, err := svc.RequestRegistration(ctx, regInput("cleo", "cleo@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if err := svc.RejectRegistration(ctx, admin, rejected.ID, "no"); err != nil {
		t.Fatalf("reject with a dead mailer = %v, want the rejection recorded anyway", err)
	}
	list, _, err := svc.ListRegistrationRequests(ctx, "pending", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("pending queue = %+v, want both decisions recorded despite the mail failures", list)
	}
}
