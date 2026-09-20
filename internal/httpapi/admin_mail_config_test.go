package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/mailconfig"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const mailConfigPath = "/api/v1/admin/mail-config"

// fakeMailConfig is the mail-configuration service as this layer sees it. It is
// a stand-in rather than the real service over a fake repository because these
// tests are about the HTTP contract — the authz gates, the typed errors, the
// audit record and, above all, what does and does not appear in a response.
type fakeMailConfig struct {
	state     mailconfig.State
	available bool
	source    string
	saveErr   error
	saved     []mailconfig.Input
	resets    int
	existed   bool
	report    mailconfig.ProbeReport
}

func (f *fakeMailConfig) Status() mailconfig.State { return f.state }
func (f *fakeMailConfig) Available() bool          { return f.available }
func (f *fakeMailConfig) Source() string {
	if f.source == "" {
		return mailconfig.SourceNone
	}
	return f.source
}

func (f *fakeMailConfig) Save(_ context.Context, in mailconfig.Input, _ uuid.UUID) (mailconfig.SaveResult, error) {
	f.saved = append(f.saved, in)
	if f.saveErr != nil {
		return mailconfig.SaveResult{}, f.saveErr
	}
	return mailconfig.SaveResult{
		Transport:     in.Transport,
		ChangedFields: []string{"smtp.host", "transport"},
		SecretChanged: true,
	}, nil
}

func (f *fakeMailConfig) Reset(context.Context, uuid.UUID) (bool, error) {
	f.resets++
	return f.existed, nil
}

func (f *fakeMailConfig) Probe(context.Context) mailconfig.ProbeReport { return f.report }

// mailConfigServer is an auth-enabled server with the mail-configuration
// service wired and its logs captured, so one test can assert the response, the
// log output and the audit row together.
func mailConfigServer(t *testing.T, buf *bytes.Buffer, svc mailConfigProvider) (*Server, *authFakeRepo) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(repo, issuer, 720*time.Hour)
	return New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithMailConfigService(svc),
		WithLogger(logger),
	), repo
}

// setRole rewrites an account's role directly in the fake store. The auth
// middleware reads the role off the account ROW on every request (not off the
// token's copy of it), so this is exactly what a promotion through the admin
// route would leave behind — without needing the admin service mounted.
func setRole(t *testing.T, repo *authFakeRepo, email, role string) {
	t.Helper()
	u, ok := repo.users[strings.ToLower(email)]
	if !ok {
		t.Fatalf("no account %q in the fake store", email)
	}
	u.Role = role
	repo.users[strings.ToLower(email)] = u
}

// The three verbs are admin-only. An unauthenticated caller is refused before
// the handler, and a MODERATOR — the role most likely to be handed to someone
// who should not hold the instance's relay password — is refused too.
func TestMailConfigIsAdminOnly(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{}
	srv, repo := mailConfigServer(t, &buf, svc)

	// The first account claims the owner; a second is promoted to moderator —
	// the role most likely to be handed to someone who should not hold the
	// instance's relay password.
	registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	modTokens := registerTokens(t, srv, `{"username":"moses","email":"moses@example.test","password":"supersecret"}`)
	setRole(t, repo, "moses@example.test", "moderator")

	for _, verb := range []struct {
		method string
		body   string
	}{
		{http.MethodGet, ""},
		{http.MethodPut, `{"transport":"resend","from_address":"a@b.test","resend":{"api_key":"k"}}`},
		{http.MethodDelete, ""},
	} {
		t.Run(verb.method+"/anonymous", func(t *testing.T) {
			if rec := doJSON(srv, verb.method, mailConfigPath, "", verb.body); rec.Code != http.StatusUnauthorized {
				t.Errorf("anonymous %s = %d, want 401; body=%s", verb.method, rec.Code, rec.Body.String())
			}
		})
		t.Run(verb.method+"/moderator", func(t *testing.T) {
			if rec := doJSON(srv, verb.method, mailConfigPath, modTokens.Token, verb.body); rec.Code != http.StatusForbidden {
				t.Errorf("moderator %s = %d, want 403; body=%s", verb.method, rec.Code, rec.Body.String())
			}
		})
	}
	if len(svc.saved) != 0 || svc.resets != 0 {
		t.Errorf("the service was reached despite the refusals: saves=%d resets=%d", len(svc.saved), svc.resets)
	}
}

