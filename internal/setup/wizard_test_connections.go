package setup

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// getRateLimiter returns the wizard's rate limiter, initializing it if needed.
func (w *Wizard) getRateLimiter() RateLimiter {
	if w.RateLimit == nil {
		w.RateLimit = NewMemoryRateLimiter()
	}
	return w.RateLimit
}

// HandleTestDatabase tests PostgreSQL connection
func (w *Wizard) HandleTestDatabase(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Rate limiting
	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}

	rateLimiter := w.getRateLimiter()
	if !rateLimiter.CheckRateLimit(clientIP) {
		http.Error(rw, "Too many test connection requests. Please wait 5 minutes.", http.StatusTooManyRequests)
		return
	}

	// Parse request
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		Database string `json:"database"`
		SSLMode  string `json:"sslmode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(rw, "Invalid request", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Host == "" || req.User == "" || req.Password == "" {
		respondTestConnectionError(rw, "Host, user, and password are required")
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		respondTestConnectionError(rw, "Invalid port: must be between 1 and 65535")
		return
	}
	if req.Database == "" {
		req.Database = "postgres"
	}
	if req.SSLMode == "" {
		req.SSLMode = "disable"
	}

	// SSRF protection - validate host doesn't point to private IPs
	testURL := fmt.Sprintf("http://%s:%d", req.Host, req.Port)
	if err := w.URLValidator.ValidateURL(testURL); err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Invalid host: %s", err.Error()))
		return
	}

	// Build connection string using net/url for proper encoding of special characters
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(req.User, req.Password),
		Host:   net.JoinHostPort(req.Host, strconv.Itoa(req.Port)),
		Path:   "/" + req.Database,
	}
	u.RawQuery = fmt.Sprintf("sslmode=%s&connect_timeout=5", req.SSLMode)
	connStr := u.String()

	// Test connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Failed to connect: %s", err.Error()))
		return
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Connection failed: %s", err.Error()))
		return
	}

	respondTestConnectionSuccess(rw, "PostgreSQL connection successful")
}

// HandleTestRedis tests Redis connection
func (w *Wizard) HandleTestRedis(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Rate limiting
	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}

	rateLimiter := w.getRateLimiter()
	if !rateLimiter.CheckRateLimit(clientIP) {
		http.Error(rw, "Too many test connection requests. Please wait 5 minutes.", http.StatusTooManyRequests)
		return
	}

	// Parse request
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(rw, "Invalid request", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Host == "" {
		respondTestConnectionError(rw, "Host is required")
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		respondTestConnectionError(rw, "Invalid port: must be between 1 and 65535")
		return
	}

	// SSRF protection - validate host doesn't point to private IPs
	testURL := fmt.Sprintf("http://%s:%d", req.Host, req.Port)
	if err := w.URLValidator.ValidateURL(testURL); err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Invalid host: %s", err.Error()))
		return
	}

	// Test connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(req.Host, strconv.Itoa(req.Port)))
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Connection failed: %s", err.Error()))
		return
	}
	defer conn.Close()

	// Set read/write deadline to prevent holding the mutex indefinitely
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Failed to set deadline: %s", err.Error()))
		return
	}

	reader := bufio.NewReader(conn)

	// Send AUTH command if password is provided
	if req.Password != "" {
		authCmd := fmt.Sprintf("*2\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n", len(req.Password), req.Password)
		if _, err := conn.Write([]byte(authCmd)); err != nil {
			respondTestConnectionError(rw, fmt.Sprintf("Failed to send AUTH: %s", err.Error()))
			return
		}

		authResp, err := reader.ReadString('\n')
		if err != nil {
			respondTestConnectionError(rw, fmt.Sprintf("Failed to read AUTH response: %s", err.Error()))
			return
		}

		if authResp != "+OK\r\n" {
			respondTestConnectionError(rw, "Redis authentication failed: invalid password")
			return
		}
	}

	// Send PING command
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Failed to send PING: %s", err.Error()))
		return
	}

	// Read response
	response, err := reader.ReadString('\n')
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Failed to read response: %s", err.Error()))
		return
	}

	// Expect +PONG\r\n
	if response != "+PONG\r\n" {
		respondTestConnectionError(rw, fmt.Sprintf("Unexpected response: %s", response))
		return
	}

	respondTestConnectionSuccess(rw, "Redis connection successful")
}

// HandleTestIPFS tests IPFS connection
func (w *Wizard) HandleTestIPFS(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Rate limiting
	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}

	rateLimiter := w.getRateLimiter()
	if !rateLimiter.CheckRateLimit(clientIP) {
		http.Error(rw, "Too many test connection requests. Please wait 5 minutes.", http.StatusTooManyRequests)
		return
	}

	// Parse request
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(rw, "Invalid request", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.URL == "" {
		respondTestConnectionError(rw, "URL is required")
		return
	}

	// SSRF protection
	if err := w.URLValidator.ValidateURL(req.URL); err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Invalid URL: %s", err.Error()))
		return
	}

	// Test connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := w.URLValidator.NewSafeHTTPClient(5 * time.Second)
	testURL := req.URL + "/api/v0/version"

	httpReq, err := http.NewRequestWithContext(ctx, "POST", testURL, nil)
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Failed to create request: %s", err.Error()))
		return
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Connection failed: %s", err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respondTestConnectionError(rw, fmt.Sprintf("IPFS returned status %d", resp.StatusCode))
		return
	}

	respondTestConnectionSuccess(rw, "IPFS connection successful")
}

// HandleTestBTCPay tests BTCPay Server connection
func (w *Wizard) HandleTestBTCPay(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Rate limiting
	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}

	rateLimiter := w.getRateLimiter()
	if !rateLimiter.CheckRateLimit(clientIP) {
		http.Error(rw, "Too many test connection requests. Please wait 5 minutes.", http.StatusTooManyRequests)
		return
	}

	// Parse request
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(rw, "Invalid request", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.URL == "" {
		respondTestConnectionError(rw, "URL is required")
		return
	}

	// SSRF protection
	if err := w.URLValidator.ValidateURL(req.URL); err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Invalid URL: %s", err.Error()))
		return
	}

	// Test connection with timeout via BTCPay health endpoint
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := w.URLValidator.NewSafeHTTPClient(5 * time.Second)

	healthURL := req.URL + "/api/v1/health"
	httpReq, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Failed to create request: %s", err.Error()))
		return
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Connection failed: %s", err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respondTestConnectionError(rw, fmt.Sprintf("BTCPay Server returned status %d", resp.StatusCode))
		return
	}

	respondTestConnectionSuccess(rw, "BTCPay Server connection successful")
}

// Helper functions for consistent JSON responses

func respondTestConnectionSuccess(rw http.ResponseWriter, message string) {
	response := map[string]interface{}{
		"success": true,
		"message": message,
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(response)
}

func respondTestConnectionError(rw http.ResponseWriter, error string) {
	response := map[string]interface{}{
		"success": false,
		"error":   error,
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(response)
}
