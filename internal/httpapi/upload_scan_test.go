package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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
// config-driven WithScanMode).
//
// A28 found the creator-facing half of this missing: a rejection came back 201
// with state:"failed" and nothing a creator could read. A refusal is now a 422
// carrying video.SafetyScanRejectedCode and the one neutral sentence; the
// outcomes that are NOT refusals (published, quarantined) still answer 201 with
// the state.
func TestUploadVideoFileScanOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		scanner   scanFake
		wantState string // "" when the request is refused instead
		wantCode  int
	}{
		// An INFECTED result always fails, independent of the fallback mode —
		// and the creator is told so.
		{"infected refused (default fail-closed)", "fail-closed", scanFake{clean: false}, "", http.StatusUnprocessableEntity},
		{"infected still refused under fail-open", "fail-open", scanFake{clean: false}, "", http.StatusUnprocessableEntity},
		{"infected still refused under quarantine", "quarantine", scanFake{clean: false}, "", http.StatusUnprocessableEntity},
		// A scan ERROR follows the configured fallback policy.
		{"scan error refused fail-closed", "fail-closed", scanFake{err: errors.New("clamd down")}, "", http.StatusUnprocessableEntity},
		{"scan error publishes fail-open", "fail-open", scanFake{err: errors.New("clamd down")}, "published", http.StatusCreated},
		{"scan error quarantines", "quarantine", scanFake{err: errors.New("clamd down")}, "quarantined", http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			// The scanner is wired, so this deployment is NOT the opted-out one
			// testConfig describes: give it an address, which is what turns
			// scanning on now.
			cfg.MalwareScanEnabled = true
			cfg.ClamAVAddr = "127.0.0.1:3310"
			cfg.MalwareScanMode = tc.mode
			// Mirror cmd/api wiring: the injected scanner + the config-driven mode.
			srv := videoServerCfg(t, cfg,
				video.WithScanner(tc.scanner),
				video.WithScanMode(cfg.MalwareScanMode),
			)
			tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
			id := createVideo(t, srv, tok, "ada", `{"title":"My Draft","privacy":"public"}`)

			rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "pretend this is an mp4", tok)
			if rec.Code != tc.wantCode {
				t.Fatalf("upload = %d, want %d; body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantState == "" {
				// The refusal: the stable code, the exact neutral sentence, and
				// NOTHING about the scanner or what it found.
				var errResp ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if errResp.Error.Code != video.SafetyScanRejectedCode {
					t.Errorf("code = %q, want %q", errResp.Error.Code, video.SafetyScanRejectedCode)
				}
				if errResp.Error.Message != video.SafetyScanRejectedMessage {
					t.Errorf("message = %q, want the neutral sentence %q", errResp.Error.Message, video.SafetyScanRejectedMessage)
				}
				for _, leak := range []string{"malware", "virus", "clam", "ClamAV", "Eicar", "3310"} {
					if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(leak)) {
						t.Errorf("refusal body leaks %q: %s", leak, rec.Body.String())
					}
				}
				return
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
