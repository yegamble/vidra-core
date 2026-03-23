package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/config"
	"athena/internal/domain"
)

func TestWebFingerWithAcctResource(t *testing.T) {

	cfg := &config.Config{
		PublicBaseURL:     "https://video.example",
		ActivityPubDomain: "video.example",
	}

	handlers := &ActivityPubHandlers{
		service: nil,
		cfg:     cfg,
	}

	req := httptest.NewRequest("GET", "/.well-known/webfinger?resource=acct:alice@video.example", nil)
	w := httptest.NewRecorder()

	handlers.WebFinger(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/jrd+json; charset=utf-8", w.Header().Get("Content-Type"))

	var response domain.WebFingerResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "acct:alice@video.example", response.Subject)
	assert.NotEmpty(t, response.Links)

	var selfLink *domain.WebFingerLink
	for _, link := range response.Links {
		if link.Rel == "self" {
			selfLink = &link
			break
		}
	}
	require.NotNil(t, selfLink, "Expected self link in response")
	assert.Equal(t, "application/activity+json", selfLink.Type)
	assert.Equal(t, "https://video.example/users/alice", selfLink.Href)
}

// TestWebFingerWithHTTPSResource tests WebFinger with https:// resource
func TestWebFingerWithHTTPSResource(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL:     "https://video.example",
		ActivityPubDomain: "video.example",
	}

	handlers := &ActivityPubHandlers{
		service: nil,
		cfg:     cfg,
	}

	req := httptest.NewRequest("GET", "/.well-known/webfinger?resource=https://video.example/users/bob", nil)
	w := httptest.NewRecorder()

	handlers.WebFinger(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response domain.WebFingerResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "acct:bob@video.example", response.Subject)
}

func TestWebFingerMissingResource(t *testing.T) {
	cfg := &config.Config{}
	handlers := &ActivityPubHandlers{cfg: cfg}

	req := httptest.NewRequest("GET", "/.well-known/webfinger", nil)
	w := httptest.NewRecorder()

	handlers.WebFinger(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWebFingerInvalidResource(t *testing.T) {
	cfg := &config.Config{}
	handlers := &ActivityPubHandlers{cfg: cfg}

	req := httptest.NewRequest("GET", "/.well-known/webfinger?resource=invalid", nil)
	w := httptest.NewRecorder()

	handlers.WebFinger(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNodeInfo(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	handlers := &ActivityPubHandlers{cfg: cfg}

	req := httptest.NewRequest("GET", "/.well-known/nodeinfo", nil)
	w := httptest.NewRecorder()

	handlers.NodeInfo(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	links, ok := response["links"].([]interface{})
	require.True(t, ok, "Expected links array in response")
	require.NotEmpty(t, links)

	firstLink := links[0].(map[string]interface{})
	assert.Equal(t, "http://nodeinfo.diaspora.software/ns/schema/2.0", firstLink["rel"])
	assert.Equal(t, "https://video.example/nodeinfo/2.0", firstLink["href"])
}

func TestNodeInfo20(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL:                  "https://video.example",
		ActivityPubInstanceDescription: "A test instance",
	}

	handlers := &ActivityPubHandlers{cfg: cfg}

	req := httptest.NewRequest("GET", "/nodeinfo/2.0", nil)
	w := httptest.NewRecorder()

	handlers.NodeInfo20(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))

	var nodeInfo domain.NodeInfo
	err := json.Unmarshal(w.Body.Bytes(), &nodeInfo)
	require.NoError(t, err)

	assert.Equal(t, "2.0", nodeInfo.Version)
	assert.Equal(t, "athena", nodeInfo.Software.Name)
	assert.Contains(t, nodeInfo.Protocols, "activitypub")
	assert.Equal(t, "A test instance", nodeInfo.Metadata["nodeDescription"])
}

func TestHostMeta(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	handlers := &ActivityPubHandlers{cfg: cfg}

	req := httptest.NewRequest("GET", "/.well-known/host-meta", nil)
	w := httptest.NewRecorder()

	handlers.HostMeta(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/xrd+xml; charset=utf-8", w.Header().Get("Content-Type"))

	body := w.Body.String()
	assert.Contains(t, body, `<?xml version="1.0"`)
	assert.Contains(t, body, `<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">`)
	assert.Contains(t, body, `https://video.example/.well-known/webfinger?resource={uri}`)
}

func TestGetInboxReturnsEmptyCollection(t *testing.T) {
	handlers := &ActivityPubHandlers{}

	req := httptest.NewRequest("GET", "/users/alice/inbox", nil)
	w := httptest.NewRecorder()

	handlers.GetInbox(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/activity+json")
}

func TestPostInboxMissingUsername(t *testing.T) {
	handlers := &ActivityPubHandlers{}

	req := httptest.NewRequest("POST", "/users//inbox", nil)
	w := httptest.NewRecorder()

	handlers.PostInbox(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostInboxInvalidJSON(t *testing.T) {
	handlers := &ActivityPubHandlers{}

	req := httptest.NewRequest("POST", "/users/alice/inbox", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("username", "alice")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handlers.PostInbox(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestContentTypeNegotiation(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		expectedType string
	}{
		{
			name:         "WebFinger",
			endpoint:     "/.well-known/webfinger?resource=acct:alice@example.com",
			expectedType: "application/jrd+json",
		},
		{
			name:         "NodeInfo Discovery",
			endpoint:     "/.well-known/nodeinfo",
			expectedType: "application/json",
		},
		{
			name:         "NodeInfo 2.0",
			endpoint:     "/nodeinfo/2.0",
			expectedType: "application/json",
		},
		{
			name:         "Host Meta",
			endpoint:     "/.well-known/host-meta",
			expectedType: "application/xrd+xml",
		},
	}

	cfg := &config.Config{
		PublicBaseURL:     "https://video.example",
		ActivityPubDomain: "video.example",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlers := &ActivityPubHandlers{cfg: cfg}

			req := httptest.NewRequest("GET", tt.endpoint, nil)
			w := httptest.NewRecorder()

			switch tt.name {
			case "WebFinger":
				handlers.WebFinger(w, req)
			case "NodeInfo Discovery":
				handlers.NodeInfo(w, req)
			case "NodeInfo 2.0":
				handlers.NodeInfo20(w, req)
			case "Host Meta":
				handlers.HostMeta(w, req)
			}

			contentType := w.Header().Get("Content-Type")
			assert.Contains(t, contentType, tt.expectedType,
				"Expected content type to contain %s, got %s", tt.expectedType, contentType)
		})
	}
}
