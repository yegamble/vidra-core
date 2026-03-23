package captiongen

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/storage"
	"athena/internal/whisper"
)

// mockS3Backend is a minimal in-memory StorageBackend for captiongen tests.
type mockS3Backend struct {
	data    map[string][]byte
	baseURL string
	// downloadErr lets tests inject a Download error.
	downloadErr error
}

func newMockS3Backend(baseURL string) *mockS3Backend {
	return &mockS3Backend{data: make(map[string][]byte), baseURL: baseURL}
}

func (m *mockS3Backend) Upload(_ context.Context, key string, data io.Reader, _ string) error {
	b, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	m.data[key] = b
	return nil
}

func (m *mockS3Backend) UploadFile(_ context.Context, key string, localPath string, _ string) error {
	b, err := os.ReadFile(localPath) //nolint:gosec
	if err != nil {
		return err
	}
	m.data[key] = b
	return nil
}

func (m *mockS3Backend) Download(_ context.Context, key string) (io.ReadCloser, error) {
	if m.downloadErr != nil {
		return nil, m.downloadErr
	}
	b, ok := m.data[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *mockS3Backend) GetURL(key string) string                         { return m.baseURL + "/" + key }
func (m *mockS3Backend) Delete(_ context.Context, _ string) error         { return nil }
func (m *mockS3Backend) Exists(_ context.Context, _ string) (bool, error) { return false, nil }
func (m *mockS3Backend) Copy(_ context.Context, _, _ string) error        { return nil }
func (m *mockS3Backend) GetMetadata(_ context.Context, _ string) (*storage.FileMetadata, error) {
	return nil, nil
}
func (m *mockS3Backend) GetSignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return m.GetURL(key), nil
}

// TestCaptionService_WithS3Backend_SetsField verifies the fluent wiring method.
func TestCaptionService_WithS3Backend_SetsField(t *testing.T) {
	svc := NewService(nil, nil, nil, nil, t.TempDir())
	concrete, ok := svc.(*service)
	require.True(t, ok)
	assert.Nil(t, concrete.s3Backend)

	backend := newMockS3Backend("https://s3.example.com")
	returned := svc.(*service).WithS3Backend(backend)

	assert.NotNil(t, concrete.s3Backend)
	assert.NotNil(t, returned)
}

// TestCreateJob_S3Backend_AllowsMissingLocalFile verifies that CreateJob succeeds
// when the local source file is missing but the video has an S3URL for "source".
func TestCreateJob_S3Backend_AllowsMissingLocalFile(t *testing.T) {
	uploadsDir := t.TempDir()
	videoID := uuid.New()
	userID := uuid.New()

	video := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
		UserID:   userID.String(),
		S3URLs:   map[string]string{"source": "https://s3.example.com/videos/" + videoID.String() + "/source.mp4"},
	}

	mockJob := new(MockJobRepository)
	mockCap := new(MockCaptionRepository)
	mockVid := new(MockVideoRepository)
	mockWhi := new(MockWhisperClient)

	mockVid.On("GetByID", context.Background(), videoID.String()).Return(video, nil)
	mockWhi.On("GetProvider").Return(domain.WhisperProvider("local"))
	mockJob.On("Create", context.Background(), mock.Anything).Return(nil)

	svc := NewService(mockJob, mockCap, mockVid, mockWhi, uploadsDir)
	// Inject S3 backend — now CreateJob should NOT reject missing local file
	svc.(*service).WithS3Backend(newMockS3Backend("https://s3.example.com"))

	req := &domain.CreateCaptionGenerationJobRequest{}
	job, err := svc.CreateJob(context.Background(), videoID, userID, req)
	require.NoError(t, err)
	assert.NotNil(t, job)
}

// TestProcessJob_S3Backend_DownloadsSourceFile verifies that when the local source
// video file is missing but the video has an S3URL and an s3Backend is set,
// processJob downloads the file and proceeds with transcription.
func TestProcessJob_S3Backend_DownloadsSourceFile(t *testing.T) {
	uploadsDir := t.TempDir()
	videoID := uuid.New()

	// Source file content (the "video" returned by S3)
	fakeVideoBytes := []byte("fake-video-bytes")

	// S3 backend has the source video under the expected key
	s3Backend := newMockS3Backend("https://s3.example.com")
	s3Key := "videos/" + videoID.String() + "/source.mp4"
	s3Backend.data[s3Key] = fakeVideoBytes

	video := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
		UserID:   uuid.New().String(),
		S3URLs:   map[string]string{"source": "https://s3.example.com/" + s3Key},
	}

	jobID := uuid.New()
	captionID := uuid.New()

	audioPath := filepath.Join(uploadsDir, "temp", videoID.String()+"_audio.wav")
	job := &domain.CaptionGenerationJob{
		ID:              jobID,
		VideoID:         videoID,
		SourceAudioPath: audioPath,
		OutputFormat:    domain.CaptionFormatVTT,
		Status:          domain.CaptionGenStatusProcessing,
	}

	transcriptionResult := &whisper.TranscriptionResult{
		DetectedLanguage: "en",
	}

	mockJob := new(MockJobRepository)
	mockCap := new(MockCaptionRepository)
	mockVid := new(MockVideoRepository)
	mockWhi := new(MockWhisperClient)

	mockVid.On("GetByID", context.Background(), videoID.String()).Return(video, nil)
	mockJob.On("UpdateProgress", context.Background(), jobID, mock.Anything).Return(nil)

	// The Whisper client MUST be called — this verifies the source was resolved
	mockWhi.On("ExtractAudioFromVideo", context.Background(), mock.AnythingOfType("string"), audioPath).Return(nil)
	mockWhi.On("Transcribe", context.Background(), audioPath, job.TargetLanguage).Return(transcriptionResult, nil)
	mockWhi.On("FormatToVTT", transcriptionResult).Return("WEBVTT\n", nil)
	mockCap.On("GetByVideoAndLanguage", context.Background(), videoID, "en").Return(nil, fmt.Errorf("not found"))
	mockCap.On("Create", context.Background(), mock.Anything).Return(nil)
	mockVid.On("Update", context.Background(), mock.Anything).Return(nil)
	mockJob.On("MarkCompleted", context.Background(), jobID, captionID, mock.Anything, mock.Anything).Return(nil).Maybe()
	mockJob.On("MarkCompleted", context.Background(), mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Ensure the temp audio directory exists
	require.NoError(t, os.MkdirAll(filepath.Dir(audioPath), 0o750))
	// Create a fake audio file so whisper mock can delete it
	require.NoError(t, os.WriteFile(audioPath, []byte("fake-audio"), 0o600))

	svc := &service{
		jobRepo:     mockJob,
		captionRepo: mockCap,
		videoRepo:   mockVid,
		whisperCli:  mockWhi,
		uploadsDir:  uploadsDir,
		s3Backend:   s3Backend,
	}

	err := svc.processJob(context.Background(), job)
	require.NoError(t, err)
	mockWhi.AssertCalled(t, "ExtractAudioFromVideo", context.Background(), mock.AnythingOfType("string"), audioPath)
}
