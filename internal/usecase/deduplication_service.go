package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"athena/internal/domain"
)

// DeduplicationStrategy defines how to handle duplicate content
type DeduplicationStrategy string

const (
	StrategyKeepLatest   DeduplicationStrategy = "latest"
	StrategyKeepOriginal DeduplicationStrategy = "original"
	StrategyMerge        DeduplicationStrategy = "merge"
	StrategyManual       DeduplicationStrategy = "manual"
)

// DeduplicationService handles duplicate detection and resolution
type DeduplicationService interface {
	// DetectDuplicate checks if a post is a duplicate
	DetectDuplicate(ctx context.Context, post *domain.FederatedPost) (*domain.FederatedPost, bool, error)
	// ResolveDuplicate handles duplicate resolution based on strategy
	ResolveDuplicate(ctx context.Context, original, duplicate *domain.FederatedPost, strategy DeduplicationStrategy) error
	// CalculateContentHash generates a hash for deduplication
	CalculateContentHash(post *domain.FederatedPost) string
	// GetDuplicates returns all duplicates of a post
	GetDuplicates(ctx context.Context, postID string) ([]domain.FederatedPost, error)
	// MergePosts merges duplicate posts into canonical version
	MergePosts(ctx context.Context, canonicalID string, duplicateIDs []string) error
}

type deduplicationService struct {
	fedRepo   FederationRepository
	hardening HardeningRepository
}

// NewDeduplicationService creates a new deduplication service
func NewDeduplicationService(fedRepo FederationRepository, hardening HardeningRepository) DeduplicationService {
	return &deduplicationService{
		fedRepo:   fedRepo,
		hardening: hardening,
	}
}

// CalculateContentHash generates a deterministic hash for duplicate detection
func (s *deduplicationService) CalculateContentHash(post *domain.FederatedPost) string {
	// Create a stable representation of the post content
	var textContent string
	if post.Text != nil {
		textContent = *post.Text
	}

	var embedURL string
	if post.EmbedURL != nil {
		embedURL = *post.EmbedURL
	}

	var cid string
	if post.CID != nil {
		cid = *post.CID
	}

	// Concatenate key fields with delimiters
	content := fmt.Sprintf("%s|%s|%s|%s|%s",
		post.ActorDID,
		post.URI,
		textContent,
		embedURL,
		cid,
	)

	// Generate SHA256 hash
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// DetectDuplicate checks if a post already exists
func (s *deduplicationService) DetectDuplicate(ctx context.Context, post *domain.FederatedPost) (*domain.FederatedPost, bool, error) {
	contentHash := s.CalculateContentHash(post)

	// Check for existing post with same content hash
	existing, err := s.fedRepo.GetPostByContentHash(ctx, contentHash)
	if err != nil {
		return nil, false, fmt.Errorf("check duplicate: %w", err)
	}

	if existing != nil && existing.ID != post.ID {
		// Record detection metric
		if s.hardening != nil {
			_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
				MetricType:  "duplicate_detected",
				MetricValue: 1,
				ActorDID:    &post.ActorDID,
				Timestamp:   time.Now(),
			})
		}
		return existing, true, nil
	}

	return nil, false, nil
}

// ResolveDuplicate handles duplicate resolution based on configured strategy
func (s *deduplicationService) ResolveDuplicate(ctx context.Context, original, duplicate *domain.FederatedPost, strategy DeduplicationStrategy) error {
	switch strategy {
	case StrategyKeepLatest:
		return s.resolveKeepLatest(ctx, original, duplicate)
	case StrategyKeepOriginal:
		return s.resolveKeepOriginal(ctx, original, duplicate)
	case StrategyMerge:
		return s.resolveMerge(ctx, original, duplicate)
	case StrategyManual:
		// Mark for manual review
		return s.markForManualReview(ctx, original, duplicate)
	default:
		return fmt.Errorf("unknown deduplication strategy: %s", strategy)
	}
}

// resolveKeepLatest keeps the most recent version
func (s *deduplicationService) resolveKeepLatest(ctx context.Context, original, duplicate *domain.FederatedPost) error {
	// Compare timestamps
	var originalTime, duplicateTime time.Time

	if original.CreatedAt != nil {
		originalTime = *original.CreatedAt
	} else {
		originalTime = original.InsertedAt
	}

	if duplicate.CreatedAt != nil {
		duplicateTime = *duplicate.CreatedAt
	} else {
		duplicateTime = duplicate.InsertedAt
	}

	if duplicateTime.After(originalTime) {
		// Swap canonical status
		if err := s.fedRepo.UpdatePostCanonical(ctx, original.ID, false); err != nil {
			return err
		}
		if err := s.fedRepo.UpdatePostCanonical(ctx, duplicate.ID, true); err != nil {
			return err
		}
		// Update duplicate reference
		if err := s.fedRepo.UpdatePostDuplicateOf(ctx, original.ID, &duplicate.ID); err != nil {
			return err
		}
	} else {
		// Keep original as canonical
		if err := s.fedRepo.UpdatePostDuplicateOf(ctx, duplicate.ID, &original.ID); err != nil {
			return err
		}
		if err := s.fedRepo.UpdatePostCanonical(ctx, duplicate.ID, false); err != nil {
			return err
		}
	}

	return s.recordResolution(ctx, original.ID, duplicate.ID, "keep_latest")
}

