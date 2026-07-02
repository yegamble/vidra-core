package media

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// fakeClamd is a minimal clamd: it reads a full INSTREAM upload (command, then
// size-prefixed chunks until the zero-length terminator) and only then replies
// with a fixed verdict — matching real clamd so the client's terminator write
// never races a premature close. Returns the listener address.
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
		r := bufio.NewReader(conn)
		if _, err := r.ReadString(0); err != nil { // "zINSTREAM\0"
			return
		}
		for {
			var sz [4]byte
			if _, err := io.ReadFull(r, sz[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint32(sz[:])
			if n == 0 { // zero-length chunk = end of stream
				break
			}
			if _, err := io.ReadFull(r, make([]byte, n)); err != nil {
				return
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
