package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveStream_Validate(t *testing.T) {
	tests := []struct {
		name    string
		stream  *LiveStream
		wantErr error
	}{
		{
			name: "Valid stream",
			stream: &LiveStream{
				Title:   "Test Stream",
				Status:  StreamStatusWaiting,
				Privacy: StreamPrivacyPublic,
			},
			wantErr: nil,
		},
		{
			name: "Missing title",
			stream: &LiveStream{
				Title:   "",
				Status:  StreamStatusWaiting,
				Privacy: StreamPrivacyPublic,
			},
			wantErr: ErrStreamTitleRequired,
		},
		{
			name: "Title too long",
			stream: &LiveStream{
				Title:   string(make([]byte, 256)),
				Status:  StreamStatusWaiting,
				Privacy: StreamPrivacyPublic,
			},
			wantErr: ErrStreamTitleTooLong,
		},
		{
			name: "Invalid status",
			stream: &LiveStream{
				Title:   "Test Stream",
				Status:  "invalid",
				Privacy: StreamPrivacyPublic,
			},
			wantErr: ErrInvalidStreamStatus,
		},
		{
			name: "Invalid privacy",
			stream: &LiveStream{
				Title:   "Test Stream",
				Status:  StreamStatusWaiting,
				Privacy: "invalid",
			},
			wantErr: ErrInvalidStreamPrivacy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.stream.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLiveStream_IsLive(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"Waiting stream", StreamStatusWaiting, false},
		{"Live stream", StreamStatusLive, true},
		{"Ended stream", StreamStatusEnded, false},
		{"Error stream", StreamStatusError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &LiveStream{Status: tt.status}
			assert.Equal(t, tt.want, stream.IsLive())
		})
	}
}

func TestLiveStream_CanStart(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"Waiting stream can start", StreamStatusWaiting, true},
		{"Live stream cannot start", StreamStatusLive, false},
		{"Ended stream cannot start", StreamStatusEnded, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &LiveStream{Status: tt.status}
			assert.Equal(t, tt.want, stream.CanStart())
		})
	}
}

func TestLiveStream_Start(t *testing.T) {
	t.Run("Start waiting stream", func(t *testing.T) {
		stream := &LiveStream{
			Status: StreamStatusWaiting,
		}

		err := stream.Start()
		require.NoError(t, err)
		assert.Equal(t, StreamStatusLive, stream.Status)
		assert.NotNil(t, stream.StartedAt)
		assert.WithinDuration(t, time.Now(), *stream.StartedAt, time.Second)
	})

	t.Run("Cannot start already live stream", func(t *testing.T) {
		now := time.Now()
		stream := &LiveStream{
			Status:    StreamStatusLive,
			StartedAt: &now,
		}

		err := stream.Start()
		assert.ErrorIs(t, err, ErrStreamAlreadyLive)
	})
}

func TestLiveStream_End(t *testing.T) {
	t.Run("End live stream", func(t *testing.T) {
		now := time.Now()
		stream := &LiveStream{
			Status:      StreamStatusLive,
			StartedAt:   &now,
			ViewerCount: 10,
		}

		err := stream.End()
		require.NoError(t, err)
		assert.Equal(t, StreamStatusEnded, stream.Status)
		assert.NotNil(t, stream.EndedAt)
		assert.Equal(t, 0, stream.ViewerCount)
		assert.WithinDuration(t, time.Now(), *stream.EndedAt, time.Second)
	})

	t.Run("Cannot end non-live stream", func(t *testing.T) {
		stream := &LiveStream{
			Status: StreamStatusWaiting,
		}

		err := stream.End()
		assert.ErrorIs(t, err, ErrStreamNotLive)
	})
}

func TestLiveStream_Duration(t *testing.T) {
	t.Run("Duration of ongoing stream", func(t *testing.T) {
		startTime := time.Now().Add(-5 * time.Minute)
		stream := &LiveStream{
			Status:    StreamStatusLive,
			StartedAt: &startTime,
		}

		duration := stream.Duration()
		assert.InDelta(t, 5*time.Minute, duration, float64(time.Second))
	})

	t.Run("Duration of ended stream", func(t *testing.T) {
		startTime := time.Now().Add(-10 * time.Minute)
		endTime := startTime.Add(5 * time.Minute)
		stream := &LiveStream{
			Status:    StreamStatusEnded,
			StartedAt: &startTime,
			EndedAt:   &endTime,
		}

		duration := stream.Duration()
		assert.Equal(t, 5*time.Minute, duration)
	})

	t.Run("Duration of not started stream", func(t *testing.T) {
		stream := &LiveStream{
			Status: StreamStatusWaiting,
		}

		duration := stream.Duration()
		assert.Equal(t, time.Duration(0), duration)
	})
}

