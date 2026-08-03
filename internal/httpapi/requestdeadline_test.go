package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// runWithDeadline drives the requestDeadline middleware over one route template
// and returns whatever the handler observed. It exercises the middleware
// directly (rather than a full server) so the assertions are about the deadline
// itself, not about any handler's behaviour.
func runWithDeadline(t *testing.T, method, template string, general, stream time.Duration, h echo.HandlerFunc) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(method, "/", nil)
	c := e.NewContext(req, httptest.NewRecorder())
	c.SetPath(template)
	if err := requestDeadline(general, stream)(h)(c); err != nil {
		t.Fatalf("%s %s: %v", method, template, err)
	}
}

// TestStreamingRoutesGetTheLongerDeadline is the regression test for the upload
// failure: resumable chunks are 8 MiB, so the general 30s deadline required a
// sustained ~2.2 Mbit/s uplink and typical residential/mobile uploaders retried
// the same chunk forever. The streaming routes must carry the (much longer)
// HTTP_STREAM_REQUEST_TIMEOUT instead.
func TestStreamingRoutesGetTheLongerDeadline(t *testing.T) {
	const general, stream = 30 * time.Second, time.Hour

	cases := []struct {
		method, template string
		wantStream       bool
	}{
		{http.MethodPut, uploadChunkRoutePath, true},
		{http.MethodPost, uploadRoutePath, true},
		{http.MethodPost, replaceRoutePath, true},
		{http.MethodPost, attachmentUploadRoutePath, true},
		{http.MethodGet, "/api/v1/videos/:id/hls/:rendition/:file", true},
		{http.MethodGet, "/api/v1/videos/:id/download/original", true},
		// Ordinary API work stays on the tight budget: a request still running
		// after 30s there is wedged, not slow.
		{http.MethodGet, "/api/v1/videos", false},
		{http.MethodPost, "/api/v1/channels/:handle/videos", false},
	}

	for _, tc := range cases {
		runWithDeadline(t, tc.method, tc.template, general, stream, func(c echo.Context) error {
			deadline, ok := c.Request().Context().Deadline()
			if !ok {
				t.Errorf("%s %s: no deadline set at all", tc.method, tc.template)
				return nil
			}
			// Compare against the midpoint so the assertion is immune to the
			// microseconds spent getting here.
			longer := time.Until(deadline) > (general+stream)/2
			if longer != tc.wantStream {
				t.Errorf("%s %s: remaining budget %v, want the %s deadline",
					tc.method, tc.template, time.Until(deadline),
					map[bool]string{true: "streaming", false: "general"}[tc.wantStream])
			}
			return nil
		})
	}
}

// TestExemptedUploadRouteIsNotCancelledAtTheGeneralDeadline proves the exemption
// behaviourally rather than by reading the deadline value: the handler outlives
// the general deadline and its context is still live. The general budget is
// scaled down to milliseconds so the test is instant; the ratio is what matters.
func TestExemptedUploadRouteIsNotCancelledAtTheGeneralDeadline(t *testing.T) {
	const general, stream = 5 * time.Millisecond, time.Minute

	var uploadErr, apiErr error
	// The chunk PUT — the route that could never finish before — survives well
	// past the general deadline.
	runWithDeadline(t, http.MethodPut, uploadChunkRoutePath, general, stream, func(c echo.Context) error {
		time.Sleep(20 * time.Millisecond)
		uploadErr = c.Request().Context().Err()
		return nil
	})
	if uploadErr != nil {
		t.Errorf("chunk upload context = %v after outliving the general deadline, want live", uploadErr)
	}

	// The control: an ordinary API route in the same situation IS cancelled, so
	// the test above is proving the exemption and not a broken deadline.
	runWithDeadline(t, http.MethodGet, "/api/v1/videos", general, stream, func(c echo.Context) error {
		time.Sleep(20 * time.Millisecond)
		apiErr = c.Request().Context().Err()
		return nil
	})
	if !errors.Is(apiErr, context.DeadlineExceeded) {
		t.Errorf("API context = %v, want context.DeadlineExceeded", apiErr)
	}
}

// TestAdminJobEventsStreamHasNoDeadline keeps the SSE exemption intact — it owns
// a bounded, token-expiry-capped lifetime inside its own handler, so a context
// deadline here would cut the stream instead.
func TestAdminJobEventsStreamHasNoDeadline(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, adminJobEventsPath, nil)
	c := e.NewContext(req, httptest.NewRecorder())
	c.SetPath(adminJobEventsPath)

	err := requestDeadline(time.Second, time.Hour)(func(c echo.Context) error {
		if _, ok := c.Request().Context().Deadline(); ok {
			t.Error("the admin job-events SSE stream must not carry a request deadline")
		}
		return nil
	})(c)
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
}
