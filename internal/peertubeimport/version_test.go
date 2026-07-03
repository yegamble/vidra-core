package peertubeimport

import "testing"

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
