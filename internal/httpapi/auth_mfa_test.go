package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
	"github.com/pquerna/otp/totp"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// mfaFakeRepo is an in-memory auth.MFARepository for handler tests, mirroring
// the user_mfa / mfa_recovery_codes semantics.
type mfaFakeRepo struct {
	rows  map[uuid.UUID]*sqlcgen.UserMfa
	codes map[uuid.UUID][]*sqlcgen.MfaRecoveryCode
}

func newMFAFakeRepo() *mfaFakeRepo {
	return &mfaFakeRepo{
		rows:  map[uuid.UUID]*sqlcgen.UserMfa{},
		codes: map[uuid.UUID][]*sqlcgen.MfaRecoveryCode{},
	}
}

func (f *mfaFakeRepo) UpsertUserMFA(_ context.Context, a sqlcgen.UpsertUserMFAParams) (sqlcgen.UserMfa, error) {
	if row, ok := f.rows[a.UserID]; ok {
		if row.Enabled {
			return sqlcgen.UserMfa{}, errors.New("no rows")
		}
		row.TotpSecretSealed = a.TotpSecretSealed
		return *row, nil
	}
	row := &sqlcgen.UserMfa{UserID: a.UserID, TotpSecretSealed: a.TotpSecretSealed, CreatedAt: time.Now()}
	f.rows[a.UserID] = row
	return *row, nil
}

func (f *mfaFakeRepo) GetUserMFA(_ context.Context, userID uuid.UUID) (sqlcgen.UserMfa, error) {
	if row, ok := f.rows[userID]; ok {
		return *row, nil
	}
	return sqlcgen.UserMfa{}, errors.New("not found")
}

func (f *mfaFakeRepo) EnableUserMFA(_ context.Context, userID uuid.UUID) (int64, error) {
	row, ok := f.rows[userID]
	if !ok || row.Enabled {
		return 0, nil
	}
	row.Enabled = true
	return 1, nil
}

func (f *mfaFakeRepo) DeleteUserMFA(_ context.Context, userID uuid.UUID) (int64, error) {
	if _, ok := f.rows[userID]; !ok {
		return 0, nil
	}
	delete(f.rows, userID)
	return 1, nil
}

func (f *mfaFakeRepo) CreateRecoveryCode(_ context.Context, a sqlcgen.CreateRecoveryCodeParams) error {
	f.codes[a.UserID] = append(f.codes[a.UserID], &sqlcgen.MfaRecoveryCode{
		ID: uuid.New(), UserID: a.UserID, CodeHash: a.CodeHash, CreatedAt: time.Now(),
	})
	return nil
}

func (f *mfaFakeRepo) DeleteRecoveryCodes(_ context.Context, userID uuid.UUID) error {
	delete(f.codes, userID)
	return nil
}

func (f *mfaFakeRepo) UseRecoveryCode(_ context.Context, a sqlcgen.UseRecoveryCodeParams) (int64, error) {
	for _, c := range f.codes[a.UserID] {
		if c.CodeHash == a.CodeHash && !c.UsedAt.Valid {
			c.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return 1, nil
		}
	}
	return 0, nil
}

// BurnTOTPStep / BurnPendingTOTPStep mirror the 0134 SQL: the accepted step is
// recorded only when it is strictly newer than the last one, so a replayed code
// writes nothing and the service refuses it like a wrong code.
func (f *mfaFakeRepo) BurnTOTPStep(_ context.Context, a sqlcgen.BurnTOTPStepParams) (int64, error) {
	row, ok := f.rows[a.UserID]
	if !ok || !row.Enabled {
		return 0, nil
	}
	return burnStep(row, a.Step), nil
}

func (f *mfaFakeRepo) BurnPendingTOTPStep(_ context.Context, a sqlcgen.BurnPendingTOTPStepParams) (int64, error) {
	row, ok := f.rows[a.UserID]
	if !ok {
		return 0, nil
	}
	return burnStep(row, a.Step), nil
}

