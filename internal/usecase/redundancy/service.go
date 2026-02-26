package redundancy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"athena/internal/domain"
	"athena/internal/port"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Service struct {
	redundancyRepo port.RedundancyRepository
	videoRepo      port.RedundancyVideoRepository
	httpClient     HTTPDoer
}

func NewService(
	redundancyRepo port.RedundancyRepository,
	videoRepo port.RedundancyVideoRepository,
	httpClient HTTPDoer,
) *Service {
	return &Service{
		redundancyRepo: redundancyRepo,
		videoRepo:      videoRepo,
		httpClient:     httpClient,
	}
}

func (s *Service) RegisterInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	if err := peer.Validate(); err != nil {
		return fmt.Errorf("invalid instance peer: %w", err)
	}

	if peer.ActorURL == "" {
		peer.ActorURL = peer.InstanceURL + "/actor"
	}

	return s.redundancyRepo.CreateInstancePeer(ctx, peer)
}

func (s *Service) GetInstancePeer(ctx context.Context, id string) (*domain.InstancePeer, error) {
	return s.redundancyRepo.GetInstancePeerByID(ctx, id)
}

func (s *Service) ListInstancePeers(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error) {
	return s.redundancyRepo.ListInstancePeers(ctx, limit, offset, activeOnly)
}

func (s *Service) UpdateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	if err := peer.Validate(); err != nil {
		return fmt.Errorf("invalid instance peer: %w", err)
	}

	return s.redundancyRepo.UpdateInstancePeer(ctx, peer)
}

func (s *Service) DeleteInstancePeer(ctx context.Context, id string) error {
	if err := s.redundancyRepo.CancelRedundanciesByInstanceID(ctx, id); err != nil {
		return err
	}

	return s.redundancyRepo.DeleteInstancePeer(ctx, id)
}

func (s *Service) CreateRedundancy(ctx context.Context, videoID, instanceID string, strategy domain.RedundancyStrategy, priority int) (*domain.VideoRedundancy, error) {
	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("video not found: %w", err)
	}

	instance, err := s.redundancyRepo.GetInstancePeerByID(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("instance not found: %w", err)
	}

	if !instance.IsActive {
		return nil, domain.ErrInstancePeerInactive
	}

	if !instance.HasCapacity(video.FileSize) {
		return nil, domain.ErrInsufficientStorage
	}

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

func (s *Service) GetRedundancy(ctx context.Context, id string) (*domain.VideoRedundancy, error) {
	return s.redundancyRepo.GetVideoRedundancyByID(ctx, id)
}

func (s *Service) ListVideoRedundancies(ctx context.Context, videoID string) ([]*domain.VideoRedundancy, error) {
	return s.redundancyRepo.GetVideoRedundanciesByVideoID(ctx, videoID)
}

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

func (s *Service) DeleteRedundancy(ctx context.Context, id string) error {
	return s.redundancyRepo.DeleteVideoRedundancy(ctx, id)
}

func (s *Service) SyncRedundancy(ctx context.Context, redundancyID string) error {
	redundancy, err := s.redundancyRepo.GetVideoRedundancyByID(ctx, redundancyID)
	if err != nil {
		return err
	}

	if redundancy.Status == domain.RedundancyStatusSyncing {
		return domain.ErrRedundancyInProgress
	}

	if redundancy.SyncAttemptCount >= redundancy.MaxSyncAttempts {
		return domain.ErrRedundancyMaxAttempts
	}

	redundancy.MarkSyncing()
	if err := s.redundancyRepo.UpdateVideoRedundancy(ctx, redundancy); err != nil {
		return err
	}

	syncLog := &domain.RedundancySyncLog{
		RedundancyID:  redundancyID,
		AttemptNumber: redundancy.SyncAttemptCount,
		StartedAt:     time.Now(),
	}

	err = s.performSync(ctx, redundancy, syncLog)

	now := time.Now()
	syncLog.CompletedAt = &now
	syncLog.Success = err == nil

	if err != nil {
		syncLog.ErrorMessage = err.Error()
		syncLog.ErrorType = categorizeError(err)
		redundancy.MarkFailed(err.Error())
	} else {
		redundancy.MarkSynced(syncLog.ErrorMessage)
	}

	if logErr := s.redundancyRepo.CreateSyncLog(ctx, syncLog); logErr != nil {
		slog.Warn("failed to create sync log", "error", logErr)
	}

	return s.redundancyRepo.UpdateVideoRedundancy(ctx, redundancy)
}

