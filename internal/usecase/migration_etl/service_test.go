package migration_etl

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMigrationRepo is a test double for port.MigrationJobRepository
type mockMigrationRepo struct {
	mu      sync.RWMutex
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
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextID++
	job.ID = "test-job-" + time.Now().Format("150405") + "-" + string(rune('0'+m.nextID))
	job.CreatedAt = time.Now()
	job.UpdatedAt = time.Now()
	m.jobs[job.ID] = cloneMigrationJob(job)
	return nil
}

func (m *mockMigrationRepo) GetByID(ctx context.Context, id string) (*domain.MigrationJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, ok := m.jobs[id]
	if !ok {
		return nil, domain.ErrMigrationNotFound
	}
	return cloneMigrationJob(job), nil
}

func (m *mockMigrationRepo) List(ctx context.Context, limit, offset int) ([]*domain.MigrationJob, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*domain.MigrationJob
	for _, j := range m.jobs {
		result = append(result, cloneMigrationJob(j))
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
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.jobs[job.ID]; !ok {
		return domain.ErrMigrationNotFound
	}
	m.jobs[job.ID] = cloneMigrationJob(job)
	return nil
}

func (m *mockMigrationRepo) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.jobs[id]; !ok {
		return domain.ErrMigrationNotFound
	}
	delete(m.jobs, id)
	return nil
}

func (m *mockMigrationRepo) GetRunning(ctx context.Context) (*domain.MigrationJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneMigrationJob(m.running), nil
}

// mockIDMappingRepo is a test double for port.IDMappingRepository
type mockIDMappingRepo struct {
	mu          sync.RWMutex
	mappings    []*domain.MigrationIDMapping
	checkpoints map[string][]string // jobID -> completed entity types
}

func newMockIDMappingRepo() *mockIDMappingRepo {
	return &mockIDMappingRepo{
		checkpoints: make(map[string][]string),
	}
}

func (m *mockIDMappingRepo) Upsert(_ context.Context, mapping *domain.MigrationIDMapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mappings = append(m.mappings, mapping)
	return nil
}

func (m *mockIDMappingRepo) GetVidraID(_ context.Context, entityType string, peertubeID int) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mp := range m.mappings {
		if mp.EntityType == entityType && mp.PeertubeID == peertubeID {
			return mp.VidraID, nil
		}
	}
	return "", domain.ErrNotFound
}

func (m *mockIDMappingRepo) GetPeertubeID(_ context.Context, entityType string, vidraID string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mp := range m.mappings {
		if mp.EntityType == entityType && mp.VidraID == vidraID {
			return mp.PeertubeID, nil
		}
	}
	return 0, domain.ErrNotFound
}

func (m *mockIDMappingRepo) ListByJobID(_ context.Context, jobID string) ([]*domain.MigrationIDMapping, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*domain.MigrationIDMapping
	for _, mp := range m.mappings {
		if mp.JobID == jobID {
			result = append(result, mp)
		}
	}
	return result, nil
}

func (m *mockIDMappingRepo) UpsertCheckpoint(_ context.Context, jobID string, entityType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints[jobID] = append(m.checkpoints[jobID], entityType)
	return nil
}

func (m *mockIDMappingRepo) GetCompletedPhases(_ context.Context, jobID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.checkpoints[jobID], nil
}

