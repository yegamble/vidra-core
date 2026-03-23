package studio

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
)

// --- mocks ---

type mockJobRepo struct {
	jobs      []*domain.StudioJob
	createErr error
	getIDErr  error
	getVIDErr error
	updateErr error
}

func (m *mockJobRepo) Create(_ context.Context, job *domain.StudioJob) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.jobs = append(m.jobs, job)
	return nil
}

func (m *mockJobRepo) GetByID(_ context.Context, id string) (*domain.StudioJob, error) {
	if m.getIDErr != nil {
		return nil, m.getIDErr
	}
	for _, j := range m.jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return nil, domain.ErrStudioJobNotFound
}

func (m *mockJobRepo) GetByVideoID(_ context.Context, videoID string) ([]*domain.StudioJob, error) {
	if m.getVIDErr != nil {
		return nil, m.getVIDErr
	}
	var result []*domain.StudioJob
	for _, j := range m.jobs {
		if j.VideoID == videoID {
			result = append(result, j)
		}
	}
	return result, nil
}

func (m *mockJobRepo) UpdateStatus(_ context.Context, id string, status domain.StudioJobStatus, errMsg string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	for _, j := range m.jobs {
		if j.ID == id {
			j.Status = status
			j.ErrorMessage = errMsg
			if status == domain.StudioJobStatusCompleted || status == domain.StudioJobStatusFailed {
				now := time.Now().UTC()
				j.CompletedAt = &now
			}
			return nil
		}
	}
	return domain.ErrStudioJobNotFound
}

func (m *mockJobRepo) List(_ context.Context, limit, offset int) ([]*domain.StudioJob, int64, error) {
	total := int64(len(m.jobs))
	end := offset + limit
	if end > len(m.jobs) {
		end = len(m.jobs)
	}
	if offset > len(m.jobs) {
		return nil, total, nil
	}
	return m.jobs[offset:end], total, nil
}

type mockVideoRepo struct {
	video *domain.Video
	err   error
}

func (m *mockVideoRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.video, nil
}

type mockRunner struct {
	output []byte
	err    error
	calls  [][]string
}

func (m *mockRunner) RunCommand(_ context.Context, name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, append([]string{name}, args...))
	return m.output, m.err
}

// --- helpers ---

func ptrFloat64(v float64) *float64 { return &v }

func validCutRequest() domain.StudioEditRequest {
	return domain.StudioEditRequest{
		Tasks: []domain.StudioTask{
			{Name: "cut", Options: domain.StudioTaskOptions{Start: ptrFloat64(5), End: ptrFloat64(30)}},
		},
	}
}

// --- tests ---

func TestCreateEditJob_Success(t *testing.T) {
	jobRepo := &mockJobRepo{}
	videoRepo := &mockVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "user-1"}}
	runner := &mockRunner{}
	svc := NewService(jobRepo, videoRepo, runner, nil)

	job, err := svc.CreateEditJob(context.Background(), "vid-1", "user-1", validCutRequest())
	require.NoError(t, err)
	require.NotNil(t, job)

	assert.NotEmpty(t, job.ID)
	assert.Equal(t, "vid-1", job.VideoID)
	assert.Equal(t, "user-1", job.UserID)
	// The job is created as pending but the background goroutine may have already
	// advanced the status via the in-memory mock. Assert it is at least pending or later.
	assert.Contains(t,
		[]domain.StudioJobStatus{domain.StudioJobStatusPending, domain.StudioJobStatusProcessing, domain.StudioJobStatusCompleted},
		job.Status,
	)
	assert.NotEmpty(t, job.Tasks)
}

func TestCreateEditJob_InvalidRequest(t *testing.T) {
	svc := NewService(&mockJobRepo{}, &mockVideoRepo{video: &domain.Video{}}, &mockRunner{}, nil)

	_, err := svc.CreateEditJob(context.Background(), "vid-1", "user-1", domain.StudioEditRequest{})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrInvalidStudioTask)
}

func TestCreateEditJob_VideoNotFound(t *testing.T) {
	videoRepo := &mockVideoRepo{err: domain.ErrVideoNotFound}
	svc := NewService(&mockJobRepo{}, videoRepo, &mockRunner{}, nil)

	_, err := svc.CreateEditJob(context.Background(), "vid-1", "user-1", validCutRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrVideoNotFound)
}

func TestCreateEditJob_InProgressJob(t *testing.T) {
	existingJob := &domain.StudioJob{
		ID:      "existing-1",
		VideoID: "vid-1",
		Status:  domain.StudioJobStatusProcessing,
	}
	jobRepo := &mockJobRepo{jobs: []*domain.StudioJob{existingJob}}
	videoRepo := &mockVideoRepo{video: &domain.Video{ID: "vid-1"}}
	svc := NewService(jobRepo, videoRepo, &mockRunner{}, nil)

	_, err := svc.CreateEditJob(context.Background(), "vid-1", "user-1", validCutRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrStudioJobInProgress)
}

