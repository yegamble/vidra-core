package mailconfig

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is the mail_config table in memory: one row or none.
type fakeRepo struct {
	row     *sqlcgen.MailConfig
	upserts int
	deletes int
	getErr  error
}

func (f *fakeRepo) GetMailConfig(context.Context) (sqlcgen.MailConfig, error) {
	if f.getErr != nil {
		return sqlcgen.MailConfig{}, f.getErr
	}
	if f.row == nil {
		return sqlcgen.MailConfig{}, pgx.ErrNoRows
	}
	return *f.row, nil
}

func (f *fakeRepo) UpsertMailConfig(_ context.Context, arg sqlcgen.UpsertMailConfigParams) (sqlcgen.MailConfig, error) {
	f.upserts++
	f.row = &sqlcgen.MailConfig{
		ID: true, Transport: arg.Transport, FromAddress: arg.FromAddress,
		FromName: arg.FromName, ReplyTo: arg.ReplyTo, Settings: arg.Settings,
		Secret: arg.Secret, UpdatedBy: arg.UpdatedBy,
	}
	return *f.row, nil
}

func (f *fakeRepo) DeleteMailConfig(context.Context) (int64, error) {
	f.deletes++
	if f.row == nil {
		return 0, nil
	}
	f.row = nil
	return 1, nil
}

func testCipher(t *testing.T) *secretbox.Cipher {
	t.Helper()
	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("kek: %v", err)
	}
	c, err := secretbox.NewCipher(kek)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return c
}

func envRelay() EnvConfig {
	return EnvConfig{Enabled: true, Host: "relay.env.test", Port: 587, From: "env@vidra.test"}
}

func smtpInput(password *string) Input {
	return Input{
		Transport:   mail.KindSMTP,
		FromAddress: "no-reply@vidra.test",
		FromName:    "Vidra",
		SMTP: &SMTPInput{
			Host: "relay.example.test", Port: 587, Username: "mailer",
			Encryption: "starttls", Password: password,
		},
	}
}

func ptr(s string) *string { return &s }

func mustSave(t *testing.T, s *Service, in Input) SaveResult {
	t.Helper()
	res, err := s.Save(context.Background(), in, uuid.New())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return res
}

func fieldsOf(t *testing.T, err error) []string {
	t.Helper()
	var ve *mail.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %T (%v), want *mail.ValidationError", err, err)
	}
	out := make([]string, 0, len(ve.Fields))
	for _, f := range ve.Fields {
		out = append(out, f.Field)
	}
	return out
}

// The document beats the environment, and a reset hands the instance back to
// it. This is the whole precedence rule in one test, because it is the rule an
// operator's mail actually depends on.
func TestDocumentOverridesEnvironmentAndResetRevertsToIt(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, testCipher(t), envRelay())
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := svc.Source(); got != SourceEnvironment {
		t.Fatalf("source with no document = %q, want %q", got, SourceEnvironment)
	}

	mustSave(t, svc, smtpInput(ptr("relay-pass")))
	if got := svc.Source(); got != SourceDatabase {
		t.Fatalf("source after save = %q, want %q", got, SourceDatabase)
	}
	if st := svc.Status(); st.Config == nil || st.Config.SMTP.Host != "relay.example.test" {
		t.Fatalf("status config = %+v, want the saved relay", st.Config)
	}
	// The environment block still reports what a reset would revert TO — and
	// never the environment relay's credentials.
	if st := svc.Status(); !st.Environment.Configured || st.Environment.Host != "relay.env.test" {
		t.Errorf("environment block = %+v, want the env relay described", st.Environment)
	}

	if _, err := svc.Reset(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got := svc.Source(); got != SourceEnvironment {
		t.Fatalf("source after reset = %q, want %q", got, SourceEnvironment)
	}
	if st := svc.Status(); st.Config != nil {
		t.Errorf("config after reset = %+v, want null", st.Config)
	}
}

