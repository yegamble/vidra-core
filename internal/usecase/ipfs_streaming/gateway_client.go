package ipfs_streaming

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// GatewayStatus represents the health status of an IPFS gateway
type GatewayStatus struct {
	URL            string
	Healthy        bool
	LastChecked    time.Time
	LastError      error
	ResponseTimeMs int64
}

// GatewayClient manages IPFS gateway requests with health checking and failover
type GatewayClient struct {
	gateways     []string
	currentIndex atomic.Uint64
	// INVARIANT: Map keys are fixed after construction in NewGatewayClient.
	// Only pointer-target fields (Healthy, ResponseTimeMs, etc.) are modified at runtime.
	// Adding/removing entries would require upgrading all RLock callers to Lock.
	gatewayStatus map[string]*GatewayStatus
	httpClient    *http.Client
	healthTicker  *time.Ticker
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	maxRetries    int
}

// NewGatewayClient creates a new IPFS gateway client
func NewGatewayClient(gateways []string, timeout time.Duration, maxRetries int, healthCheckInterval time.Duration) *GatewayClient {
	ctx, cancel := context.WithCancel(context.Background())

	client := &GatewayClient{
		gateways:      gateways,
		gatewayStatus: make(map[string]*GatewayStatus),
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		ctx:        ctx,
		cancel:     cancel,
		maxRetries: maxRetries,
	}

	// Initialize gateway status
	for _, gateway := range gateways {
		client.gatewayStatus[gateway] = &GatewayStatus{
			URL:         gateway,
			Healthy:     true, // Assume healthy initially
			LastChecked: time.Now(),
		}
	}

	// Start health checks
	if healthCheckInterval > 0 {
		client.healthTicker = time.NewTicker(healthCheckInterval)
		go client.runHealthChecks()
	}

	return client
}

// FetchCID fetches content from IPFS by CID
func (c *GatewayClient) FetchCID(ctx context.Context, cid string) (io.ReadCloser, error) {
	var lastErr error

	for attempt := 0; attempt < c.maxRetries; attempt++ {
		gateway := c.selectHealthyGateway()
		if gateway == "" {
			return nil, fmt.Errorf("no healthy gateways available")
		}

		url := fmt.Sprintf("%s/ipfs/%s", gateway, cid)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			lastErr = err
			continue
		}

		start := time.Now()
		resp, err := c.httpClient.Do(req)
		responseTime := time.Since(start).Milliseconds()

		if err != nil {
			c.markGatewayUnhealthy(gateway, err)
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			err = fmt.Errorf("gateway returned status %d", resp.StatusCode)
			c.markGatewayUnhealthy(gateway, err)
			lastErr = err
			continue
		}

		// Success - update gateway metrics
		c.updateGatewayMetrics(gateway, responseTime, nil)
		return resp.Body, nil
	}

	return nil, fmt.Errorf("failed to fetch CID after %d attempts: %w", c.maxRetries, lastErr)
}

// FetchCIDWithRange fetches content from IPFS by CID with range support
func (c *GatewayClient) FetchCIDWithRange(ctx context.Context, cid string, rangeHeader string) (io.ReadCloser, int, error) {
	var lastErr error

	for attempt := 0; attempt < c.maxRetries; attempt++ {
		gateway := c.selectHealthyGateway()
		if gateway == "" {
			return nil, 0, fmt.Errorf("no healthy gateways available")
		}

		url := fmt.Sprintf("%s/ipfs/%s", gateway, cid)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			lastErr = err
			continue
		}

		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}

		start := time.Now()
		resp, err := c.httpClient.Do(req)
		responseTime := time.Since(start).Milliseconds()

		if err != nil {
			c.markGatewayUnhealthy(gateway, err)
			lastErr = err
			continue
		}

		// Accept both 200 OK (full content) and 206 Partial Content
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			_ = resp.Body.Close()
			err = fmt.Errorf("gateway returned status %d", resp.StatusCode)
			c.markGatewayUnhealthy(gateway, err)
			lastErr = err
			continue
		}

		// Success - update gateway metrics
		c.updateGatewayMetrics(gateway, responseTime, nil)
		return resp.Body, resp.StatusCode, nil
	}

	return nil, 0, fmt.Errorf("failed to fetch CID with range after %d attempts: %w", c.maxRetries, lastErr)
}

// selectHealthyGateway selects the next healthy gateway using round-robin
// Note: Uses atomic counter increment + RLock for read parallelism. There is a small
// TOCTOU window between the atomic increment and RLock acquisition where health status
// could change, but this is acceptable - the window is nanoseconds and duplicate gateway
// selection temporarily reduces distribution uniformity but doesn't affect correctness.
func (c *GatewayClient) selectHealthyGateway() string {
	// Atomically increment the counter and get the starting index
	startIndex := c.currentIndex.Add(1) - 1

	// Acquire read lock to check gateway health status
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Try to find a healthy gateway starting from current index
	for i := 0; i < len(c.gateways); i++ {
		index := (startIndex + uint64(i)) % uint64(len(c.gateways))
		gateway := c.gateways[index]

		if status, ok := c.gatewayStatus[gateway]; ok && status.Healthy {
			return gateway
		}
	}

	// Fallback: return first gateway even if unhealthy. Counter has already
	// advanced unconditionally; this is acceptable for load distribution
	// purposes and ensures a non-deterministic starting position on recovery.
	if len(c.gateways) > 0 {
		return c.gateways[0]
	}

	return ""
}

// markGatewayUnhealthy marks a gateway as unhealthy
func (c *GatewayClient) markGatewayUnhealthy(gateway string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if status, ok := c.gatewayStatus[gateway]; ok {
		status.Healthy = false
		status.LastError = err
		status.LastChecked = time.Now()
	}
}

// updateGatewayMetrics updates gateway metrics after a successful request
func (c *GatewayClient) updateGatewayMetrics(gateway string, responseTimeMs int64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if status, ok := c.gatewayStatus[gateway]; ok {
		status.Healthy = (err == nil)
		status.ResponseTimeMs = responseTimeMs
		status.LastError = err
		status.LastChecked = time.Now()
	}
}

// GetGatewayStatus returns the current status of all gateways
func (c *GatewayClient) GetGatewayStatus() []GatewayStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	statuses := make([]GatewayStatus, 0, len(c.gatewayStatus))
	for _, status := range c.gatewayStatus {
		statuses = append(statuses, *status)
	}

	return statuses
}

// runHealthChecks periodically checks gateway health
func (c *GatewayClient) runHealthChecks() {
	// Use a well-known small CID for health checks (empty dir)
	testCID := "QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn"

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.healthTicker.C:
			c.performHealthChecks(testCID)
		}
	}
}

// performHealthChecks checks the health of all gateways
func (c *GatewayClient) performHealthChecks(testCID string) {
	for _, gateway := range c.gateways {
		go func(gw string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			url := fmt.Sprintf("%s/ipfs/%s", gw, testCID)
			req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
			if err != nil {
				c.updateGatewayMetrics(gw, 0, err)
				return
			}

			start := time.Now()
			resp, err := c.httpClient.Do(req)
			responseTime := time.Since(start).Milliseconds()

			if err != nil {
				c.updateGatewayMetrics(gw, responseTime, err)
				return
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode == http.StatusOK {
				c.updateGatewayMetrics(gw, responseTime, nil)
			} else {
				c.updateGatewayMetrics(gw, responseTime, fmt.Errorf("status code %d", resp.StatusCode))
			}
		}(gateway)
	}
}

// Close stops health checks and cleans up resources
func (c *GatewayClient) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.healthTicker != nil {
		c.healthTicker.Stop()
	}
}
