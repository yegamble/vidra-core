package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// The email-change fixtures. Addresses only — this flow's one secret is the
// account password, and it is the shared fixture the change-password tests
// already define, so nothing new that looks like a credential is introduced.
const (
	fixtureOldEmail   = "ada@example.test"
	fixtureNewEmail   = "ada.new@example.test"
	fixtureOtherEmail = "bob@example.test"
)

const emailChangePath = "/api/v1/auth/me/email-change"

// changeEmailBody builds a POST /auth/me/email-change body.
func changeEmailBody(password, newEmail string) string {
	return fmt.Sprintf(`{"current_password":%q,"new_email":%q}`, password, newEmail)
}

// emailChangeServer is authServerWithMailer plus the backing fake, because the
// email-change assertions need BOTH: the mailer to recover the token the way a
// real mailbox would, and the repository to put an account into a state no
// endpoint can produce (an empty password hash).
func emailChangeServer(t *testing.T) (*Server, *authFakeRepo, *captureResetMailer) {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	mailer := &captureResetMailer{}
	svc := auth.NewService(repo, issuer, 720*time.Hour, auth.WithMailer(mailer))
	return New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute)), repo, mailer
}

// meEmail reads the account's address back through the API — an independent
// readback, not a re-read of what the change endpoint returned.
func meEmail(t *testing.T, srv *Server, token string) string {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("/auth/me = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var view struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode /auth/me: %v", err)
	}
	return view.Email
}

// pendingState reads GET /auth/me/email-change.
func pendingState(t *testing.T, srv *Server, token string) emailChangeStateView {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, emailChangePath, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET email-change = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var st emailChangeStateView
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode pending state: %v", err)
	}
	return st
}

// TestRequestEmailChangeMailsTheNewAddressAndChangesNothingYet is SC1's first
// half at the HTTP boundary: 202, a readable pending state, the confirmation
// addressed to the NEW mailbox, and an account whose address has not moved.
func TestRequestEmailChangeMailsTheNewAddressAndChangesNothingYet(t *testing.T) {
	srv, _, mailer := emailChangeServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)

	rec := sendJSONAuth(srv, http.MethodPost, emailChangePath,
		changeEmailBody(fixtureCurrentPassword, fixtureNewEmail), reg.Token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request email change = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var body emailChangeStateView
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Pending || body.NewEmail != fixtureNewEmail {
		t.Errorf("response = %+v, want pending for the new address", body)
	}
	if body.ExpiresAt == nil || !body.ExpiresAt.After(time.Now()) {
		t.Errorf("response expiry = %v, want a future instant", body.ExpiresAt)
	}
	// The response must never carry the token: it is the possession proof, and
	// handing it to the requester would defeat the entire second step.
	if mailer.changeToken == "" {
		t.Fatal("no confirmation token was delivered")
	}
	if strings.Contains(rec.Body.String(), mailer.changeToken) {
		t.Error("the response body carries the confirmation token")
	}
	if mailer.changeTo != fixtureNewEmail {
		t.Errorf("confirmation delivered to %q, want the NEW address", mailer.changeTo)
	}
	if mailer.changeNotices != 0 {
		t.Errorf("a change notice was sent before anything changed (%d)", mailer.changeNotices)
	}
	if got := meEmail(t, srv, reg.Token); got != fixtureOldEmail {
		t.Errorf("/auth/me email after the request = %q, want it UNCHANGED", got)
	}
	if st := pendingState(t, srv, reg.Token); !st.Pending || st.NewEmail != fixtureNewEmail {
		t.Errorf("pending state = %+v, want the new address pending", st)
	}
}

