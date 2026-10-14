package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

// peertubeSpoolPattern names the temp files a PeerTube archive upload is
// spooled to; the boot sweep matches the same pattern.
const peertubeSpoolPattern = "vidra-peertube-import-*.zip"

// peertubeSpool is the process-wide admission state for PeerTube archive
// uploads. Each one can be as large as PEERTUBE_IMPORT_MAX_SIZE and lands on
// local disk, so they are admitted against BOTH a fixed number of concurrent
// spools and a free-space budget that remembers what earlier admissions were
// promised.
type peertubeSpool struct {
	// slots bounds concurrent spools; a full set answers 503 + Retry-After
	// rather than queueing, so a waiting request never holds a connection.
	slots chan struct{}

	mu sync.Mutex
	// reserved is the bytes admitted but not yet written, summed over every
	// in-flight spool. Written bytes already show up in a fresh free-space
	// measurement, so each spool's share shrinks as it writes.
	reserved int64

	// checkEvery is how many written bytes pass between free-space re-measures.
	checkEvery int64
	// idle is the longest a single body read may block before the upload is
	// abandoned (408).
	idle time.Duration
}

func newPeerTubeSpool() *peertubeSpool {
	return &peertubeSpool{slots: make(chan struct{}, 2), checkEvery: 256 << 20, idle: 60 * time.Second}
}

func errSpoolDiskLow() error {
	return echo.NewHTTPError(http.StatusServiceUnavailable, "not enough free space to receive the archive; try again later")
}

func errSpoolBusy() error {
	return echo.NewHTTPError(http.StatusServiceUnavailable, "too many archive imports are being received; try again later")
}

// peertubeSpoolRetryAfter is the Retry-After (seconds) on a refused admission.
const peertubeSpoolRetryAfter = "60"

// peertubeSpoolDir is PEERTUBE_IMPORT_TMPDIR, else the process temp dir.
func (s *Server) peertubeSpoolDir() string {
	if s.cfg.PeerTubeImportTmpDir != "" {
		return s.cfg.PeerTubeImportTmpDir
	}
	return os.TempDir()
}

// spoolPeerTubeArchive streams the "archive" file part of the multipart body to
// a temp file and returns it with its size. On any error the file is already
// removed. The parse semaphore is NOT held here: a slow upload must not occupy
// a parse slot. Admission, in order, before a body byte is read:
//
//   - a Content-Length is required (411): the free-space budget is reserved up
//     front from it, so an undeclared body cannot be admitted honestly;
//   - a spool slot (503 + Retry-After when all are busy);
//   - free space >= TRANSCODING_MIN_FREE_SCRATCH_MB + every reservation still
//     outstanding + this body (503 + Retry-After otherwise).
//
// While writing, free space is re-measured every checkEvery bytes against the
// same floor (another consumer of the volume may be filling it), and a read
// that blocks longer than idle abandons the upload (408).
func (s *Server) spoolPeerTubeArchive(c echo.Context) (_ *os.File, _ int64, err error) {
	req := c.Request()
	if req.ContentLength < 0 {
		return nil, 0, echo.NewHTTPError(http.StatusLengthRequired, "a Content-Length is required")
	}
	sp := s.ptSpool
	select {
	case sp.slots <- struct{}{}:
	default:
		c.Response().Header().Set("Retry-After", peertubeSpoolRetryAfter)
		return nil, 0, errSpoolBusy()
	}
	defer func() { <-sp.slots }()

	dir := s.peertubeSpoolDir()
	floor := int64(s.cfg.TranscodingMinFreeScratchMB) << 20
	held := req.ContentLength // this spool's share of sp.reserved
	sp.mu.Lock()
	if s.measureDisk != nil {
		if u, merr := s.measureDisk(dir); merr == nil && int64(u.FreeBytes) < floor+sp.reserved+held { //nolint:gosec // free bytes fit int64
			sp.mu.Unlock()
			c.Response().Header().Set("Retry-After", peertubeSpoolRetryAfter)
			return nil, 0, errSpoolDiskLow()
		}
	}
	sp.reserved += held
	sp.mu.Unlock()
	defer func() {
		sp.mu.Lock()
		sp.reserved -= held
		sp.mu.Unlock()
	}()

	// Per-read idle deadline on the socket, never past the request's own.
	rc := http.NewResponseController(c.Response())
	hard, _ := req.Context().Deadline()
	req.Body = &idleBody{ReadCloser: req.Body, rc: rc, idle: sp.idle, hard: hard}
	defer func() { _ = rc.SetReadDeadline(hard) }()

	mr, err := req.MultipartReader()
	if err != nil {
		return nil, 0, &ValidationError{Fields: []FieldError{{Field: "archive", Message: "a zip file is required (multipart field \"archive\")"}}}
	}
	part, err := nextArchivePart(mr)
	if err != nil {
		return nil, 0, spoolReadError(err)
	}
	f, err := os.CreateTemp(dir, peertubeSpoolPattern)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
			_ = f.Close()
		}
	}()
	w := &spoolWriter{f: f, every: sp.checkEvery, check: func(written int64) error {
		sp.mu.Lock()
		defer sp.mu.Unlock()
		left := max(req.ContentLength-written, 0)
		sp.reserved += left - held
		held = left
		if s.measureDisk == nil {
			return nil
		}
		if u, merr := s.measureDisk(dir); merr == nil && int64(u.FreeBytes) < floor+sp.reserved { //nolint:gosec // as above
			c.Response().Header().Set("Retry-After", peertubeSpoolRetryAfter)
			return errSpoolDiskLow()
		}
		return nil
	}}
	n, err := io.Copy(w, part)
	if err != nil {
		return nil, 0, spoolReadError(err)
	}
	return f, n, nil
}

