package auth

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func termsService(repo *fakeRepo, terms SignupTerms) *Service {
	return NewService(repo, newTestIssuer(), 3600e9, WithSignupTermsFunc(func() SignupTerms { return terms }))
}

var bothDocuments = SignupTerms{
	TermsURL: "https://example.test/terms", TermsVersion: "2026-11-12",
	PrivacyURL: "https://example.test/privacy", PrivacyVersion: "v3",
	MinimumAge: 16,
}

// With documents configured, no account is created unless they were accepted —
// on either password path — and nothing reaches the repository at all.
func TestSignupWithoutAcceptingTheTermsCreatesNothing(t *testing.T) {
	ctx := context.Background()
	in := RegisterInput{Username: "bob", Email: "bob@example.test", Password: "supersecret"}
	for _, terms := range []SignupTerms{bothDocuments, {TermsURL: "https://example.test/terms"}, {PrivacyURL: "https://example.test/privacy"}} {
		repo := newFakeRepo()
		svc := termsService(repo, terms)
		if _, _, err := svc.Register(ctx, in, "agent"); !errors.Is(err, ErrTermsRequired) {
			t.Errorf("Register with %+v = %v, want ErrTermsRequired", terms, err)
		}
		if _, err := svc.RegisterPendingVerification(ctx, in); !errors.Is(err, ErrTermsRequired) {
			t.Errorf("RegisterPendingVerification with %+v = %v, want ErrTermsRequired", terms, err)
		}
		if len(repo.created) != 0 {
			t.Errorf("an account was created without the acceptance: %+v", repo.created)
		}
	}
}

// What is recorded is what the instance has configured at that moment — each
// document's URL and version label — in the statement that creates the account.
func TestSignupRecordsWhatWasAccepted(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		terms     SignupTerms
		accepted  SignupAcceptance
		documents []string
		versions  []string
		urls      []string
		ages      []int32
	}{
		{"terms, privacy and age", bothDocuments, SignupAcceptance{Terms: true, Age: true},
			[]string{"terms", "privacy", "age"}, []string{"2026-11-12", "v3", ""},
			[]string{"https://example.test/terms", "https://example.test/privacy", ""}, []int32{0, 0, 16}},
		{"age not attested is not recorded", bothDocuments, SignupAcceptance{Terms: true},
			[]string{"terms", "privacy"}, []string{"2026-11-12", "v3"},
			[]string{"https://example.test/terms", "https://example.test/privacy"}, []int32{0, 0}},
		{"only a privacy policy, no label", SignupTerms{PrivacyURL: "https://example.test/privacy"}, SignupAcceptance{Terms: true},
			[]string{"privacy"}, []string{""}, []string{"https://example.test/privacy"}, []int32{0}},
		{"only a minimum age", SignupTerms{MinimumAge: 13}, SignupAcceptance{Age: true},
			[]string{"age"}, []string{""}, []string{""}, []int32{13}},
		{"nothing configured: a stray tick records nothing", SignupTerms{}, SignupAcceptance{Terms: true, Age: true}, nil, nil, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			svc := termsService(repo, tc.terms)
			if _, _, err := svc.Register(ctx, RegisterInput{Username: "bob", Email: "bob@example.test", Password: "supersecret", Accepted: tc.accepted}, "agent"); err != nil {
				t.Fatalf("Register: %v", err)
			}
			if _, err := svc.RegisterPendingVerification(ctx, RegisterInput{Username: "cal", Email: "cal@example.test", Password: "supersecret", Accepted: tc.accepted}); err != nil {
				t.Fatalf("RegisterPendingVerification: %v", err)
			}
			if len(repo.created) != 2 {
				t.Fatalf("accounts created = %d, want 2", len(repo.created))
			}
			for _, got := range repo.created {
				if got.ConsentSource != ConsentSourceSignup {
					t.Errorf("source = %q, want signup", got.ConsentSource)
				}
				if !slices.Equal(got.ConsentDocuments, tc.documents) || !slices.Equal(got.ConsentVersions, tc.versions) ||
					!slices.Equal(got.ConsentUrls, tc.urls) || !slices.Equal(got.ConsentMinimumAges, tc.ages) {
					t.Errorf("recorded %v / %v / %v / %v, want %v / %v / %v / %v",
						got.ConsentDocuments, got.ConsentVersions, got.ConsentUrls, got.ConsentMinimumAges,
						tc.documents, tc.versions, tc.urls, tc.ages)
				}
			}
		})
	}
}