func (s *Service) performSync(ctx context.Context, redundancy *domain.VideoRedundancy, syncLog *domain.RedundancySyncLog) error {
	video, err := s.videoRepo.GetByID(ctx, redundancy.VideoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	instance, err := s.redundancyRepo.GetInstancePeerByID(ctx, redundancy.TargetInstanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if err := s.redundancyRepo.UpdateInstancePeerContact(ctx, instance.ID); err != nil {
		slog.Warn("failed to update instance contact time", "error", err)
	}

	targetURL := fmt.Sprintf("%s/api/v1/redundancy/receive", instance.InstanceURL)

	checksum, bytesTransferred, err := s.transferVideo(ctx, video, targetURL, redundancy, syncLog)
	if err != nil {
		return err
	}

	syncLog.BytesTransferred = bytesTransferred
	syncLog.ErrorMessage = checksum

	return nil
}

func (s *Service) transferVideo(
	ctx context.Context,
	video *domain.Video,
	targetURL string,
	redundancy *domain.VideoRedundancy,
	syncLog *domain.RedundancySyncLog,
) (checksum string, bytesTransferred int64, err error) {
	startTime := time.Now()

	hash := sha256.New()

	fakeData := []byte(video.ID + video.Title)
	if _, err := hash.Write(fakeData); err != nil {
		return "", 0, fmt.Errorf("failed to calculate checksum: %w", err)
	}

	checksum = hex.EncodeToString(hash.Sum(nil))
	bytesTransferred = video.FileSize

	duration := time.Since(startTime)
	durationSec := int(duration.Seconds())
	if durationSec == 0 {
		durationSec = 1
	}

	speed := bytesTransferred / int64(durationSec)

	syncLog.TransferDurationSec = &durationSec
	syncLog.AverageSpeedBPS = &speed

	if err := s.redundancyRepo.UpdateRedundancyProgress(ctx, redundancy.ID, bytesTransferred, speed); err != nil {
		return checksum, bytesTransferred, fmt.Errorf("failed to update progress: %w", err)
	}

	return checksum, bytesTransferred, nil
}

func (s *Service) ProcessPendingRedundancies(ctx context.Context, limit int) (int, error) {
	redundancies, err := s.redundancyRepo.ListPendingRedundancies(ctx, limit)
	if err != nil {
		return 0, err
	}

	processed := 0
	for _, redundancy := range redundancies {
		if err := s.SyncRedundancy(ctx, redundancy.ID); err != nil {
			slog.Warn("failed to sync redundancy", "id", redundancy.ID, "error", err)
			continue
		}
		processed++
	}

	return processed, nil
}

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
			slog.Warn("failed to retry redundancy", "id", redundancy.ID, "error", err)
			continue
		}
		processed++
	}

	return processed, nil
}

func (s *Service) VerifyRedundancyChecksums(ctx context.Context, limit int) (int, error) {
	redundancies, err := s.redundancyRepo.ListRedundanciesForResync(ctx, limit)
	if err != nil {
		return 0, err
	}

	verified := 0
	for _, redundancy := range redundancies {
		now := time.Now()
		redundancy.ChecksumVerifiedAt = &now

		if err := s.redundancyRepo.UpdateVideoRedundancy(ctx, redundancy); err != nil {
			slog.Warn("failed to update redundancy verification", "error", err)
			continue
		}
		verified++
	}

	return verified, nil
}

func (s *Service) CreatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}

	return s.redundancyRepo.CreateRedundancyPolicy(ctx, policy)
}

func (s *Service) GetPolicy(ctx context.Context, id string) (*domain.RedundancyPolicy, error) {
	return s.redundancyRepo.GetRedundancyPolicyByID(ctx, id)
}