// TestConfirmEmailChangeMovesTheAddressAndNoticesTheOldOne is SC1's second half
// plus SC3's consequences: the address moves, the old one stops signing in, the
// new one starts, and the OLD mailbox is told.
func TestConfirmEmailChangeMovesTheAddressAndNoticesTheOldOne(t *testing.T) {
	srv, _, mailer := emailChangeServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath,
		changeEmailBody(fixtureCurrentPassword, fixtureNewEmail), reg.Token); rec.Code != http.StatusAccepted {
		t.Fatalf("request = %d, want 202", rec.Code)
	}
	token := mailer.changeToken

	rec := sendJSONAuth(srv, http.MethodPost, emailChangePath+"/confirm",
		fmt.Sprintf(`{"token":%q}`, token), reg.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var confirmed emailChangeConfirmedView
	if err := json.Unmarshal(rec.Body.Bytes(), &confirmed); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if confirmed.Email != fixtureNewEmail {
		t.Errorf("confirm returned %q, want the new address", confirmed.Email)
	}
	if got := meEmail(t, srv, reg.Token); got != fixtureNewEmail {
		t.Errorf("/auth/me email = %q, want the new one", got)
	}
	if st := pendingState(t, srv, reg.Token); st.Pending {
		t.Errorf("pending state after confirm = %+v, want nothing pending", st)
	}
	// Login moves with it: the old address stops working, the new one starts.
	if old := postTo(srv, "/api/v1/auth/login",
		`{"email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`); old.Code != http.StatusUnauthorized {
		t.Errorf("login with the OLD address = %d, want 401", old.Code)
	}
	if fresh := postTo(srv, "/api/v1/auth/login",
		`{"email":"`+fixtureNewEmail+`","password":"`+fixtureCurrentPassword+`"}`); fresh.Code != http.StatusOK {
		t.Errorf("login with the NEW address = %d, want 200; body=%s", fresh.Code, fresh.Body.String())
	}
	// The notice went to the OLD address and named the new one.
	if mailer.changeNotices != 1 || mailer.noticeOld != fixtureOldEmail || mailer.noticeNew != fixtureNewEmail {
		t.Errorf("notice = (%d, %q -> %q), want exactly one from the old to the new address",
			mailer.changeNotices, mailer.noticeOld, mailer.noticeNew)
	}
	// Single use.
	replay := sendJSONAuth(srv, http.MethodPost, emailChangePath+"/confirm",
		fmt.Sprintf(`{"token":%q}`, token), reg.Token)
	if replay.Code != http.StatusBadRequest {
		t.Errorf("replaying the token = %d, want 400", replay.Code)
	}
}

// TestEmailChangeDoesNotRevokeOtherSessions pins the shipped decision. An email
// address is not a credential — knowing it grants nothing — and the change is
// already gated on the password, so signing every device out would be a cost
// with no security bought. The old-address notice is the safeguard instead.
func TestEmailChangeDoesNotRevokeOtherSessions(t *testing.T) {
	srv, _, mailer := emailChangeServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)
	// A second device: its own session, minted by a real login.
	second := postTo(srv, "/api/v1/auth/login",
		`{"email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second login = %d, want 200", second.Code)
	}
	var other authResponse
	if err := json.Unmarshal(second.Body.Bytes(), &other); err != nil {
		t.Fatalf("decode second login: %v", err)
	}

	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath,
		changeEmailBody(fixtureCurrentPassword, fixtureNewEmail), reg.Token); rec.Code != http.StatusAccepted {
		t.Fatalf("request = %d, want 202", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath+"/confirm",
		fmt.Sprintf(`{"token":%q}`, mailer.changeToken), reg.Token); rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d, want 200", rec.Code)
	}
	if got := meEmail(t, srv, other.Token); got != fixtureNewEmail {
		t.Errorf("the OTHER session reads %q; it must stay signed in and see the new address", got)
	}
}

// TestEmailChangeRefusalsChangeNothing walks every refusal the flow ships, each
// asserting the account's address afterwards through an independent read.
func TestEmailChangeRefusalsChangeNothing(t *testing.T) {
	srv, repo, mailer := emailChangeServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)
	// Another account, and an account whose USERNAME looks like an address.
	if rec := postTo(srv, "/api/v1/auth/register",
		`{"username":"bob","email":"`+fixtureOtherEmail+`","password":"`+fixtureCurrentPassword+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("register bob = %d", rec.Code)
	}
	if rec := postTo(srv, "/api/v1/auth/register",
		`{"username":"carol","email":"carol@mail.example.test","password":"`+fixtureCurrentPassword+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("register carol = %d", rec.Code)
	}
	// Carol predates the '@' ban on usernames, which registration now enforces,
	// so her lookalike name is written straight into the store exactly as a
	// legacy row would carry it. Those rows are precisely why an email equal to
	// a username has to be refused.
	carol := repo.users["carol@mail.example.test"]
	carol.Username = "carol@example.test"
	repo.users["carol@mail.example.test"] = carol

	cases := []struct {
		name string
		body string
		want int
	}{
		{"wrong current password", changeEmailBody("not-the-password", fixtureNewEmail), http.StatusForbidden},
		{"the address it already has", changeEmailBody(fixtureCurrentPassword, fixtureOldEmail), http.StatusUnprocessableEntity},
		{"same address, different case", changeEmailBody(fixtureCurrentPassword, "ADA@example.test"), http.StatusUnprocessableEntity},
		{"malformed address", changeEmailBody(fixtureCurrentPassword, "not-an-address"), http.StatusUnprocessableEntity},
		{"missing password", `{"new_email":"` + fixtureNewEmail + `"}`, http.StatusUnprocessableEntity},
		{"another account's address", changeEmailBody(fixtureCurrentPassword, fixtureOtherEmail), http.StatusConflict},
		{"another account's USERNAME", changeEmailBody(fixtureCurrentPassword, "carol@example.test"), http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendJSONAuth(srv, http.MethodPost, emailChangePath, tc.body, reg.Token)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if got := meEmail(t, srv, reg.Token); got != fixtureOldEmail {
				t.Errorf("the refused request moved the address to %q", got)
			}
			if st := pendingState(t, srv, reg.Token); st.Pending {
				t.Errorf("a refused request left something pending: %+v", st)
			}
			if mailer.changeToken != "" {
				t.Errorf("a refused request mailed a confirmation to %q", mailer.changeTo)
			}
		})
	}
}

