package plugin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func BenchmarkListPlugins(b *testing.B) {
	// Setup sqlmock
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()
	sqlxDB := sqlx.NewDb(db, "sqlmock")

	repo := repository.NewPluginRepository(sqlxDB)
	handler := NewPluginHandler(repo, nil, nil, false)

	// Create dummy plugins
	numPlugins := 50
	plugins := make([]*domain.PluginRecord, numPlugins)
	for i := 0; i < numPlugins; i++ {
		plugins[i] = &domain.PluginRecord{
			ID:          uuid.New(),
			Name:        fmt.Sprintf("plugin-%03d", i), // Use padding to ensure sort order matches iteration
			Version:     "1.0.0",
			Author:      "test",
			Description: "test plugin",
			Status:      domain.PluginStatusInstalled,
			InstalledAt: time.Now(),
			UpdatedAt:   time.Now(),
		}
	}

	// Prepare request
	req, _ := http.NewRequest("GET", "/api/v1/admin/plugins", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Mock the List query
		rows := sqlmock.NewRows([]string{
			"id", "name", "version", "author", "description", "status", "config",
			"permissions", "hooks", "install_path", "checksum",
			"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
		})
		for _, p := range plugins {
			rows.AddRow(
				p.ID, p.Name, p.Version, p.Author, p.Description, p.Status, []byte("{}"),
				pq.Array(p.Permissions), pq.Array(p.Hooks), p.InstallPath, p.Checksum,
				p.InstalledAt, p.UpdatedAt, p.EnabledAt, p.DisabledAt, p.LastError,
			)
		}

		mock.ExpectQuery("(?s)SELECT id, name, .* FROM plugins.*ORDER BY name ASC").
			WillReturnRows(rows)

		// Mock the GetStatistics query for ALL plugins (1 query)
		statsRows := sqlmock.NewRows([]string{
			"plugin_id", "plugin_name", "total_executions", "success_count",
			"failure_count", "avg_duration_ms", "last_executed_at",
		})
		for _, p := range plugins {
			statsRows.AddRow(
				p.ID, p.Name, 100, 90, 10, 50.5, time.Now(),
			)
		}

		mock.ExpectQuery("(?s)SELECT .* FROM plugin_statistics WHERE plugin_id IN").
			WillReturnRows(statsRows)

		w := httptest.NewRecorder()
		handler.ListPlugins(w, req)

		if w.Code != http.StatusOK {
			b.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			b.Errorf("there were unfulfilled expectations: %s", err)
		}
	}
}
