package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a net.Listener speaking just enough SMTP for one delivery. When
// tlsConfig is set it advertises STARTTLS and upgrades the connection; when
// offerAuth is set it advertises AUTH PLAIN.
type fakeSMTP struct {
	ln        net.Listener
	tlsConfig *tls.Config
	offerAuth bool

	mu          sync.Mutex
	sawSTARTTLS bool
	sawAuth     bool
	mailFrom    string
	rcptTo      []string
	data        string

	done chan struct{}
}

func newFakeSMTP(t *testing.T, tlsConfig *tls.Config, offerAuth bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln, tlsConfig: tlsConfig, offerAuth: offerAuth, done: make(chan struct{})}
	go f.serveOne(t)
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSMTP) hostPort(t *testing.T) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(f.ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}

// serveOne accepts a single connection and walks the SMTP dialogue until QUIT.
func (f *fakeSMTP) serveOne(t *testing.T) {
	defer close(f.done)
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	r := bufio.NewReader(conn)
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	ehlo := func() {
		lines := []string{"250-fake.test"}
		if f.tlsConfig != nil && !f.sawSTARTTLS {
			lines = append(lines, "250-STARTTLS")
		}
		if f.offerAuth {
			lines = append(lines, "250-AUTH PLAIN")
		}
		lines = append(lines, "250 8BITMIME")
		for _, l := range lines {
			write(l)
		}
	}

	write("220 fake.test ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			ehlo()
		case cmd == "STARTTLS":
			write("220 go ahead")
			tconn := tls.Server(conn, f.tlsConfig)
			if err := tconn.Handshake(); err != nil {
				return
			}
			f.mu.Lock()
			f.sawSTARTTLS = true
			f.mu.Unlock()
			conn = tconn
			r = bufio.NewReader(conn)
			write = func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		case strings.HasPrefix(cmd, "AUTH"):
			f.mu.Lock()
			f.sawAuth = true
			f.mu.Unlock()
			write("235 2.7.0 accepted")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			f.mu.Lock()
			f.mailFrom = strings.TrimSpace(line[len("MAIL FROM:"):])
			f.mu.Unlock()
			write("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.mu.Lock()
			f.rcptTo = append(f.rcptTo, strings.TrimSpace(line[len("RCPT TO:"):]))
			f.mu.Unlock()
			write("250 ok")
		case cmd == "DATA":
			write("354 end with .")
			var data bytes.Buffer
			for {
				dl, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
				data.WriteString(dl)
			}
			f.mu.Lock()
			f.data = data.String()
			f.mu.Unlock()
			write("250 queued")
		case cmd == "QUIT":
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

// snapshot returns the captured conversation after the server finished.
func (f *fakeSMTP) snapshot(t *testing.T) (mailFrom string, rcptTo []string, data string, sawSTARTTLS, sawAuth bool) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(10 * time.Second):
		t.Fatal("fake SMTP server did not finish")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mailFrom, f.rcptTo, f.data, f.sawSTARTTLS, f.sawAuth
}

// selfSignedTLS builds a self-signed cert for 127.0.0.1 plus a client config
// trusting it, so the STARTTLS path can be exercised end to end.
func selfSignedTLS(t *testing.T) (server *tls.Config, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	server = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	client = &tls.Config{ServerName: "127.0.0.1", RootCAs: pool, MinVersion: tls.VersionTLS12}
	return server, client
}

func newMailer(t *testing.T, f *fakeSMTP, username, password string, opts ...Option) *SMTP {
	t.Helper()
	host, port := f.hostPort(t)
	return NewSMTP(Config{
		Host: host, Port: port,
		Username: username, Password: password,
		From: "no-reply@vidra.test", InstanceName: "Vidra Test",
	}, opts...)
}

func TestSendPasswordResetDeliversTokenInBody(t *testing.T) {
	f := newFakeSMTP(t, nil, true)
	m := newMailer(t, f, "mailer", "relay-pass")

	const token = "raw-reset-token-abc123"
	if err := m.SendPasswordReset(context.Background(), "ada@example.test", token); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}
	from, rcpt, data, _, sawAuth := f.snapshot(t)
	if !strings.Contains(from, "no-reply@vidra.test") {
		t.Errorf("MAIL FROM = %q, want no-reply@vidra.test", from)
	}
	if len(rcpt) != 1 || !strings.Contains(rcpt[0], "ada@example.test") {
		t.Errorf("RCPT TO = %v, want ada@example.test", rcpt)
	}
	if !sawAuth {
		t.Error("expected AUTH with configured credentials")
	}
	if !strings.Contains(data, token) {
		t.Error("message body does not carry the reset token")
	}
	if !strings.Contains(data, "Subject: Reset your password on Vidra Test") {
		t.Errorf("missing/incorrect subject; data:\n%s", data)
	}
	for _, h := range []string{"From: no-reply@vidra.test", "To: ada@example.test", "Content-Type: text/plain"} {
		if !strings.Contains(data, h) {
			t.Errorf("message missing header %q", h)
		}
	}
	// The relay password must never appear in the message.
	if strings.Contains(data, "relay-pass") {
		t.Error("relay password leaked into the message")
	}
}

func TestSendEmailVerificationDeliversTokenInBody(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	m := newMailer(t, f, "", "") // no credentials → no AUTH attempted

	const token = "raw-verify-token-xyz789"
	if err := m.SendEmailVerification(context.Background(), "bob@example.test", token); err != nil {
		t.Fatalf("SendEmailVerification: %v", err)
	}
	_, rcpt, data, _, sawAuth := f.snapshot(t)
	if sawAuth {
		t.Error("AUTH attempted with no configured credentials")
	}
	if len(rcpt) != 1 || !strings.Contains(rcpt[0], "bob@example.test") {
		t.Errorf("RCPT TO = %v, want bob@example.test", rcpt)
	}
	if !strings.Contains(data, token) {
		t.Error("message body does not carry the verification token")
	}
	if !strings.Contains(data, "Subject: Confirm your email address on Vidra Test") {
		t.Errorf("missing/incorrect subject; data:\n%s", data)
	}
}

func TestSendContactFormUsesReplyToAndSanitizesHeaders(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	m := newMailer(t, f, "", "")

	err := m.SendContactForm(context.Background(),
		"admin@example.test",
		"Ada\r\nBcc: bad@example.test",
		"ada@example.test",
		"Hello\r\nBcc: bad@example.test",
		"Please take a look.\nThanks.",
	)
	if err != nil {
		t.Fatalf("SendContactForm: %v", err)
	}
	from, rcpt, data, _, _ := f.snapshot(t)
	if !strings.Contains(from, "no-reply@vidra.test") {
		t.Errorf("MAIL FROM = %q, want configured sender", from)
	}
	if len(rcpt) != 1 || !strings.Contains(rcpt[0], "admin@example.test") {
		t.Errorf("RCPT TO = %v, want admin@example.test", rcpt)
	}
	for _, want := range []string{
		"Reply-To: ada@example.test",
		"Subject: [Vidra Test contact] Hello  Bcc: bad@example.test",
		"From: Ada  Bcc: bad@example.test <ada@example.test>",
		"Subject: Hello  Bcc: bad@example.test",
		"Please take a look.\r\nThanks.",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("message missing %q; data:\n%s", want, data)
		}
	}
	if strings.Contains(data, "\r\nBcc: bad@example.test\r\n") {
		t.Errorf("header injection survived sanitization; data:\n%s", data)
	}
}

