//go:build integration

// These tests exercise a REAL clamd daemon and so are excluded from the default
// `make ci` gate (which must stay green on hosts without clamd). Bring up the
// compose `scan` profile and point CLAMAV_TEST_ADDR at it:
//
//	docker compose --profile scan up -d clamav
//	CLAMAV_TEST_ADDR=localhost:3310 go test -tags integration ./internal/media/
//
// The clamav/clamav image's first boot downloads signature databases (freshclam);
// give it up to ~2 minutes before the daemon accepts scans.
package media

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// eicarTestString is the standard, industry-agreed anti-malware test file. It is
// harmless — every scanner recognises it as "EICAR-Test-File" without shipping a
// real virus. Assembled from fragments so this source file itself never contains
// the contiguous signature (which some editors/scanners would flag).
var eicarTestString = strings.Join([]string{
	`X5O!P%@AP[4\PZX54(P^)7CC)7}`,
	`$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`,
}, "")

func realClamd(t *testing.T) (*ClamAV, storage.Backend) {
	t.Helper()
	addr := os.Getenv("CLAMAV_TEST_ADDR")
	if addr == "" {
		t.Skip("CLAMAV_TEST_ADDR not set; skipping real-clamd integration test")
	}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	// 2 minutes tolerates a freshly-booted clamd still loading its databases.
	return NewClamAV(addr, blobs, 2*time.Minute), blobs
}

func TestClamAVRealDetectsEICAR(t *testing.T) {
	sc, blobs := realClamd(t)
	if _, err := blobs.Put(context.Background(), "originals/eicar.bin", strings.NewReader(eicarTestString)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	clean, err := sc.Scan(context.Background(), "originals/eicar.bin")
	if err != nil {
		t.Fatalf("Scan returned err (clamd not ready?): %v", err)
	}
	if clean {
		t.Error("real clamd reported the EICAR test file as clean; expected INFECTED")
	}
}

func TestClamAVRealPassesBenign(t *testing.T) {
	sc, blobs := realClamd(t)
	if _, err := blobs.Put(context.Background(), "originals/benign.bin", strings.NewReader("just some harmless bytes for vidra")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	clean, err := sc.Scan(context.Background(), "originals/benign.bin")
	if err != nil {
		t.Fatalf("Scan returned err (clamd not ready?): %v", err)
	}
	if !clean {
		t.Error("real clamd reported a benign file as infected")
	}
}