// A save with no KEK is a typed 409 an admin can act on, not a generic failure
// and not a silently weaker store. The refusal is audited.
func TestMailConfigSaveWithoutAKEKIs409(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{saveErr: mailconfig.ErrSecretsKeyMissing}
	srv, _ := mailConfigServer(t, &buf, svc)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := doJSON(srv, http.MethodPut, mailConfigPath, tok,
		`{"transport":"resend","from_address":"a@b.test","resend":{"api_key":"re_key"}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "mail_secrets_key_missing" {
		t.Errorf("code = %q, want mail_secrets_key_missing", code)
	}
	// The message names the remedy. A 409 that says only "conflict" sends an
	// operator to read source code.
	if body := rec.Body.String(); !strings.Contains(body, "MFA_KEY_KEK") {
		t.Errorf("the refusal does not name the variable to set:\n%s", body)
	}
	if ev := findAudit(auditEvents(t, &buf), observability.ActionAdminMailConfigUpdate, observability.ResultFailure); ev == nil {
		t.Error("a refused save was not audited")
	}
}

// A 422 carries the DOTTED paths the transport builder produced — that is what
// the admin form binds its inputs to, and inventing a second vocabulary here
// would make the two disagree.
func TestMailConfigValidationFieldsAreDotted(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{saveErr: &mail.ValidationError{Fields: []mail.FieldError{
		{Field: "smtp.host", Msg: "required"},
		{Field: "mailgun.api_key", Msg: "required"},
	}}}
	srv, _ := mailConfigServer(t, &buf, svc)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := doJSON(srv, http.MethodPut, mailConfigPath, tok, `{"transport":"smtp","from_address":"a@b.test"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var env ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := make([]string, 0, len(env.Error.Fields))
	for _, f := range env.Error.Fields {
		got = append(got, f.Field)
	}
	if strings.Join(got, ",") != "smtp.host,mailgun.api_key" {
		t.Errorf("fields = %v, want the dotted paths carried through unchanged", got)
	}
}

// An unknown field is refused rather than ignored. A panel that mis-spells
// "password" would otherwise save a configuration with no credential and be
// told it succeeded.
func TestMailConfigRejectsUnknownFields(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{}
	srv, _ := mailConfigServer(t, &buf, svc)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := doJSON(srv, http.MethodPut, mailConfigPath, tok,
		`{"transport":"smtp","from_address":"a@b.test","smtp":{"host":"h","port":25,"encryption":"none","passwrod":"typo"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unknown field; body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.saved) != 0 {
		t.Error("a body with an unknown field reached the service")
	}
}

// The save is audited with the transport, the changed field NAMES and whether
// the credential was replaced — and with no value of any kind.
func TestMailConfigSaveIsAudited(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{}
	srv, _ := mailConfigServer(t, &buf, svc)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	const sentinel = "SENTINEL-RESEND-KEY-4b1c"
	rec := doJSON(srv, http.MethodPut, mailConfigPath, tok,
		`{"transport":"resend","from_address":"a@b.test","resend":{"api_key":"`+sentinel+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	ev := findAudit(auditEvents(t, &buf), observability.ActionAdminMailConfigUpdate, observability.ResultSuccess)
	if ev == nil {
		t.Fatal("the save was not audited")
	}
	if ev["actor_id"] == nil || ev["actor_id"] == "" {
		t.Error("the audit row carries no actor")
	}
	// The whole captured log, audit rows included, must not contain the key.
	if strings.Contains(buf.String(), sentinel) {
		t.Errorf("the credential reached the log:\n%s", buf.String())
	}
	if strings.Contains(rec.Body.String(), sentinel) {
		t.Errorf("the PUT echoed the credential back:\n%s", rec.Body.String())
	}
}

// DELETE is audited too — the review's point: prove the refusals and the
// destructive verb, not just the happy path of the write.
func TestMailConfigResetIsAudited(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{existed: true}
	srv, _ := mailConfigServer(t, &buf, svc)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if rec := doJSON(srv, http.MethodDelete, mailConfigPath, tok, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if svc.resets != 1 {
		t.Errorf("resets = %d, want 1", svc.resets)
	}
	ev := findAudit(auditEvents(t, &buf), observability.ActionAdminMailConfigReset, observability.ResultSuccess)
	if ev == nil {
		t.Fatal("the reset was not audited")
	}
	if ev["reason"] != "reverted_to_environment" {
		t.Errorf("reason = %v, want reverted_to_environment", ev["reason"])
	}
}

// The GET renders the state with *_set booleans and never a credential. One
// sentinel value, driven through the real service so the rendering is the real
// one, checked against the whole response body.
func TestMailConfigGetNeverEchoesTheStoredSecret(t *testing.T) {
	const sentinel = "SENTINEL-SMTP-PASSWORD-77c2"
	var buf bytes.Buffer

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("kek: %v", err)
	}
	cipher, err := secretbox.NewCipher(kek)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	real := mailconfig.NewService(newMailConfigMemoryRepo(), cipher, mailconfig.EnvConfig{
		Enabled: true, Host: "env-relay.test", Port: 587, From: "env@vidra.test",
		// The environment relay's credentials, which must never be rendered
		// anywhere either — they are on the absolute never-list.
		Username: "SENTINEL-ENV-USERNAME", Password: "SENTINEL-ENV-PASSWORD",
	})
	if _, err := real.Save(context.Background(), mailconfig.Input{
		Transport: mail.KindSMTP, FromAddress: "no-reply@vidra.test",
		SMTP: &mailconfig.SMTPInput{
			Host: "relay.example.test", Port: 587, Username: "mailer",
			Encryption: "starttls", Password: &[]string{sentinel}[0],
		},
	}, uuid.New()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	srv, _ := mailConfigServer(t, &buf, real)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := getWithAuth(srv, mailConfigPath, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{sentinel, "SENTINEL-ENV-USERNAME", "SENTINEL-ENV-PASSWORD"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("GET leaks %q:\n%s", forbidden, body)
		}
	}
	if strings.Contains(buf.String(), sentinel) {
		t.Errorf("the credential reached the log:\n%s", buf.String())
	}
	var state mailconfig.State
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if state.Config == nil || !state.Config.SMTP.PasswordSet {
		t.Fatalf("password_set is not reported: %+v", state.Config)
	}
	if state.SecretStatus != mailconfig.SecretStatusOK || state.Source != mailconfig.SourceDatabase {
		t.Errorf("state = %+v, want an ok secret on the database source", state)
	}
	// The environment block reports coordinates, never credentials.
	if !state.Environment.Configured || state.Environment.Host != "env-relay.test" {
		t.Errorf("environment = %+v, want the env relay described", state.Environment)
	}
}

// newMailConfigMemoryRepo is a one-row in-memory mail_config table, so a test
// in this package can drive the REAL service without a database.
func newMailConfigMemoryRepo() mailconfig.Repository { return &mailConfigMemoryRepo{} }

// --- /admin/system + the test-send classification -----------------------------

// A reachable provider whose send-only key cannot be proven must not read as
// `down` (it would condemn the recommended setup) and must not read as a bare
// `ok` (nobody has proved anything). It is degraded with a sentence that says
// which of the two it is.
func TestMailProbeStatusUnverifiableIsDegradedNotDown(t *testing.T) {
	got := mailProbeStatus(mailconfig.ProbeReport{
		Source: mailconfig.SourceDatabase, Kind: mail.KindResend,
		Result: mail.ProbeResult{Verified: false, Encrypted: true},
	})
	if got.Status != "degraded" {
		t.Errorf("status = %q, want degraded", got.Status)
	}
	if !strings.Contains(got.Error, "test message") {
		t.Errorf("the note does not point at the only check that settles it: %q", got.Error)
	}
}

// A verified handshake over an UNENCRYPTED session is the finding the transport
// review named: the relay works, and every token crosses the wire in the clear.
// Neither `ok` nor `down` is the honest answer.
func TestMailProbeStatusCleartextIsDegraded(t *testing.T) {
	got := mailProbeStatus(mailconfig.ProbeReport{
		Source: mailconfig.SourceEnvironment, Kind: mail.KindSMTP,
		Result:    mail.ProbeResult{Verified: true, Encrypted: false},
		Cleartext: true,
	})
	if got.Status != "degraded" {
		t.Errorf("status = %q, want degraded", got.Status)
	}
	if !strings.Contains(got.Error, "NOT ENCRYPTED") {
		t.Errorf("the note does not say the session is unencrypted: %q", got.Error)
	}
	// The same probe on an ENCRYPTED session is a plain ok.
	ok := mailProbeStatus(mailconfig.ProbeReport{
		Source: mailconfig.SourceEnvironment, Kind: mail.KindSMTP,
		Result: mail.ProbeResult{Verified: true, Encrypted: true},
	})
	if ok.Status != "ok" || ok.Error != "" {
		t.Errorf("an encrypted verified relay = %+v, want a clean ok", ok)
	}
}

// connect_failed on a submission port is the single most common self-hosting
// failure there is, and it is indistinguishable from a wrong hostname unless
// the page says so.
func TestMailProbeStatusNamesTheBlockedPort(t *testing.T) {
	got := mailProbeStatus(mailconfig.ProbeReport{
		Source: mailconfig.SourceDatabase, Kind: mail.KindSMTP,
		Result: mail.ProbeResult{Err: &mail.SendError{Reason: mail.ReasonConnectFailed, Port: 587}},
	})
	if got.Status != "down" {
		t.Errorf("status = %q, want down", got.Status)
	}
	if !strings.Contains(got.Error, "587") || !strings.Contains(got.Error, "2525") {
		t.Errorf("the note does not name the port or the remedy: %q", got.Error)
	}
}

// Dev capture is reported as degraded, not ok: an instance that delivers
// nothing must not read as healthy on the page an operator checks.
func TestMailProbeStatusDevCaptureIsDegraded(t *testing.T) {
	got := mailProbeStatus(mailconfig.ProbeReport{Source: mailconfig.SourceDevCapture, Kind: mail.KindSMTP})
	if got.Status != "degraded" || !strings.Contains(got.Error, "CAPTURED") {
		t.Errorf("dev capture status = %+v, want a degraded capture note", got)
	}
}

// Nothing configured stays `not_configured`, which never degrades the instance:
// mail off is a supported deployment, not a fault.
func TestMailProbeStatusNotConfigured(t *testing.T) {
	if got := mailProbeStatus(mailconfig.ProbeReport{Source: mailconfig.SourceNone}); got.Status != "not_configured" {
		t.Errorf("status = %q, want not_configured", got.Status)
	}
}

// The 502 carries the classification and the port, and NOT one byte of the
// vendor's own answer — which is where a relay quotes the recipient address
// back and where a provider echoes submitted content.
func TestMailTestFailureCarriesReasonAndPortButNotTheVendorText(t *testing.T) {
	const sentinel = "SENTINEL-VENDOR-BODY-relay-said-550-ops@example.test"
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	failing := &failingTestMailer{err: &mail.SendError{
		Reason: mail.ReasonConnectFailed,
		Port:   587,
		Err:    errors.New(sentinel),
	}}
	cfg := testConfig()
	// The probe mails the instance's own contact address and nowhere else; the
	// caller cannot choose it.
	cfg.InstanceContactEmail = "ops@example.test"
	srv := New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithContactMailer(failing),
		WithLogger(logger),
	)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := doJSON(srv, http.MethodPost, "/api/v1/admin/mail/test", tok, `{}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	var env ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Error.Code != "mail_test_failed" {
		t.Errorf("code = %q, want mail_test_failed", env.Error.Code)
	}
	if env.Error.Reason != string(mail.ReasonConnectFailed) {
		t.Errorf("reason = %q, want %q", env.Error.Reason, mail.ReasonConnectFailed)
	}
	if env.Error.Port != 587 {
		t.Errorf("port = %d, want 587 — without it the panel cannot name a blocked submission port", env.Error.Port)
	}
	// The vendor/relay text is LOG-ONLY. Both halves are asserted: it must be
	// in the log (or the operator has lost their only diagnostic) and in no
	// byte of the response.
	if !strings.Contains(buf.String(), sentinel) {
		t.Errorf("the relay's own answer never reached the server log:\n%s", buf.String())
	}
	if strings.Contains(rec.Body.String(), sentinel) {
		t.Errorf("the relay's own answer reached the response body:\n%s", rec.Body.String())
	}
}

type failingTestMailer struct {
	auth.Mailer
	err error
}

func (f *failingTestMailer) SendTest(context.Context, string) error { return f.err }

// mailConfigMemoryRepo is a one-row mail_config table in memory.
type mailConfigMemoryRepo struct{ row *sqlcgen.MailConfig }

func (m *mailConfigMemoryRepo) GetMailConfig(context.Context) (sqlcgen.MailConfig, error) {
	if m.row == nil {
		return sqlcgen.MailConfig{}, pgx.ErrNoRows
	}
	return *m.row, nil
}

func (m *mailConfigMemoryRepo) UpsertMailConfig(_ context.Context, arg sqlcgen.UpsertMailConfigParams) (sqlcgen.MailConfig, error) {
	m.row = &sqlcgen.MailConfig{
		ID: true, Transport: arg.Transport, FromAddress: arg.FromAddress,
		FromName: arg.FromName, ReplyTo: arg.ReplyTo, Settings: arg.Settings,
		Secret: arg.Secret, UpdatedBy: arg.UpdatedBy, UpdatedAt: time.Now().UTC(),
	}
	return *m.row, nil
}

func (m *mailConfigMemoryRepo) DeleteMailConfig(context.Context) (int64, error) {
	if m.row == nil {
		return 0, nil
	}
	m.row = nil
	return 1, nil
}

// The status page's `smtp` component now probes the ACTIVE transport through
// the configuration service. The id is deliberately unchanged — every operator
// dashboard and runbook keys on it — so this asserts the wiring rather than a
// rename: a probeSMTP that still read boot config would report not_configured
// on an instance sending happily over Resend.
func TestSystemStatusSMTPComponentProbesTheActiveTransport(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{
		available: true,
		source:    mailconfig.SourceDatabase,
		report: mailconfig.ProbeReport{
			Source: mailconfig.SourceDatabase, Kind: mail.KindResend,
			Result: mail.ProbeResult{Verified: true, Encrypted: true},
		},
	}
	srv, _ := mailConfigServer(t, &buf, svc)
	srv.lookPath = ffmpegFound
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	read := func() componentStatus {
		t.Helper()
		rec := getWithAuth(srv, "/api/v1/admin/system", tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("system status = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body systemStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		c, ok := body.Components["smtp"]
		if !ok {
			t.Fatal("the smtp component disappeared from the status page; every operator dashboard keys on that id")
		}
		return c
	}
	if c := read(); c.Status != "ok" {
		t.Errorf("smtp = %+v, want ok for a verified provider", c)
	}

	// The same page, with the provider refusing the credentials.
	svc.report = mailconfig.ProbeReport{
		Source: mailconfig.SourceDatabase, Kind: mail.KindResend,
		Result: mail.ProbeResult{Err: &mail.SendError{Reason: mail.ReasonAuthFailed}},
	}
	if c := read(); c.Status != "down" || !strings.Contains(c.Error, "rejected these credentials") {
		t.Errorf("smtp with a rejected key = %+v, want down with a provider-shaped sentence", c)
	}
}

// The infrastructure page reads the LIVE source too. A deployment configured
// from the panel must not be reported as unconfigured just because
// MAIL_ENABLED is unset in its env file — that is the page contradicting the
// product instead of the other way round.
func TestInfrastructureMailRowReadsTheLiveSource(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{
		available: true,
		source:    mailconfig.SourceDatabase,
		state: mailconfig.State{
			Source: mailconfig.SourceDatabase,
			Config: &mailconfig.Document{Transport: mail.KindResend, FromAddress: "no-reply@vidra.test"},
		},
	}
	srv, _ := mailConfigServer(t, &buf, svc)
	body, _ := infrastructure(t, srv)
	row := featureNamed(t, body, "mail")
	if !row.Enabled || !row.Configured {
		t.Errorf("mail row = %+v, want enabled+configured for a panel-configured transport", row)
	}
	if !strings.Contains(row.Note, "ADMIN PANEL") {
		t.Errorf("note = %q, want it to say where the configuration actually lives", row.Note)
	}
}

// The composer is installed unconditionally now, so "a mailer is wired" no
// longer means "this instance can send". The test button must still answer 503
// on an instance with no mail path — an unconfigured composer's send returns
// nil, and a 202 "sent" there would be the no-op mailer's silent success
// surfaced on the one button that exists to detect it.
func TestMailTestIs503WhenNothingIsConfiguredEvenWithAComposerWired(t *testing.T) {
	var buf bytes.Buffer
	svc := &fakeMailConfig{available: false, source: mailconfig.SourceNone}
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	cfg := testConfig()
	cfg.InstanceContactEmail = "ops@example.test"
	// A composer over a resolver that resolves nothing — exactly what cmd/api
	// installs on an instance with no configured transport.
	composer := mail.NewComposer(mail.ComposerConfig{}, mail.NewStaticResolver(nil, netmail.Address{}, ""))
	srv := New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithMailConfigService(svc),
		WithContactMailer(composer),
		WithLogger(logger),
	)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := doJSON(srv, http.MethodPost, "/api/v1/admin/mail/test", tok, `{}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — a 202 here is a claim that a message was sent when none was; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "mail_not_configured" {
		t.Errorf("code = %q, want mail_not_configured", code)
	}

	// With a transport configured, the same wiring answers the button.
	svc.available = true
	svc.source = mailconfig.SourceDatabase
	if rec := doJSON(srv, http.MethodPost, "/api/v1/admin/mail/test", tok, `{}`); rec.Code != http.StatusAccepted {
		t.Errorf("status with a configured transport = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}
