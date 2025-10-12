package domain

import (
	"encoding/json"
	"testing"
	"time"
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
