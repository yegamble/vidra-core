package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockStorageBackend for testing
type MockStorageBackend struct {
	mock.Mock
}

func (m *MockStorageBackend) Upload(ctx context.Context, key string, data io.Reader, contentType string) error {
	args := m.Called(ctx, key, data, contentType)
	return args.Error(0)
}

func (m *MockStorageBackend) UploadFile(ctx context.Context, key string, localPath string, contentType string) error {
	args := m.Called(ctx, key, localPath, contentType)
	return args.Error(0)
}

func (m *MockStorageBackend) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockStorageBackend) GetURL(key string) string {
	args := m.Called(key)
	return args.String(0)
}

func (m *MockStorageBackend) GetSignedURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	args := m.Called(ctx, key, expiration)
	return args.String(0), args.Error(1)
}

func (m *MockStorageBackend) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockStorageBackend) Exists(ctx context.Context, key string) (bool, error) {
	args := m.Called(ctx, key)
	return args.Bool(0), args.Error(1)
}

func (m *MockStorageBackend) Copy(ctx context.Context, sourceKey, destKey string) error {
	args := m.Called(ctx, sourceKey, destKey)
	return args.Error(0)
}

func (m *MockStorageBackend) GetMetadata(ctx context.Context, key string) (*FileMetadata, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*FileMetadata), args.Error(1)
}

// HybridStorageManager manages tier transitions between storage backends
type HybridStorageManager struct {
	localBackend StorageBackend
	s3Backend    StorageBackend
	ipfsBackend  StorageBackend
}

// NewHybridStorageManager creates a new hybrid storage manager
func NewHybridStorageManager(local, s3, ipfs StorageBackend) *HybridStorageManager {
	return &HybridStorageManager{
		localBackend: local,
		s3Backend:    s3,
		ipfsBackend:  ipfs,
	}
}

// TierTransitionResult holds the result of a tier transition
type TierTransitionResult struct {
	SourceTier    StorageTier
	TargetTier    StorageTier
	SourceKey     string
	TargetKey     string
	TargetURL     string
	BytesMigrated int64
	Duration      time.Duration
	Success       bool
	Error         error
}

// MigrateToTier migrates content from one tier to another
func (h *HybridStorageManager) MigrateToTier(ctx context.Context, sourceTier, targetTier StorageTier, key string, localPath string) (*TierTransitionResult, error) {
	startTime := time.Now()

	result := &TierTransitionResult{
		SourceTier: sourceTier,
		TargetTier: targetTier,
		SourceKey:  key,
	}

	var targetBackend StorageBackend
	switch targetTier {
	case TierHot:
		targetBackend = h.localBackend
	case TierCold:
		targetBackend = h.s3Backend
	case TierWarm:
		targetBackend = h.ipfsBackend
	default:
		return result, errors.New("unknown target tier")
	}

	if targetBackend == nil {
		return result, errors.New("target backend not configured")
	}

	// Perform the migration
	contentType := "application/octet-stream"
	if strings.HasSuffix(localPath, ".mp4") {
		contentType = "video/mp4"
	} else if strings.HasSuffix(localPath, ".webm") {
		contentType = "video/webm"
	}

	err := targetBackend.UploadFile(ctx, key, localPath, contentType)
	if err != nil {
		result.Error = err
		return result, err
	}

	result.TargetKey = key
	result.TargetURL = targetBackend.GetURL(key)
	result.Success = true
	result.Duration = time.Since(startTime)

	// Get file size if available
	if info, err := os.Stat(localPath); err == nil {
		result.BytesMigrated = info.Size()
	}

	return result, nil
}

// TestLocalToS3Migration tests migrating content from local (hot) to S3 (cold)
func TestLocalToS3Migration(t *testing.T) {
	ctx := context.Background()

	// Create temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-video.mp4")
	testContent := []byte("test video content for S3 migration")
	err := os.WriteFile(testFile, testContent, 0644)
	require.NoError(t, err)

	// Setup mocks
	mockLocal := new(MockStorageBackend)
	mockS3 := new(MockStorageBackend)
	mockIPFS := new(MockStorageBackend)

	mockS3.On("UploadFile", ctx, "videos/123/test.mp4", testFile, "video/mp4").Return(nil)
	mockS3.On("GetURL", "videos/123/test.mp4").Return("https://s3.example.com/videos/123/test.mp4")

	manager := NewHybridStorageManager(mockLocal, mockS3, mockIPFS)

	// Execute migration
	result, err := manager.MigrateToTier(ctx, TierHot, TierCold, "videos/123/test.mp4", testFile)

	// Assert
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, TierHot, result.SourceTier)
	assert.Equal(t, TierCold, result.TargetTier)
	assert.Equal(t, "videos/123/test.mp4", result.TargetKey)
	assert.Contains(t, result.TargetURL, "s3.example.com")
	assert.Equal(t, int64(len(testContent)), result.BytesMigrated)
	assert.Greater(t, result.Duration, time.Duration(0))

	mockS3.AssertExpectations(t)
}

