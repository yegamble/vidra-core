package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRateLimiter_Allow(t *testing.T) {
	rate := 100 * time.Millisecond
	burst := 3

	rl := NewRateLimiter(rate, burst)

	ip := "192.168.1.1"

	// First 3 requests should be allowed
	for i := 1; i <= burst; i++ {
		if !rl.Allow(ip) {
			t.Errorf("Request %d should be allowed", i)
		}
	}

	// 4th request should be blocked
	if rl.Allow(ip) {
		t.Error("Request 4 should be blocked")
	}

	// Wait for the window to reset
	time.Sleep(rate + 10*time.Millisecond)

	// Next request should be allowed after window reset
	if !rl.Allow(ip) {
		t.Error("Request after window reset should be allowed")
	}
}

func TestRateLimiter_MultipleIPs(t *testing.T) {
	rate := 100 * time.Millisecond
	burst := 2

	rl := NewRateLimiter(rate, burst)

	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	// Requests from ip1
	for i := 0; i < burst; i++ {
		if !rl.Allow(ip1) {
			t.Errorf("Request %d from ip1 should be allowed", i+1)
		}
	}

	// ip1 should be rate limited
	if rl.Allow(ip1) {
		t.Error("Additional request from ip1 should be blocked")
	}

	// Requests from ip2 should still be allowed
	for i := 0; i < burst; i++ {
		if !rl.Allow(ip2) {
			t.Errorf("Request %d from ip2 should be allowed", i+1)
		}
	}

	// ip2 should be rate limited
	if rl.Allow(ip2) {
		t.Error("Additional request from ip2 should be blocked")
	}
}

func TestRateLimiter_Cleanup(t *testing.T) {
	rate := 50 * time.Millisecond
	burst := 2

	rl := NewRateLimiter(rate, burst)

	ip := "192.168.1.1"

	// Make a request
	rl.Allow(ip)

	// Verify visitor exists
	rl.mu.RLock()
	if _, exists := rl.visitors[ip]; !exists {
		t.Error("Visitor should exist")
	}
	rl.mu.RUnlock()

	// Note: The cleanup goroutine runs every minute and removes visitors
	// that haven't been seen in 3 minutes. We can't easily test this without
	// waiting, but we can verify the visitor is still there shortly after.
	time.Sleep(100 * time.Millisecond)

	rl.mu.RLock()
	if _, exists := rl.visitors[ip]; !exists {
		t.Error("Visitor should still exist after short delay")
	}
	rl.mu.RUnlock()
}

func TestRateLimit_Middleware(t *testing.T) {
	rate := 100 * time.Millisecond
	burst := 2

	middleware := RateLimit(rate, burst)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(next)

	ip := "192.168.1.100"

	// First requests should succeed
	for i := 0; i < burst; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Request %d should succeed, got status %d", i+1, w.Code)
		}
	}

	// Next request should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = ip
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}

	// Wait for window to reset
	time.Sleep(rate + 10*time.Millisecond)

	// Request should succeed after reset
	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = ip
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Request after reset should succeed, got status %d", w.Code)
	}
}

func TestRateLimit_IPExtraction(t *testing.T) {
	rate := 100 * time.Millisecond
	burst := 2

	middleware := RateLimit(rate, burst)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(next)

	tests := []struct {
		name       string
		setupReq   func(*http.Request)
		expectedIP string
	}{
		{
			name: "X-Real-IP header",
			setupReq: func(req *http.Request) {
				req.Header.Set("X-Real-IP", "10.0.0.1")
			},
			expectedIP: "10.0.0.1",
		},
		{
			name: "X-Forwarded-For header",
			setupReq: func(req *http.Request) {
				req.Header.Set("X-Forwarded-For", "10.0.0.2")
			},
			expectedIP: "10.0.0.2",
		},
		{
			name: "RemoteAddr fallback",
			setupReq: func(req *http.Request) {
				req.RemoteAddr = "10.0.0.3:12345"
			},
			expectedIP: "10.0.0.3:12345",
		},
		{
			name: "X-Real-IP takes precedence",
			setupReq: func(req *http.Request) {
				req.Header.Set("X-Real-IP", "10.0.0.4")
				req.Header.Set("X-Forwarded-For", "10.0.0.5")
				req.RemoteAddr = "10.0.0.6:12345"
			},
			expectedIP: "10.0.0.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make burst requests
			for i := 0; i < burst; i++ {
				req := httptest.NewRequest(http.MethodGet, "/test", nil)
				tt.setupReq(req)
				w := httptest.NewRecorder()

				handler.ServeHTTP(w, req)

				if w.Code != http.StatusOK {
					t.Errorf("Request %d should succeed", i+1)
				}
			}

			// Next request should be rate limited
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			tt.setupReq(req)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusTooManyRequests {
				t.Errorf("Expected rate limit for IP %s", tt.expectedIP)
			}

			// Wait for reset
			time.Sleep(rate + 10*time.Millisecond)
		})
	}
}

func TestRateLimiter_Concurrent(t *testing.T) {
	rate := 50 * time.Millisecond
	burst := 10

	rl := NewRateLimiter(rate, burst)

	ip := "192.168.1.1"

	var wg sync.WaitGroup
	allowed := 0
	blocked := 0
	var mu sync.Mutex

	// Send burst*2 concurrent requests
	for i := 0; i < burst*2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rl.Allow(ip) {
				mu.Lock()
				allowed++
				mu.Unlock()
			} else {
				mu.Lock()
				blocked++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// We should have exactly burst allowed requests
	if allowed != burst {
		t.Errorf("Expected %d allowed requests, got %d", burst, allowed)
	}

	// The rest should be blocked
	if blocked != burst {
		t.Errorf("Expected %d blocked requests, got %d", burst, blocked)
	}
}

func TestRateLimiter_WindowReset(t *testing.T) {
	rate := 50 * time.Millisecond
	burst := 2

	rl := NewRateLimiter(rate, burst)

	ip := "192.168.1.1"

	// Use up the burst
	for i := 0; i < burst; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("Request %d should be allowed", i+1)
		}
	}

	// Should be blocked
	if rl.Allow(ip) {
		t.Error("Should be rate limited")
	}

	// Wait for window reset
	time.Sleep(rate + 10*time.Millisecond)

	// Should be allowed again
	for i := 0; i < burst; i++ {
		if !rl.Allow(ip) {
			t.Errorf("Request %d after reset should be allowed", i+1)
		}
	}

	// Should be blocked again
	if rl.Allow(ip) {
		t.Error("Should be rate limited again")
	}
}
