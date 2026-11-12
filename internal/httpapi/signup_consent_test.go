package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

// TestSignupMustAcceptTheConfiguredDocuments: while the instance has terms or a
// privacy policy configured, POST /auth/register without terms_accepted is a
// 422 on that field and creates nothing; with it, the account is created and
// what was accepted — each URL and the operator's version label — is stored
// with it. With nothing configured the field is ignored.
func TestSignupMustAcceptTheConfiguredDocuments(t *testing.T) {
	srv := videoServer(t)
	repo := authRepoBySrv[srv]
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if got := repo.created[len(repo.created)-1]; len(got.ConsentDocuments) != 0 {
		t.Fatalf("with no documents configured the signup recorded %v", got.ConsentDocuments)
	}
	patchSettings(t, srv, adminTok, `{"terms_url":"https://example.test/terms","terms_version":"2026-11-12","privacy_url":"https://example.test/privacy","registration_minimum_age":16}`)

	inst := publicInstance(t, srv)
	if inst.TermsURL == "" || inst.TermsVersion != "2026-11-12" || inst.PrivacyURL == "" || inst.PrivacyVersion != "" {
		t.Fatalf("instance documents = %q %q / %q %q", inst.TermsURL, inst.TermsVersion, inst.PrivacyURL, inst.PrivacyVersion)
	}

	accounts := len(repo.created)
	for name, body := range map[string]string{
		"absent": `{"username":"bob","email":"bob@example.test","password":"supersecret","age_attestation":true}`,
		"false":  `{"username":"bob","email":"bob@example.test","password":"supersecret","age_attestation":true,"terms_accepted":false}`,
	} {
		rec := postTo(srv, "/api/v1/auth/register", body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("terms_accepted %s: register = %d, want 422; body=%s", name, rec.Code, rec.Body.String())
		}
		var env ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if len(env.Error.Fields) != 1 || env.Error.Fields[0].Field != "terms_accepted" {
			t.Fatalf("terms_accepted %s: fields = %+v, want one terms_accepted error", name, env.Error.Fields)
		}
	}
	if len(repo.created) != accounts {
		t.Fatalf("a refused signup created an account")
	}

	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret","age_attestation":true,"terms_accepted":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register with the acceptance = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	got := repo.created[len(repo.created)-1]
	if got.ConsentSource != "signup" ||
		!slices.Equal(got.ConsentDocuments, []string{"terms", "privacy", "age"}) ||
		!slices.Equal(got.ConsentVersions, []string{"2026-11-12", "", ""}) ||
		!slices.Equal(got.ConsentUrls, []string{"https://example.test/terms", "https://example.test/privacy", ""}) ||
		!slices.Equal(got.ConsentMinimumAges, []int32{0, 0, 16}) {
		t.Fatalf("recorded consent = %q %v %v %v %v", got.ConsentSource, got.ConsentDocuments, got.ConsentVersions, got.ConsentUrls, got.ConsentMinimumAges)
	}
}
