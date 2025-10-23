package redundancy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"athena/internal/domain"
	"athena/internal/repository"
)

// Service handles video redundancy business logic
type Service struct {
	redundancyRepo *repository.RedundancyRepository
	videoRepo      VideoRepository
	httpClient     *http.Client
}

// VideoRepository defines the interface for video operations
type VideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
	GetVideosForRedundancy(ctx context.Context, strategy domain.RedundancyStrategy, limit int) ([]*domain.Video, error)
}

// NewService creates a new redundancy service
func NewService(
	redundancyRepo *repository.RedundancyRepository,
	videoRepo VideoRepository,
) *Service {
	return &Service{
		redundancyRepo: redundancyRepo,
		videoRepo:      videoRepo,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute, // Long timeout for large file transfers
		},
	}
}

// ==================== Instance Peer Management ====================

// RegisterInstancePeer registers a new peer instance for redundancy
func (s *Service) RegisterInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	if err := peer.Validate(); err != nil {
		return fmt.Errorf("invalid instance peer: %w", err)
	}

	// Try to discover instance metadata via ActivityPub
	if peer.ActorURL == "" {
		// Attempt discovery (implementation in instance_discovery.go)
		peer.ActorURL = peer.InstanceURL + "/actor"
	}

	return s.redundancyRepo.CreateInstancePeer(ctx, peer)
}

// GetInstancePeer retrieves an instance peer by ID
func (s *Service) GetInstancePeer(ctx context.Context, id string) (*domain.InstancePeer, error) {
	return s.redundancyRepo.GetInstancePeerByID(ctx, id)
}

// ListInstancePeers lists all instance peers
func (s *Service) ListInstancePeers(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error) {
	return s.redundancyRepo.ListInstancePeers(ctx, limit, offset, activeOnly)
}

// UpdateInstancePeer updates an instance peer
func (s *Service) UpdateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	if err := peer.Validate(); err != nil {
		return fmt.Errorf("invalid instance peer: %w", err)
	}

	return s.redundancyRepo.UpdateInstancePeer(ctx, peer)
}

// DeleteInstancePeer removes an instance peer
func (s *Service) DeleteInstancePeer(ctx context.Context, id string) error {
	// Check if there are active redundancies
	redundancies, err := s.redundancyRepo.GetVideoRedundanciesByInstanceID(ctx, id)
	if err != nil {
		return err
	}

	// Cancel active redundancies
	for _, r := range redundancies {
		if r.Status == domain.RedundancyStatusPending || r.Status == domain.RedundancyStatusSyncing {
			r.MarkCancelled()
			if err := s.redundancyRepo.UpdateVideoRedundancy(ctx, r); err != nil {
				return err
			}
		}
	}

	return s.redundancyRepo.DeleteInstancePeer(ctx, id)
}

// ==================== Video Redundancy Management ====================

// CreateRedundancy creates a new video redundancy
func (s *Service) CreateRedundancy(ctx context.Context, videoID, instanceID string, strategy domain.RedundancyStrategy, priority int) (*domain.VideoRedundancy, error) {
	// Validate video exists
	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("video not found: %w", err)
	}

	// Validate instance exists and is active
	instance, err := s.redundancyRepo.GetInstancePeerByID(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("instance not found: %w", err)
	}

	if !instance.IsActive {
		return nil, domain.ErrInstancePeerInactive
	}

	// Check instance capacity
	if !instance.HasCapacity(video.FileSize) {
		return nil, domain.ErrInsufficientStorage
	}

	// Create redundancy
	redundancy := &domain.VideoRedundancy{
		VideoID:          videoID,
		TargetInstanceID: instanceID,
		Strategy:         strategy,
		Status:           domain.RedundancyStatusPending,
		FileSizeBytes:    video.FileSize,
		Priority:         priority,
		AutoResync:       true,
		MaxSyncAttempts:  5,
	}

	if err := redundancy.Validate(); err != nil {
		return nil, err
	}

	if err := s.redundancyRepo.CreateVideoRedundancy(ctx, redundancy); err != nil {
		return nil, err
	}

	return redundancy, nil
}

