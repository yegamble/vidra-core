package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestPlaylistJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	desc := "My favorite videos"
	thumbnailURL := "https://example.com/thumb.jpg"
	userID := uuid.New()
	playlistID := uuid.New()

	playlist := Playlist{
		ID:           playlistID,
		UserID:       userID,
		Name:         "Favorites",
		Description:  &desc,
		Privacy:      PrivacyPublic,
		ThumbnailURL: &thumbnailURL,
		IsWatchLater: false,
		ItemCount:    10,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	data, err := json.Marshal(playlist)
	assert.NoError(t, err)

	var decoded Playlist
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, playlistID, decoded.ID)
	assert.Equal(t, userID, decoded.UserID)
	assert.Equal(t, "Favorites", decoded.Name)
	assert.NotNil(t, decoded.Description)
	assert.Equal(t, desc, *decoded.Description)
	assert.Equal(t, PrivacyPublic, decoded.Privacy)
	assert.NotNil(t, decoded.ThumbnailURL)
	assert.Equal(t, thumbnailURL, *decoded.ThumbnailURL)
	assert.False(t, decoded.IsWatchLater)
	assert.Equal(t, 10, decoded.ItemCount)
}

func TestPlaylistMinimalJSON(t *testing.T) {
	playlist := Playlist{
		ID:      uuid.New(),
		UserID:  uuid.New(),
		Name:    "Watch Later",
		Privacy: PrivacyPrivate,
	}

	data, err := json.Marshal(playlist)
	assert.NoError(t, err)

	var decoded Playlist
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "Watch Later", decoded.Name)
	assert.Nil(t, decoded.Description)
	assert.Nil(t, decoded.ThumbnailURL)
	assert.Equal(t, PrivacyPrivate, decoded.Privacy)
}

func TestCreatePlaylistRequestJSON(t *testing.T) {
	desc := "A collection of tutorials"
	req := CreatePlaylistRequest{
		Name:        "Tutorials",
		Description: &desc,
		Privacy:     PrivacyUnlisted,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded CreatePlaylistRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "Tutorials", decoded.Name)
	assert.NotNil(t, decoded.Description)
	assert.Equal(t, desc, *decoded.Description)
	assert.Equal(t, PrivacyUnlisted, decoded.Privacy)
	assert.Nil(t, decoded.ThumbnailURL)
}

func TestCreatePlaylistRequestMinimal(t *testing.T) {
	req := CreatePlaylistRequest{
		Name:    "My Playlist",
		Privacy: PrivacyPublic,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded CreatePlaylistRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "My Playlist", decoded.Name)
	assert.Equal(t, PrivacyPublic, decoded.Privacy)
	assert.Nil(t, decoded.Description)
	assert.Nil(t, decoded.ThumbnailURL)
}

func TestPlaylistItemJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	item := PlaylistItem{
		ID:         uuid.New(),
		PlaylistID: uuid.New(),
		VideoID:    uuid.New(),
		Position:   3,
		AddedAt:    now,
	}

	data, err := json.Marshal(item)
	assert.NoError(t, err)

	var decoded PlaylistItem
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, item.ID, decoded.ID)
	assert.Equal(t, item.PlaylistID, decoded.PlaylistID)
	assert.Equal(t, item.VideoID, decoded.VideoID)
	assert.Equal(t, 3, decoded.Position)
}

func TestAddToPlaylistRequestJSON(t *testing.T) {
	videoID := uuid.New()
	position := 5
	req := AddToPlaylistRequest{
		VideoID:  videoID,
		Position: &position,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded AddToPlaylistRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, videoID, decoded.VideoID)
	assert.NotNil(t, decoded.Position)
	assert.Equal(t, 5, *decoded.Position)
}

func TestAddToPlaylistRequestWithoutPosition(t *testing.T) {
	req := AddToPlaylistRequest{
		VideoID: uuid.New(),
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded AddToPlaylistRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Nil(t, decoded.Position)
}

func TestPlaylistWithItemsJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	pwi := PlaylistWithItems{
		Playlist: Playlist{
			ID:      uuid.New(),
			UserID:  uuid.New(),
			Name:    "My Playlist",
			Privacy: PrivacyPublic,
		},
		Items: []PlaylistItem{
			{ID: uuid.New(), VideoID: uuid.New(), Position: 0, AddedAt: now},
			{ID: uuid.New(), VideoID: uuid.New(), Position: 1, AddedAt: now},
		},
	}

	data, err := json.Marshal(pwi)
	assert.NoError(t, err)

	var decoded PlaylistWithItems
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "My Playlist", decoded.Name)
	assert.Len(t, decoded.Items, 2)
	assert.Equal(t, 0, decoded.Items[0].Position)
	assert.Equal(t, 1, decoded.Items[1].Position)
}

func TestPlaylistListResponseJSON(t *testing.T) {
	resp := PlaylistListResponse{
		Playlists: []*Playlist{
			{ID: uuid.New(), Name: "Playlist 1", Privacy: PrivacyPublic},
			{ID: uuid.New(), Name: "Playlist 2", Privacy: PrivacyPrivate},
		},
		Total:  50,
		Limit:  20,
		Offset: 0,
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded PlaylistListResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Len(t, decoded.Playlists, 2)
	assert.Equal(t, 50, decoded.Total)
	assert.Equal(t, 20, decoded.Limit)
	assert.Equal(t, 0, decoded.Offset)
}

func TestUpdatePlaylistRequestJSON(t *testing.T) {
	name := "Updated Name"
	desc := "Updated description"
	privacy := PrivacyUnlisted

	req := UpdatePlaylistRequest{
		Name:        &name,
		Description: &desc,
		Privacy:     &privacy,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded UpdatePlaylistRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.NotNil(t, decoded.Name)
	assert.Equal(t, name, *decoded.Name)
	assert.NotNil(t, decoded.Description)
	assert.Equal(t, desc, *decoded.Description)
	assert.NotNil(t, decoded.Privacy)
	assert.Equal(t, PrivacyUnlisted, *decoded.Privacy)
}
