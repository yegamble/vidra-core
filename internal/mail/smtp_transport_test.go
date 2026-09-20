package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	netmail "net/mail"
	"strconv"
	"strings"
	"testing"
)

// transportFor builds an SMTP transport pointed at a fake relay.
func transportFor(t *testing.T, f *fakeSMTP, enc Encryption, clientTLS *tls.Config, username, password string) *SMTPTransport {
	t.Helper()
	host, port := f.hostPort(t)
	return newSMTPTransport(smtpTransportConfig{
		Host: host, Port: port,
		Username: username, Password: password,
		Encryption: enc,
		TLSConfig:  clientTLS,
	})
}

func testMessage() Message {
	return Message{
		From:    netmail.Address{Address: "no-reply@vidra.test"},
		To:      "ada@example.test",
		Subject: "Reset your password",
		Text:    "Hi,\n\nBody.\n",
	}
}

// Implicit TLS is the port-465 shape: the session is encrypted before the
// greeting, and STARTTLS is neither offered nor attempted. Without it an
// operator whose only open port is 465 has no working configuration at all.
func TestImplicitTLSDelivers(t *testing.T) {
	serverTLS, clientTLS := selfSignedTLS(t)
	f := newImplicitTLSFakeSMTP(t, serverTLS, true)
	tr := transportFor(t, f, EncryptionTLS, clientTLS, "mailer", "relay-pass")

	if err := tr.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send over implicit TLS: %v", err)
	}
	_, rcpt, data, sawSTARTTLS, sawAuth := f.snapshot(t)
	if sawSTARTTLS {
		t.Error("client issued STARTTLS inside an already-encrypted session")
	}
	if !sawAuth {
		t.Error("client did not AUTH over the implicit TLS session")
	}
	if len(rcpt) != 1 || !strings.Contains(rcpt[0], "ada@example.test") {
		t.Errorf("RCPT TO = %v", rcpt)
	}
	if !strings.Contains(data, "Subject: Reset your password") {
		t.Errorf("message not delivered over TLS; data:\n%s", data)
	}
}

// Required STARTTLS must FAIL against a relay that does not offer it. The whole
// point of the explicit mode is that it cannot silently become a plaintext
// send — which is exactly what the opportunistic mode does.
func TestRequiredSTARTTLSFailsWhenRelayDoesNotOfferIt(t *testing.T) {
	f := newFakeSMTP(t, nil, false) // advertises no STARTTLS
	tr := transportFor(t, f, EncryptionSTARTTLS, nil, "", "")

	err := tr.Send(context.Background(), testMessage())
	if err == nil {
		t.Fatal("send succeeded against a relay offering no STARTTLS, want tls_failed")
	}
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T (%v), want *SendError", err, err)
	}
	if se.Reason != ReasonTLSFailed {
		t.Errorf("Reason = %q, want %q", se.Reason, ReasonTLSFailed)
	}
	if se.Port == 0 {
		t.Error("SendError carries no port; the UI cannot explain a blocked submission port without it")
	}
	// Nothing may have been handed over in the clear.
	_, rcpt, data, _, _ := f.snapshot(t)
	if len(rcpt) != 0 || data != "" {
		t.Errorf("message leaked to an unencrypted relay: rcpt=%v data=%q", rcpt, data)
	}
}

func TestRequiredSTARTTLSUpgradesWhenOffered(t *testing.T) {
	serverTLS, clientTLS := selfSignedTLS(t)
	f := newFakeSMTP(t, serverTLS, true)
	tr := transportFor(t, f, EncryptionSTARTTLS, clientTLS, "mailer", "relay-pass")

	if err := tr.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send with required STARTTLS: %v", err)
	}
	_, _, data, sawSTARTTLS, sawAuth := f.snapshot(t)
	if !sawSTARTTLS {
		t.Error("required STARTTLS did not upgrade the session")
	}
	if !sawAuth {
		t.Error("client did not AUTH after STARTTLS")
	}
	if !strings.Contains(data, "Body.") {
		t.Error("message not delivered")
	}
}

