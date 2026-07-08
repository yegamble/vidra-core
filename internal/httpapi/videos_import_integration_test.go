//go:build integration

// EICAR-through-import proof (backport W2.C1, UPLOAD-09). This exercises the
// yt-dlp import path against a REAL clamd, so it is excluded from the default
// `make ci` gate and self-skips unless CLAMAV_TEST_ADDR points at a running
// clamd (bring up the compose `scan` profile, as the sibling media test does):
//
//	docker compose --profile scan up -d clamav
//	CLAMAV_TEST_ADDR=localhost:3310 go test -tags integration ./internal/httpapi/
//
// It delivers the industry-standard EICAR test string through the ytdlp resolver
// (a stub extractor stands in for the yt-dlp binary — the binary is not the unit
// under test; the SCAN HOOK on the import path is) and asserts the imported body
// finishes failed/quarantined per MALWARE_SCAN_MODE, NEVER published. This is the
// end-to-end guarantee that every W2 ingestion path lands bytes exclusively via
// AttachOriginal → Process.
package httpapi

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/videoimport"
	"github.com/vidra/vidra-core/internal/ytdlp"
)

// eicarBytes is the standard anti-malware test file, assembled from fragments so
// this source file never contains the contiguous signature.
var eicarBytes = []byte(strings.Join([]string{
	`X5O!P%@AP[4\PZX54(P^)7CC)7}`,
	`$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`,
}, ""))

func TestImportViaYtdlpEicarRealClamdNeverPublishes(t *testing.T) {
	addr := os.Getenv("CLAMAV_TEST_ADDR")
	if addr == "" {
		t.Skip("CLAMAV_TEST_ADDR not set; skipping real-clamd EICAR-through-import test")
	}

	cfg := testConfig()
	// videoServerFull exposes the SAME blob backend + video repo the HTTP API
	// writes to, so a second (scanner-wired) video service can run the import over
	// the identical storage and state.
	srv, blobs, _, _, vrepo := videoServerFull(t, cfg)
	tok := createChannelFor(t, srv, "eve", "eve@example.test", "eve")
	id := createVideo(t, srv, tok, "eve", `{"title":"Evil import","privacy":"public"}`)
	vid := uuid.MustParse(id)

	// A real clamd scanner over the harness storage; fail-closed so an infected
	// body must end 'failed'.
	sc := media.NewClamAV(addr, blobs, 2*time.Minute) // 2m tolerates a freshly-booted clamd
	scanVideosvc := video.NewService(vrepo, blobs,
		video.WithScanner(sc),
		video.WithScanMode("fail-closed"),
	)
	imp := videoimport.NewService(newImportFakeRepo(), scanVideosvc, 1<<20,
		videoimport.WithYtdlp(stubExtractor{body: eicarBytes, meta: ytdlp.Meta{Title: "Evil"}}, t.TempDir()),
	)

	if _, err := imp.Enqueue(context.Background(), vid, "https://platform.example/watch?v=evil", videoimport.ResolverYtdlp); err != nil {
		t.Fatalf("enqueue ytdlp import: %v", err)
	}
	if _, err := imp.DrainJobs(context.Background(), 5); err != nil {
		t.Fatalf("drain: %v", err)
	}

	got, err := scanVideosvc.GetByID(context.Background(), vid)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.State == "published" {
		t.Fatalf("EICAR-through-import PUBLISHED the video — the scan hook did not fire on the ytdlp path")
	}
	if got.State != "failed" {
		t.Errorf("EICAR import state = %q, want failed (fail-closed)", got.State)
	}
}
