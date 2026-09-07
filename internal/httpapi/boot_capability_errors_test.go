package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// The A17/A27 finding, as a unit test on the one thing that decides it: 503 is
// a 5xx, and the central handler scrubs every 5xx message it has no stable code
// for down to "an unexpected error occurred". A bare echo.NewHTTPError(503,
// "platform-URL import is not enabled on this instance") therefore reached the
// caller as nothing at all — the operator sentence existed in the source and
// was measured, twice, never arriving. Typing the error is what keeps it.
//
// It also pins the CODES. A client that branches on "why can I not import"
// needs a stable string, and "service_unavailable" (which is what the scrubber
// produced) is the same string for four unrelated causes.
func TestBootCapabilityErrorsSurviveThe5xxScrubber(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	// Handler() installs the error handler; the assertions below invoke it
	// directly so each error is exercised without standing up five routes.
	_ = srv.Handler()

	for _, tc := range []struct {
		name    string
		err     error
		code    string
		mustSay string
	}{
		{"platform import", &PlatformImportNotConfiguredError{}, "ytdlp_import_not_configured", "YTDLP_IMPORT_ENABLED"},
		{"channel sync", &ChannelSyncNotConfiguredError{}, "channel_sync_not_configured", "YTDLP_IMPORT_ENABLED"},
		{"auto captions", &AutoCaptionsNotConfiguredError{}, "auto_captions_not_configured", "WHISPER_ENDPOINT"},
		{"live", &LiveNotConfiguredError{}, "live_not_configured", "LIVE_RTMP_URL"},
		{"mail", &MailNotConfiguredError{}, "mail_not_configured", "MAIL_ENABLED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c := srv.echo.NewContext(httptest.NewRequest(http.MethodGet, "/probe", nil), rec)
			srv.echo.HTTPErrorHandler(tc.err, c)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
			}
			if body.Error.Code != tc.code {
				t.Errorf("code = %q, want %q", body.Error.Code, tc.code)
			}
			if !strings.Contains(body.Error.Message, tc.mustSay) {
				t.Errorf("message = %q; it must name %s", body.Error.Message, tc.mustSay)
			}
			if strings.Contains(body.Error.Message, "unexpected error") {
				t.Errorf("the scrubber ate the message: %q", body.Error.Message)
			}
		})
	}

	// The control: a BARE 503 still loses its message, which is exactly why
	// each of the above had to be typed.
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(httptest.NewRequest(http.MethodGet, "/probe", nil), rec)
	srv.echo.HTTPErrorHandler(echo.NewHTTPError(http.StatusServiceUnavailable, "set FOO_ENABLED and restart"), c)
	if strings.Contains(rec.Body.String(), "FOO_ENABLED") {
		t.Errorf("a bare 503 kept its message (%s); the scrubbing assumption in this test is stale", rec.Body.String())
	}
}
