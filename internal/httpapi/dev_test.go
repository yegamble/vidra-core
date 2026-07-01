package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vidra/vidra-core/internal/auth"
)

// getDevToken issues the dev endpoint request and returns the status + decoded
// {"token":...} body (token empty when the body is not a token envelope).
func getDevToken(t *testing.T, srv *Server, query string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/dev/email-token"+query, nil))
	var body struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Token
}

func TestDevEmailTokenReturnsCapturedToken(t *testing.T) {
	ctx := context.Background()
	cm := auth.NewCaptureMailer()
	_ = cm.SendPasswordReset(ctx, "user@example.test", "reset-raw-tok")
	_ = cm.SendEmailVerification(ctx, "user@example.test", "verify-raw-tok")
	srv := New(testConfig(), nil, nil, WithDevMailCapture(cm))

	// Explicit reset kind.
	if code, tok := getDevToken(t, srv, "?email=user@example.test&kind=reset"); code != http.StatusOK || tok != "reset-raw-tok" {
		t.Fatalf("reset: got (%d, %q); want (200, reset-raw-tok)", code, tok)
	}
	// Default kind is reset.
	if code, tok := getDevToken(t, srv, "?email=user@example.test"); code != http.StatusOK || tok != "reset-raw-tok" {
		t.Fatalf("default kind: got (%d, %q); want (200, reset-raw-tok)", code, tok)
	}
	// Verification kind.
	if code, tok := getDevToken(t, srv, "?email=user@example.test&kind=verification"); code != http.StatusOK || tok != "verify-raw-tok" {
		t.Fatalf("verification: got (%d, %q); want (200, verify-raw-tok)", code, tok)
	}
}

func TestDevEmailTokenValidation(t *testing.T) {
	cm := auth.NewCaptureMailer()
	_ = cm.SendPasswordReset(context.Background(), "known@example.test", "tok")
	srv := New(testConfig(), nil, nil, WithDevMailCapture(cm))

	cases := []struct {
		name, query string
		want        int
	}{
		{"missing email", "?kind=reset", http.StatusUnprocessableEntity},
		{"bad kind", "?email=known@example.test&kind=bogus", http.StatusUnprocessableEntity},
		{"no token for email", "?email=nobody@example.test&kind=reset", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := getDevToken(t, srv, tc.query); code != tc.want {
				t.Fatalf("got %d; want %d", code, tc.want)
			}
		})
	}
}

// The dev route must NOT exist unless the capture mailer is wired — production
// (DEV_MAIL_CAPTURE_ENABLED off) never carries it.
func TestDevEmailTokenAbsentWithoutCapture(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	if code, _ := getDevToken(t, srv, "?email=user@example.test&kind=reset"); code != http.StatusNotFound {
		t.Fatalf("route should be unregistered without capture; got %d, want 404", code)
	}
}