// A service with no terms wired behaves exactly as before: nothing required,
// nothing recorded.
func TestSignupWithoutTermsWiredIsUnchanged(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	register(t, svc, "bob", "bob@example.test")
	if len(repo.created) != 1 || len(repo.created[0].ConsentDocuments) != 0 {
		t.Fatalf("created = %+v, want one account and no consent", repo.created)
	}
}

// A signup held for approval is accepted by the applicant when they apply: the
// request is refused without the acceptance, and otherwise keeps exactly what
// was accepted so the approved account can record it.
func TestQueuedSignupKeepsWhatTheApplicantAccepted(t *testing.T) {
	ctx := context.Background()
	in := RegisterInput{Username: "bob", Email: "bob@example.test", Password: "supersecret"}

	repo := newFakeRepo()
	svc := termsService(repo, bothDocuments)
	if _, err := svc.RequestRegistration(ctx, in, ""); !errors.Is(err, ErrTermsRequired) {
		t.Fatalf("RequestRegistration without the acceptance = %v, want ErrTermsRequired", err)
	}
	if len(repo.requested) != 0 {
		t.Fatalf("a request was filed without the acceptance")
	}

	in.Accepted = SignupAcceptance{Terms: true, Age: true}
	if _, err := svc.RequestRegistration(ctx, in, "hello"); err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	got := repo.requested[0]
	str := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	if !got.ConsentRecorded || str(got.ConsentTermsUrl) != "https://example.test/terms" || str(got.ConsentTermsVersion) != "2026-11-12" ||
		str(got.ConsentPrivacyUrl) != "https://example.test/privacy" || str(got.ConsentPrivacyVersion) != "v3" ||
		got.ConsentMinimumAge == nil || *got.ConsentMinimumAge != 16 {
		t.Errorf("request kept %+v", got)
	}

	// Nothing configured: the request records nothing, as before.
	plain := newFakeRepo()
	if _, err := newTestService(plain).RequestRegistration(ctx, RegisterInput{Username: "cal", Email: "cal@example.test", Password: "supersecret"}, ""); err != nil {
		t.Fatalf("RequestRegistration with nothing configured: %v", err)
	}
	if got := plain.requested[0]; got.ConsentRecorded || got.ConsentTermsUrl != nil || got.ConsentPrivacyUrl != nil || got.ConsentMinimumAge != nil {
		t.Errorf("a request with nothing to accept kept %+v", got)
	}
}

