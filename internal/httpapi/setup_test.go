package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/ratelimit"
)

// claimServer is an auth-only harness whose audit stream is captured in buf
// (nil = discard), for the first-run owner-claim endpoint tests (0104).
func claimServer(t *testing.T, buf *bytes.Buffer) *Server {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	opts := []Option{WithAuthService(svc, 15*time.Minute)}
	if buf != nil {
		opts = append(opts, WithLogger(slog.New(slog.NewJSONHandler(buf, nil))))
	}
	return New(testConfig(), nil, nil, opts...)
}

// mintSetupToken boots the claim flow the way cmd/api does and returns the raw
// token the operator would read off the console.
func mintSetupToken(t *testing.T, srv *Server) string {
	t.Helper()
	raw, minted, _, err := srv.authsvc.EnsureOwnerClaimToken(context.Background())
	if err != nil || !minted {
		t.Fatalf("EnsureOwnerClaimToken: minted=%v err=%v", minted, err)
	}
	return raw
}

func TestClaimOwnerEndpointCreatesAdmin(t *testing.T) {
	var buf bytes.Buffer
	srv := claimServer(t, &buf)
	raw := mintSetupToken(t, srv)

	// While the claim is pending, registration answers the stable refusal code.
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"bot","email":"bot@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("register while pending = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "owner_claim_required" {
		t.Errorf("register code = %q, want owner_claim_required", er.Error.Code)
	}

	// Redeeming the token creates THE admin with a session.
	rec = postTo(srv, "/api/v1/setup/claim-owner",
		`{"token":"`+raw+`","username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("claim-owner = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var body authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.User.Role != "admin" || body.Token == "" || body.RefreshToken == "" {
		t.Errorf("claim response = role %q token/refresh present=%v/%v, want admin with a full session",
			body.User.Role, body.Token != "", body.RefreshToken != "")
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("password_hash")) {
		t.Error("response leaked password_hash")
	}
	if ev := findAudit(auditEvents(t, &buf), observability.ActionOwnerClaim, observability.ResultSuccess); ev == nil {
		t.Error("expected an auth.owner_claim success audit event")
	}

	// The claim opens registration — as a plain user.
	rec = postTo(srv, "/api/v1/auth/register", `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register after claim = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var second authResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &second)
	if second.User.Role != "user" {
		t.Errorf("post-claim registrant role = %q, want user", second.User.Role)
	}
}

func TestClaimOwnerEndpointRejectsWrongAndReusedTokens(t *testing.T) {
	var buf bytes.Buffer
	srv := claimServer(t, &buf)
	raw := mintSetupToken(t, srv)

	// A wrong guess is a non-probing 403 and burns nothing.
	rec := postTo(srv, "/api/v1/setup/claim-owner",
		`{"token":"not-the-token","username":"eve","email":"eve@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "owner_claim_invalid" {
		t.Errorf("wrong-token code = %q, want owner_claim_invalid", er.Error.Code)
	}
	if ev := findAudit(auditEvents(t, &buf), observability.ActionOwnerClaim, observability.ResultFailure); ev == nil {
		t.Error("expected an auth.owner_claim failure audit event")
	}
	if bytes.Contains(buf.Bytes(), []byte(raw)) {
		t.Error("the real setup token must never appear in logs or audit events")
	}

	// The real token still claims; a second redemption is refused.
	if rec := postTo(srv, "/api/v1/setup/claim-owner",
		`{"token":"`+raw+`","username":"ada","email":"ada@example.test","password":"supersecret"}`); rec.Code != http.StatusCreated {
		t.Fatalf("claim = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	rec = postTo(srv, "/api/v1/setup/claim-owner",
		`{"token":"`+raw+`","username":"eve","email":"eve@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reused token = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "owner_claim_invalid" {
		t.Errorf("reused-token code = %q, want owner_claim_invalid", er.Error.Code)
	}
}

func TestClaimOwnerEndpointWithoutPendingClaim(t *testing.T) {
	// No token was ever minted (an instance upgraded with users behaves the
	// same): the endpoint answers the one non-probing 403.
	srv := claimServer(t, nil)
	rec := postTo(srv, "/api/v1/setup/claim-owner",
		`{"token":"anything","username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "owner_claim_invalid" {
		t.Errorf("code = %q, want owner_claim_invalid", er.Error.Code)
	}
}

func TestInstanceReportsOwnerClaimPending(t *testing.T) {
	// A repo that never minted a token (e.g. an upgraded instance with users)
	// is not pending — and the signal latches false once observed, so the
	// pending case below needs its own server.
	if publicInstance(t, claimServer(t, nil)).OwnerClaimPending {
		t.Fatal("owner_claim_pending = true before any mint, want false")
	}

	srv := claimServer(t, nil)
	raw := mintSetupToken(t, srv)
	if !publicInstance(t, srv).OwnerClaimPending {
		t.Fatal("owner_claim_pending = false while the claim is outstanding, want true")
	}
	rec := postTo(srv, "/api/v1/setup/claim-owner",
		`{"token":"`+raw+`","username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("claim-owner = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if publicInstance(t, srv).OwnerClaimPending {
		t.Fatal("owner_claim_pending = true after the claim, want false")
	}
}

func TestClaimOwnerEndpointValidation(t *testing.T) {
	srv := claimServer(t, nil)
	rec := postTo(srv, "/api/v1/setup/claim-owner", `{"username":"a","email":"nope","password":"short"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if len(er.Error.Fields) < 4 {
		t.Errorf("fields = %+v, want token/username/email/password errors", er.Error.Fields)
	}
}

func TestClaimOwnerEndpointIsRateLimited(t *testing.T) {
	// The claim endpoint is a token-guessing surface: it sits behind the same
	// strict auth limiter as login.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	srv := New(testConfig(), nil, nil,
		WithAuthService(svc, 15*time.Minute),
		WithAuthRateLimiter(ratelimit.NewLimiter(&fakeCounter{}, 2, time.Minute)),
		WithLogger(logger),
	)
	body := `{"token":"guess","username":"eve","email":"eve@example.test","password":"supersecret"}`
	for i := 1; i <= 2; i++ {
		if rec := postTo(srv, "/api/v1/setup/claim-owner", body); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt #%d throttled too early (status 429)", i)
		}
	}
	if rec := postTo(srv, "/api/v1/setup/claim-owner", body); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt #3 status = %d, want 429", rec.Code)
	}
}