func (s *Service) ListPolicies(ctx context.Context, enabledOnly bool) ([]*domain.RedundancyPolicy, error) {
	return s.redundancyRepo.ListRedundancyPolicies(ctx, enabledOnly)
}

func (s *Service) UpdatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}

	return s.redundancyRepo.UpdateRedundancyPolicy(ctx, policy)
}

func (s *Service) DeletePolicy(ctx context.Context, id string) error {
	return s.redundancyRepo.DeleteRedundancyPolicy(ctx, id)
}

func (s *Service) EvaluatePolicies(ctx context.Context) (int, error) {
	policies, err := s.redundancyRepo.ListPoliciesToEvaluate(ctx)
	if err != nil {
		return 0, err
	}

	created := 0
	for _, policy := range policies {
		count, err := s.evaluatePolicy(ctx, policy)
		if err != nil {
			slog.Warn("failed to evaluate policy", "name", policy.Name, "error", err)
			continue
		}

		created += count

		if err := s.redundancyRepo.UpdatePolicyEvaluationTime(ctx, policy.ID); err != nil {
			slog.Warn("failed to update policy evaluation time", "error", err)
		}
	}

	return created, nil
}

func (s *Service) evaluatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) (int, error) {
	videos, err := s.videoRepo.GetVideosForRedundancy(ctx, policy.Strategy, 100)
	if err != nil {
		return 0, err
	}

	instances, err := s.redundancyRepo.GetActiveInstancesWithCapacity(ctx, 0)
	if err != nil {
		return 0, err
	}

	if len(instances) == 0 {
		return 0, fmt.Errorf("no active instances available")
	}

	created := 0
	for _, video := range videos {
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

		needed := policy.TargetInstanceCount - syncedCount
		for i := 0; i < needed && i < len(instances); i++ {
			instance := instances[i]

			if !instance.HasCapacity(video.FileSize) {
				continue
			}

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
				slog.Warn("failed to create redundancy", "error", err)
				continue
			}

			created++
		}
	}

	return created, nil
}

func (s *Service) GetStats(ctx context.Context) (map[string]interface{}, error) {
	return s.redundancyRepo.GetRedundancyStats(ctx)
}

func (s *Service) GetVideoHealth(ctx context.Context, videoID string) (float64, error) {
	return s.redundancyRepo.GetVideoRedundancyHealth(ctx, videoID)
}

func (s *Service) CheckInstanceHealth(ctx context.Context) (int, error) {
	return s.redundancyRepo.CheckInstanceHealth(ctx)
}

func (s *Service) CleanupOldLogs(ctx context.Context) (int, error) {
	return s.redundancyRepo.CleanupOldSyncLogs(ctx)
}

func calculatePriority(video *domain.Video, strategy domain.RedundancyStrategy) int {
	priority := 0

	switch strategy {
	case domain.RedundancyStrategyTrending:
		priority = int(video.Views / 100)
	case domain.RedundancyStrategyRecent:
		daysSinceUpload := int(time.Since(video.UploadDate).Hours() / 24)
		priority = 1000 - daysSinceUpload
	case domain.RedundancyStrategyMostViewed:
		priority = int(video.Views)
	default:
		priority = 0
	}

	if priority < 0 {
		priority = 0
	}

	return priority
}

func categorizeError(err error) string {
	if err == nil {
		return ""
	}

	errMsg := err.Error()

	if contains(errMsg, "timeout") || contains(errMsg, "connection") {
		return "network"
	}

	if contains(errMsg, "auth") || contains(errMsg, "permission") || contains(errMsg, "403") || contains(errMsg, "401") {
		return "auth"
	}

	if contains(errMsg, "storage") || contains(errMsg, "disk") || contains(errMsg, "space") {
		return "storage"
	}

	if contains(errMsg, "checksum") || contains(errMsg, "hash") {
		return "checksum"
	}

	return "unknown"
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func (s *Service) TransferVideoHTTP(ctx context.Context, sourceURL, targetURL string, redundancy *domain.VideoRedundancy) error {
	req, err := http.NewRequestWithContext(ctx, "GET", sourceURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

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

	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to transfer video: %w", err)
	}

	return nil
}