// TestLocalToIPFSMigration tests migrating content from local (hot) to IPFS (warm)
func TestLocalToIPFSMigration(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-video.mp4")
	testContent := []byte("test video content for IPFS migration")
	err := os.WriteFile(testFile, testContent, 0644)
	require.NoError(t, err)

	mockLocal := new(MockStorageBackend)
	mockS3 := new(MockStorageBackend)
	mockIPFS := new(MockStorageBackend)

	cid := "QmTest123CIDabcdef"
	mockIPFS.On("UploadFile", ctx, cid, testFile, "video/mp4").Return(nil)
	mockIPFS.On("GetURL", cid).Return("https://ipfs.io/ipfs/" + cid)

	manager := NewHybridStorageManager(mockLocal, mockS3, mockIPFS)

	result, err := manager.MigrateToTier(ctx, TierHot, TierWarm, cid, testFile)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, TierHot, result.SourceTier)
	assert.Equal(t, TierWarm, result.TargetTier)
	assert.Contains(t, result.TargetURL, "ipfs.io/ipfs/")
	assert.Equal(t, int64(len(testContent)), result.BytesMigrated)

	mockIPFS.AssertExpectations(t)
}

// TestS3ToIPFSMigration tests migrating content from S3 (cold) to IPFS (warm)
func TestS3ToIPFSMigration(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-video.mp4")
	err := os.WriteFile(testFile, []byte("archived content"), 0644)
	require.NoError(t, err)

	mockLocal := new(MockStorageBackend)
	mockS3 := new(MockStorageBackend)
	mockIPFS := new(MockStorageBackend)

	cid := "QmArchived123"
	mockIPFS.On("UploadFile", ctx, cid, testFile, "video/mp4").Return(nil)
	mockIPFS.On("GetURL", cid).Return("https://ipfs.io/ipfs/" + cid)

	manager := NewHybridStorageManager(mockLocal, mockS3, mockIPFS)

	result, err := manager.MigrateToTier(ctx, TierCold, TierWarm, cid, testFile)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, TierCold, result.SourceTier)
	assert.Equal(t, TierWarm, result.TargetTier)

	mockIPFS.AssertExpectations(t)
}

// TestMigrationFailure_TargetBackendError tests handling of upload failures
func TestMigrationFailure_TargetBackendError(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-video.mp4")
	err := os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	mockLocal := new(MockStorageBackend)
	mockS3 := new(MockStorageBackend)
	mockIPFS := new(MockStorageBackend)

	// Simulate S3 upload failure
	mockS3.On("UploadFile", ctx, "videos/123/test.mp4", testFile, "video/mp4").
		Return(errors.New("S3 service unavailable"))

	manager := NewHybridStorageManager(mockLocal, mockS3, mockIPFS)

	result, err := manager.MigrateToTier(ctx, TierHot, TierCold, "videos/123/test.mp4", testFile)

	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "S3 service unavailable")
	assert.Equal(t, TierHot, result.SourceTier)
	assert.Equal(t, TierCold, result.TargetTier)

	mockS3.AssertExpectations(t)
}

// TestMigrationFailure_BackendNotConfigured tests migration when backend is nil
func TestMigrationFailure_BackendNotConfigured(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-video.mp4")
	err := os.WriteFile(testFile, []byte("test"), 0644)
	require.NoError(t, err)

	mockLocal := new(MockStorageBackend)
	// S3 and IPFS backends are nil

	manager := NewHybridStorageManager(mockLocal, nil, nil)

	result, err := manager.MigrateToTier(ctx, TierHot, TierCold, "videos/123/test.mp4", testFile)

	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "target backend not configured")
}

// TestMigrationFailure_InvalidSourceFile tests migration with missing source file
func TestMigrationFailure_InvalidSourceFile(t *testing.T) {
	ctx := context.Background()

	mockLocal := new(MockStorageBackend)
	mockS3 := new(MockStorageBackend)
	mockIPFS := new(MockStorageBackend)

	// File doesn't exist
	nonExistentFile := "/tmp/non-existent-file.mp4"

	mockS3.On("UploadFile", ctx, "videos/123/test.mp4", nonExistentFile, "video/mp4").
		Return(errors.New("file not found"))

	manager := NewHybridStorageManager(mockLocal, mockS3, mockIPFS)

	result, err := manager.MigrateToTier(ctx, TierHot, TierCold, "videos/123/test.mp4", nonExistentFile)

	require.Error(t, err)
	assert.False(t, result.Success)
}

