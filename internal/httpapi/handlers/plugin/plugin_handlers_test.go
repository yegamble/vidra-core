package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"
	coreplugin "athena/internal/plugin"
	"athena/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPluginTest creates a test environment with mocked DB and temporary directories
func setupPluginTest(t *testing.T) (*PluginHandler, sqlmock.Sqlmock, string, *coreplugin.SignatureVerifier) {
	// Setup mock DB
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "sqlmock")

	// Setup repositories
	pluginRepo := repository.NewPluginRepository(sqlxDB)

	// Setup PluginManager with temp directory
	tempDir := t.TempDir()
	pluginManager := coreplugin.NewManager(tempDir)

	// Setup SignatureVerifier with temp key file
	keyFile := filepath.Join(tempDir, "trusted_keys.json")
	signatureVerifier, err := coreplugin.NewSignatureVerifier(keyFile)
	require.NoError(t, err)

	// Create Handler
	handler := NewPluginHandler(pluginRepo, pluginManager, signatureVerifier, false)

	return handler, mock, tempDir, signatureVerifier
}

func TestListPlugins(t *testing.T) {
	handler, mock, _, _ := setupPluginTest(t)
	defer mock.ExpectationsWereMet()

	// Mock data
	pluginID := uuid.New()
	mockPlugin := &domain.PluginRecord{
		ID:          pluginID,
		Name:        "test-plugin",
		Version:     "1.0.0",
		Author:      "Tester",
		Description: "A test plugin",
		Status:      domain.PluginStatusInstalled,
		Config:      map[string]any{"key": "value"},
		Permissions: []string{"read"},
		Hooks:       []string{"hook1"},
		InstallPath: "/tmp/plugin",
		Checksum:    "checksum",
		InstalledAt: time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Expect List query
	rows := sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).AddRow(
		mockPlugin.ID, mockPlugin.Name, mockPlugin.Version, mockPlugin.Author,
		mockPlugin.Description, mockPlugin.Status, []byte(`{"key":"value"}`),
		[]byte("{read}"), []byte("{hook1}"), mockPlugin.InstallPath, mockPlugin.Checksum,
		mockPlugin.InstalledAt, mockPlugin.UpdatedAt, nil, nil, "",
	)

	// Note: The repository might run a query for statistics too.
	// The handler calls `h.pluginRepo.GetStatisticsForPlugins(r.Context(), pluginIDs)`
	// Let's check the handler code again.
	// Yes: `statsMap, _ := h.pluginRepo.GetStatisticsForPlugins(r.Context(), pluginIDs)`
	// We should mock that too or expect it.

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version, author, description, status, config`)).
		WillReturnRows(rows)

	statsRows := sqlmock.NewRows([]string{
		"plugin_id", "plugin_name", "total_executions", "success_count",
		"failure_count", "avg_duration_ms", "last_executed_at",
	})
	// Expect statistics query (using IN clause which sqlx handles)
	// sqlx.In expands the query. The repository uses `SELECT ... WHERE plugin_id IN (?)`
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT plugin_id, plugin_name, total_executions`)).
		WithArgs(pluginID).
		WillReturnRows(statsRows)

	// Create request
	req := httptest.NewRequest("GET", "/api/v1/admin/plugins", nil)
	w := httptest.NewRecorder()

	// Call handler
	handler.ListPlugins(w, req)

	// Assert response
	if w.Code != http.StatusOK {
		t.Logf("Response body: %s", w.Body.String())
	}
	require.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Data []map[string]any `json:"data"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Len(t, response.Data, 1)
	assert.Equal(t, "test-plugin", response.Data[0]["name"])
}

func TestUploadPlugin_Success(t *testing.T) {
	handler, mock, managerDir, verifier := setupPluginTest(t)
	defer mock.ExpectationsWereMet()

	// Trust the author
	pubKey, _, err := coreplugin.GenerateKeyPair()
	require.NoError(t, err)
	err = verifier.AddTrustedKey("New Author", pubKey, "Test key")
	require.NoError(t, err)

	// Create a valid zip file in memory
	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)

	// Add plugin.json
	manifest := `{
		"name": "new-plugin",
		"version": "1.0.0",
		"author": "New Author",
		"description": "New Description",
		"main": "main.js"
	}`
	f, err := zipWriter.Create("plugin.json")
	require.NoError(t, err)
	_, err = f.Write([]byte(manifest))
	require.NoError(t, err)

	// Add a dummy file
	f, err = zipWriter.Create("main.js")
	require.NoError(t, err)
	_, err = f.Write([]byte("console.log('hello');"))
	require.NoError(t, err)

	err = zipWriter.Close()
	require.NoError(t, err)

	// Prepare multipart request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("plugin", "plugin.zip")
	require.NoError(t, err)
	_, err = part.Write(buf.Bytes())
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	// Expect GetByName (should return error/no rows for success path of upload)
	// If GetByName returns NO rows, it means plugin doesn't exist, which is what we want.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version`)).
		WithArgs("new-plugin").
		WillReturnError(domain.ErrPluginNotFound) // Simulate not found

	// Expect Create
	// The Insert query returns id, installed_at, updated_at
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO plugins`)).
		WithArgs(
			sqlmock.AnyArg(), // ID
			"new-plugin",
			"1.0.0",
			"New Author",
			"New Description",
			domain.PluginStatusInstalled,
			sqlmock.AnyArg(), // Config (nil/empty map)
			sqlmock.AnyArg(), // Permissions (empty array)
			sqlmock.AnyArg(), // Hooks (empty array)
			sqlmock.AnyArg(), // InstallPath
			sqlmock.AnyArg(), // Checksum
			sqlmock.AnyArg(), // InstalledAt
			sqlmock.AnyArg(), // UpdatedAt
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "installed_at", "updated_at"}).
			AddRow(uuid.New(), time.Now(), time.Now()))

	// Create request
	req := httptest.NewRequest("POST", "/api/v1/admin/plugins", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	// Call handler
	handler.UploadPlugin(w, req)

	// Assert response
	if w.Code != http.StatusCreated {
		t.Logf("Response body: %s", w.Body.String())
	}
	assert.Equal(t, http.StatusCreated, w.Code)

	// Verify file extraction
	pluginDir := filepath.Join(managerDir, "new-plugin")
	assert.DirExists(t, pluginDir)
	assert.FileExists(t, filepath.Join(pluginDir, "plugin.json"))
	assert.FileExists(t, filepath.Join(pluginDir, "main.js"))
}

func TestUploadPlugin_MissingManifest(t *testing.T) {
	handler, mock, _, _ := setupPluginTest(t)
	defer mock.ExpectationsWereMet()

	// Create a zip without plugin.json
	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)
	f, err := zipWriter.Create("other.txt")
	require.NoError(t, err)
	_, err = f.Write([]byte("content"))
	require.NoError(t, err)
	err = zipWriter.Close()
	require.NoError(t, err)

	// Prepare multipart request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("plugin", "plugin.zip")
	require.NoError(t, err)
	_, err = part.Write(buf.Bytes())
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/v1/admin/plugins", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	handler.UploadPlugin(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "plugin.json not found")
}

func TestGetPlugin(t *testing.T) {
	handler, mock, _, _ := setupPluginTest(t)
	defer mock.ExpectationsWereMet()

	pluginID := uuid.New()
	mockPlugin := &domain.PluginRecord{
		ID:          pluginID,
		Name:        "test-plugin",
		Version:     "1.0.0",
		Author:      "Tester",
		Status:      domain.PluginStatusInstalled,
	}

	// Expect GetByID query
	rows := sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).AddRow(
		mockPlugin.ID, mockPlugin.Name, mockPlugin.Version, mockPlugin.Author,
		mockPlugin.Description, mockPlugin.Status, []byte(`{}`),
		[]byte("{}"), []byte("{}"), "/tmp/path", "checksum",
		time.Now(), time.Now(), nil, nil, "",
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name`)).
		WithArgs(pluginID).
		WillReturnRows(rows)

	// Expect stats query
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT plugin_id`)).
		WithArgs(pluginID).
		WillReturnRows(sqlmock.NewRows([]string{}))

	// Expect health query
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM get_plugin_health`)).
		WithArgs(pluginID).
		WillReturnRows(sqlmock.NewRows([]string{}))

	req := httptest.NewRequest("GET", "/api/v1/admin/plugins/"+pluginID.String(), nil)

	// Need to setup chi context for URL param
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", pluginID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetPlugin(w, req)

	if w.Code != http.StatusOK {
		t.Logf("Response body: %s", w.Body.String())
	}
	assert.Equal(t, http.StatusOK, w.Code)
}
