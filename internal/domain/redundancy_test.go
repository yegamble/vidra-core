package domain

import (
	"testing"
	"time"
)

func TestInstancePeer_Validate(t *testing.T) {
	tests := []struct {
		name    string
		peer    *InstancePeer
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid instance peer with https",
			peer: &InstancePeer{
				InstanceURL:         "https://peertube.example.com",
				MaxRedundancySizeGB: 100,
			},
			wantErr: false,
		},
		{
			name: "valid instance peer with http",
			peer: &InstancePeer{
				InstanceURL:         "http://localhost:3000",
				MaxRedundancySizeGB: 0,
			},
			wantErr: false,
		},
		{
			name: "empty instance URL",
			peer: &InstancePeer{
				InstanceURL: "",
			},
			wantErr: true,
			errMsg:  "invalid instance URL",
		},
		{
			name: "invalid URL scheme",
			peer: &InstancePeer{
				InstanceURL: "ftp://example.com",
			},
			wantErr: true,
			errMsg:  "scheme must be http or https",
		},
		{
			name: "negative max size",
			peer: &InstancePeer{
				InstanceURL:         "https://example.com",
				MaxRedundancySizeGB: -1,
			},
			wantErr: true,
			errMsg:  "max redundancy size cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.peer.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errMsg != "" {
				if err.Error() == "" || !contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestInstancePeer_CalculateHealthScore(t *testing.T) {
	now := time.Now()
	hourAgo := now.Add(-1 * time.Hour)
	weekAgo := now.Add(-7 * 24 * time.Hour)

	tests := []struct {
		name           string
		peer           *InstancePeer
		wantScoreRange [2]float64 // min, max
	}{
		{
			name: "perfect health",
			peer: &InstancePeer{
				IsActive:          true,
				FailedSyncCount:   0,
				LastContactedAt:   &hourAgo,
				LastSyncSuccessAt: &hourAgo,
			},
			wantScoreRange: [2]float64{1.0, 1.1}, // Can go above 1.0 with bonus
		},
		{
			name: "inactive instance",
			peer: &InstancePeer{
				IsActive: false,
			},
			wantScoreRange: [2]float64{0.0, 0.0},
		},
		{
			name: "multiple failed syncs",
			peer: &InstancePeer{
				IsActive:        true,
				FailedSyncCount: 5,
				LastContactedAt: &hourAgo,
			},
			wantScoreRange: [2]float64{0.7, 0.8}, // 1.0 - (5 * 0.05) = 0.75
		},
		{
			name: "not contacted recently",
			peer: &InstancePeer{
				IsActive:        true,
				FailedSyncCount: 0,
				LastContactedAt: &weekAgo,
			},
			wantScoreRange: [2]float64{0.2, 0.4}, // Penalty for old contact (7*24 hours = 168 hours, penalty = 0.1 * 168/24 = 0.7)
		},
		{
			name: "never contacted",
			peer: &InstancePeer{
				IsActive:        true,
				FailedSyncCount: 0,
			},
			wantScoreRange: [2]float64{0.6, 0.8}, // 1.0 - 0.3 = 0.7
		},
		{
			name: "recent sync success",
			peer: &InstancePeer{
				IsActive:          true,
				FailedSyncCount:   0,
				LastContactedAt:   &hourAgo,
				LastSyncSuccessAt: &hourAgo,
			},
			wantScoreRange: [2]float64{1.0, 1.1}, // Bonus for recent success
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := tt.peer.CalculateHealthScore()

			if score < tt.wantScoreRange[0] || score > tt.wantScoreRange[1] {
				t.Errorf("CalculateHealthScore() = %v, want between %v and %v",
					score, tt.wantScoreRange[0], tt.wantScoreRange[1])
			}
		})
	}
}

func TestInstancePeer_HasCapacity(t *testing.T) {
	tests := []struct {
		name         string
		peer         *InstancePeer
		videoSizeGB  int64
		wantCapacity bool
	}{
		{
			name: "unlimited capacity",
			peer: &InstancePeer{
				AcceptsNewRedundancy: true,
				MaxRedundancySizeGB:  0,
				TotalStorageBytes:    100 * 1024 * 1024 * 1024,
			},
			videoSizeGB:  10 * 1024 * 1024 * 1024,
			wantCapacity: true,
		},
		{
			name: "has capacity",
			peer: &InstancePeer{
				AcceptsNewRedundancy: true,
				MaxRedundancySizeGB:  100,
				TotalStorageBytes:    50 * 1024 * 1024 * 1024,
			},
			videoSizeGB:  10 * 1024 * 1024 * 1024,
			wantCapacity: true,
		},
		{
			name: "no capacity",
			peer: &InstancePeer{
				AcceptsNewRedundancy: true,
				MaxRedundancySizeGB:  100,
				TotalStorageBytes:    95 * 1024 * 1024 * 1024,
			},
			videoSizeGB:  10 * 1024 * 1024 * 1024,
			wantCapacity: false,
		},
		{
			name: "does not accept redundancy",
			peer: &InstancePeer{
				AcceptsNewRedundancy: false,
				MaxRedundancySizeGB:  100,
				TotalStorageBytes:    0,
			},
			videoSizeGB:  1 * 1024 * 1024 * 1024,
			wantCapacity: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.peer.HasCapacity(tt.videoSizeGB); got != tt.wantCapacity {
				t.Errorf("HasCapacity() = %v, want %v", got, tt.wantCapacity)
			}
		})
	}
}