func burnStep(row *sqlcgen.UserMfa, step int64) int64 {
	if row.LastTotpStep != nil && *row.LastTotpStep >= step {
		return 0
	}
	row.LastTotpStep = &step
	return 1
}

// ListRecentUserMFASecrets mirrors the SQL's newest-first LIMIT.
func (f *mfaFakeRepo) ListRecentUserMFASecrets(_ context.Context, rowLimit int32) ([]sqlcgen.ListRecentUserMFASecretsRow, error) {
	rows := make([]sqlcgen.ListRecentUserMFASecretsRow, 0, len(f.rows))
	for id, row := range f.rows {
		rows = append(rows, sqlcgen.ListRecentUserMFASecretsRow{UserID: id, TotpSecretSealed: row.TotpSecretSealed})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UserID.String() < rows[j].UserID.String() })
	if rowLimit > 0 && len(rows) > int(rowLimit) {
		rows = rows[:rowLimit]
	}
	return rows, nil
}

func (f *mfaFakeRepo) CountUnusedRecoveryCodes(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for _, c := range f.codes[userID] {
		if !c.UsedAt.Valid {
			n++
		}
	}
	return n, nil
}

// mfaTestSecret matches authServer's issuer secret so cross-purpose token
// tests can mint tokens with controlled audiences/TTLs.
const mfaTestSecret = "test-secret-test-secret-test-secret-0"