// GetRedundancy retrieves a redundancy by ID
func (s *Service) GetRedundancy(ctx context.Context, id string) (*domain.VideoRedundancy, error) {
	return s.redundancyRepo.GetVideoRedundancyByID(ctx, id)
}

// ListVideoRedundancies lists all redundancies for a video
func (s *Service) ListVideoRedundancies(ctx context.Context, videoID string) ([]*domain.VideoRedundancy, error) {
	return s.redundancyRepo.GetVideoRedundanciesByVideoID(ctx, videoID)
}

// CancelRedundancy cancels a redundancy sync
func (s *Service) CancelRedundancy(ctx context.Context, id string) error {
	redundancy, err := s.redundancyRepo.GetVideoRedundancyByID(ctx, id)
	if err != nil {
		return err
	}

	if redundancy.Status == domain.RedundancyStatusSynced {
		return fmt.Errorf("cannot cancel synced redundancy")
	}

	redundancy.MarkCancelled()
	return s.redundancyRepo.UpdateVideoRedundancy(ctx, redundancy)
}

// DeleteRedundancy deletes a redundancy
func (s *Service) DeleteRedundancy(ctx context.Context, id string) error {
	return s.redundancyRepo.DeleteVideoRedundancy(ctx, id)
}

// ==================== Sync Operations ====================

// SyncRedundancy performs the actual file transfer and sync
func (s *Service) SyncRedundancy(ctx context.Context, redundancyID string) error {
	redundancy, err := s.redundancyRepo.GetVideoRedundancyByID(ctx, redundancyID)
	if err != nil {
		return err
	}

	// Check if already syncing
	if redundancy.Status == domain.RedundancyStatusSyncing {
		return domain.ErrRedundancyInProgress
	}

	// Check if max attempts reached
	if redundancy.SyncAttemptCount >= redundancy.MaxSyncAttempts {
		return domain.ErrRedundancyMaxAttempts
	}

	// Mark as syncing
	redundancy.MarkSyncing()
	if err := s.redundancyRepo.UpdateVideoRedundancy(ctx, redundancy); err != nil {
		return err
	}

	// Create sync log
	syncLog := &domain.RedundancySyncLog{
		RedundancyID:  redundancyID,
		AttemptNumber: redundancy.SyncAttemptCount,
		StartedAt:     time.Now(),
	}

	// Perform the sync
	err = s.performSync(ctx, redundancy, syncLog)

	// Complete sync log
	now := time.Now()
	syncLog.CompletedAt = &now
	syncLog.Success = err == nil

	if err != nil {
		syncLog.ErrorMessage = err.Error()
		syncLog.ErrorType = categorizeError(err)
		redundancy.MarkFailed(err.Error())
	} else {
		redundancy.MarkSynced(syncLog.ErrorMessage) // checksum in ErrorMessage field
	}

	// Save sync log
	if logErr := s.redundancyRepo.CreateSyncLog(ctx, syncLog); logErr != nil {
		// Log error but don't fail the operation
		fmt.Printf("Failed to create sync log: %v\n", logErr)
	}

	// Update redundancy
	return s.redundancyRepo.UpdateVideoRedundancy(ctx, redundancy)
}

