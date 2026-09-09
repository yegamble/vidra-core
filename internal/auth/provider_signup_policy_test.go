package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The provider signup paths (OIDC and ATProto) create accounts. Until this
// slice they consulted only the owner-claim gate, so an instance whose operator
// had CLOSED registration — or put it behind approval — still minted accounts
// through any configured provider, and handed each one a session. The password
// path has honoured both policies since W7; these tests hold the provider paths
// to the same rule.

// providerPolicyServices wires an OAuth and an ATProto service over one store,
// with a live registration policy the caller can flip mid-test.
func providerPolicyServices(t *testing.T, policy func() (bool, bool)) (*oauthFakeRepo, *Service, *OAuthService, *ATProtoOAuthService) {
	t.Helper()
	repo := newOAuthFakeRepo()
	svc := NewService(repo, newTestIssuer(), time.Hour, WithRegistrationPolicyFunc(policy))
	osvc := NewOAuthService(repo, svc, []OAuthProvider{
		{Name: "fake", IssuerURL: "https://idp.invalid", ClientID: "id", ClientSecret: "secret"},
	})
	asvc := NewATProtoOAuthService(repo, svc, nil)
	return repo, svc, osvc, asvc
}

// TestOIDCSignupRefusedWhenRegistrationClosed proves a first OIDC sign-in for an
// unknown subject creates nothing while registration is closed, and that an
// ALREADY-LINKED identity still logs in — closing registration must not lock
// out the accounts that already exist.
func TestOIDCSignupRefusedWhenRegistrationClosed(t *testing.T) {
	open := false
	repo, _, osvc, _ := providerPolicyServices(t, func() (bool, bool) { return open, false })
	ctx := context.Background()
	as := OAuthAssertion{Provider: "fake", Subject: "sub-closed", Email: "closed@example.test", EmailVerified: true}

	if _, err := osvc.ResolveAssertion(ctx, as, "test-agent"); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed registration: err = %v, want ErrRegistrationClosed", err)
	}
	if len(repo.byEmail) != 0 {
		t.Fatalf("closed registration created accounts: users = %d, want 0", len(repo.byEmail))
	}

	// Re-open, create the account, close again: the existing identity logs in.
	open = true
	if _, err := osvc.ResolveAssertion(ctx, as, "test-agent"); err != nil {
		t.Fatalf("open registration: %v", err)
	}
	open = false
	sess, err := osvc.ResolveAssertion(ctx, as, "test-agent")
	if err != nil {
		t.Fatalf("existing identity locked out by closed registration: %v", err)
	}
	if sess.Outcome != OAuthLogin {
		t.Fatalf("existing identity outcome = %v, want OAuthLogin", sess.Outcome)
	}
}

// TestOIDCSignupQueuesWhenApprovalRequired proves the first sign-in files a
// pending registration request instead of an account, refuses the session, is
// idempotent across repeats, and that approval creates the account WITH the
// provider identity attached — so the next sign-in is an ordinary login.
func TestOIDCSignupQueuesWhenApprovalRequired(t *testing.T) {
	repo, svc, osvc, _ := providerPolicyServices(t, func() (bool, bool) { return true, true })
	ctx := context.Background()
	admin, _ := register(t, svc, "ada", "ada@example.test")
	as := OAuthAssertion{Provider: "fake", Subject: "sub-queue", Email: "queue@example.test",
		EmailVerified: true, PreferredUsername: "queued"}

	if _, err := osvc.ResolveAssertion(ctx, as, "test-agent"); !errors.Is(err, ErrRegistrationPending) {
		t.Fatalf("approval required: err = %v, want ErrRegistrationPending", err)
	}
	if _, ok := repo.byEmail["queue@example.test"]; ok {
		t.Fatal("approval required created an account for the provider identity")
	}
	reqs, _, err := svc.ListRegistrationRequests(ctx, "pending", 10, 0)
	if err != nil {
		t.Fatalf("ListRegistrationRequests: %v", err)
	}
	if len(reqs) != 1 || reqs[0].Email != "queue@example.test" {
		t.Fatalf("queue = %+v, want one pending request for queue@example.test", reqs)
	}

	// A second attempt while pending must not file a duplicate.
	if _, err := osvc.ResolveAssertion(ctx, as, "test-agent"); !errors.Is(err, ErrRegistrationPending) {
		t.Fatalf("repeat attempt: err = %v, want ErrRegistrationPending", err)
	}
	if _, total, _ := svc.ListRegistrationRequests(ctx, "pending", 10, 0); total != 1 {
		t.Fatalf("repeat attempt filed a duplicate: pending total = %d, want 1", total)
	}

	// Approval creates the account AND the identity, so the next sign-in logs in.
	if _, err := svc.ApproveRegistration(ctx, admin.ID, reqs[0].ID); err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	sess, err := osvc.ResolveAssertion(ctx, as, "test-agent")
	if err != nil {
		t.Fatalf("post-approval sign-in: %v", err)
	}
	if sess.Outcome != OAuthLogin {
		t.Fatalf("post-approval outcome = %v, want OAuthLogin (the identity was attached at approval)", sess.Outcome)
	}
	if sess.User.Email != "queue@example.test" {
		t.Fatalf("post-approval email = %q, want queue@example.test", sess.User.Email)
	}
}

