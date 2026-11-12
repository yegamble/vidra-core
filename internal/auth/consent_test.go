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
