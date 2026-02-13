package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestCommentStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		status   CommentStatus
		expected string
	}{
		{"Active", CommentStatusActive, "active"},
		{"Deleted", CommentStatusDeleted, "deleted"},
		{"Flagged", CommentStatusFlagged, "flagged"},
		{"Hidden", CommentStatusHidden, "hidden"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, CommentStatus(tt.expected), tt.status)
			assert.Equal(t, tt.expected, string(tt.status))
		})
	}
}

func TestCommentFlagReasonConstants(t *testing.T) {
	tests := []struct {
		name     string
		reason   CommentFlagReason
		expected string
	}{
		{"Spam", FlagReasonSpam, "spam"},
		{"Harassment", FlagReasonHarassment, "harassment"},
		{"HateSpeech", FlagReasonHateSpeech, "hate_speech"},
		{"Inappropriate", FlagReasonInappropriate, "inappropriate"},
		{"Misinformation", FlagReasonMisinformation, "misinformation"},
		{"Other", FlagReasonOther, "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, CommentFlagReason(tt.expected), tt.reason)
			assert.Equal(t, tt.expected, string(tt.reason))
		})
	}
}

func TestCommentJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	videoID := uuid.New()
	userID := uuid.New()
	commentID := uuid.New()

	comment := Comment{
		ID:        commentID,
		VideoID:   videoID,
		UserID:    userID,
		ParentID:  nil,
		Body:      "This is a test comment",
		Status:    CommentStatusActive,
		FlagCount: 0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	data, err := json.Marshal(comment)
	assert.NoError(t, err)

	var decoded Comment
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, comment.ID, decoded.ID)
	assert.Equal(t, comment.VideoID, decoded.VideoID)
	assert.Equal(t, comment.UserID, decoded.UserID)
	assert.Nil(t, decoded.ParentID)
	assert.Equal(t, comment.Body, decoded.Body)
	assert.Equal(t, comment.Status, decoded.Status)
	assert.Equal(t, comment.FlagCount, decoded.FlagCount)
}

func TestCommentWithParentIDJSON(t *testing.T) {
	parentID := uuid.New()
	comment := Comment{
		ID:       uuid.New(),
		VideoID:  uuid.New(),
		UserID:   uuid.New(),
		ParentID: &parentID,
		Body:     "Reply to parent comment",
		Status:   CommentStatusActive,
	}

	data, err := json.Marshal(comment)
	assert.NoError(t, err)

	var decoded Comment
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.NotNil(t, decoded.ParentID)
	assert.Equal(t, parentID, *decoded.ParentID)
}

func TestCreateCommentRequestJSON(t *testing.T) {
	videoID := uuid.New()
	req := CreateCommentRequest{
		VideoID: videoID,
		Body:    "New comment body",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded CreateCommentRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, req.VideoID, decoded.VideoID)
	assert.Equal(t, req.Body, decoded.Body)
	assert.Nil(t, decoded.ParentID)
}

func TestFlagCommentRequestJSON(t *testing.T) {
	details := "Contains offensive language"
	req := FlagCommentRequest{
		Reason:  FlagReasonHarassment,
		Details: &details,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded FlagCommentRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, FlagReasonHarassment, decoded.Reason)
	assert.NotNil(t, decoded.Details)
	assert.Equal(t, details, *decoded.Details)
}

func TestCommentFlagJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	flag := CommentFlag{
		ID:        uuid.New(),
		CommentID: uuid.New(),
		UserID:    uuid.New(),
		Reason:    FlagReasonSpam,
		CreatedAt: now,
	}

	data, err := json.Marshal(flag)
	assert.NoError(t, err)

	var decoded CommentFlag
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, flag.ID, decoded.ID)
	assert.Equal(t, flag.CommentID, decoded.CommentID)
	assert.Equal(t, flag.UserID, decoded.UserID)
	assert.Equal(t, FlagReasonSpam, decoded.Reason)
}

func TestCommentWithUserJSON(t *testing.T) {
	avatar := "https://example.com/avatar.png"
	cwu := CommentWithUser{
		Comment: Comment{
			ID:     uuid.New(),
			Body:   "A comment with user info",
			Status: CommentStatusActive,
		},
		Username: "testuser",
		Avatar:   &avatar,
	}

	data, err := json.Marshal(cwu)
	assert.NoError(t, err)

	var decoded CommentWithUser
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, cwu.Username, decoded.Username)
	assert.NotNil(t, decoded.Avatar)
	assert.Equal(t, avatar, *decoded.Avatar)
	assert.Equal(t, cwu.Body, decoded.Body)
}