// A stored credential this process cannot open is the KEK-was-rotated case. It
// must be configured-but-broken, never "unconfigured": the silent alternative
// sends the instance's password resets through the environment relay, which is
// somewhere the operator did not choose.
func TestUndecryptableSecretIsConfiguredAndBroken(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, testCipher(t), envRelay())
	mustSave(t, svc, smtpInput(ptr("relay-pass")))

	// A DIFFERENT process, with a different KEK, over the same row.
	rotated := NewService(repo, testCipher(t), envRelay())
	if err := rotated.Load(context.Background()); err != nil {
		t.Fatalf("Load with a rotated KEK returned an error; it must report the state instead: %v", err)
	}
	st := rotated.Status()
	if st.SecretStatus != SecretStatusUndecryptable {
		t.Errorf("secret_status = %q, want %q", st.SecretStatus, SecretStatusUndecryptable)
	}
	if st.Source != SourceDatabase {
		t.Errorf("source = %q, want %q — falling back to the environment relay would redirect this instance's mail", st.Source, SourceDatabase)
	}
	if !rotated.Available() {
		t.Error("Available() = false; the instance IS configured, it is broken")
	}
	transport, _, _, ok := rotated.Current()
	if !ok {
		t.Fatal("Current() reported no transport")
	}
	err := transport.Send(context.Background(), mail.Message{
		From: mail.Message{}.From, To: "ada@example.test",
	})
	if mail.ReasonOf(err) != mail.ReasonSecretUndecryptable {
		t.Errorf("send reason = %q, want %q", mail.ReasonOf(err), mail.ReasonSecretUndecryptable)
	}
	// And the probe says the same thing rather than reporting a healthy relay.
	if report := rotated.Probe(context.Background()); mail.ReasonOf(report.Result.Err) != mail.ReasonSecretUndecryptable {
		t.Errorf("probe = %+v, want a secret_undecryptable failure", report)
	}
}

// Sealed or not at all. Without a KEK a credential is refused rather than
// written in the clear into a queryable table and every database dump.
func TestSaveWithoutAKEKRefusesACredentialButAcceptsAnAnonymousRelay(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, nil, EnvConfig{})

	_, err := svc.Save(context.Background(), smtpInput(ptr("relay-pass")), uuid.New())
	if !errors.Is(err, ErrSecretsKeyMissing) {
		t.Fatalf("Save with a credential and no KEK = %v, want ErrSecretsKeyMissing", err)
	}
	if repo.upserts != 0 {
		t.Fatal("the row was written despite the refusal")
	}
	if st := svc.Status(); st.SecretsAvailable {
		t.Error("secrets_available = true with no KEK; the panel must be able to disable the inputs in advance")
	}

	anon := smtpInput(nil)
	anon.SMTP.Username = ""
	if _, err := svc.Save(context.Background(), anon, uuid.New()); err != nil {
		t.Fatalf("an anonymous relay needs no credential and must still save: %v", err)
	}
	if st := svc.Status(); st.SecretStatus != SecretStatusNone || st.Config.SMTP.PasswordSet {
		t.Errorf("anonymous relay state = %+v", st)
	}
}

