package preflight

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeRelay is a scriptable ESMTP server: enough of the protocol to answer
// EHLO, STARTTLS, AUTH and QUIT, and no more. It is a real listener rather than
// a fake dialer because the handshake IS the thing under test — a stubbed
// client would only prove the stub agrees with itself.
type fakeRelay struct {
	// greeting replaces the 220 line entirely (for the "not a relay" case).
	greeting string
	// offerSTARTTLS / offerAUTH control what EHLO advertises.
	offerSTARTTLS bool
	offerAUTH     bool
	// acceptAUTH decides between 235 and 535.
	acceptAUTH bool
	// tlsCert, when set, is served on STARTTLS.
	tlsCert *tls.Certificate

	// commands records every verb the client sent, so a test can prove the
	// probe stopped BEFORE MAIL FROM.
	commands chan string
}

func (r *fakeRelay) start(t *testing.T) (host string, port int) {
	t.Helper()
	r.commands = make(chan string, 32)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(conn)
		}
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(p)
	return h, n
}

func (r *fakeRelay) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	greeting := r.greeting
	if greeting == "" {
		greeting = "220 relay.example.test ESMTP\r\n"
	}
	if _, err := conn.Write([]byte(greeting)); err != nil {
		return
	}
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.Fields(strings.TrimSpace(line) + " ")[0])
		select {
		case r.commands <- verb:
		default:
		}
		switch verb {
		case "EHLO", "HELO":
			var b strings.Builder
			b.WriteString("250-relay.example.test\r\n")
			if r.offerSTARTTLS {
				b.WriteString("250-STARTTLS\r\n")
			}
			if r.offerAUTH {
				b.WriteString("250-AUTH PLAIN\r\n")
			}
			b.WriteString("250 8BITMIME\r\n")
			if _, err := conn.Write([]byte(b.String())); err != nil {
				return
			}
		case "STARTTLS":
			if r.tlsCert == nil {
				_, _ = conn.Write([]byte("454 TLS not available\r\n"))
				continue
			}
			if _, err := conn.Write([]byte("220 Ready to start TLS\r\n")); err != nil {
				return
			}
			tconn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*r.tlsCert}})
			if err := tconn.Handshake(); err != nil {
				return
			}
			conn = tconn
			br = bufio.NewReader(conn)
			// After STARTTLS the client re-sends EHLO; the offers stay the same,
			// minus STARTTLS itself (which is what a real relay does).
			r.offerSTARTTLS = false
		case "AUTH":
			if r.acceptAUTH {
				_, _ = conn.Write([]byte("235 2.7.0 Authentication successful\r\n"))
			} else {
				_, _ = conn.Write([]byte("535 5.7.8 Authentication credentials invalid\r\n"))
			}
		case "QUIT":
			_, _ = conn.Write([]byte("221 2.0.0 Bye\r\n"))
			return
		default:
			_, _ = conn.Write([]byte("250 2.0.0 OK\r\n"))
		}
	}
}

func (r *fakeRelay) sawCommand(verb string) bool {
	for {
		select {
		case got := <-r.commands:
			if got == verb {
				return true
			}
		default:
			return false
		}
	}
}

// selfSignedCert mints a throwaway certificate for host. Generated at run time
// on purpose: a PEM literal in the source is a secret-scanner incident that
// outlives the test.
func selfSignedCert(t *testing.T, host string) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP(host)},
		DNSNames:     []string{host},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func stageOf(t *testing.T, err error) string {
	t.Helper()
	var he *SMTPHandshakeError
	if !errors.As(err, &he) {
		t.Fatalf("error %v is not an *SMTPHandshakeError", err)
	}
	return he.Stage
}

// TestSMTPHandshakePlainRelaySucceedsAndSendsNothing is the positive control,
// and the assertion that matters most beside it: the probe must never put a
// message on the wire.
func TestSMTPHandshakePlainRelaySucceedsAndSendsNothing(t *testing.T) {
	relay := &fakeRelay{}
	host, port := relay.start(t)

	res, err := CheckSMTPHandshake(t.Context(), SMTPHandshake{Host: host, Port: port})
	if err != nil {
		t.Fatalf("CheckSMTPHandshake on a healthy relay: %v", err)
	}
	if res.STARTTLS || res.Authenticated {
		t.Errorf("result = %+v; a plain anonymous relay offers neither", res)
	}
	if !relay.sawCommand("QUIT") {
		t.Error("the probe did not QUIT; it abandoned the connection")
	}
	for _, verb := range []string{"MAIL", "RCPT", "DATA"} {
		if relay.sawCommand(verb) {
			t.Errorf("the probe sent %s — a diagnostic must not put a message on the wire", verb)
		}
	}
}

// TestSMTPHandshakeCredentialsAgainstNoAuthRelay reproduces one of the two
// states A05 measured reporting `smtp: ok` while every send failed. The mailer
// fails closed here rather than sending unauthenticated, so the probe must too.
func TestSMTPHandshakeCredentialsAgainstNoAuthRelay(t *testing.T) {
	relay := &fakeRelay{}
	host, port := relay.start(t)

	_, err := CheckSMTPHandshake(t.Context(), SMTPHandshake{
		Host: host, Port: port, Username: "postmaster", Password: "not-a-real-password",
	})
	if err == nil {
		t.Fatal("credentials against a relay with no AUTH returned no error")
	}
	if stage := stageOf(t, err); stage != SMTPStageAuthUnsupported {
		t.Errorf("stage = %q, want %q", stage, SMTPStageAuthUnsupported)
	}
	if relay.sawCommand("AUTH") {
		t.Error("the probe tried to AUTH against a relay that offers none")
	}
}