func TestVideoRedundancy_Validate(t *testing.T) {
	tests := []struct {
		name       string
		redundancy *VideoRedundancy
		wantErr    bool
		errMsg     string
	}{
		{
			name: "valid redundancy",
			redundancy: &VideoRedundancy{
				VideoID:          "video-123",
				TargetInstanceID: "instance-456",
				Status:           RedundancyStatusPending,
				Strategy:         RedundancyStrategyManual,
				FileSizeBytes:    1000,
				BytesTransferred: 0,
				SyncAttemptCount: 0,
				MaxSyncAttempts:  5,
				Priority:         0,
			},
			wantErr: false,
		},
		{
			name: "empty video ID",
			redundancy: &VideoRedundancy{
				TargetInstanceID: "instance-456",
				Status:           RedundancyStatusPending,
				Strategy:         RedundancyStrategyManual,
				MaxSyncAttempts:  5,
			},
			wantErr: true,
			errMsg:  "video ID is required",
		},
		{
			name: "negative file size",
			redundancy: &VideoRedundancy{
				VideoID:          "video-123",
				TargetInstanceID: "instance-456",
				Status:           RedundancyStatusPending,
				Strategy:         RedundancyStrategyManual,
				FileSizeBytes:    -100,
				MaxSyncAttempts:  5,
			},
			wantErr: true,
			errMsg:  "file size cannot be negative",
		},
		{
			name: "invalid bytes transferred",
			redundancy: &VideoRedundancy{
				VideoID:          "video-123",
				TargetInstanceID: "instance-456",
				Status:           RedundancyStatusPending,
				Strategy:         RedundancyStrategyManual,
				FileSizeBytes:    1000,
				BytesTransferred: 2000,
				MaxSyncAttempts:  5,
			},
			wantErr: true,
			errMsg:  "bytes transferred must be between 0 and file size",
		},
		{
			name: "max attempts exceeded",
			redundancy: &VideoRedundancy{
				VideoID:          "video-123",
				TargetInstanceID: "instance-456",
				Status:           RedundancyStatusPending,
				Strategy:         RedundancyStrategyManual,
				FileSizeBytes:    1000,
				SyncAttemptCount: 6,
				MaxSyncAttempts:  5,
			},
			wantErr: true,
			errMsg:  "maximum sync attempts",
		},
		{
			name: "invalid status",
			redundancy: &VideoRedundancy{
				VideoID:          "video-123",
				TargetInstanceID: "instance-456",
				Status:           RedundancyStatus("invalid"),
				Strategy:         RedundancyStrategyManual,
				FileSizeBytes:    1000,
				MaxSyncAttempts:  5,
			},
			wantErr: true,
			errMsg:  "invalid redundancy status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.redundancy.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errMsg != "" {
				if err.Error() == "" || !contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestVideoRedundancy_CalculateProgress(t *testing.T) {
	tests := []struct {
		name         string
		redundancy   *VideoRedundancy
		wantProgress float64
	}{
		{
			name: "no progress",
			redundancy: &VideoRedundancy{
				FileSizeBytes:    1000,
				BytesTransferred: 0,
			},
			wantProgress: 0.0,
		},
		{
			name: "half progress",
			redundancy: &VideoRedundancy{
				FileSizeBytes:    1000,
				BytesTransferred: 500,
			},
			wantProgress: 50.0,
		},
		{
			name: "complete",
			redundancy: &VideoRedundancy{
				FileSizeBytes:    1000,
				BytesTransferred: 1000,
			},
			wantProgress: 100.0,
		},
		{
			name: "zero file size",
			redundancy: &VideoRedundancy{
				FileSizeBytes:    0,
				BytesTransferred: 0,
			},
			wantProgress: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.redundancy.CalculateProgress(); got != tt.wantProgress {
				t.Errorf("CalculateProgress() = %v, want %v", got, tt.wantProgress)
			}
		})
	}
}

func TestVideoRedundancy_CanRetry(t *testing.T) {
	tests := []struct {
		name       string
		redundancy *VideoRedundancy
		canRetry   bool
	}{
		{
			name: "can retry - failed with attempts left",
			redundancy: &VideoRedundancy{
				Status:           RedundancyStatusFailed,
				SyncAttemptCount: 2,
				MaxSyncAttempts:  5,
			},
			canRetry: true,
		},
		{
			name: "cannot retry - max attempts reached",
			redundancy: &VideoRedundancy{
				Status:           RedundancyStatusFailed,
				SyncAttemptCount: 5,
				MaxSyncAttempts:  5,
			},
			canRetry: false,
		},
		{
			name: "cannot retry - not failed",
			redundancy: &VideoRedundancy{
				Status:           RedundancyStatusPending,
				SyncAttemptCount: 0,
				MaxSyncAttempts:  5,
			},
			canRetry: false,
		},
		{
			name: "cannot retry - synced",
			redundancy: &VideoRedundancy{
				Status:           RedundancyStatusSynced,
				SyncAttemptCount: 1,
				MaxSyncAttempts:  5,
			},
			canRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.redundancy.CanRetry(); got != tt.canRetry {
				t.Errorf("CanRetry() = %v, want %v", got, tt.canRetry)
			}
		})
	}
}

func TestVideoRedundancy_ShouldResync(t *testing.T) {
	now := time.Now()
	recent := now.Add(-3 * 24 * time.Hour) // 3 days ago
	old := now.Add(-10 * 24 * time.Hour)   // 10 days ago

	tests := []struct {
		name         string
		redundancy   *VideoRedundancy
		shouldResync bool
	}{
		{
			name: "should resync - old verification",
			redundancy: &VideoRedundancy{
				Status:             RedundancyStatusSynced,
				AutoResync:         true,
				ChecksumVerifiedAt: &old,
			},
			shouldResync: true,
		},
		{
			name: "should not resync - recent verification",
			redundancy: &VideoRedundancy{
				Status:             RedundancyStatusSynced,
				AutoResync:         true,
				ChecksumVerifiedAt: &recent,
			},
			shouldResync: false,
		},
		{
			name: "should resync - never verified",
			redundancy: &VideoRedundancy{
				Status:             RedundancyStatusSynced,
				AutoResync:         true,
				ChecksumVerifiedAt: nil,
			},
			shouldResync: true,
		},
		{
			name: "should not resync - auto resync disabled",
			redundancy: &VideoRedundancy{
				Status:             RedundancyStatusSynced,
				AutoResync:         false,
				ChecksumVerifiedAt: &old,
			},
			shouldResync: false,
		},
		{
			name: "should not resync - not synced",
			redundancy: &VideoRedundancy{
				Status:             RedundancyStatusPending,
				AutoResync:         true,
				ChecksumVerifiedAt: nil,
			},
			shouldResync: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.redundancy.ShouldResync(); got != tt.shouldResync {
				t.Errorf("ShouldResync() = %v, want %v", got, tt.shouldResync)
			}
		})
	}
}

