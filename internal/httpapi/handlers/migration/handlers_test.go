package migration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/port"
	"vidra-core/internal/usecase/migration_etl"
)

// mockMigrationRepo is a test double for port.MigrationJobRepository
type mockMigrationRepo struct {
	jobs    map[string]*domain.MigrationJob
	nextID  int
	running *domain.MigrationJob
}

func newMockRepo() *mockMigrationRepo {
	return &mockMigrationRepo{
		jobs: make(map[string]*domain.MigrationJob),
	}
}

func (m *mockMigrationRepo) Create(ctx context.Context, job *domain.MigrationJob) error {
	m.nextID++
	job.ID = "test-handler-job-" + string(rune('0'+m.nextID))
	job.CreatedAt = time.Now()
	job.UpdatedAt = time.Now()
	m.jobs[job.ID] = job
	return nil
}

func (m *mockMigrationRepo) GetByID(ctx context.Context, id string) (*domain.MigrationJob, error) {
	job, ok := m.jobs[id]
	if !ok {
		return nil, domain.ErrMigrationNotFound
	}
	return job, nil
}

func (m *mockMigrationRepo) List(ctx context.Context, limit, offset int) ([]*domain.MigrationJob, int64, error) {
	var result []*domain.MigrationJob
	for _, j := range m.jobs {
		result = append(result, j)
	}
	total := int64(len(result))
	if offset >= len(result) {
		return []*domain.MigrationJob{}, total, nil
	}
	end := offset + limit
	if end > len(result) {
		end = len(result)
	}
	return result[offset:end], total, nil
}

func (m *mockMigrationRepo) Update(ctx context.Context, job *domain.MigrationJob) error {
	if _, ok := m.jobs[job.ID]; !ok {
		return domain.ErrMigrationNotFound
	}
	m.jobs[job.ID] = job
	return nil
}

func (m *mockMigrationRepo) Delete(ctx context.Context, id string) error {
	if _, ok := m.jobs[id]; !ok {
		return domain.ErrMigrationNotFound
	}
	delete(m.jobs, id)
	return nil
}

func (m *mockMigrationRepo) GetRunning(ctx context.Context) (*domain.MigrationJob, error) {
	return m.running, nil
}

// Verify mockMigrationRepo implements the interface
var _ port.MigrationJobRepository = (*mockMigrationRepo)(nil)

func setupTestHandlers() (*MigrationHandlers, *mockMigrationRepo) {
	repo := newMockRepo()
	svc := migration_etl.NewETLService(repo, nil, nil, nil, nil, nil, nil)
	return NewMigrationHandlers(svc), repo
}

var testAdminUUID = uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

func addUserIDToContext(r *http.Request) *http.Request {
	ctx := middleware.WithUserID(r.Context(), testAdminUUID)
	return r.WithContext(ctx)
}

func TestStartMigrationHandler(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		withAuth   bool
		wantStatus int
	}{
		{
			name: "valid request returns 201",
			body: `{
				"source_host": "peertube.example.com",
				"source_db_host": "10.0.0.5",
				"source_db_port": 5432,
				"source_db_name": "peertube_prod",
				"source_db_user": "peertube",
				"source_db_password": "secret"
			}`,
			withAuth:   true,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "missing body returns 400",
			body:       `{}`,
			withAuth:   true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "no auth returns 401",
			body: `{
				"source_host": "peertube.example.com",
				"source_db_host": "10.0.0.5",
				"source_db_name": "peertube_prod",
				"source_db_user": "peertube",
				"source_db_password": "secret"
			}`,
			withAuth:   false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid JSON returns 400",
			body:       `{invalid}`,
			withAuth:   true,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := setupTestHandlers()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/migrations/peertube", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			if tt.withAuth {
				req = addUserIDToContext(req)
			}

			rec := httptest.NewRecorder()
			h.StartMigration(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantStatus == http.StatusCreated {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				assert.True(t, resp["success"].(bool))
				data := resp["data"].(map[string]interface{})
				assert.NotEmpty(t, data["id"])
				// Goroutine may transition status before JSON encoding completes
				status := data["status"].(string)
				assert.Contains(t, []string{"pending", "running", "completed"}, status)
			}
		})
	}
}

