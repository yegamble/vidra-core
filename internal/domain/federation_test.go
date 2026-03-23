package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFederationJobStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		status   FederationJobStatus
		expected string
	}{
		{"Pending", FedJobPending, "pending"},
		{"Processing", FedJobProcessing, "processing"},
		{"Completed", FedJobCompleted, "completed"},
		{"Failed", FedJobFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, FederationJobStatus(tt.expected), tt.status)
			assert.Equal(t, tt.expected, string(tt.status))
		})
	}
}

func TestFederationJobJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	lastErr := "connection timeout"
	job := FederationJob{
		ID:            "job-123",
		JobType:       "deliver_activity",
		Payload:       json.RawMessage(`{"actor":"https://example.com/users/alice","type":"Create"}`),
		Status:        FedJobPending,
		Attempts:      2,
		MaxAttempts:   5,
		NextAttemptAt: now.Add(5 * time.Minute),
		LastError:     &lastErr,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	data, err := json.Marshal(job)
	assert.NoError(t, err)

	var decoded FederationJob
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, job.ID, decoded.ID)
	assert.Equal(t, job.JobType, decoded.JobType)
	assert.Equal(t, FedJobPending, decoded.Status)
	assert.Equal(t, job.Attempts, decoded.Attempts)
	assert.Equal(t, job.MaxAttempts, decoded.MaxAttempts)
	assert.NotNil(t, decoded.LastError)
	assert.Equal(t, lastErr, *decoded.LastError)

	// Verify payload round-trip
	var payloadMap map[string]interface{}
	err = json.Unmarshal(decoded.Payload, &payloadMap)
	assert.NoError(t, err)
	assert.Equal(t, "https://example.com/users/alice", payloadMap["actor"])
	assert.Equal(t, "Create", payloadMap["type"])
}

func TestFederationJobWithoutLastError(t *testing.T) {
	job := FederationJob{
		ID:          "job-456",
		JobType:     "ingest_post",
		Payload:     json.RawMessage(`{}`),
		Status:      FedJobCompleted,
		Attempts:    1,
		MaxAttempts: 3,
		LastError:   nil,
	}

	data, err := json.Marshal(job)
	assert.NoError(t, err)

	var decoded FederationJob
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Nil(t, decoded.LastError)
	assert.Equal(t, FedJobCompleted, decoded.Status)
}

func TestFederatedPostJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	handle := "alice.bsky.social"
	text := "Check out this video!"
	contentHash := "sha256:abc123"

	post := FederatedPost{
		ID:            "post-789",
		ActorDID:      "did:plc:abc123",
		ActorHandle:   &handle,
		URI:           "at://did:plc:abc123/app.bsky.feed.post/abc",
		Text:          &text,
		CreatedAt:     &now,
		InsertedAt:    now,
		UpdatedAt:     now,
		ContentHash:   &contentHash,
		Labels:        json.RawMessage(`["label1"]`),
		Raw:           json.RawMessage(`{"$type":"app.bsky.feed.post"}`),
		VersionNumber: 1,
		IsCanonical:   true,
	}

	data, err := json.Marshal(post)
	assert.NoError(t, err)

	var decoded FederatedPost
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, post.ID, decoded.ID)
	assert.Equal(t, post.ActorDID, decoded.ActorDID)
	assert.NotNil(t, decoded.ActorHandle)
	assert.Equal(t, handle, *decoded.ActorHandle)
	assert.Equal(t, post.URI, decoded.URI)
	assert.NotNil(t, decoded.Text)
	assert.Equal(t, text, *decoded.Text)
	assert.NotNil(t, decoded.ContentHash)
	assert.Equal(t, contentHash, *decoded.ContentHash)
	assert.Equal(t, 1, decoded.VersionNumber)
	assert.True(t, decoded.IsCanonical)
}

func TestFederatedTimelineJSONRoundTrip(t *testing.T) {
	timeline := FederatedTimeline{
		Total:    100,
		Page:     1,
		PageSize: 20,
		Data: []FederatedPost{
			{
				ID:       "post-1",
				ActorDID: "did:plc:abc",
				URI:      "at://did:plc:abc/post/1",
			},
			{
				ID:       "post-2",
				ActorDID: "did:plc:def",
				URI:      "at://did:plc:def/post/2",
			},
		},
	}

	data, err := json.Marshal(timeline)
	assert.NoError(t, err)

	var decoded FederatedTimeline
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, timeline.Total, decoded.Total)
	assert.Equal(t, timeline.Page, decoded.Page)
	assert.Equal(t, timeline.PageSize, decoded.PageSize)
	assert.Len(t, decoded.Data, 2)
	assert.Equal(t, "post-1", decoded.Data[0].ID)
	assert.Equal(t, "post-2", decoded.Data[1].ID)
}
