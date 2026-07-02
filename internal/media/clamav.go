package media

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// ClamAV scans stored objects for malware by streaming them to a clamd daemon
// over its INSTREAM command. It satisfies video.Scanner.
type ClamAV struct {
	blobs storage.Backend
	addr  string
}

// NewClamAV builds a scanner that streams objects from blobs to the clamd at addr
// (host:port).
func NewClamAV(addr string, blobs storage.Backend) *ClamAV {
	return &ClamAV{blobs: blobs, addr: addr}
}

const (
	clamChunkSize    = 64 * 1024
	clamDialTimeout  = 5 * time.Second
	clamScanDeadline = 60 * time.Second
)

// Scan streams the object at key to clamd and reports whether it is clean. An
// infected object returns (false, nil); a dial/IO/protocol failure returns a
// non-nil error (the caller fails closed on either).
func (c *ClamAV) Scan(ctx context.Context, key string) (bool, error) {
	rc, err := c.blobs.Open(ctx, key)
	if err != nil {
		return false, err
	}
	defer func() { _ = rc.Close() }()

	dialer := net.Dialer{Timeout: clamDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return false, fmt.Errorf("clamav: dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(clamScanDeadline)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return false, fmt.Errorf("clamav: write command: %w", err)
	}
	buf := make([]byte, clamChunkSize)
	for {
		n, rerr := rc.Read(buf)
		if n > 0 {
			var sz [4]byte
			binary.BigEndian.PutUint32(sz[:], uint32(n))
			if _, err := conn.Write(sz[:]); err != nil {
				return false, fmt.Errorf("clamav: write size: %w", err)
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return false, fmt.Errorf("clamav: write chunk: %w", err)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return false, fmt.Errorf("clamav: read object: %w", rerr)
		}
	}
	// A zero-length chunk terminates the stream.
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return false, fmt.Errorf("clamav: write terminator: %w", err)
	}

	resp, err := bufio.NewReader(conn).ReadString('\x00')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("clamav: read response: %w", err)
	}
	resp = strings.TrimRight(resp, "\x00\n ")
	// clamd replies "stream: OK" (clean) or "stream: <signature> FOUND" (infected).
	switch {
	case strings.HasSuffix(resp, "OK"):
		return true, nil
	case strings.HasSuffix(resp, "FOUND"):
		return false, nil
	default:
		return false, fmt.Errorf("clamav: unexpected response %q", resp)
	}
}
