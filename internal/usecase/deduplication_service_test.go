package usecase

import (
	"context"
	"fmt"
	"testing"
	"time"

	"athena/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestDeduplicationService_CalculateContentHash(t *testing.T) {
	tests := []struct {
		name     string
		post     *domain.FederatedPost
		wantLen  int
		scenario string
	}{
		{
			name: "full post with all fields",
			post: &domain.FederatedPost{
				ActorDID: "did:plc:abc123",
				URI:      "at://did:plc:abc123/app.bsky.feed.post/3k1",
				Text:     strPtr("Hello world"),
				EmbedURL: strPtr("https://example.com/video"),
				CID:      strPtr("bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"),
			},
			wantLen:  64,
			scenario: "should generate 64-char SHA256 hash",
		},
		{
			name: "post with nil fields",
			post: &domain.FederatedPost{
				ActorDID: "did:plc:def456",
				URI:      "at://did:plc:def456/app.bsky.feed.post/3k2",
				Text:     nil,
				EmbedURL: nil,
				CID:      nil,
			},
			wantLen:  64,
			scenario: "should handle nil fields gracefully",
		},
		{
			name: "post with empty strings",
			post: &domain.FederatedPost{
				ActorDID: "",
				URI:      "",
				Text:     strPtr(""),
				EmbedURL: strPtr(""),
				CID:      strPtr(""),
			},
			wantLen:  64,
			scenario: "should handle empty strings",
		},
	}

	mockRepo := new(MockFederationRepositoryExt)
	mockHardening := new(MockHardeningRepository)
	service := NewDeduplicationService(mockRepo, mockHardening)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := service.CalculateContentHash(tt.post)
			assert.Len(t, hash, tt.wantLen, tt.scenario)

			// Same content should produce same hash (deterministic)
			hash2 := service.CalculateContentHash(tt.post)
			assert.Equal(t, hash, hash2, "hash should be deterministic")
		})
	}

	// Test that different content produces different hashes
	t.Run("different content produces different hashes", func(t *testing.T) {
		post1 := &domain.FederatedPost{
			ActorDID: "did:plc:same",
			URI:      "at://did:plc:same/app.bsky.feed.post/3k1",
			Text:     strPtr("Content 1"),
		}
		post2 := &domain.FederatedPost{
			ActorDID: "did:plc:same",
			URI:      "at://did:plc:same/app.bsky.feed.post/3k1",
			Text:     strPtr("Content 2"),
		}

		hash1 := service.CalculateContentHash(post1)
		hash2 := service.CalculateContentHash(post2)
		assert.NotEqual(t, hash1, hash2, "different content should produce different hashes")
	})
}

func TestDeduplicationService_DetectDuplicate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		post          *domain.FederatedPost
		existingPost  *domain.FederatedPost
		setupMocks    func(*MockFederationRepositoryExt, *MockHardeningRepository, string)
		wantDuplicate bool
		wantExisting  *domain.FederatedPost
		wantErr       bool
	}{
		{
			name: "duplicate detected",
			post: &domain.FederatedPost{
				ID:       "new-id",
				ActorDID: "did:plc:test",
				URI:      "at://did:plc:test/app.bsky.feed.post/new",
				Text:     strPtr("Duplicate content"),
			},
			existingPost: &domain.FederatedPost{
				ID:       "existing-id",
				ActorDID: "did:plc:test",
				URI:      "at://did:plc:test/app.bsky.feed.post/old",
				Text:     strPtr("Duplicate content"),
			},
			setupMocks: func(repo *MockFederationRepositoryExt, hardening *MockHardeningRepository, hash string) {
				repo.On("GetPostByContentHash", ctx, hash).Return(&domain.FederatedPost{
					ID:       "existing-id",
					ActorDID: "did:plc:test",
					URI:      "at://did:plc:test/app.bsky.feed.post/old",
					Text:     strPtr("Duplicate content"),
				}, nil)
				hardening.On("RecordMetric", ctx, mock.MatchedBy(func(m *domain.FederationMetric) bool {
					return m.MetricType == "duplicate_detected" && m.MetricValue == 1
				})).Return(nil)
			},
			wantDuplicate: true,
			wantExisting: &domain.FederatedPost{
				ID:       "existing-id",
				ActorDID: "did:plc:test",
				URI:      "at://did:plc:test/app.bsky.feed.post/old",
				Text:     strPtr("Duplicate content"),
			},
			wantErr: false,
		},
		{
			name: "no duplicate found",
			post: &domain.FederatedPost{
				ID:       "unique-id",
				ActorDID: "did:plc:unique",
				URI:      "at://did:plc:unique/app.bsky.feed.post/1",
				Text:     strPtr("Unique content"),
			},
			setupMocks: func(repo *MockFederationRepositoryExt, hardening *MockHardeningRepository, hash string) {
				repo.On("GetPostByContentHash", ctx, hash).Return(nil, nil)
			},
			wantDuplicate: false,
			wantExisting:  nil,
			wantErr:       false,
		},
		{
			name: "same post ID not considered duplicate",
			post: &domain.FederatedPost{
				ID:       "same-id",
				ActorDID: "did:plc:test",
				URI:      "at://did:plc:test/app.bsky.feed.post/1",
				Text:     strPtr("Content"),
			},
			setupMocks: func(repo *MockFederationRepositoryExt, hardening *MockHardeningRepository, hash string) {
				repo.On("GetPostByContentHash", ctx, hash).Return(&domain.FederatedPost{
					ID:       "same-id",
					ActorDID: "did:plc:test",
					URI:      "at://did:plc:test/app.bsky.feed.post/1",
					Text:     strPtr("Content"),
				}, nil)
			},
			wantDuplicate: false,
			wantExisting:  nil,
			wantErr:       false,
		},
		{
			name: "repository error",
			post: &domain.FederatedPost{
				ID:       "error-id",
				ActorDID: "did:plc:error",
				URI:      "at://did:plc:error/app.bsky.feed.post/1",
			},
			setupMocks: func(repo *MockFederationRepositoryExt, hardening *MockHardeningRepository, hash string) {
				repo.On("GetPostByContentHash", ctx, hash).Return(nil, fmt.Errorf("database error"))
			},
			wantDuplicate: false,
			wantExisting:  nil,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockFederationRepositoryExt)
			mockHardening := new(MockHardeningRepository)
			service := NewDeduplicationService(mockRepo, mockHardening)

			hash := service.CalculateContentHash(tt.post)
			tt.setupMocks(mockRepo, mockHardening, hash)

			existing, isDuplicate, err := service.DetectDuplicate(ctx, tt.post)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantDuplicate, isDuplicate)
				assert.Equal(t, tt.wantExisting, existing)
			}

			mockRepo.AssertExpectations(t)
			mockHardening.AssertExpectations(t)
		})
	}
}