// Omitting a secret keeps the stored one; switching transport without a new one
// is a 422, because the stored secret belongs to the provider it was typed for.
func TestSecretSemantics(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, testCipher(t), EnvConfig{})
	mustSave(t, svc, smtpInput(ptr("relay-pass")))

	// Omitted, same transport, same relay: kept, and not reported as a change.
	// Port, username and encryption all travel to the SAME server, so none of
	// them costs the admin the password again.
	changed := smtpInput(nil)
	changed.SMTP.Port = 2525
	res := mustSave(t, svc, changed)
	if res.SecretChanged {
		t.Error("secret_changed = true for a save that did not touch the credential")
	}
	if strings.Join(res.ChangedFields, ",") != "smtp.port" {
		t.Errorf("changed fields = %v, want [smtp.port]", res.ChangedFields)
	}
	if st := svc.Status(); !st.Config.SMTP.PasswordSet {
		t.Error("the stored password was dropped by a save that omitted it")
	}

	// The same relay spelled differently is the same relay: hostnames are
	// case-insensitive and a stray space is a typo, not a new server.
	respelled := smtpInput(nil)
	respelled.SMTP.Port = 2525
	respelled.SMTP.Host = "  RELAY.Example.Test "
	if _, err := svc.Save(context.Background(), respelled, uuid.New()); err != nil {
		t.Fatalf("a re-spelling of the same host must keep the credential: %v", err)
	}

	// A CHANGED relay address with the password omitted must NOT re-point the
	// stored credential: the sealed column exists so an admin session cannot
	// read the credential back, and unsealing it into a relay the caller chose
	// would hand it over one AUTH PLAIN at a time. Re-entering it is the cost.
	storedSecret, storedHost := repo.row.Secret, decodeSettings(repo.row.Settings).Host
	repointed := smtpInput(nil)
	repointed.SMTP.Port = 2525
	repointed.SMTP.Host = "relay.attacker.test"
	_, err := svc.Save(context.Background(), repointed, uuid.New())
	if got := fieldsOf(t, err); strings.Join(got, ",") != "smtp.password" {
		t.Errorf("fields for a re-pointed relay = %v, want [smtp.password]", got)
	}
	if repo.row.Secret != storedSecret {
		t.Error("the stored credential was rewritten by a save that was refused")
	}
	if got := decodeSettings(repo.row.Settings).Host; got != storedHost {
		t.Errorf("stored host = %q, want the save to have been refused at %q", got, storedHost)
	}
	// The refusal is about KEEPING a secret, not about changing a host: supply
	// the password and the same move is an ordinary save.
	moved := smtpInput(ptr("new-relay-pass"))
	moved.SMTP.Host = "relay.attacker.test"
	if res := mustSave(t, svc, moved); !res.SecretChanged {
		t.Error("secret_changed = false after the password was re-entered")
	}

	// Empty string on smtp.password clears it.
	if _, err := svc.Save(context.Background(), func() Input {
		in := smtpInput(ptr(""))
		in.SMTP.Username = ""
		return in
	}(), uuid.New()); err != nil {
		t.Fatalf("clearing an SMTP password must be allowed: %v", err)
	}
	if st := svc.Status(); st.Config.SMTP.PasswordSet {
		t.Error("password_set = true after an explicit clear")
	}

	// Switching transport with no new secret names the field that is missing.
	_, err = svc.Save(context.Background(), Input{
		Transport: mail.KindResend, FromAddress: "no-reply@vidra.test",
		Resend: &APIKeyInput{},
	}, uuid.New())
	if got := fieldsOf(t, err); strings.Join(got, ",") != "resend.api_key" {
		t.Errorf("fields = %v, want [resend.api_key]", got)
	}
	// And an empty string is a clear, which no provider accepts.
	_, err = svc.Save(context.Background(), Input{
		Transport: mail.KindResend, FromAddress: "no-reply@vidra.test",
		Resend: &APIKeyInput{APIKey: ptr("")},
	}, uuid.New())
	if got := fieldsOf(t, err); strings.Join(got, ",") != "resend.api_key" {
		t.Errorf("fields for an explicit clear = %v, want [resend.api_key]", got)
	}
}

// A VENDOR transport pins its own hosts, so the settings an admin can change on
// one never decide where the key is sent: Mailgun's domain and region select
// between api.mailgun.net and api.eu.mailgun.net and nothing else. Keeping the
// stored key across such a change is therefore safe, and making the admin
// re-type it would be friction bought with no security.
func TestAVendorSettingChangeKeepsTheStoredKey(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, testCipher(t), EnvConfig{})
	base := Input{
		Transport: mail.KindMailgun, FromAddress: "no-reply@vidra.test",
		Mailgun: &MailgunInput{Domain: "mail.vidra.test", Region: "us", APIKey: ptr("key-1")},
	}
	mustSave(t, svc, base)

	moved := base
	moved.Mailgun = &MailgunInput{Domain: "mail2.vidra.test", Region: "eu"} // key omitted
	res := mustSave(t, svc, moved)
	if res.SecretChanged {
		t.Error("secret_changed = true for a save that did not touch the key")
	}
	if st := svc.Status(); st.Config == nil || !st.Config.Mailgun.APIKeySet {
		t.Errorf("stored key was dropped by a domain/region change: %+v", st.Config)
	}
}