func TestVideoRedundancy_StateTransitions(t *testing.T) {
	t.Run("MarkSyncing", func(t *testing.T) {
		r := &VideoRedundancy{
			Status:           RedundancyStatusPending,
			SyncAttemptCount: 0,
		}

		r.MarkSyncing()

		if r.Status != RedundancyStatusSyncing {
			t.Errorf("Status = %v, want %v", r.Status, RedundancyStatusSyncing)
		}

		if r.SyncStartedAt == nil {
			t.Error("SyncStartedAt should not be nil")
		}

		if r.SyncAttemptCount != 1 {
			t.Errorf("SyncAttemptCount = %v, want 1", r.SyncAttemptCount)
		}
	})

	t.Run("MarkSynced", func(t *testing.T) {
		r := &VideoRedundancy{
			Status:           RedundancyStatusSyncing,
			FileSizeBytes:    1000,
			BytesTransferred: 500,
		}

		checksum := "abc123"
		r.MarkSynced(checksum)

		if r.Status != RedundancyStatusSynced {
			t.Errorf("Status = %v, want %v", r.Status, RedundancyStatusSynced)
		}

		if r.ChecksumSHA256 != checksum {
			t.Errorf("ChecksumSHA256 = %v, want %v", r.ChecksumSHA256, checksum)
		}

		if r.BytesTransferred != r.FileSizeBytes {
			t.Errorf("BytesTransferred = %v, want %v", r.BytesTransferred, r.FileSizeBytes)
		}

		if r.SyncError != "" {
			t.Error("SyncError should be empty")
		}
	})

	t.Run("MarkFailed", func(t *testing.T) {
		r := &VideoRedundancy{
			Status:           RedundancyStatusSyncing,
			SyncAttemptCount: 2,
			MaxSyncAttempts:  5,
		}

		errMsg := "network error"
		r.MarkFailed(errMsg)

		if r.Status != RedundancyStatusFailed {
			t.Errorf("Status = %v, want %v", r.Status, RedundancyStatusFailed)
		}

		if r.SyncError != errMsg {
			t.Errorf("SyncError = %v, want %v", r.SyncError, errMsg)
		}

		if r.NextSyncAt == nil {
			t.Error("NextSyncAt should be set for retry")
		}
	})

	t.Run("MarkCancelled", func(t *testing.T) {
		r := &VideoRedundancy{
			Status: RedundancyStatusPending,
		}

		r.MarkCancelled()

		if r.Status != RedundancyStatusCancelled {
			t.Errorf("Status = %v, want %v", r.Status, RedundancyStatusCancelled)
		}
	})
}

