package ipfs_streaming

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== NewGatewayClient Tests ====================

func TestNewGatewayClient(t *testing.T) {
	t.Run("initializes gateways and status", func(t *testing.T) {
		gateways := []string{"http://gw1.example.com", "http://gw2.example.com"}
		client := NewGatewayClient(gateways, 5*time.Second, 3, 0)
		defer client.Close()

		assert.Equal(t, gateways, client.gateways)
		assert.Equal(t, 0, client.currentIndex)
		assert.Equal(t, 3, client.maxRetries)
		assert.Len(t, client.gatewayStatus, 2)

		for _, gw := range gateways {
			status, ok := client.gatewayStatus[gw]
			require.True(t, ok)
			assert.Equal(t, gw, status.URL)
			assert.True(t, status.Healthy)
		}
	})

	t.Run("starts health check ticker when interval positive", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1.example.com"}, 5*time.Second, 3, 1*time.Minute)
		defer client.Close()

		assert.NotNil(t, client.healthTicker)
	})

	t.Run("no health check ticker when interval is zero", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1.example.com"}, 5*time.Second, 3, 0)
		defer client.Close()

		assert.Nil(t, client.healthTicker)
	})

	t.Run("empty gateways list", func(t *testing.T) {
		client := NewGatewayClient([]string{}, 5*time.Second, 3, 0)
		defer client.Close()

		assert.Empty(t, client.gateways)
		assert.Empty(t, client.gatewayStatus)
	})
}

// ==================== FetchCID Tests ====================

func TestFetchCID(t *testing.T) {
	t.Run("success returns body on 200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/ipfs/QmTestCID", r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ipfs content"))
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 3, 0)
		defer client.Close()

		reader, err := client.FetchCID(context.Background(), "QmTestCID")
		require.NoError(t, err)
		defer func() { _ = reader.Close() }()

		body, _ := io.ReadAll(reader)
		assert.Equal(t, "ipfs content", string(body))
	})

	t.Run("all gateways unhealthy returns error with empty gateways", func(t *testing.T) {
		client := NewGatewayClient([]string{}, 5*time.Second, 3, 0)
		defer client.Close()

		reader, err := client.FetchCID(context.Background(), "QmTestCID")
		assert.Error(t, err)
		assert.Nil(t, reader)
		assert.Contains(t, err.Error(), "no healthy gateways available")
	})

	t.Run("gateway returns non-200 marks unhealthy and retries", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 3, 0)
		defer client.Close()

		reader, err := client.FetchCID(context.Background(), "QmTestCID")
		require.NoError(t, err)
		defer func() { _ = reader.Close() }()

		body, _ := io.ReadAll(reader)
		assert.Equal(t, "ok", string(body))
		assert.Equal(t, 2, callCount)
	})

	t.Run("exhausts all retries returns last error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 2, 0)
		defer client.Close()

		reader, err := client.FetchCID(context.Background(), "QmTestCID")
		assert.Error(t, err)
		assert.Nil(t, reader)
		assert.Contains(t, err.Error(), "failed to fetch CID after 2 attempts")
	})

	t.Run("cancelled context returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 1, 0)
		defer client.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		reader, err := client.FetchCID(ctx, "QmTestCID")
		assert.Error(t, err)
		assert.Nil(t, reader)
	})
}

// ==================== FetchCIDWithRange Tests ====================