func cloneMigrationJob(job *domain.MigrationJob) *domain.MigrationJob {
	if job == nil {
		return nil
	}

	cloned := *job
	cloned.StatsJSON = append([]byte(nil), job.StatsJSON...)
	cloned.ErrorMessage = cloneStringPtr(job.ErrorMessage)
	cloned.SourceDBHost = cloneStringPtr(job.SourceDBHost)
	cloned.SourceDBName = cloneStringPtr(job.SourceDBName)
	cloned.SourceDBUser = cloneStringPtr(job.SourceDBUser)
	cloned.SourceDBPassword = cloneStringPtr(job.SourceDBPassword)
	cloned.SourceMediaPath = cloneStringPtr(job.SourceMediaPath)
	cloned.SourceDBPort = cloneIntPtr(job.SourceDBPort)
	cloned.StartedAt = cloneTimePtr(job.StartedAt)
	cloned.CompletedAt = cloneTimePtr(job.CompletedAt)
	return &cloned
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func validRequest() *domain.MigrationRequest {
	return &domain.MigrationRequest{
		SourceHost:       "peertube.example.com",
		SourceDBHost:     "10.0.0.5",
		SourceDBPort:     5432,
		SourceDBName:     "peertube_prod",
		SourceDBUser:     "peertube",
		SourceDBPassword: "secret",
		SourceMediaPath:  "/var/www/peertube/storage",
	}
}

func TestStartMigration(t *testing.T) {
	tests := []struct {
		name       string
		req        *domain.MigrationRequest
		running    *domain.MigrationJob
		wantErr    bool
		errContain string
	}{
		{
			name:    "valid request creates job",
			req:     validRequest(),
			running: nil,
			wantErr: false,
		},
		{
			name: "missing source_host returns error",
			req: &domain.MigrationRequest{
				SourceDBHost:     "10.0.0.5",
				SourceDBName:     "peertube_prod",
				SourceDBUser:     "peertube",
				SourceDBPassword: "secret",
			},
			wantErr:    true,
			errContain: "source_host",
		},
		{
			name: "missing db credentials returns error",
			req: &domain.MigrationRequest{
				SourceHost: "peertube.example.com",
			},
			wantErr:    true,
			errContain: "source_db_host",
		},
		{
			name: "existing running migration returns conflict",
			req:  validRequest(),
			running: &domain.MigrationJob{
				ID:     "existing-job",
				Status: domain.MigrationStatusRunning,
			},
			wantErr:    true,
			errContain: "already in progress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			repo.running = tt.running
			svc := NewETLService(repo, nil, nil, nil, nil, nil, nil, nil)
			ctx := context.Background()

			job, err := svc.StartMigration(ctx, "admin-user-id", tt.req)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContain != "" {
					assert.Contains(t, err.Error(), tt.errContain)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, job)
			assert.NotEmpty(t, job.ID)
			assert.Equal(t, domain.MigrationStatusPending, job.Status)
			assert.Equal(t, "admin-user-id", job.AdminUserID)
			assert.Equal(t, "peertube.example.com", job.SourceHost)
			assert.False(t, job.DryRun)
		})
	}
}

func TestGetMigrationStatus(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		setup   func(repo *mockMigrationRepo)
		wantErr bool
	}{
		{
			name: "existing job returns successfully",
			id:   "job-1",
			setup: func(repo *mockMigrationRepo) {
				repo.jobs["job-1"] = &domain.MigrationJob{
					ID:     "job-1",
					Status: domain.MigrationStatusRunning,
				}
			},
			wantErr: false,
		},
		{
			name:    "nonexistent job returns error",
			id:      "nonexistent",
			setup:   func(repo *mockMigrationRepo) {},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			tt.setup(repo)
			svc := NewETLService(repo, nil, nil, nil, nil, nil, nil, nil)
			ctx := context.Background()

			job, err := svc.GetMigrationStatus(ctx, tt.id)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.id, job.ID)
		})
	}
}

func TestListMigrations(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		offset    int
		setup     func(repo *mockMigrationRepo)
		wantCount int
		wantTotal int64
	}{
		{
			name:   "returns all jobs",
			limit:  20,
			offset: 0,
			setup: func(repo *mockMigrationRepo) {
				repo.jobs["j1"] = &domain.MigrationJob{ID: "j1", Status: domain.MigrationStatusCompleted}
				repo.jobs["j2"] = &domain.MigrationJob{ID: "j2", Status: domain.MigrationStatusPending}
			},
			wantCount: 2,
			wantTotal: 2,
		},
		{
			name:      "empty list",
			limit:     20,
			offset:    0,
			setup:     func(repo *mockMigrationRepo) {},
			wantCount: 0,
			wantTotal: 0,
		},
		{
			name:   "negative limit defaults to 20",
			limit:  -1,
			offset: 0,
			setup: func(repo *mockMigrationRepo) {
				repo.jobs["j1"] = &domain.MigrationJob{ID: "j1"}
			},
			wantCount: 1,
			wantTotal: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			tt.setup(repo)
			svc := NewETLService(repo, nil, nil, nil, nil, nil, nil, nil)
			ctx := context.Background()

			jobs, total, err := svc.ListMigrations(ctx, tt.limit, tt.offset)
			require.NoError(t, err)
			assert.Len(t, jobs, tt.wantCount)
			assert.Equal(t, tt.wantTotal, total)
		})
	}
}