// resolveKeepOriginal always keeps the first seen version
func (s *deduplicationService) resolveKeepOriginal(ctx context.Context, original, duplicate *domain.FederatedPost) error {
	// Mark duplicate as non-canonical
	if err := s.fedRepo.UpdatePostDuplicateOf(ctx, duplicate.ID, &original.ID); err != nil {
		return err
	}
	if err := s.fedRepo.UpdatePostCanonical(ctx, duplicate.ID, false); err != nil {
		return err
	}

	return s.recordResolution(ctx, original.ID, duplicate.ID, "keep_original")
}

// resolveMerge combines information from both posts
func (s *deduplicationService) resolveMerge(ctx context.Context, original, duplicate *domain.FederatedPost) error {
	// Merge strategy: combine fields, preferring non-nil values
	merged := *original

	// Merge text content - concatenate if both exist
	if duplicate.Text != nil {
		if merged.Text == nil {
			merged.Text = duplicate.Text
		} else if *duplicate.Text != *merged.Text {
			// Concatenate with separator
			combinedText := fmt.Sprintf("%s\n---\n%s", *merged.Text, *duplicate.Text)
			merged.Text = &combinedText
		}
	}

	// Merge embed information - prefer duplicate if original is nil
	if merged.EmbedURL == nil && duplicate.EmbedURL != nil {
		merged.EmbedURL = duplicate.EmbedURL
		merged.EmbedTitle = duplicate.EmbedTitle
		merged.EmbedDescription = duplicate.EmbedDescription
		merged.EmbedType = duplicate.EmbedType
	}

	// Merge labels
	if duplicate.Labels != nil {
		if merged.Labels == nil {
			merged.Labels = duplicate.Labels
		} else {
			// TODO: Merge JSON labels properly
		}
	}

	// Update the original with merged content
	if err := s.fedRepo.UpsertPost(ctx, &merged); err != nil {
		return err
	}

	// Mark duplicate as non-canonical
	if err := s.fedRepo.UpdatePostDuplicateOf(ctx, duplicate.ID, &original.ID); err != nil {
		return err
	}
	if err := s.fedRepo.UpdatePostCanonical(ctx, duplicate.ID, false); err != nil {
		return err
	}

	return s.recordResolution(ctx, original.ID, duplicate.ID, "merge")
}

// markForManualReview flags duplicates for human review
func (s *deduplicationService) markForManualReview(ctx context.Context, original, duplicate *domain.FederatedPost) error {
	// Create an abuse report for manual review
	if s.hardening != nil {
		report := &domain.FederationAbuseReport{
			ReporterDID:        "system",
			ReportedContentURI: &duplicate.URI,
			ReportedActorDID:   &duplicate.ActorDID,
			ReportType:         "duplicate_content",
			Description: fmt.Sprintf("Duplicate content detected. Original: %s, Duplicate: %s",
				original.URI, duplicate.URI),
			Status:    "pending",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := s.hardening.CreateAbuseReport(ctx, report); err != nil {
			return err
		}
	}

	return s.recordResolution(ctx, original.ID, duplicate.ID, "manual")
}

// recordResolution logs the duplicate resolution
func (s *deduplicationService) recordResolution(ctx context.Context, originalID, duplicateID string, resolutionType string) error {
	if s.hardening != nil {
		// Record resolution metric
		_ = s.hardening.RecordMetric(ctx, &domain.FederationMetric{
			MetricType:  "duplicate_resolved",
			MetricValue: 1,
			Metadata: map[string]interface{}{
				"resolution_type": resolutionType,
				"original_id":     originalID,
				"duplicate_id":    duplicateID,
			},
			Timestamp: time.Now(),
		})
	}
	return nil
}

// GetDuplicates returns all duplicates of a post
func (s *deduplicationService) GetDuplicates(ctx context.Context, postID string) ([]domain.FederatedPost, error) {
	return s.fedRepo.GetPostDuplicates(ctx, postID)
}

// MergePosts merges multiple duplicate posts into a canonical version
func (s *deduplicationService) MergePosts(ctx context.Context, canonicalID string, duplicateIDs []string) error {
	// Get the canonical post
	canonical, err := s.fedRepo.GetPost(ctx, canonicalID)
	if err != nil {
		return fmt.Errorf("get canonical post: %w", err)
	}

	// Process each duplicate
	for _, dupID := range duplicateIDs {
		dup, err := s.fedRepo.GetPost(ctx, dupID)
		if err != nil {
			continue
		}

		// Merge into canonical
		if err := s.resolveMerge(ctx, canonical, dup); err != nil {
			return fmt.Errorf("merge duplicate %s: %w", dupID, err)
		}
	}

	return nil
}

// Additional repository methods needed:

type FederationRepositoryExt interface {
	FederationRepository
	// Deduplication methods
	GetPostByContentHash(ctx context.Context, hash string) (*domain.FederatedPost, error)
	UpdatePostCanonical(ctx context.Context, id string, canonical bool) error
	UpdatePostDuplicateOf(ctx context.Context, id string, duplicateOf *string) error
	GetPostDuplicates(ctx context.Context, postID string) ([]domain.FederatedPost, error)
	GetPost(ctx context.Context, id string) (*domain.FederatedPost, error)
}