func TestRedundancyPolicy_Validate(t *testing.T) {
	maxAge := 30
	tests := []struct {
		name    string
		policy  *RedundancyPolicy
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid policy",
			policy: &RedundancyPolicy{
				Name:                    "test-policy",
				Strategy:                RedundancyStrategyRecent,
				TargetInstanceCount:     2,
				MinInstanceCount:        1,
				EvaluationIntervalHours: 24,
			},
			wantErr: false,
		},
		{
			name: "empty name",
			policy: &RedundancyPolicy{
				Strategy:                RedundancyStrategyRecent,
				TargetInstanceCount:     2,
				MinInstanceCount:        1,
				EvaluationIntervalHours: 24,
			},
			wantErr: true,
			errMsg:  "policy name is required",
		},
		{
			name: "target less than min",
			policy: &RedundancyPolicy{
				Name:                    "test-policy",
				Strategy:                RedundancyStrategyRecent,
				TargetInstanceCount:     1,
				MinInstanceCount:        2,
				EvaluationIntervalHours: 24,
			},
			wantErr: true,
			errMsg:  "target instance count must be >= minimum instance count",
		},
		{
			name: "invalid max age",
			policy: &RedundancyPolicy{
				Name:                    "test-policy",
				Strategy:                RedundancyStrategyRecent,
				TargetInstanceCount:     2,
				MinInstanceCount:        1,
				MinAgeDays:              10,
				MaxAgeDays:              &maxAge,
				EvaluationIntervalHours: 24,
			},
			wantErr: false, // 30 > 10, so valid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errMsg != "" {
				if err.Error() == "" || !contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestRedundancyPolicy_ShouldEvaluate(t *testing.T) {
	now := time.Now()
	future := now.Add(1 * time.Hour)
	past := now.Add(-1 * time.Hour)

	tests := []struct {
		name           string
		policy         *RedundancyPolicy
		shouldEvaluate bool
	}{
		{
			name: "should evaluate - past evaluation time",
			policy: &RedundancyPolicy{
				Enabled:          true,
				NextEvaluationAt: &past,
			},
			shouldEvaluate: true,
		},
		{
			name: "should not evaluate - future evaluation time",
			policy: &RedundancyPolicy{
				Enabled:          true,
				NextEvaluationAt: &future,
			},
			shouldEvaluate: false,
		},
		{
			name: "should evaluate - never evaluated",
			policy: &RedundancyPolicy{
				Enabled:          true,
				NextEvaluationAt: nil,
			},
			shouldEvaluate: true,
		},
		{
			name: "should not evaluate - disabled",
			policy: &RedundancyPolicy{
				Enabled:          false,
				NextEvaluationAt: &past,
			},
			shouldEvaluate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.ShouldEvaluate(); got != tt.shouldEvaluate {
				t.Errorf("ShouldEvaluate() = %v, want %v", got, tt.shouldEvaluate)
			}
		})
	}
}

func TestRedundancyPolicy_CalculateNextEvaluation(t *testing.T) {
	policy := &RedundancyPolicy{
		EvaluationIntervalHours: 12,
	}

	nextEval := policy.CalculateNextEvaluation()
	expectedTime := time.Now().Add(12 * time.Hour)

	// Allow 1 second difference for test execution time
	diff := nextEval.Sub(expectedTime)
	if diff < -1*time.Second || diff > 1*time.Second {
		t.Errorf("CalculateNextEvaluation() = %v, want approximately %v", nextEval, expectedTime)
	}
}

func TestValidateRedundancyStatus(t *testing.T) {
	tests := []struct {
		status  string
		wantErr bool
	}{
		{"pending", false},
		{"syncing", false},
		{"synced", false},
		{"failed", false},
		{"cancelled", false},
		{"invalid", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			err := ValidateRedundancyStatus(tt.status)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRedundancyStatus(%v) error = %v, wantErr %v", tt.status, err, tt.wantErr)
			}
		})
	}
}

func TestValidateRedundancyStrategy(t *testing.T) {
	tests := []struct {
		strategy string
		wantErr  bool
	}{
		{"recent", false},
		{"most_viewed", false},
		{"trending", false},
		{"manual", false},
		{"all", false},
		{"invalid", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.strategy, func(t *testing.T) {
			err := ValidateRedundancyStrategy(tt.strategy)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRedundancyStrategy(%v) error = %v, wantErr %v", tt.strategy, err, tt.wantErr)
			}
		})
	}
}

// Helper function for substring checking
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