// TestRequestEmailChangeRefusesPasswordlessAccount: the OAuth/ATProto-only shape
// gets the 409 that names the flow which CAN set a password, not the
// unfalsifiable 403 an empty bcrypt hash would otherwise produce.
func TestRequestEmailChangeRefusesPasswordlessAccount(t *testing.T) {
	srv, repo, _ := emailChangeServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)
	for k, u := range repo.users {
		u.PasswordHash = ""
		repo.users[k] = u
	}
	rec := sendJSONAuth(srv, http.MethodPost, emailChangePath,
		changeEmailBody(fixtureCurrentPassword, fixtureNewEmail), reg.Token)
	if rec.Code != http.StatusConflict {
		t.Fatalf("passwordless account = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "reset") {
		t.Errorf("the 409 does not point at the reset flow: %s", rec.Body.String())
	}
}

// TestConfirmEmailChangeRefusesAnotherAccountsToken: the token proves the
// mailbox, the session proves the account, and both are required. Bob holding
// Ada's token moves nothing.
func TestConfirmEmailChangeRefusesAnotherAccountsToken(t *testing.T) {
	srv, _, mailer := emailChangeServer(t)
	ada := registerTokens(t, srv, `{"username":"ada","email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)
	bob := registerTokens(t, srv, `{"username":"bob","email":"`+fixtureOtherEmail+`","password":"`+fixtureCurrentPassword+`"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath,
		changeEmailBody(fixtureCurrentPassword, fixtureNewEmail), ada.Token); rec.Code != http.StatusAccepted {
		t.Fatalf("ada's request = %d, want 202", rec.Code)
	}
	token := mailer.changeToken

	rec := sendJSONAuth(srv, http.MethodPost, emailChangePath+"/confirm",
		fmt.Sprintf(`{"token":%q}`, token), bob.Token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bob confirming ada's token = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if got := meEmail(t, srv, bob.Token); got != fixtureOtherEmail {
		t.Errorf("bob's address moved to %q", got)
	}
	if got := meEmail(t, srv, ada.Token); got != fixtureOldEmail {
		t.Errorf("ada's address moved to %q", got)
	}
	// Ada's own token is untouched by the failed attempt.
	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath+"/confirm",
		fmt.Sprintf(`{"token":%q}`, token), ada.Token); rec.Code != http.StatusOK {
		t.Errorf("ada's own confirmation after bob's attempt = %d, want 200", rec.Code)
	}
}

