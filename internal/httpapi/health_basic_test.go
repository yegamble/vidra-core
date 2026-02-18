package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestHealthCheck_BasicStructure(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	HealthCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var envelope struct {
		Data    HealthResponse `json:"data"`
		Success bool           `json:"success"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	if err != nil {
		t.Errorf("Failed to unmarshal response: %v", err)
	}

	if envelope.Data.Status != "ok" {
		t.Errorf("Expected status 'ok', got '%s'", envelope.Data.Status)
	}

	if envelope.Data.Version != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got '%s'", envelope.Data.Version)
	}

	t.Logf("Health check basic test passed: status=%s, version=%s",
		envelope.Data.Status, envelope.Data.Version)
}

func TestReadinessCheck_BasicStructure(t *testing.T) {
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	ReadinessCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var envelope struct {
		Data    HealthResponse `json:"data"`
		Success bool           `json:"success"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	if err != nil {
		t.Errorf("Failed to unmarshal response: %v", err)
	}

	expectedComponents := []string{"database", "redis", "ipfs", "queue"}
	for _, component := range expectedComponents {
		if status, exists := envelope.Data.Checks[component]; !exists {
			t.Errorf("Missing component check: %s", component)
		} else if status != "ok" {
			t.Errorf("Component %s status is '%s', expected 'ok' (stub implementation)",
				component, status)
		}
	}

	t.Logf("Readiness check structure test passed with %d components checked",
		len(envelope.Data.Checks))
}

func TestServerReadinessCheck_DBUnhealthy(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("Failed to create sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectPing().WillReturnError(fmt.Errorf("connection refused"))

	server := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)
	server.SetDB(db)

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	server.ReadinessCheck(w, req)

	var envelope struct {
		Data struct {
			Checks struct {
				Database *string `json:"database"`
			} `json:"checks"`
		} `json:"data"`
		Success bool `json:"success"`
	}
	err = json.Unmarshal(w.Body.Bytes(), &envelope)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if envelope.Data.Checks.Database == nil {
		t.Fatal("Expected database check in response")
	}
	if *envelope.Data.Checks.Database != "unhealthy" {
		t.Errorf("Expected database status 'unhealthy' when DB ping fails, got '%s'",
			*envelope.Data.Checks.Database)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled sqlmock expectations: %v", err)
	}
}

func TestServerReadinessCheck_NilDB(t *testing.T) {
	server := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	server.ReadinessCheck(w, req)

	var envelope struct {
		Data struct {
			Checks struct {
				Database *string `json:"database"`
			} `json:"checks"`
		} `json:"data"`
		Success bool `json:"success"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if envelope.Data.Checks.Database == nil {
		t.Fatal("Expected database check in response")
	}
	if *envelope.Data.Checks.Database != "healthy" {
		t.Errorf("Expected database status 'healthy' when no DB configured, got '%s'",
			*envelope.Data.Checks.Database)
	}
}

func TestReadinessCheck_StubLimitations(t *testing.T) {

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	start := time.Now()
	ReadinessCheck(w, req)
	duration := time.Since(start)

	if duration > 10*time.Millisecond {
		t.Logf("Warning: Readiness check took %v, which might indicate real checks are happening", duration)
	} else {
		t.Logf("Readiness check completed in %v (indicates stub implementation)", duration)
	}

	if w.Code != http.StatusOK {
		t.Errorf("Stub should always return 200, got %d", w.Code)
	}

	var response HealthResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	for component, status := range response.Checks {
		if status != "ok" {
			t.Errorf("Stub implementation: component %s should always be 'ok', got '%s'",
				component, status)
		}
	}

	t.Log("Confirmed: Current implementation uses stubs that always return success")
	t.Log("Real implementation needed for:")
	t.Log("  - Actual database ping with timeout")
	t.Log("  - Redis PING command with timeout")
	t.Log("  - IPFS API version check with timeout")
	t.Log("  - Real queue depth monitoring")
}
