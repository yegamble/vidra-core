package ipfs

import (
	"strings"
	"testing"
)

func TestValidateCIDValid(t *testing.T) {
	valid := []string{
		// Canonical CIDv1 dag-pb sha2-256 (the IPFS-docs example).
		"bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		// Deterministically constructed, so the parser is exercised against a CID
		// we know the exact bytes of.
		RawLeafCIDv1([]byte("hello world")),
		RawLeafCIDv1([]byte("")),
		DirCIDv1([]byte("a tree")),
		encodeCIDv1(codecDagCBOR, mhSHA2256, make([]byte, 32)),
		encodeCIDv1(codecRaw, mhBlake2b256, make([]byte, 32)),
	}
	for _, c := range valid {
		if err := ValidateCID(c); err != nil {
			t.Errorf("ValidateCID(%q) = %v, want nil", c, err)
		}
	}
}

func TestValidateCIDInvalid(t *testing.T) {
	cases := []struct {
		name string
		cid  string
	}{
		{"empty", ""},
		{"too long", "b" + strings.Repeat("a", maxCIDLen)},
		{"cidv0 base58", "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG"},
		{"path traversal dotdot", "bafy..beig"},
		{"path separator", "bafy/beig"},
		{"backslash", "bafy\\beig"},
		{"url encoding", "bafy%2fbeig"},
		{"control char", "bafy\x01beig"},
		{"space", "bafy beig"},
		{"non-ascii", "bafybeigé"},
		{"uppercase multibase prefix", "Bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"},
		{"non-b multibase", "zdj7Wk"},
		{"undecodable base32 body", "b1889"},
		{"disallowed codec", encodeCIDv1(0x0129 /* dag-json */, mhSHA2256, make([]byte, 32))},
		{"disallowed multihash", encodeCIDv1(codecRaw, 0x11 /* sha1 */, make([]byte, 20))},
		{"wrong digest length", encodeCIDv1(codecRaw, mhSHA2256, make([]byte, 16))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCID(tc.cid); err == nil {
				t.Errorf("ValidateCID(%q) = nil, want an error", tc.cid)
			}
		})
	}
}

// TestRawLeafCIDv1Deterministic asserts content-addressing: identical bytes ⇒
// identical CID (the property the reference-checked unpin dedupe relies on), and
// different bytes ⇒ different CID.
func TestRawLeafCIDv1Deterministic(t *testing.T) {
	a := RawLeafCIDv1([]byte("same"))
	b := RawLeafCIDv1([]byte("same"))
	c := RawLeafCIDv1([]byte("different"))
	if a != b {
		t.Errorf("identical content produced different CIDs: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("different content produced the same CID: %q", a)
	}
	// raw+sha256 CIDv1 strings share the well-known bafkrei… prefix.
	if !strings.HasPrefix(a, "bafkrei") {
		t.Errorf("RawLeafCIDv1 = %q, want a bafkrei… (raw/sha2-256) CID", a)
	}
}

// FuzzValidateCID must never panic and must be self-consistent: any input it
// accepts must still validate on a second pass (idempotent), and an accepted CID
// must be within the length cap and start with 'b'.
func FuzzValidateCID(f *testing.F) {
	seeds := []string{
		"", "b", "Qm", "bafy/beig", "bafy..beig",
		"bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		RawLeafCIDv1([]byte("fuzz")),
		DirCIDv1([]byte("dir")),
		string([]byte{'b', 0x00, 0x01}),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cid string) {
		err := ValidateCID(cid)
		if err != nil {
			return
		}
		// Accepted: assert the invariants and idempotency.
		if len(cid) == 0 || len(cid) > maxCIDLen {
			t.Fatalf("accepted CID with bad length: %d", len(cid))
		}
		if cid[0] != 'b' {
			t.Fatalf("accepted CID not starting with 'b': %q", cid)
		}
		if strings.ContainsAny(cid, "/\\%") || strings.Contains(cid, "..") {
			t.Fatalf("accepted CID with traversal chars: %q", cid)
		}
		if err2 := ValidateCID(cid); err2 != nil {
			t.Fatalf("ValidateCID not idempotent: first nil, second %v for %q", err2, cid)
		}
	})
}

// TestClientConstructors is a light guard that the exported constructors are
// wired (no network).
func TestClientConstructors(t *testing.T) {
	if NewKuboClient("http://ipfs:5001/", nil) == nil {
		t.Fatal("NewKuboClient returned nil")
	}
	if NewFakeIPFSClient() == nil {
		t.Fatal("NewFakeIPFSClient returned nil")
	}
}
