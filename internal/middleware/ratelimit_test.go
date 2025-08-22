package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func hit(handler http.Handler, r *http.Request, n int) (codes []int) {
	for i := 0; i < n; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, r)
		codes = append(codes, rr.Code)
	}
	return
}

func TestRateLimit_IgnoresForwardedByDefault(t *testing.T) {
	// burst=2 per 100ms window; 3rd call should be 429 using RemoteAddr identity
	rl := RateLimit(100*time.Millisecond, 2)
	h := rl(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:12345"
	req.Header.Set("X-Forwarded-For", "9.8.7.6")

	codes := hit(h, req, 3)
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("unexpected codes: %v", codes)
	}
}

func TestRateLimitWithTrust_UsesForwardedHeader(t *testing.T) {
	rl := RateLimitWithTrust(100*time.Millisecond, 2, true)
	h := rl(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	// Two different clients behind a proxy via XFF should each have their own budget
	reqA := httptest.NewRequest(http.MethodGet, "/", nil)
	reqA.RemoteAddr = "127.0.0.1:12345" // proxy
	reqA.Header.Set("X-Forwarded-For", "10.0.0.1")

	reqB := httptest.NewRequest(http.MethodGet, "/", nil)
	reqB.RemoteAddr = "127.0.0.1:12345"
	reqB.Header.Set("X-Forwarded-For", "10.0.0.2")

	codesA := hit(h, reqA, 2)
	codesB := hit(h, reqB, 2)

	if codesA[0] != 200 || codesA[1] != 200 {
		t.Fatalf("unexpected A codes: %v", codesA)
	}
	if codesB[0] != 200 || codesB[1] != 200 {
		t.Fatalf("unexpected B codes: %v", codesB)
	}
}