// TestEmailChangeResendAndCancel: resend supersedes, cancel kills, and both are
// honest 404s when there is nothing pending.
func TestEmailChangeResendAndCancel(t *testing.T) {
	srv, _, mailer := emailChangeServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)

	if rec := sendJSONAuth(srv, http.MethodDelete, emailChangePath, "", reg.Token); rec.Code != http.StatusNotFound {
		t.Errorf("cancel with nothing pending = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath+"/resend", "", reg.Token); rec.Code != http.StatusNotFound {
		t.Errorf("resend with nothing pending = %d, want 404", rec.Code)
	}

	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath,
		changeEmailBody(fixtureCurrentPassword, fixtureNewEmail), reg.Token); rec.Code != http.StatusAccepted {
		t.Fatalf("request = %d, want 202", rec.Code)
	}
	first := mailer.changeToken
	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath+"/resend", "", reg.Token); rec.Code != http.StatusAccepted {
		t.Fatalf("resend = %d, want 202", rec.Code)
	}
	second := mailer.changeToken
	if first == second {
		t.Fatal("resend reused the token instead of superseding it")
	}
	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath+"/confirm",
		fmt.Sprintf(`{"token":%q}`, first), reg.Token); rec.Code != http.StatusBadRequest {
		t.Errorf("the pre-resend token = %d, want 400", rec.Code)
	}

	if rec := sendJSONAuth(srv, http.MethodDelete, emailChangePath, "", reg.Token); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d, want 204", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, emailChangePath+"/confirm",
		fmt.Sprintf(`{"token":%q}`, second), reg.Token); rec.Code != http.StatusBadRequest {
		t.Errorf("a cancelled token still confirmed: %d", rec.Code)
	}
	if got := meEmail(t, srv, reg.Token); got != fixtureOldEmail {
		t.Errorf("address after cancel = %q", got)
	}
}

// TestEmailChangeRoutesRequireAuthentication — every one of the five, including
// the confirmation: a mail token alone must not move anybody's address.
func TestEmailChangeRoutesRequireAuthentication(t *testing.T) {
	srv, _, _ := emailChangeServer(t)
	registerTokens(t, srv, `{"username":"ada","email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, emailChangePath, changeEmailBody(fixtureCurrentPassword, fixtureNewEmail)},
		{http.MethodGet, emailChangePath, ""},
		{http.MethodDelete, emailChangePath, ""},
		{http.MethodPost, emailChangePath + "/resend", ""},
		{http.MethodPost, emailChangePath + "/confirm", `{"token":"token-fixture-1"}`},
	} {
		rec := sendJSONAuth(srv, tc.method, tc.path, tc.body, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// TestEmailChangeAuditNamesTheRuleNotTheAddress: the audit trail records that a
// change was requested, refused or confirmed, and by whom — never either
// address, which is the sensitive-key discipline this repo enforces everywhere
// else.
func TestEmailChangeAuditNamesTheRuleNotTheAddress(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	mailer := &captureResetMailer{}
	svc := auth.NewService(repo, issuer, 720*time.Hour, auth.WithMailer(mailer))
	srv := New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute), WithLogger(logger))

	reg := registerTokens(t, srv, `{"username":"ada","email":"`+fixtureOldEmail+`","password":"`+fixtureCurrentPassword+`"}`)
	sendJSONAuth(srv, http.MethodPost, emailChangePath, changeEmailBody("not-the-password", fixtureNewEmail), reg.Token)
	sendJSONAuth(srv, http.MethodPost, emailChangePath, changeEmailBody(fixtureCurrentPassword, fixtureNewEmail), reg.Token)
	sendJSONAuth(srv, http.MethodPost, emailChangePath+"/confirm",
		fmt.Sprintf(`{"token":%q}`, mailer.changeToken), reg.Token)

	events := auditEvents(t, &buf)
	if e := findAudit(events, observability.ActionEmailChangeRequest, observability.ResultFailure); e == nil {
		t.Error("no failure audit event for the refused request")
	} else if e["reason"] != "invalid_password" {
		t.Errorf("refusal reason = %v, want invalid_password", e["reason"])
	}
	if findAudit(events, observability.ActionEmailChangeRequest, observability.ResultSuccess) == nil {
		t.Error("no success audit event for the accepted request")
	}
	if findAudit(events, observability.ActionEmailChangeConfirm, observability.ResultSuccess) == nil {
		t.Error("no success audit event for the confirmation")
	}
	// Neither address, and no token, may appear anywhere in the captured log.
	for _, needle := range []string{fixtureOldEmail, fixtureNewEmail, mailer.changeToken} {
		if needle != "" && strings.Contains(buf.String(), needle) {
			t.Errorf("the audit log contains %q", needle)
		}
	}
}
