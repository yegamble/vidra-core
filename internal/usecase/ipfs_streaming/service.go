package ipfs_streaming

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"

	"athena/internal/config"
)

// StreamMetrics tracks streaming metrics
type StreamMetrics struct {
	IPFSRequests   int64
	IPFSSuccesses  int64
	IPFSFailures   int64
	LocalRequests  int64
	LocalSuccesses int64
	LocalFailures  int64
	CacheHits      int64
	CacheMisses    int64
}

// Service provides IPFS streaming functionality with fallback
type Service struct {
	cfg           *config.Config
	gatewayClient *GatewayClient
	metrics       *StreamMetrics
	enabled       bool
}

// NewService creates a new IPFS streaming service
func NewService(cfg *config.Config) *Service {
	svc := &Service{
		cfg:     cfg,
		metrics: &StreamMetrics{},
		enabled: cfg.EnableIPFSStreaming,
	}

	if cfg.EnableIPFSStreaming {
		svc.gatewayClient = NewGatewayClient(
			cfg.IPFSGatewayURLs,
			cfg.IPFSStreamingTimeout,
			cfg.IPFSStreamingMaxRetries,
			cfg.IPFSGatewayHealthCheckInterval,
		)
	}

	return svc
}

// StreamFile attempts to stream a file from IPFS, falling back to local filesystem
func (s *Service) StreamFile(ctx context.Context, w http.ResponseWriter, r *http.Request, localPath string, cid string) error {
	if !s.enabled {
		// IPFS streaming disabled - serve from local filesystem
		return s.serveFromLocal(w, r, localPath)
	}

	// Determine strategy based on configuration
	if s.cfg.IPFSStreamingPreferLocal {
		// Try local first, then IPFS
		if err := s.tryServeFromLocal(w, r, localPath); err == nil {
			return nil
		}
		// Local failed, try IPFS
		if cid != "" {
			if err := s.serveFromIPFS(ctx, w, r, cid); err == nil {
				return nil
			}
		}
		return fmt.Errorf("both local and IPFS streaming failed")
	}

	// IPFS-first mode
	if cid != "" {
		if err := s.serveFromIPFS(ctx, w, r, cid); err == nil {
			return nil
		}
	}

	// IPFS failed, try local if fallback enabled
	if s.cfg.IPFSStreamingFallbackToLocal {
		if err := s.tryServeFromLocal(w, r, localPath); err == nil {
			return nil
		}
	}

	return fmt.Errorf("IPFS streaming failed and fallback disabled or unavailable")
}

// serveFromIPFS serves content from IPFS
func (s *Service) serveFromIPFS(ctx context.Context, w http.ResponseWriter, r *http.Request, cid string) error {
	atomic.AddInt64(&s.metrics.IPFSRequests, 1)

	rangeHeader := r.Header.Get("Range")

	var reader io.ReadCloser
	var statusCode int
	var err error

	if rangeHeader != "" {
		reader, statusCode, err = s.gatewayClient.FetchCIDWithRange(ctx, cid, rangeHeader)
	} else {
		reader, err = s.gatewayClient.FetchCID(ctx, cid)
		statusCode = http.StatusOK
	}

	if err != nil {
		atomic.AddInt64(&s.metrics.IPFSFailures, 1)
		return fmt.Errorf("failed to fetch from IPFS: %w", err)
	}
	defer func() { _ = reader.Close() }()

	// Set headers
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Content-Source", "ipfs")

	// Determine content type from file path/extension
	// Note: In production, you might want to store this in DB with the CID
	w.Header().Set("Content-Type", "application/octet-stream")

	// Write status code
	w.WriteHeader(statusCode)

	// Stream content
	buffer := make([]byte, s.cfg.IPFSStreamingBufferSize)
	_, copyErr := io.CopyBuffer(w, reader, buffer)

	if copyErr != nil {
		atomic.AddInt64(&s.metrics.IPFSFailures, 1)
		return fmt.Errorf("failed to stream from IPFS: %w", copyErr)
	}

	atomic.AddInt64(&s.metrics.IPFSSuccesses, 1)
	return nil
}

// tryServeFromLocal attempts to serve from local filesystem (returns error if file doesn't exist)
func (s *Service) tryServeFromLocal(w http.ResponseWriter, r *http.Request, localPath string) error {
	// Check if file exists
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		return err
	}

	return s.serveFromLocal(w, r, localPath)
}

// serveFromLocal serves content from local filesystem
func (s *Service) serveFromLocal(w http.ResponseWriter, r *http.Request, localPath string) error {
	atomic.AddInt64(&s.metrics.LocalRequests, 1)

	// Open file
	file, err := os.Open(localPath)
	if err != nil {
		atomic.AddInt64(&s.metrics.LocalFailures, 1)
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		atomic.AddInt64(&s.metrics.LocalFailures, 1)
		return fmt.Errorf("failed to stat local file: %w", err)
	}

	// Set headers
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Content-Source", "local")

	// Serve file with range support
	http.ServeContent(w, r, filepath.Base(localPath), fileInfo.ModTime(), file)

	atomic.AddInt64(&s.metrics.LocalSuccesses, 1)
	return nil
}

// GetMetrics returns current streaming metrics
func (s *Service) GetMetrics() StreamMetrics {
	return StreamMetrics{
		IPFSRequests:   atomic.LoadInt64(&s.metrics.IPFSRequests),
		IPFSSuccesses:  atomic.LoadInt64(&s.metrics.IPFSSuccesses),
		IPFSFailures:   atomic.LoadInt64(&s.metrics.IPFSFailures),
		LocalRequests:  atomic.LoadInt64(&s.metrics.LocalRequests),
		LocalSuccesses: atomic.LoadInt64(&s.metrics.LocalSuccesses),
		LocalFailures:  atomic.LoadInt64(&s.metrics.LocalFailures),
		CacheHits:      atomic.LoadInt64(&s.metrics.CacheHits),
		CacheMisses:    atomic.LoadInt64(&s.metrics.CacheMisses),
	}
}

// GetGatewayHealth returns the health status of all gateways
func (s *Service) GetGatewayHealth() []GatewayStatus {
	if !s.enabled || s.gatewayClient == nil {
		return []GatewayStatus{}
	}

	return s.gatewayClient.GetGatewayStatus()
}

// IsEnabled returns whether IPFS streaming is enabled
func (s *Service) IsEnabled() bool {
	return s.enabled
}

// Close cleans up resources
func (s *Service) Close() {
	if s.gatewayClient != nil {
		s.gatewayClient.Close()
	}
}
