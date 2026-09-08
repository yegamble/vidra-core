package media

import (
	"bufio"
	"bytes"
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
	blobs   storage.Backend
	addr    string
	timeout time.Duration
}

// NewClamAV builds a scanner that streams objects from blobs to the clamd at addr
// (host:port). timeout bounds a single scan (dial + stream + verdict); a
// non-positive value falls back to the built-in default so callers that don't
// configure one still behave sanely.
func NewClamAV(addr string, blobs storage.Backend, timeout time.Duration) *ClamAV {
	if timeout <= 0 {
		timeout = clamDefaultScanDeadline
	}
	return &ClamAV{blobs: blobs, addr: addr, timeout: timeout}
}

const (
	clamChunkSize = 64 * 1024
	// clamDialTimeout caps the TCP dial; the overall scan is additionally bounded
	// by the configured timeout (it never exceeds it).
	clamDialTimeout = 5 * time.Second
	// clamDefaultScanDeadline is the fallback overall scan bound when no timeout
	// is configured (CLAMAV_TIMEOUT default mirrors this).
	clamDefaultScanDeadline = 60 * time.Second
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
	return c.scan(ctx, rc)
}

// ScanBytes runs the same INSTREAM scan over an in-memory buffer, for the
// small user-supplied files that are scanned BEFORE they are stored: poster
// images, channel avatars and banners, playlist covers, caption tracks and
// account-import archives. Those paths have no "delete the object we just
// wrote" recovery worth writing — the file is bounded by the 8 MiB body limit
// and is already in memory when the handler validates it — so scanning the
// buffer keeps a rejected file from ever reaching the object store at all.
func (c *ClamAV) ScanBytes(ctx context.Context, data []byte) (bool, error) {
	return c.scan(ctx, bytes.NewReader(data))
}

// scan is the shared INSTREAM body: everything after "where do the bytes come
// from".
func (c *ClamAV) scan(ctx context.Context, rc io.Reader) (bool, error) {
	// The dial never waits longer than the whole scan budget.
	dialTimeout := clamDialTimeout
	if c.timeout < dialTimeout {
		dialTimeout = c.timeout
	}
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return false, fmt.Errorf("clamav: dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(c.timeout)
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

// Ping asks the clamd at addr whether it is alive (its PING/PONG command) and
// returns nil when it answers. It is the liveness half of the scanner
// dependency: /admin/system had no scanner component at all, so a dead clamd
// left the page reporting a healthy instance while every upload and every URL
// import failed closed.
//
// PING is deliberately not Scan: it costs the daemon one connection and no
// signature work, so an admin refreshing the page during an incident cannot
// make the queue slower. timeout bounds the whole exchange; a non-positive
// value falls back to the same built-in default NewClamAV uses.
func Ping(ctx context.Context, addr string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = clamDefaultScanDeadline
	}
	dialTimeout := clamDialTimeout
	if timeout < dialTimeout {
		dialTimeout = timeout
	}
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("clamav: dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write([]byte("zPING\x00")); err != nil {
		return fmt.Errorf("clamav: write command: %w", err)
	}
	resp, err := bufio.NewReader(conn).ReadString('\x00')
	if err != nil && err != io.EOF {
		return fmt.Errorf("clamav: read response: %w", err)
	}
	if strings.TrimRight(resp, "\x00\n ") != "PONG" {
		// Something is listening but it is not a clamd — an SSH banner, a proxy,
		// a mistyped port. Saying so is the difference between an operator
		// restarting the daemon and one fixing CLAMAV_ADDR.
		return fmt.Errorf("clamav: the address answered but did not reply PONG")
	}
	return nil
}
