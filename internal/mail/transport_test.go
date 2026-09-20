package mail

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
)

// fieldErrors flattens a validation failure into field→message, which is the
// shape the admin endpoint answers 422 with.
func fieldErrors(t *testing.T, err error) map[string]string {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %T (%v), want *ValidationError", err, err)
	}
	out := make(map[string]string, len(ve.Fields))
	for _, f := range ve.Fields {
		out[f.Field] = f.Msg
	}
	return out
}

func fieldNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func TestNewTransportBuildsEveryKind(t *testing.T) {
	cases := []struct {
		kind     string
		settings TransportSettings
		secret   string
	}{
		{KindSMTP, TransportSettings{SMTP: SMTPSettings{Host: "smtp.example.test", Port: 587, Username: "u", Encryption: EncryptionSTARTTLS}}, "pw"},
		{KindMailgun, TransportSettings{Mailgun: MailgunSettings{Domain: "mail.vidra.test", Region: RegionEU}}, "key"},
		{KindResend, TransportSettings{}, "re_key"},
		{KindBrevo, TransportSettings{}, "xkeysib"},
		{KindPostmark, TransportSettings{Postmark: PostmarkSettings{}}, "token"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			tr, err := NewTransport(tc.kind, tc.settings, tc.secret)
			if err != nil {
				t.Fatalf("NewTransport(%q): %v", tc.kind, err)
			}
			if tr.Kind() != tc.kind {
				t.Errorf("Kind() = %q, want %q", tr.Kind(), tc.kind)
			}
		})
	}
	// Every kind the panel offers must actually be constructible; a name in
	// Kinds() with no constructor would be a dropdown entry that 422s.
	for _, kind := range Kinds() {
		found := false
		for _, tc := range cases {
			if tc.kind == kind {
				found = true
			}
		}
		if !found {
			t.Errorf("Kinds() offers %q but this test does not build it", kind)
		}
	}
}

// An anonymous relay (no username, no password) is a real shape — a local
// postfix on the same host — and must stay configurable.
func TestNewTransportAllowsAnonymousSMTP(t *testing.T) {
	tr, err := NewTransport(KindSMTP, TransportSettings{
		SMTP: SMTPSettings{Host: "localhost", Port: 25, Encryption: EncryptionNone},
	}, "")
	if err != nil {
		t.Fatalf("anonymous relay rejected: %v", err)
	}
	if tr.Kind() != KindSMTP {
		t.Errorf("Kind() = %q", tr.Kind())
	}
}

// The end-to-end shape Core-B will build: a stored configuration goes through
// NewTransport and delivers a real message to a relay, with the encryption mode
// the document asked for actually applied.
func TestNewTransportSMTPDeliversWithTheConfiguredEncryption(t *testing.T) {
	serverTLS, clientTLS := selfSignedTLS(t)
	f := newFakeSMTP(t, serverTLS, true)
	host, port := f.hostPort(t)

	tr, err := NewTransport(KindSMTP, TransportSettings{SMTP: SMTPSettings{
		Host: host, Port: port, Username: "mailer", Encryption: EncryptionSTARTTLS,
	}}, "relay-pass", withTransportTLSConfig(clientTLS))
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	if err := tr.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, rcpt, data, sawSTARTTLS, sawAuth := f.snapshot(t)
	if !sawSTARTTLS || !sawAuth {
		t.Errorf("starttls=%v auth=%v, want both — the stored mode was not applied", sawSTARTTLS, sawAuth)
	}
	if len(rcpt) != 1 || !strings.Contains(data, "Body.") {
		t.Errorf("message not delivered: rcpt=%v data=%q", rcpt, data)
	}
}

