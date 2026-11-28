package processedupload

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

var (
	ErrRejected     = errors.New("could not reconstruct the uploaded video")
	ErrInfected     = errors.New("processed media failed its safety scan")
	ErrScanTooLarge = errors.New("processed media exceeds the whole-file scan limit")
)

type Scanner interface {
	ScanReader(context.Context, io.Reader) (bool, error)
}

// ScanFile returns an unlinked, seekable copy of the exact bytes that clamd
// accepted. The caller closes it after storing or serving those bytes.
func ScanFile(ctx context.Context, scanner Scanner, src io.Reader, size int64) (*os.File, error) {
	if size > MaxScannedBytes {
		return nil, ErrScanTooLarge
	}
	if scanner == nil || size == 0 || size < -1 {
		return nil, errors.New("invalid scan configuration or file length")
	}
	f, err := os.CreateTemp("", "vidra-media-scan-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(f.Name()); err != nil {
		f.Close()
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			f.Close()
		}
	}()
	body := io.TeeReader(io.LimitReader(src, MaxScannedBytes+1), f)
	clean, err := scanner.ScanReader(ctx, body)
	n, seekErr := f.Seek(0, io.SeekCurrent)
	if seekErr != nil {
		return nil, seekErr
	}
	if n > MaxScannedBytes {
		return nil, ErrScanTooLarge
	}
	if err != nil {
		return nil, err
	}
	if !clean {
		return nil, ErrInfected
	}
	var extra [1]byte
	if n, err := io.ReadFull(src, extra[:]); n != 0 || err != io.EOF {
		return nil, errors.New("scanner returned before input ended")
	}
	if n <= 0 || (size >= 0 && n != size) {
		return nil, errors.New("scanned byte count differs from expected file")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	accepted = true
	return f, nil
}

// Client streams into a local privileged broker. Only the separately scanned
// derivative ever reaches the upload finalizer as an attachable video.
type Client struct {
	Socket  string
	Scanner Scanner
}

func (c Client) Normalize(ctx context.Context, raw io.Reader, size int64) (*os.File, error) {
	if c.Socket == "" || c.Scanner == nil || size <= 0 || size > MaxInputBytes {
		return nil, errors.New("invalid normalizer configuration or input length")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Hour)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/normalize", io.NopCloser(raw))
	if err != nil {
		return nil, err
	}
	req.ContentLength = size
	req.Close = true
	// Synchronous writing ensures the chunk assembler is released only after
	// its final read. The server may spend hours encoding after that point.
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnprocessableEntity {
		return nil, ErrRejected
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("normalizer returned HTTP %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "video/mp4" || resp.ContentLength <= 0 || resp.ContentLength > MaxScannedBytes {
		return nil, errors.New("invalid processed file response")
	}
	return ScanFile(ctx, c.Scanner, resp.Body, resp.ContentLength)
}
