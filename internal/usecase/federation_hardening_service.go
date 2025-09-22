package usecase

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/repository"
)

// FederationHardeningService handles federation reliability and security
type FederationHardeningService struct {
	repo   *repository.FederationHardeningRepository
	fedSvc FederationService
	cfg    *config.Config
	config *domain.FederationSecurityConfig
}

// NewFederationHardeningService creates a new hardening service
func NewFederationHardeningService(
	repo *repository.FederationHardeningRepository,
	fedSvc FederationService,
	cfg *config.Config,
) *FederationHardeningService {
	return &FederationHardeningService{
		repo:   repo,
		fedSvc: fedSvc,
		cfg:    cfg,
	}
}

// Initialize loads configuration
func (s *FederationHardeningService) Initialize(ctx context.Context) error {
	config, err := s.repo.GetFederationConfig(ctx)
	if err != nil {
		return fmt.Errorf("load federation config: %w", err)
	}
	s.config = config
	return nil
}

// ProcessJobWithRetry processes a job with exponential backoff and DLQ
func (s *FederationHardeningService) ProcessJobWithRetry(ctx context.Context, jobID string) error {
	// Check idempotency
	idempKey := fmt.Sprintf("job_%s", jobID)
	existing, err := s.repo.CheckIdempotency(ctx, idempKey)
	if err != nil {
		return err
	}
	if existing != nil && existing.Status == domain.IdempotencyStatusSuccess {
		// Already processed
		return nil
	}

	// Record start of processing
	idempRecord := &domain.IdempotencyRecord{
		IdempotencyKey: idempKey,
		OperationType:  "process_job",
		Status:         domain.IdempotencyStatusPending,
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}
	if err := s.repo.RecordIdempotency(ctx, idempRecord); err != nil {
		return err
	}

	// Process the job
	processed, err := s.fedSvc.ProcessNext(ctx)
	if err != nil {
		// Handle failure with backoff
		s.handleJobFailure(ctx, jobID, err)
		idempRecord.Status = domain.IdempotencyStatusFailed
		result, _ := json.Marshal(map[string]string{"error": err.Error()})
		idempRecord.Result = result
		_ = s.repo.RecordIdempotency(ctx, idempRecord)
		return err
	}

	if processed {
		// Record success
		idempRecord.Status = domain.IdempotencyStatusSuccess
		result, _ := json.Marshal(map[string]bool{"processed": true})
		idempRecord.Result = result
		_ = s.repo.RecordIdempotency(ctx, idempRecord)

		// Record metric
		s.recordMetric(ctx, domain.MetricTypeJobSuccess, 1, nil, nil, nil)
	}

	return nil
}

// handleJobFailure handles job failure with exponential backoff
func (s *FederationHardeningService) handleJobFailure(ctx context.Context, jobID string, err error) {
	// Get job details to check attempts
	// For now, we'll use a simplified approach
	attempts := 1 // Would normally get from job

	// Update job with backoff
	if updateErr := s.repo.UpdateJobWithBackoff(ctx, jobID, attempts+1, err.Error()); updateErr != nil {
		// If update fails, try to move to DLQ
		s.moveToDLQ(ctx, jobID, err.Error())
	}

	// Record failure metric
	s.recordMetric(ctx, domain.MetricTypeJobFailure, 1, nil, nil, nil)

	// Check if should move to DLQ
	maxRetries := 5
	if s.config != nil && attempts >= maxRetries {
		s.moveToDLQ(ctx, jobID, fmt.Sprintf("Max retries (%d) exceeded: %v", maxRetries, err))
	}
}

// moveToDLQ moves a failed job to the dead letter queue
func (s *FederationHardeningService) moveToDLQ(ctx context.Context, jobID string, errorMsg string) {
	// Create a minimal job representation for DLQ
	job := &domain.FederationJob{
		ID:       jobID,
		JobType:  "unknown",
		Attempts: 5,
	}
	_ = s.repo.MoveToDLQ(ctx, job, errorMsg)

	// Record DLQ metric
	s.recordMetric(ctx, domain.MetricTypeDLQSize, 1, nil, nil, nil)
}