func TestDeduplicationService_ResolveStrategies(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	earlier := now.Add(-time.Hour)
	later := now.Add(time.Hour)

	tests := []struct {
		name       string
		strategy   DeduplicationStrategy
		original   *domain.FederatedPost
		duplicate  *domain.FederatedPost
		setupMocks func(*MockFederationRepositoryExt, *MockHardeningRepository)
		wantErr    bool
	}{
		{
			name:     "keep latest - duplicate is newer",
			strategy: StrategyKeepLatest,
			original: &domain.FederatedPost{
				ID:        "old-id",
				CreatedAt: &earlier,
			},
			duplicate: &domain.FederatedPost{
				ID:        "new-id",
				CreatedAt: &later,
			},
			setupMocks: func(repo *MockFederationRepositoryExt, hardening *MockHardeningRepository) {
				newID := "new-id"
				repo.On("UpdatePostCanonical", ctx, "old-id", false).Return(nil)
				repo.On("UpdatePostCanonical", ctx, "new-id", true).Return(nil)
				repo.On("UpdatePostDuplicateOf", ctx, "old-id", &newID).Return(nil)
				hardening.On("RecordMetric", ctx, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name:     "keep latest - original is newer",
			strategy: StrategyKeepLatest,
			original: &domain.FederatedPost{
				ID:        "newer-id",
				CreatedAt: &later,
			},
			duplicate: &domain.FederatedPost{
				ID:        "older-id",
				CreatedAt: &earlier,
			},
			setupMocks: func(repo *MockFederationRepositoryExt, hardening *MockHardeningRepository) {
				newerID := "newer-id"
				repo.On("UpdatePostDuplicateOf", ctx, "older-id", &newerID).Return(nil)
				repo.On("UpdatePostCanonical", ctx, "older-id", false).Return(nil)
				hardening.On("RecordMetric", ctx, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name:     "keep original",
			strategy: StrategyKeepOriginal,
			original: &domain.FederatedPost{
				ID:        "original-id",
				CreatedAt: &earlier,
			},
			duplicate: &domain.FederatedPost{
				ID:        "duplicate-id",
				CreatedAt: &later,
			},
			setupMocks: func(repo *MockFederationRepositoryExt, hardening *MockHardeningRepository) {
				origID := "original-id"
				repo.On("UpdatePostDuplicateOf", ctx, "duplicate-id", &origID).Return(nil)
				repo.On("UpdatePostCanonical", ctx, "duplicate-id", false).Return(nil)
				hardening.On("RecordMetric", ctx, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name:     "merge strategy",
			strategy: StrategyMerge,
			original: &domain.FederatedPost{
				ID:       "orig-id",
				ActorDID: "did:plc:test",
				URI:      "at://test/1",
				Text:     strPtr("Original text"),
				EmbedURL: nil,
			},
			duplicate: &domain.FederatedPost{
				ID:       "dup-id",
				ActorDID: "did:plc:test",
				URI:      "at://test/2",
				Text:     strPtr("Duplicate text"),
				EmbedURL: strPtr("https://example.com"),
			},
			setupMocks: func(repo *MockFederationRepositoryExt, hardening *MockHardeningRepository) {
				// Expect merged content to be saved
				repo.On("UpsertPost", ctx, mock.MatchedBy(func(p *domain.FederatedPost) bool {
					return p.Text != nil && *p.Text == "Original text\n---\nDuplicate text" &&
						p.EmbedURL != nil && *p.EmbedURL == "https://example.com"
				})).Return(nil)
				origID := "orig-id"
				repo.On("UpdatePostDuplicateOf", ctx, "dup-id", &origID).Return(nil)
				repo.On("UpdatePostCanonical", ctx, "dup-id", false).Return(nil)
				hardening.On("RecordMetric", ctx, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name:     "manual review",
			strategy: StrategyManual,
			original: &domain.FederatedPost{
				ID:       "orig-id",
				ActorDID: "did:plc:test",
				URI:      "at://test/1",
			},
			duplicate: &domain.FederatedPost{
				ID:       "dup-id",
				ActorDID: "did:plc:test",
				URI:      "at://test/2",
			},
			setupMocks: func(repo *MockFederationRepositoryExt, hardening *MockHardeningRepository) {
				hardening.On("CreateAbuseReport", ctx, mock.MatchedBy(func(r *domain.FederationAbuseReport) bool {
					return r.ReportType == "duplicate_content" && r.Status == "pending"
				})).Return(nil)
				hardening.On("RecordMetric", ctx, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockFederationRepositoryExt)
			mockHardening := new(MockHardeningRepository)
			service := NewDeduplicationService(mockRepo, mockHardening)

			tt.setupMocks(mockRepo, mockHardening)

			err := service.ResolveDuplicate(ctx, tt.original, tt.duplicate, tt.strategy)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockRepo.AssertExpectations(t)
			mockHardening.AssertExpectations(t)
		})
	}
}

func TestDeduplicationService_MergePosts(t *testing.T) {
	ctx := context.Background()

	canonicalPost := &domain.FederatedPost{
		ID:       "canonical-id",
		ActorDID: "did:plc:test",
		URI:      "at://test/canonical",
		Text:     strPtr("Canonical text"),
	}

	duplicate1 := &domain.FederatedPost{
		ID:       "dup1-id",
		ActorDID: "did:plc:test",
		URI:      "at://test/dup1",
		Text:     strPtr("Duplicate 1 text"),
		EmbedURL: strPtr("https://example1.com"),
	}

	duplicate2 := &domain.FederatedPost{
		ID:       "dup2-id",
		ActorDID: "did:plc:test",
		URI:      "at://test/dup2",
		Text:     strPtr("Duplicate 2 text"),
		EmbedURL: strPtr("https://example2.com"),
	}

	mockRepo := new(MockFederationRepositoryExt)
	mockHardening := new(MockHardeningRepository)
	service := NewDeduplicationService(mockRepo, mockHardening)

	// Setup mocks
	mockRepo.On("GetPost", ctx, "canonical-id").Return(canonicalPost, nil)
	mockRepo.On("GetPost", ctx, "dup1-id").Return(duplicate1, nil)
	mockRepo.On("GetPost", ctx, "dup2-id").Return(duplicate2, nil)

	// Expect merge operations for each duplicate
	canonicalID := "canonical-id"
	mockRepo.On("UpsertPost", ctx, mock.Anything).Return(nil).Twice()
	mockRepo.On("UpdatePostDuplicateOf", ctx, "dup1-id", &canonicalID).Return(nil)
	mockRepo.On("UpdatePostCanonical", ctx, "dup1-id", false).Return(nil)
	mockRepo.On("UpdatePostDuplicateOf", ctx, "dup2-id", &canonicalID).Return(nil)
	mockRepo.On("UpdatePostCanonical", ctx, "dup2-id", false).Return(nil)
	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Twice()

	err := service.MergePosts(ctx, "canonical-id", []string{"dup1-id", "dup2-id"})
	assert.NoError(t, err)

	mockRepo.AssertExpectations(t)
	mockHardening.AssertExpectations(t)
}

func TestDeduplicationService_GetDuplicates(t *testing.T) {
	ctx := context.Background()

	expectedDuplicates := []domain.FederatedPost{
		{ID: "dup1", DuplicateOf: strPtr("main-id")},
		{ID: "dup2", DuplicateOf: strPtr("main-id")},
	}

	mockRepo := new(MockFederationRepositoryExt)
	mockHardening := new(MockHardeningRepository)
	service := NewDeduplicationService(mockRepo, mockHardening)

	mockRepo.On("GetPostDuplicates", ctx, "main-id").Return(expectedDuplicates, nil)

	duplicates, err := service.GetDuplicates(ctx, "main-id")
	assert.NoError(t, err)
	assert.Equal(t, expectedDuplicates, duplicates)

	mockRepo.AssertExpectations(t)
}