// Mode `none` is for a relay on localhost. It must not opportunistically
// upgrade: an operator who picked it gets what they picked.
func TestEncryptionNoneNeverUpgrades(t *testing.T) {
	serverTLS, clientTLS := selfSignedTLS(t)
	f := newFakeSMTP(t, serverTLS, false) // STARTTLS IS offered
	tr := transportFor(t, f, EncryptionNone, clientTLS, "", "")

	if err := tr.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send with encryption none: %v", err)
	}
	_, _, data, sawSTARTTLS, _ := f.snapshot(t)
	if sawSTARTTLS {
		t.Error("encryption=none upgraded anyway; the mode is not honoured")
	}
	if !strings.Contains(data, "Body.") {
		t.Error("message not delivered")
	}
}

// A refused connection is the DigitalOcean signature on 25/465/587, so the port
// has to survive into the error or the UI cannot say "your host blocks this".
func TestConnectFailureCarriesReasonAndPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	_ = ln.Close() // nothing is listening now

	tr := newSMTPTransport(smtpTransportConfig{Host: host, Port: port, Encryption: EncryptionNone})
	err = tr.Send(context.Background(), testMessage())
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T (%v), want *SendError", err, err)
	}
	if se.Reason != ReasonConnectFailed {
		t.Errorf("Reason = %q, want %q", se.Reason, ReasonConnectFailed)
	}
	if se.Port != port {
		t.Errorf("Port = %d, want %d", se.Port, port)
	}
}

// The probe must make the same demand a send makes, or the status page answers
// a different question. A relay with no STARTTLS against required-STARTTLS is
// `down` for both.
func TestProbeMakesTheSameDemandAsASend(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	tr := transportFor(t, f, EncryptionSTARTTLS, nil, "", "")

	res := tr.Probe(context.Background())
	if res.Verified {
		t.Error("probe verified a relay that cannot satisfy the configured encryption")
	}
	var se *SendError
	if !errors.As(res.Err, &se) || se.Reason != ReasonTLSFailed {
		t.Fatalf("probe Err = %v, want a *SendError with reason %q", res.Err, ReasonTLSFailed)
	}
	// And it sent nothing.
	_, rcpt, data, _, _ := f.snapshot(t)
	if len(rcpt) != 0 || data != "" {
		t.Errorf("probe delivered a message: rcpt=%v data=%q", rcpt, data)
	}
}

func TestProbeVerifiesAHealthyRelay(t *testing.T) {
	serverTLS, clientTLS := selfSignedTLS(t)
	f := newFakeSMTP(t, serverTLS, true)
	tr := transportFor(t, f, EncryptionSTARTTLS, clientTLS, "mailer", "relay-pass")

	res := tr.Probe(context.Background())
	if res.Err != nil || !res.Verified {
		t.Fatalf("Probe = %+v, want verified with no error", res)
	}
	_, rcpt, data, _, sawAuth := f.snapshot(t)
	if !sawAuth {
		t.Error("probe did not prove the credentials")
	}
	if len(rcpt) != 0 || data != "" {
		t.Errorf("probe delivered a message: rcpt=%v data=%q", rcpt, data)
	}
}

// A display name is optional, and its presence must not change how a bare
// address renders — the environment path has always emitted the bare form and
// an unexplained change there is a deliverability change nobody asked for.
func TestFromDisplayNameRendering(t *testing.T) {
	cases := []struct {
		name string
		from netmail.Address
		want string
	}{
		{"bare address renders exactly as configured", netmail.Address{Address: "no-reply@vidra.test"}, "From: no-reply@vidra.test\r\n"},
		{"display name is quoted by the stdlib", netmail.Address{Name: "Vidra Mail", Address: "no-reply@vidra.test"}, "From: \"Vidra Mail\" <no-reply@vidra.test>\r\n"},
		{"whitespace-only name is no name", netmail.Address{Name: "   ", Address: "no-reply@vidra.test"}, "From: no-reply@vidra.test\r\n"},
		{"non-ASCII name is RFC 2047 encoded", netmail.Address{Name: "Vidrá", Address: "no-reply@vidra.test"}, "From: =?utf-8?q?Vidr=C3=A1?= <no-reply@vidra.test>\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testMessage()
			m.From = tc.from
			if got := string(render(m)); !strings.Contains(got, tc.want) {
				t.Errorf("rendered message missing %q; got:\n%s", tc.want, got)
			}
		})
	}
}