// mfaServer builds a server whose auth service has TOTP MFA wired (no cipher —
// dev raw storage; the sealing itself is covered by internal/auth tests). Audit
// output goes to buf.
func mfaServer(t *testing.T, buf *bytes.Buffer) *Server {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer(mfaTestSecret, "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour, auth.WithMFA(newMFAFakeRepo(), nil, "Vidra Test"))
	opts := []Option{WithAuthService(svc, 15*time.Minute)}
	if buf != nil {
		opts = append(opts, WithLogger(slog.New(slog.NewJSONHandler(buf, nil))))
	}
	return New(testConfig(), nil, nil, opts...)
}

func postJSONWithAuth(srv *Server, path, token, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func deleteJSONWithAuth(srv *Server, path, token, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// enrollAndEnable drives register → enroll → verify and returns the access
// token, the TOTP secret, and the recovery codes.
// totpNow returns the TOTP code for secret at the current instant — the code an
// ENROLLMENT is confirmed with.
func totpNow(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	return code
}

// challengeCode returns a TOTP code the CHALLENGE will accept after
// enrollAndEnable. It comes from the next 30-second step because the code that
// confirmed the enrollment is burned (0134) — a code is single-use, so a test
// that reuses the enrollment code is asserting the old, replayable behaviour.
func challengeCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	return code
}

func enrollAndEnable(t *testing.T, srv *Server) (token, secret string, recovery []string) {
	t.Helper()
	token = registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postJSONWithAuth(srv, "/api/v1/auth/mfa/totp", token, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var enr totpEnrollmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &enr); err != nil {
		t.Fatalf("unmarshal enrollment: %v", err)
	}
	if enr.Secret == "" || !strings.HasPrefix(enr.OtpauthURI, "otpauth://totp/") {
		t.Fatalf("enrollment = %+v; want secret + otpauth URI", enr)
	}

	code, err := totp.GenerateCode(enr.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	rec = postJSONWithAuth(srv, "/api/v1/auth/mfa/totp/verify", token, `{"code":"`+code+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var rcs recoveryCodesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rcs); err != nil {
		t.Fatalf("unmarshal recovery codes: %v", err)
	}
	if len(rcs.RecoveryCodes) != 10 {
		t.Fatalf("got %d recovery codes, want 10", len(rcs.RecoveryCodes))
	}
	return token, enr.Secret, rcs.RecoveryCodes
}

// mfaLogin logs in and asserts the MFA-required shape, returning the mfa_token.
func mfaLogin(t *testing.T, srv *Server) string {
	t.Helper()
	rec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{`"token"`, `"refresh_token"`, `"user"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("MFA-required login leaked %s: %s", forbidden, body)
		}
	}
	var mr mfaRequiredResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &mr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !mr.MFARequired || mr.MFAToken == "" {
		t.Fatalf("login response = %+v; want mfa_required + mfa_token", mr)
	}
	return mr.MFAToken
}

func TestMFAEnrollVerifyLoginChallengeRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	srv := mfaServer(t, &buf)
	_, secret, _ := enrollAndEnable(t, srv)

	// Status reflects the enabled state and the fresh recovery codes.
	tokenBefore := mfaLogin(t, srv) // MFA challenge shape asserted inside
	code := challengeCode(t, secret)
	rec := postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"`+tokenBefore+`","code":"`+code+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatalf("unmarshal auth response: %v", err)
	}
	if ar.Token == "" || ar.RefreshToken == "" || ar.User.Username != "ada" {
		t.Fatalf("challenge auth response = %+v; want full session", ar)
	}
	// The session works (and /auth/mfa reports enabled + 10 codes).
	st := getWithAuth(srv, "/api/v1/auth/mfa", ar.Token)
	if st.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", st.Code, st.Body.String())
	}
	var ms mfaStatusResponse
	_ = json.Unmarshal(st.Body.Bytes(), &ms)
	if !ms.Enabled || ms.RecoveryCodesRemaining != 10 {
		t.Errorf("mfa status = %+v, want enabled with 10 codes", ms)
	}

	// The mfa_token must NOT work as an access token.
	if rec := getWithAuth(srv, "/api/v1/auth/me", tokenBefore); rec.Code != http.StatusUnauthorized {
		t.Errorf("mfa_token used as access token: status = %d, want 401", rec.Code)
	}

	// Audit trail: challenge success with the method as reason, never the code
	// or secret.
	events := auditEvents(t, &buf)
	ev := findAudit(events, observability.ActionMFAChallenge, observability.ResultSuccess)
	if ev == nil {
		t.Fatal("expected an auth.mfa.challenge success audit event")
	}
	if ev["reason"] != "totp" {
		t.Errorf("challenge reason = %v, want totp", ev["reason"])
	}
	if findAudit(events, observability.ActionMFAEnable, observability.ResultSuccess) == nil {
		t.Error("expected an auth.mfa.enable success audit event")
	}
	logs := buf.String()
	if strings.Contains(logs, secret) || strings.Contains(logs, code) {
		t.Error("TOTP secret/code must never appear in logs")
	}
}

func TestMFAChallengeCookieMode(t *testing.T) {
	srv := mfaServer(t, nil)
	_, secret, _ := enrollAndEnable(t, srv)
	mfaToken := mfaLogin(t, srv)

	code := challengeCode(t, secret)
	rec := postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"`+mfaToken+`","code":"`+code+`","cookie_mode":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"refresh_token"`) {
		t.Error("cookie-mode body must omit refresh_token")
	}
	var cookie *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "vidra_refresh" {
			cookie = ck
		}
	}
	if cookie == nil || cookie.Value == "" || !cookie.HttpOnly {
		t.Fatalf("expected an httpOnly vidra_refresh cookie, got %+v", cookie)
	}
}

func TestMFAChallengeRecoveryCodeSingleUse(t *testing.T) {
	var buf bytes.Buffer
	srv := mfaServer(t, &buf)
	_, _, recovery := enrollAndEnable(t, srv)

	// Redeem a recovery code.
	rec := postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"`+mfaLogin(t, srv)+`","code":"`+recovery[0]+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery challenge status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &ar)
	if ar.Token == "" {
		t.Fatal("recovery challenge should mint a session")
	}
	// One fewer remaining.
	st := getWithAuth(srv, "/api/v1/auth/mfa", ar.Token)
	var ms mfaStatusResponse
	_ = json.Unmarshal(st.Body.Bytes(), &ms)
	if ms.RecoveryCodesRemaining != 9 {
		t.Errorf("remaining = %d, want 9", ms.RecoveryCodesRemaining)
	}
	// The same code is single-use.
	rec = postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"`+mfaLogin(t, srv)+`","code":"`+recovery[0]+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused recovery code status = %d, want 401", rec.Code)
	}
	// The audit trail has the recovery_code success and the failure, and never
	// a raw recovery code.
	events := auditEvents(t, &buf)
	if ev := findAudit(events, observability.ActionMFAChallenge, observability.ResultSuccess); ev == nil || ev["reason"] != "recovery_code" {
		t.Errorf("expected a recovery_code challenge success event, got %v", ev)
	}
	if findAudit(events, observability.ActionMFAChallenge, observability.ResultFailure) == nil {
		t.Error("expected a challenge failure audit event")
	}
	if strings.Contains(buf.String(), recovery[0]) {
		t.Error("a raw recovery code must never appear in logs")
	}
}

