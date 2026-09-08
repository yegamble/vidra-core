package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/video"
)

// The ingestion-side safety-scan gate.
//
// A28 measured the shipped posture: with CLAMAV_ADDR unset an instance accepted
// and PUBLISHED an mp4 with EICAR appended, with no scan, no boot warning and
// no log line — discoverable only by an operator who happened to open
// /admin/infrastructure. INT-04 asks that scanning gate all ingestion; the
// default gated none. The posture here inverts that: an instance with no
// scanner and no explicit opt-out refuses every user-supplied file with a typed
// 503, and an operator who genuinely wants unscanned ingestion says so once
// (MALWARE_SCAN_MODE=disabled) and gets a boot WARN and an audit row for it.
//
// A28 also found the second half missing — the scan only ever covered video
// originals and DM attachments. Posters, avatars, banners, playlist covers,
// caption tracks and account-import archives all reached the object store
// unscanned. They go through scanBeforeStore below.

// ScannerNotConfiguredError renders as 503 with the stable code
// scanner_not_configured: this instance has no malware scanner wired and has
// not declared that it means to ingest unscanned, so it accepts no
// user-supplied file at all.
//
// Typed for the reason live_not_configured is: a bare echo.NewHTTPError with a
// 5xx status is scrubbed to a generic message by the error handler, and the
// operator sentence — which is the only useful part — never reaches anyone.
type ScannerNotConfiguredError struct{}

func (e *ScannerNotConfiguredError) Error() string { return "malware scanner is not configured" }

// SafetyScanRejectedError renders as 422 with the stable code
// safety_scan_rejected and video.SafetyScanRejectedMessage. It is the
// synchronous half of the same verdict the async pipelines write onto an upload
// session's failure_reason.
type SafetyScanRejectedError struct{}

func (e *SafetyScanRejectedError) Error() string { return "file rejected by the safety scan" }

// FileScanner scans a small user-supplied file held in memory, before it is
// stored. *media.ClamAV satisfies it.
type FileScanner interface {
	ScanBytes(ctx context.Context, data []byte) (clean bool, err error)
}

// requireScanner is the gate every user-supplied ingestion route calls FIRST,
// before it reads a byte of the body. It returns the typed 503 when this
// instance has no scanner and no opt-out.
//
// It is deliberately NOT folded into uploadsEnabled(): that gate answers 403
// feature_disabled ("the operator turned uploads off"), which is a different
// fact with a different remedy, and a client cannot act on the two the same way.
func (s *Server) requireScanner() error {
	if s.cfg.IngestionRefusedForScanner() {
		return &ScannerNotConfiguredError{}
	}
	return nil
}

// scannerReadyForIngestion reports the same fact as requireScanner, inverted,
// for /instance: it is what makes features.uploads and features.imports read
// false so the Studio hides the affordances instead of offering a control that
// answers 503 (the A17 capability-truth pattern).
func (s *Server) scannerReadyForIngestion() bool {
	return !s.cfg.IngestionRefusedForScanner()
}

// scanBeforeStore applies the instance's scan policy to a small user-supplied
// file that is about to be stored. It returns the vetted bytes on success and a
// typed refusal otherwise.
//
// kind names the asset class in the log and the audit reason (never the file
// name, never the bytes). The policy:
//
//   - infected      → refused in EVERY mode, as on the video path.
//   - scan error    → fail-closed (default) refuses; fail-open stores the file
//     unscanned and writes the same audit row the video path
//     writes; quarantine ALSO refuses here, because these
//     object classes have no moderation queue to park in —
//     an avatar cannot be "held for review". That fallback is
//     documented in docs/operations.md rather than silently
//     re-interpreted.
//   - no scanner    → cannot be reached: requireScanner already refused, or the
//     operator opted out and there is nothing to apply.
func (s *Server) scanBeforeStore(ctx context.Context, kind string, data []byte) error {
	if s.filescanner == nil {
		return nil
	}
	clean, err := s.filescanner.ScanBytes(ctx, data)
	switch {
	case err != nil:
		if video.ScanMode(s.cfg.MalwareScanMode) == video.ScanModeFailOpen {
			slog.WarnContext(ctx, "malware scan failed; storing anyway (MALWARE_SCAN_MODE=fail-open)",
				"asset_kind", kind, "error", err.Error())
			s.auditScanSkipped(ctx, kind)
			return nil
		}
		// fail-closed AND quarantine: no queue exists for this object class.
		slog.WarnContext(ctx, "malware scan failed; refusing the upload",
			"asset_kind", kind, "mode", s.cfg.MalwareScanMode, "error", err.Error())
		s.auditScanRejected(ctx, kind, "scan_error")
		return &SafetyScanRejectedError{}
	case !clean:
		s.auditScanRejected(ctx, kind, "infected")
		return &SafetyScanRejectedError{}
	}
	return nil
}

// readScannable reads a bounded body into memory so it can be scanned before it
// is stored. limit is the accepted maximum; one extra byte is read so an
// oversize file is detected rather than silently truncated into the scanner.
func readScannable(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, echo.NewHTTPError(http.StatusRequestEntityTooLarge, "the file is too large")
	}
	return data, nil
}

// auditScanRejected records a non-video safety-scan refusal. The reason carries
// the asset CLASS and the outcome only — never the file name, the bytes, or the
// scanner's error string (which embeds CLAMAV_ADDR).
func (s *Server) auditScanRejected(ctx context.Context, kind, outcome string) {
	if s.auditLog == nil {
		return
	}
	policy := s.cfg.MalwareScanMode
	if policy == "" {
		policy = string(video.ScanModeFailClosed)
	}
	_ = s.auditLog.Record(ctx, audit.Event{
		Action: observability.ActionUploadMalwareRejected,
		Result: observability.ResultFailure,
		Actor:  audit.ActorSnapshot{Kind: "system"},
		Reason: "asset=" + kind + " outcome=" + outcome + " policy=" + policy,
	})
}

// auditScanSkipped records that a non-video file was stored WITHOUT being
// scanned under MALWARE_SCAN_MODE=fail-open.
func (s *Server) auditScanSkipped(ctx context.Context, kind string) {
	if s.auditLog == nil {
		return
	}
	_ = s.auditLog.Record(ctx, audit.Event{
		Action: observability.ActionUploadMalwareSkipped,
		Result: observability.ResultFailure,
		Actor:  audit.ActorSnapshot{Kind: "system"},
		Reason: "asset=" + kind + " reason=scanner_unavailable policy=" + string(video.ScanModeFailOpen),
	})
}

// requireScannerGate is the route-middleware form of requireScanner. It is
// registered AFTER requireAuth (so an anonymous caller still gets 401, not a
// posture disclosure) and before the body-limit middleware, so a client is told
// the instance will not accept the file before it spends bandwidth sending it.
//
// Every user-supplied ingestion route carries it. The exempt paths are the
// DERIVED ones — server-generated storyboards, transcoder renditions, the
// frame-pick poster, and the Whisper transcript — which are produced by this
// instance from bytes the scanner already cleared.
func (s *Server) requireScannerGate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := s.requireScanner(); err != nil {
			return err
		}
		return next(c)
	}
}