// Every header-bound field is refused before a byte goes out, not sanitized:
// a From or Reply-To the operator did not type is a configuration error, and
// quietly mangling it would hide it.
func TestTransportRejectsHeaderInjection(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	tr := transportFor(t, f, EncryptionNone, nil, "", "")

	cases := []struct {
		name string
		mut  func(*Message)
	}{
		{"recipient", func(m *Message) { m.To = "a@b.test\r\nBcc: evil@x.test" }},
		{"from address", func(m *Message) { m.From.Address = "a@b.test\r\nBcc: evil@x.test" }},
		{"from display name", func(m *Message) { m.From.Name = "Ada\r\nBcc: evil@x.test" }},
		{"reply-to", func(m *Message) { m.ReplyTo = "a@b.test\r\nBcc: evil@x.test" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testMessage()
			tc.mut(&m)
			if err := tr.Send(context.Background(), m); err == nil {
				t.Error("send accepted a CRLF-carrying header field")
			}
		})
	}
}

// --- the resolver seam ------------------------------------------------------

// An unconfigured instance returns nil from every send — the historical
// noopMailer contract — and must not dial anything on the way.
func TestUnconfiguredComposerSendsNothingAndReturnsNil(t *testing.T) {
	c := NewComposer(ComposerConfig{InstanceName: "Vidra Test"}, NewStaticResolver(nil, netmail.Address{}, ""))
	if c.Configured() {
		t.Error("Configured() = true with no transport")
	}
	if err := c.SendPasswordReset(context.Background(), "ada@example.test", "tok"); err != nil {
		t.Fatalf("SendPasswordReset on an unconfigured instance = %v, want nil", err)
	}
	if _, ok := c.Transport(); ok {
		t.Error("Transport() reported a transport on an unconfigured instance")
	}
}

// recordingTransport captures what the composer hands over.
type recordingTransport struct {
	msgs []Message
	err  error
}

func (r *recordingTransport) Kind() string { return "recording" }
func (r *recordingTransport) Send(_ context.Context, m Message) error {
	r.msgs = append(r.msgs, m)
	return r.err
}
func (r *recordingTransport) Probe(context.Context) ProbeResult { return ProbeResult{Verified: true} }

// The composer resolves the transport PER SEND, so an instance that switches
// provider takes effect on the next message without a restart.
func TestComposerResolvesTransportPerSend(t *testing.T) {
	first := &recordingTransport{}
	second := &recordingTransport{}
	current := Transport(first)
	c := NewComposer(ComposerConfig{InstanceName: "Vidra Test"}, resolverFunc(func() (Transport, netmail.Address, string, bool) {
		return current, netmail.Address{Name: "Vidra", Address: "no-reply@vidra.test"}, "ops@vidra.test", true
	}))

	if err := c.SendPasswordReset(context.Background(), "ada@example.test", "tok"); err != nil {
		t.Fatalf("first send: %v", err)
	}
	current = second
	if err := c.SendPasswordReset(context.Background(), "bob@example.test", "tok"); err != nil {
		t.Fatalf("second send: %v", err)
	}
	if len(first.msgs) != 1 || len(second.msgs) != 1 {
		t.Fatalf("messages: first=%d second=%d, want 1 each — the transport is not resolved per send", len(first.msgs), len(second.msgs))
	}
	got := first.msgs[0]
	if got.From.Name != "Vidra" || got.From.Address != "no-reply@vidra.test" {
		t.Errorf("From = %+v, want the resolved sender", got.From)
	}
	// The resolved default reply-to rides every message that has no explicit one.
	if got.ReplyTo != "ops@vidra.test" {
		t.Errorf("ReplyTo = %q, want the resolved default", got.ReplyTo)
	}
}

// The contact form's visitor address is an EXPLICIT reply-to and must beat the
// configured default: the operator answers the visitor, not themselves.
func TestExplicitReplyToBeatsTheResolvedDefault(t *testing.T) {
	rec := &recordingTransport{}
	c := NewComposer(ComposerConfig{InstanceName: "Vidra Test"}, NewStaticResolver(rec, netmail.Address{Address: "no-reply@vidra.test"}, "ops@vidra.test"))

	if err := c.SendContactForm(context.Background(), "admin@example.test", "Ada", "ada@example.test", "Hello", "Body"); err != nil {
		t.Fatalf("SendContactForm: %v", err)
	}
	if len(rec.msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(rec.msgs))
	}
	if rec.msgs[0].ReplyTo != "ada@example.test" {
		t.Errorf("ReplyTo = %q, want the visitor's address", rec.msgs[0].ReplyTo)
	}
}

type resolverFunc func() (Transport, netmail.Address, string, bool)

func (f resolverFunc) Current() (Transport, netmail.Address, string, bool) { return f() }