func TestLiveStream_UpdateViewerCount(t *testing.T) {
	stream := &LiveStream{
		ViewerCount:     5,
		PeakViewerCount: 10,
	}

	t.Run("Update with lower count", func(t *testing.T) {
		stream.UpdateViewerCount(3)
		assert.Equal(t, 3, stream.ViewerCount)
		assert.Equal(t, 10, stream.PeakViewerCount) // Peak unchanged
	})

	t.Run("Update with higher count", func(t *testing.T) {
		stream.UpdateViewerCount(15)
		assert.Equal(t, 15, stream.ViewerCount)
		assert.Equal(t, 15, stream.PeakViewerCount) // Peak updated
	})
}

func TestGenerateStreamKey(t *testing.T) {
	key1, err := GenerateStreamKey()
	require.NoError(t, err)
	assert.NotEmpty(t, key1)

	key2, err := GenerateStreamKey()
	require.NoError(t, err)
	assert.NotEmpty(t, key2)

	// Keys should be unique
	assert.NotEqual(t, key1, key2)

	// Keys should be URL-safe base64 encoded (with optional padding)
	assert.Regexp(t, `^[A-Za-z0-9_-]+=*$`, key1)
}

func TestStreamKey_IsExpired(t *testing.T) {
	t.Run("Key with no expiration", func(t *testing.T) {
		key := &StreamKey{}
		assert.False(t, key.IsExpired())
	})

	t.Run("Key not yet expired", func(t *testing.T) {
		futureTime := time.Now().Add(time.Hour)
		key := &StreamKey{
			ExpiresAt: &futureTime,
		}
		assert.False(t, key.IsExpired())
	})

	t.Run("Expired key", func(t *testing.T) {
		pastTime := time.Now().Add(-time.Hour)
		key := &StreamKey{
			ExpiresAt: &pastTime,
		}
		assert.True(t, key.IsExpired())
	})
}

func TestStreamKey_CanUse(t *testing.T) {
	t.Run("Active non-expired key", func(t *testing.T) {
		key := &StreamKey{
			IsActive: true,
		}
		assert.NoError(t, key.CanUse())
	})

	t.Run("Inactive key", func(t *testing.T) {
		key := &StreamKey{
			IsActive: false,
		}
		assert.ErrorIs(t, key.CanUse(), ErrStreamKeyInactive)
	})

	t.Run("Expired key", func(t *testing.T) {
		pastTime := time.Now().Add(-time.Hour)
		key := &StreamKey{
			IsActive:  true,
			ExpiresAt: &pastTime,
		}
		assert.ErrorIs(t, key.CanUse(), ErrStreamKeyExpired)
	})
}

func TestViewerSession_IsActive(t *testing.T) {
	t.Run("Active session", func(t *testing.T) {
		session := &ViewerSession{
			LeftAt: nil,
		}
		assert.True(t, session.IsActive())
	})

	t.Run("Inactive session", func(t *testing.T) {
		leftTime := time.Now()
		session := &ViewerSession{
			LeftAt: &leftTime,
		}
		assert.False(t, session.IsActive())
	})
}

func TestViewerSession_WatchDuration(t *testing.T) {
	t.Run("Active session duration", func(t *testing.T) {
		joinedAt := time.Now().Add(-5 * time.Minute)
		session := &ViewerSession{
			JoinedAt: joinedAt,
			LeftAt:   nil,
		}

		duration := session.WatchDuration()
		assert.InDelta(t, 5*time.Minute, duration, float64(time.Second))
	})

	t.Run("Ended session duration", func(t *testing.T) {
		joinedAt := time.Now().Add(-10 * time.Minute)
		leftAt := joinedAt.Add(3 * time.Minute)
		session := &ViewerSession{
			JoinedAt: joinedAt,
			LeftAt:   &leftAt,
		}

		duration := session.WatchDuration()
		assert.Equal(t, 3*time.Minute, duration)
	})
}