// TestSMTPHandshakeRejectedCredentials is the other state A05 measured as `ok`.
func TestSMTPHandshakeRejectedCredentials(t *testing.T) {
	relay := &fakeRelay{offerAUTH: true, acceptAUTH: false}
	host, port := relay.start(t)

	_, err := CheckSMTPHandshake(t.Context(), SMTPHandshake{
		Host: host, Port: port, Username: "postmaster", Password: "not-a-real-password",
	})
	if err == nil {
		t.Fatal("rejected credentials returned no error")
	}
	if stage := stageOf(t, err); stage != SMTPStageAuth {
		t.Errorf("stage = %q, want %q", stage, SMTPStageAuth)
	}
}

// TestSMTPHandshakeAcceptedCredentials: the same relay, saying yes.
func TestSMTPHandshakeAcceptedCredentials(t *testing.T) {
	relay := &fakeRelay{offerAUTH: true, acceptAUTH: true}
	host, port := relay.start(t)

	res, err := CheckSMTPHandshake(t.Context(), SMTPHandshake{
		Host: host, Port: port, Username: "postmaster", Password: "not-a-real-password",
	})
	if err != nil {
		t.Fatalf("accepted credentials: %v", err)
	}
	if !res.Authenticated {
		t.Error("result does not record that AUTH succeeded")
	}
}

// TestSMTPHandshakeUntrustedSTARTTLSCertificate is the defect's headline case:
// a relay whose certificate this instance refuses. Every send fails, and the
// greeting-only probe called it `ok`.
func TestSMTPHandshakeUntrustedSTARTTLSCertificate(t *testing.T) {
	relay := &fakeRelay{offerSTARTTLS: true}
	host, port := relay.start(t)
	relay.tlsCert = selfSignedCert(t, host)

	_, err := CheckSMTPHandshake(t.Context(), SMTPHandshake{Host: host, Port: port})
	if err == nil {
		t.Fatal("an untrusted STARTTLS certificate returned no error")
	}
	if stage := stageOf(t, err); stage != SMTPStageSTARTTLS {
		t.Errorf("stage = %q, want %q", stage, SMTPStageSTARTTLS)
	}
}

// TestSMTPHandshakeTrustedSTARTTLS: the same relay with its CA trusted goes all
// the way through, so the refusal above is about trust and not about STARTTLS.
func TestSMTPHandshakeTrustedSTARTTLS(t *testing.T) {
	relay := &fakeRelay{offerSTARTTLS: true, offerAUTH: true, acceptAUTH: true}
	host, port := relay.start(t)
	cert := selfSignedCert(t, host)
	relay.tlsCert = cert

	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	res, err := CheckSMTPHandshake(t.Context(), SMTPHandshake{
		Host: host, Port: port, Username: "postmaster", Password: "not-a-real-password",
		TLSConfig: &tls.Config{ServerName: host, RootCAs: pool, MinVersion: tls.VersionTLS12},
	})
	if err != nil {
		t.Fatalf("trusted STARTTLS relay: %v", err)
	}
	if !res.STARTTLS || !res.Authenticated {
		t.Errorf("result = %+v, want STARTTLS and AUTH both recorded", res)
	}
}

// TestSMTPHandshakeRelayDown and its neighbour keep the two failures the old
// greeting probe DID catch working.
func TestSMTPHandshakeRelayDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	_ = ln.Close()

	_, err = CheckSMTPHandshake(t.Context(), SMTPHandshake{Host: h, Port: port})
	if err == nil {
		t.Fatal("a closed port returned no error")
	}
	if stage := stageOf(t, err); stage != SMTPStageDial {
		t.Errorf("stage = %q, want %q", stage, SMTPStageDial)
	}
}

func TestSMTPHandshakeNotARelay(t *testing.T) {
	relay := &fakeRelay{greeting: "HTTP/1.1 400 Bad Request\r\n"}
	host, port := relay.start(t)

	_, err := CheckSMTPHandshake(t.Context(), SMTPHandshake{Host: host, Port: port})
	if err == nil {
		t.Fatal("a non-SMTP greeting returned no error")
	}
	if stage := stageOf(t, err); stage != SMTPStageGreeting {
		t.Errorf("stage = %q, want %q", stage, SMTPStageGreeting)
	}
}

// TestSMTPHandshakeSilentRelayHitsDeadline: a server that accepts and then says
// nothing must not hang the admin page.
func TestSMTPHandshakeSilentRelayHitsDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				<-done
				_ = conn.Close()
			}()
		}
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := CheckSMTPHandshake(ctx, SMTPHandshake{Host: h, Port: port}); err == nil {
		t.Fatal("a silent relay returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s; the deadline did not reach the read", elapsed)
	}
}