func TestSendContactFormRejectsReplyToInjection(t *testing.T) {
	m := NewSMTP(Config{Host: "smtp.example.test", Port: 587, From: "no-reply@vidra.test", InstanceName: "Vidra"})
	if err := m.SendContactForm(context.Background(), "admin@example.test", "Ada", "ada@example.test\r\nBcc: bad@example.test", "Hello", "Body"); err == nil {
		t.Fatal("SendContactForm accepted reply-to header injection")
	}
}

func TestSTARTTLSUsedWhenOffered(t *testing.T) {
	serverTLS, clientTLS := selfSignedTLS(t)
	f := newFakeSMTP(t, serverTLS, true)
	m := newMailer(t, f, "mailer", "relay-pass", WithTLSConfig(clientTLS))

	if err := m.SendPasswordReset(context.Background(), "ada@example.test", "tok"); err != nil {
		t.Fatalf("SendPasswordReset over STARTTLS: %v", err)
	}
	_, _, data, sawSTARTTLS, sawAuth := f.snapshot(t)
	if !sawSTARTTLS {
		t.Error("client did not upgrade with STARTTLS although the relay offered it")
	}
	if !sawAuth {
		t.Error("client did not AUTH after STARTTLS")
	}
	if !strings.Contains(data, "tok") {
		t.Error("message not delivered over the TLS session")
	}
}