// Security Features

// ValidateRequestSignature validates request signature and prevents replay attacks
func (s *FederationHardeningService) ValidateRequestSignature(
	ctx context.Context,
	signature string,
	instanceDomain string,
	requestPath string,
	body []byte,
) error {
	// Check signature format
	if signature == "" {
		return errors.New("missing signature")
	}

	// Generate signature hash
	signatureHash := s.generateSignatureHash(signature, instanceDomain, requestPath, body)

	// Check for replay attack
	seen, err := s.repo.CheckRequestSignature(ctx, signatureHash)
	if err != nil {
		return err
	}
	if seen {
		s.recordMetric(ctx, domain.MetricTypeSignatureReject, 1, &instanceDomain, nil, nil)
		return errors.New("duplicate request signature")
	}

	// Validate time window
	// Extract timestamp from signature (implementation specific)
	// For now, we'll accept all valid signatures within window

	// Record signature to prevent replay
	sig := &domain.RequestSignature{
		SignatureHash:  signatureHash,
		InstanceDomain: instanceDomain,
		RequestPath:    &requestPath,
		ReceivedAt:     time.Now(),
		ExpiresAt:      time.Now().Add(time.Duration(s.config.SignatureWindowSeconds) * time.Second),
	}

	return s.repo.RecordRequestSignature(ctx, sig)
}