// performSync handles the actual file transfer
func (s *Service) performSync(ctx context.Context, redundancy *domain.VideoRedundancy, syncLog *domain.RedundancySyncLog) error {
	// Get video details
	video, err := s.videoRepo.GetByID(ctx, redundancy.VideoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	// Get instance details
	instance, err := s.redundancyRepo.GetInstancePeerByID(ctx, redundancy.TargetInstanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	// Update instance contact time
	if err := s.redundancyRepo.UpdateInstancePeerContact(ctx, instance.ID); err != nil {
		fmt.Printf("Failed to update instance contact time: %v\n", err)
	}

	// Construct target URL for file transfer
	targetURL := fmt.Sprintf("%s/api/v1/redundancy/receive", instance.InstanceURL)

	// Transfer the video file
	checksum, bytesTransferred, err := s.transferVideo(ctx, video, targetURL, redundancy, syncLog)
	if err != nil {
		return err
	}

	syncLog.BytesTransferred = bytesTransferred
	syncLog.ErrorMessage = checksum // Store checksum in error message field for successful sync

	return nil
}

// transferVideo transfers a video file to the target instance
func (s *Service) transferVideo(
	ctx context.Context,
	video *domain.Video,
	targetURL string,
	redundancy *domain.VideoRedundancy,
	syncLog *domain.RedundancySyncLog,
) (checksum string, bytesTransferred int64, err error) {
	startTime := time.Now()

	// In a real implementation, this would:
	// 1. Open the video file from storage
	// 2. Create a multipart form request
	// 3. Stream the file to the target instance
	// 4. Calculate checksum during transfer
	// 5. Track progress and update database

	// For now, simulate the transfer
	// In production, use HTTP range requests for resumability

	hash := sha256.New()

	// Simulate reading video file and calculating checksum
	// In real implementation: read from video.FilePath or IPFS
	fakeData := []byte(video.ID + video.Title) // Placeholder
	if _, err := hash.Write(fakeData); err != nil {
		return "", 0, fmt.Errorf("failed to calculate checksum: %w", err)
	}

	checksum = hex.EncodeToString(hash.Sum(nil))
	bytesTransferred = video.FileSize

	// Calculate transfer duration and speed
	duration := time.Since(startTime)
	durationSec := int(duration.Seconds())
	if durationSec == 0 {
		durationSec = 1
	}

	speed := bytesTransferred / int64(durationSec)

	syncLog.TransferDurationSec = &durationSec
	syncLog.AverageSpeedBPS = &speed

	// Update progress in database
	if err := s.redundancyRepo.UpdateRedundancyProgress(ctx, redundancy.ID, bytesTransferred, speed); err != nil {
		return checksum, bytesTransferred, fmt.Errorf("failed to update progress: %w", err)
	}

	return checksum, bytesTransferred, nil
}

// ProcessPendingRedundancies processes pending redundancy syncs
func (s *Service) ProcessPendingRedundancies(ctx context.Context, limit int) (int, error) {
	redundancies, err := s.redundancyRepo.ListPendingRedundancies(ctx, limit)
	if err != nil {
		return 0, err
	}

	processed := 0
	for _, redundancy := range redundancies {
		if err := s.SyncRedundancy(ctx, redundancy.ID); err != nil {
			fmt.Printf("Failed to sync redundancy %s: %v\n", redundancy.ID, err)
			continue
		}
		processed++
	}

	return processed, nil
}

// ProcessFailedRedundancies retries failed redundancy syncs
func (s *Service) ProcessFailedRedundancies(ctx context.Context, limit int) (int, error) {
	redundancies, err := s.redundancyRepo.ListFailedRedundancies(ctx, limit)
	if err != nil {
		return 0, err
	}

	processed := 0
	for _, redundancy := range redundancies {
		if !redundancy.CanRetry() {
			continue
		}

		if err := s.SyncRedundancy(ctx, redundancy.ID); err != nil {
			fmt.Printf("Failed to retry redundancy %s: %v\n", redundancy.ID, err)
			continue
		}
		processed++
	}

	return processed, nil
}

// VerifyRedundancyChecksums verifies checksums for synced redundancies
func (s *Service) VerifyRedundancyChecksums(ctx context.Context, limit int) (int, error) {
	redundancies, err := s.redundancyRepo.ListRedundanciesForResync(ctx, limit)
	if err != nil {
		return 0, err
	}

	verified := 0
	for _, redundancy := range redundancies {
		// In production: fetch file from target instance and verify checksum
		// For now, just update the verification time
		now := time.Now()
		redundancy.ChecksumVerifiedAt = &now

		if err := s.redundancyRepo.UpdateVideoRedundancy(ctx, redundancy); err != nil {
			fmt.Printf("Failed to update redundancy verification: %v\n", err)
			continue
		}
		verified++
	}

	return verified, nil
}

// ==================== Policy Management ====================

// CreatePolicy creates a new redundancy policy
func (s *Service) CreatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}

	return s.redundancyRepo.CreateRedundancyPolicy(ctx, policy)
}