func TestMFADisableRequiresPassword(t *testing.T) {
	var buf bytes.Buffer
	srv := mfaServer(t, &buf)
	token, _, _ := enrollAndEnable(t, srv)

	// Wrong password → 403, still enabled.
	rec := deleteJSONWithAuth(srv, "/api/v1/auth/mfa/totp", token, `{"password":"wrong-password"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong-password disable status = %d, want 403", rec.Code)
	}
	st := getWithAuth(srv, "/api/v1/auth/mfa", token)
	var ms mfaStatusResponse
	_ = json.Unmarshal(st.Body.Bytes(), &ms)
	if !ms.Enabled {
		t.Fatal("failed disable must leave MFA enabled")
	}
	// Missing password → 422.
	if rec := deleteJSONWithAuth(srv, "/api/v1/auth/mfa/totp", token, `{}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing-password disable status = %d, want 422", rec.Code)
	}
	// Correct password → 204, MFA off, login is plain again.
	if rec := deleteJSONWithAuth(srv, "/api/v1/auth/mfa/totp", token, `{"password":"supersecret"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("disable status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	login := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if login.Code != http.StatusOK || strings.Contains(login.Body.String(), "mfa_required") {
		t.Fatalf("post-disable login = %d %s; want a plain session", login.Code, login.Body.String())
	}
	// Nothing left to disable → 404.
	if rec := deleteJSONWithAuth(srv, "/api/v1/auth/mfa/totp", token, `{"password":"supersecret"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("second disable status = %d, want 404", rec.Code)
	}
	// Audit: disable failure (invalid_password) + success; no password logged.
	events := auditEvents(t, &buf)
	if findAudit(events, observability.ActionMFADisable, observability.ResultFailure) == nil {
		t.Error("expected an auth.mfa.disable failure audit event")
	}
	if findAudit(events, observability.ActionMFADisable, observability.ResultSuccess) == nil {
		t.Error("expected an auth.mfa.disable success audit event")
	}
	for _, e := range events {
		for k := range e {
			if observability.IsSensitiveKey(k) {
				t.Errorf("audit event carries denylisted key %q", k)
			}
		}
	}
	if strings.Contains(buf.String(), "wrong-password") {
		t.Error("the attempted password must never appear in logs")
	}
}

func TestMFAChallengeRejectsTamperedAndExpiredTokens(t *testing.T) {
	srv := mfaServer(t, nil)
	_, secret, _ := enrollAndEnable(t, srv)
	code := challengeCode(t, secret)

	// Tampered/garbage token.
	rec := postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"garbage","code":"`+code+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage token status = %d, want 401", rec.Code)
	}
	// A signature-tampered variant of a real token. Flip a character at the
	// START of the signature, not at its tail: golang-jwt v5 decodes without
	// WithStrictDecoding, so the FINAL base64url character of a 43-char (32
	// byte) signature carries only four significant bits. Replacing the last
	// two characters with "xx" therefore decodes to the very same signature
	// whenever the real one ended "xw" — the token stays valid, the challenge
	// answers 200, and this test fails. Measured at 293 in 300000 (~0.1%, one
	// run in a thousand), and it flaked a required CI lane on 2026-09-09. The
	// first signature character carries a full six bits, so any change to it
	// is a real change.
	real := mfaLogin(t, srv)
	sigAt := strings.LastIndex(real, ".") + 1
	flipped := "A"
	if strings.HasPrefix(real[sigAt:], "A") {
		flipped = "B"
	}
	tampered := real[:sigAt] + flipped + real[sigAt+1:]
	if tampered == real {
		t.Fatal("the tamper did not change the token")
	}
	rec = postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"`+tampered+`","code":"`+code+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered token status = %d, want 401", rec.Code)
	}
	// An expired mfa_token (same secret + issuer + ":mfa" audience, negative TTL).
	adaID := loginUserID(t, srv, real)
	expired, err := auth.NewTokenIssuer(mfaTestSecret, "vidra", "vidra:mfa", -time.Minute).Issue(adaID, "user")
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}
	rec = postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"`+expired+`","code":"`+code+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token status = %d, want 401", rec.Code)
	}
	// An ACCESS token is not an mfa_token (single-purpose audience).
	access, err := auth.NewTokenIssuer(mfaTestSecret, "vidra", "vidra", time.Minute).Issue(adaID, "user")
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	rec = postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"`+access+`","code":"`+code+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("access-token-as-mfa_token status = %d, want 401", rec.Code)
	}
	// The genuine token with a wrong code → 401 too.
	rec = postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"`+real+`","code":"000000"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-code status = %d, want 401", rec.Code)
	}
}