// spoolReadError maps a failed body read or spool write to its answer: the
// body-limit reader's 413 and the disk guard's 503 pass through, a stalled
// client is 408, anything else (client abort, disk error) stays as is.
func spoolReadError(err error) error {
	var he *echo.HTTPError
	if errors.As(err, &he) {
		return he
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return echo.NewHTTPError(http.StatusRequestTimeout, "the upload stalled")
	}
	return err
}

// idleBody pushes the connection's read deadline out by idle before every
// body read, capped at the request's own deadline. A client that stops
// sending fails the blocked read instead of holding a spool slot until the
// stream timeout. On a writer without deadline support (httptest) it is a
// plain pass-through.
type idleBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	idle time.Duration
	hard time.Time
}

func (b *idleBody) Read(p []byte) (int, error) {
	if b.idle > 0 {
		d := time.Now().Add(b.idle)
		if !b.hard.IsZero() && b.hard.Before(d) {
			d = b.hard
		}
		_ = b.rc.SetReadDeadline(d)
	}
	return b.ReadCloser.Read(p)
}

// spoolWriter writes the spool file and calls check every `every` bytes.
type spoolWriter struct {
	f          *os.File
	every      int64
	written    int64
	sinceCheck int64
	check      func(written int64) error
}

func (w *spoolWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	w.written += int64(n)
	w.sinceCheck += int64(n)
	if err != nil {
		return n, err
	}
	if w.every > 0 && w.sinceCheck >= w.every {
		w.sinceCheck = 0
		if err := w.check(w.written); err != nil {
			return n, err
		}
	}
	return n, nil
}

// sweepStalePeerTubeSpools removes spool files older than maxAge left in dir
// by a process that died mid-import (the request path removes its own file on
// every path it can observe). Only regular files matching the spool pattern
// are touched. A live spool is never that old: its reads are idle-bounded and
// every write refreshes its mtime.
func sweepStalePeerTubeSpools(dir string, maxAge time.Duration, now time.Time, logger *slog.Logger) {
	matches, err := filepath.Glob(filepath.Join(dir, peertubeSpoolPattern))
	if err != nil {
		return
	}
	for _, p := range matches {
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() || now.Sub(fi.ModTime()) < maxAge {
			continue
		}
		if err := os.Remove(p); err == nil && logger != nil {
			logger.Info("removed stale PeerTube import spool file", "path", p, "size", fi.Size())
		}
	}
}
