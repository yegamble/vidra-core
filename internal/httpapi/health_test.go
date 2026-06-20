package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/config"
)

// fakePinger is a test double for a dependency probe.
type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func testConfig() *config.Config {
	return &config.Config{
		Environment:         "test",
		HTTPHost:            "127.0.0.1",
		HTTPPort:            8080,
		CORSAllowedOrigins:  []string{"http://localhost:3000"},
		InstanceName:        "Vidra Test",
		RegistrationEnabled: true,
		HTTPRequestTimeout:  30 * time.Second,
		HTTPBodyLimit:       "8M",
		UploadMaxSize:       "64K",
	}
}

func TestHealthz(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body livenessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

func TestReadyzAllHealthy(t *testing.T) {
	srv := New(testConfig(), fakePinger{}, fakePinger{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Components["postgres"].Status != "ok" || body.Components["redis"].Status != "ok" {
		t.Errorf("components = %+v, want both ok", body.Components)
	}
}

func TestReadyzDependencyDown(t *testing.T) {
	srv := New(testConfig(), fakePinger{err: errors.New("connection refused")}, fakePinger{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	if body.Components["postgres"].Status != "down" {
		t.Errorf("postgres status = %q, want down", body.Components["postgres"].Status)
	}
}

func TestNodeInfo(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodeinfo", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body nodeInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Software.Name != "vidra" {
		t.Errorf("software.name = %q, want vidra", body.Software.Name)
	}
	if body.Instance.Name != "Vidra Test" {
		t.Errorf("instance.name = %q, want Vidra Test", body.Instance.Name)
	}
}
