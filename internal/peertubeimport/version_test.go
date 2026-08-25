package peertubeimport

import (
	"errors"
	"testing"
)

func TestClassifyVersion(t *testing.T) {
	cases := []struct {
		v    int
		want VersionSupport
	}{
		{0, VersionUnknown},
		{-5, VersionUnknown},
		{MinSupportedSchemaVersion - 1, VersionTooOld},
		{MinSupportedSchemaVersion, VersionSupported},
		{(MinSupportedSchemaVersion + MaxSupportedSchemaVersion) / 2, VersionSupported},
		{MaxSupportedSchemaVersion, VersionSupported},
		{MaxSupportedSchemaVersion + 1, VersionTooNew},
	}
	for _, c := range cases {
		if got := ClassifyVersion(c.v); got != c.want {
			t.Errorf("ClassifyVersion(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestIsSupportedAndError(t *testing.T) {
	if !IsSupported(MinSupportedSchemaVersion) {
		t.Error("min version should be supported")
	}
	if IsSupported(0) {
		t.Error("unknown version must not be supported")
	}
	if VersionError(MinSupportedSchemaVersion) != nil {
		t.Error("supported version must have nil error")
	}
	// Unknown/too-old/too-new all produce a non-nil, operator-facing error.
	for _, v := range []int{0, MinSupportedSchemaVersion - 1, MaxSupportedSchemaVersion + 1} {
		if VersionError(v) == nil {
			t.Errorf("version %d must produce an error", v)
		}
	}
}

// The refusal has to be readable by a MACHINE, not only by a person: the admin
// UI must be able to tell "your source schema is unverified, and here is the
// number" apart from every other way a run can fail, because only the first of
// those is something an administrator can sign off on.
func TestVersionErrorIsStructured(t *testing.T) {
	cases := []struct {
		v        int
		wantCode string
	}{
		{MaxSupportedSchemaVersion + 40, CodeUnverifiedSchema}, // the 1040 case
		{MinSupportedSchemaVersion - 1, CodeUnverifiedSchema},
		{0, CodeUndetectableSchema},
		{-1, CodeUndetectableSchema},
	}
	for _, c := range cases {
		err := VersionError(c.v)
		var refusal *UnverifiedSchemaError
		if !errors.As(err, &refusal) {
			t.Fatalf("VersionError(%d) = %T, want *UnverifiedSchemaError", c.v, err)
		}
		if refusal.Code() != c.wantCode {
			t.Errorf("VersionError(%d).Code() = %q, want %q", c.v, refusal.Code(), c.wantCode)
		}
		if refusal.Version != c.v {
			t.Errorf("VersionError(%d).Version = %d, want %d", c.v, refusal.Version, c.v)
		}
		// The prose still names the detected version and the verified range, and
		// carries nothing about the source but that integer.
		if refusal.Error() == "" {
			t.Errorf("VersionError(%d) has an empty message", c.v)
		}
	}
	if VersionError(MinSupportedSchemaVersion) != nil {
		t.Error("a supported version must produce no refusal at all")
	}
}

// AcknowledgesVersion is the whole of the widening this change makes. It opens
// for exactly one thing: an acknowledgement NAMING the version that was actually
// detected. Anything else — a blank acknowledgement, a stale one, a "yes" that
// names a different number, or one aimed at a source whose version could not be
// read at all — leaves the gate shut.
func TestAcknowledgesVersion(t *testing.T) {
	cases := []struct {
		name          string
		ack, detected int
		want          bool
	}{
		{"names the detected version", 1040, 1040, true},
		{"no acknowledgement", 0, 1040, false},
		{"negative is not an acknowledgement", -1, 1040, false},
		{"names a different version", 1000, 1040, false},
		{"stale: source moved on since the dry run", 1040, 1055, false},
		{"cannot acknowledge an undetectable version", 1040, 0, false},
		{"zero never matches zero", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AcknowledgesVersion(c.ack, c.detected); got != c.want {
				t.Errorf("AcknowledgesVersion(%d, %d) = %v, want %v", c.ack, c.detected, got, c.want)
			}
		})
	}
}