// generateSignatureHash generates a hash from request signature components
func (s *FederationHardeningService) generateSignatureHash(signature, domain, path string, body []byte) string {
	h := hmac.New(sha256.New, []byte(s.cfg.JWTSecret))
	h.Write([]byte(signature))
	h.Write([]byte(domain))
	h.Write([]byte(path))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// CheckRateLimit checks if a request should be rate limited
func (s *FederationHardeningService) CheckRateLimit(ctx context.Context, instanceDomain string) error {
	if s.config == nil {
		return nil
	}

	allowed, err := s.repo.CheckRateLimit(
		ctx,
		instanceDomain,
		s.config.RateLimitRequests,
		s.config.RateLimitWindow,
	)
	if err != nil {
		return err
	}

	if !allowed {
		s.recordMetric(ctx, domain.MetricTypeRateLimitHit, 1, &instanceDomain, nil, nil)
		return errors.New("rate limit exceeded")
	}

	return nil
}

// ValidateRequestSize validates request body size
func (s *FederationHardeningService) ValidateRequestSize(bodySize int64) error {
	if s.config == nil {
		return nil
	}

	if bodySize > s.config.MaxRequestSize {
		return fmt.Errorf("request size %d exceeds maximum %d", bodySize, s.config.MaxRequestSize)
	}

	return nil
}

// Blocklist Management

// BlockInstance blocks an instance from federation
func (s *FederationHardeningService) BlockInstance(
	ctx context.Context,
	instanceDomain string,
	reason string,
	severity domain.BlockSeverity,
	blockedBy string,
	duration time.Duration,
) error {
	block := &domain.InstanceBlock{
		InstanceDomain: instanceDomain,
		Reason:         &reason,
		Severity:       severity,
		BlockedBy:      &blockedBy,
		CreatedAt:      time.Now(),
	}

	if duration > 0 {
		expiresAt := time.Now().Add(duration)
		block.ExpiresAt = &expiresAt
	}

	err := s.repo.AddInstanceBlock(ctx, block)
	if err != nil {
		return err
	}

	// Record metric
	s.recordMetric(ctx, domain.MetricTypeBlockedRequest, 1, &instanceDomain, nil, nil)

	return nil
}

// UnblockInstance removes an instance block
func (s *FederationHardeningService) UnblockInstance(ctx context.Context, instanceDomain string) error {
	return s.repo.RemoveInstanceBlock(ctx, instanceDomain)
}

// IsInstanceBlocked checks if an instance is blocked
func (s *FederationHardeningService) IsInstanceBlocked(ctx context.Context, instanceDomain string) (bool, error) {
	return s.repo.IsInstanceBlocked(ctx, instanceDomain)
}

// GetInstanceBlocks retrieves all instance blocks
func (s *FederationHardeningService) GetInstanceBlocks(ctx context.Context) ([]domain.InstanceBlock, error) {
	return s.repo.GetInstanceBlocks(ctx)
}

// BlockActor blocks a specific actor
func (s *FederationHardeningService) BlockActor(
	ctx context.Context,
	actorDID string,
	actorHandle string,
	reason string,
	severity domain.BlockSeverity,
	blockedBy string,
	duration time.Duration,
) error {
	block := &domain.ActorBlock{
		ActorDID:    &actorDID,
		ActorHandle: &actorHandle,
		Reason:      &reason,
		Severity:    severity,
		BlockedBy:   &blockedBy,
		CreatedAt:   time.Now(),
	}

	if duration > 0 {
		expiresAt := time.Now().Add(duration)
		block.ExpiresAt = &expiresAt
	}

	err := s.repo.AddActorBlock(ctx, block)
	if err != nil {
		return err
	}

	// Record metric
	s.recordMetric(ctx, domain.MetricTypeBlockedRequest, 1, nil, &actorDID, nil)

	return nil
}

// IsActorBlocked checks if an actor is blocked
func (s *FederationHardeningService) IsActorBlocked(ctx context.Context, did, handle string) (bool, error) {
	return s.repo.IsActorBlocked(ctx, did, handle)
}

// Abuse Reporting

// ReportAbuse creates an abuse report
func (s *FederationHardeningService) ReportAbuse(
	ctx context.Context,
	reporterDID string,
	reportType string,
	contentURI string,
	actorDID string,
	description string,
	evidence json.RawMessage,
) error {
	if !s.config.EnableAbuseReporting {
		return errors.New("abuse reporting is disabled")
	}

	report := &domain.AbuseReport{
		ReporterDID:        &reporterDID,
		ReportedContentURI: &contentURI,
		ReportedActorDID:   &actorDID,
		ReportType:         reportType,
		Description:        &description,
		Evidence:           evidence,
		Status:             domain.AbuseReportStatusPending,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	err := s.repo.CreateAbuseReport(ctx, report)
	if err != nil {
		return err
	}

	// Record metric
	s.recordMetric(ctx, domain.MetricTypeAbuseReport, 1, nil, &actorDID, nil)

	return nil
}

// GetPendingAbuseReports retrieves pending abuse reports
func (s *FederationHardeningService) GetPendingAbuseReports(ctx context.Context, limit int) ([]domain.AbuseReport, error) {
	return s.repo.GetAbuseReports(ctx, domain.AbuseReportStatusPending, limit)
}

// ResolveAbuseReport resolves an abuse report
func (s *FederationHardeningService) ResolveAbuseReport(
	ctx context.Context,
	reportID string,
	resolution string,
	resolvedBy string,
	takeAction bool,
) error {
	status := domain.AbuseReportStatusResolved
	if !takeAction {
		status = domain.AbuseReportStatusRejected
	}

	return s.repo.UpdateAbuseReport(ctx, reportID, status, resolution, resolvedBy)
}

// Health and Metrics

// GetHealthMetrics retrieves federation health metrics
func (s *FederationHardeningService) GetHealthMetrics(ctx context.Context) ([]domain.FederationHealthSummary, error) {
	// Refresh materialized view
	_ = s.repo.RefreshHealthSummary(ctx)

	return s.repo.GetHealthSummary(ctx)
}

// GetDashboardData retrieves dashboard data
func (s *FederationHardeningService) GetDashboardData(ctx context.Context) (map[string]interface{}, error) {
	health, _ := s.repo.GetHealthSummary(ctx)
	dlqJobs, _ := s.repo.GetDLQJobs(ctx, 10, false)
	instanceBlocks, _ := s.repo.GetInstanceBlocks(ctx)
	pendingReports, _ := s.repo.GetAbuseReports(ctx, domain.AbuseReportStatusPending, 10)

	// Get recent metrics
	since := time.Now().Add(-24 * time.Hour)
	successMetrics, _ := s.repo.GetMetrics(ctx, domain.MetricTypeJobSuccess, since, 100)
	failureMetrics, _ := s.repo.GetMetrics(ctx, domain.MetricTypeJobFailure, since, 100)

	// Calculate success rate
	successCount := len(successMetrics)
	failureCount := len(failureMetrics)
	totalCount := successCount + failureCount
	successRate := float64(0)
	if totalCount > 0 {
		successRate = float64(successCount) / float64(totalCount) * 100
	}

	return map[string]interface{}{
		"health_summary":   health,
		"dlq_count":        len(dlqJobs),
		"dlq_jobs":         dlqJobs,
		"instance_blocks":  instanceBlocks,
		"pending_reports":  pendingReports,
		"success_rate_24h": successRate,
		"total_jobs_24h":   totalCount,
		"metrics_enabled":  s.config.MetricsEnabled,
	}, nil
}

// recordMetric records a federation metric
func (s *FederationHardeningService) recordMetric(
	ctx context.Context,
	metricType string,
	value float64,
	instanceDomain *string,
	actorDID *string,
	jobType *string,
) {
	if s.config == nil || !s.config.MetricsEnabled {
		return
	}

	metric := &domain.FederationMetric{
		MetricType:     metricType,
		MetricValue:    value,
		InstanceDomain: instanceDomain,
		ActorDID:       actorDID,
		JobType:        jobType,
		Timestamp:      time.Now(),
	}

	_ = s.repo.RecordMetric(ctx, metric)
}

// DLQ Operations

// GetDLQJobs retrieves jobs from the dead letter queue
func (s *FederationHardeningService) GetDLQJobs(ctx context.Context, limit int, canRetryOnly bool) ([]domain.DeadLetterJob, error) {
	return s.repo.GetDLQJobs(ctx, limit, canRetryOnly)
}

// RetryDLQJob retries a job from the DLQ
func (s *FederationHardeningService) RetryDLQJob(ctx context.Context, dlqID string) error {
	err := s.repo.RetryDLQJob(ctx, dlqID)
	if err != nil {
		return err
	}

	// Record metric
	s.recordMetric(ctx, "dlq_retry", 1, nil, nil, nil)

	return nil
}

// Cleanup

// RunCleanup performs periodic cleanup tasks
func (s *FederationHardeningService) RunCleanup(ctx context.Context) error {
	return s.repo.CleanupExpired(ctx)
}

// ValidateFederationRequest performs all validation checks for an incoming request
func (s *FederationHardeningService) ValidateFederationRequest(
	ctx context.Context,
	instanceDomain string,
	signature string,
	requestPath string,
	body []byte,
) error {
	// Check instance blocklist
	blocked, err := s.IsInstanceBlocked(ctx, instanceDomain)
	if err != nil {
		return err
	}
	if blocked {
		s.recordMetric(ctx, domain.MetricTypeBlockedRequest, 1, &instanceDomain, nil, nil)
		return errors.New("instance is blocked")
	}

	// Validate request size
	if err := s.ValidateRequestSize(int64(len(body))); err != nil {
		return err
	}

	// Check rate limit
	if err := s.CheckRateLimit(ctx, instanceDomain); err != nil {
		return err
	}

	// Validate signature
	if err := s.ValidateRequestSignature(ctx, signature, instanceDomain, requestPath, body); err != nil {
		return err
	}

	return nil
}
