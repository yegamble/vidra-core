package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestUserView_GenerateSessionID(t *testing.T) {
	t.Run("generates UUID when session ID is empty", func(t *testing.T) {
		uv := &UserView{}
		uv.GenerateSessionID()
		assert.NotEmpty(t, uv.SessionID)
		assert.Len(t, uv.SessionID, 36) // UUID v4 length with dashes
	})

	t.Run("does not overwrite existing session ID", func(t *testing.T) {
		uv := &UserView{SessionID: "existing-session-id"}
		uv.GenerateSessionID()
		assert.Equal(t, "existing-session-id", uv.SessionID)
	})

	t.Run("generates unique IDs on successive calls", func(t *testing.T) {
		uv1 := &UserView{}
		uv2 := &UserView{}
		uv1.GenerateSessionID()
		uv2.GenerateSessionID()
		assert.NotEqual(t, uv1.SessionID, uv2.SessionID)
	})
}

func TestUserView_CalculateCompletion(t *testing.T) {
	tests := []struct {
		name                   string
		watchDuration          int
		videoDuration          int
		expectedCompletion     float64
		expectedIsCompleted    bool
		completionApproxEquals bool
	}{
		{
			name:                "zero video duration does nothing",
			watchDuration:       60,
			videoDuration:       0,
			expectedCompletion:  0,
			expectedIsCompleted: false,
		},
		{
			name:                "full watch equals 100 percent",
			watchDuration:       100,
			videoDuration:       100,
			expectedCompletion:  100.0,
			expectedIsCompleted: true,
		},
		{
			name:                "half watched",
			watchDuration:       50,
			videoDuration:       100,
			expectedCompletion:  50.0,
			expectedIsCompleted: false,
		},
		{
			name:                "exactly 95 percent marks completed",
			watchDuration:       95,
			videoDuration:       100,
			expectedCompletion:  95.0,
			expectedIsCompleted: true,
		},
		{
			name:                "94 percent is not completed",
			watchDuration:       94,
			videoDuration:       100,
			expectedCompletion:  94.0,
			expectedIsCompleted: false,
		},
		{
			name:                "capped at 100 when watch exceeds video",
			watchDuration:       200,
			videoDuration:       100,
			expectedCompletion:  100.0,
			expectedIsCompleted: true,
		},
		{
			name:                "zero watch duration",
			watchDuration:       0,
			videoDuration:       100,
			expectedCompletion:  0.0,
			expectedIsCompleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uv := &UserView{
				WatchDuration: tt.watchDuration,
				VideoDuration: tt.videoDuration,
			}
			uv.CalculateCompletion()
			assert.InDelta(t, tt.expectedCompletion, uv.CompletionPercentage, 0.01)
			assert.Equal(t, tt.expectedIsCompleted, uv.IsCompleted)
		})
	}
}

func TestUserView_SetViewDate(t *testing.T) {
	t.Run("sets date hour and weekday from timestamp", func(t *testing.T) {
		// Wednesday, 2025-06-18 at 14:30:00 UTC
		loc := time.UTC
		ts := time.Date(2025, 6, 18, 14, 30, 45, 0, loc)

		uv := &UserView{}
		uv.SetViewDate(ts)

		expectedDate := time.Date(2025, 6, 18, 0, 0, 0, 0, loc)
		assert.Equal(t, expectedDate, uv.ViewDate)
		assert.Equal(t, 14, uv.ViewHour)
		assert.Equal(t, int(time.Wednesday), uv.Weekday)
	})

	t.Run("midnight edge case", func(t *testing.T) {
		ts := time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC) // Sunday
		uv := &UserView{}
		uv.SetViewDate(ts)

		assert.Equal(t, 0, uv.ViewHour)
		assert.Equal(t, int(time.Sunday), uv.Weekday)
	})

	t.Run("end of day edge case", func(t *testing.T) {
		ts := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC) // Wednesday
		uv := &UserView{}
		uv.SetViewDate(ts)

		assert.Equal(t, 23, uv.ViewHour)
		expectedDate := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, expectedDate, uv.ViewDate)
	})
}

func TestUserView_IsValidForTracking(t *testing.T) {
	tests := []struct {
		name            string
		videoID         string
		fingerprintHash string
		want            bool
	}{
		{"valid with both fields", "video-123", "fp-abc", true},
		{"empty video ID", "", "fp-abc", false},
		{"empty fingerprint hash", "video-123", "", false},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uv := &UserView{
				VideoID:         tt.videoID,
				FingerprintHash: tt.fingerprintHash,
			}
			assert.Equal(t, tt.want, uv.IsValidForTracking())
		})
	}
}