// The sender identity ends up in a message header, so it is REJECTED rather
// than sanitized, with the dotted field name the form binds to.
func TestIdentityValidation(t *testing.T) {
	svc := NewService(&fakeRepo{}, testCipher(t), EnvConfig{})
	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"no sender", Input{Transport: mail.KindSMTP}, "from_address"},
		{"sender that is not an address", func() Input {
			in := smtpInput(ptr("p"))
			in.FromAddress = "not an address"
			return in
		}(), "from_address"},
		{"two senders", func() Input {
			in := smtpInput(ptr("p"))
			in.FromAddress = "a@b.test,evil@x.test"
			return in
		}(), "from_address"},
		{"display name carrying a header break", func() Input {
			in := smtpInput(ptr("p"))
			in.FromName = "Vidra\r\nBcc: evil@x.test"
			return in
		}(), "from_name"},
		{"reply-to that is not an address", func() Input {
			in := smtpInput(ptr("p"))
			in.ReplyTo = "ops"
			return in
		}(), "reply_to"},
		// A NAME-ADDR parses, so it used to save — and then went to MAIL FROM
		// verbatim as `MAIL FROM:<Vidra <a@b.test>>`, which every relay answers
		// 501 to, on every message, with the probe still reading ok. The display
		// name belongs in from_name.
		{"sender carrying a display name", func() Input {
			in := smtpInput(ptr("p"))
			in.FromAddress = "Vidra <a@b.test>"
			return in
		}(), "from_address"},
		{"sender in angle brackets", func() Input {
			in := smtpInput(ptr("p"))
			in.FromAddress = "<a@b.test>"
			return in
		}(), "from_address"},
		{"reply-to carrying a display name", func() Input {
			in := smtpInput(ptr("p"))
			in.ReplyTo = "Ops <ops@vidra.test>"
			return in
		}(), "reply_to"},
		{"reply-to in angle brackets", func() Input {
			in := smtpInput(ptr("p"))
			in.ReplyTo = "<ops@vidra.test>"
			return in
		}(), "reply_to"},
		{"unknown transport", Input{Transport: "sendgrid", FromAddress: "a@b.test"}, "transport"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Save(context.Background(), tc.in, uuid.New())
			got := fieldsOf(t, err)
			found := false
			for _, f := range got {
				if f == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("fields = %v, want one of them to be %q", got, tc.want)
			}
		})
	}
}

// The transport builder's field paths reach the caller unchanged — this layer
// must not invent a second, possibly disagreeing, set of rules.
func TestTransportValidationFieldsAreCarriedThrough(t *testing.T) {
	svc := NewService(&fakeRepo{}, testCipher(t), EnvConfig{})
	_, err := svc.Save(context.Background(), Input{
		Transport:   mail.KindSMTP,
		FromAddress: "no-reply@vidra.test",
		SMTP:        &SMTPInput{Host: "", Port: 0, Encryption: "auto", Password: ptr("p"), Username: "u"},
	}, uuid.New())
	got := strings.Join(fieldsOf(t, err), ",")
	if !strings.Contains(got, "smtp.host") || !strings.Contains(got, "smtp.port") || !strings.Contains(got, "smtp.encryption") {
		t.Errorf("fields = %s, want the transport builder's own dotted paths", got)
	}
}

// Save announces the change so every other replica reloads. Without it a
// transport switch takes effect on exactly one process and the rest — including
// the workers — keep sending through the old one until restart.
func TestSaveAndResetAnnounceTheChange(t *testing.T) {
	bumps := 0
	svc := NewService(&fakeRepo{}, testCipher(t), EnvConfig{},
		WithVersionBump(func(context.Context) error { bumps++; return nil }))
	mustSave(t, svc, smtpInput(ptr("relay-pass")))
	if bumps != 1 {
		t.Errorf("bumps after a save = %d, want 1", bumps)
	}
	if _, err := svc.Reset(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if bumps != 2 {
		t.Errorf("bumps after a reset = %d, want 2", bumps)
	}
}

// Dev capture wins over everything for REPORTING and counts as an outbound
// path: answering otherwise would 503 the mail-test button on every developer
// machine and hide the fact that nothing is being delivered.
func TestDevCaptureWinsAndCounts(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil, EnvConfig{}, WithDevCapture(true))
	if !svc.Available() {
		t.Error("Available() = false with the capture seam on")
	}
	if got := svc.Source(); got != SourceDevCapture {
		t.Errorf("source = %q, want %q", got, SourceDevCapture)
	}
	if got := svc.Probe(context.Background()).Source; got != SourceDevCapture {
		t.Errorf("probe source = %q, want %q", got, SourceDevCapture)
	}
}

// Nothing configured is not an error: the composer treats it exactly as the
// historical no-op mailer did, and every caller that must not proceed without
// delivery checks the capability first.
func TestNoMailPathAtAll(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil, EnvConfig{})
	if svc.Available() {
		t.Error("Available() = true with no document, no env relay and no capture")
	}
	if _, _, _, ok := svc.Current(); ok {
		t.Error("Current() resolved a transport out of nothing")
	}
	st := svc.Status()
	if st.Source != SourceNone || st.Config != nil || st.Environment.Configured {
		t.Errorf("state = %+v, want the empty shape", st)
	}
	if got := svc.Probe(context.Background()); got.Kind != "" {
		t.Errorf("probe kind = %q, want empty", got.Kind)
	}
}

