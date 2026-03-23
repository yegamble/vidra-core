package config

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// UpdateInterfaceLanguage tests
// ---------------------------------------------------------------------------

func TestUpdateInterfaceLanguage_OK(t *testing.T) {
	h := NewClientConfigHandlers()

	body := `{"language":"en"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/client-config/update-interface-language", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.UpdateInterfaceLanguage(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data map[string]string `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "en", resp.Data["language"])
}

func TestUpdateInterfaceLanguage_MultipleLanguages(t *testing.T) {
	h := NewClientConfigHandlers()

	tests := []struct {
		name     string
		language string
		wantCode int
	}{
		{"english", "en", http.StatusOK},
		{"french", "fr", http.StatusOK},
		{"chinese", "zh-CN", http.StatusOK},
		{"german", "de", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"language":"` + tt.language + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/client-config/update-interface-language", strings.NewReader(body))
			rr := httptest.NewRecorder()

			h.UpdateInterfaceLanguage(rr, req)

			assert.Equal(t, tt.wantCode, rr.Code)
		})
	}
}

func TestUpdateInterfaceLanguage_MissingLanguage(t *testing.T) {
	h := NewClientConfigHandlers()

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/client-config/update-interface-language", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.UpdateInterfaceLanguage(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUpdateInterfaceLanguage_InvalidBody(t *testing.T) {
	h := NewClientConfigHandlers()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/client-config/update-interface-language", strings.NewReader("{bad"))
	rr := httptest.NewRecorder()

	h.UpdateInterfaceLanguage(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUpdateInterfaceLanguage_InvalidLanguageCode(t *testing.T) {
	h := NewClientConfigHandlers()

	// Too short
	body := `{"language":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/client-config/update-interface-language", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.UpdateInterfaceLanguage(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUpdateInterfaceLanguage_TooLongLanguageCode(t *testing.T) {
	h := NewClientConfigHandlers()

	body := `{"language":"this-is-way-too-long"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/client-config/update-interface-language", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.UpdateInterfaceLanguage(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
