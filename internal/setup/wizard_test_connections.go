package setup

import (
	"bufio"
	"bytes"
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

	conn, err := dialRedis(ctx, req.Host, req.Port)
	if err != nil {
		respondTestConnectionError(rw, err.Error())
		return
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	if req.Password != "" {
		if err := sendRedisAuth(conn, reader, req.Password); err != nil {
			respondTestConnectionError(rw, err.Error())
			return
		}
	}

	if err := sendRedisPing(conn, reader); err != nil {
		respondTestConnectionError(rw, err.Error())
		return
	}

	respondTestConnectionSuccess(rw, "Redis connection successful")
}

// dialRedis establishes a TCP connection to Redis and sets a read/write deadline.
func dialRedis(ctx context.Context, host string, port int) (net.Conn, error) {
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("Connection failed: %s", err.Error())
	}

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("Failed to set deadline: %s", err.Error())
	}

	return conn, nil
}

// sendRedisAuth sends the AUTH command and validates the response.
func sendRedisAuth(conn net.Conn, reader *bufio.Reader, password string) error {
	authCmd := fmt.Sprintf("*2\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n", len(password), password)
	if _, err := conn.Write([]byte(authCmd)); err != nil {
		return fmt.Errorf("Failed to send AUTH: %s", err.Error())
	}

	authResp, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("Failed to read AUTH response: %s", err.Error())
	}

	if authResp != "+OK\r\n" {
		return fmt.Errorf("Redis authentication failed: invalid password")
	}

	return nil
}

// sendRedisPing sends the PING command and validates the PONG response.
func sendRedisPing(conn net.Conn, reader *bufio.Reader) error {
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		return fmt.Errorf("Failed to send PING: %s", err.Error())
	}

	response, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("Failed to read response: %s", err.Error())
	}

	if response != "+PONG\r\n" {
		return fmt.Errorf("Unexpected response: %s", response)
	}

	return nil
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

// HandleTestIOTA tests IOTA node connection
func (w *Wizard) HandleTestIOTA(rw http.ResponseWriter, r *http.Request) {
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

	// Use iota_getChainIdentifier as a lightweight health check for IOTA Rebased
	jsonRPCReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "iota_getChainIdentifier",
		"id":      1,
	}

	body, err := json.Marshal(jsonRPCReq)
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Failed to create request: %s", err.Error()))
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", req.URL, bytes.NewReader(body))
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Failed to create request: %s", err.Error()))
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Connection failed: %s", err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respondTestConnectionError(rw, fmt.Sprintf("IOTA node returned status %d", resp.StatusCode))
		return
	}

	// Decode and validate JSON-RPC response
	var jsonRPCResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&jsonRPCResp); err != nil {
		respondTestConnectionError(rw, fmt.Sprintf("Invalid JSON-RPC response: %s", err.Error()))
		return
	}

	if _, ok := jsonRPCResp["error"]; ok {
		respondTestConnectionError(rw, "IOTA node returned an error")
		return
	}

	respondTestConnectionSuccess(rw, "IOTA node connection successful")
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