// The probe is cached. /admin/system is a poll target and the probe is now an
// outbound call — a relay handshake, or a GET that counts against a provider's
// rate limit — so an open admin page must not be able to throttle the account.
func TestProbeIsCachedAndInvalidatedByLoad(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, testCipher(t), EnvConfig{})
	mustSave(t, svc, smtpInput(ptr("relay-pass")))

	counted := &countingTransport{}
	svc.cur.Load().transport = counted
	// Re-store so the pointer read sees the substituted transport.
	snap := *svc.cur.Load()
	snap.transport = counted
	svc.replace(&snap)

	for range 5 {
		svc.Probe(context.Background())
	}
	if counted.probes != 1 {
		t.Errorf("transport probed %d times across five page loads, want 1", counted.probes)
	}
	// A configuration change must never be masked by the cache.
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc.replace(&snap)
	svc.Probe(context.Background())
	if counted.probes != 2 {
		t.Errorf("probes after a reload = %d, want 2 — a reload must invalidate the cache", counted.probes)
	}
}

type countingTransport struct{ probes int }

func (c *countingTransport) Kind() string                             { return mail.KindSMTP }
func (c *countingTransport) Send(context.Context, mail.Message) error { return nil }
func (c *countingTransport) Probe(context.Context) mail.ProbeResult {
	c.probes++
	return mail.ProbeResult{Verified: true, Encrypted: true}
}

// The stored row never holds a plaintext credential, and the rendered document
// never carries one either. One sentinel, checked against both.
func TestTheStoredRowAndTheRenderedDocumentHoldNoPlaintextSecret(t *testing.T) {
	const sentinel = "SENTINEL-RELAY-PASSWORD-9f13"
	repo := &fakeRepo{}
	svc := NewService(repo, testCipher(t), EnvConfig{})
	mustSave(t, svc, smtpInput(ptr(sentinel)))

	if strings.Contains(repo.row.Secret, sentinel) {
		t.Error("the stored secret column holds the plaintext credential")
	}
	if !secretbox.IsSealed(repo.row.Secret) {
		t.Errorf("the stored secret is not sealed: %q", repo.row.Secret)
	}
	if strings.Contains(string(repo.row.Settings), sentinel) {
		t.Error("the settings JSON holds the credential")
	}
	st := svc.Status()
	if !st.Config.SMTP.PasswordSet {
		t.Error("password_set = false after saving one")
	}
	// The whole rendered state, as JSON, must not contain it anywhere.
	if body := mustJSON(t, st); strings.Contains(body, sentinel) {
		t.Errorf("the rendered state leaks the credential:\n%s", body)
	}
	// Nor may the audit facts.
	res := mustSave(t, svc, smtpInput(ptr(sentinel+"-2")))
	if strings.Contains(mustJSON(t, res), sentinel) {
		t.Errorf("the save result leaks the credential: %+v", res)
	}
}

// A row whose secret was written unsealed — which this service never does — is
// refused rather than used. It is the fail-closed half of "sealed or not at all".
func TestAnUnsealedStoredSecretIsRefused(t *testing.T) {
	repo := &fakeRepo{row: &sqlcgen.MailConfig{
		ID: true, Transport: mail.KindResend, FromAddress: "no-reply@vidra.test",
		Settings: []byte(`{}`), Secret: "re_plaintext_key",
	}}
	svc := NewService(repo, testCipher(t), EnvConfig{})
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := svc.Status().SecretStatus; got != SecretStatusUndecryptable {
		t.Errorf("secret_status = %q, want %q", got, SecretStatusUndecryptable)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// An anonymous relay saved twice must not claim the credential changed: the
// audit row's secret_changed is the only thing it may say about a credential,
// so it has to mean something.
func TestSecretChangedIsFalseWhenThereIsNoSecretEitherSide(t *testing.T) {
	svc := NewService(&fakeRepo{}, testCipher(t), EnvConfig{})
	anon := smtpInput(nil)
	anon.SMTP.Username = ""
	if res := mustSave(t, svc, anon); res.SecretChanged {
		t.Error("secret_changed = true on a first save that carried no credential")
	}
	if res := mustSave(t, svc, anon); res.SecretChanged {
		t.Error("secret_changed = true on a re-save of an anonymous relay")
	}
}