func TestListMigrationsHandler(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		setup      func(repo *mockMigrationRepo)
		wantStatus int
		wantCount  int
	}{
		{
			name:  "returns empty list",
			query: "",
			setup: func(repo *mockMigrationRepo) {},

			wantStatus: http.StatusOK,
			wantCount:  0,
		},
		{
			name:  "returns existing jobs",
			query: "?count=20&start=0",
			setup: func(repo *mockMigrationRepo) {
				repo.jobs["j1"] = &domain.MigrationJob{ID: "j1", Status: domain.MigrationStatusCompleted}
				repo.jobs["j2"] = &domain.MigrationJob{ID: "j2", Status: domain.MigrationStatusPending}
			},
			wantStatus: http.StatusOK,
			wantCount:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, repo := setupTestHandlers()
			tt.setup(repo)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/migrations"+tt.query, nil)
			rec := httptest.NewRecorder()
			h.ListMigrations(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			var resp map[string]interface{}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.True(t, resp["success"].(bool))

			data := resp["data"].([]interface{})
			assert.Len(t, data, tt.wantCount)
		})
	}
}

func TestGetMigrationHandler(t *testing.T) {
	tests := []struct {
		name       string
		jobID      string
		setup      func(repo *mockMigrationRepo)
		wantStatus int
	}{
		{
			name:  "existing job returns 200",
			jobID: "job-1",
			setup: func(repo *mockMigrationRepo) {
				repo.jobs["job-1"] = &domain.MigrationJob{
					ID:         "job-1",
					SourceHost: "pt.example.com",
					Status:     domain.MigrationStatusRunning,
				}
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "nonexistent job returns 404",
			jobID:      "nonexistent",
			setup:      func(repo *mockMigrationRepo) {},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, repo := setupTestHandlers()
			tt.setup(repo)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/migrations/"+tt.jobID, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.jobID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			rec := httptest.NewRecorder()
			h.GetMigration(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestCancelMigrationHandler(t *testing.T) {
	tests := []struct {
		name       string
		jobID      string
		setup      func(repo *mockMigrationRepo)
		wantStatus int
	}{
		{
			name:  "cancel pending returns 204",
			jobID: "job-1",
			setup: func(repo *mockMigrationRepo) {
				repo.jobs["job-1"] = &domain.MigrationJob{
					ID:     "job-1",
					Status: domain.MigrationStatusPending,
				}
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:  "cancel completed returns error",
			jobID: "job-2",
			setup: func(repo *mockMigrationRepo) {
				repo.jobs["job-2"] = &domain.MigrationJob{
					ID:     "job-2",
					Status: domain.MigrationStatusCompleted,
				}
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "cancel nonexistent returns 404",
			jobID:      "nonexistent",
			setup:      func(repo *mockMigrationRepo) {},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, repo := setupTestHandlers()
			tt.setup(repo)

			req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/migrations/"+tt.jobID, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.jobID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			rec := httptest.NewRecorder()
			h.CancelMigration(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestDryRunHandler(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		withAuth   bool
		wantStatus int
	}{
		{
			name: "valid dry-run returns 201",
			body: `{
				"source_host": "peertube.example.com",
				"source_db_host": "10.0.0.5",
				"source_db_name": "peertube_prod",
				"source_db_user": "peertube",
				"source_db_password": "secret"
			}`,
			withAuth:   true,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "no auth returns 401",
			body:       `{}`,
			withAuth:   false,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := setupTestHandlers()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/migrations/new/dry-run", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", "new")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			if tt.withAuth {
				req = addUserIDToContext(req)
			}

			rec := httptest.NewRecorder()
			h.DryRun(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantStatus == http.StatusCreated {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				assert.True(t, resp["success"].(bool))
				data := resp["data"].(map[string]interface{})
				assert.True(t, data["dry_run"].(bool))
			}
		})
	}
}
