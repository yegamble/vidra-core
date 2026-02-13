package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		name     string
		status   ImportStatus
		expected bool
	}{
		{"Pending is not terminal", ImportStatusPending, false},
		{"Downloading is not terminal", ImportStatusDownloading, false},
		{"Processing is not terminal", ImportStatusProcessing, false},
		{"Completed is terminal", ImportStatusCompleted, true},
		{"Failed is terminal", ImportStatusFailed, true},
		{"Cancelled is terminal", ImportStatusCancelled, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.expected {
				t.Errorf("IsTerminal() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestVideoImport_Validate(t *testing.T) {
	tests := []struct {
		name    string
		imp     *VideoImport
		wantErr bool
	}{
		{
			name: "Valid import",
			imp: &VideoImport{
				UserID:        "user-123",
				SourceURL:     "https://youtube.com/watch?v=test",
				Progress:      50,
				TargetPrivacy: string(PrivacyPublic),
			},
			wantErr: false,
		},
		{
			name: "Missing user ID",
			imp: &VideoImport{
				SourceURL: "https://youtube.com/watch?v=test",
			},
			wantErr: true,
		},
		{
			name: "Missing source URL",
			imp: &VideoImport{
				UserID: "user-123",
			},
			wantErr: true,
		},
		{
			name: "Invalid source URL",
			imp: &VideoImport{
				UserID:    "user-123",
				SourceURL: "not-a-url",
			},
			wantErr: true,
		},
		{
			name: "Progress out of range",
			imp: &VideoImport{
				UserID:    "user-123",
				SourceURL: "https://youtube.com/watch?v=test",
				Progress:  150,
			},
			wantErr: true,
		},
		{
			name: "Invalid privacy",
			imp: &VideoImport{
				UserID:        "user-123",
				SourceURL:     "https://youtube.com/watch?v=test",
				TargetPrivacy: "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.imp.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVideoImport_CanTransition(t *testing.T) {
	tests := []struct {
		name       string
		fromStatus ImportStatus
		toStatus   ImportStatus
		expected   bool
	}{
		// Valid transitions
		{"Pending to Downloading", ImportStatusPending, ImportStatusDownloading, true},
		{"Pending to Cancelled", ImportStatusPending, ImportStatusCancelled, true},
		{"Pending to Failed", ImportStatusPending, ImportStatusFailed, true},
		{"Downloading to Processing", ImportStatusDownloading, ImportStatusProcessing, true},
		{"Downloading to Failed", ImportStatusDownloading, ImportStatusFailed, true},
		{"Downloading to Cancelled", ImportStatusDownloading, ImportStatusCancelled, true},
		{"Processing to Completed", ImportStatusProcessing, ImportStatusCompleted, true},
		{"Processing to Failed", ImportStatusProcessing, ImportStatusFailed, true},

		// Invalid transitions
		{"Pending to Processing", ImportStatusPending, ImportStatusProcessing, false},
		{"Pending to Completed", ImportStatusPending, ImportStatusCompleted, false},
		{"Downloading to Completed", ImportStatusDownloading, ImportStatusCompleted, false},
		{"Processing to Cancelled", ImportStatusProcessing, ImportStatusCancelled, false},

		// From terminal states
		{"Completed to Downloading", ImportStatusCompleted, ImportStatusDownloading, false},
		{"Failed to Processing", ImportStatusFailed, ImportStatusProcessing, false},
		{"Cancelled to Downloading", ImportStatusCancelled, ImportStatusDownloading, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := &VideoImport{Status: tt.fromStatus}
			if got := imp.CanTransition(tt.toStatus); got != tt.expected {
				t.Errorf("CanTransition() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestVideoImport_Start(t *testing.T) {
	imp := &VideoImport{
		Status: ImportStatusPending,
	}

	err := imp.Start()
	if err != nil {
		t.Errorf("Start() error = %v", err)
	}

	if imp.Status != ImportStatusDownloading {
		t.Errorf("Status = %v, want %v", imp.Status, ImportStatusDownloading)
	}

	if imp.StartedAt == nil {
		t.Error("StartedAt should be set")
	}

	// Test invalid transition
	imp2 := &VideoImport{Status: ImportStatusCompleted}
	err = imp2.Start()
	if err == nil {
		t.Error("Start() should fail from completed status")
	}
}

func TestVideoImport_MarkProcessing(t *testing.T) {
	imp := &VideoImport{
		Status: ImportStatusDownloading,
	}

	err := imp.MarkProcessing()
	if err != nil {
		t.Errorf("MarkProcessing() error = %v", err)
	}

	if imp.Status != ImportStatusProcessing {
		t.Errorf("Status = %v, want %v", imp.Status, ImportStatusProcessing)
	}
}

func TestVideoImport_Complete(t *testing.T) {
	imp := &VideoImport{
		Status: ImportStatusProcessing,
	}

	videoID := "video-123"
	err := imp.Complete(videoID)
	if err != nil {
		t.Errorf("Complete() error = %v", err)
	}

	if imp.Status != ImportStatusCompleted {
		t.Errorf("Status = %v, want %v", imp.Status, ImportStatusCompleted)
	}

	if imp.VideoID == nil || *imp.VideoID != videoID {
		t.Errorf("VideoID = %v, want %v", imp.VideoID, videoID)
	}

	if imp.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}

	if imp.Progress != 100 {
		t.Errorf("Progress = %d, want 100", imp.Progress)
	}
}

func TestVideoImport_Fail(t *testing.T) {
	imp := &VideoImport{
		Status: ImportStatusDownloading,
	}

	errorMsg := "download failed"
	err := imp.Fail(errorMsg)
	if err != nil {
		t.Errorf("Fail() error = %v", err)
	}

	if imp.Status != ImportStatusFailed {
		t.Errorf("Status = %v, want %v", imp.Status, ImportStatusFailed)
	}

	if imp.ErrorMessage == nil || *imp.ErrorMessage != errorMsg {
		t.Errorf("ErrorMessage = %v, want %v", imp.ErrorMessage, errorMsg)
	}
}

func TestVideoImport_Cancel(t *testing.T) {
	imp := &VideoImport{
		Status: ImportStatusDownloading,
	}

	err := imp.Cancel()
	if err != nil {
		t.Errorf("Cancel() error = %v", err)
	}

	if imp.Status != ImportStatusCancelled {
		t.Errorf("Status = %v, want %v", imp.Status, ImportStatusCancelled)
	}

	// Test cannot cancel from terminal state
	imp2 := &VideoImport{Status: ImportStatusCompleted}
	err = imp2.Cancel()
	if err == nil {
		t.Error("Cancel() should fail from completed status")
	}
}

func TestVideoImport_UpdateProgress(t *testing.T) {
	imp := &VideoImport{}

	err := imp.UpdateProgress(50, 1024000)
	if err != nil {
		t.Errorf("UpdateProgress() error = %v", err)
	}

	if imp.Progress != 50 {
		t.Errorf("Progress = %d, want 50", imp.Progress)
	}

	if imp.DownloadedBytes != 1024000 {
		t.Errorf("DownloadedBytes = %d, want 1024000", imp.DownloadedBytes)
	}

	// Test invalid progress
	err = imp.UpdateProgress(150, 0)
	if err == nil {
		t.Error("UpdateProgress() should fail with progress > 100")
	}

	// Test negative bytes
	err = imp.UpdateProgress(50, -1)
	if err == nil {
		t.Error("UpdateProgress() should fail with negative bytes")
	}
}

func TestVideoImport_SetMetadata(t *testing.T) {
	imp := &VideoImport{}
	metadata := &ImportMetadata{
		Title:       "Test Video",
		Description: "Test Description",
		Duration:    300,
		Filesize:    1024000,
	}

	err := imp.SetMetadata(metadata)
	if err != nil {
		t.Errorf("SetMetadata() error = %v", err)
	}

	if len(imp.Metadata) == 0 {
		t.Error("Metadata should be set")
	}

	if imp.FileSizeBytes == nil || *imp.FileSizeBytes != 1024000 {
		t.Errorf("FileSizeBytes = %v, want 1024000", imp.FileSizeBytes)
	}

	// Test nil metadata
	err = imp.SetMetadata(nil)
	if err == nil {
		t.Error("SetMetadata() should fail with nil metadata")
	}
}

func TestVideoImport_GetMetadata(t *testing.T) {
	metadata := &ImportMetadata{
		Title:       "Test Video",
		Description: "Test Description",
		Duration:    300,
	}

	metadataJSON, _ := json.Marshal(metadata)

	imp := &VideoImport{
		Metadata: metadataJSON,
	}

	retrieved, err := imp.GetMetadata()
	if err != nil {
		t.Errorf("GetMetadata() error = %v", err)
	}

	if retrieved.Title != metadata.Title {
		t.Errorf("Title = %v, want %v", retrieved.Title, metadata.Title)
	}

	// Test cached metadata
	retrieved2, err := imp.GetMetadata()
	if err != nil {
		t.Errorf("GetMetadata() error = %v", err)
	}

	if retrieved2 != retrieved {
		t.Error("Metadata should be cached")
	}

	// Test empty metadata
	imp2 := &VideoImport{}
	_, err = imp2.GetMetadata()
	if err == nil {
		t.Error("GetMetadata() should fail with empty metadata")
	}
}

func TestVideoImport_GetSourcePlatform(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"YouTube", "https://youtube.com/watch?v=test", "YouTube"},
		{"YouTube short", "https://youtu.be/test", "YouTube"},
		{"Vimeo", "https://vimeo.com/123456", "Vimeo"},
		{"Dailymotion", "https://dailymotion.com/video/test", "Dailymotion"},
		{"Twitch", "https://twitch.tv/videos/123", "Twitch"},
		{"Twitter", "https://twitter.com/user/status/123", "Twitter"},
		{"X", "https://x.com/user/status/123", "Twitter"},
		{"Unknown", "https://example.com/video", "example.com"},
		{"Invalid", "not-a-url", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := &VideoImport{SourceURL: tt.url}
			if got := imp.GetSourcePlatform(); got != tt.expected {
				t.Errorf("GetSourcePlatform() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"Valid HTTP URL", "http://example.com", false},
		{"Valid HTTPS URL", "https://example.com/video", false},
		{"Empty URL", "", true},
		{"Invalid scheme", "ftp://example.com", true},
		{"No scheme", "example.com", true},
		{"Invalid format", "not a url", true},
		{"No host", "https://", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatePrivacy(t *testing.T) {
	tests := []struct {
		name    string
		privacy string
		wantErr bool
	}{
		{"Public", string(PrivacyPublic), false},
		{"Unlisted", string(PrivacyUnlisted), false},
		{"Private", string(PrivacyPrivate), false},
		{"Invalid", "invalid", true},
		{"Empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePrivacy(tt.privacy)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePrivacy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVideoImport_StateMachine(t *testing.T) {
	// Test complete workflow
	imp := &VideoImport{
		UserID:        "user-123",
		SourceURL:     "https://youtube.com/watch?v=test",
		Status:        ImportStatusPending,
		TargetPrivacy: string(PrivacyPrivate),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Start
	if err := imp.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if imp.Status != ImportStatusDownloading || imp.StartedAt == nil {
		t.Error("Start() did not transition properly")
	}

	// Update progress
	if err := imp.UpdateProgress(50, 500000); err != nil {
		t.Fatalf("UpdateProgress() failed: %v", err)
	}

	// Mark processing
	if err := imp.MarkProcessing(); err != nil {
		t.Fatalf("MarkProcessing() failed: %v", err)
	}
	if imp.Status != ImportStatusProcessing {
		t.Error("MarkProcessing() did not transition properly")
	}

	// Complete
	if err := imp.Complete("video-123"); err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	if imp.Status != ImportStatusCompleted || imp.CompletedAt == nil || imp.Progress != 100 {
		t.Error("Complete() did not transition properly")
	}

	// Ensure terminal state cannot transition
	if imp.CanTransition(ImportStatusDownloading) {
		t.Error("Should not be able to transition from completed status")
	}
}

func TestValidateURLWithSSRFCheck(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantErr    bool
		errContain string
	}{
		{
			name:       "Empty URL fails basic validation",
			url:        "",
			wantErr:    true,
			errContain: "empty",
		},
		{
			name:       "Invalid scheme fails basic validation",
			url:        "ftp://example.com",
			wantErr:    true,
			errContain: "scheme",
		},
		{
			name:       "Localhost resolves to private IP",
			url:        "https://localhost/path",
			wantErr:    true,
			errContain: "private",
		},
		{
			name:       "127.0.0.1 is private",
			url:        "https://127.0.0.1/path",
			wantErr:    true,
			errContain: "private",
		},
		{
			name:       "URL with port and localhost",
			url:        "https://localhost:8080/path",
			wantErr:    true,
			errContain: "private",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURLWithSSRFCheck(tt.url)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContain)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCanTransition_AdditionalEdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		fromStatus ImportStatus
		toStatus   ImportStatus
		expected   bool
	}{
		{
			name:       "Completed to Failed is blocked (terminal)",
			fromStatus: ImportStatusCompleted,
			toStatus:   ImportStatusFailed,
			expected:   false,
		},
		{
			name:       "Completed to Completed is blocked (terminal)",
			fromStatus: ImportStatusCompleted,
			toStatus:   ImportStatusCompleted,
			expected:   false,
		},
		{
			name:       "Unknown status to Downloading returns false",
			fromStatus: ImportStatus("unknown"),
			toStatus:   ImportStatusDownloading,
			expected:   false,
		},
		{
			name:       "Unknown status to Failed returns false",
			fromStatus: ImportStatus("unknown"),
			toStatus:   ImportStatusFailed,
			expected:   false,
		},
		{
			name:       "Downloading to Downloading is invalid",
			fromStatus: ImportStatusDownloading,
			toStatus:   ImportStatusDownloading,
			expected:   false,
		},
		{
			name:       "Processing to Downloading is invalid",
			fromStatus: ImportStatusProcessing,
			toStatus:   ImportStatusDownloading,
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := &VideoImport{Status: tt.fromStatus}
			got := imp.CanTransition(tt.toStatus)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestMarkProcessing_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name       string
		fromStatus ImportStatus
		wantErr    bool
	}{
		{
			name:       "From pending should fail",
			fromStatus: ImportStatusPending,
			wantErr:    true,
		},
		{
			name:       "From completed should fail",
			fromStatus: ImportStatusCompleted,
			wantErr:    true,
		},
		{
			name:       "From processing should fail (already processing)",
			fromStatus: ImportStatusProcessing,
			wantErr:    true,
		},
		{
			name:       "From failed should fail",
			fromStatus: ImportStatusFailed,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := &VideoImport{Status: tt.fromStatus}
			err := imp.MarkProcessing()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "cannot transition")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestComplete_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name       string
		fromStatus ImportStatus
		wantErr    bool
	}{
		{
			name:       "From pending should fail",
			fromStatus: ImportStatusPending,
			wantErr:    true,
		},
		{
			name:       "From downloading should fail",
			fromStatus: ImportStatusDownloading,
			wantErr:    true,
		},
		{
			name:       "From completed should fail (terminal)",
			fromStatus: ImportStatusCompleted,
			wantErr:    true,
		},
		{
			name:       "From failed should fail (terminal)",
			fromStatus: ImportStatusFailed,
			wantErr:    true,
		},
		{
			name:       "From cancelled should fail (terminal)",
			fromStatus: ImportStatusCancelled,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := &VideoImport{Status: tt.fromStatus}
			err := imp.Complete("video-999")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "cannot transition")
		})
	}
}

func TestFail_FromTerminalNonFailedStatus(t *testing.T) {
	tests := []struct {
		name       string
		fromStatus ImportStatus
		wantErr    bool
	}{
		{
			name:       "From completed should fail (terminal, non-failed)",
			fromStatus: ImportStatusCompleted,
			wantErr:    true,
		},
		{
			name:       "From cancelled should fail (terminal, non-failed)",
			fromStatus: ImportStatusCancelled,
			wantErr:    true,
		},
		{
			name:       "From failed should succeed (re-fail is allowed)",
			fromStatus: ImportStatusFailed,
			wantErr:    false,
		},
		{
			name:       "From pending should succeed",
			fromStatus: ImportStatusPending,
			wantErr:    false,
		},
		{
			name:       "From processing should succeed",
			fromStatus: ImportStatusProcessing,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := &VideoImport{Status: tt.fromStatus}
			err := imp.Fail("something went wrong")
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "cannot transition from terminal status")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, ImportStatusFailed, imp.Status)
				require.NotNil(t, imp.ErrorMessage)
				assert.Equal(t, "something went wrong", *imp.ErrorMessage)
			}
		})
	}
}

func TestSetMetadata_FileSizeBranches(t *testing.T) {
	tests := []struct {
		name             string
		metadata         *ImportMetadata
		wantFileSize     *int64
		wantMetadataJSON bool
	}{
		{
			name: "Filesize > 0 sets FileSizeBytes from Filesize",
			metadata: &ImportMetadata{
				Title:    "Video with filesize",
				Filesize: 5000000,
			},
			wantFileSize:     int64Ptr(5000000),
			wantMetadataJSON: true,
		},
		{
			name: "FilesizeApprox > 0 sets FileSizeBytes when Filesize is 0",
			metadata: &ImportMetadata{
				Title:          "Video with approx filesize",
				Filesize:       0,
				FilesizeApprox: 3000000,
			},
			wantFileSize:     int64Ptr(3000000),
			wantMetadataJSON: true,
		},
		{
			name: "Both zero leaves FileSizeBytes nil",
			metadata: &ImportMetadata{
				Title:          "Video with no filesize",
				Filesize:       0,
				FilesizeApprox: 0,
			},
			wantFileSize:     nil,
			wantMetadataJSON: true,
		},
		{
			name: "Filesize takes precedence over FilesizeApprox",
			metadata: &ImportMetadata{
				Title:          "Video with both sizes",
				Filesize:       8000000,
				FilesizeApprox: 7500000,
			},
			wantFileSize:     int64Ptr(8000000),
			wantMetadataJSON: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := &VideoImport{}
			err := imp.SetMetadata(tt.metadata)
			require.NoError(t, err)

			if tt.wantMetadataJSON {
				assert.NotEmpty(t, imp.Metadata)
			}

			assert.Equal(t, tt.metadata, imp.MetadataParsed)

			if tt.wantFileSize == nil {
				assert.Nil(t, imp.FileSizeBytes)
			} else {
				require.NotNil(t, imp.FileSizeBytes)
				assert.Equal(t, *tt.wantFileSize, *imp.FileSizeBytes)
			}
		})
	}
}

func TestGetMetadata_EdgeCases(t *testing.T) {
	t.Run("Returns cached MetadataParsed without unmarshalling", func(t *testing.T) {
		cached := &ImportMetadata{
			Title:    "Cached Video",
			Duration: 120,
		}
		imp := &VideoImport{
			MetadataParsed: cached,
			// Metadata (JSON) is intentionally empty - should still return cached
		}

		result, err := imp.GetMetadata()
		require.NoError(t, err)
		assert.Equal(t, cached, result)
		assert.Equal(t, "Cached Video", result.Title)
	})

	t.Run("Empty Metadata with nil MetadataParsed returns error", func(t *testing.T) {
		imp := &VideoImport{
			Metadata:       nil,
			MetadataParsed: nil,
		}

		result, err := imp.GetMetadata()
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "no metadata available")
	})

	t.Run("Invalid JSON in Metadata returns unmarshal error", func(t *testing.T) {
		imp := &VideoImport{
			Metadata: json.RawMessage(`{invalid json`),
		}

		result, err := imp.GetMetadata()
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to unmarshal metadata")
	})

	t.Run("Valid JSON sets MetadataParsed cache on first call", func(t *testing.T) {
		metadata := &ImportMetadata{
			Title:       "Fresh Video",
			Description: "A test",
			Duration:    60,
		}
		metadataJSON, err := json.Marshal(metadata)
		require.NoError(t, err)

		imp := &VideoImport{
			Metadata: metadataJSON,
		}

		// MetadataParsed is nil before first call
		assert.Nil(t, imp.MetadataParsed)

		result, err := imp.GetMetadata()
		require.NoError(t, err)
		assert.Equal(t, "Fresh Video", result.Title)

		// MetadataParsed should now be cached
		assert.NotNil(t, imp.MetadataParsed)
		assert.Equal(t, result, imp.MetadataParsed)
	})
}

// int64Ptr is a helper to create a pointer to an int64 value.
func int64Ptr(v int64) *int64 {
	return &v
}
