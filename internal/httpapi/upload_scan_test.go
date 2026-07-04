package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/video"
)

// scanFake is an injectable malware Scanner for the upload handler tests: it
// reports a fixed clean/infected verdict or a scan error, so the finalisation
// path can be exercised without a real clamd.
type scanFake struct {
	clean bool
	err   error
}

func (s scanFake) Scan(context.Context, string) (bool, error) { return s.clean, s.err }

// TestUploadVideoFileScanOutcomes closes the noted P6.1 test gap at the HTTP
// boundary: it drives the real POST /videos/:id/file handler with an injected
// Scanner returning infected/error and asserts the upload FINALISES to the
// expected state per MALWARE_SCAN_MODE. The service-layer matrix lives in
// internal/video/scanmode_test.go; this proves the handler surfaces the same
// outcome end to end (wired exactly as cmd/api does: WithScanner + the
// config-driven WithScanMode). The finalisation itself always succeeds (201) —
// the scan verdict is carried in the returned video state, never a 5xx.
func TestUploadVideoFileScanOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		scanner   scanFake
		wantState string
	}{
		// An INFECTED result always fails, independent of the fallback mode.
		{"infected fails (default fail-closed)", "fail-closed", scanFake{clean: false}, "failed"},
		{"infected still fails under fail-open", "fail-open", scanFake{clean: false}, "failed"},
		{"infected still fails under quarantine", "quarantine", scanFake{clean: false}, "failed"},
		// A scan ERROR follows the configured fallback policy.
		{"scan error fails closed", "fail-closed", scanFake{err: errors.New("clamd down")}, "failed"},
		{"scan error publishes fail-open", "fail-open", scanFake{err: errors.New("clamd down")}, "published"},
		{"scan error quarantines", "quarantine", scanFake{err: errors.New("clamd down")}, "quarantined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.MalwareScanEnabled = true
			cfg.MalwareScanMode = tc.mode
			// Mirror cmd/api wiring: the injected scanner + the config-driven mode.
			srv := videoServerCfg(t, cfg,
				video.WithScanner(tc.scanner),
				video.WithScanMode(cfg.MalwareScanMode),
			)
			tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
			id := createVideo(t, srv, tok, "ada", `{"title":"My Draft","privacy":"public"}`)

			rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "pretend this is an mp4", tok)
			if rec.Code != http.StatusCreated {
				t.Fatalf("upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
			}
			var resp uploadVideoFileResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Video.State != tc.wantState {
				t.Errorf("state = %q, want %q (mode=%s)", resp.Video.State, tc.wantState, tc.mode)
			}

			// A quarantined upload must not be publicly visible: an anonymous
			// detail read is hidden (404), proving the finalised state is enforced
			// downstream, not just reported in the upload response.
			if tc.wantState == "quarantined" {
				if got := getVideo(srv, id, ""); got.Code != http.StatusNotFound {
					t.Errorf("anon detail of quarantined video = %d, want 404", got.Code)
				}
			}
		})
	}
}