// loginUserID extracts the subject user ID from a JWT-shaped token by parsing
// its (unverified) payload — good enough to address a test account.
func loginUserID(t *testing.T, srv *Server, mfaToken string) uuid.UUID {
	t.Helper()
	// The fake repo has exactly one user; grab it via a fresh full login after
	// temporarily satisfying MFA is overkill — decode the JWT payload instead.
	parts := strings.Split(mfaToken, ".")
	if len(parts) != 3 {
		t.Fatalf("mfa_token is not a JWT: %q", mfaToken)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	id, err := uuid.Parse(claims.Sub)
	if err != nil {
		t.Fatalf("parse sub: %v", err)
	}
	return id
}

func TestMFAValidationAndStateErrors(t *testing.T) {
	srv := mfaServer(t, nil)
	token, _, _ := enrollAndEnable(t, srv)

	// 422s: missing fields.
	if rec := postJSONWithAuth(srv, "/api/v1/auth/mfa/totp/verify", token, `{}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("verify without code status = %d, want 422", rec.Code)
	}
	if rec := postTo(srv, "/api/v1/auth/mfa/challenge", `{"code":"123456"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("challenge without mfa_token status = %d, want 422", rec.Code)
	}
	if rec := postTo(srv, "/api/v1/auth/mfa/challenge", `{"mfa_token":"x"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("challenge without code status = %d, want 422", rec.Code)
	}
	// Enrolling again while enabled → 409 (both begin and verify).
	if rec := postJSONWithAuth(srv, "/api/v1/auth/mfa/totp", token, `{}`); rec.Code != http.StatusConflict {
		t.Errorf("begin-when-enabled status = %d, want 409", rec.Code)
	}
	if rec := postJSONWithAuth(srv, "/api/v1/auth/mfa/totp/verify", token, `{"code":"123456"}`); rec.Code != http.StatusConflict {
		t.Errorf("verify-when-enabled status = %d, want 409", rec.Code)
	}
	// Anonymous access → 401 on the requireAuth routes.
	for _, probe := range []func() *httptest.ResponseRecorder{
		func() *httptest.ResponseRecorder { return getWithAuth(srv, "/api/v1/auth/mfa", "") },
		func() *httptest.ResponseRecorder { return postJSONWithAuth(srv, "/api/v1/auth/mfa/totp", "", `{}`) },
		func() *httptest.ResponseRecorder {
			return postJSONWithAuth(srv, "/api/v1/auth/mfa/totp/verify", "", `{"code":"123456"}`)
		},
		func() *httptest.ResponseRecorder {
			return deleteJSONWithAuth(srv, "/api/v1/auth/mfa/totp", "", `{"password":"x"}`)
		},
	} {
		if rec := probe(); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous MFA route status = %d, want 401", rec.Code)
		}
	}
	// Verify without a pending enrollment (fresh user) → 400.
	fresh := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := postJSONWithAuth(srv, "/api/v1/auth/mfa/totp/verify", fresh, `{"code":"123456"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("verify-without-enrollment status = %d, want 400", rec.Code)
	}
	// Fresh user's status: disabled, zero codes.
	st := getWithAuth(srv, "/api/v1/auth/mfa", fresh)
	var ms mfaStatusResponse
	_ = json.Unmarshal(st.Body.Bytes(), &ms)
	if ms.Enabled || ms.RecoveryCodesRemaining != 0 {
		t.Errorf("fresh status = %+v, want disabled/0", ms)
	}
}

func TestMFAChallengeIsAuthRateLimited(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer(mfaTestSecret, "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour, auth.WithMFA(newMFAFakeRepo(), nil, "Vidra Test"))
	srv := New(testConfig(), nil, nil,
		WithAuthService(svc, 15*time.Minute),
		WithAuthRateLimiter(ratelimit.NewLimiter(&fakeCounter{}, 2, time.Minute)),
		WithLogger(logger),
	)

	body := `{"mfa_token":"garbage","code":"000000"}`
	for i := 1; i <= 2; i++ {
		if rec := postTo(srv, "/api/v1/auth/mfa/challenge", body); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt #%d throttled too early", i)
		}
	}
	rec := postTo(srv, "/api/v1/auth/mfa/challenge", body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt #3 status = %d, want 429", rec.Code)
	}
	if findAudit(auditEvents(t, &buf), observability.ActionRateLimited, observability.ResultFailure) == nil {
		t.Error("expected an auth.rate_limited audit event")
	}
}

// --- an MFA secret the configured KEK cannot open (A37-1) ---------------------

// mfaTestKEK and mfaTestOtherKEK are throwaway 32-byte fixture keys, not
// credentials: they exist only so a test can seal a secret under one and ask the
// server to open it with the other, which is exactly what restoring a dump
// without its config archive does to a live instance.
var (
	mfaTestKEK      = []byte("vidra-test-kek-A-not-a-real-key!")
	mfaTestOtherKEK = []byte("vidra-test-kek-B-not-a-real-key!")
)

// mfaSealedServer builds a server whose TOTP secrets are sealed under cipher and
// hands back the fake repo, so a test can replace a stored secret with one this
// server's KEK cannot open.
func mfaSealedServer(t *testing.T, buf *bytes.Buffer, cipher *secretbox.Cipher) (*Server, *mfaFakeRepo) {
	t.Helper()
	repo := newAuthFakeRepo()
	mfaRepo := newMFAFakeRepo()
	issuer := auth.NewTokenIssuer(mfaTestSecret, "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour, auth.WithMFA(mfaRepo, cipher, "Vidra Test"))
	opts := []Option{WithAuthService(svc, 15*time.Minute)}
	if buf != nil {
		opts = append(opts, WithLogger(slog.New(slog.NewJSONHandler(buf, nil))))
	}
	return New(testConfig(), nil, nil, opts...), mfaRepo
}

// reseal replaces the single stored TOTP secret with one sealed under other,
// and returns the account's id and the ciphertext now on the row.
func reseal(t *testing.T, repo *mfaFakeRepo, other *secretbox.Cipher, plaintext string) (uuid.UUID, string) {
	t.Helper()
	if len(repo.rows) != 1 {
		t.Fatalf("expected exactly one user_mfa row, got %d", len(repo.rows))
	}
	for id, row := range repo.rows {
		sealed, err := other.Seal([]byte(plaintext))
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		row.TotpSecretSealed = sealed
		return id, sealed
	}
	return uuid.Nil, ""
}

func TestMFAChallengeWithAnUndecryptableSecretIsIndistinguishableFromAWrongCode(t *testing.T) {
	kek, err := secretbox.NewCipher(mfaTestKEK)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	other, err := secretbox.NewCipher(mfaTestOtherKEK)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	// The baseline: what a WRONG CODE looks like on a healthy instance.
	var wrongBuf bytes.Buffer
	healthy, _ := mfaSealedServer(t, &wrongBuf, kek)
	_, _, _ = enrollAndEnable(t, healthy)
	wrongRec := postTo(healthy, "/api/v1/auth/mfa/challenge",
		`{"mfa_token":"`+mfaLogin(t, healthy)+`","code":"000000"}`)
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-code status = %d, want 401; body=%s", wrongRec.Code, wrongRec.Body.String())
	}

	// The restore-with-the-wrong-KEK instance: the secret on the row was sealed
	// under a key this server does not have.
	var buf bytes.Buffer
	srv, repo := mfaSealedServer(t, &buf, kek)
	_, secret, recovery := enrollAndEnable(t, srv)
	userID, ciphertext := reseal(t, repo, other, secret)

	rec := postTo(srv, "/api/v1/auth/mfa/challenge",
		`{"mfa_token":"`+mfaLogin(t, srv)+`","code":"`+challengeCode(t, secret)+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("undecryptable-secret status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	// Byte-identical apart from request_id, which is per-request by design and
	// is the ONLY field a caller may legitimately see differ.
	if got, want := withoutRequestID(t, rec.Body.Bytes()), withoutRequestID(t, wrongRec.Body.Bytes()); got != want {
		t.Errorf("body = %s, want identical to the wrong-code body %s", got, want)
	}

	// ONE structured line at error level, naming the failure class and the
	// account — and neither the ciphertext nor any key material.
	logs := buf.String()
	if n := strings.Count(logs, "mfa_secret_undecryptable"); n != 1 {
		t.Errorf("mfa_secret_undecryptable appears %d times in the log, want exactly 1:\n%s", n, logs)
	}
	if !strings.Contains(logs, `"level":"ERROR"`) {
		t.Errorf("no ERROR-level line in the log:\n%s", logs)
	}
	if !strings.Contains(logs, userID.String()) {
		t.Errorf("log does not name the account %s:\n%s", userID, logs)
	}
	for _, leaked := range []string{ciphertext, secret, base64.StdEncoding.EncodeToString(mfaTestKEK)} {
		if strings.Contains(logs, leaked) {
			t.Fatalf("log leaked secret material:\n%s", logs)
		}
	}

	// Recovery codes are hashed, not KEK-sealed, so the account is not locked
	// out: the operator-facing failure must not become a user-facing one.
	rec = postTo(srv, "/api/v1/auth/mfa/challenge",
		`{"mfa_token":"`+mfaLogin(t, srv)+`","code":"`+recovery[0]+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery-code challenge status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMFAChallengeWithASealedSecretAndNoKEKFailsClosed(t *testing.T) {
	// The other half of A37-1: a dump restored with MFA_KEY_KEK unset entirely.
	// The row still carries `enc:` ciphertext and there is no cipher to open it.
	other, err := secretbox.NewCipher(mfaTestOtherKEK)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	var buf bytes.Buffer
	srv, repo := mfaSealedServer(t, &buf, nil)
	_, secret, _ := enrollAndEnable(t, srv)
	reseal(t, repo, other, secret)
	rec := postTo(srv, "/api/v1/auth/mfa/challenge",
		`{"mfa_token":"`+mfaLogin(t, srv)+`","code":"`+challengeCode(t, secret)+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(buf.String(), "mfa_secret_undecryptable") {
		t.Errorf("log does not name the failure class:\n%s", buf.String())
	}
}

// withoutRequestID renders an error body with the per-request id dropped, so a
// test can assert that two refusals are otherwise indistinguishable.
func withoutRequestID(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("unmarshal error body %s: %v", body, err)
	}
	delete(envelope.Error, "request_id")
	out, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}