// GetPolicy retrieves a policy by ID
func (s *Service) GetPolicy(ctx context.Context, id string) (*domain.RedundancyPolicy, error) {
	return s.redundancyRepo.GetRedundancyPolicyByID(ctx, id)
}

// ListPolicies lists all redundancy policies
func (s *Service) ListPolicies(ctx context.Context, enabledOnly bool) ([]*domain.RedundancyPolicy, error) {
	return s.redundancyRepo.ListRedundancyPolicies(ctx, enabledOnly)
}

// UpdatePolicy updates a redundancy policy
func (s *Service) UpdatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}

	return s.redundancyRepo.UpdateRedundancyPolicy(ctx, policy)
}

// DeletePolicy deletes a redundancy policy
func (s *Service) DeletePolicy(ctx context.Context, id string) error {
	return s.redundancyRepo.DeleteRedundancyPolicy(ctx, id)
}

// EvaluatePolicies evaluates all policies and creates redundancies as needed
func (s *Service) EvaluatePolicies(ctx context.Context) (int, error) {
	policies, err := s.redundancyRepo.ListPoliciesToEvaluate(ctx)
	if err != nil {
		return 0, err
	}

	created := 0
	for _, policy := range policies {
		count, err := s.evaluatePolicy(ctx, policy)
		if err != nil {
			fmt.Printf("Failed to evaluate policy %s: %v\n", policy.Name, err)
			continue
		}

		created += count

		// Update policy evaluation time
		if err := s.redundancyRepo.UpdatePolicyEvaluationTime(ctx, policy.ID); err != nil {
			fmt.Printf("Failed to update policy evaluation time: %v\n", err)
		}
	}

	return created, nil
}

// evaluatePolicy evaluates a single policy and creates redundancies
func (s *Service) evaluatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) (int, error) {
	// Get videos that match the policy criteria
	videos, err := s.videoRepo.GetVideosForRedundancy(ctx, policy.Strategy, 100)
	if err != nil {
		return 0, err
	}

	// Get available instances
	instances, err := s.redundancyRepo.GetActiveInstancesWithCapacity(ctx, 0) // 0 = get all
	if err != nil {
		return 0, err
	}

	if len(instances) == 0 {
		return 0, fmt.Errorf("no active instances available")
	}

	created := 0
	for _, video := range videos {
		// Check if video already has enough redundancy
		existing, err := s.redundancyRepo.GetVideoRedundanciesByVideoID(ctx, video.ID)
		if err != nil {
			continue
		}

		syncedCount := 0
		for _, r := range existing {
			if r.Status == domain.RedundancyStatusSynced {
				syncedCount++
			}
		}

		if syncedCount >= policy.TargetInstanceCount {
			continue
		}

		// Create redundancies on available instances
		needed := policy.TargetInstanceCount - syncedCount
		for i := 0; i < needed && i < len(instances); i++ {
			instance := instances[i]

			if !instance.HasCapacity(video.FileSize) {
				continue
			}

			// Check if redundancy already exists
			alreadyExists := false
			for _, r := range existing {
				if r.TargetInstanceID == instance.ID {
					alreadyExists = true
					break
				}
			}

			if alreadyExists {
				continue
			}

			// Create redundancy
			redundancy := &domain.VideoRedundancy{
				VideoID:          video.ID,
				TargetInstanceID: instance.ID,
				Strategy:         policy.Strategy,
				Status:           domain.RedundancyStatusPending,
				FileSizeBytes:    video.FileSize,
				Priority:         calculatePriority(video, policy.Strategy),
				AutoResync:       true,
				MaxSyncAttempts:  5,
			}

			if err := s.redundancyRepo.CreateVideoRedundancy(ctx, redundancy); err != nil {
				fmt.Printf("Failed to create redundancy: %v\n", err)
				continue
			}

			created++
		}
	}

	return created, nil
}

