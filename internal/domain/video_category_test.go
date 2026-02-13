package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestVideoCategoryJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	desc := "Videos about programming and software development"
	icon := "code"
	color := "#FF5733"
	createdBy := uuid.New()

	cat := VideoCategory{
		ID:           uuid.New(),
		Name:         "Technology",
		Slug:         "technology",
		Description:  &desc,
		Icon:         &icon,
		Color:        &color,
		DisplayOrder: 1,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
		CreatedBy:    &createdBy,
	}

	data, err := json.Marshal(cat)
	assert.NoError(t, err)

	var decoded VideoCategory
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, cat.ID, decoded.ID)
	assert.Equal(t, "Technology", decoded.Name)
	assert.Equal(t, "technology", decoded.Slug)
	assert.NotNil(t, decoded.Description)
	assert.Equal(t, desc, *decoded.Description)
	assert.NotNil(t, decoded.Icon)
	assert.Equal(t, icon, *decoded.Icon)
	assert.NotNil(t, decoded.Color)
	assert.Equal(t, color, *decoded.Color)
	assert.Equal(t, 1, decoded.DisplayOrder)
	assert.True(t, decoded.IsActive)
	assert.NotNil(t, decoded.CreatedBy)
	assert.Equal(t, createdBy, *decoded.CreatedBy)
}

func TestVideoCategoryMinimalJSON(t *testing.T) {
	cat := VideoCategory{
		ID:           uuid.New(),
		Name:         "Music",
		Slug:         "music",
		DisplayOrder: 0,
		IsActive:     false,
	}

	data, err := json.Marshal(cat)
	assert.NoError(t, err)

	var decoded VideoCategory
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "Music", decoded.Name)
	assert.Equal(t, "music", decoded.Slug)
	assert.Nil(t, decoded.Description)
	assert.Nil(t, decoded.Icon)
	assert.Nil(t, decoded.Color)
	assert.Equal(t, 0, decoded.DisplayOrder)
	assert.False(t, decoded.IsActive)
	assert.Nil(t, decoded.CreatedBy)
}

func TestCreateVideoCategoryRequestJSON(t *testing.T) {
	desc := "Educational content"
	icon := "book"
	color := "#00FF00"

	req := CreateVideoCategoryRequest{
		Name:         "Education",
		Slug:         "education",
		Description:  &desc,
		Icon:         &icon,
		Color:        &color,
		DisplayOrder: 5,
		IsActive:     true,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded CreateVideoCategoryRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "Education", decoded.Name)
	assert.Equal(t, "education", decoded.Slug)
	assert.NotNil(t, decoded.Description)
	assert.Equal(t, desc, *decoded.Description)
	assert.NotNil(t, decoded.Icon)
	assert.Equal(t, icon, *decoded.Icon)
	assert.NotNil(t, decoded.Color)
	assert.Equal(t, color, *decoded.Color)
	assert.Equal(t, 5, decoded.DisplayOrder)
	assert.True(t, decoded.IsActive)
}

func TestCreateVideoCategoryRequestMinimal(t *testing.T) {
	req := CreateVideoCategoryRequest{
		Name:     "Gaming",
		Slug:     "gaming",
		IsActive: false,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded CreateVideoCategoryRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "Gaming", decoded.Name)
	assert.Equal(t, "gaming", decoded.Slug)
	assert.Nil(t, decoded.Description)
	assert.Nil(t, decoded.Icon)
	assert.Nil(t, decoded.Color)
	assert.Equal(t, 0, decoded.DisplayOrder)
	assert.False(t, decoded.IsActive)
}

func TestUpdateVideoCategoryRequestJSON(t *testing.T) {
	name := "Updated Name"
	slug := "updated-name"
	desc := "Updated description"
	icon := "star"
	color := "#0000FF"
	displayOrder := 10
	isActive := true

	req := UpdateVideoCategoryRequest{
		Name:         &name,
		Slug:         &slug,
		Description:  &desc,
		Icon:         &icon,
		Color:        &color,
		DisplayOrder: &displayOrder,
		IsActive:     &isActive,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded UpdateVideoCategoryRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.NotNil(t, decoded.Name)
	assert.Equal(t, name, *decoded.Name)
	assert.NotNil(t, decoded.Slug)
	assert.Equal(t, slug, *decoded.Slug)
	assert.NotNil(t, decoded.Description)
	assert.Equal(t, desc, *decoded.Description)
	assert.NotNil(t, decoded.Icon)
	assert.Equal(t, icon, *decoded.Icon)
	assert.NotNil(t, decoded.Color)
	assert.Equal(t, color, *decoded.Color)
	assert.NotNil(t, decoded.DisplayOrder)
	assert.Equal(t, displayOrder, *decoded.DisplayOrder)
	assert.NotNil(t, decoded.IsActive)
	assert.True(t, *decoded.IsActive)
}

func TestUpdateVideoCategoryRequestPartialJSON(t *testing.T) {
	name := "Only Name Changed"

	req := UpdateVideoCategoryRequest{
		Name: &name,
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded UpdateVideoCategoryRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.NotNil(t, decoded.Name)
	assert.Equal(t, name, *decoded.Name)
	assert.Nil(t, decoded.Slug)
	assert.Nil(t, decoded.Description)
	assert.Nil(t, decoded.Icon)
	assert.Nil(t, decoded.Color)
	assert.Nil(t, decoded.DisplayOrder)
	assert.Nil(t, decoded.IsActive)
}

func TestVideoCategoryListOptionsDefaults(t *testing.T) {
	opts := VideoCategoryListOptions{}

	assert.False(t, opts.ActiveOnly)
	assert.Empty(t, opts.OrderBy)
	assert.Empty(t, opts.OrderDir)
	assert.Equal(t, 0, opts.Limit)
	assert.Equal(t, 0, opts.Offset)
}

func TestVideoCategoryListOptionsJSON(t *testing.T) {
	opts := VideoCategoryListOptions{
		ActiveOnly: true,
		OrderBy:    "display_order",
		OrderDir:   "asc",
		Limit:      20,
		Offset:     40,
	}

	data, err := json.Marshal(opts)
	assert.NoError(t, err)

	var decoded VideoCategoryListOptions
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.True(t, decoded.ActiveOnly)
	assert.Equal(t, "display_order", decoded.OrderBy)
	assert.Equal(t, "asc", decoded.OrderDir)
	assert.Equal(t, 20, decoded.Limit)
	assert.Equal(t, 40, decoded.Offset)
}
