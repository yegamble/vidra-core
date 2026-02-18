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

	for i := 1; i <= burst; i++ {
		if !rl.Allow(ip) {
			t.Errorf("Request %d should be allowed", i)
		}
	}

	if rl.Allow(ip) {
		t.Error("Request 4 should be blocked")
	}

	time.Sleep(rate + 10*time.Millisecond)

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

	for i := 0; i < burst; i++ {
		if !rl.Allow(ip1) {
			t.Errorf("Request %d from ip1 should be allowed", i+1)
		}
	}

	if rl.Allow(ip1) {
		t.Error("Additional request from ip1 should be blocked")
	}

	for i := 0; i < burst; i++ {
		if !rl.Allow(ip2) {
			t.Errorf("Request %d from ip2 should be allowed", i+1)
		}
	}

	if rl.Allow(ip2) {
		t.Error("Additional request from ip2 should be blocked")
	}
}

func TestRateLimiter_Cleanup(t *testing.T) {
	rate := 50 * time.Millisecond
	burst := 2

	rl := NewRateLimiter(rate, burst)

	ip := "192.168.1.1"

	rl.Allow(ip)

	rl.mu.RLock()
	if _, exists := rl.visitors[ip]; !exists {
		t.Error("Visitor should exist")
	}
	rl.mu.RUnlock()

	// Note: The cleanup goroutine runs every minute and removes visitors
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

	rl := RateLimit(rate, burst)
	defer func() { _ = rl.Shutdown() }()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.Limit(next)

	ip := "192.168.1.100"

	for i := 0; i < burst; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Request %d should succeed, got status %d", i+1, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = ip
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status %d, got %d", http.StatusTooManyRequests, w.Code)
	}

	time.Sleep(rate + 10*time.Millisecond)

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

	rl := RateLimit(rate, burst)
	defer func() { _ = rl.Shutdown() }()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.Limit(next)

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
			for i := 0; i < burst; i++ {
				req := httptest.NewRequest(http.MethodGet, "/test", nil)
				tt.setupReq(req)
				w := httptest.NewRecorder()

				handler.ServeHTTP(w, req)

				if w.Code != http.StatusOK {
					t.Errorf("Request %d should succeed", i+1)
				}
			}

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			tt.setupReq(req)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusTooManyRequests {
				t.Errorf("Expected rate limit for IP %s", tt.expectedIP)
			}

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

	if allowed != burst {
		t.Errorf("Expected %d allowed requests, got %d", burst, allowed)
	}

	if blocked != burst {
		t.Errorf("Expected %d blocked requests, got %d", burst, blocked)
	}
}

func TestRateLimit_ReturnsShuttableRateLimiter(t *testing.T) {
	rate := 100 * time.Millisecond
	burst := 5

	rl := RateLimit(rate, burst)

	if err := rl.Shutdown(); err != nil {
		t.Errorf("Shutdown() should not error, got: %v", err)
	}

	if !rl.IsShutdown() {
		t.Error("Expected IsShutdown() == true after Shutdown()")
	}
}

func TestRateLimit_LimitMiddleware(t *testing.T) {
	rate := 100 * time.Millisecond
	burst := 2

	rl := RateLimit(rate, burst)
	defer func() { _ = rl.Shutdown() }()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.Limit(next)
	ip := "192.168.99.99"

	for i := 0; i < burst; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d should succeed, got %d", i+1, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = ip + ":1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestRateLimiter_WindowReset(t *testing.T) {
	rate := 50 * time.Millisecond
	burst := 2

	rl := NewRateLimiter(rate, burst)

	ip := "192.168.1.1"

	for i := 0; i < burst; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("Request %d should be allowed", i+1)
		}
	}

	if rl.Allow(ip) {
		t.Error("Should be rate limited")
	}

	time.Sleep(rate + 10*time.Millisecond)

	for i := 0; i < burst; i++ {
		if !rl.Allow(ip) {
			t.Errorf("Request %d after reset should be allowed", i+1)
		}
	}

	if rl.Allow(ip) {
		t.Error("Should be rate limited again")
	}
}
