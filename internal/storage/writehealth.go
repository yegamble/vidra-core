package storage

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// WriteProbeInterval is how often a running instance re-asks whether it can
// still store a byte.
//
// Five minutes, and deliberately its OWN ticker rather than a hook on the
// settings poller: the poller is role-gated (it is not wired in worker-role
// processes at all), and the worker is precisely the process whose ability to
// write must not go unwatched. It is also the process that discovers a revoked
// key the slowest — a transcode can run for minutes before its first PUT.
//
// The cost is one PUT and one DELETE of a 60-byte object every five minutes per
// process, at a key nothing else uses (WriteProbePrefix), outside every swept
// prefix.
const WriteProbeInterval = 5 * time.Minute

// WriteStatus is the last thing the write probe learned. The zero value means
// "never probed", which is NOT a fault: an embedder or a test that never wired
// a monitor has not failed a probe, it has skipped one.
type WriteStatus struct {
	// Probed reports that at least one probe has completed.
	Probed bool
	// OK reports that the most recent probe stored and removed an object.
	OK bool
	// Class is why the most recent probe failed. Empty while OK.
	Class ErrorClass
	// At is when the most recent probe finished (UTC).
	At time.Time
	// Leaked reports that the probe wrote its scratch object and could not
	// remove it again. It is NOT a failure — the question asked was "can this
	// credential write", and the answer was yes — but it is worth an operator's
	// attention, because on Backblaze B2 deleteFiles is granted separately from
	// writeFiles and this is what a half-granted key looks like.
	Leaked bool
}

// WriteHealth keeps one backend's write verdict fresh.
//
// It exists because every OTHER probe in this codebase is a read. EnsureBucket
// is a HeadBucket, the ownership-marker read is a GET, the emptiness check is a
// list, and an S3 credential scoped to reads passes all three — so an instance
// boots green, serves every page, and fails the first upload (A24 measured
// exactly that, and storage.ProbeWrite's own comment records the migration that
// failed 1,321 uploads on such a key). This is the one probe that asks the
// question the answer to which decides whether the instance can do its job.
//
// A nil *WriteHealth is usable: every method is nil-safe and reports "never
// probed", so no test, embedder or single-process install has to wire one.
type WriteHealth struct {
	backend  Backend
	interval time.Duration

	mu     sync.RWMutex
	status WriteStatus
}

// NewWriteHealth returns a monitor for b. A zero interval means
// WriteProbeInterval; a nil backend returns nil, so the caller can pass the
// result straight through without a branch.
func NewWriteHealth(b Backend, interval time.Duration) *WriteHealth {
	if b == nil {
		return nil
	}
	if interval <= 0 {
		interval = WriteProbeInterval
	}
	return &WriteHealth{backend: b, interval: interval}
}

// Status returns the latest verdict.
func (h *WriteHealth) Status() WriteStatus {
	if h == nil {
		return WriteStatus{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.status
}

// Writable reports whether the instance may currently be asked to store
// something, and why not when it may not.
//
// An UNPROBED monitor answers true. That is the important half: this gate must
// never be the reason work stops on an instance that has simply not been asked
// yet, and "no evidence" is not evidence of a refusal.
func (h *WriteHealth) Writable() (bool, ErrorClass) {
	st := h.Status()
	if !st.Probed || st.OK {
		return true, ""
	}
	return false, st.Class
}

// Probe runs one write probe and records the verdict. The returned error is the
// probe's own (unredacted, for the caller's log); Status carries the class.
//
// An unrecognised refusal is recorded as ClassWriteDenied rather than left
// blank: the store was asked to accept an object and did not, which is literally
// what write_denied names, and a blank class would render as an empty sentence
// on the admin page.
func (h *WriteHealth) Probe(ctx context.Context) error {
	if h == nil || h.backend == nil {
		return nil
	}
	res, err := ProbeWrite(ctx, h.backend)
	st := WriteStatus{Probed: true, At: time.Now().UTC(), Leaked: res.Leaked()}
	if err != nil {
		st.Class = ClassWriteDenied
		if c, ok := ClassOf(err); ok {
			st.Class = c
		}
	} else {
		st.OK = true
	}
	h.mu.Lock()
	h.status = st
	h.mu.Unlock()
	return err
}

// RecordDenied records a refusal this monitor did not have to probe for.
//
// Boot uses it for the migration target: EnsureBucket has already been refused
// with write_denied, and a PUT to a store that would not confirm its own bucket
// to this credential only spends a round trip learning the same thing. Recording
// it directly is what lets the process come up degraded — with the campaign
// paused and a class to show an operator — instead of exiting before the
// listener opens, which is what it used to do.
//
// The recorded verdict is not sticky: the next Probe on the ticker overwrites
// it, which is exactly how a repaired credential un-pauses the campaign without
// anybody restarting anything.
func (h *WriteHealth) RecordDenied(class ErrorClass) {
	if h == nil {
		return
	}
	if class == "" {
		class = ClassWriteDenied
	}
	h.mu.Lock()
	h.status = WriteStatus{Probed: true, At: time.Now().UTC(), Class: class}
	h.mu.Unlock()
}

// Run probes on the monitor's interval until ctx is cancelled. It does NOT probe
// immediately: boot has already taken the first sample synchronously (see
// cmd/api), and a second one a millisecond later would only cost the store a
// round trip.
func (h *WriteHealth) Run(ctx context.Context, logger *slog.Logger) {
	if h == nil || h.backend == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	t := time.NewTicker(h.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			was := h.Status()
			_ = h.Probe(ctx)
			h.LogTransition(ctx, logger, was)
		}
	}
}

// LogTransition writes a line only when the verdict CHANGED, so a store that has
// been refusing writes for a week does not fill the log with the same sentence
// every five minutes — and a recovery is never silent.
//
// It logs the CLASS and nothing else. Not the probe key, not the object key, not
// the provider's sentence: the class is the whole actionable content, and the
// alternative is a log line that carries a bucket layout past whatever reads it.
func (h *WriteHealth) LogTransition(ctx context.Context, logger *slog.Logger, was WriteStatus) {
	if h == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	now := h.Status()
	switch {
	case !now.Probed:
		return
	case !now.OK && (!was.Probed || was.OK || was.Class != now.Class):
		logger.WarnContext(ctx, "storage: the object store did not accept a write probe",
			"class", string(now.Class),
			"consequence", "nothing this instance stores can land: uploads, thumbnails, captions and transcode output all fail until this is fixed")
	case now.OK && was.Probed && !was.OK:
		logger.InfoContext(ctx, "storage: the object store accepts writes again")
	case now.OK && now.Leaked && !was.Leaked:
		// Writable, but a half-granted key: worth saying once.
		logger.WarnContext(ctx, "storage: the write probe could not remove its own scratch object",
			"prefix", WriteProbePrefix,
			"consequence", "writes work but deletes may not, so media garbage collection and video deletion will leave objects behind (on Backblaze B2, deleteFiles is granted separately from writeFiles)")
	}
}
