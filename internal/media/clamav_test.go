package media

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// fakeClamd is a minimal clamd that reads an INSTREAM upload and replies with a
// fixed verdict. It returns the listener address.
func fakeClamd(t *testing.T, verdict string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Drain the request until the client is done writing (it half-closes or we
		// just read whatever arrives); then reply. Reading a little is enough to
		// exercise the write path without fully parsing the framing.
		r := bufio.NewReader(conn)
		_, _ = r.ReadString(0) // consume the "zINSTREAM\0" command
		buf := make([]byte, 4096)
		for {
			if _, err := r.Read(buf); err != nil {
				break
			}
			if r.Buffered() == 0 {
				break
			}
		}
		_, _ = conn.Write([]byte(verdict + "\x00"))
	}()
	return ln.Addr().String()
}

func localWithFile(t *testing.T, key, content string) storage.Backend {
	t.Helper()
	b, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if _, err := b.Put(context.Background(), key, strings.NewReader(content)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	return b
}

func TestClamAVClean(t *testing.T) {
	blobs := localWithFile(t, "originals/x.mp4", "harmless bytes")
	sc := NewClamAV(fakeClamd(t, "stream: OK"), blobs)
	clean, err := sc.Scan(context.Background(), "originals/x.mp4")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !clean {
		t.Error("clean object reported infected")
	}
}

func TestClamAVInfected(t *testing.T) {
	blobs := localWithFile(t, "originals/x.mp4", "EICAR-ish")
	sc := NewClamAV(fakeClamd(t, "stream: Eicar-Test-Signature FOUND"), blobs)
	clean, err := sc.Scan(context.Background(), "originals/x.mp4")
	if err != nil {
		t.Fatalf("Scan returned err: %v", err)
	}
	if clean {
		t.Error("infected object reported clean")
	}
}

func TestClamAVUnexpectedResponseIsError(t *testing.T) {
	blobs := localWithFile(t, "originals/x.mp4", "bytes")
	sc := NewClamAV(fakeClamd(t, "gibberish"), blobs)
	if _, err := sc.Scan(context.Background(), "originals/x.mp4"); err == nil {
		t.Error("unexpected clamd response should be an error (fail closed)")
	}
}

func TestClamAVDialFailureIsError(t *testing.T) {
	blobs := localWithFile(t, "originals/x.mp4", "bytes")
	// Nothing is listening here.
	sc := NewClamAV("127.0.0.1:1", blobs)
	if _, err := sc.Scan(context.Background(), "originals/x.mp4"); err == nil {
		t.Error("dial failure should be an error")
	}
}