func TestNewTransportValidation(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		settings   TransportSettings
		secret     string
		wantFields []string
	}{
		{
			name:       "unknown transport",
			kind:       "sendgrid",
			wantFields: []string{"transport"},
		},
		{
			name:       "smtp with nothing filled in",
			kind:       KindSMTP,
			settings:   TransportSettings{SMTP: SMTPSettings{}},
			wantFields: []string{"smtp.encryption", "smtp.host", "smtp.port"},
		},
		{
			name: "smtp auto is env-only",
			kind: KindSMTP,
			settings: TransportSettings{SMTP: SMTPSettings{
				Host: "smtp.example.test", Port: 587, Encryption: EncryptionAuto,
			}},
			wantFields: []string{"smtp.encryption"},
		},
		{
			name: "smtp port out of range",
			kind: KindSMTP,
			settings: TransportSettings{SMTP: SMTPSettings{
				Host: "smtp.example.test", Port: 70000, Encryption: EncryptionTLS,
			}},
			wantFields: []string{"smtp.port"},
		},
		{
			name: "smtp host carrying a header break",
			kind: KindSMTP,
			settings: TransportSettings{SMTP: SMTPSettings{
				Host: "smtp.example.test\r\nEVIL", Port: 587, Encryption: EncryptionTLS,
			}},
			wantFields: []string{"smtp.host"},
		},
		{
			name: "smtp password without a username",
			kind: KindSMTP,
			settings: TransportSettings{SMTP: SMTPSettings{
				Host: "smtp.example.test", Port: 587, Encryption: EncryptionTLS,
			}},
			secret:     "pw",
			wantFields: []string{"smtp.username"},
		},
		{
			name:       "mailgun with no domain, region or key",
			kind:       KindMailgun,
			settings:   TransportSettings{},
			wantFields: []string{"mailgun.api_key", "mailgun.domain", "mailgun.region"},
		},
		{
			name: "smtp cleartext with credentials against a remote relay",
			kind: KindSMTP,
			settings: TransportSettings{SMTP: SMTPSettings{
				Host: "10.0.0.5", Port: 25, Username: "mailer", Encryption: EncryptionNone,
			}},
			secret: "relay-pass",
			// The send would fail anyway (net/smtp refuses to transmit AUTH over
			// a cleartext non-localhost connection) but as `auth_failed`, which
			// sends the operator to re-type a password that was correct.
			wantFields: []string{"smtp.encryption"},
		},
		{
			name: "mailgun domain that could steer the request path",
			kind: KindMailgun,
			settings: TransportSettings{Mailgun: MailgunSettings{
				Domain: "mail.vidra.test/../../v3/other.test", Region: RegionUS,
			}},
			secret:     "key",
			wantFields: []string{"mailgun.domain"},
		},
		{
			// Go preserves RawPath, so a percent-encoded separator reaches the
			// wire verbatim and a path-normalising gateway in front of the API
			// could route the call elsewhere. The allowlist has no % in it.
			name: "mailgun domain with a percent-encoded separator",
			kind: KindMailgun,
			settings: TransportSettings{Mailgun: MailgunSettings{
				Domain: "mail.vidra.test%2f..%2fv3%2fother.test", Region: RegionUS,
			}},
			secret:     "key",
			wantFields: []string{"mailgun.domain"},
		},
		{
			name: "mailgun domain with percent-encoded dot segments",
			kind: KindMailgun,
			settings: TransportSettings{Mailgun: MailgunSettings{
				Domain: "%2e%2e/%2e%2e/domains", Region: RegionUS,
			}},
			secret:     "key",
			wantFields: []string{"mailgun.domain"},
		},
		{
			name: "mailgun domain with bare dot segments",
			kind: KindMailgun,
			settings: TransportSettings{Mailgun: MailgunSettings{
				Domain: "..", Region: RegionUS,
			}},
			secret:     "key",
			wantFields: []string{"mailgun.domain"},
		},
		{
			name:       "resend without a key",
			kind:       KindResend,
			wantFields: []string{"resend.api_key"},
		},
		{
			name:       "brevo without a key",
			kind:       KindBrevo,
			wantFields: []string{"brevo.api_key"},
		},
		{
			name:       "postmark without a token",
			kind:       KindPostmark,
			wantFields: []string{"postmark.server_token"},
		},
		{
			name:       "a key carrying a header break",
			kind:       KindResend,
			secret:     "re_key\r\nX-Injected: 1",
			wantFields: []string{"resend.api_key"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := NewTransport(tc.kind, tc.settings, tc.secret)
			if err == nil {
				t.Fatalf("NewTransport accepted an invalid configuration, got %v", tr)
			}
			got := fieldNames(fieldErrors(t, err))
			if strings.Join(got, ",") != strings.Join(tc.wantFields, ",") {
				t.Errorf("fields = %v, want %v", got, tc.wantFields)
			}
		})
	}
}

// The default message stream is Postmark's own, and it is stored explicitly so
// the panel can show what a message will be sent on.
func TestPostmarkStreamDefaults(t *testing.T) {
	tr, err := NewTransport(KindPostmark, TransportSettings{}, "token")
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	pm, ok := tr.(*postmarkTransport)
	if !ok {
		t.Fatalf("transport is %T", tr)
	}
	if pm.stream != defaultPostmarkStream {
		t.Errorf("stream = %q, want %q", pm.stream, defaultPostmarkStream)
	}
}

// SecretField must name the field a missing credential actually reports, or
// the endpoint's 422 and the denylist disagree about what the secret is called.
func TestSecretFieldMatchesTheValidationPath(t *testing.T) {
	missing := map[string]TransportSettings{
		KindSMTP:     {SMTP: SMTPSettings{Host: "smtp.example.test", Port: 587, Encryption: EncryptionTLS, Username: "u"}},
		KindMailgun:  {Mailgun: MailgunSettings{Domain: "mail.vidra.test", Region: RegionUS}},
		KindResend:   {},
		KindBrevo:    {},
		KindPostmark: {},
	}
	for _, kind := range Kinds() {
		t.Run(kind, func(t *testing.T) {
			field := SecretField(kind)
			if field == "" {
				t.Fatalf("SecretField(%q) is empty", kind)
			}
			_, err := NewTransport(kind, missing[kind], "")
			if kind == KindSMTP {
				// SMTP is the one kind whose secret is optional.
				if err != nil {
					t.Fatalf("SMTP without a password should build: %v", err)
				}
				return
			}
			if _, ok := fieldErrors(t, err)[field]; !ok {
				t.Errorf("a missing secret for %q is not reported under %q, got %v", kind, field, err)
			}
		})
	}
	if SecretField("sendgrid") != "" {
		t.Error("SecretField named a secret for a transport that does not exist")
	}
}

// A validation failure names every bad field at once: an admin filling in a
// form should not discover its problems one round trip at a time.
func TestValidationErrorMessageNamesEveryField(t *testing.T) {
	_, err := NewTransport(KindMailgun, TransportSettings{}, "")
	msg := err.Error()
	for _, want := range []string{"mailgun.domain", "mailgun.region", "mailgun.api_key"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not name %q", msg, want)
		}
	}
}
