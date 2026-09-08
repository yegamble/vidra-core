package media

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// fakePingResponder answers every connection with a fixed reply and closes.
// PING needs no INSTREAM framing, so it is deliberately simpler than fakeClamd.
func fakePingResponder(t *testing.T, reply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 16)
			_, _ = c.Read(buf)
			_, _ = c.Write([]byte(reply))
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestPingAnswersPONG(t *testing.T) {
	addr := fakePingResponder(t, "PONG\x00")
	if err := Ping(context.Background(), addr, time.Second); err != nil {
		t.Fatalf("Ping = %v, want nil", err)
	}
}

// Something listening that is not a clamd must not read as healthy: a mistyped
// port in front of an unrelated service is the failure this separates from a
// dead daemon.
func TestPingRejectsNonClamdReply(t *testing.T) {
	addr := fakePingResponder(t, "SSH-2.0-OpenSSH\x00")
	err := Ping(context.Background(), addr, time.Second)
	if err == nil {
		t.Fatal("Ping = nil, want an error for a non-clamd reply")
	}
	if !strings.Contains(err.Error(), "PONG") {
		t.Errorf("error %q does not say what was expected", err)
	}
}

func TestPingUnreachableIsAnError(t *testing.T) {
	if err := Ping(context.Background(), "127.0.0.1:1", time.Second); err == nil {
		t.Fatal("Ping = nil, want a dial error")
	}
}