func TestFetchCIDWithRange(t *testing.T) {
	t.Run("success with 206 partial content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "bytes=0-1023", r.Header.Get("Range"))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("partial content"))
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 3, 0)
		defer client.Close()

		reader, statusCode, err := client.FetchCIDWithRange(context.Background(), "QmTestCID", "bytes=0-1023")
		require.NoError(t, err)
		defer func() { _ = reader.Close() }()

		assert.Equal(t, http.StatusPartialContent, statusCode)

		body, _ := io.ReadAll(reader)
		assert.Equal(t, "partial content", string(body))
	})

	t.Run("success with 200 full content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("full content"))
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 3, 0)
		defer client.Close()

		reader, statusCode, err := client.FetchCIDWithRange(context.Background(), "QmTestCID", "bytes=0-1023")
		require.NoError(t, err)
		defer func() { _ = reader.Close() }()

		assert.Equal(t, http.StatusOK, statusCode)

		body, _ := io.ReadAll(reader)
		assert.Equal(t, "full content", string(body))
	})

	t.Run("empty range header sends request without Range header", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Empty(t, r.Header.Get("Range"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("full content"))
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 3, 0)
		defer client.Close()

		reader, statusCode, err := client.FetchCIDWithRange(context.Background(), "QmTestCID", "")
		require.NoError(t, err)
		defer func() { _ = reader.Close() }()

		assert.Equal(t, http.StatusOK, statusCode)
	})

	t.Run("gateway returns 500 retries and fails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 2, 0)
		defer client.Close()

		reader, statusCode, err := client.FetchCIDWithRange(context.Background(), "QmTestCID", "bytes=0-1023")
		assert.Error(t, err)
		assert.Nil(t, reader)
		assert.Equal(t, 0, statusCode)
		assert.Contains(t, err.Error(), "failed to fetch CID with range after 2 attempts")
	})

	t.Run("no healthy gateways with empty list", func(t *testing.T) {
		client := NewGatewayClient([]string{}, 5*time.Second, 3, 0)
		defer client.Close()

		reader, statusCode, err := client.FetchCIDWithRange(context.Background(), "QmTestCID", "bytes=0-1023")
		assert.Error(t, err)
		assert.Nil(t, reader)
		assert.Equal(t, 0, statusCode)
		assert.Contains(t, err.Error(), "no healthy gateways available")
	})
}

// ==================== selectHealthyGateway Tests ====================

func TestSelectHealthyGateway(t *testing.T) {
	t.Run("round-robin selection among healthy gateways", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1", "http://gw2", "http://gw3"}, 5*time.Second, 3, 0)
		defer client.Close()

		first := client.selectHealthyGateway()
		second := client.selectHealthyGateway()
		third := client.selectHealthyGateway()
		fourth := client.selectHealthyGateway()

		assert.Equal(t, "http://gw1", first)
		assert.Equal(t, "http://gw2", second)
		assert.Equal(t, "http://gw3", third)
		assert.Equal(t, "http://gw1", fourth) // wraps around
	})

	t.Run("skips unhealthy gateways in round-robin", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1", "http://gw2", "http://gw3"}, 5*time.Second, 3, 0)
		defer client.Close()

		// Mark gw2 as unhealthy
		client.mu.Lock()
		client.gatewayStatus["http://gw2"].Healthy = false
		client.mu.Unlock()

		first := client.selectHealthyGateway()
		second := client.selectHealthyGateway()
		third := client.selectHealthyGateway()

		assert.Equal(t, "http://gw1", first)
		assert.Equal(t, "http://gw3", second)
		assert.Equal(t, "http://gw1", third)
	})

	t.Run("all unhealthy returns first gateway as fallback", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1", "http://gw2"}, 5*time.Second, 3, 0)
		defer client.Close()

		client.mu.Lock()
		client.gatewayStatus["http://gw1"].Healthy = false
		client.gatewayStatus["http://gw2"].Healthy = false
		client.mu.Unlock()

		result := client.selectHealthyGateway()
		assert.Equal(t, "http://gw1", result)
	})

	t.Run("empty gateways list returns empty string", func(t *testing.T) {
		client := NewGatewayClient([]string{}, 5*time.Second, 3, 0)
		defer client.Close()

		result := client.selectHealthyGateway()
		assert.Equal(t, "", result)
	})
}

// ==================== markGatewayUnhealthy Tests ====================

func TestMarkGatewayUnhealthy(t *testing.T) {
	t.Run("updates status correctly", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1"}, 5*time.Second, 3, 0)
		defer client.Close()

		testErr := assert.AnError
		client.markGatewayUnhealthy("http://gw1", testErr)

		client.mu.RLock()
		status := client.gatewayStatus["http://gw1"]
		client.mu.RUnlock()

		assert.False(t, status.Healthy)
		assert.Equal(t, testErr, status.LastError)
		assert.False(t, status.LastChecked.IsZero())
	})

	t.Run("unknown gateway is no-op", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1"}, 5*time.Second, 3, 0)
		defer client.Close()

		// Should not panic
		client.markGatewayUnhealthy("http://unknown", assert.AnError)
	})
}

// ==================== updateGatewayMetrics Tests ====================

