package qoe

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	secretA = []byte("secret-a-secret-a-secret-a-secret-a")
	secretB = []byte("secret-b-secret-b-secret-b-secret-b")
	day1    = time.Date(2026, 8, 23, 11, 0, 0, 0, time.UTC)
)

// TestViewerDigestStableWithinASecretAndDay is the property an incident needs:
// two events from the same viewer on the same day must be recognisable as the
// same viewer.
func TestViewerDigestStableWithinASecretAndDay(t *testing.T) {
	d := NewDigester(secretA)
	morning := d.Viewer(time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC), false, uuid.Nil, "203.0.113.7")
	evening := d.Viewer(time.Date(2026, 8, 23, 23, 59, 0, 0, time.UTC), false, uuid.Nil, "203.0.113.7")
	if morning == "" || morning != evening {
		t.Fatalf("digest changed within a UTC day: %q vs %q", morning, evening)
	}
	// A different viewer on the same day must not collide.
	other := d.Viewer(day1, false, uuid.Nil, "203.0.113.8")
	if other == morning {
		t.Error("two different IPs produced the same digest")
	}
}

// TestViewerDigestDoesNotSurviveTheDay is the privacy property. A per-viewer
// hash with 7 days of retention would let anyone with table access follow one
// viewer for a week; the day inside the MAC'd bytes is what prevents that.
func TestViewerDigestDoesNotSurviveTheDay(t *testing.T) {
	d := NewDigester(secretA)
	today := d.Viewer(day1, false, uuid.Nil, "203.0.113.7")
	tomorrow := d.Viewer(day1.Add(24*time.Hour), false, uuid.Nil, "203.0.113.7")
	if today == tomorrow {
		t.Error("the same viewer produced the same digest on two different days — cross-day linkage must be impossible")
	}
}

// TestViewerDigestDiffersAcrossSecrets pins the documented rotation policy:
// rotating JWT_SECRET re-derives the key, so nothing correlates across the
// rotation. This test exists so that a future change which "fixes" that by
// versioning keys has to argue with a named expectation rather than a silence.
func TestViewerDigestDiffersAcrossSecrets(t *testing.T) {
	a := NewDigester(secretA).Viewer(day1, false, uuid.Nil, "203.0.113.7")
	b := NewDigester(secretB).Viewer(day1, false, uuid.Nil, "203.0.113.7")
	if a == "" || b == "" {
		t.Fatal("digest empty for a configured secret")
	}
	if a == b {
		t.Error("the same viewer digested identically under two different secrets — the digest is not actually keyed")
	}
}

// TestViewerDigestKeyedByPrincipalKind: an account id and an IP must not be able
// to collide, which is what the "u:"/"ip:" prefixes are for.
func TestViewerDigestKeyedByPrincipalKind(t *testing.T) {
	d := NewDigester(secretA)
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	authed := d.Viewer(day1, true, id, "203.0.113.7")
	anon := d.Viewer(day1, false, id, "203.0.113.7")
	if authed == anon {
		t.Error("authenticated and anonymous digests collided for the same request")
	}
	// An authenticated viewer's digest must not depend on which address they
	// happen to be on: that is the whole point of preferring the account id.
	if other := d.Viewer(day1, true, id, "198.51.100.9"); other != authed {
		t.Error("an authenticated viewer's digest changed with their IP")
	}
}

// TestNilDigesterDegradesToEmpty: a weak or missing key must produce NO field
// rather than an unkeyed hash, which is the construction this package exists to
// avoid persisting.
func TestNilDigesterDegradesToEmpty(t *testing.T) {
	if d := NewDigester(nil); d != nil {
		t.Fatal("NewDigester(nil) returned a digester; an absent secret must yield no digester at all")
	}
	var d *Digester
	if got := d.Viewer(day1, false, uuid.Nil, "203.0.113.7"); got != "" {
		t.Errorf("nil digester produced %q, want the empty digest", got)
	}
	// No principal at all is also empty: there is nothing to digest.
	if got := NewDigester(secretA).Viewer(day1, false, uuid.Nil, "  "); got != "" {
		t.Errorf("digest of a blank client IP = %q, want empty", got)
	}
}
