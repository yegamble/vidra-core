package preflight

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// greetingListener stands up a loopback server that writes greeting to every
// connection. An empty greeting means it accepts and then says nothing, holding
// the connection open until the test ends — the "server is there but wedged"
// shape. It is a real TCP listener rather than a fake dialer because the dial IS
// the thing under test; loopback keeps it hermetic.
func greetingListener(t *testing.T, greeting string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = ln.Close()
		close(done)
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if greeting == "" {
				go func() {
					<-done
					_ = conn.Close()
				}()
				continue
			}
			_, _ = conn.Write([]byte(greeting))
			_ = conn.Close()
		}
	}()
	return ln.Addr().String()
}

func TestCheckSMTPReadsGreeting(t *testing.T) {
	addr := greetingListener(t, "220 mail.example.test ESMTP ready\r\n")

	banner, err := CheckSMTP(t.Context(), addr)
	if err != nil {
		t.Fatalf("CheckSMTP: %v", err)
	}
	if banner != "220 mail.example.test ESMTP ready" {
		t.Errorf("banner = %q, want the greeting with CRLF trimmed", banner)
	}
}

// A relay that is not a relay still answers a line; the caller — not this
// function — decides that a non-220 greeting is a problem.
func TestCheckSMTPReturnsNon220Greeting(t *testing.T) {
	addr := greetingListener(t, "HTTP/1.1 400 Bad Request\r\n")

	banner, err := CheckSMTP(t.Context(), addr)
	if err != nil {
		t.Fatalf("CheckSMTP: %v", err)
	}
	if strings.HasPrefix(banner, "220") {
		t.Errorf("banner = %q, want the verbatim non-SMTP line", banner)
	}
}

func TestCheckSMTPNothingListening(t *testing.T) {
	// Bind a port and immediately release it, so the address is routable and
	// almost certainly refusing connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	if _, err := CheckSMTP(t.Context(), addr); err == nil {
		t.Fatal("CheckSMTP on a closed port returned no error")
	}
}

// A server that accepts and then says nothing must not hang the caller: the
// context deadline has to reach the read, not just the dial.
func TestCheckSMTPSilentServerHitsDeadline(t *testing.T) {
	addr := greetingListener(t, "")

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := CheckSMTP(ctx, addr); err == nil {
		t.Fatal("CheckSMTP against a silent server returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("CheckSMTP took %s; the read deadline was not applied", elapsed)
	}
}
