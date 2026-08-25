package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// failingBackend is a store that breaks exactly one of the two calls the probe
// makes. It embeds a real Local so everything else behaves, which is what makes
// a failure in a test attributable to the line that caused it.
type failingBackend struct {
	Backend
	putErr    error
	deleteErr error

	puts    []string
	deletes []string
}

func newFailingBackend(t *testing.T) *failingBackend {
	t.Helper()
	local, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return &failingBackend{Backend: local}
}

func (b *failingBackend) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	b.puts = append(b.puts, key)
	if b.putErr != nil {
		return 0, b.putErr
	}
	return b.Backend.Put(ctx, key, r)
}

func (b *failingBackend) Delete(ctx context.Context, key string) error {
	b.deletes = append(b.deletes, key)
	if b.deleteErr != nil {
		return b.deleteErr
	}
	return b.Backend.Delete(ctx, key)
}

// sizedRecorder is a store that records the length it was told to expect, so the
// probe can be held to the same single-PUT path every other caller uses.
type sizedRecorder struct {
	Backend
	sawSize int64
}

func (b *sizedRecorder) PutSized(ctx context.Context, key string, r io.Reader, size int64) (int64, error) {
	b.sawSize = size
	return b.Backend.Put(ctx, key, r)
}

// The probe's key has to be storable by every backend and must never be
// mistaken for — or collide with — the ownership marker, which is the one object
// in this store whose presence is an adoption decision.
func TestWriteProbeKeyIsValidAndIsNotTheOwnerMarker(t *testing.T) {
	local, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		key := newWriteProbeKey()
		if err := validateKey(key); err != nil {
			t.Fatalf("validateKey(%q) = %v, want nil — the probe key must be storable", key, err)
		}
		if _, err := local.resolve(key); err != nil {
			t.Fatalf("Local.resolve(%q) = %v, want nil", key, err)
		}
		if key == OwnerMarkerKey || strings.HasPrefix(OwnerMarkerKey, key) || strings.HasPrefix(key, OwnerMarkerKey) {
			t.Fatalf("probe key %q overlaps the ownership marker %q", key, OwnerMarkerKey)
		}
		if !strings.HasPrefix(key, WriteProbePrefix) {
			t.Errorf("probe key %q is not under %q, so an operator cannot tell what it is", key, WriteProbePrefix)
		}
	}
}

// Two probes must not share a key: doctor, the api and a migration preflight can
// all run at once, and a shared key means one probe's delete removes the object
// another is still proving it wrote.
func TestWriteProbeKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 32 {
		key := newWriteProbeKey()
		if seen[key] {
			t.Fatalf("newWriteProbeKey repeated %q", key)
		}
		seen[key] = true
	}
}

// The happy path: the probe proves a write and leaves the store exactly as it
// found it.
func TestProbeWriteLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	b, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := ProbeWrite(ctx, b)
	if err != nil {
		t.Fatalf("ProbeWrite on a writable store: %v", err)
	}
	if !res.Wrote {
		t.Error("Wrote = false on a store that took the object")
	}
	if res.CleanupErr != nil || res.Leaked() {
		t.Errorf("cleanup failed on a writable store: %v", res.CleanupErr)
	}
	if res.Key == "" {
		t.Fatal("the result does not name the key it used")
	}
	if ok, _ := b.Exists(ctx, res.Key); ok {
		t.Errorf("the probe object %q is still there", res.Key)
	}
	// The ownership marker is the one object the probe must never touch: writing
	// it is an adoption, and doctor's whole ownership check reads it.
	if ok, _ := b.Exists(ctx, OwnerMarkerKey); ok {
		t.Error("the probe wrote the ownership marker — a diagnostic must never make an adoption decision")
	}
}

// A credential that cannot PUT is the failure this whole probe exists for: a
// destination B2 key with readFiles and not writeFiles, which nothing in the
// codebase noticed until 1,321 avatar uploads had failed.
func TestProbeWriteReportsARefusedWrite(t *testing.T) {
	ctx := context.Background()
	b := newFailingBackend(t)
	b.putErr = errors.New("not entitled")

	res, err := ProbeWrite(ctx, b)
	if err == nil {
		t.Fatal("ProbeWrite succeeded against a store that refused the PUT")
	}
	if !errors.Is(err, b.putErr) {
		t.Errorf("error = %v, want it to wrap the store's own refusal", err)
	}
	if res.Wrote {
		t.Error("Wrote = true after a refused PUT")
	}
	// Cleanup runs on the failure path too: a PUT can fail after the object has
	// landed, and a probe that leaves scratch objects behind on the path it is
	// most likely to take is not a probe anyone should run against production.
	if len(b.deletes) == 0 {
		t.Error("no cleanup was attempted after a failed PUT")
	}
	if res.Key == "" {
		t.Error("the result does not name the key it tried, so a leaked object cannot be found")
	}
}

// A credential that can write and cannot delete is a real and separate
// condition (B2 grants deleteFiles separately from writeFiles). The write
// SUCCEEDED, so the probe must say so rather than reporting the store
// unwritable — and it must hand back the key it could not remove.
func TestProbeWriteSurvivesAFailedCleanup(t *testing.T) {
	ctx := context.Background()
	b := newFailingBackend(t)
	b.deleteErr = errors.New("not entitled")

	res, err := ProbeWrite(ctx, b)
	if err != nil {
		t.Fatalf("a failed cleanup turned a successful write into a failure: %v", err)
	}
	if !res.Wrote {
		t.Error("Wrote = false after a PUT the store accepted")
	}
	if res.CleanupErr == nil || !res.Leaked() {
		t.Fatal("the failed cleanup was not reported, so the leaked object is invisible")
	}
	if !strings.Contains(res.CleanupErr.Error(), "not entitled") {
		t.Errorf("CleanupErr = %v, want the store's own reason", res.CleanupErr)
	}
}

// The probe passes the exact length like every other writer in this package: an
// unknown length turns a 60-byte object into a multipart upload with a 16 MiB
// part buffer, which is an absurd cost for a diagnostic.
func TestProbeWriteTellsTheBackendTheLength(t *testing.T) {
	local, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := &sizedRecorder{Backend: local, sawSize: -2}
	if _, err := ProbeWrite(context.Background(), rec); err != nil {
		t.Fatalf("ProbeWrite: %v", err)
	}
	if rec.sawSize != int64(len(writeProbeBody)) {
		t.Errorf("PutSized saw size %d, want %d", rec.sawSize, len(writeProbeBody))
	}
}

// The body is what an operator finds if a probe is killed between the PUT and
// the DELETE. It has to explain itself, and it has to be small.
func TestWriteProbeBodyExplainsItself(t *testing.T) {
	if len(writeProbeBody) > 200 {
		t.Errorf("the probe body is %d bytes; it is written to production storage and should be trivial", len(writeProbeBody))
	}
	for _, want := range []string{"vidra", "delete"} {
		if !strings.Contains(strings.ToLower(writeProbeBody), want) {
			t.Errorf("the probe body does not mention %q: %q", want, writeProbeBody)
		}
	}
}
