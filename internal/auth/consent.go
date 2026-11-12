package auth

import (
	"errors"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The documents and sources of a consent row (user_consents, migration 0189).
const (
	ConsentTerms   = "terms"
	ConsentPrivacy = "privacy"
	ConsentAge     = "age"

	ConsentSourceSignup = "signup"
	// ConsentSourceApproval is written by the statement that approves a queued
	// request (ApproveRegistrationRequest), from what the request kept.
	ConsentSourceApproval = "approval"
	// A first sign-in through a provider creates the account.
	ConsentSourceOIDC    = "oidc"
	ConsentSourceATProto = "atproto"
)

// ErrTermsRequired is returned when the instance has terms or a privacy policy
// configured and a signup did not accept them.
var ErrTermsRequired = errors.New("auth: the terms and privacy policy were not accepted")

// SignupTerms is what a new account has to accept on this instance right now:
// the operator's two documents with their version labels, and the minimum age.
type SignupTerms struct {
	TermsURL, TermsVersion     string
	PrivacyURL, PrivacyVersion string
	// MinimumAge is the registration age-attestation threshold; 0 = off.
	MinimumAge int32
}

// Required reports whether there is a document to accept at all.
func (t SignupTerms) Required() bool { return t.TermsURL != "" || t.PrivacyURL != "" }

// SignupAcceptance is what the person creating the account said yes to.
type SignupAcceptance struct {
	// Terms: they accepted the instance's terms and privacy policy.
	Terms bool
	// Age: they attested the instance's minimum age.
	Age bool
}

// WithSignupTermsFunc wires the live signup terms. Without it nothing is
// required and nothing is recorded, which is what an instance with no
// documents configured does anyway.
func WithSignupTermsFunc(f func() SignupTerms) Option {
	return func(s *Service) {
		if f != nil {
			s.signupTermsFn = f
		}
	}
}

// consentRecord is what one signup accepted, in the parallel arrays the
// account-creating statement stores (one element per document).
type consentRecord struct {
	documents, versions, urls []string
	minimumAges               []int32
}

func (r *consentRecord) add(document, version, url string, minimumAge int32) {
	r.documents = append(r.documents, document)
	r.versions = append(r.versions, version)
	r.urls = append(r.urls, url)
	r.minimumAges = append(r.minimumAges, minimumAge)
}

// onCreate puts the record on the statement that creates the account, so the
// two are written together or not at all.
func (r consentRecord) onCreate(p sqlcgen.CreateUserParams, source string) sqlcgen.CreateUserParams {
	p.ConsentSource = source
	p.ConsentDocuments, p.ConsentVersions, p.ConsentUrls, p.ConsentMinimumAges = r.documents, r.versions, r.urls, r.minimumAges
	return p
}

// signupConsent is the one place a signup's acceptance is checked and turned
// into rows. It refuses when the instance has documents configured and they
// were not accepted, and it records the documents as configured NOW — the
// caller says only yes or no, never which version it thinks it saw.
func (s *Service) signupConsent(a SignupAcceptance) (consentRecord, error) {
	var t SignupTerms
	if s.signupTermsFn != nil {
		t = s.signupTermsFn()
	}
	if t.Required() && !a.Terms {
		return consentRecord{}, ErrTermsRequired
	}
	var r consentRecord
	if t.TermsURL != "" {
		r.add(ConsentTerms, t.TermsVersion, t.TermsURL, 0)
	}
	if t.PrivacyURL != "" {
		r.add(ConsentPrivacy, t.PrivacyVersion, t.PrivacyURL, 0)
	}
	if t.MinimumAge > 0 && a.Age {
		r.add(ConsentAge, "", "", t.MinimumAge)
	}
	return r, nil
}

// requestConsent is a consentRecord in the columns a queued registration
// request keeps until an admin decides: the applicant accepted when they
// applied, and the account's rows must say so, with that time.
type requestConsent struct {
	recorded                                           bool
	termsURL, termsVersion, privacyURL, privacyVersion *string
	minimumAge                                         *int32
}

func (r consentRecord) forRequest() requestConsent {
	out := requestConsent{recorded: len(r.documents) > 0}
	for i, document := range r.documents {
		switch document {
		case ConsentTerms:
			out.termsURL, out.termsVersion = &r.urls[i], &r.versions[i]
		case ConsentPrivacy:
			out.privacyURL, out.privacyVersion = &r.urls[i], &r.versions[i]
		case ConsentAge:
			out.minimumAge = &r.minimumAges[i]
		}
	}
	return out
}