// A first sign-in through a provider is a signup: on an instance with documents
// configured it creates nothing unless the attempt carried the acceptance, and
// records it when it did. An identity that is already linked signs in without
// being asked, whatever the attempt carried.
func TestProviderFirstSignInMustAcceptTheTerms(t *testing.T) {
	ctx := context.Background()
	repo := newOAuthFakeRepo()
	issuer := NewTokenIssuer("oauth-test-secret-oauth-test-secret-0", "vidra", "vidra", 60e9)
	svc := NewService(repo.fakeRepo, issuer, 3600e9, WithSignupTermsFunc(func() SignupTerms { return bothDocuments }))
	oidc := NewOAuthService(repo, svc, []OAuthProvider{{Name: "fake", IssuerURL: "https://idp.invalid", ClientID: "id", ClientSecret: "secret"}})
	claims := oidcClaims{Email: "bob@example.test", EmailVerified: true, PreferredUsername: "bob"}

	if _, err := oidc.resolveIdentity(ctx, "fake", "sub-bob", claims, false, "ua"); !errors.Is(err, ErrTermsRequired) {
		t.Fatalf("first sign-in without the acceptance = %v, want ErrTermsRequired", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("an account was created without the acceptance")
	}
	sess, err := oidc.resolveIdentity(ctx, "fake", "sub-bob", claims, true, "ua")
	if err != nil || sess.Outcome != OAuthCreated {
		t.Fatalf("first sign-in with the acceptance: outcome=%v err=%v", sess.Outcome, err)
	}
	got := repo.created[0]
	if got.ConsentSource != ConsentSourceOIDC || !slices.Equal(got.ConsentDocuments, []string{"terms", "privacy"}) ||
		!slices.Equal(got.ConsentVersions, []string{"2026-11-12", "v3"}) {
		t.Errorf("recorded %q %v %v", got.ConsentSource, got.ConsentDocuments, got.ConsentVersions)
	}
	// The same subject again is a login: never asked, nothing created.
	if sess, err := oidc.resolveIdentity(ctx, "fake", "sub-bob", claims, false, "ua"); err != nil || sess.Outcome != OAuthLogin {
		t.Fatalf("returning sign-in without the acceptance: outcome=%v err=%v; want a login", sess.Outcome, err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("accounts created = %d, want 1", len(repo.created))
	}

	// ATProto: the acceptance rides in the attempt state.
	at := NewATProtoOAuthService(repo, svc, defaultFlow(), WithATProtoEnabled(true), WithATProtoPublicBaseURL("https://vidra.test"))
	_, st, err := at.Begin(ctx, "alice.example", "/home")
	if err != nil {
		t.Fatalf("atproto begin: %v", err)
	}
	if _, err := at.Complete(ctx, st, "code-1", st.Issuer, "ua"); !errors.Is(err, ErrTermsRequired) {
		t.Fatalf("atproto first sign-in without the acceptance = %v, want ErrTermsRequired", err)
	}
	st.TermsAccepted = true
	if sess, err := at.Complete(ctx, st, "code-2", st.Issuer, "ua"); err != nil || sess.Outcome != OAuthCreated {
		t.Fatalf("atproto first sign-in with the acceptance: outcome=%v err=%v", sess.Outcome, err)
	}
	if got := repo.created[len(repo.created)-1]; got.ConsentSource != ConsentSourceATProto || !slices.Equal(got.ConsentDocuments, []string{"terms", "privacy"}) {
		t.Errorf("atproto recorded %q %v", got.ConsentSource, got.ConsentDocuments)
	}
	st.TermsAccepted = false
	if sess, err := at.Complete(ctx, st, "code-3", st.Issuer, "ua"); err != nil || sess.Outcome != OAuthLogin {
		t.Fatalf("atproto returning sign-in: outcome=%v err=%v; want a login", sess.Outcome, err)
	}
}

// Behind the approval queue a provider first sign-in files a request; the
// request keeps the acceptance exactly as a password request does.
func TestProviderRequestKeepsTheAcceptance(t *testing.T) {
	ctx := context.Background()
	repo := newOAuthFakeRepo()
	issuer := NewTokenIssuer("oauth-test-secret-oauth-test-secret-0", "vidra", "vidra", 60e9)
	svc := NewService(repo.fakeRepo, issuer, 3600e9,
		WithSignupTermsFunc(func() SignupTerms { return bothDocuments }),
		WithRegistrationPolicyFunc(func() (bool, bool) { return true, true }))
	oidc := NewOAuthService(repo, svc, []OAuthProvider{{Name: "fake", IssuerURL: "https://idp.invalid", ClientID: "id", ClientSecret: "secret"}})
	claims := oidcClaims{Email: "bob@example.test", EmailVerified: true, PreferredUsername: "bob"}

	if _, err := oidc.resolveIdentity(ctx, "fake", "sub-bob", claims, false, "ua"); !errors.Is(err, ErrTermsRequired) {
		t.Fatalf("queued provider sign-in without the acceptance = %v, want ErrTermsRequired", err)
	}
	if _, err := oidc.resolveIdentity(ctx, "fake", "sub-bob", claims, true, "ua"); !errors.Is(err, ErrRegistrationPending) {
		t.Fatalf("queued provider sign-in with the acceptance = %v, want ErrRegistrationPending", err)
	}
	if len(repo.providerRequested) != 1 {
		t.Fatalf("provider requests filed = %d, want 1", len(repo.providerRequested))
	}
	got := repo.providerRequested[0]
	if !got.ConsentRecorded || got.ConsentTermsUrl == nil || *got.ConsentTermsUrl != "https://example.test/terms" ||
		got.ConsentPrivacyVersion == nil || *got.ConsentPrivacyVersion != "v3" || got.ConsentMinimumAge != nil {
		t.Errorf("the provider request kept %+v", got)
	}
}
