package storage

import (
	"context"
	"strings"
	"testing"
)

// The marker key has to be one BOTH backends accept, and the leading dot is the
// part worth pinning: it is the character most likely to be special somewhere.
// validateKey is the rule every method funnels through, so testing it here
// covers S3 as well as Local without needing a bucket.
func TestOwnerMarkerKeyIsValidForEveryBackend(t *testing.T) {
	if err := validateKey(OwnerMarkerKey); err != nil {
		t.Fatalf("validateKey(%q) = %v, want nil — the marker key must be storable", OwnerMarkerKey, err)
	}
	local, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.resolve(OwnerMarkerKey); err != nil {
		t.Fatalf("Local.resolve(%q) = %v, want nil", OwnerMarkerKey, err)
	}
}

func TestOwnerMarkerRoundTrip(t *testing.T) {
	ctx := context.Background()
	b, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Absent is a normal answer, not an error: a fresh store has no marker, and
	// that case has to be distinguishable from "the store would not answer".
	id, found, err := ReadOwnerMarker(ctx, b)
	if err != nil || found || id != "" {
		t.Fatalf("ReadOwnerMarker on an empty store = (%q, %v, %v), want ('', false, nil)", id, found, err)
	}

	const identity = "44444444-4444-4444-8444-444444444444"
	if err := WriteOwnerMarker(ctx, b, identity); err != nil {
		t.Fatalf("WriteOwnerMarker: %v", err)
	}
	id, found, err = ReadOwnerMarker(ctx, b)
	if err != nil || !found {
		t.Fatalf("ReadOwnerMarker after write = (%q, %v, %v)", id, found, err)
	}
	if id != identity {
		t.Errorf("marker = %q, want %q", id, identity)
	}

	// Trailing whitespace is what an operator who edited the object by hand
	// leaves behind, and an identity that fails to match by a newline would read
	// as another install's bucket.
	if _, err := b.Put(ctx, OwnerMarkerKey, strings.NewReader(identity+"\n")); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := ReadOwnerMarker(ctx, b); id != identity {
		t.Errorf("marker with a trailing newline = %q, want %q", id, identity)
	}
}

// An empty identity must never reach the store: a marker holding "" would be
// read back on the next boot, fail to match, and be reported as a conflict with
// an install that does not exist.
func TestWriteOwnerMarkerRefusesAnEmptyIdentity(t *testing.T) {
	ctx := context.Background()
	b, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteOwnerMarker(ctx, b, "  "); err == nil {
		t.Fatal("WriteOwnerMarker accepted a blank identity")
	}
	if _, found, _ := ReadOwnerMarker(ctx, b); found {
		t.Error("a marker was written for a blank identity")
	}
}