func TestCredentialsWithoutAuthExtensionFailClosed(t *testing.T) {
	f := newFakeSMTP(t, nil, false) // relay does NOT offer AUTH
	m := newMailer(t, f, "mailer", "relay-pass")

	err := m.SendPasswordReset(context.Background(), "ada@example.test", "tok")
	if err == nil || !strings.Contains(err.Error(), "no AUTH") {
		t.Fatalf("err = %v, want fail-closed 'relay offers no AUTH'", err)
	}
}

func TestRecipientHeaderInjectionRejected(t *testing.T) {
	m := NewSMTP(Config{Host: "smtp.example.test", Port: 587, From: "no-reply@vidra.test", InstanceName: "Vidra"})
	for _, to := range []string{"a@b.test\r\nBcc: evil@x.test", "a@b.test\n", "", "  "} {
		if err := m.SendPasswordReset(context.Background(), to, "tok"); err == nil {
			t.Errorf("recipient %q accepted, want rejection before any dial", to)
		}
	}
}

func TestUnreachableRelayHonoursContext(t *testing.T) {
	// A listener that never accepts: the context deadline must bound the send.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	m := NewSMTP(Config{Host: host, Port: port, From: "no-reply@vidra.test", InstanceName: "Vidra"})

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := m.SendPasswordReset(ctx, "ada@example.test", "tok"); err == nil {
		t.Fatal("send to a silent relay succeeded, want context timeout")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("send took %v, want it bounded by the 250ms context", time.Since(start))
	}
}

// --- email customization seam (config-parity W6) -----------------------------

// TestDecorateSubjectAndBody covers the pure decoration seam: prefix
// prepending with {instance_name} substitution, signature appending after a
// blank line, and every no-op case (no provider, empty value, whitespace).
func TestDecorateSubjectAndBody(t *testing.T) {
	str := func(v string) func() string { return func() string { return v } }
	cases := []struct {
		name        string
		opts        []Option
		subject     string
		body        string
		wantSubject string
		wantBody    string
	}{
		{
			name:        "no providers is a no-op",
			subject:     "Reset your password",
			body:        "Hi,\n\nBody.\n",
			wantSubject: "Reset your password",
			wantBody:    "Hi,\n\nBody.\n",
		},
		{
			name:        "empty prefix and signature are no-ops",
			opts:        []Option{WithSubjectPrefixFunc(str("")), WithBodySignatureFunc(str(""))},
			subject:     "Reset your password",
			body:        "Body.\n",
			wantSubject: "Reset your password",
			wantBody:    "Body.\n",
		},
		{
			name:        "whitespace-only prefix and signature are no-ops",
			opts:        []Option{WithSubjectPrefixFunc(str("   ")), WithBodySignatureFunc(str(" \n\t"))},
			subject:     "Reset your password",
			body:        "Body.\n",
			wantSubject: "Reset your password",
			wantBody:    "Body.\n",
		},
		{
			name:        "prefix prepends with a space",
			opts:        []Option{WithSubjectPrefixFunc(str("[Vidra]"))},
			subject:     "Reset your password",
			body:        "Body.\n",
			wantSubject: "[Vidra] Reset your password",
			wantBody:    "Body.\n",
		},
		{
			name:        "prefix substitutes {instance_name} from the boot config",
			opts:        []Option{WithSubjectPrefixFunc(str("[{instance_name}]"))},
			subject:     "Reset your password",
			body:        "Body.\n",
			wantSubject: "[Vidra Test] Reset your password",
			wantBody:    "Body.\n",
		},
		{
			name: "prefix substitutes the EFFECTIVE instance name when provided",
			opts: []Option{
				WithSubjectPrefixFunc(str("[{instance_name}]")),
				WithInstanceNameFunc(str("ExampleTube")),
			},
			subject:     "Reset your password",
			body:        "Body.\n",
			wantSubject: "[ExampleTube] Reset your password",
			wantBody:    "Body.\n",
		},
		{
			name: "blank effective name falls back to the boot config",
			opts: []Option{
				WithSubjectPrefixFunc(str("[{instance_name}]")),
				WithInstanceNameFunc(str("  ")),
			},
			subject:     "Reset your password",
			body:        "Body.\n",
			wantSubject: "[Vidra Test] Reset your password",
			wantBody:    "Body.\n",
		},
		{
			name:        "signature appends after exactly one blank line",
			opts:        []Option{WithBodySignatureFunc(str("— the ops team"))},
			subject:     "s",
			body:        "Hi,\n\nBody.\n",
			wantSubject: "s",
			wantBody:    "Hi,\n\nBody.\n\n— the ops team\n",
		},
		{
			name:        "signature on a body without trailing newline",
			opts:        []Option{WithBodySignatureFunc(str("bye"))},
			subject:     "s",
			body:        "Body.",
			wantSubject: "s",
			wantBody:    "Body.\n\nbye\n",
		},
		{
			name: "prefix and signature together",
			opts: []Option{
				WithSubjectPrefixFunc(str("[{instance_name}]")),
				WithBodySignatureFunc(str("Run by admins.")),
			},
			subject:     "Confirm your email",
			body:        "Hello.\n",
			wantSubject: "[Vidra Test] Confirm your email",
			wantBody:    "Hello.\n\nRun by admins.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewSMTP(Config{Host: "smtp.example.test", Port: 587, From: "no-reply@vidra.test", InstanceName: "Vidra Test"}, tc.opts...)
			if got := m.decorateSubject(tc.subject); got != tc.wantSubject {
				t.Errorf("decorateSubject = %q, want %q", got, tc.wantSubject)
			}
			if got := m.decorateBody(tc.body); got != tc.wantBody {
				t.Errorf("decorateBody = %q, want %q", got, tc.wantBody)
			}
		})
	}
}

