package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreplugin "athena/internal/plugin"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withPluginParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func makeZipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func multipartRequest(t *testing.T, fileField, filename string, fileData []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if fileField != "" {
		part, err := writer.CreateFormFile(fileField, filename)
		require.NoError(t, err)
		_, err = part.Write(fileData)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestPluginHandlers_ConstructorAndHelpers(t *testing.T) {
	t.Run("constructors", func(t *testing.T) {
		h := NewPluginHandler(nil, nil, nil, true)
		require.NotNil(t, h)
		assert.True(t, h.requireSignatures)

		hs := NewPluginHandlers(nil)
		require.NotNil(t, hs)
	})

	t.Run("convert event types", func(t *testing.T) {
		got := convertEventTypesToStrings([]coreplugin.EventType{
			coreplugin.EventVideoUploaded,
			coreplugin.EventUserRegistered,
		})
		require.Equal(t, []string{"video.uploaded", "user.registered"}, got)
	})

	t.Run("extract manifest success and errors", func(t *testing.T) {
		h := NewPluginHandler(nil, nil, nil, false)

		_, err := h.extractManifest([]byte("not-a-zip"))
		require.Error(t, err)

		zipWithoutManifest := makeZipBytes(t, map[string]string{"README.md": "x"})
		_, err = h.extractManifest(zipWithoutManifest)
		require.Error(t, err)

		zipBadManifest := makeZipBytes(t, map[string]string{"plugin.json": "{bad json"})
		_, err = h.extractManifest(zipBadManifest)
		require.Error(t, err)

		zipMissingAuthor := makeZipBytes(t, map[string]string{"plugin.json": `{"name":"demo","version":"1.0.0"}`})
		_, err = h.extractManifest(zipMissingAuthor)
		require.Error(t, err)

		validZip := makeZipBytes(t, map[string]string{"plugin.json": `{"name":"demo","version":"1.0.0","author":"alice","permissions":["read_videos"]}`})
		manifest, err := h.extractManifest(validZip)
		require.NoError(t, err)
		require.NotNil(t, manifest)
		assert.Equal(t, "demo", manifest.Name)
	})

	t.Run("extract plugin success and traversal blocked", func(t *testing.T) {
		h := NewPluginHandler(nil, nil, nil, false)
		validZip := makeZipBytes(t, map[string]string{
			"plugin.json": `{"name":"demo","version":"1.0.0","author":"alice"}`,
			"main.js":     `console.log("ok")`,
		})
		dest := t.TempDir()
		pluginDir, err := h.extractPlugin(validZip, dest, "demo")
		require.NoError(t, err)
		assert.DirExists(t, pluginDir)
		assert.FileExists(t, pluginDir+"/main.js")

		traversalZip := makeZipBytes(t, map[string]string{"../evil.txt": "nope"})
		_, err = h.extractPlugin(traversalZip, t.TempDir(), "demo")
		require.Error(t, err)
	})
}

func TestPluginHandlers_InvalidIDBranches(t *testing.T) {
	h := NewPluginHandler(nil, nil, nil, false)
	tests := []struct {
		name   string
		call   func(http.ResponseWriter, *http.Request)
		method string
		path   string
		needID bool
		body   string
		want   int
	}{
		{"GetPlugin", h.GetPlugin, http.MethodGet, "/x", true, "", http.StatusBadRequest},
		{"EnablePlugin", h.EnablePlugin, http.MethodPut, "/x/enable", true, "", http.StatusBadRequest},
		{"DisablePlugin", h.DisablePlugin, http.MethodPut, "/x/disable", true, "", http.StatusBadRequest},
		{"UpdatePluginConfig-invalid-id", h.UpdatePluginConfig, http.MethodPut, "/x/config", true, `{"config":{}}`, http.StatusBadRequest},
		{"UninstallPlugin", h.UninstallPlugin, http.MethodDelete, "/x", true, "", http.StatusBadRequest},
		{"GetPluginStatistics", h.GetPluginStatistics, http.MethodGet, "/x/statistics", true, "", http.StatusBadRequest},
		{"GetExecutionHistory", h.GetExecutionHistory, http.MethodGet, "/x/executions", true, "", http.StatusBadRequest},
		{"GetPluginHealth", h.GetPluginHealth, http.MethodGet, "/x/health", true, "", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.needID {
				req = withPluginParam(req, "id", "not-a-uuid")
			}
			rr := httptest.NewRecorder()
			tc.call(rr, req)
			require.Equal(t, tc.want, rr.Code)
		})
	}
}

func TestPluginHandlers_BodyValidationBranches(t *testing.T) {
	manager := coreplugin.NewManager(t.TempDir())
	h := NewPluginHandler(nil, manager, nil, false)

	t.Run("update config invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/123/config", strings.NewReader("{bad"))
		req = withPluginParam(req, "id", "11111111-1111-1111-1111-111111111111")
		rr := httptest.NewRecorder()
		h.UpdatePluginConfig(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("update config missing config", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/123/config", strings.NewReader(`{"config":null}`))
		req = withPluginParam(req, "id", "11111111-1111-1111-1111-111111111111")
		rr := httptest.NewRecorder()
		h.UpdatePluginConfig(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("trigger hook invalid json and missing type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/hooks/trigger", strings.NewReader("{bad"))
		rr := httptest.NewRecorder()
		h.TriggerHook(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)

		req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/hooks/trigger", strings.NewReader(`{"event_type":"","data":{}}`))
		rr = httptest.NewRecorder()
		h.TriggerHook(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("trigger hook success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/hooks/trigger", strings.NewReader(`{"event_type":"video.uploaded","data":{"id":"v1"}}`))
		rr := httptest.NewRecorder()
		h.TriggerHook(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("trigger hook uses request context not background", func(t *testing.T) {
		type ctxKey struct{}
		ctxWithVal := context.WithValue(context.Background(), ctxKey{}, "sentinel-value")
		var receivedCtxVal any

		manager.GetHookManager().Register(coreplugin.EventVideoUploaded, "test-plugin", func(ctx context.Context, data any) error {
			receivedCtxVal = ctx.Value(ctxKey{})
			return nil
		})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/hooks/trigger",
			strings.NewReader(`{"event_type":"video.uploaded","data":{"id":"v1"}}`))
		req = req.WithContext(ctxWithVal)
		rr := httptest.NewRecorder()
		h.TriggerHook(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "sentinel-value", receivedCtxVal)
	})

	t.Run("get hooks success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/hooks", nil)
		rr := httptest.NewRecorder()
		h.GetHooks(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestPluginHandlers_UploadValidationBranches(t *testing.T) {
	h := NewPluginHandler(nil, nil, nil, false)

	t.Run("multipart parse failure", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins", strings.NewReader("not multipart"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		h.UploadPlugin(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("missing plugin file", func(t *testing.T) {
		req := multipartRequest(t, "", "", nil)
		rr := httptest.NewRecorder()
		h.UploadPlugin(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("invalid extension", func(t *testing.T) {
		req := multipartRequest(t, "plugin", "plugin.txt", []byte("x"))
		rr := httptest.NewRecorder()
		h.UploadPlugin(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("invalid manifest", func(t *testing.T) {
		zipBytes := makeZipBytes(t, map[string]string{"README.md": "no manifest"})
		req := multipartRequest(t, "plugin", "plugin.zip", zipBytes)
		rr := httptest.NewRecorder()
		h.UploadPlugin(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("signature required", func(t *testing.T) {
		verifier, err := coreplugin.NewSignatureVerifier(t.TempDir() + "/trusted_keys.json")
		require.NoError(t, err)
		handler := NewPluginHandler(nil, nil, verifier, true)
		zipBytes := makeZipBytes(t, map[string]string{
			"plugin.json": `{"name":"demo","version":"1.0.0","author":"alice","permissions":["read_videos"]}`,
			"main.js":     "console.log('ok')",
		})
		req := multipartRequest(t, "plugin", "plugin.zip", zipBytes)
		rr := httptest.NewRecorder()
		handler.UploadPlugin(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("untrusted author without signature", func(t *testing.T) {
		verifier, err := coreplugin.NewSignatureVerifier(t.TempDir() + "/trusted_keys.json")
		require.NoError(t, err)
		handler := NewPluginHandler(nil, nil, verifier, false)
		zipBytes := makeZipBytes(t, map[string]string{
			"plugin.json": `{"name":"demo","version":"1.0.0","author":"alice","permissions":["read_videos"]}`,
			"main.js":     "console.log('ok')",
		})
		req := multipartRequest(t, "plugin", "plugin.zip", zipBytes)
		rr := httptest.NewRecorder()
		handler.UploadPlugin(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("invalid permissions", func(t *testing.T) {
		handler := NewPluginHandler(nil, nil, nil, false)
		zipBytes := makeZipBytes(t, map[string]string{
			"plugin.json": `{"name":"demo","version":"1.0.0","author":"alice","permissions":["not_a_permission"]}`,
			"main.js":     "console.log('ok')",
		})
		req := multipartRequest(t, "plugin", "plugin.zip", zipBytes)
		rr := httptest.NewRecorder()
		handler.UploadPlugin(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}
