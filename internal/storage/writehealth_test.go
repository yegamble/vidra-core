package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

// The whole point of the monitor: a store that reads perfectly and refuses every
// write must report DOWN, because that is exactly the instance A24 booted green
// and then watched fail its first upload.
func TestWriteHealthReportsARefusedWriteWithItsClass(t *testing.T) {
	b := newFailingBackend(t)
	b.putErr = classifyS3("put", minio.ErrorResponse{Code: "AccessDenied", Message: "Access Denied.", StatusCode: http.StatusForbidden})
	h := NewWriteHealth(b, time.Minute)

	if ok, _ := h.Writable(); !ok {
		t.Fatal("an unprobed monitor must answer writable: no evidence is not evidence of a refusal")
	}
	if err := h.Probe(context.Background()); err == nil {
		t.Fatal("Probe reported success against a store that refused the PUT")
	}
	st := h.Status()
	switch {
	case !st.Probed:
		t.Error("Probed = false after a probe")
	case st.OK:
		t.Error("OK = true after a refused write")
	case st.Class != ClassWriteDenied:
		t.Errorf("Class = %q, want %q", st.Class, ClassWriteDenied)
	case st.At.IsZero():
		t.Error("At is zero, so the admin page cannot say how fresh the verdict is")
	}
	ok, class := h.Writable()
	if ok || class != ClassWriteDenied {
		t.Errorf("Writable() = (%v, %q), want (false, write_denied)", ok, class)
	}
}

func TestWriteHealthReportsAFullBucketAsQuota(t *testing.T) {
	b := newFailingBackend(t)
	b.putErr = classifyS3("put", minio.ErrorResponse{Code: "XMinioAdminBucketQuotaExceeded", Message: "Bucket quota exceeded", StatusCode: http.StatusBadRequest})
	h := NewWriteHealth(b, time.Minute)
	_ = h.Probe(context.Background())
	if got := h.Status().Class; got != ClassQuotaExceeded {
		t.Fatalf("Class = %q, want %q — a full bucket and a revoked key need different fixes", got, ClassQuotaExceeded)
	}
}

// An unrecognised refusal still has to produce a class, or the admin page renders
// an empty sentence beside a down component.
func TestWriteHealthClassesAnUnrecognisedRefusal(t *testing.T) {
	b := newFailingBackend(t)
	b.putErr = errors.New("the store said something nobody has seen before")
	h := NewWriteHealth(b, time.Minute)
	_ = h.Probe(context.Background())
	if got := h.Status().Class; got != ClassWriteDenied {
		t.Fatalf("Class = %q, want %q for an unrecognised refusal", got, ClassWriteDenied)
	}
}

func TestWriteHealthHappyPathLeavesNothingBehind(t *testing.T) {
	b := newFailingBackend(t)
	h := NewWriteHealth(b, time.Minute)
	if err := h.Probe(context.Background()); err != nil {
		t.Fatalf("Probe on a writable store: %v", err)
	}
	st := h.Status()
	if !st.OK || st.Class != "" || st.Leaked {
		t.Fatalf("status = %+v, want a clean OK", st)
	}
	if len(b.puts) != 1 || len(b.deletes) != 1 {
		t.Fatalf("puts=%d deletes=%d, want exactly one of each", len(b.puts), len(b.deletes))
	}
	if !strings.HasPrefix(b.puts[0], WriteProbePrefix) {
		t.Errorf("probe wrote %q, which is not under %q", b.puts[0], WriteProbePrefix)
	}
}

// A store that takes the write and refuses the delete IS writable — the question
// asked was answered yes — but the half-granted key is worth one line.
func TestWriteHealthTreatsAFailedCleanupAsWritable(t *testing.T) {
	b := newFailingBackend(t)
	b.deleteErr = errors.New("not entitled")
	h := NewWriteHealth(b, time.Minute)
	if err := h.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	st := h.Status()
	if !st.OK {
		t.Error("a failed cleanup must not report an unwritable store")
	}
	if !st.Leaked {
		t.Error("Leaked = false, so the operator is never told a scratch object is still there")
	}
}

// A nil monitor is a supported wiring (tests, embedders, unit servers) and must
// never panic or claim a fault.
func TestNilWriteHealthIsUsable(t *testing.T) {
	var h *WriteHealth
	if st := h.Status(); st.Probed {
		t.Error("a nil monitor claims to have probed")
	}
	if ok, _ := h.Writable(); !ok {
		t.Error("a nil monitor refuses writes")
	}
	if err := h.Probe(context.Background()); err != nil {
		t.Errorf("nil Probe: %v", err)
	}
	h.Run(context.Background(), nil)
}

// The log line is the operator's only view of this, and it must carry the class
// and NOT the key or the provider's sentence.
func TestWriteHealthLogsTheClassAndNoKey(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	b := newFailingBackend(t)
	b.putErr = classifyS3("put", minio.ErrorResponse{Code: "AccessDenied", Message: "Access Denied.", StatusCode: http.StatusForbidden})
	h := NewWriteHealth(b, time.Minute)

	was := h.Status()
	_ = h.Probe(context.Background())
	h.LogTransition(context.Background(), logger, was)

	line := buf.String()
	if line == "" {
		t.Fatal("a store that will not accept a write produced no log line at all")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if rec["class"] != string(ClassWriteDenied) {
		t.Errorf("class = %v, want %q", rec["class"], ClassWriteDenied)
	}
	if strings.Contains(line, "Access Denied") {
		t.Errorf("the provider's own sentence reached the log: %s", line)
	}
	if strings.Contains(line, WriteProbePrefix) {
		t.Errorf("the probe object key reached the log: %s", line)
	}

	// A second failure with the same class stays quiet; a recovery does not.
	buf.Reset()
	was = h.Status()
	_ = h.Probe(context.Background())
	h.LogTransition(context.Background(), logger, was)
	if buf.Len() != 0 {
		t.Errorf("an unchanged verdict logged again: %s", buf.String())
	}

	buf.Reset()
	b.putErr = nil
	was = h.Status()
	_ = h.Probe(context.Background())
	h.LogTransition(context.Background(), logger, was)
	if !strings.Contains(buf.String(), "accepts writes again") {
		t.Errorf("recovery was silent: %q", buf.String())
	}
}
