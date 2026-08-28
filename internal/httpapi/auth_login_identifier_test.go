package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// POST /api/v1/auth/login takes EITHER `email` (legacy) OR `identifier`
// (email-or-username), never both. Registration additionally refuses '@' and
// whitespace in NEW usernames so the two namespaces stop overlapping going
// forward.

func TestLoginEndpointAcceptsIdentifierField(t *testing.T) {
	srv := authServer(t)
	_ = postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	byUsername := postTo(srv, "/api/v1/auth/login", `{"identifier":"ada","password":"supersecret"}`)
	if byUsername.Code != http.StatusOK {
		t.Fatalf("username login = %d, want 200; body=%s", byUsername.Code, byUsername.Body.String())
	}
	byEmail := postTo(srv, "/api/v1/auth/login", `{"identifier":"ADA@example.test","password":"supersecret"}`)
	if byEmail.Code != http.StatusOK {
		t.Fatalf("identifier-as-email login = %d, want 200; body=%s", byEmail.Code, byEmail.Body.String())
	}
	wrong := postTo(srv, "/api/v1/auth/login", `{"identifier":"ada","password":"nope"}`)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", wrong.Code)
	}
}

// TestLoginEndpointEmailFieldRemainsSupported is the back-compat guard: an
// older client (or the frontend before it deploys) keeps signing in with
// `email` alone.
func TestLoginEndpointEmailFieldRemainsSupported(t *testing.T) {
	srv := authServer(t)
	_ = postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if rec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`); rec.Code != http.StatusOK {
		t.Fatalf("legacy email login = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// A malformed email in the legacy field still 422s on the email field.
	rec := postTo(srv, "/api/v1/auth/login", `{"email":"not-an-email","password":"supersecret"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed email = %d, want 422", rec.Code)
	}
	if f := fieldErrorFor(t, rec.Body.Bytes(), "email"); f == "" {
		t.Errorf("expected an `email` field error; body=%s", rec.Body.String())
	}
}

// TestLoginEndpointRejectsBothIdentifierAndEmail: two identifiers in one body
// is ambiguous, and silently preferring one would make the client's intent
// unknowable. 422 rather than a guess.
func TestLoginEndpointRejectsBothIdentifierAndEmail(t *testing.T) {
	srv := authServer(t)
	_ = postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","identifier":"ada","password":"supersecret"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("both fields = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if f := fieldErrorFor(t, rec.Body.Bytes(), "identifier"); f == "" {
		t.Errorf("expected an `identifier` field error; body=%s", rec.Body.String())
	}
}

func TestLoginEndpointIdentifierValidation(t *testing.T) {
	srv := authServer(t)
	for _, tc := range []struct{ name, body string }{
		{"empty body", `{}`},
		{"blank identifier", `{"identifier":"   ","password":"supersecret"}`},
		{"too short", `{"identifier":"ab","password":"supersecret"}`},
		{"too long", `{"identifier":"` + strings.Repeat("a", 255) + `","password":"supersecret"}`},
		{"no password", `{"identifier":"ada"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := postTo(srv, "/api/v1/auth/login", tc.body); rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	// An identifier with no shape rule beyond length: '@'-less, spaces, unicode
	// — all reach the lookup and come back 401, never 422. Legacy usernames
	// predate the '@'/whitespace ban and must stay signable-in.
	for _, id := range []string{"weird name", "ünïcodé", "a@b@c", "not-an-email"} {
		if rec := postTo(srv, "/api/v1/auth/login", `{"identifier":`+quote(id)+`,"password":"supersecret"}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("identifier %q = %d, want 401 (no shape rule on identifier); body=%s", id, rec.Code, rec.Body.String())
		}
	}
}

// TestRegisterRejectsAtSignAndSpacesInUsername closes the overlap going
// forward. Sign-in resolves email before username, so a NEW username holding
// '@' could only ever be an unreachable sign-in identifier — or a deliberate
// lookalike of somebody else's address. Whitespace is banned alongside it
// because a padded username is indistinguishable from a typo at the prompt.
func TestRegisterRejectsAtSignAndSpacesInUsername(t *testing.T) {
	for _, tc := range []struct{ name, username string }{
		{"at sign", "ada@example.test"},
		{"at sign only", "a@b"},
		{"inner space", "ada smith"},
		{"tab", "ada\tsmith"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := authServer(t)
			body, err := json.Marshal(map[string]string{
				"username": tc.username, "email": "new@example.test", "password": "supersecret",
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			rec := postTo(srv, "/api/v1/auth/register", string(body))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("register %q = %d, want 422; body=%s", tc.username, rec.Code, rec.Body.String())
			}
			if f := fieldErrorFor(t, rec.Body.Bytes(), "username"); f == "" {
				t.Errorf("expected a `username` field error; body=%s", rec.Body.String())
			}
		})
	}
}

func TestClaimOwnerRejectsAtSignAndSpacesInUsername(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/setup/claim-owner",
		`{"token":"whatever","username":"root@example.test","email":"root@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("claim-owner with '@' username = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if f := fieldErrorFor(t, rec.Body.Bytes(), "username"); f == "" {
		t.Errorf("expected a `username` field error; body=%s", rec.Body.String())
	}
}

// fieldErrorFor returns the message of the named 422 field error, or "".
func fieldErrorFor(t *testing.T, body []byte, field string) string {
	t.Helper()
	var er ErrorResponse
	if err := json.Unmarshal(body, &er); err != nil {
		t.Fatalf("unmarshal error response: %v (body=%s)", err, body)
	}
	for _, f := range er.Error.Fields {
		if f.Field == field {
			if f.Message == "" {
				return "(empty message)"
			}
			return f.Message
		}
	}
	return ""
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