func TestUpdateGatewayMetrics(t *testing.T) {
	t.Run("updates on success", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1"}, 5*time.Second, 3, 0)
		defer client.Close()

		client.updateGatewayMetrics("http://gw1", 42, nil)

		client.mu.RLock()
		status := client.gatewayStatus["http://gw1"]
		client.mu.RUnlock()

		assert.True(t, status.Healthy)
		assert.Equal(t, int64(42), status.ResponseTimeMs)
		assert.Nil(t, status.LastError)
	})

	t.Run("updates on error", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1"}, 5*time.Second, 3, 0)
		defer client.Close()

		testErr := assert.AnError
		client.updateGatewayMetrics("http://gw1", 100, testErr)

		client.mu.RLock()
		status := client.gatewayStatus["http://gw1"]
		client.mu.RUnlock()

		assert.False(t, status.Healthy)
		assert.Equal(t, int64(100), status.ResponseTimeMs)
		assert.Equal(t, testErr, status.LastError)
	})

	t.Run("unknown gateway is no-op", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1"}, 5*time.Second, 3, 0)
		defer client.Close()

		// Should not panic
		client.updateGatewayMetrics("http://unknown", 42, nil)
	})
}

// ==================== GetGatewayStatus Tests ====================

func TestGetGatewayStatus(t *testing.T) {
	t.Run("returns all gateway statuses", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1", "http://gw2"}, 5*time.Second, 3, 0)
		defer client.Close()

		statuses := client.GetGatewayStatus()
		assert.Len(t, statuses, 2)

		urls := map[string]bool{}
		for _, s := range statuses {
			urls[s.URL] = true
			assert.True(t, s.Healthy)
		}
		assert.True(t, urls["http://gw1"])
		assert.True(t, urls["http://gw2"])
	})

	t.Run("reflects updated health", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1"}, 5*time.Second, 3, 0)
		defer client.Close()

		client.markGatewayUnhealthy("http://gw1", assert.AnError)

		statuses := client.GetGatewayStatus()
		require.Len(t, statuses, 1)
		assert.False(t, statuses[0].Healthy)
		assert.Equal(t, assert.AnError, statuses[0].LastError)
	})

	t.Run("empty gateways returns empty slice", func(t *testing.T) {
		client := NewGatewayClient([]string{}, 5*time.Second, 3, 0)
		defer client.Close()

		statuses := client.GetGatewayStatus()
		assert.Empty(t, statuses)
	})
}

// ==================== Close Tests ====================

func TestGatewayClientClose(t *testing.T) {
	t.Run("stops ticker and cancels context", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1"}, 5*time.Second, 3, 1*time.Minute)

		client.Close()

		// Context should be cancelled
		select {
		case <-client.ctx.Done():
			// expected
		default:
			t.Fatal("context should be cancelled after Close")
		}
	})

	t.Run("close without ticker does not panic", func(t *testing.T) {
		client := NewGatewayClient([]string{"http://gw1"}, 5*time.Second, 3, 0)

		assert.Nil(t, client.healthTicker)
		// Should not panic
		client.Close()
	})
}

// ==================== performHealthChecks Tests ====================

func TestPerformHealthChecks(t *testing.T) {
	t.Run("marks healthy gateway on 200 response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "HEAD", r.Method)
			assert.True(t, strings.HasPrefix(r.URL.Path, "/ipfs/"))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 3, 0)
		defer client.Close()

		// First mark it unhealthy
		client.markGatewayUnhealthy(server.URL, assert.AnError)

		// Run health checks
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.performHealthChecks("QmTestCID")
		}()
		wg.Wait()
		// Give goroutines time to complete
		time.Sleep(100 * time.Millisecond)

		client.mu.RLock()
		status := client.gatewayStatus[server.URL]
		client.mu.RUnlock()

		assert.True(t, status.Healthy)
		assert.Nil(t, status.LastError)
	})

	t.Run("marks gateway unhealthy on non-200 response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client := NewGatewayClient([]string{server.URL}, 5*time.Second, 3, 0)
		defer client.Close()

		client.performHealthChecks("QmTestCID")
		// Give goroutines time to complete
		time.Sleep(100 * time.Millisecond)

		client.mu.RLock()
		status := client.gatewayStatus[server.URL]
		client.mu.RUnlock()

		assert.False(t, status.Healthy)
		assert.NotNil(t, status.LastError)
	})

	t.Run("marks gateway unhealthy on connection error", func(t *testing.T) {
		// Use a URL that will fail to connect
		client := NewGatewayClient([]string{"http://127.0.0.1:1"}, 1*time.Second, 3, 0)
		defer client.Close()

		client.performHealthChecks("QmTestCID")
		// Give goroutines time to complete
		time.Sleep(2 * time.Second)

		client.mu.RLock()
		status := client.gatewayStatus["http://127.0.0.1:1"]
		client.mu.RUnlock()

		assert.False(t, status.Healthy)
		assert.NotNil(t, status.LastError)
	})
}
