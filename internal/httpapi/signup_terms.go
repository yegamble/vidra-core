package httpapi

import (
	"fmt"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/instancesettings"
)

// SignupTerms reads what a new account must accept from the instance settings:
// the two documents with the operator's version labels, and the minimum age.
// cmd/api hands this same function to the auth service, so the 422 the signup
// route answers and the service's own refusal can never disagree about what is
// configured.
func SignupTerms(settings *instancesettings.Service) auth.SignupTerms {
	if settings == nil {
		return auth.SignupTerms{}
	}
	return auth.SignupTerms{
		TermsURL:       settings.String(instancesettings.KeyTermsURL),
		TermsVersion:   settings.String(instancesettings.KeyTermsVersion),
		PrivacyURL:     settings.String(instancesettings.KeyPrivacyURL),
		PrivacyVersion: settings.String(instancesettings.KeyPrivacyVersion),
		MinimumAge:     int32(settings.Int(instancesettings.KeyRegistrationMinimumAge)),
	}
}

// termsRequiredError is the 422 a signup gets when the instance has terms or a
// privacy policy configured and the request did not accept them.
func termsRequiredError() error {
	return &ValidationError{Fields: []FieldError{{
		Field:   "terms_accepted",
		Message: "you must accept the terms of service and the privacy policy to create an account",
	}}}
}

// ageRequiredError is the 422 a signup gets when the instance has a minimum age
// set and the request did not attest it.
func ageRequiredError(minAge int64) error {
	return &ValidationError{Fields: []FieldError{{
		Field:   "age_attestation",
		Message: fmt.Sprintf("you must confirm you are at least %d years old", minAge),
	}}}
}