func TestCreateEditJob_CompletedJobAllowsNew(t *testing.T) {
	existingJob := &domain.StudioJob{
		ID:      "existing-1",
		VideoID: "vid-1",
		Status:  domain.StudioJobStatusCompleted,
	}
	jobRepo := &mockJobRepo{jobs: []*domain.StudioJob{existingJob}}
	videoRepo := &mockVideoRepo{video: &domain.Video{ID: "vid-1"}}
	svc := NewService(jobRepo, videoRepo, &mockRunner{}, nil)

	job, err := svc.CreateEditJob(context.Background(), "vid-1", "user-1", validCutRequest())
	require.NoError(t, err)
	require.NotNil(t, job)
}

func TestGetJob_Found(t *testing.T) {
	existingJob := &domain.StudioJob{ID: "job-1", VideoID: "vid-1"}
	jobRepo := &mockJobRepo{jobs: []*domain.StudioJob{existingJob}}
	svc := NewService(jobRepo, &mockVideoRepo{}, &mockRunner{}, nil)

	job, err := svc.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	assert.Equal(t, "job-1", job.ID)
}

func TestGetJob_NotFound(t *testing.T) {
	svc := NewService(&mockJobRepo{}, &mockVideoRepo{}, &mockRunner{}, nil)

	_, err := svc.GetJob(context.Background(), "nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrStudioJobNotFound)
}

func TestListJobsForVideo(t *testing.T) {
	jobs := []*domain.StudioJob{
		{ID: "job-1", VideoID: "vid-1"},
		{ID: "job-2", VideoID: "vid-1"},
		{ID: "job-3", VideoID: "vid-2"},
	}
	jobRepo := &mockJobRepo{jobs: jobs}
	svc := NewService(jobRepo, &mockVideoRepo{}, &mockRunner{}, nil)

	result, err := svc.ListJobsForVideo(context.Background(), "vid-1")
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestBuildFFmpegArgs(t *testing.T) {
	tests := []struct {
		name    string
		task    domain.StudioTask
		wantErr bool
		check   func(t *testing.T, args []string)
	}{
		{
			name: "cut",
			task: domain.StudioTask{Name: "cut", Options: domain.StudioTaskOptions{Start: ptrFloat64(5), End: ptrFloat64(30)}},
			check: func(t *testing.T, args []string) {
				assert.Contains(t, args, "-ss")
				assert.Contains(t, args, "5.00")
				assert.Contains(t, args, "-to")
				assert.Contains(t, args, "30.00")
				assert.Contains(t, args, "-c")
				assert.Contains(t, args, "copy")
			},
		},
		{
			name: "add-intro",
			task: domain.StudioTask{Name: "add-intro", Options: domain.StudioTaskOptions{File: "/uploads/intro.mp4"}},
			check: func(t *testing.T, args []string) {
				assert.Contains(t, args, "-i")
				assert.Contains(t, args, "/uploads/intro.mp4")
				assert.Contains(t, args, "-filter_complex")
			},
		},
		{
			name: "add-outro",
			task: domain.StudioTask{Name: "add-outro", Options: domain.StudioTaskOptions{File: "/uploads/outro.mp4"}},
			check: func(t *testing.T, args []string) {
				assert.Contains(t, args, "-i")
				assert.Contains(t, args, "/uploads/outro.mp4")
			},
		},
		{
			name: "add-watermark",
			task: domain.StudioTask{Name: "add-watermark", Options: domain.StudioTaskOptions{File: "/uploads/logo.png"}},
			check: func(t *testing.T, args []string) {
				assert.Contains(t, args, "-i")
				assert.Contains(t, args, "/uploads/logo.png")
				assert.Contains(t, args, "-filter_complex")
				assert.Contains(t, args, "overlay=W-w-10:H-h-10")
			},
		},
		{
			name:    "unsupported task",
			task:    domain.StudioTask{Name: "rotate"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := buildFFmpegArgs(tt.task)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.check(t, args)
		})
	}
}

func TestCreateEditJob_RepoCreateError(t *testing.T) {
	jobRepo := &mockJobRepo{createErr: errors.New("db connection lost")}
	videoRepo := &mockVideoRepo{video: &domain.Video{ID: "vid-1"}}
	svc := NewService(jobRepo, videoRepo, &mockRunner{}, nil)

	_, err := svc.CreateEditJob(context.Background(), "vid-1", "user-1", validCutRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create studio job")
}