func TestCancelMigration(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(repo *mockMigrationRepo) string
		wantErr bool
	}{
		{
			name: "cancel pending job succeeds",
			setup: func(repo *mockMigrationRepo) string {
				repo.jobs["job-1"] = &domain.MigrationJob{
					ID:     "job-1",
					Status: domain.MigrationStatusPending,
				}
				return "job-1"
			},
			wantErr: false,
		},
		{
			name: "cancel running job succeeds",
			setup: func(repo *mockMigrationRepo) string {
				repo.jobs["job-2"] = &domain.MigrationJob{
					ID:     "job-2",
					Status: domain.MigrationStatusRunning,
				}
				return "job-2"
			},
			wantErr: false,
		},
		{
			name: "cancel completed job fails",
			setup: func(repo *mockMigrationRepo) string {
				repo.jobs["job-3"] = &domain.MigrationJob{
					ID:     "job-3",
					Status: domain.MigrationStatusCompleted,
				}
				return "job-3"
			},
			wantErr: true,
		},
		{
			name: "cancel nonexistent job fails",
			setup: func(repo *mockMigrationRepo) string {
				return "nonexistent"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			id := tt.setup(repo)
			svc := NewETLService(repo, nil, nil, nil, nil, nil, nil, nil)
			ctx := context.Background()

			err := svc.CancelMigration(ctx, id)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Verify the job is now cancelled
			job, err := repo.GetByID(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, domain.MigrationStatusCancelled, job.Status)
			assert.NotNil(t, job.CompletedAt)
		})
	}
}

func TestDryRun(t *testing.T) {
	tests := []struct {
		name    string
		req     *domain.MigrationRequest
		wantErr bool
	}{
		{
			name:    "valid dry run creates job",
			req:     validRequest(),
			wantErr: false,
		},
		{
			name: "invalid request fails",
			req: &domain.MigrationRequest{
				SourceHost: "peertube.example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			svc := NewETLService(repo, nil, nil, nil, nil, nil, nil, nil)
			ctx := context.Background()

			job, err := svc.DryRun(ctx, "admin-user-id", tt.req)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, job)
			assert.True(t, job.DryRun)
			assert.Equal(t, domain.MigrationStatusPending, job.Status)
		})
	}
}

func TestResumeMigration(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(repo *mockMigrationRepo) string
		wantErr bool
		errMsg  string
	}{
		{
			name: "resume failed job succeeds",
			setup: func(repo *mockMigrationRepo) string {
				repo.jobs["failed-job"] = &domain.MigrationJob{
					ID:          "failed-job",
					AdminUserID: "admin",
					SourceHost:  "pt.example.com",
					Status:      domain.MigrationStatusFailed,
				}
				return "failed-job"
			},
			wantErr: false,
		},
		{
			name: "resume completed job returns error",
			setup: func(repo *mockMigrationRepo) string {
				repo.jobs["done-job"] = &domain.MigrationJob{
					ID:     "done-job",
					Status: domain.MigrationStatusCompleted,
				}
				return "done-job"
			},
			wantErr: true,
			errMsg:  "cannot resume",
		},
		{
			name: "resume cancelled job returns error",
			setup: func(repo *mockMigrationRepo) string {
				repo.jobs["cancelled-job"] = &domain.MigrationJob{
					ID:     "cancelled-job",
					Status: domain.MigrationStatusCancelled,
				}
				return "cancelled-job"
			},
			wantErr: true,
			errMsg:  "cannot resume",
		},
		{
			name: "resume nonexistent job returns error",
			setup: func(repo *mockMigrationRepo) string {
				return "nonexistent"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			idRepo := newMockIDMappingRepo()
			jobID := tt.setup(repo)
			svc := NewETLService(repo, nil, nil, nil, nil, nil, nil, idRepo)
			ctx := context.Background()

			job, err := svc.ResumeMigration(ctx, jobID)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, job)
			assert.Equal(t, domain.MigrationStatusResuming, job.Status)
		})
	}
}

