package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSocialInteractionTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		itype    SocialInteractionType
		expected string
	}{
		{"Follow", InteractionFollow, "follow"},
		{"Like", InteractionLike, "like"},
		{"Comment", InteractionComment, "comment"},
		{"Repost", InteractionRepost, "repost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, SocialInteractionType(tt.expected), tt.itype)
			assert.Equal(t, tt.expected, string(tt.itype))
		})
	}
}

func TestATProtoActorJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	displayName := "Alice Johnson"
	bio := "Video creator and streamer"
	avatarURL := "https://cdn.example.com/avatar/alice.jpg"
	bannerURL := "https://cdn.example.com/banner/alice.jpg"
	localUserID := "local-user-123"

	actor := ATProtoActor{
		DID:         "did:plc:abc123def456",
		Handle:      "alice.bsky.social",
		DisplayName: &displayName,
		Bio:         &bio,
		AvatarURL:   &avatarURL,
		BannerURL:   &bannerURL,
		CreatedAt:   now,
		UpdatedAt:   now,
		IndexedAt:   now,
		Labels:      json.RawMessage(`["verified"]`),
		LocalUserID: &localUserID,
	}

	data, err := json.Marshal(actor)
	assert.NoError(t, err)

	var decoded ATProtoActor
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, actor.DID, decoded.DID)
	assert.Equal(t, actor.Handle, decoded.Handle)
	assert.NotNil(t, decoded.DisplayName)
	assert.Equal(t, displayName, *decoded.DisplayName)
	assert.NotNil(t, decoded.Bio)
	assert.Equal(t, bio, *decoded.Bio)
	assert.NotNil(t, decoded.AvatarURL)
	assert.Equal(t, avatarURL, *decoded.AvatarURL)
	assert.NotNil(t, decoded.BannerURL)
	assert.Equal(t, bannerURL, *decoded.BannerURL)
	assert.NotNil(t, decoded.LocalUserID)
	assert.Equal(t, localUserID, *decoded.LocalUserID)

	var labels []string
	err = json.Unmarshal(decoded.Labels, &labels)
	assert.NoError(t, err)
	assert.Contains(t, labels, "verified")
}

func TestATProtoActorMinimalJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	actor := ATProtoActor{
		DID:       "did:plc:minimal",
		Handle:    "bob.bsky.social",
		CreatedAt: now,
		UpdatedAt: now,
		IndexedAt: now,
	}

	data, err := json.Marshal(actor)
	assert.NoError(t, err)

	var decoded ATProtoActor
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "did:plc:minimal", decoded.DID)
	assert.Equal(t, "bob.bsky.social", decoded.Handle)
	assert.Nil(t, decoded.DisplayName)
	assert.Nil(t, decoded.Bio)
	assert.Nil(t, decoded.AvatarURL)
	assert.Nil(t, decoded.BannerURL)
	assert.Nil(t, decoded.LocalUserID)
}

func TestFollowJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	cid := "bafyrei-test-cid"

	follow := Follow{
		ID:           "follow-123",
		FollowerDID:  "did:plc:follower",
		FollowingDID: "did:plc:following",
		URI:          "at://did:plc:follower/app.bsky.graph.follow/abc",
		CID:          &cid,
		CreatedAt:    now,
		Raw:          json.RawMessage(`{"$type":"app.bsky.graph.follow"}`),
	}

	data, err := json.Marshal(follow)
	assert.NoError(t, err)

	var decoded Follow
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, follow.ID, decoded.ID)
	assert.Equal(t, follow.FollowerDID, decoded.FollowerDID)
	assert.Equal(t, follow.FollowingDID, decoded.FollowingDID)
	assert.Equal(t, follow.URI, decoded.URI)
	assert.NotNil(t, decoded.CID)
	assert.Equal(t, cid, *decoded.CID)
	assert.Nil(t, decoded.RevokedAt)
}

func TestLikeJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	videoID := "video-456"

	like := Like{
		ID:         "like-789",
		ActorDID:   "did:plc:liker",
		SubjectURI: "at://did:plc:creator/app.bsky.feed.post/abc",
		URI:        "at://did:plc:liker/app.bsky.feed.like/xyz",
		CreatedAt:  now,
		VideoID:    &videoID,
	}

	data, err := json.Marshal(like)
	assert.NoError(t, err)

	var decoded Like
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, like.ID, decoded.ID)
	assert.Equal(t, like.ActorDID, decoded.ActorDID)
	assert.Equal(t, like.SubjectURI, decoded.SubjectURI)
	assert.Equal(t, like.URI, decoded.URI)
	assert.NotNil(t, decoded.VideoID)
	assert.Equal(t, videoID, *decoded.VideoID)
}

func TestSocialStatsJSON(t *testing.T) {
	stats := SocialStats{
		Follows:   150,
		Followers: 500,
		Likes:     2000,
		Comments:  300,
		Reposts:   75,
	}

	data, err := json.Marshal(stats)
	assert.NoError(t, err)

	var decoded SocialStats
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, int64(150), decoded.Follows)
	assert.Equal(t, int64(500), decoded.Followers)
	assert.Equal(t, int64(2000), decoded.Likes)
	assert.Equal(t, int64(300), decoded.Comments)
	assert.Equal(t, int64(75), decoded.Reposts)
}

func TestSocialCommentJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	handle := "commenter.bsky.social"
	videoID := "video-123"

	comment := SocialComment{
		ID:          "sc-456",
		ActorDID:    "did:plc:commenter",
		ActorHandle: &handle,
		URI:         "at://did:plc:commenter/app.bsky.feed.post/abc",
		Text:        "Great video!",
		RootURI:     "at://did:plc:creator/app.bsky.feed.post/root",
		CreatedAt:   now,
		IndexedAt:   now,
		VideoID:     &videoID,
		Blocked:     false,
	}

	data, err := json.Marshal(comment)
	assert.NoError(t, err)

	var decoded SocialComment
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, comment.ID, decoded.ID)
	assert.Equal(t, comment.ActorDID, decoded.ActorDID)
	assert.NotNil(t, decoded.ActorHandle)
	assert.Equal(t, handle, *decoded.ActorHandle)
	assert.Equal(t, "Great video!", decoded.Text)
	assert.Equal(t, comment.RootURI, decoded.RootURI)
	assert.NotNil(t, decoded.VideoID)
	assert.Equal(t, videoID, *decoded.VideoID)
	assert.False(t, decoded.Blocked)
}

func TestModerationLabelJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	reason := "Automated detection"
	uri := "at://did:plc:bad/post/1"
	expiresAt := now.Add(7 * 24 * time.Hour)

	label := ModerationLabel{
		ID:        "label-123",
		ActorDID:  "did:plc:bad",
		LabelType: "spam",
		Reason:    &reason,
		AppliedBy: "did:plc:labeler",
		URI:       &uri,
		CreatedAt: now,
		ExpiresAt: &expiresAt,
	}

	data, err := json.Marshal(label)
	assert.NoError(t, err)

	var decoded ModerationLabel
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, label.ID, decoded.ID)
	assert.Equal(t, "spam", decoded.LabelType)
	assert.NotNil(t, decoded.Reason)
	assert.Equal(t, reason, *decoded.Reason)
	assert.Equal(t, "did:plc:labeler", decoded.AppliedBy)
	assert.NotNil(t, decoded.URI)
	assert.NotNil(t, decoded.ExpiresAt)
}