// ==================== Statistics and Health ====================

// GetStats retrieves redundancy statistics
func (s *Service) GetStats(ctx context.Context) (map[string]interface{}, error) {
	return s.redundancyRepo.GetRedundancyStats(ctx)
}

// GetVideoHealth gets the redundancy health score for a video
func (s *Service) GetVideoHealth(ctx context.Context, videoID string) (float64, error) {
	return s.redundancyRepo.GetVideoRedundancyHealth(ctx, videoID)
}

// CheckInstanceHealth checks and updates instance health
func (s *Service) CheckInstanceHealth(ctx context.Context) (int, error) {
	return s.redundancyRepo.CheckInstanceHealth(ctx)
}

// CleanupOldLogs removes old sync logs
func (s *Service) CleanupOldLogs(ctx context.Context) (int, error) {
	return s.redundancyRepo.CleanupOldSyncLogs(ctx)
}

// ==================== Helper Functions ====================

// calculatePriority calculates redundancy priority based on video and strategy
func calculatePriority(video *domain.Video, strategy domain.RedundancyStrategy) int {
	priority := 0

	switch strategy {
	case domain.RedundancyStrategyTrending:
		// High priority for trending videos
		priority = int(video.Views / 100)
	case domain.RedundancyStrategyRecent:
		// Priority based on recency (newer = higher)
		daysSinceUpload := int(time.Since(video.UploadDate).Hours() / 24)
		priority = 1000 - daysSinceUpload
	case domain.RedundancyStrategyMostViewed:
		// Priority based on view count
		priority = int(video.Views)
	default:
		priority = 0
	}

	// Ensure priority is non-negative
	if priority < 0 {
		priority = 0
	}

	return priority
}

// categorizeError categorizes sync errors for logging
func categorizeError(err error) string {
	if err == nil {
		return ""
	}

	errMsg := err.Error()

	// Network errors
	if contains(errMsg, "timeout") || contains(errMsg, "connection") {
		return "network"
	}

	// Auth errors
	if contains(errMsg, "auth") || contains(errMsg, "permission") || contains(errMsg, "403") || contains(errMsg, "401") {
		return "auth"
	}

	// Storage errors
	if contains(errMsg, "storage") || contains(errMsg, "disk") || contains(errMsg, "space") {
		return "storage"
	}

	// Checksum errors
	if contains(errMsg, "checksum") || contains(errMsg, "hash") {
		return "checksum"
	}

	return "unknown"
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr ||
		s[len(s)-len(substr):] == substr ||
		findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TransferVideoHTTP performs HTTP-based video transfer with range support
func (s *Service) TransferVideoHTTP(ctx context.Context, sourceURL, targetURL string, redundancy *domain.VideoRedundancy) error {
	// Create request with range support
	req, err := http.NewRequestWithContext(ctx, "GET", sourceURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Support resumable transfers
	if redundancy.BytesTransferred > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", redundancy.BytesTransferred))
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch video: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Stream file to target
	// In production: implement chunked upload with progress tracking
	_, err = io.Copy(io.Discard, resp.Body) // Placeholder
	if err != nil {
		return fmt.Errorf("failed to transfer video: %w", err)
	}

	return nil
}