// TestCustomizationAppliedAtSendSeam proves the decoration rides the single
// send() seam end to end — every sender path (here the password reset) gets
// the prefixed subject and appended signature, and a prefix can never inject
// headers (sanitizeHeader strips CR/LF downstream).
func TestCustomizationAppliedAtSendSeam(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	m := newMailer(t, f, "", "",
		WithSubjectPrefixFunc(func() string { return "[{instance_name}]" }),
		WithBodySignatureFunc(func() string { return "Questions? support@vidra.test" }),
		WithInstanceNameFunc(func() string { return "ExampleTube" }),
	)
	if err := m.SendPasswordReset(context.Background(), "ada@example.test", "tok-123"); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}
	_, _, data, _, _ := f.snapshot(t)
	if !strings.Contains(data, "Subject: [ExampleTube] Reset your password on Vidra Test") {
		t.Errorf("subject not prefixed with the substituted instance name; data:\n%s", data)
	}
	if !strings.Contains(data, "Questions? support@vidra.test") {
		t.Error("body signature missing from the delivered message")
	}
}

// The admin mail probe goes through the same send seam as everything else, so
// what the operator receives is what a real recipient would receive — prefix,
// signature and all. A test that bypassed the decoration would prove the relay
// works and prove nothing about the messages users actually get.
func TestSendTestGoesThroughTheDecorationSeam(t *testing.T) {
	f := newFakeSMTP(t, nil, true)
	m := newMailer(t, f, "mailer", "relay-pass",
		WithSubjectPrefixFunc(func() string { return "[{instance_name}]" }),
		WithBodySignatureFunc(func() string { return "Questions? support@vidra.test" }),
		// The EFFECTIVE (DB-overlaid) name, deliberately not the boot config's:
		// an admin who just renamed the instance and immediately sends a test
		// should not read the old name back.
		WithInstanceNameFunc(func() string { return "ExampleTube" }),
	)

	if err := m.SendTest(context.Background(), "ops@example.test"); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	from, rcpt, data, _, sawAuth := f.snapshot(t)
	if !strings.Contains(from, "no-reply@vidra.test") {
		t.Errorf("MAIL FROM = %q, want the configured sender", from)
	}
	if len(rcpt) != 1 || !strings.Contains(rcpt[0], "ops@example.test") {
		t.Errorf("RCPT TO = %v, want exactly the operator address", rcpt)
	}
	if !sawAuth {
		t.Error("expected AUTH with configured credentials")
	}
	if !strings.Contains(data, "Subject: [ExampleTube] Test message from ExampleTube") {
		t.Errorf("subject is not prefixed/substituted; data:\n%s", data)
	}
	if !strings.Contains(data, "Questions? support@vidra.test") {
		t.Error("the configured body signature is missing, so the probe is not representative")
	}
	// The message has to say what it is: an operator who forgot they pressed
	// the button must not read it as a real alert.
	if !strings.Contains(data, "test message") {
		t.Errorf("the body does not identify itself as a test; data:\n%s", data)
	}
	if strings.Contains(data, "relay-pass") {
		t.Error("relay password leaked into the message")
	}
}

// A recipient that could break the envelope is refused before a byte is sent —
// the same guard every other send path gets, reached through SendTest.
func TestSendTestRejectsHeaderInjection(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	m := newMailer(t, f, "", "")
	if err := m.SendTest(context.Background(), "ops@example.test\r\nBcc: victim@elsewhere.test"); err == nil {
		t.Fatal("SendTest accepted a recipient carrying CRLF")
	}
}
