package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/testutil"
	ucenc "vidra-core/internal/usecase/encoding"

	"github.com/google/uuid"
)

func main() {
	spec := testutil.TestVideoSpec{
		Name:       "360p",
		Width:      640,
		Height:     360,
		Resolution: "360p",
	}
	videoPath, err := testutil.EnsureTestVideoExists(spec)
	if err != nil {
		log.Fatalf("Failed to generate test video: %v", err)
	}

	tempDir := "/tmp/encoding_test_" + uuid.NewString()
	if err := os.MkdirAll(tempDir, 0750); err != nil {
		log.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	encodingRepo := &mockEncodingRepository{jobs: make(map[string]*domain.EncodingJob)}
	videoRepo := &mockVideoRepository{videos: make(map[string]*domain.Video)}

	cfg := &config.Config{
		FFMPEGPath:         "/opt/homebrew/bin/ffmpeg",
		HLSSegmentDuration: 4,
	}

	service := ucenc.NewService(encodingRepo, videoRepo, nil, tempDir, cfg, nil, nil, nil)

	metadata, err := testutil.GetVideoMetadata(videoPath)
	if err != nil {
		log.Fatalf("Failed to get metadata: %v", err)
	}

	fmt.Printf("Source video: %dx%d, codec: %s, duration: %.2f fps\n",
		metadata.Width, metadata.Height, metadata.VideoCodec, metadata.Framerate)

	videoID := uuid.NewString()
	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           videoID,
		SourceFilePath:    videoPath,
		SourceResolution:  "360p",
		TargetResolutions: []string{"360p", "240p"},
		Status:            domain.EncodingStatusPending,
		Progress:          0,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if err := encodingRepo.CreateJob(context.Background(), job); err != nil {
		log.Fatalf("Failed to create job: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	fmt.Printf("Starting encoding job for %s...\n", videoID)
	start := time.Now()

	processed, err := service.ProcessNext(ctx)
	elapsed := time.Since(start)

	if err != nil {
		log.Fatalf("Encoding failed: %v", err)
	}

	if !processed {
		log.Fatalf("No job was processed")
	}

	fmt.Printf("Encoding completed successfully in %v\n", elapsed)

	fmt.Printf("Checking outputs in %s\n", tempDir)

	hlsDir := fmt.Sprintf("%s/streaming-playlists/hls/%s", tempDir, videoID)
	if _, err := os.Stat(hlsDir); err != nil {
		log.Fatalf("HLS directory not created: %v", err)
	}

	masterPlaylist := fmt.Sprintf("%s/master.m3u8", hlsDir)
	if _, err := os.Stat(masterPlaylist); err != nil {
		log.Fatalf("Master playlist not created: %v", err)
	}

	fmt.Printf("✅ Master playlist created: %s\n", masterPlaylist)

	for _, res := range []string{"360p", "240p"} {
		height, _ := domain.HeightForResolution(res)
		resDir := fmt.Sprintf("%s/%dp", hlsDir, height)
		playlist := fmt.Sprintf("%s/stream.m3u8", resDir)

		if _, err := os.Stat(resDir); err != nil {
			log.Fatalf("Resolution directory not created: %s", resDir)
		}

		if _, err := os.Stat(playlist); err != nil {
			log.Fatalf("Resolution playlist not created: %s", playlist)
		}

		fmt.Printf("✅ %s encoding created: %s\n", res, playlist)
	}

	thumbnailPath := fmt.Sprintf("%s/thumbnails/%s_thumb.jpg", tempDir, videoID)
	if _, err := os.Stat(thumbnailPath); err != nil {
		log.Fatalf("Thumbnail not created: %v", err)
	}
	fmt.Printf("✅ Thumbnail created: %s\n", thumbnailPath)

	previewPath := fmt.Sprintf("%s/previews/%s_preview.webp", tempDir, videoID)
	if _, err := os.Stat(previewPath); err != nil {
		log.Fatalf("Preview not created: %v", err)
	}
	fmt.Printf("✅ Preview created: %s\n", previewPath)

	fmt.Println("\n🎉 All encoding outputs verified successfully!")
}

type mockEncodingRepository struct {
	jobs map[string]*domain.EncodingJob
}

func (r *mockEncodingRepository) CreateJob(ctx context.Context, job *domain.EncodingJob) error {
	r.jobs[job.ID] = job
	return nil
}

func (r *mockEncodingRepository) GetJob(ctx context.Context, id string) (*domain.EncodingJob, error) {
	job, exists := r.jobs[id]
	if !exists {
		return nil, fmt.Errorf("JOB_NOT_FOUND")
	}
	return job, nil
}

func (r *mockEncodingRepository) GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error) {
	for _, job := range r.jobs {
		if job.VideoID == videoID {
			return job, nil
		}
	}
	return nil, fmt.Errorf("JOB_NOT_FOUND")
}

func (r *mockEncodingRepository) UpdateJob(ctx context.Context, job *domain.EncodingJob) error {
	r.jobs[job.ID] = job
	return nil
}

func (r *mockEncodingRepository) UpdateJobStatus(ctx context.Context, id string, status domain.EncodingStatus) error {
	if job, exists := r.jobs[id]; exists {
		job.Status = status
		job.UpdatedAt = time.Now()
	}
	return nil
}

func (r *mockEncodingRepository) UpdateJobProgress(ctx context.Context, id string, progress int) error {
	if job, exists := r.jobs[id]; exists {
		job.Progress = progress
		job.UpdatedAt = time.Now()
	}
	return nil
}

func (r *mockEncodingRepository) SetJobError(ctx context.Context, id string, errorMsg string) error {
	if job, exists := r.jobs[id]; exists {
		job.Status = domain.EncodingStatusFailed
		job.ErrorMessage = errorMsg
		job.UpdatedAt = time.Now()
	}
	return nil
}

func (r *mockEncodingRepository) GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error) {
	var pending []*domain.EncodingJob
	for _, job := range r.jobs {
		if job.Status == domain.EncodingStatusPending && len(pending) < limit {
			pending = append(pending, job)
		}
	}
	return pending, nil
}

func (r *mockEncodingRepository) GetNextJob(ctx context.Context) (*domain.EncodingJob, error) {
	for _, job := range r.jobs {
		if job.Status == domain.EncodingStatusPending {
			job.Status = domain.EncodingStatusProcessing
			job.UpdatedAt = time.Now()
			now := time.Now()
			job.StartedAt = &now
			return job, nil
		}
	}
	return nil, nil
}

func (r *mockEncodingRepository) DeleteJob(ctx context.Context, id string) error {
	delete(r.jobs, id)
	return nil
}

func (r *mockEncodingRepository) GetJobCounts(ctx context.Context) (map[string]int64, error) {
	counts := make(map[string]int64)
	for _, job := range r.jobs {
		counts[string(job.Status)]++
	}
	return counts, nil
}

func (r *mockEncodingRepository) ResetStaleJobs(ctx context.Context, staleDuration time.Duration) (int64, error) {
	var count int64
	cutoff := time.Now().Add(-staleDuration)
	for _, job := range r.jobs {
		if job.Status == domain.EncodingStatusProcessing && job.UpdatedAt.Before(cutoff) {
			job.Status = domain.EncodingStatusPending
			job.Progress = 0
			job.StartedAt = nil
			job.ErrorMessage = ""
			job.UpdatedAt = time.Now()
			count++
		}
	}
	return count, nil
}

func (r *mockEncodingRepository) GetJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error) {
	var jobs []*domain.EncodingJob
	for _, job := range r.jobs {
		if job.VideoID == videoID {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (r *mockEncodingRepository) GetActiveJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error) {
	var jobs []*domain.EncodingJob
	for _, job := range r.jobs {
		if job.VideoID == videoID && (job.Status == domain.EncodingStatusPending || job.Status == domain.EncodingStatusProcessing) {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (r *mockEncodingRepository) ListJobsByStatus(_ context.Context, _ string) ([]*domain.EncodingJob, error) {
	return nil, nil
}

type mockVideoRepository struct {
	videos map[string]*domain.Video
}

func (r *mockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	r.videos[video.ID] = video
	return nil
}

func (r *mockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	video, exists := r.videos[id]
	if !exists {
		return nil, fmt.Errorf("VIDEO_NOT_FOUND")
	}
	return video, nil
}

func (r *mockVideoRepository) Update(ctx context.Context, video *domain.Video) error {
	r.videos[video.ID] = video
	return nil
}

func (r *mockVideoRepository) Delete(ctx context.Context, id string, userID string) error {
	delete(r.videos, id)
	return nil
}

func (r *mockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (r *mockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (r *mockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (r *mockVideoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	if video, exists := r.videos[videoID]; exists {
		video.Status = status
		video.OutputPaths = outputPaths
		video.ThumbnailPath = thumbnailPath
		video.PreviewPath = previewPath
		video.UpdatedAt = time.Now()
	}
	return nil
}

func (r *mockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	if video, exists := r.videos[videoID]; exists {
		video.Status = status
		video.OutputPaths = outputPaths
		video.ThumbnailPath = thumbnailPath
		video.PreviewPath = previewPath
		video.ProcessedCIDs = processedCIDs
		video.ThumbnailCID = thumbnailCID
		video.UpdatedAt = time.Now()
	}
	return nil
}

func (r *mockVideoRepository) Count(ctx context.Context) (int64, error) {
	return int64(len(r.videos)), nil
}

func (r *mockVideoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	return nil, nil
}

func (r *mockVideoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	r.videos[video.ID] = video
	return nil
}

func (r *mockVideoRepository) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	return nil, nil
}

func (r *mockVideoRepository) GetByChannelID(ctx context.Context, channelID string, limit, offset int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (r *mockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	return []*domain.Video{}, nil
}

func (r *mockVideoRepository) GetVideoQuotaUsed(ctx context.Context, userID string) (int64, error) {
	return 0, nil
}