// TestOIDCSignupRejectedRequestStaysRefused proves a rejected applicant is not
// silently admitted by simply signing in again.
func TestOIDCSignupRejectedRequestStaysRefused(t *testing.T) {
	repo, svc, osvc, _ := providerPolicyServices(t, func() (bool, bool) { return true, true })
	ctx := context.Background()
	admin, _ := register(t, svc, "ada", "ada@example.test")
	as := OAuthAssertion{Provider: "fake", Subject: "sub-reject", Email: "reject@example.test", EmailVerified: true}

	if _, err := osvc.ResolveAssertion(ctx, as, "test-agent"); !errors.Is(err, ErrRegistrationPending) {
		t.Fatalf("first attempt: err = %v, want ErrRegistrationPending", err)
	}
	reqs, _, _ := svc.ListRegistrationRequests(ctx, "pending", 10, 0)
	if err := svc.RejectRegistration(ctx, admin.ID, reqs[0].ID, "no"); err != nil {
		t.Fatalf("RejectRegistration: %v", err)
	}
	if _, err := osvc.ResolveAssertion(ctx, as, "test-agent"); !errors.Is(err, ErrRegistrationPending) {
		t.Fatalf("after rejection: err = %v, want ErrRegistrationPending (a fresh request, not an account)", err)
	}
	if _, ok := repo.byEmail["reject@example.test"]; ok {
		t.Fatal("a rejected applicant got an account by signing in again")
	}
}

// TestATProtoSignupHonoursRegistrationPolicy proves the Bluesky path refuses and
// queues by the same rule — the bypass must not simply move one provider over.
func TestATProtoSignupHonoursRegistrationPolicy(t *testing.T) {
	enabled, approval := false, false
	repo, _, _, asvc := providerPolicyServices(t, func() (bool, bool) { return enabled, approval })
	ctx := context.Background()
	st := ATProtoState{Handle: "alice.example.test"}

	if _, err := asvc.resolveATProtoIdentity(ctx, st, "did:plc:alicealicealice", "ua"); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed: err = %v, want ErrRegistrationClosed", err)
	}
	enabled, approval = true, true
	if _, err := asvc.resolveATProtoIdentity(ctx, st, "did:plc:alicealicealice", "ua"); !errors.Is(err, ErrRegistrationPending) {
		t.Fatalf("approval required: err = %v, want ErrRegistrationPending", err)
	}
	if len(repo.byEmail) != 0 {
		t.Fatalf("atproto signup created an account under policy: users = %d, want 0", len(repo.byEmail))
	}
}

// TestProviderSignupUnpolicedWhenNoPolicyWired proves the option is optional:
// with nothing wired the paths behave exactly as they did before this slice.
func TestProviderSignupUnpolicedWhenNoPolicyWired(t *testing.T) {
	repo := newOAuthFakeRepo()
	svc := NewService(repo.fakeRepo, newTestIssuer(), time.Hour)
	osvc := NewOAuthService(repo, svc, []OAuthProvider{{Name: "fake", IssuerURL: "https://idp.invalid", ClientID: "id", ClientSecret: "secret"}})
	sess, err := osvc.ResolveAssertion(context.Background(), OAuthAssertion{
		Provider: "fake", Subject: "sub-none", Email: "none@example.test", EmailVerified: true,
	}, "ua")
	if err != nil {
		t.Fatalf("no policy wired: %v", err)
	}
	if sess.Outcome != OAuthCreated {
		t.Fatalf("no policy wired: outcome = %v, want OAuthCreated", sess.Outcome)
	}
}
