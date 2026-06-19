package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vidra/vidra-core/internal/version"
)

func TestVersion(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Name != "vidra" {
		t.Errorf("name = %q, want vidra", body.Name)
	}
	if body.Version != version.Version {
		t.Errorf("version = %q, want %q", body.Version, version.Version)
	}
	if body.Go == "" {
		t.Error("go version is empty")
	}
}
