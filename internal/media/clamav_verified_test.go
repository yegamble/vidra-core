package media

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestScanVerifiedBindsTheVerdictToTheReceivedObject(t *testing.T) {
	const key = "originals/source.mp4"
	const body = "bounded synthetic file"
	correct := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	for _, tc := range []struct {
		name, digest, verdict string
		size                  int64
		clean, fails          bool
	}{
		{"correct", correct, "stream: OK", int64(len(body)), true, false},
		{"wrong size", correct, "stream: OK", int64(len(body)) - 1, false, true},
		{"wrong hash", fmt.Sprintf("%064x", 1), "stream: OK", int64(len(body)), false, true},
		{"detection", correct, "stream: Test FOUND", int64(len(body)), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scanner := NewClamAV(fakeClamd(t, tc.verdict), localWithFile(t, key, body), testScanTimeout)
			clean, err := scanner.ScanVerified(t.Context(), key, tc.size, tc.digest)
			if clean != tc.clean || (err != nil) != tc.fails {
				t.Fatalf("clean=%v, error=%v", clean, err)
			}
		})
	}
}