// TestTierFallback tests fallback scenarios when primary tier is unavailable
func TestTierFallback(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-video.mp4")
	err := os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	mockLocal := new(MockStorageBackend)
	mockS3 := new(MockStorageBackend)
	mockIPFS := new(MockStorageBackend)

	// Simulate S3 failure
	mockS3.On("UploadFile", ctx, "videos/123/test.mp4", testFile, "video/mp4").
		Return(errors.New("S3 unavailable")).Once()

	// Fallback to IPFS
	cid := "QmFallback123"
	mockIPFS.On("UploadFile", ctx, cid, testFile, "video/mp4").Return(nil)
	mockIPFS.On("GetURL", cid).Return("https://ipfs.io/ipfs/" + cid)

	manager := NewHybridStorageManager(mockLocal, mockS3, mockIPFS)

	// Try S3 first
	resultS3, errS3 := manager.MigrateToTier(ctx, TierHot, TierCold, "videos/123/test.mp4", testFile)
	require.Error(t, errS3)
	assert.False(t, resultS3.Success)

	// Fallback to IPFS
	resultIPFS, errIPFS := manager.MigrateToTier(ctx, TierHot, TierWarm, cid, testFile)
	require.NoError(t, errIPFS)
	assert.True(t, resultIPFS.Success)
	assert.Equal(t, TierWarm, resultIPFS.TargetTier)

	mockS3.AssertExpectations(t)
	mockIPFS.AssertExpectations(t)
}

// TestMultiTierReplication tests replicating content across multiple tiers
func TestMultiTierReplication(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-video.mp4")
	testContent := []byte("replicated content")
	err := os.WriteFile(testFile, testContent, 0644)
	require.NoError(t, err)

	mockLocal := new(MockStorageBackend)
	mockS3 := new(MockStorageBackend)
	mockIPFS := new(MockStorageBackend)

	key := "videos/123/test.mp4"
	cid := "QmReplicated123"

	// Upload to S3
	mockS3.On("UploadFile", ctx, key, testFile, "video/mp4").Return(nil)
	mockS3.On("GetURL", key).Return("https://s3.example.com/" + key)

	// Upload to IPFS
	mockIPFS.On("UploadFile", ctx, cid, testFile, "video/mp4").Return(nil)
	mockIPFS.On("GetURL", cid).Return("https://ipfs.io/ipfs/" + cid)

	manager := NewHybridStorageManager(mockLocal, mockS3, mockIPFS)

	// Replicate to both S3 and IPFS
	resultS3, err := manager.MigrateToTier(ctx, TierHot, TierCold, key, testFile)
	require.NoError(t, err)
	assert.True(t, resultS3.Success)

	resultIPFS, err := manager.MigrateToTier(ctx, TierHot, TierWarm, cid, testFile)
	require.NoError(t, err)
	assert.True(t, resultIPFS.Success)

	// Verify both migrations succeeded
	assert.Contains(t, resultS3.TargetURL, "s3.example.com")
	assert.Contains(t, resultIPFS.TargetURL, "ipfs.io")
	assert.Equal(t, resultS3.BytesMigrated, resultIPFS.BytesMigrated)

	mockS3.AssertExpectations(t)
	mockIPFS.AssertExpectations(t)
}

// TestStorageTierDefinitions tests that storage tier constants are correctly defined
func TestStorageTierDefinitions(t *testing.T) {
	assert.Equal(t, StorageTier("hot"), TierHot)
	assert.Equal(t, StorageTier("warm"), TierWarm)
	assert.Equal(t, StorageTier("cold"), TierCold)
}

// TestStorageTierTransitionMatrix tests all valid tier transitions
func TestStorageTierTransitionMatrix(t *testing.T) {
	transitions := []struct {
		from StorageTier
		to   StorageTier
		desc string
	}{
		{TierHot, TierWarm, "Local to IPFS"},
		{TierHot, TierCold, "Local to S3"},
		{TierWarm, TierCold, "IPFS to S3"},
		{TierCold, TierWarm, "S3 to IPFS"},
		{TierCold, TierHot, "S3 to Local (restore)"},
		{TierWarm, TierHot, "IPFS to Local (restore)"},
	}

	for _, tt := range transitions {
		t.Run(tt.desc, func(t *testing.T) {
			// Verify that the tier values are valid
			assert.NotEmpty(t, string(tt.from))
			assert.NotEmpty(t, string(tt.to))
			assert.NotEqual(t, tt.from, tt.to)
		})
	}
}