func TestViewerSession_NeedsHeartbeat(t *testing.T) {
	t.Run("Recent heartbeat", func(t *testing.T) {
		session := &ViewerSession{
			LastHeartbeatAt: time.Now(),
		}
		assert.False(t, session.NeedsHeartbeat())
	})

	t.Run("Old heartbeat", func(t *testing.T) {
		session := &ViewerSession{
			LastHeartbeatAt: time.Now().Add(-30 * time.Second),
		}
		assert.True(t, session.NeedsHeartbeat())
	})
}

func TestViewerSession_UpdateHeartbeat(t *testing.T) {
	oldTime := time.Now().Add(-30 * time.Second)
	session := &ViewerSession{
		LastHeartbeatAt: oldTime,
	}

	session.UpdateHeartbeat()
	assert.WithinDuration(t, time.Now(), session.LastHeartbeatAt, time.Second)
}

func TestCreateLiveStreamParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  *CreateLiveStreamParams
		wantErr error
	}{
		{
			name: "Valid params",
			params: &CreateLiveStreamParams{
				ChannelID:   uuid.New(),
				UserID:      uuid.New(),
				Title:       "Test Stream",
				Description: "A test stream",
				Privacy:     StreamPrivacyPublic,
				SaveReplay:  true,
			},
			wantErr: nil,
		},
		{
			name: "Missing title",
			params: &CreateLiveStreamParams{
				ChannelID: uuid.New(),
				UserID:    uuid.New(),
				Title:     "",
				Privacy:   StreamPrivacyPublic,
			},
			wantErr: ErrStreamTitleRequired,
		},
		{
			name: "Title too long",
			params: &CreateLiveStreamParams{
				ChannelID: uuid.New(),
				UserID:    uuid.New(),
				Title:     string(make([]byte, 256)),
				Privacy:   StreamPrivacyPublic,
			},
			wantErr: ErrStreamTitleTooLong,
		},
		{
			name: "Invalid privacy",
			params: &CreateLiveStreamParams{
				ChannelID: uuid.New(),
				UserID:    uuid.New(),
				Title:     "Test Stream",
				Privacy:   "invalid",
			},
			wantErr: ErrInvalidStreamPrivacy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLiveStream_IsEnded(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"Ended stream", StreamStatusEnded, true},
		{"Waiting stream", StreamStatusWaiting, false},
		{"Live stream", StreamStatusLive, false},
		{"Error stream", StreamStatusError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &LiveStream{Status: tt.status}
			assert.Equal(t, tt.want, stream.IsEnded())
		})
	}
}

func TestGenerateStreamKey_Length(t *testing.T) {
	t.Run("Key is non-empty and at least 32 chars", func(t *testing.T) {
		key, err := GenerateStreamKey()
		require.NoError(t, err)
		assert.NotEmpty(t, key)
		// base64 encoding of 32 bytes produces 44 characters (with padding)
		assert.GreaterOrEqual(t, len(key), 32, "stream key should be at least 32 characters")
	})

	t.Run("Multiple keys are unique and consistently sized", func(t *testing.T) {
		keys := make(map[string]struct{})
		for i := 0; i < 10; i++ {
			key, err := GenerateStreamKey()
			require.NoError(t, err)
			assert.NotEmpty(t, key)
			assert.GreaterOrEqual(t, len(key), 32)
			keys[key] = struct{}{}
		}
		assert.Len(t, keys, 10, "all generated keys should be unique")
	})
}

func TestValidationHelpers(t *testing.T) {
	t.Run("isValidStreamStatus", func(t *testing.T) {
		assert.True(t, isValidStreamStatus(StreamStatusWaiting))
		assert.True(t, isValidStreamStatus(StreamStatusLive))
		assert.True(t, isValidStreamStatus(StreamStatusEnded))
		assert.True(t, isValidStreamStatus(StreamStatusError))
		assert.False(t, isValidStreamStatus("invalid"))
	})

	t.Run("isValidPrivacy", func(t *testing.T) {
		assert.True(t, isValidPrivacy(StreamPrivacyPublic))
		assert.True(t, isValidPrivacy(StreamPrivacyUnlisted))
		assert.True(t, isValidPrivacy(StreamPrivacyPrivate))
		assert.False(t, isValidPrivacy("invalid"))
	})
}