func TestCanTransitionFromFailed(t *testing.T) {
	job := &domain.MigrationJob{Status: domain.MigrationStatusFailed}
	assert.True(t, job.CanTransition(domain.MigrationStatusResuming), "failed -> resuming should be allowed")
	assert.False(t, job.CanTransition(domain.MigrationStatusRunning), "failed -> running should not be allowed directly")
}

func TestMigrationRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     domain.MigrationRequest
		wantErr bool
	}{
		{
			name: "valid request",
			req: domain.MigrationRequest{
				SourceHost:       "pt.example.com",
				SourceDBHost:     "10.0.0.5",
				SourceDBPort:     5432,
				SourceDBName:     "peertube",
				SourceDBUser:     "user",
				SourceDBPassword: "pass",
			},
			wantErr: false,
		},
		{
			name: "default port set when zero",
			req: domain.MigrationRequest{
				SourceHost:       "pt.example.com",
				SourceDBHost:     "10.0.0.5",
				SourceDBPort:     0,
				SourceDBName:     "peertube",
				SourceDBUser:     "user",
				SourceDBPassword: "pass",
			},
			wantErr: false,
		},
		{
			name:    "empty request fails",
			req:     domain.MigrationRequest{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMigrationJobCanTransition(t *testing.T) {
	tests := []struct {
		name      string
		from      domain.MigrationStatus
		to        domain.MigrationStatus
		canChange bool
	}{
		{"pending to running", domain.MigrationStatusPending, domain.MigrationStatusRunning, true},
		{"pending to cancelled", domain.MigrationStatusPending, domain.MigrationStatusCancelled, true},
		{"pending to dry_run", domain.MigrationStatusPending, domain.MigrationStatusDryRun, true},
		{"running to completed", domain.MigrationStatusRunning, domain.MigrationStatusCompleted, true},
		{"running to failed", domain.MigrationStatusRunning, domain.MigrationStatusFailed, true},
		{"running to cancelled", domain.MigrationStatusRunning, domain.MigrationStatusCancelled, true},
		{"completed to running", domain.MigrationStatusCompleted, domain.MigrationStatusRunning, false},
		{"failed to running", domain.MigrationStatusFailed, domain.MigrationStatusRunning, false},
		{"cancelled to running", domain.MigrationStatusCancelled, domain.MigrationStatusRunning, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &domain.MigrationJob{Status: tt.from}
			assert.Equal(t, tt.canChange, job.CanTransition(tt.to))
		})
	}
}

func TestMigrationStats(t *testing.T) {
	job := &domain.MigrationJob{}

	stats := &domain.MigrationStats{
		Users:    domain.EntityStats{Total: 100, Migrated: 95, Skipped: 3, Failed: 2},
		Videos:   domain.EntityStats{Total: 500, Migrated: 480, Skipped: 10, Failed: 10},
		Channels: domain.EntityStats{Total: 20, Migrated: 20},
	}

	err := job.SetStats(stats)
	require.NoError(t, err)
	assert.NotEmpty(t, job.StatsJSON)

	retrieved, err := job.GetStats()
	require.NoError(t, err)
	assert.Equal(t, 100, retrieved.Users.Total)
	assert.Equal(t, 95, retrieved.Users.Migrated)
	assert.Equal(t, 500, retrieved.Videos.Total)
	assert.Equal(t, 20, retrieved.Channels.Total)
}

func TestMigrationJobGetStatsEmpty(t *testing.T) {
	job := &domain.MigrationJob{}
	stats, err := job.GetStats()
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Users.Total)
	assert.Equal(t, 0, stats.Videos.Total)
}

func TestMigrationJobSetStatsRoundTrip(t *testing.T) {
	job := &domain.MigrationJob{}
	stats := &domain.MigrationStats{
		Users:     domain.EntityStats{Total: 10, Migrated: 8, Failed: 2, Errors: []string{"user foo failed"}},
		Playlists: domain.EntityStats{Total: 5, Migrated: 5},
	}

	require.NoError(t, job.SetStats(stats))

	var decoded domain.MigrationStats
	require.NoError(t, json.Unmarshal(job.StatsJSON, &decoded))
	assert.Equal(t, 10, decoded.Users.Total)
	assert.Equal(t, []string{"user foo failed"}, decoded.Users.Errors)
	assert.Equal(t, 5, decoded.Playlists.Total)
}
